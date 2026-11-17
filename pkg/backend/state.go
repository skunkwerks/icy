package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skunkwerks/icy/git"
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/db"
	"github.com/skunkwerks/icy/pkg/db/models"
	"github.com/skunkwerks/icy/pkg/hooks"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/sshutils"
	"github.com/skunkwerks/icy/pkg/statefile"
	"github.com/skunkwerks/icy/pkg/utils"
	"golang.org/x/crypto/ssh"
)

// stateMu stops two reloads from applying the state at once.
var stateMu sync.Mutex

// checkStateWritable refuses a change to users, repositories or access
// when they come from config.yaml. The check runs before anything is
// looked up, so the answer is the same whether or not the target exists.
func (d *Backend) checkStateWritable() error {
	if d.cfg.StateFromConfig() {
		return proto.ErrManagedByConfig
	}
	return nil
}

// ApplyConfigState reads the users, repos and access settings from the
// server's config.yaml, checks them, and makes the database match. Nothing
// changes if they are invalid. Other config.yaml keys are not touched.
func (d *Backend) ApplyConfigState(ctx context.Context) ([]string, error) {
	opts, err := statefile.OptionsFor(d.cfg)
	if err != nil {
		return nil, err
	}
	st, err := statefile.Load(d.cfg.ConfigPath(), opts)
	if err != nil {
		return nil, err
	}
	return d.ApplyState(ctx, st)
}

// ApplyState makes the database match st in a single transaction, then
// brings the repositories on disk in line. It returns a line for each
// change it made.
//
// Repositories that are not in st are removed from the database and their
// directories are moved under repos-removed in the data path, not deleted.
func (d *Backend) ApplyState(ctx context.Context, st *statefile.State) ([]string, error) {
	stateMu.Lock()
	defer stateMu.Unlock()

	var changes []string
	var removed []models.Repo
	note := func(format string, args ...any) {
		changes = append(changes, fmt.Sprintf(format, args...))
	}

	if err := d.db.TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		removed, err = d.applyState(ctx, tx, st, note)
		return err
	}); err != nil {
		return nil, fmt.Errorf("apply state: %w", db.WrapError(err))
	}

	if len(removed) > 0 {
		dest := filepath.Join(d.cfg.DataPath, "repos-removed", time.Now().UTC().Format("20060102T150405.000Z"))
		for _, r := range removed {
			d.archiveRepo(dest, r, note)
		}
	}
	for _, r := range st.Repos {
		d.ensureRepoOnDisk(ctx, r, note)
	}

	return changes, nil
}

func (d *Backend) applyState(ctx context.Context, tx *db.Tx, st *statefile.State, note func(string, ...any)) ([]models.Repo, error) {
	// Settings.
	anon, err := d.store.GetAnonAccess(ctx, tx)
	if err != nil {
		return nil, err
	}
	if anon != st.AnonAccess {
		if err := d.store.SetAnonAccess(ctx, tx, st.AnonAccess); err != nil {
			return nil, err
		}
		note("set anon-access to %s", st.AnonAccess)
	}
	allow, err := d.store.GetAllowKeylessAccess(ctx, tx)
	if err != nil {
		return nil, err
	}
	if allow != st.AllowKeyless {
		if err := d.store.SetAllowKeylessAccess(ctx, tx, st.AllowKeyless); err != nil {
			return nil, err
		}
		note("set allow-keyless to %t", st.AllowKeyless)
	}

	// Users. Keys come off first so that a key can move from one user to
	// another, since a key may belong to only one user.
	dbUsers, err := d.store.GetAllUsers(ctx, tx)
	if err != nil {
		return nil, err
	}
	wantUsers := make(map[string]statefile.UserState, len(st.Users))
	for _, u := range st.Users {
		wantUsers[u.Name] = u
	}
	haveUsers := make(map[string]models.User, len(dbUsers))
	for _, u := range dbUsers {
		haveUsers[u.Username] = u
		want, keep := wantUsers[u.Username]
		pks, err := d.store.ListPublicKeysByUserID(ctx, tx, u.ID)
		if err != nil {
			return nil, err
		}
		for _, pk := range pks {
			if keep && hasKey(want.PublicKeys, pk) {
				continue
			}
			if err := d.store.RemovePublicKeyByUsername(ctx, tx, u.Username, pk); err != nil {
				return nil, err
			}
			if keep {
				note("remove key %s from user %s", keyLabel(pk), u.Username)
			}
		}
	}

	for _, u := range st.Users {
		have, ok := haveUsers[u.Name]
		if !ok {
			if err := d.store.CreateUser(ctx, tx, u.Name, u.Admin, u.PublicKeys); err != nil {
				return nil, fmt.Errorf("create user %s: %w", u.Name, err)
			}
			note("create user %s", u.Name)
			continue
		}
		if have.Admin != u.Admin {
			if err := d.store.SetAdminByUsername(ctx, tx, u.Name, u.Admin); err != nil {
				return nil, err
			}
			note("set admin to %t for user %s", u.Admin, u.Name)
		}
		pks, err := d.store.ListPublicKeysByUserID(ctx, tx, have.ID)
		if err != nil {
			return nil, err
		}
		for _, pk := range u.PublicKeys {
			if hasKey(pks, pk) {
				continue
			}
			if err := d.store.AddPublicKeyByUsername(ctx, tx, u.Name, pk); err != nil {
				return nil, fmt.Errorf("add key to user %s: %w", u.Name, err)
			}
			note("add key %s to user %s", keyLabel(pk), u.Name)
		}
	}

	// Look the users up again so new ones have IDs.
	dbUsers, err = d.store.GetAllUsers(ctx, tx)
	if err != nil {
		return nil, err
	}
	userIDs := make(map[string]int64, len(dbUsers))
	userNames := make(map[int64]string, len(dbUsers))
	for _, u := range dbUsers {
		userIDs[u.Username] = u.ID
		userNames[u.ID] = u.Username
	}

	// Repositories.
	dbRepos, err := d.store.GetAllRepos(ctx, tx)
	if err != nil {
		return nil, err
	}
	haveRepos := make(map[string]models.Repo, len(dbRepos))
	for _, r := range dbRepos {
		haveRepos[r.Name] = r
	}

	for _, r := range st.Repos {
		ownerID := userIDs[r.Owner]
		have, ok := haveRepos[r.Name]
		if !ok {
			// A name chosen now must avoid the segments that the web UI
			// reserves. Repositories that already exist keep their names.
			if err := utils.ValidateNewRepo(r.Name); err != nil {
				return nil, fmt.Errorf("create repository %s: %w", r.Name, err)
			}
			if err := d.store.CreateRepo(ctx, tx, r.Name, ownerID, r.ProjectName, r.Description, r.Private, r.Hidden, false); err != nil {
				return nil, fmt.Errorf("create repository %s: %w", r.Name, err)
			}
			note("create repository %s", r.Name)
		} else if err := d.updateRepo(ctx, tx, have, r, ownerID, note); err != nil {
			return nil, err
		}

		if err := d.applyCollabs(ctx, tx, r, userNames, note); err != nil {
			return nil, err
		}
	}

	wantRepos := make(map[string]bool, len(st.Repos))
	for _, r := range st.Repos {
		wantRepos[r.Name] = true
	}
	var removed []models.Repo
	for _, r := range dbRepos {
		if wantRepos[r.Name] {
			continue
		}
		if err := d.store.DeleteRepoByName(ctx, tx, r.Name); err != nil {
			return nil, err
		}
		note("remove repository %s", r.Name)
		removed = append(removed, r)
	}

	// Every remaining repository now belongs to a user in the file, so
	// deleting the others cannot cascade to a repository.
	for _, u := range dbUsers {
		if _, ok := wantUsers[u.Username]; ok {
			continue
		}
		if err := d.store.DeleteUserByUsername(ctx, tx, u.Username); err != nil {
			return nil, err
		}
		note("remove user %s", u.Username)
	}

	return removed, nil
}

func (d *Backend) updateRepo(ctx context.Context, tx *db.Tx, have models.Repo, r statefile.RepoState, ownerID int64, note func(string, ...any)) error {
	if have.ProjectName != r.ProjectName {
		if err := d.store.SetRepoProjectNameByName(ctx, tx, r.Name, r.ProjectName); err != nil {
			return err
		}
		note("set project-name of repository %s", r.Name)
	}
	if have.Description != r.Description {
		if err := d.store.SetRepoDescriptionByName(ctx, tx, r.Name, r.Description); err != nil {
			return err
		}
		note("set description of repository %s", r.Name)
	}
	if have.Private != r.Private {
		if err := d.store.SetRepoIsPrivateByName(ctx, tx, r.Name, r.Private); err != nil {
			return err
		}
		note("set private to %t for repository %s", r.Private, r.Name)
	}
	if have.Hidden != r.Hidden {
		if err := d.store.SetRepoIsHiddenByName(ctx, tx, r.Name, r.Hidden); err != nil {
			return err
		}
		note("set hidden to %t for repository %s", r.Hidden, r.Name)
	}
	if !have.UserID.Valid || have.UserID.Int64 != ownerID {
		if err := d.store.SetRepoUserIDByName(ctx, tx, r.Name, ownerID); err != nil {
			return err
		}
		note("set owner of repository %s to %s", r.Name, r.Owner)
	}
	return nil
}

func (d *Backend) applyCollabs(ctx context.Context, tx *db.Tx, r statefile.RepoState, userNames map[int64]string, note func(string, ...any)) error {
	collabs, err := d.store.ListCollabsByRepo(ctx, tx, r.Name)
	if err != nil {
		return err
	}

	have := make(map[string]access.AccessLevel, len(collabs))
	for _, c := range collabs {
		name, ok := userNames[c.UserID]
		if !ok {
			continue
		}
		want, keep := r.Collaborators[name]
		if keep && want == c.AccessLevel {
			have[name] = c.AccessLevel
			continue
		}
		if err := d.store.RemoveCollabByUsernameAndRepo(ctx, tx, name, r.Name); err != nil {
			return err
		}
		if !keep {
			note("remove collaborator %s from repository %s", name, r.Name)
		}
	}

	names := make([]string, 0, len(r.Collaborators))
	for name := range r.Collaborators {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := have[name]; ok {
			continue
		}
		level := r.Collaborators[name]
		if err := d.store.AddCollabByUsernameAndRepo(ctx, tx, name, r.Name, level); err != nil {
			return fmt.Errorf("add collaborator %s to repository %s: %w", name, r.Name, err)
		}
		note("set collaborator %s on repository %s to %s", name, r.Name, level)
	}

	return nil
}

// archiveRepo moves a removed repository, and its LFS objects, out of the
// way into dest. Errors are logged: the database has already changed and
// the next reload does not retry.
func (d *Backend) archiveRepo(dest string, r models.Repo, note func(string, ...any)) {
	move := func(src, dst string) {
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			return
		}
		if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
			d.logger.Error("failed to archive removed repository", "repo", r.Name, "err", err)
			return
		}
		if err := os.Rename(src, dst); err != nil {
			d.logger.Error("failed to archive removed repository", "repo", r.Name, "err", err)
			return
		}
		note("move %s to %s", src, dst)
	}

	rel := strings.ReplaceAll(r.Name, "/", string(os.PathSeparator))
	move(d.repoPath(r.Name), filepath.Join(dest, rel+".git"))
	move(filepath.Join(d.cfg.DataPath, "lfs", strconv.FormatInt(r.ID, 10)), filepath.Join(dest, rel+".lfs"))
}

// ensureRepoOnDisk creates a bare repository for r if there is none, and
// brings its description, daemon export flag and hooks in line. Errors are
// logged, and the next reload tries again.
func (d *Backend) ensureRepoOnDisk(ctx context.Context, r statefile.RepoState, note func(string, ...any)) {
	rp := d.repoPath(r.Name)
	logErr := func(what string, err error) {
		d.logger.Error("failed to "+what, "repo", r.Name, "err", err)
	}

	if _, err := os.Stat(rp); errors.Is(err, fs.ErrNotExist) {
		if err := claimRepoPath(rp); err != nil {
			logErr("create repository directory", err)
			return
		}
		if _, err := git.Init(rp, true); err != nil {
			logErr("initialise repository", err)
			os.RemoveAll(rp) //nolint:errcheck
			return
		}
		note("initialise %s", rp)
	} else if err != nil {
		logErr("stat repository", err)
		return
	}

	descPath := filepath.Join(rp, "description")
	if cur, err := os.ReadFile(descPath); err != nil || !bytes.Equal(cur, []byte(r.Description)) {
		if err := os.WriteFile(descPath, []byte(r.Description), fs.ModePerm); err != nil { //nolint:gosec
			logErr("write description", err)
		}
	}

	export := filepath.Join(rp, "git-daemon-export-ok")
	if r.Private {
		if err := os.Remove(export); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logErr("remove git-daemon-export-ok", err)
		}
	} else if _, err := os.Stat(export); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(export, nil, fs.ModePerm); err != nil { //nolint:gosec
			logErr("write git-daemon-export-ok", err)
		}
	}

	if err := hooks.GenerateHooks(ctx, d.cfg, r.Name); err != nil {
		logErr("generate hooks", err)
	}
}

// ExportState reads users, repositories and access settings from the
// database.
func (d *Backend) ExportState(ctx context.Context) (*statefile.State, error) {
	st := &statefile.State{}
	if err := d.db.TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		if st.AnonAccess, err = d.store.GetAnonAccess(ctx, tx); err != nil {
			return err
		}
		if st.AllowKeyless, err = d.store.GetAllowKeylessAccess(ctx, tx); err != nil {
			return err
		}

		users, err := d.store.GetAllUsers(ctx, tx)
		if err != nil {
			return err
		}
		names := make(map[int64]string, len(users))
		for _, u := range users {
			names[u.ID] = u.Username
			pks, err := d.store.ListPublicKeysByUserID(ctx, tx, u.ID)
			if err != nil {
				return err
			}
			st.Users = append(st.Users, statefile.UserState{Name: u.Username, Admin: u.Admin, PublicKeys: pks})
		}

		repos, err := d.store.GetAllRepos(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range repos {
			rs := statefile.RepoState{
				Name:          r.Name,
				ProjectName:   r.ProjectName,
				Description:   r.Description,
				Private:       r.Private,
				Hidden:        r.Hidden,
				Collaborators: map[string]access.AccessLevel{},
			}
			if r.UserID.Valid {
				rs.Owner = names[r.UserID.Int64]
			}
			collabs, err := d.store.ListCollabsByRepo(ctx, tx, r.Name)
			if err != nil {
				return err
			}
			for _, c := range collabs {
				if name, ok := names[c.UserID]; ok {
					rs.Collaborators[name] = c.AccessLevel
				}
			}
			st.Repos = append(st.Repos, rs)
		}
		return nil
	}); err != nil {
		return nil, db.WrapError(err)
	}

	return st, nil
}

func hasKey(pks []ssh.PublicKey, pk ssh.PublicKey) bool {
	for _, k := range pks {
		if sshutils.KeysEqual(k, pk) {
			return true
		}
	}
	return false
}

// keyLabel names a key in a change line without printing all of it.
func keyLabel(pk ssh.PublicKey) string {
	return ssh.FingerprintSHA256(pk)
}
