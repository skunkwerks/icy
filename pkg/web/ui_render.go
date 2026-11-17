package web

import (
	"bytes"
	"html/template"
	"path"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// codeFormatter renders highlighted code with CSS classes, so the page
// needs no inline styles, and with line numbers that can be linked to as
// #L<n>.
var codeFormatter = chtml.New(
	chtml.WithClasses(true),
	chtml.WithAllClasses(true),
	chtml.WithLineNumbers(true),
	chtml.WithLinkableLineNumbers(true, "L"),
	chtml.TabWidth(8),
)

// markdown renders READMEs. Raw HTML in the source is dropped and links
// with dangerous schemes such as javascript: are removed, which is
// goldmark's default without html.WithUnsafe.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// chromaCSS returns the highlighting styles for the light theme and, under
// prefers-color-scheme, the dark one.
func chromaCSS() []byte {
	var b bytes.Buffer
	b.WriteString("\n/* syntax highlighting: light */\n")
	codeFormatter.WriteCSS(&b, styleOr("github", styles.Fallback)) //nolint:errcheck
	b.WriteString("\n/* syntax highlighting: dark */\n@media (prefers-color-scheme: dark) {\n")
	codeFormatter.WriteCSS(&b, styleOr("github-dark", styles.Get("monokai"))) //nolint:errcheck
	b.WriteString("}\n")
	return b.Bytes()
}

func styleOr(name string, fallback *chroma.Style) *chroma.Style {
	if s, ok := styles.Registry[name]; ok {
		return s
	}
	return fallback
}

// highlight renders source code as HTML. chroma escapes every token.
func highlight(name, src string) (template.HTML, error) {
	lexer := lexers.Match(path.Base(name))
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := codeFormatter.Format(&b, styles.Fallback, it); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil //nolint:gosec
}

// isMarkdown reports whether a file name looks like markdown.
func isMarkdown(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd", ".mkdn":
		return true
	}
	return false
}

// renderReadme renders a README as markdown, or as plain text when it is
// not markdown.
func renderReadme(name, src string) template.HTML {
	if isMarkdown(name) {
		var b bytes.Buffer
		if err := markdown.Convert([]byte(src), &b); err == nil {
			return template.HTML(b.String()) //nolint:gosec
		}
	}
	return template.HTML("<pre>" + template.HTMLEscapeString(src) + "</pre>") //nolint:gosec
}
