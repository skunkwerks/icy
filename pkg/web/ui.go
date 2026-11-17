package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/mux"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/utils"
)

// The web UI is a read-only, server-rendered repository browser.
//
// Routes. "~" can never appear in a repository name (see
// utils.ValidateRepo), so a "~" path segment always marks where the
// repository name ends and cannot collide with a git, LFS or go-get path:
//
//	GET  /                              front page: server info and listed repos
//	GET  /~/style.css                   stylesheet
//	GET  /~/login?token=T               swap a sign-in token for a session cookie
//	POST /~/logout                      end the session
//	GET  /{repo}                        summary, README and clone URLs
//	GET  /{repo}/~/tree/{path}?h=REV    directory listing
//	GET  /{repo}/~/blob/{path}?h=REV    file with syntax highlighting
//	GET  /{repo}/~/raw/{path}?h=REV     file contents as they are
//	GET  /{repo}/~/log?h=REV&page=N     commit log
//	GET  /{repo}/~/commit/{id}          one commit with its diff
//	GET  /{repo}/~/refs                 branches and tags
//
// REV is a branch, a tag or a commit id, and defaults to HEAD. The summary
// route is registered after the git routes, so /{repo}/info/refs and friends
// keep their meaning, and it skips ?go-get=1 requests.

// uiSep is the path segment that ends a repository name in web UI routes.
const uiSep = "~"

//go:embed ui
var uiFS embed.FS

// webUI serves the web UI.
type webUI struct {
	// epoch prefixes every session id this instance mints.
	epoch   string
	revoked *revocations
	css     []byte
	cssVer  string
	pages   map[string]*template.Template
}

// uiPages are the page templates, each rendered inside ui/layout.html.
var uiPages = []string{"home", "summary", "tree", "blob", "log", "commit", "refs", "error"}

func newWebUI() *webUI {
	css, err := fs.ReadFile(uiFS, "ui/style.css")
	if err != nil {
		panic(err)
	}
	css = append(css, chromaCSS()...)
	sum := sha256.Sum256(css)

	ui := &webUI{
		epoch:   rand.Text()[:10],
		revoked: newRevocations(),
		css:     css,
		cssVer:  hex.EncodeToString(sum[:4]),
		pages:   make(map[string]*template.Template, len(uiPages)),
	}
	for _, p := range uiPages {
		ui.pages[p] = template.Must(template.New(p).Funcs(uiFuncs).ParseFS(uiFS, "ui/layout.html", "ui/"+p+".html"))
	}
	return ui
}

func isRead(r *http.Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

// registerFront registers the routes that must win over the git routes:
// the front page and every path with a "~" segment.
func (ui *webUI) registerFront(r *mux.Router) {
	r.MatcherFunc(func(req *http.Request, _ *mux.RouteMatch) bool {
		return req.URL.Path == "/" && isRead(req) && !isGoGet(req)
	}).Handler(ui)
	r.MatcherFunc(func(req *http.Request, _ *mux.RouteMatch) bool {
		_, _, ok := splitUIPath(req.URL.Path)
		return ok
	}).Handler(ui)
}

// registerSummary registers the repository summary route. It goes after
// the git and go-get routes so it only sees what they leave.
func (ui *webUI) registerSummary(r *mux.Router) {
	r.MatcherFunc(func(req *http.Request, _ *mux.RouteMatch) bool {
		return isRead(req) && !isGoGet(req)
	}).Handler(ui)
}

// splitUIPath splits a request path at its first "~" segment into the
// repository name before it and the segments after it. ok is false when
// there is no "~" segment.
func splitUIPath(p string) (repo string, sub []string, ok bool) {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		if s == uiSep {
			return strings.Join(segs[:i], "/"), segs[i+1:], true
		}
	}
	return strings.Trim(p, "/"), nil, false
}

// ServeHTTP implements http.Handler.
func (ui *webUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setUIHeaders(w)

	user, _ := ui.session(r)
	r = r.WithContext(proto.WithUserContext(r.Context(), user))

	name, sub, hasSep := splitUIPath(r.URL.Path)
	if name == "" {
		ui.serveGlobal(w, r, sub, hasSep)
		return
	}

	// Every check below runs the same way for a repository the caller
	// cannot read and for one that does not exist, so the two give the
	// same answer. Nothing opens the repository before readable says yes.
	if !isRead(r) {
		ui.renderError(w, r, http.StatusMethodNotAllowed, "This page only answers GET requests.")
		return
	}

	repo := readableForWeb(r.Context(), name)
	if repo == nil {
		ui.renderNotFound(w, r)
		return
	}

	page := ""
	if len(sub) > 0 {
		page, sub = sub[0], sub[1:]
	}
	rest := strings.Join(sub, "/")

	rc := &repoCtx{ui: ui, w: w, r: r, repo: repo, rev: r.URL.Query().Get("h")}
	switch page {
	case "":
		rc.summary()
	case "tree":
		rc.tree(rest)
	case "blob":
		rc.blob(rest)
	case "raw":
		rc.raw(rest)
	case "log":
		rc.log()
	case "commit":
		rc.commit(rest)
	case "refs":
		rc.refs()
	default:
		ui.renderNotFound(w, r)
	}
}

// serveGlobal serves the pages that belong to no repository.
func (ui *webUI) serveGlobal(w http.ResponseWriter, r *http.Request, sub []string, hasSep bool) {
	if !hasSep {
		ui.home(w, r)
		return
	}

	page := strings.Join(sub, "/")
	switch {
	case page == "style.css" && isRead(r):
		ui.style(w, r)
	case page == "login" && isRead(r):
		ui.login(w, r)
	case page == "logout" && r.Method == http.MethodPost:
		ui.logout(w, r)
	case page == "style.css" || page == "login" || page == "logout":
		ui.renderError(w, r, http.StatusMethodNotAllowed, "This page does not answer that request method.")
	default:
		ui.renderNotFound(w, r)
	}
}

// readableForWeb returns the repository if the caller in ctx may read it,
// using the same access checks as git over HTTP. Anonymous callers are
// refused when the server does not allow keyless access, the same as for
// git over HTTP. The result is nil for a missing repository and for one the
// caller may not read alike.
func readableForWeb(ctx context.Context, name string) proto.Repository {
	be := backend.FromContext(ctx)
	user := proto.UserFromContext(ctx)
	if user == nil && !be.AllowKeyless(ctx) {
		return nil
	}
	repo, _ := be.Readable(ctx, name, user)
	return repo
}

// setUIHeaders sets the headers every web UI response carries.
func setUIHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Vary", "Cookie")
}

// style serves the stylesheet.
func (ui *webUI) style(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if r.URL.Query().Get("v") == ui.cssVer {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	w.Write(ui.css) //nolint:errcheck
}

// siteInfo is the server information every page shows.
type siteInfo struct {
	Name        string
	Owner       string
	Description string
	LoginCmd    string
	CSSVersion  string
}

// pageData is what every page template receives.
type pageData struct {
	Site  siteInfo
	User  string
	Title string
	Repo  *repoView
	Tab   string
	Body  any
}

func (ui *webUI) newPage(r *http.Request, title string) *pageData {
	ctx := r.Context()
	cfg := config.FromContext(ctx)
	p := &pageData{
		Site: siteInfo{
			Name:        cfg.Name,
			Owner:       cfg.Web.Owner,
			Description: cfg.Web.Description,
			LoginCmd:    sshCommand(cfg) + " web login",
			CSSVersion:  ui.cssVer,
		},
		Title: title,
	}
	if user := proto.UserFromContext(ctx); user != nil {
		p.User = user.Username()
	}
	return p
}

// sshCommand returns the ssh command line that reaches the server.
func sshCommand(cfg *config.Config) string {
	host, port := "localhost", ""
	if u, err := url.Parse(cfg.SSH.PublicURL); err == nil && u.Hostname() != "" {
		host, port = u.Hostname(), u.Port()
	}
	if port != "" && port != "22" {
		return "ssh -p " + port + " " + host
	}
	return "ssh " + host
}

// render executes a page template into a buffer and writes it with status.
func (ui *webUI) render(w http.ResponseWriter, r *http.Request, status int, page string, data *pageData) {
	var buf bytes.Buffer
	if err := ui.pages[page].ExecuteTemplate(&buf, "layout", data); err != nil {
		log.FromContext(r.Context()).Error("failed to render web page", "page", page, "err", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		renderInternalServerError(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes()) //nolint:errcheck
}

// renderError renders a plain message page.
func (ui *webUI) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	p := ui.newPage(r, http.StatusText(status))
	p.Body = struct {
		Status  int
		Heading string
		Message string
	}{status, http.StatusText(status), msg}
	ui.render(w, r, status, "error", p)
}

// renderNotFound is the one answer for anything missing or unreadable. It
// never names what was asked for.
func (ui *webUI) renderNotFound(w http.ResponseWriter, r *http.Request) {
	ui.renderError(w, r, http.StatusNotFound, "There is nothing here, or you are not allowed to see it.")
}

// homeRepo is one row of the front page list.
type homeRepo struct {
	Name        string
	Description string
	Private     bool
	Updated     time.Time
}

// home renders the front page with every listed repository the caller can
// read. Unlisted (hidden) repositories are never listed.
func (ui *webUI) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	be := backend.FromContext(ctx)
	user := proto.UserFromContext(ctx)

	var rows []homeRepo
	if user != nil || be.AllowKeyless(ctx) {
		repos, err := be.Repositories(ctx)
		if err != nil {
			log.FromContext(ctx).Error("failed to list repositories", "err", err)
			ui.renderError(w, r, http.StatusInternalServerError, "The repository list is not available right now.")
			return
		}
		for _, repo := range repos {
			if repo.IsHidden() {
				continue
			}
			if rr, _ := be.Readable(ctx, repo.Name(), user); rr == nil {
				continue
			}
			rows = append(rows, homeRepo{
				Name:        repo.Name(),
				Description: utils.Sanitize(repo.Description()),
				Private:     repo.IsPrivate(),
				Updated:     repo.UpdatedAt(),
			})
		}
	}

	p := ui.newPage(r, "")
	p.Body = struct{ Repos []homeRepo }{rows}
	ui.render(w, r, http.StatusOK, "home", p)
}

// uiFuncs are the template helpers.
var uiFuncs = template.FuncMap{
	"repoURL": repoURL,
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return humanize.Time(t)
	},
	"date": func(t time.Time) string {
		return t.UTC().Format("2006-01-02 15:04 UTC")
	},
	"iso": func(t time.Time) string {
		return t.UTC().Format(time.RFC3339)
	},
}

// escapePath escapes each segment of a slash-separated path.
func escapePath(p string) string {
	return (&url.URL{Path: p}).EscapedPath()
}

// repoURL is the summary page of a repository.
func repoURL(name string) string {
	return "/" + escapePath(name)
}

// pageURL is a web UI page of a repository. rev is added as ?h= unless
// empty.
func pageURL(name, page, p, rev string) string {
	u := repoURL(name) + "/" + uiSep + "/" + page
	if p != "" {
		u += "/" + escapePath(p)
	}
	if rev != "" {
		u += "?h=" + url.QueryEscape(rev)
	}
	return u
}

// httpCloneURL is the https (or http) clone URL of a repository.
func httpCloneURL(cfg *config.Config, name string) string {
	return cfg.HTTP.PublicURL + "/" + name + ".git"
}

// sshCloneURL is the ssh clone URL of a repository, in the short scp form
// when the server listens on port 22.
func sshCloneURL(cfg *config.Config, name string) string {
	u, err := url.Parse(cfg.SSH.PublicURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Sprintf("%s/%s.git", cfg.SSH.PublicURL, name)
	}
	if port := u.Port(); port != "" && port != "22" {
		return fmt.Sprintf("ssh://%s:%s/%s.git", u.Hostname(), port, name)
	}
	return fmt.Sprintf("git@%s:%s.git", u.Hostname(), name)
}
