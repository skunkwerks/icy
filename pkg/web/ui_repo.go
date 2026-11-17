package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
	gitm "github.com/aymanbagabas/git-module"
	"github.com/dustin/go-humanize"
	"github.com/skunkwerks/icy/git"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/config"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/utils"
)

const (
	// logPageSize is the number of commits on a log page.
	logPageSize = 50
	// summaryCommits is the number of commits on the summary page.
	summaryCommits = 5
	// maxHighlightSize is the largest file shown in the blob view.
	maxHighlightSize = 1 << 20
	// maxLogPage keeps the log offset in a sane range.
	maxLogPage = 1 << 20
)

// hexRev matches what is accepted as a commit id in ?h= and in commit URLs.
var hexRev = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// errNoRev is returned when a revision does not resolve to a commit.
var errNoRev = errors.New("revision not found")

// repoView is the repository header every repository page shows.
type repoView struct {
	Name        string
	ProjectName string
	Description string
	Visibility  string
	Mirror      bool
	HTTPURL     string
	SSHURL      string
	// Rev is the ?h= value links carry on, empty for the default branch.
	Rev string
}

// TabURL returns the URL of a repository tab.
func (v *repoView) TabURL(tab string) string {
	switch tab {
	case "summary":
		return repoURL(v.Name)
	case "tree", "log":
		return pageURL(v.Name, tab, "", v.Rev)
	default:
		return pageURL(v.Name, tab, "", "")
	}
}

// repoCtx carries one request for a repository page. The caller has
// already been checked for read access.
type repoCtx struct {
	ui   *webUI
	w    http.ResponseWriter
	r    *http.Request
	repo proto.Repository
	rev  string
	gr   *git.Repository
}

func (c *repoCtx) cfg() *config.Config {
	return config.FromContext(c.r.Context())
}

func (c *repoCtx) logger() *log.Logger {
	return log.FromContext(c.r.Context())
}

func (c *repoCtx) notFound() {
	c.ui.renderNotFound(c.w, c.r)
}

func (c *repoCtx) internalError(err error) {
	c.logger().Error("web ui", "repo", c.repo.Name(), "err", err)
	c.ui.renderError(c.w, c.r, http.StatusInternalServerError, "Something went wrong reading this repository.")
}

// open opens the git repository. It reports false after answering the
// request itself.
func (c *repoCtx) open() bool {
	gr, err := c.repo.Open()
	if err != nil {
		c.internalError(err)
		return false
	}
	c.gr = gr
	return true
}

func (c *repoCtx) page(title, tab string) *pageData {
	cfg := c.cfg()
	name := c.repo.Name()
	p := c.ui.newPage(c.r, title)
	p.Tab = tab
	p.Repo = &repoView{
		Name:        name,
		ProjectName: utils.Sanitize(c.repo.ProjectName()),
		Description: utils.Sanitize(c.repo.Description()),
		Visibility:  string(proto.VisibilityOf(c.repo)),
		Mirror:      c.repo.IsMirror(),
		HTTPURL:     httpCloneURL(cfg, name),
		Rev:         c.rev,
	}
	if cfg.SSH.Enabled {
		p.Repo.SSHURL = sshCloneURL(cfg, name)
	}
	return p
}

// resolve turns the ?h= value into a full commit id. Only branch names, tag
// names and hex ids are accepted, and only a hex id ever reaches git on the
// command line, so the value cannot be read as an option.
func (c *repoCtx) resolve() (string, error) {
	h := c.rev
	if h == "" {
		head, err := c.gr.HEAD()
		if err != nil {
			return "", errNoRev
		}
		return peel(c.gr, head.ID)
	}

	refs, err := c.gr.References()
	if err == nil {
		for _, prefix := range []string{git.RefsHeads, git.RefsTags} {
			for _, ref := range refs {
				if ref.Refspec == prefix+h {
					return peel(c.gr, ref.ID)
				}
			}
		}
	}

	return peel(c.gr, h)
}

// peel resolves a hex object id to the commit it names.
func peel(gr *git.Repository, id string) (string, error) {
	if !hexRev.MatchString(id) {
		return "", errNoRev
	}
	full, err := gr.RevParse(id + "^{commit}")
	if err != nil || !hexRev.MatchString(full) {
		return "", errNoRev
	}
	return full, nil
}

// crumb is one step of a path breadcrumb.
type crumb struct {
	Name string
	URL  string
}

// crumbs builds the breadcrumb for p, each step linking to its tree page.
func (c *repoCtx) crumbs(p string) []crumb {
	name := c.repo.Name()
	out := []crumb{{Name: name, URL: pageURL(name, "tree", "", c.rev)}}
	if p == "" {
		return out
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		out = append(out, crumb{Name: s, URL: pageURL(name, "tree", strings.Join(segs[:i+1], "/"), c.rev)})
	}
	return out
}

// cleanPath normalises a path inside the repository. ok is false for paths
// that try to leave it.
func cleanPath(p string) (string, bool) {
	if p == "" {
		return "", true
	}
	clean := path.Clean("/" + p)[1:]
	if clean != strings.Trim(p, "/") {
		return "", false
	}
	return clean, true
}

// commitRow is a commit in a list.
type commitRow struct {
	ID      string
	Short   string
	Summary string
	Author  string
	When    time.Time
	URL     string
}

func (c *repoCtx) commitRow(cm *git.Commit) commitRow {
	id := cm.ID.String()
	row := commitRow{
		ID:      id,
		Short:   shortID(id),
		Summary: utils.Sanitize(cm.Summary()),
		URL:     pageURL(c.repo.Name(), "commit", id, ""),
	}
	if cm.Author != nil {
		row.Author = utils.Sanitize(cm.Author.Name)
		row.When = cm.Author.When
	}
	return row
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// summary renders the repository front page.
func (c *repoCtx) summary() {
	if !c.open() {
		return
	}

	body := struct {
		Empty      bool
		Branch     string
		Readme     template.HTML
		ReadmeName string
		Commits    []commitRow
		LogURL     string
	}{LogURL: pageURL(c.repo.Name(), "log", "", c.rev)}

	id, err := c.resolve()
	switch {
	case err != nil && c.rev != "":
		c.notFound()
		return
	case err != nil:
		body.Empty = true
	default:
		if head, err := c.gr.HEAD(); err == nil {
			body.Branch = head.Name().Short()
		}
		if c.rev != "" {
			body.Branch = c.rev
		}

		commits, err := c.gr.Repository.CommitsByPage(id, 1, summaryCommits)
		if err != nil {
			c.internalError(err)
			return
		}
		for _, cm := range commits {
			body.Commits = append(body.Commits, c.commitRow(cm))
		}

		ref := &git.Reference{Reference: &gitm.Reference{ID: id, Refspec: id}}
		if readme, rpath, err := backend.Readme(c.repo, ref); err == nil && rpath != "" {
			body.ReadmeName = rpath
			body.Readme = renderReadme(rpath, readme)
		}
	}

	p := c.page("", "summary")
	p.Body = body
	c.ui.render(c.w, c.r, http.StatusOK, "summary", p)
}

// treeRow is one entry of a directory listing.
type treeRow struct {
	Name   string
	URL    string
	Mode   string
	Size   string
	IsDir  bool
	IsLink bool
}

// tree renders a directory listing. A path that names a file shows the
// file instead.
func (c *repoCtx) tree(p string) {
	p, ok := cleanPath(p)
	if !ok || !c.open() {
		if !ok {
			c.notFound()
		}
		return
	}

	id, err := c.resolve()
	if err != nil {
		c.notFound()
		return
	}
	root, err := c.gr.LsTree(id)
	if err != nil {
		c.notFound()
		return
	}

	tree := root
	if p != "" {
		te, err := root.TreeEntry(p)
		if err != nil {
			c.notFound()
			return
		}
		if !te.IsTree() {
			c.blob(p)
			return
		}
		if tree, err = root.SubTree(p); err != nil {
			c.notFound()
			return
		}
	}

	ents, err := tree.Entries()
	if err != nil {
		c.internalError(err)
		return
	}
	ents.Sort()

	name := c.repo.Name()
	rows := make([]treeRow, 0, len(ents))
	for _, e := range ents {
		full := path.Join(p, e.Name())
		row := treeRow{Name: e.Name(), Mode: e.Mode().String()}
		switch {
		case e.IsTree():
			row.IsDir = true
			row.URL = pageURL(name, "tree", full, c.rev)
		case e.IsCommit():
			row.Mode = "submodule"
		default:
			row.IsLink = e.IsSymlink()
			row.URL = pageURL(name, "blob", full, c.rev)
			row.Size = humanize.IBytes(uint64(e.Size())) //nolint:gosec
		}
		rows = append(rows, row)
	}

	pg := c.page(p, "tree")
	pg.Body = struct {
		Crumbs  []crumb
		Entries []treeRow
	}{c.crumbs(p), rows}
	c.ui.render(c.w, c.r, http.StatusOK, "tree", pg)
}

// fileAt resolves a file path at the current revision.
func (c *repoCtx) fileAt(p string) (*git.TreeEntry, bool) {
	p, ok := cleanPath(p)
	if !ok || p == "" {
		c.notFound()
		return nil, false
	}
	if c.gr == nil && !c.open() {
		return nil, false
	}
	id, err := c.resolve()
	if err != nil {
		c.notFound()
		return nil, false
	}
	root, err := c.gr.LsTree(id)
	if err != nil {
		c.notFound()
		return nil, false
	}
	te, err := root.TreeEntry(p)
	if err != nil || te.IsTree() || te.IsCommit() {
		c.notFound()
		return nil, false
	}
	return te, true
}

// blob renders one file with syntax highlighting.
func (c *repoCtx) blob(p string) {
	te, ok := c.fileAt(p)
	if !ok {
		return
	}
	p, _ = cleanPath(p)

	body := struct {
		Crumbs   []crumb
		Size     string
		RawURL   string
		Binary   bool
		TooLarge bool
		Code     template.HTML
	}{
		Crumbs: c.crumbs(p),
		Size:   humanize.IBytes(uint64(te.Size())), //nolint:gosec
		RawURL: pageURL(c.repo.Name(), "raw", p, c.rev),
	}

	if te.Size() > maxHighlightSize {
		body.TooLarge = true
	} else {
		data, err := te.Contents()
		if err != nil {
			c.internalError(err)
			return
		}
		if bin, _ := git.IsBinary(bytes.NewReader(data)); bin {
			body.Binary = true
		} else {
			code, err := highlight(p, string(data))
			if err != nil {
				c.internalError(err)
				return
			}
			body.Code = code
		}
	}

	pg := c.page(p, "tree")
	pg.Body = body
	c.ui.render(c.w, c.r, http.StatusOK, "blob", pg)
}

// raw serves a file as it is. Text is sent as text/plain and anything else
// as a download, both under a sandboxing policy, so a file in a repository
// can never run as a page on this site.
func (c *repoCtx) raw(p string) {
	te, ok := c.fileAt(p)
	if !ok {
		return
	}

	sw := &sniffWriter{w: c.w, name: te.Name(), size: te.Size()}
	if err := te.File().Pipeline(sw, io.Discard); err != nil {
		if !sw.started {
			c.internalError(err)
			return
		}
		c.logger().Error("web ui raw", "repo", c.repo.Name(), "err", err)
		return
	}
	sw.flush()
}

// sniffWriter holds back the start of a file until it can tell text from
// binary, then sets the content type and streams the rest.
type sniffWriter struct {
	w       http.ResponseWriter
	name    string
	size    int64
	buf     []byte
	started bool
}

const sniffLen = 8000

func (s *sniffWriter) Write(p []byte) (int, error) {
	if s.started {
		return s.w.Write(p)
	}
	s.buf = append(s.buf, p...)
	if len(s.buf) >= sniffLen {
		if err := s.flushErr(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (s *sniffWriter) flush() {
	if !s.started {
		s.flushErr() //nolint:errcheck
	}
}

func (s *sniffWriter) flushErr() error {
	s.started = true
	h := s.w.Header()
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Content-Length", strconv.FormatInt(s.size, 10))
	if bin, _ := git.IsBinary(bytes.NewReader(s.buf)); bin {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(s.name))
	} else {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	s.w.WriteHeader(http.StatusOK)
	_, err := s.w.Write(s.buf)
	s.buf = nil
	return err
}

// log renders a page of the commit log.
func (c *repoCtx) log() {
	if !c.open() {
		return
	}
	id, err := c.resolve()
	if err != nil {
		c.notFound()
		return
	}

	page, _ := strconv.Atoi(c.r.URL.Query().Get("page"))
	if page < 1 || page > maxLogPage {
		page = 1
	}

	// Ask for one more than a page to learn whether there is a next one.
	commits, err := c.gr.Log(id, gitm.LogOptions{MaxCount: logPageSize + 1, Skip: (page - 1) * logPageSize})
	if err != nil {
		c.internalError(err)
		return
	}
	more := len(commits) > logPageSize
	if more {
		commits = commits[:logPageSize]
	}
	if len(commits) == 0 && page > 1 {
		c.notFound()
		return
	}

	rows := make([]commitRow, 0, len(commits))
	for _, cm := range commits {
		rows = append(rows, c.commitRow(cm))
	}

	base := pageURL(c.repo.Name(), "log", "", c.rev)
	pageLink := func(n int) string {
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		return base + sep + "page=" + strconv.Itoa(n)
	}
	body := struct {
		Commits []commitRow
		Page    int
		PrevURL string
		NextURL string
	}{Commits: rows, Page: page}
	if page > 1 {
		body.PrevURL = pageLink(page - 1)
	}
	if more {
		body.NextURL = pageLink(page + 1)
	}

	pg := c.page("log", "log")
	pg.Body = body
	c.ui.render(c.w, c.r, http.StatusOK, "log", pg)
}

// diffFile is one file of a commit's diff.
type diffFile struct {
	Name    string
	OldName string
	Status  string
	Add     int
	Del     int
	Binary  bool
	Anchor  string
	Lines   []diffLine
}

// diffLine is one line of a diff with its CSS class.
type diffLine struct {
	Class string
	Text  string
}

// commit renders one commit and its diff.
func (c *repoCtx) commit(id string) {
	if !hexRev.MatchString(id) || !c.open() {
		if !hexRev.MatchString(id) {
			c.notFound()
		}
		return
	}
	full, err := peel(c.gr, id)
	if err != nil {
		c.notFound()
		return
	}
	cm, err := c.gr.CatFileCommit(full)
	if err != nil {
		c.notFound()
		return
	}
	diff, err := c.gr.Diff(cm)
	if err != nil {
		c.internalError(err)
		return
	}

	name := c.repo.Name()
	type parent struct{ Short, URL string }
	body := struct {
		ID        string
		Author    string
		Committer string
		When      time.Time
		Message   string
		Parents   []parent
		TreeURL   string
		Files     []diffFile
		Additions int
		Deletions int
	}{
		ID:        full,
		Message:   strings.ReplaceAll(utils.Sanitize(cm.Message), "\r\n", "\n"),
		TreeURL:   pageURL(name, "tree", "", full),
		Additions: diff.TotalAdditions(),
		Deletions: diff.TotalDeletions(),
	}
	if cm.Author != nil {
		body.Author = utils.Sanitize(cm.Author.Name)
		body.When = cm.Author.When
	}
	if cm.Committer != nil {
		body.Committer = utils.Sanitize(cm.Committer.Name)
	}
	for i := range cm.ParentsCount() {
		if pid, err := cm.ParentID(i); err == nil {
			body.Parents = append(body.Parents, parent{shortID(pid.String()), pageURL(name, "commit", pid.String(), "")})
		}
	}

	for i, f := range diff.Files {
		df := diffFile{
			Name:   f.Name,
			Add:    f.NumAdditions(),
			Del:    f.NumDeletions(),
			Binary: f.IsBinary(),
			Anchor: fmt.Sprintf("f%d", i),
		}
		switch f.Type {
		case gitm.DiffFileAdd:
			df.Status = "added"
		case gitm.DiffFileDelete:
			df.Status = "deleted"
		case gitm.DiffFileRename:
			df.Status = "renamed"
			df.OldName = f.OldName()
		default:
			df.Status = "modified"
		}
		for _, s := range f.Sections {
			for _, l := range s.Lines {
				dl := diffLine{Text: l.Content}
				switch l.Type {
				case gitm.DiffLineAdd:
					dl.Class = "add"
				case gitm.DiffLineDelete:
					dl.Class = "del"
				case gitm.DiffLineSection:
					dl.Class = "hunk"
				}
				df.Lines = append(df.Lines, dl)
			}
		}
		body.Files = append(body.Files, df)
	}

	pg := c.page(shortID(full), "log")
	pg.Body = body
	c.ui.render(c.w, c.r, http.StatusOK, "commit", pg)
}

// refRow is a branch or a tag.
type refRow struct {
	Name    string
	Short   string
	TreeURL string
	LogURL  string
	IsHead  bool
}

// refs renders the branches and tags.
func (c *repoCtx) refs() {
	if !c.open() {
		return
	}
	refs, err := c.gr.References()
	if err != nil {
		// An empty repository has no refs and show-ref fails.
		refs = nil
	}
	var head string
	if h, err := c.gr.HEAD(); err == nil {
		head = h.Refspec
	}

	name := c.repo.Name()
	var branches, tags []refRow
	for _, ref := range refs {
		short := ref.Name().Short()
		row := refRow{
			Name:    short,
			Short:   shortID(ref.ID),
			TreeURL: pageURL(name, "tree", "", short),
			LogURL:  pageURL(name, "log", "", short),
			IsHead:  ref.Refspec == head,
		}
		switch {
		case ref.IsBranch():
			branches = append(branches, row)
		case ref.IsTag():
			tags = append(tags, row)
		}
	}

	pg := c.page("refs", "refs")
	pg.Body = struct{ Branches, Tags []refRow }{branches, tags}
	c.ui.render(c.w, c.r, http.StatusOK, "refs", pg)
}
