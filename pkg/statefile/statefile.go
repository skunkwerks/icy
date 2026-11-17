// Package statefile reads, checks and writes the managed state in
// config.yaml: the anon_access, allow_keyless, users and repos keys that
// list a server's access settings, users and repositories, so that a
// configuration management tool can own them.
package statefile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/sshutils"
	"github.com/skunkwerks/icy/pkg/utils"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// Defaults for settings the file leaves out. They match a new database.
const (
	DefaultAnonAccess   = access.ReadOnlyAccess
	DefaultAllowKeyless = true
)

// File is the managed state part of config.yaml.
type File struct {
	// AnonAccess is the access level for anonymous users.
	AnonAccess string `yaml:"anon_access,omitempty"`

	// AllowKeyless allows connections that offer no public key.
	AllowKeyless *bool `yaml:"allow_keyless,omitempty"`

	// Users lists every user on the server. It is nil when config.yaml
	// has no users key.
	Users []User `yaml:"users"`

	// Repos lists every repository on the server. It is nil when
	// config.yaml has no repos key.
	Repos []Repo `yaml:"repos"`
}

// User is one user.
type User struct {
	Name       string   `yaml:"name"`
	Admin      bool     `yaml:"admin,omitempty"`
	PublicKeys []string `yaml:"public_keys,omitempty"`
}

// Repo is one repository.
type Repo struct {
	Name        string `yaml:"name"`
	ProjectName string `yaml:"project_name,omitempty"`
	Description string `yaml:"description,omitempty"`
	Private     bool   `yaml:"private,omitempty"`
	Hidden      bool   `yaml:"hidden,omitempty"`

	// Owner is the user who owns the repository and has admin access to
	// it. It defaults to the first admin in users.
	Owner string `yaml:"owner,omitempty"`

	// Collaborators maps a username to an access level.
	Collaborators map[string]string `yaml:"collaborators,omitempty"`
}

// State is checked state, ready to apply.
type State struct {
	AnonAccess   access.AccessLevel
	AllowKeyless bool
	Users        []UserState
	Repos        []RepoState
}

// UserState is a checked user.
type UserState struct {
	Name       string
	Admin      bool
	PublicKeys []ssh.PublicKey
}

// RepoState is a checked repository.
type RepoState struct {
	Name          string
	ProjectName   string
	Description   string
	Private       bool
	Hidden        bool
	Owner         string
	Collaborators map[string]access.AccessLevel
}

// Options changes how a file is checked.
type Options struct {
	// AdminKeysConfigured is true when the server config has
	// initial_admin_keys, which grant admin access whatever the file says.
	AdminKeysConfigured bool

	// AnonAccess and AllowKeyless, when set, replace the values in the
	// file. They carry ICY_ANON_ACCESS and ICY_ALLOW_KEYLESS, which beat
	// config.yaml here as everywhere else.
	AnonAccess   *access.AccessLevel
	AllowKeyless *bool
}

// OptionsFor returns the options for checking state on the server that
// cfg describes.
func OptionsFor(cfg *config.Config) (Options, error) {
	anon, allow, err := config.EnvAccessOverrides()
	if err != nil {
		return Options{}, err
	}
	return Options{
		AdminKeysConfigured: len(cfg.AdminKeys()) > 0,
		AnonAccess:          anon,
		AllowKeyless:        allow,
	}, nil
}

var (
	// ErrNoAdmin is returned when applying the file would leave nobody
	// able to administer the server.
	ErrNoAdmin = errors.New("no admin user with a public key, and no initial_admin_keys in the server config: applying this file would lock every admin out")

	// ErrNoState is returned when config.yaml has neither a users nor a
	// repos key. Applying that would remove every user and repository,
	// which is far more likely a mistake than the intent.
	ErrNoState = errors.New("config.yaml has no users or repos key: add them, for example from icy admin export-state, or write \"users: []\" and \"repos: []\" to really remove everything")
)

// doc is config.yaml as statefile sees it. The managed keys are decoded
// strictly. Every other top-level key lands in Rest and is left to package
// config, which decodes the whole file leniently.
type doc struct {
	File `yaml:",inline"`
	Rest map[string]yaml.Node `yaml:",inline"`
}

// Parse decodes the managed state from the contents of a config.yaml.
// Unknown keys inside users and repos are errors, so a typo cannot drop a
// setting. Other top-level keys are ignored.
func Parse(r io.Reader) (*File, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var d doc
	if err := dec.Decode(&d); err != nil {
		if errors.Is(err, io.EOF) {
			return &d.File, nil
		}
		return nil, fmt.Errorf("decode users and repos: %w", err)
	}

	return &d.File, nil
}

// HasState reports whether the file has a users or a repos key.
func (f *File) HasState() bool {
	return f.Users != nil || f.Repos != nil
}

// ParseFile parses the managed state in the config file at path.
func ParseFile(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	f, err := Parse(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Load reads, parses and checks the managed state in the config file at
// path.
func Load(path string, opts Options) (*State, error) {
	f, err := ParseFile(path)
	if err != nil {
		return nil, err
	}

	st, err := f.Check(opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return st, nil
}

// Check validates the file and returns the state it describes. It reports
// every problem it finds, not just the first.
func (f *File) Check(opts Options) (*State, error) {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	st := &State{
		AnonAccess:   DefaultAnonAccess,
		AllowKeyless: DefaultAllowKeyless,
	}

	if !f.HasState() {
		errs = append(errs, ErrNoState)
	}

	// The environment beats config.yaml, which beats the default.
	if f.AnonAccess != "" {
		st.AnonAccess = access.ParseAccessLevel(f.AnonAccess)
		if st.AnonAccess < 0 {
			fail("anon_access: %q is not one of no-access, read-only, read-write, admin-access", f.AnonAccess)
		}
	}
	if opts.AnonAccess != nil {
		st.AnonAccess = *opts.AnonAccess
	}
	if f.AllowKeyless != nil {
		st.AllowKeyless = *f.AllowKeyless
	}
	if opts.AllowKeyless != nil {
		st.AllowKeyless = *opts.AllowKeyless
	}

	users := make(map[string]bool, len(f.Users))
	keys := make(map[string]string)
	var firstAdmin string
	var adminWithKey bool
	for i, u := range f.Users {
		name := strings.ToLower(strings.TrimSpace(u.Name))
		if err := utils.ValidateUsername(name); err != nil {
			fail("users[%d]: %q: %w", i, u.Name, err)
			continue
		}
		if users[name] {
			fail("users[%d]: %q is listed more than once", i, name)
			continue
		}
		users[name] = true

		us := UserState{Name: name, Admin: u.Admin}
		for j, k := range u.PublicKeys {
			pk, _, err := sshutils.ParseAuthorizedKey(strings.TrimSpace(k))
			if err != nil {
				fail("users[%d] %s: public_keys[%d]: %w", i, name, j, err)
				continue
			}
			ak := sshutils.MarshalAuthorizedKey(pk)
			if other, ok := keys[ak]; ok {
				if other == name {
					fail("users[%d] %s: public_keys[%d] is listed more than once", i, name, j)
				} else {
					fail("users[%d] %s: public_keys[%d] already belongs to %s", i, name, j, other)
				}
				continue
			}
			keys[ak] = name
			us.PublicKeys = append(us.PublicKeys, pk)
		}

		if us.Admin {
			if firstAdmin == "" {
				firstAdmin = name
			}
			if len(us.PublicKeys) > 0 {
				adminWithKey = true
			}
		}

		st.Users = append(st.Users, us)
	}

	if !adminWithKey && !opts.AdminKeysConfigured {
		errs = append(errs, ErrNoAdmin)
	}

	repos := make(map[string]string, len(f.Repos))
	for i, r := range f.Repos {
		name := utils.SanitizeRepo(r.Name)
		if name != r.Name {
			fail("repos[%d]: %q is not in its canonical form, use %q", i, r.Name, name)
			continue
		}
		if err := utils.ValidateRepo(name); err != nil {
			fail("repos[%d]: %q: %w", i, r.Name, err)
			continue
		}
		// Names that differ only in case share a directory on some file
		// systems, so treat them as the same repository.
		folded := strings.ToLower(name)
		if other, ok := repos[folded]; ok {
			if other == name {
				fail("repos[%d]: %q is listed more than once", i, name)
			} else {
				fail("repos[%d]: %q differs from %q only in case", i, name, other)
			}
			continue
		}
		repos[folded] = name

		rs := RepoState{
			Name:          name,
			ProjectName:   utils.Sanitize(r.ProjectName),
			Description:   utils.Sanitize(r.Description),
			Private:       r.Private,
			Hidden:        r.Hidden,
			Owner:         strings.ToLower(strings.TrimSpace(r.Owner)),
			Collaborators: make(map[string]access.AccessLevel, len(r.Collaborators)),
		}

		switch {
		case rs.Owner == "" && firstAdmin != "":
			rs.Owner = firstAdmin
		case rs.Owner == "":
			fail("repos[%d] %s: no owner, and no admin user to default to", i, name)
		case !users[rs.Owner]:
			fail("repos[%d] %s: owner %q is not in users", i, name, r.Owner)
		}

		for user, level := range r.Collaborators {
			un := strings.ToLower(strings.TrimSpace(user))
			if !users[un] {
				fail("repos[%d] %s: collaborator %q is not in users", i, name, user)
				continue
			}
			if _, ok := rs.Collaborators[un]; ok {
				fail("repos[%d] %s: collaborator %q is listed more than once", i, name, un)
				continue
			}
			al := access.ParseAccessLevel(level)
			if al < 0 {
				fail("repos[%d] %s: collaborator %s: %q is not one of no-access, read-only, read-write, admin-access", i, name, un, level)
				continue
			}
			rs.Collaborators[un] = al
		}

		st.Repos = append(st.Repos, rs)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return st, nil
}

// File returns the state as a file, sorted so that the output is stable.
func (st *State) File() *File {
	allow := st.AllowKeyless
	f := &File{
		AnonAccess:   st.AnonAccess.String(),
		AllowKeyless: &allow,
		Users:        make([]User, 0, len(st.Users)),
		Repos:        make([]Repo, 0, len(st.Repos)),
	}

	for _, u := range st.Users {
		fu := User{Name: u.Name, Admin: u.Admin}
		for _, pk := range u.PublicKeys {
			fu.PublicKeys = append(fu.PublicKeys, sshutils.MarshalAuthorizedKey(pk))
		}
		f.Users = append(f.Users, fu)
	}
	sort.Slice(f.Users, func(i, j int) bool { return f.Users[i].Name < f.Users[j].Name })

	for _, r := range st.Repos {
		fr := Repo{
			Name:        r.Name,
			ProjectName: r.ProjectName,
			Description: r.Description,
			Private:     r.Private,
			Hidden:      r.Hidden,
			Owner:       r.Owner,
		}
		if len(r.Collaborators) > 0 {
			fr.Collaborators = make(map[string]string, len(r.Collaborators))
			for u, l := range r.Collaborators {
				fr.Collaborators[u] = l.String()
			}
		}
		f.Repos = append(f.Repos, fr)
	}
	sort.Slice(f.Repos, func(i, j int) bool { return f.Repos[i].Name < f.Repos[j].Name })

	return f
}

// Write encodes the file as YAML.
func (f *File) Write(w io.Writer) error {
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return err
	}
	return enc.Close()
}

const header = `# Users, repositories and access settings for config.yaml.
#
# Paste this into config.yaml, replacing any anon_access, allow_keyless,
# users or repos keys already there, and set state_source: "config". At
# startup and on SIGHUP the server then makes its database match these
# keys. Anything they do not list is removed.
`
