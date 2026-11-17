package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/keygen"
	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/proto"
)

// uiFixture is a server with four repositories owned by "owner":
//
//	pub         public
//	org/nested  public, nested name
//	hid         unlisted
//	secret      private
//
// Each holds the same small history. "stranger" is a registered user with
// no access to anything private.
type uiFixture struct {
	ctx      context.Context
	be       *backend.Backend
	cfg      *config.Config
	handler  http.Handler
	owner    proto.User
	stranger proto.User
	head     string
}

func newUIFixture(t *testing.T) *uiFixture {
	t.Helper()
	is := is.New(t)
	ctx, be, _ := newLFSTestContext(t)

	cfg := config.FromContext(ctx)
	cfg.Name = "Test Icy"
	cfg.Web.Owner = "ops@example.org"
	cfg.Web.Description = "A test server."
	cfg.SSH.KeyPath = filepath.Join(cfg.DataPath, "ssh", "host_key")
	_, err := keygen.New(cfg.SSH.KeyPath, keygen.WithKeyType(keygen.Ed25519), keygen.WithWrite())
	is.NoErr(err)

	owner, err := be.CreateUser(ctx, "owner", proto.UserOptions{})
	is.NoErr(err)
	stranger, err := be.CreateUser(ctx, "stranger", proto.UserOptions{})
	is.NoErr(err)

	f := &uiFixture{ctx: ctx, be: be, cfg: cfg, owner: owner, stranger: stranger}
	for name, v := range map[string]proto.Visibility{
		"pub":        proto.VisibilityPublic,
		"org/nested": proto.VisibilityPublic,
		"hid":        proto.VisibilityUnlisted,
		"secret":     proto.VisibilityPrivate,
	} {
		private, hidden := v.Flags()
		_, err := be.CreateRepository(ctx, name, owner, proto.RepositoryOptions{
			Private:     private,
			Hidden:      hidden,
			Description: "the " + name + " repository",
		})
		is.NoErr(err)
		f.head = seedRepo(t, filepath.Join(cfg.DataPath, "repos", name+".git"))
	}

	f.handler = NewRouter(ctx)
	return f
}

// seedRepo pushes two commits and a tag to the bare repository at dir and
// returns the head commit id.
func seedRepo(t *testing.T, dir string) string {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Ada",
			"GIT_AUTHOR_EMAIL=ada@example.org",
			"GIT_COMMITTER_NAME=Ada",
			"GIT_COMMITTER_EMAIL=ada@example.org",
			"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z",
			"GIT_COMMITTER_DATE=2026-01-02T03:04:05Z",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	write("README.md", "# Hello readme\n\nSome *text* and <script>alert(1)</script> [x](javascript:alert(1)).\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("docs/guide.txt", "guide\n")
	run("add", "-A")
	run("commit", "-q", "-m", "first commit")
	run("tag", "v1")
	write("main.go", "package main\n\nfunc main() { println(\"hi <b>\") }\n")
	write("bin.dat", "\x00\x01\x02binary")
	run("add", "-A")
	run("commit", "-q", "-m", "second commit")
	run("push", "-q", dir, "main", "v1")
	return run("rev-parse", "HEAD")
}

// do serves a request with an optional session cookie.
func (f *uiFixture) do(method, path string, cookie *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func (f *uiFixture) get(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return f.do(http.MethodGet, path, cookie)
}

// sessionCookieOf returns the session cookie set by a response, if any.
func sessionCookieOf(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

// login signs user in through the sign-in link and returns the cookie.
func (f *uiFixture) login(t *testing.T, user proto.User) *http.Cookie {
	t.Helper()
	is := is.New(t)
	token, err := f.be.CreateWebLogin(f.ctx, user, "/", time.Minute)
	is.NoErr(err)
	w := f.get("/~/login?token="+url.QueryEscape(token), nil)
	is.Equal(w.Code, http.StatusSeeOther)
	c := sessionCookieOf(w)
	is.True(c != nil)
	return &http.Cookie{Name: c.Name, Value: c.Value}
}

func TestUIHome(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)

	w := f.get("/", nil)
	is.Equal(w.Code, http.StatusOK)
	is.Equal(w.Header().Get("Content-Type"), "text/html; charset=utf-8")
	is.True(strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'"))
	body := w.Body.String()
	for _, want := range []string{"Test Icy", "ops@example.org", "A test server.", `href="/pub"`, `href="/org/nested"`, "the pub repository", "web login"} {
		is.True(strings.Contains(body, want))
	}
	is.True(!strings.Contains(body, `href="/hid"`))
	is.True(!strings.Contains(body, "secret"))

	// The owner also sees their private repository, but never the
	// unlisted one.
	body = f.get("/", f.login(t, f.owner)).Body.String()
	is.True(strings.Contains(body, `href="/secret"`))
	is.True(strings.Contains(body, "owner"))
	is.True(!strings.Contains(body, `href="/hid"`))

	// go-get on the front page is not the front page.
	is.Equal(f.get("/?go-get=1", nil).Code, http.StatusNotFound)

	is.Equal(f.get("/~/style.css", nil).Code, http.StatusOK)
	is.True(strings.Contains(f.get("/~/style.css", nil).Body.String(), ".chroma"))
}

func TestUIPages(t *testing.T) {
	f := newUIFixture(t)

	cases := []struct {
		path string
		code int
		want []string
		not  []string
	}{
		{"/pub", 200, []string{"Hello readme", "<em>text</em>", "git clone http://localhost:23232/pub.git", "second commit", "README.md"}, []string{"<script>", "javascript:"}},
		{"/pub.git", 200, []string{"Hello readme"}, nil},
		{"/org/nested", 200, []string{"Hello readme", "org/nested.git"}, nil},
		{"/pub/~/tree", 200, []string{"main.go", "docs/", "bin.dat"}, nil},
		{"/pub/~/tree/docs", 200, []string{"guide.txt"}, nil},
		{"/pub/~/tree/main.go", 200, []string{`class="chroma"`}, nil},
		{"/pub/~/blob/main.go", 200, []string{`class="chroma"`, `id="L3"`, "&lt;b&gt;", "/pub/~/raw/main.go"}, []string{"<b>"}},
		{"/pub/~/blob/main.go?h=v1", 200, []string{`id="L3"`, "at <code>v1</code>"}, []string{"println"}},
		{"/pub/~/blob/bin.dat", 200, []string{"binary file"}, nil},
		{"/pub/~/log", 200, []string{"second commit", "first commit", "Ada"}, nil},
		{"/pub/~/log?h=v1", 200, []string{"first commit"}, []string{"second commit"}},
		{"/pub/~/commit/" + f.head, 200, []string{"second commit", "main.go", `class="add"`, "println", "bin.dat"}, nil},
		{"/pub/~/commit/" + f.head[:8], 200, []string{f.head}, nil},
		{"/pub/~/refs", 200, []string{"main", "v1", "default"}, nil},
		{"/hid", 200, []string{"unlisted"}, nil},
		{"/hid/~/tree", 200, []string{"main.go"}, nil},

		// Things that do not exist in a readable repository.
		{"/pub/~/tree/nope", 404, nil, nil},
		{"/pub/~/blob/docs", 404, nil, nil},
		// The router cleans dot segments with a redirect before any
		// handler runs, whatever the repository.
		{"/pub/~/blob/../../secret", 301, nil, nil},
		{"/pub/~/tree?h=nope", 404, nil, nil},
		{"/pub/~/tree?h=--output=x", 404, nil, nil},
		{"/pub/~/log?h=-n1", 404, nil, nil},
		{"/pub/~/commit/HEAD", 404, nil, nil},
		{"/pub/~/commit/0000000000", 404, nil, nil},
		{"/pub/~/log?page=99", 404, nil, nil},
		{"/pub/~/unknown", 404, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			is := is.New(t)
			w := f.get(c.path, nil)
			is.Equal(w.Code, c.code)
			body := w.Body.String()
			for _, s := range c.want {
				if !strings.Contains(body, s) {
					t.Errorf("%s: body lacks %q", c.path, s)
				}
			}
			for _, s := range c.not {
				if strings.Contains(body, s) {
					t.Errorf("%s: body has %q", c.path, s)
				}
			}
		})
	}
}

func TestUIRaw(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)

	w := f.get("/pub/~/raw/main.go", nil)
	is.Equal(w.Code, http.StatusOK)
	is.Equal(w.Header().Get("Content-Type"), "text/plain; charset=utf-8")
	is.True(strings.HasPrefix(w.Header().Get("Content-Security-Policy"), "sandbox"))
	is.Equal(w.Body.String(), "package main\n\nfunc main() { println(\"hi <b>\") }\n")

	w = f.get("/pub/~/raw/bin.dat", nil)
	is.Equal(w.Code, http.StatusOK)
	is.Equal(w.Header().Get("Content-Type"), "application/octet-stream")
	is.True(strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment"))
	is.Equal(w.Body.String(), "\x00\x01\x02binary")

	is.Equal(f.get("/pub/~/raw/docs", nil).Code, http.StatusNotFound)
}

// TestUIUnlisted verifies an unlisted repository is left off the front
// page but can be browsed and cloned over HTTP by anyone with its name.
func TestUIUnlisted(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)

	is.True(!strings.Contains(f.get("/", nil).Body.String(), "/hid"))
	is.Equal(f.get("/hid", nil).Code, http.StatusOK)
	is.Equal(f.get("/hid/~/blob/main.go", nil).Code, http.StatusOK)

	w := f.get("/hid.git/info/refs?service=git-upload-pack", nil)
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "refs/heads/main"))

	// go-get finds it too.
	is.True(strings.Contains(f.get("/hid?go-get=1", nil).Body.String(), "go-import"))
}

// TestUIPrivateAccess verifies the owner and a read-only collaborator can
// browse a private repository with a session, and a stranger cannot.
func TestUIPrivateAccess(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)

	owner := f.login(t, f.owner)
	for _, p := range []string{"/secret", "/secret/~/tree", "/secret/~/blob/main.go", "/secret/~/raw/main.go", "/secret/~/log", "/secret/~/refs", "/secret/~/commit/" + f.head} {
		is.Equal(f.get(p, owner).Code, http.StatusOK)
	}

	stranger := f.login(t, f.stranger)
	is.Equal(f.get("/secret", stranger).Code, http.StatusNotFound)

	ownerCtx := proto.WithUserContext(f.ctx, f.owner)
	is.NoErr(f.be.AddCollaborator(ownerCtx, "secret", "stranger", access.ReadOnlyAccess))
	is.Equal(f.get("/secret", stranger).Code, http.StatusOK)
	is.True(strings.Contains(f.get("/", stranger).Body.String(), `href="/secret"`))
}

// TestUIKeylessOff verifies anonymous visitors see nothing when the server
// does not allow keyless access, the same as git over HTTP.
func TestUIKeylessOff(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	is.NoErr(f.be.SetAllowKeyless(f.ctx, false))

	is.True(!strings.Contains(f.get("/", nil).Body.String(), `href="/pub"`))
	is.Equal(f.get("/pub", nil).Code, http.StatusNotFound)
	is.Equal(f.get("/pub", f.login(t, f.stranger)).Code, http.StatusOK)
}

// TestUIDisabled verifies the web UI can be turned off.
func TestUIDisabled(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	f.cfg.Web.Enabled = false
	h := NewRouter(f.ctx)

	for _, p := range []string{"/", "/pub", "/pub/~/tree", "/~/style.css"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, p, nil))
		is.Equal(w.Code, http.StatusNotFound)
		is.True(!strings.Contains(w.Body.String(), "<html"))
	}

	// Git over HTTP still works.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/pub.git/info/refs?service=git-upload-pack", nil))
	is.Equal(w.Code, http.StatusOK)
}

func TestSplitUIPath(t *testing.T) {
	is := is.New(t)
	for _, c := range []struct {
		in   string
		repo string
		sub  string
		ok   bool
	}{
		{"/", "", "", false},
		{"/pub", "pub", "", false},
		{"/org/nested", "org/nested", "", false},
		{"/~/login", "", "login", true},
		{"/pub/~/tree/a/~/b", "pub", "tree/a/~/b", true},
		{"/org/nested/~", "org/nested", "", true},
		{"/a~b/c", "a~b/c", "", false},
	} {
		repo, sub, ok := splitUIPath(c.in)
		is.Equal(repo, c.repo)
		is.Equal(strings.Join(sub, "/"), c.sub)
		is.Equal(ok, c.ok)
	}
}
