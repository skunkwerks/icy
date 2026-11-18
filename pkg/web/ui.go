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
	"github.com/skunkwerks/icy/pkg/version"
)

// The web UI is a read-only, server-rendered repository browser.
//
// Routes. The first path segment that is one of utils.ReservedRepoSegments
// ends the repository name, in any letter case. No new repository may
// contain a reserved segment, so the split is decided by the path alone and
// never by which repositories exist:
//
//	GET  /                            front page: server info and listed repos
//	GET  /-/style.css                 stylesheet
//	GET  /-/favicon.svg               icons, also favicon.ico and apple-touch-icon.png
//	GET  /-/login?token=T             swap a sign-in token for a session cookie
//	POST /-/logout                    end the session
//	GET  /{repo}                      summary, README and clone URLs
//	GET  /{repo}/tree/{path}?h=REV    directory listing
//	GET  /{repo}/blob/{path}?h=REV    file with syntax highlighting
//	GET  /{repo}/raw/{path}?h=REV     file contents as they are
//	GET  /{repo}/log?h=REV&page=N     commit log
//	GET  /{repo}/commit/{id}          one commit with its diff
//	GET  /{repo}/refs                 branches and tags
//
// REV is a branch, a tag or a commit id, and defaults to HEAD.
//
// The web UI is registered after the git, LFS and go-get routes. A path
// shaped like a git request, such as /{repo}/info/refs, stays with git, so
// a repository created before its name was reserved, such as docs/log, can
// still be cloned with or without ".git". The cost is that a file whose
// path ends like a git request (HEAD, info/refs, objects/info/packs, ...)
// cannot be shown in the tree, blob or raw views.

// serverSegment is the reserved segment that holds the server's own pages.
const serverSegment = "-"

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

// register adds the web UI as the last route before the final not-found
// one. It takes every GET or HEAD that is not a go-get request, and any
// method on a path with a reserved segment, so a POST to a web page gets
// the web UI's answer.
func (ui *webUI) register(r *mux.Router) {
	r.MatcherFunc(func(req *http.Request, _ *mux.RouteMatch) bool {
		if isGoGet(req) {
			return false
		}
		_, _, _, reserved := splitUIPath(req.URL.Path)
		return isRead(req) || reserved
	}).Handler(ui)
}

// splitUIPath splits a request path at its first reserved segment into the
// repository name before it, the page the segment names, and the rest of
// the path. ok is false when there is no reserved segment, and the whole
// path is then the repository name.
func splitUIPath(p string) (repo, page, rest string, ok bool) {
	trimmed := strings.Trim(p, "/")
	segs := strings.Split(trimmed, "/")
	for i, s := range segs {
		if word, reserved := utils.ReservedSegment(s); reserved {
			return strings.Join(segs[:i], "/"), word, strings.Join(segs[i+1:], "/"), true
		}
	}
	return trimmed, "", "", false
}

// ServeHTTP implements http.Handler.
func (ui *webUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setUIHeaders(w)

	user, _ := ui.session(r)
	r = r.WithContext(proto.WithUserContext(r.Context(), user))

	name, page, rest, _ := splitUIPath(r.URL.Path)
	if name == "" {
		ui.serveGlobal(w, r, page, rest)
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

// serveGlobal serves the pages that belong to no repository: the front
// page at / and the server pages under /-/.
func (ui *webUI) serveGlobal(w http.ResponseWriter, r *http.Request, segment, page string) {
	switch {
	case segment == "" && isRead(r):
		ui.home(w, r)
		return
	case segment != serverSegment:
		ui.renderNotFound(w, r)
		return
	}

	switch {
	case page == "style.css" && isRead(r):
		ui.style(w, r)
	case brandFiles[page] != "" && isRead(r):
		ui.brand(w, r, page)
	case page == "login" && isRead(r):
		ui.login(w, r)
	case page == "logout" && r.Method == http.MethodPost:
		ui.logout(w, r)
	case page == "style.css" || page == "login" || page == "logout" || brandFiles[page] != "":
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
//
// A name with a reserved segment is refused before any lookup. The URL
// parser never produces one, and an existing repository created before its
// name was reserved has no web URL that would not mean another page.
func readableForWeb(ctx context.Context, name string) proto.Repository {
	be := backend.FromContext(ctx)
	user := proto.UserFromContext(ctx)
	if utils.HasReservedSegment(name) || (user == nil && !be.AllowKeyless(ctx)) {
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

// brandFiles are the icons under ui/brand and their content types. They
// are built by branding/build_brand.
var brandFiles = map[string]string{
	"favicon.svg":          "image/svg+xml",
	"favicon.ico":          "image/x-icon",
	"apple-touch-icon.png": "image/png",
}

// brand serves one of the icons.
func (ui *webUI) brand(w http.ResponseWriter, _ *http.Request, name string) {
	data, err := fs.ReadFile(uiFS, "ui/brand/"+name)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", brandFiles[name])
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data) //nolint:errcheck
}

// siteInfo is the server information every page shows.
type siteInfo struct {
	Name        string
	Owner       string
	Description string
	LoginCmd    string
	CSSVersion  string
	// Version is the git version icy was built from. Only the front
	// page sets it, so it shows in that page's header.
	Version string
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
	Unlisted    bool
	Updated     time.Time
}

// home renders the front page with every repository the caller can read
// and proto.Visibility.Listed lets them see.
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
			// Names with a reserved segment have no web URL, see
			// readableForWeb.
			vis := proto.VisibilityOf(repo)
			if !vis.Listed(user != nil) || utils.HasReservedSegment(repo.Name()) {
				continue
			}
			if rr, _ := be.Readable(ctx, repo.Name(), user); rr == nil {
				continue
			}
			rows = append(rows, homeRepo{
				Name:        repo.Name(),
				Description: utils.Sanitize(repo.Description()),
				Private:     repo.IsPrivate(),
				Unlisted:    vis == proto.VisibilityUnlisted,
				Updated:     repo.UpdatedAt(),
			})
		}
	}

	p := ui.newPage(r, "")
	p.Site.Version = version.Version
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
	u := repoURL(name) + "/" + page
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
