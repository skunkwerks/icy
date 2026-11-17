package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/matryer/is"
)

// uiAnswer is everything about a web UI response a caller could compare
// between two repository names.
type uiAnswer struct {
	code    int
	body    string
	headers string
}

func uiAnswerOf(w *httptest.ResponseRecorder) uiAnswer {
	var hdr []string
	for _, k := range []string{"Content-Type", "Content-Length", "Cache-Control", "Content-Security-Policy", "Content-Disposition", "Location", "Set-Cookie", "Vary", "WWW-Authenticate"} {
		hdr = append(hdr, k+": "+w.Header().Get(k))
	}
	return uiAnswer{code: w.Code, body: w.Body.String(), headers: strings.Join(hdr, "\n")}
}

// uiOraclePaths are every web UI page of a repository, with %s for its
// name. The ids and paths exist in the private repository, so a page that
// looked inside it before checking access would answer differently.
func uiOraclePaths(head string) []string {
	return []string{
		"/%s",
		"/%s/",
		"/%s.git",
		"/%s/tree",
		"/%s/TREE",
		"/%s/tree/docs",
		"/%s/tree/log",
		"/%s/blob/log/tree.txt",
		"/%s/tree?h=main",
		"/%s/tree?h=v1",
		"/%s/blob/main.go",
		"/%s/blob/main.go?h=" + head,
		"/%s/raw/main.go",
		"/%s/raw/bin.dat",
		"/%s/log",
		"/%s/log?page=2",
		"/%s/commit/" + head,
		"/%s/commit/" + head[:8],
		"/%s/refs",
		"/%s/nope",
		"/%s/sub/tree",
		"/%s/tree/tree/x",
		"/%s/-/login",
	}
}

// ownerSkips are oracle paths that do not exist even for the owner.
var ownerSkips = []string{"nope", "/sub/", "page=2", "tree/tree/x", "/-/"}

// TestUIPrivateLooksMissing checks that, for every page and every kind of
// caller who cannot read it, a private repository gets byte-for-byte the
// same answer as a repository that does not exist.
func TestUIPrivateLooksMissing(t *testing.T) {
	f := newUIFixture(t)

	callers := map[string]*http.Cookie{
		"anonymous": nil,
		"stranger":  f.login(t, f.stranger),
		"bad cookie": {
			Name:  sessionCookie,
			Value: "not-a-token",
		},
	}

	for who, cookie := range callers {
		for _, p := range uiOraclePaths(f.head) {
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
				t.Run(fmt.Sprintf("%s %s %s", who, method, p), func(t *testing.T) {
					is := is.New(t)
					private := uiAnswerOf(f.do(method, fmt.Sprintf(p, "secret"), cookie))
					missing := uiAnswerOf(f.do(method, fmt.Sprintf(p, "ghost"), cookie))
					is.Equal(private, missing)
					is.True(private.code != http.StatusOK)
					is.True(!strings.Contains(private.body, "secret"))
					is.True(!strings.Contains(private.body, "ghost"))
				})
			}
		}
	}

	// The same pages are there for the owner, so the paths above are real.
	owner := f.login(t, f.owner)
	for _, p := range uiOraclePaths(f.head) {
		if slices.ContainsFunc(ownerSkips, func(s string) bool { return strings.Contains(p, s) }) {
			continue
		}
		if got := f.get(fmt.Sprintf(p, "secret"), owner).Code; got != http.StatusOK {
			t.Errorf("owner %s: got %d", p, got)
		}
	}
}

// TestUIPrivateLooksMissingWhenNested covers names under a public
// repository's name and names next to a private one.
func TestUIPrivateLooksMissingWhenNested(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	ownerCtx := f.ctx
	_, err := f.be.CreateRepository(ownerCtx, "org/private", f.owner, privateOpts())
	is.NoErr(err)

	for _, p := range []string{"/org/%s", "/org/%s/tree", "/org/%s/refs", "/org/%s/tree/tree/x", "/org/%s/Log"} {
		private := uiAnswerOf(f.get(fmt.Sprintf(p, "private"), nil))
		missing := uiAnswerOf(f.get(fmt.Sprintf(p, "missing"), nil))
		is.Equal(private, missing)
		is.Equal(private.code, http.StatusNotFound)
	}
}

// TestUIFrontPageDoesNotListHidden checks the front page never mentions a
// private or unlisted repository to someone who should not see it.
func TestUIFrontPageDoesNotListHidden(t *testing.T) {
	is := is.New(t)
	f := newUIFixture(t)
	for _, c := range []*http.Cookie{nil, f.login(t, f.stranger)} {
		body := f.get("/", c).Body.String()
		is.True(!strings.Contains(body, "secret"))
		is.True(!strings.Contains(body, "/hid"))
	}
}
