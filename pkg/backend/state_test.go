package backend

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/sshutils"
	"github.com/skunkwerks/icy/pkg/statefile"
	"golang.org/x/crypto/ssh"
)

func stateKey(t *testing.T, seed byte) string {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	pk, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	return sshutils.MarshalAuthorizedKey(pk)
}

func checkState(t *testing.T, s string) *statefile.State {
	t.Helper()
	f, err := statefile.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	st, err := f.Check(statefile.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestApplyState(t *testing.T) {
	is := is.New(t)
	be, cfg := newTestBackend(t)
	ctx := context.Background()
	k1, k2, k3 := stateKey(t, 1), stateKey(t, 2), stateKey(t, 3)

	// A repository made the usual way, which the file does not list.
	_, err := be.CreateRepository(ctx, "old", nil, proto.RepositoryOptions{})
	is.NoErr(err)
	is.NoErr(os.MkdirAll(filepath.Join(cfg.DataPath, "lfs", "1"), os.ModePerm))

	st := checkState(t, `
anon_access: no-access
allow_keyless: false
users:
  - name: alice
    admin: true
    public_keys: ["`+k1+`"]
  - name: bob
    public_keys: ["`+k2+`"]
repos:
  - name: icy
    project_name: Icy
    description: hello
    private: true
    collaborators: {bob: read-write}
  - name: team/notes
    owner: bob
`)

	changes, err := be.ApplyState(ctx, st)
	is.NoErr(err)
	is.True(len(changes) > 0)

	is.Equal(be.AnonAccess(ctx), access.NoAccess)
	is.Equal(be.AllowKeyless(ctx), false)

	users, err := be.Users(ctx)
	is.NoErr(err)
	slices.Sort(users)
	is.Equal(users, []string{"alice", "bob"}) // the seeded admin user is gone

	alice, err := be.User(ctx, "alice")
	is.NoErr(err)
	is.True(alice.IsAdmin())
	is.Equal(len(alice.PublicKeys()), 1)

	icy, err := be.Repository(ctx, "icy")
	is.NoErr(err)
	is.True(icy.IsPrivate())
	is.Equal(icy.ProjectName(), "Icy")
	is.Equal(icy.Description(), "hello")
	is.Equal(icy.UserID(), alice.ID())
	desc, err := os.ReadFile(filepath.Join(be.repoPath("icy"), "description"))
	is.NoErr(err)
	is.Equal(string(desc), "hello")
	_, err = os.Stat(filepath.Join(be.repoPath("icy"), "git-daemon-export-ok"))
	is.True(errors.Is(err, os.ErrNotExist))

	level, ok, err := be.IsCollaborator(ctx, "icy", "bob")
	is.NoErr(err)
	is.True(ok)
	is.Equal(level, access.ReadWriteAccess)

	notes, err := be.Repository(ctx, "team/notes")
	is.NoErr(err)
	bob, err := be.User(ctx, "bob")
	is.NoErr(err)
	is.Equal(notes.UserID(), bob.ID())

	// The repository the file does not list is gone from the database and
	// its directory has been moved aside, not deleted.
	_, err = be.Repository(ctx, "old")
	is.True(errors.Is(err, proto.ErrRepoNotFound))
	archived, err := filepath.Glob(filepath.Join(cfg.DataPath, "repos-removed", "*", "old.git"))
	is.NoErr(err)
	is.Equal(len(archived), 1)
	lfsArchived, err := filepath.Glob(filepath.Join(cfg.DataPath, "repos-removed", "*", "old.lfs"))
	is.NoErr(err)
	is.Equal(len(lfsArchived), 1)

	// Applying the same state again changes nothing.
	changes, err = be.ApplyState(ctx, st)
	is.NoErr(err)
	is.Equal(changes, []string(nil))

	// Move a key from bob to a new user, demote alice's grant, drop bob
	// from icy, delete notes, and remove bob, who owned it.
	st = checkState(t, `
users:
  - name: alice
    admin: true
    public_keys: ["`+k1+`", "`+k3+`"]
  - name: carol
    public_keys: ["`+k2+`"]
repos:
  - name: icy
    collaborators: {carol: read-only}
`)
	_, err = be.ApplyState(ctx, st)
	is.NoErr(err)

	users, err = be.Users(ctx)
	is.NoErr(err)
	slices.Sort(users)
	is.Equal(users, []string{"alice", "carol"})

	pk, _, err := sshutils.ParseAuthorizedKey(k2)
	is.NoErr(err)
	owner, err := be.UserByPublicKey(ctx, pk)
	is.NoErr(err)
	is.Equal(owner.Username(), "carol")

	alice, err = be.User(ctx, "alice")
	is.NoErr(err)
	is.Equal(len(alice.PublicKeys()), 2)

	icy, err = be.Repository(ctx, "icy")
	is.NoErr(err)
	is.True(!icy.IsPrivate())
	is.Equal(icy.Description(), "")
	_, err = os.Stat(filepath.Join(be.repoPath("icy"), "git-daemon-export-ok"))
	is.NoErr(err)
	collabs, err := be.Collaborators(ctx, "icy")
	is.NoErr(err)
	is.Equal(collabs, []string{"carol"})
	level, _, err = be.IsCollaborator(ctx, "icy", "carol")
	is.NoErr(err)
	is.Equal(level, access.ReadOnlyAccess)

	_, err = be.Repository(ctx, "team/notes")
	is.True(errors.Is(err, proto.ErrRepoNotFound))

	is.Equal(be.AnonAccess(ctx), statefile.DefaultAnonAccess)
	is.Equal(be.AllowKeyless(ctx), statefile.DefaultAllowKeyless)
}

// TestApplyStateIsAtomic feeds ApplyState a state that fails part way, which
// Check would never pass, and expects nothing to have changed.
func TestApplyStateIsAtomic(t *testing.T) {
	is := is.New(t)
	be, _ := newTestBackend(t)
	ctx := context.Background()

	pk, _, err := sshutils.ParseAuthorizedKey(stateKey(t, 1))
	is.NoErr(err)
	st := &statefile.State{
		AnonAccess: access.NoAccess,
		Users:      []statefile.UserState{{Name: "alice", Admin: true, PublicKeys: []ssh.PublicKey{pk}}},
		Repos: []statefile.RepoState{{
			Name:          "icy",
			Owner:         "alice",
			Collaborators: map[string]access.AccessLevel{"ghost": access.ReadOnlyAccess},
		}},
	}

	_, err = be.ApplyState(ctx, st)
	is.True(err != nil)

	users, err := be.Users(ctx)
	is.NoErr(err)
	is.Equal(users, []string{"admin"})
	is.Equal(be.AnonAccess(ctx), access.ReadOnlyAccess)
	repos, err := be.Repositories(ctx)
	is.NoErr(err)
	is.Equal(len(repos), 0)
	_, err = os.Stat(be.repoPath("icy"))
	is.True(errors.Is(err, os.ErrNotExist))
}

func TestExportStateRoundTrip(t *testing.T) {
	is := is.New(t)
	be, _ := newTestBackend(t)
	ctx := context.Background()
	k1, k2 := stateKey(t, 1), stateKey(t, 2)

	pk1, _, err := sshutils.ParseAuthorizedKey(k1)
	is.NoErr(err)
	pk2, _, err := sshutils.ParseAuthorizedKey(k2)
	is.NoErr(err)
	alice, err := be.CreateUser(ctx, "alice", proto.UserOptions{Admin: true, PublicKeys: []ssh.PublicKey{pk1}})
	is.NoErr(err)
	_, err = be.CreateUser(ctx, "bob", proto.UserOptions{PublicKeys: []ssh.PublicKey{pk2}})
	is.NoErr(err)
	_, err = be.CreateRepository(ctx, "icy", alice, proto.RepositoryOptions{Private: true, Description: "d", ProjectName: "p"})
	is.NoErr(err)
	// The backend method sends a webhook, which needs more context than
	// this test has.
	is.NoErr(be.store.AddCollabByUsernameAndRepo(ctx, be.db, "bob", "icy", access.AdminAccess))
	is.NoErr(be.SetAnonAccess(ctx, access.NoAccess))

	st, err := be.ExportState(ctx)
	is.NoErr(err)

	var b bytes.Buffer
	is.NoErr(st.File().Write(&b))
	f, err := statefile.Parse(&b)
	is.NoErr(err)
	again, err := f.Check(statefile.Options{})
	is.NoErr(err)

	is.Equal(again.AnonAccess, access.NoAccess)
	is.Equal(len(again.Repos), 1)
	is.Equal(again.Repos[0].Owner, "alice")
	is.Equal(again.Repos[0].Collaborators["bob"], access.AdminAccess)
	is.True(again.Repos[0].Private)

	// Applying the export to the database it came from changes nothing.
	changes, err := be.ApplyState(ctx, again)
	is.NoErr(err)
	is.Equal(changes, []string(nil))
}

func TestConfigModeRefusesChanges(t *testing.T) {
	is := is.New(t)
	be, cfg := newTestBackend(t)
	ctx := context.Background()

	_, err := be.CreateRepository(ctx, "icy", nil, proto.RepositoryOptions{})
	is.NoErr(err)
	cfg.StateSource = config.StateSourceConfig

	pk, _, err := sshutils.ParseAuthorizedKey(stateKey(t, 1))
	is.NoErr(err)

	refused := map[string]error{
		"CreateUser":       func() error { _, err := be.CreateUser(ctx, "bob", proto.UserOptions{}); return err }(),
		"DeleteUser":       be.DeleteUser(ctx, "admin"),
		"AddPublicKey":     be.AddPublicKey(ctx, "admin", pk),
		"RemovePublicKey":  be.RemovePublicKey(ctx, "admin", pk),
		"SetAdmin":         be.SetAdmin(ctx, "admin", false),
		"SetUsername":      be.SetUsername(ctx, "admin", "root"),
		"SetAnonAccess":    be.SetAnonAccess(ctx, access.AdminAccess),
		"SetAllowKeyless":  be.SetAllowKeyless(ctx, false),
		"CreateRepository": func() error { _, err := be.CreateRepository(ctx, "new", nil, proto.RepositoryOptions{}); return err }(),
		"CreateExisting":   func() error { _, err := be.CreateRepository(ctx, "icy", nil, proto.RepositoryOptions{}); return err }(),
		"ImportRepository": func() error {
			_, err := be.ImportRepository(ctx, "imp", nil, "https://example.com/x.git", proto.RepositoryOptions{})
			return err
		}(),
		"DeleteRepository":   be.DeleteRepository(ctx, "icy"),
		"RenameRepository":   be.RenameRepository(ctx, "icy", "icy2"),
		"SetPrivate":         be.SetPrivate(ctx, "icy", true),
		"SetHidden":          be.SetHidden(ctx, "icy", true),
		"SetDescription":     be.SetDescription(ctx, "icy", "x"),
		"SetProjectName":     be.SetProjectName(ctx, "icy", "x"),
		"AddCollaborator":    be.AddCollaborator(ctx, "icy", "admin", access.ReadOnlyAccess),
		"RemoveCollaborator": be.RemoveCollaborator(ctx, "icy", "admin"),
	}
	for name, err := range refused {
		if !errors.Is(err, proto.ErrManagedByConfig) {
			t.Errorf("%s: err = %v, want ErrManagedByConfig", name, err)
		}
	}

	// Nothing changed underneath.
	_, err = be.Repository(ctx, "icy")
	is.NoErr(err)
	is.Equal(be.AnonAccess(ctx), access.ReadOnlyAccess)
}

// TestConfigModeAccessSettings checks the one rule for anon_access and
// allow_keyless: the environment beats config.yaml, which beats the
// database. In config mode the reconcile writes the effective value to the
// database and the backend reads it from there, so a reload can change it.
func TestConfigModeAccessSettings(t *testing.T) {
	is := is.New(t)
	be, cfg := newTestBackend(t)
	ctx := context.Background()
	k1 := stateKey(t, 1)

	path := filepath.Join(cfg.DataPath, "config.yaml")
	t.Setenv("ICY_CONFIG_LOCATION", path)
	t.Setenv("ICY_ANON_ACCESS", "")
	t.Setenv("ICY_ALLOW_KEYLESS", "")
	write := func(s string) {
		t.Helper()
		is.NoErr(os.WriteFile(path, []byte(s), 0o600))
	}
	users := "users:\n  - name: alice\n    admin: true\n    public_keys: [\"" + k1 + "\"]\n"

	// The startup copy of config.yaml says admin-access. In db mode it
	// overrides the database.
	admin := access.AdminAccess
	cfg.AnonAccess = &admin
	is.Equal(be.AnonAccess(ctx), access.AdminAccess)

	// In config mode the reconcile decides, from config.yaml as it is now.
	cfg.StateSource = config.StateSourceConfig
	write("anon_access: no-access\n" + users)
	_, err := be.ApplyConfigState(ctx)
	is.NoErr(err)
	is.Equal(be.AnonAccess(ctx), access.NoAccess)

	// Dropping the key from config.yaml falls back to the default.
	write(users)
	_, err = be.ApplyConfigState(ctx)
	is.NoErr(err)
	is.Equal(be.AnonAccess(ctx), statefile.DefaultAnonAccess)
	is.Equal(be.AllowKeyless(ctx), statefile.DefaultAllowKeyless)

	// The environment beats config.yaml.
	t.Setenv("ICY_ANON_ACCESS", "read-write")
	t.Setenv("ICY_ALLOW_KEYLESS", "false")
	write("anon_access: no-access\nallow_keyless: true\n" + users)
	_, err = be.ApplyConfigState(ctx)
	is.NoErr(err)
	is.Equal(be.AnonAccess(ctx), access.ReadWriteAccess)
	is.Equal(be.AllowKeyless(ctx), false)
}

// TestApplyConfigStateRefusesBadFile checks that a config.yaml with errors,
// or with no users or repos at all, changes nothing.
func TestApplyConfigStateRefusesBadFile(t *testing.T) {
	is := is.New(t)
	be, cfg := newTestBackend(t)
	ctx := context.Background()
	cfg.StateSource = config.StateSourceConfig

	path := filepath.Join(cfg.DataPath, "config.yaml")
	t.Setenv("ICY_CONFIG_LOCATION", path)

	_, err := be.CreateRepository(ctx, "keep", nil, proto.RepositoryOptions{})
	is.True(err != nil) // refused in config mode
	cfg.StateSource = config.StateSourceDB
	_, err = be.CreateRepository(ctx, "keep", nil, proto.RepositoryOptions{})
	is.NoErr(err)
	cfg.StateSource = config.StateSourceConfig

	for _, s := range []string{
		"name: Icy\n", // no users or repos key
		"users:\n  - name: alice\n    admn: true\n", // typo
		"users:\n  - name: alice\n",                 // nobody can administer
	} {
		is.NoErr(os.WriteFile(path, []byte(s), 0o600))
		_, err := be.ApplyConfigState(ctx)
		is.True(err != nil)
	}

	users, err := be.Users(ctx)
	is.NoErr(err)
	is.Equal(users, []string{"admin"})
	_, err = be.Repository(ctx, "keep")
	is.NoErr(err)
}
