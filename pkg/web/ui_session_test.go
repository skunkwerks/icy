package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/matryer/is"
	"github.com/skunkwerks/icy/pkg/proto"
)

func privateOpts() proto.RepositoryOptions {
	return proto.RepositoryOptions{Private: true}
}

// TestUILoginIsSingleUse checks a sign-in link sets a session once, and
// that a used, unknown or expired link all get the same refusal.
func TestUILoginIsSingleUse(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)

	token, err := f.be.CreateWebLogin(f.ctx, f.owner, "/secret", time.Minute)
	is.NoErr(err)
	link := "/-/login?token=" + url.QueryEscape(token)

	w := f.get(link, nil)
	is.Equal(w.Code, http.StatusSeeOther)
	is.Equal(w.Header().Get("Location"), "/secret")
	is.Equal(w.Header().Get("Cache-Control"), "no-store")
	c := sessionCookieOf(w)
	is.True(c != nil)
	is.True(c.HttpOnly)
	is.Equal(c.SameSite, http.SameSiteLaxMode)
	is.Equal(c.Path, "/")
	is.True(!c.Secure) // the test server is plain http
	is.True(c.MaxAge > 0 && c.MaxAge <= int(sessionTTL.Seconds()))

	// The cookie works.
	is.Equal(f.get("/secret", &http.Cookie{Name: c.Name, Value: c.Value}).Code, http.StatusOK)

	// The link does not work twice.
	used := uiAnswerOf(f.get(link, nil))
	is.Equal(used.code, http.StatusBadRequest)
	is.True(sessionCookieOf(f.get(link, nil)) == nil)

	// Unknown and empty tokens get the same answer as a used one.
	is.Equal(uiAnswerOf(f.get("/-/login?token=icy_login_nope", nil)), used)
	is.Equal(uiAnswerOf(f.get("/-/login", nil)), used)

	// An expired token gets it too.
	expired, err := f.be.CreateWebLogin(f.ctx, f.owner, "/", time.Millisecond)
	is.NoErr(err)
	time.Sleep(5 * time.Millisecond)
	is.Equal(uiAnswerOf(f.get("/-/login?token="+url.QueryEscape(expired), nil)), used)

	// Only GET signs in.
	is.Equal(f.do(http.MethodPost, link, nil).Code, http.StatusMethodNotAllowed)
}

// TestUILoginSecureCookie checks the cookie is Secure when the public URL
// is https, as it is behind a TLS-terminating proxy.
func TestUILoginSecureCookie(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	f.cfg.HTTP.PublicURL = "https://git.example.org"

	token, err := f.be.CreateWebLogin(f.ctx, f.owner, "/", time.Minute)
	is.NoErr(err)
	c := sessionCookieOf(f.get("/-/login?token="+url.QueryEscape(token), nil))
	is.True(c != nil)
	is.True(c.Secure)
}

// TestUILogout checks signing out revokes the session on the server, not
// just in the browser.
func TestUILogout(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	c := f.login(t, f.owner)
	is.Equal(f.get("/secret", c).Code, http.StatusOK)

	// A cross-site form cannot sign the user out.
	w := f.do(http.MethodPost, "/-/logout", c, "Origin", "https://evil.example")
	is.Equal(w.Code, http.StatusForbidden)
	is.Equal(f.get("/secret", c).Code, http.StatusOK)

	// Signing out needs POST.
	is.Equal(f.get("/-/logout", c).Code, http.StatusMethodNotAllowed)

	w = f.do(http.MethodPost, "/-/logout", c, "Origin", "http://localhost:23232")
	is.Equal(w.Code, http.StatusSeeOther)
	cleared := sessionCookieOf(w)
	is.True(cleared != nil)
	is.True(cleared.MaxAge < 0)

	// The old cookie value no longer works, even if the browser kept it.
	is.Equal(f.get("/secret", c).Code, http.StatusNotFound)
	is.True(!strings.Contains(f.get("/", c).Body.String(), "Sign out"))
}

// TestUISessionTokenIsNotABearerToken checks a session cookie value cannot
// be used as a git bearer token, and a git bearer token cannot be used as
// a session.
func TestUISessionTokenIsNotABearerToken(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	c := f.login(t, f.owner)

	w := f.do(http.MethodGet, "/secret.git/info/refs?service=git-upload-pack", nil, "Authorization", "Bearer "+c.Value)
	is.Equal(w.Code, http.StatusForbidden)

	bearer := mintJWT(f.ctx, t, f.owner, "secret")
	w = f.do(http.MethodGet, "/secret.git/info/refs?service=git-upload-pack", nil, "Authorization", "Bearer "+bearer)
	is.Equal(w.Code, http.StatusOK)
	is.Equal(f.get("/secret", &http.Cookie{Name: sessionCookie, Value: bearer}).Code, http.StatusNotFound)

	// A session is tied to the public URL that issued it.
	f.cfg.HTTP.PublicURL = "http://elsewhere.example"
	is.Equal(f.get("/secret", c).Code, http.StatusNotFound)
}

// TestUISessionEndsOnRestart checks a new server instance does not accept
// sessions minted by the old one, so in-memory sign-outs cannot be undone
// by a restart.
func TestUISessionEndsOnRestart(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	c := f.login(t, f.owner)
	is.Equal(f.get("/secret", c).Code, http.StatusOK)

	f.handler = NewRouter(f.ctx)
	is.Equal(f.get("/secret", c).Code, http.StatusNotFound)
}

// TestUISessionEndsWithUser checks a session stops working when its user
// is deleted or renamed.
func TestUISessionEndsWithUser(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	c := f.login(t, f.stranger)
	is.True(strings.Contains(f.get("/", c).Body.String(), "stranger"))

	is.NoErr(f.be.SetUsername(f.ctx, "stranger", "renamed"))
	is.True(!strings.Contains(f.get("/", c).Body.String(), "Sign out"))
}

func TestSafeNext(t *testing.T) {
	is := is.New(t)
	for in, want := range map[string]string{
		"/":                  "/",
		"/org/repo":          "/org/repo",
		"":                   "/",
		"//evil.example":     "/",
		"https://evil":       "/",
		"/\\evil":            "/",
		"/a\r\nSet-Cookie:x": "/",
	} {
		is.Equal(safeNext(in), want)
	}
}

func TestCleanPath(t *testing.T) {
	is := is.New(t)
	for in, ok := range map[string]bool{
		"":           true,
		"a/b":        true,
		"a/b/":       true,
		"../x":       false,
		"a/../../x":  false,
		"a/./b":      false,
		"a//b":       false,
		"docs/guide": true,
	} {
		_, got := cleanPath(in)
		is.Equal(got, ok)
	}
}

func TestLogURLRedactsToken(t *testing.T) {
	is := is.New(t)
	u, err := url.Parse("/-/login?token=icy_login_secret")
	is.NoErr(err)
	is.True(!strings.Contains(logURL(u), "icy_login_secret"))
	u, err = url.Parse("/repo.git/info/refs?service=git-upload-pack")
	is.NoErr(err)
	is.Equal(logURL(u), "/repo.git/info/refs?service=git-upload-pack")
}
