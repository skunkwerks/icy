package statefile

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/sshutils"
	"golang.org/x/crypto/ssh"
)

// testKey returns a fixed authorized key for seed.
func testKey(t *testing.T, seed byte) string {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	pk, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	return sshutils.MarshalAuthorizedKey(pk)
}

func parse(t *testing.T, s string) *File {
	t.Helper()
	f, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func TestParseAndCheck(t *testing.T) {
	k1, k2 := testKey(t, 1), testKey(t, 2)
	// A whole config.yaml: the server settings are someone else's business.
	f := parse(t, `
name: "Icy"
ssh:
  listen_addr: ":23231"
  some_future_key: true
state_source: config
anon_access: no-access
allow_keyless: false
users:
  - name: Alice
    admin: true
    public_keys:
      - "`+k1+` alice@laptop"
  - name: bob
    public_keys: ["`+k2+`"]
repos:
  - name: icy
    project_name: Icy
    description: A Git server
    private: true
    collaborators:
      bob: read-write
  - name: team/notes
    hidden: true
    owner: bob
`)

	st, err := f.Check(Options{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if st.AnonAccess != access.NoAccess || st.AllowKeyless {
		t.Errorf("settings = %v, %v", st.AnonAccess, st.AllowKeyless)
	}
	if len(st.Users) != 2 || st.Users[0].Name != "alice" || !st.Users[0].Admin || len(st.Users[0].PublicKeys) != 1 {
		t.Errorf("users = %+v", st.Users)
	}
	if len(st.Repos) != 2 {
		t.Fatalf("repos = %+v", st.Repos)
	}
	icy := st.Repos[0]
	if icy.Owner != "alice" || !icy.Private || icy.ProjectName != "Icy" || icy.Collaborators["bob"] != access.ReadWriteAccess {
		t.Errorf("icy = %+v", icy)
	}
	if notes := st.Repos[1]; notes.Owner != "bob" || !notes.Hidden || notes.Private {
		t.Errorf("notes = %+v", notes)
	}
}

func TestDefaultsAndEnv(t *testing.T) {
	opts := Options{AdminKeysConfigured: true}
	st, err := parse(t, "users: []\n").Check(opts)
	if err != nil {
		t.Fatal(err)
	}
	if st.AnonAccess != DefaultAnonAccess || st.AllowKeyless != DefaultAllowKeyless {
		t.Errorf("defaults = %v, %v", st.AnonAccess, st.AllowKeyless)
	}

	// The environment beats config.yaml.
	anon, allow := access.AdminAccess, false
	opts.AnonAccess, opts.AllowKeyless = &anon, &allow
	st, err = parse(t, "anon_access: no-access\nallow_keyless: true\nrepos: []\n").Check(opts)
	if err != nil {
		t.Fatal(err)
	}
	if st.AnonAccess != access.AdminAccess || st.AllowKeyless {
		t.Errorf("env overrides = %v, %v", st.AnonAccess, st.AllowKeyless)
	}
}

func TestCheckNeedsUsersOrRepos(t *testing.T) {
	for _, s := range []string{"", "name: Icy\n", "anon_access: read-only\n", "users:\n"} {
		f := parse(t, s)
		if f.HasState() {
			t.Errorf("%q: HasState = true", s)
		}
		if _, err := f.Check(Options{AdminKeysConfigured: true}); !errors.Is(err, ErrNoState) {
			t.Errorf("%q: err = %v, want ErrNoState", s, err)
		}
	}
	for _, s := range []string{"users: []\n", "repos: []\n"} {
		if !parse(t, s).HasState() {
			t.Errorf("%q: HasState = false", s)
		}
	}
}

func TestParseIsStrictOnlyInsideState(t *testing.T) {
	// Unknown top-level keys belong to the rest of config.yaml.
	parse(t, "not_a_real_setting: 1\nusers: []\n")

	_, err := Parse(strings.NewReader("users:\n  - name: a\n    admn: true\n"))
	if err == nil || !strings.Contains(err.Error(), "admn") {
		t.Errorf("err = %v, want unknown field admn", err)
	}

	_, err = Parse(strings.NewReader("repos:\n  - name: a\n    project-name: x\n"))
	if err == nil || !strings.Contains(err.Error(), "project-name") {
		t.Errorf("err = %v, want unknown field project-name", err)
	}

	_, err = Parse(strings.NewReader("repos:\n  - name: a\n    collaborators:\n      bob: read-only\n      bob: admin-access\n"))
	if err == nil {
		t.Error("duplicate collaborator key accepted")
	}
}

func TestCheckLockout(t *testing.T) {
	k1 := testKey(t, 1)
	cases := map[string]string{
		"no users":          "users: []\n",
		"admin without key": "users:\n  - name: alice\n    admin: true\n",
		"keyed non-admin":   "users:\n  - name: alice\n    public_keys: [\"" + k1 + "\"]\n",
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, s).Check(Options{})
			if !errors.Is(err, ErrNoAdmin) {
				t.Errorf("err = %v, want ErrNoAdmin", err)
			}
			if _, err := parse(t, s).Check(Options{AdminKeysConfigured: true}); err != nil {
				t.Errorf("with admin keys configured: %v", err)
			}
		})
	}
}

func TestCheckErrors(t *testing.T) {
	k1, k2 := testKey(t, 1), testKey(t, 2)
	admin := "  - name: root\n    admin: true\n    public_keys: [\"" + k1 + "\"]\n"
	cases := []struct {
		name, file, want string
	}{
		{"bad anon_access", "anon_access: everyone\nusers:\n" + admin, `anon_access: "everyone"`},
		{"bad username", "users:\n" + admin + "  - name: 1bob\n", "must start with a letter"},
		{"duplicate user", "users:\n" + admin + "  - name: ROOT\n", `"root" is listed more than once`},
		{"bad key", "users:\n" + admin + "  - name: bob\n    public_keys: [nope]\n", "bob: public_keys[0]"},
		{"shared key", "users:\n" + admin + "  - name: bob\n    public_keys: [\"" + k1 + "\"]\n", "already belongs to root"},
		{"repeated key", "users:\n" + admin + "  - name: bob\n    public_keys: [\"" + k2 + "\", \"" + k2 + "\"]\n", "public_keys[1] is listed more than once"},
		{"non-canonical repo", "users:\n" + admin + "repos:\n  - name: foo.git\n", `use "foo"`},
		{"bad repo", "users:\n" + admin + "repos:\n  - name: \"a b\"\n", "repo can only contain"},
		{"duplicate repo", "users:\n" + admin + "repos:\n  - name: foo\n  - name: foo\n", `"foo" is listed more than once`},
		{"case clash", "users:\n" + admin + "repos:\n  - name: foo\n  - name: Foo\n", "only in case"},
		{"unknown owner", "users:\n" + admin + "repos:\n  - name: foo\n    owner: carol\n", `owner "carol" is not in users`},
		{"unknown collab", "users:\n" + admin + "repos:\n  - name: foo\n    collaborators: {carol: read-only}\n", `collaborator "carol" is not in users`},
		{"bad level", "users:\n" + admin + "repos:\n  - name: foo\n    collaborators: {root: write}\n", `"write" is not one of`},
		{"no owner", "users:\n  - name: bob\nrepos:\n  - name: foo\n", "no owner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parse(t, c.file).Check(Options{AdminKeysConfigured: true})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestCheckReportsEveryError(t *testing.T) {
	_, err := parse(t, "anon_access: x\nusers:\n  - name: 1a\n  - name: 2b\n").Check(Options{})
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"anon_access", `"1a"`, `"2b"`, "lock every admin out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestWriteRoundTrip(t *testing.T) {
	k1 := testKey(t, 1)
	src := parse(t, `
users:
  - name: zed
  - name: alice
    admin: true
    public_keys: ["`+k1+`"]
repos:
  - name: b
    collaborators: {zed: read-only}
  - name: a
    private: true
    project_name: "Project A"
    description: "line: with colon"
`)
	st, err := src.Check(Options{})
	if err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	if err := st.File().Write(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"anon_access: read-only", "allow_keyless: true", "public_keys:", "project_name: Project A"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	again, err := parse(t, out).Check(Options{})
	if err != nil {
		t.Fatalf("written keys do not check: %v\n%s", err, out)
	}

	// Output is sorted by name.
	if again.Users[0].Name != "alice" || again.Repos[0].Name != "a" {
		t.Errorf("not sorted:\n%s", out)
	}
	if again.Repos[0].Description != "line: with colon" || !again.Repos[0].Private || again.Repos[0].Owner != "alice" {
		t.Errorf("repo a = %+v", again.Repos[0])
	}
	if again.Repos[1].Collaborators["zed"] != access.ReadOnlyAccess {
		t.Errorf("repo b = %+v", again.Repos[1])
	}

	// An empty state still writes both keys, so that it checks again.
	b.Reset()
	if err := (&State{AnonAccess: access.ReadOnlyAccess}).File().Write(&b); err != nil {
		t.Fatal(err)
	}
	if !parse(t, b.String()).HasState() {
		t.Errorf("empty export has no users or repos key:\n%s", b.String())
	}
}
