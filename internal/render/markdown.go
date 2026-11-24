package render

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"regexp"
	"runtime"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Every page view renders anew, and goldmark is quadratic on crafted input such as "[a](" or
// ">" repeated, while chroma lexers can take about 1 ms per byte on crafted code. So the work per
// view is bounded: the source size, the total highlighted code, and the concurrent renders.
const (
	maxMarkdownSize = 64 << 10
	highlightBudget = 8 << 10
)

// ErrTooLarge means the source is over the size that is rendered; callers show it as text.
var ErrTooLarge = errors.New("markdown source too large to render")

// tableWrap gives every table a scroll container; "<table>" can only come from the table renderer
// because raw HTML is off and text is escaped.
var tableWrap = strings.NewReplacer("<table>", `<div class="tbl"><table>`, "</table>", "</table></div>")

var renderSlots = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2))

// The extensions are listed one by one: extension.GFM would register a second table renderer
// that emits style= attributes. Attribute syntax and auto heading IDs stay off, so
// "{onclick=...}" remains text and headings get no id (no DOM clobbering).
func newMarkdown(budget int) goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
			extension.Strikethrough,
			extension.Linkify,
			extension.TaskList,
		),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(linkSafety{}, 100))),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(&nodeRenderer{budget: budget}, 100))),
	)
}

// Markdown renders CommonMark + GFM to an HTML fragment that is safe to embed in an app page:
//   - Raw HTML is dropped (no html.WithUnsafe).
//   - Unsafe link schemes are stripped: only http:, https:, mailto: and relative URLs are
//     kept, so javascript:, vbscript:, file:, data: and custom protocol handlers are dropped.
//   - Tables use extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute); the
//     default emits style=, which the page CSP blocks.
//   - Fenced code is highlighted with chroma using CSS classes only: html.WithClasses(true),
//     HighlightClassPrefix, and a <pre class="hl-chroma"> wrapper. An unknown or absent
//     language gives an escaped plain <pre><code>. If highlighting fails, the block falls
//     back to the escaped plain <pre><code>. Once a document has had 8 KiB of code highlighted,
//     later blocks stay plain. Sources over 64 KiB are refused with ErrTooLarge.
//   - An <img> is emitted only when the destination is a same-origin relative URL (no scheme
//     and not starting with "//"). Any other image is rendered as <a href="dest">alt text</a>
//     (or dest when alt is empty), because the page CSP blocks external and data: images.
//     Inside a link such an image renders its alt text only.
//     The scheme stripping above still applies.
//
// The output contains no style attributes and no script. Errors: ErrTooLarge, and those from
// goldmark's Convert. At most GOMAXPROCS/2 renders run at once; others wait, and return ctx.Err()
// if ctx ends first, so a client that went away does not leave queued work behind.
func Markdown(ctx context.Context, src []byte) (template.HTML, error) {
	if len(src) > maxMarkdownSize {
		return "", ErrTooLarge
	}
	select {
	case renderSlots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-renderSlots }()
	src = bytes.ToValidUTF8(src, []byte("�"))
	var buf bytes.Buffer
	if err := newMarkdown(highlightBudget).Convert(src, &buf); err != nil {
		return "", err
	}
	return template.HTML(tableWrap.Replace(buf.String())), nil //nolint:gosec // raw HTML is disabled and links are filtered
}

// emittedURL is the href or src goldmark writes for a raw AST destination, before attribute
// escaping (which the browser undoes). Checking it rather than a separate decoding means the
// checks see exactly what the browser parses: entities and backslash escapes are resolved, and
// spaces, controls, backslashes and non-ASCII are percent-encoded, so none can hide a scheme or
// turn a path into "//host".
func emittedURL(dest []byte) string {
	return string(util.URLEscape(dest, true))
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// dangerous allows only relative URLs and a few schemes: besides javascript:, vbscript:,
// file: and data:, custom handlers such as ms-msdt: or vscode: can launch local programs.
func dangerous(u string) bool {
	switch strings.ToLower(schemeRe.FindString(u)) {
	case "", "http:", "https:", "mailto:":
		return false
	}
	return true
}

func sameOriginRelative(u string) bool {
	return !schemeRe.MatchString(u) && !strings.HasPrefix(u, "//")
}

// linkSafety removes links and images with dangerous destinations, keeping their text.
type linkSafety struct{}

func (linkSafety) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var bad []ast.Node
	// Collect first: changing the tree during ast.Walk would skip siblings.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Link:
			if dangerous(emittedURL(n.Destination)) {
				bad = append(bad, n)
			}
		case *ast.Image:
			if dangerous(emittedURL(n.Destination)) {
				bad = append(bad, n)
			}
		case *ast.AutoLink:
			if dangerous(emittedURL(n.URL(source))) {
				bad = append(bad, n)
			}
		}
		return ast.WalkContinue, nil
	})
	// Pre-order puts descendants after ancestors, so reversing handles the innermost first.
	for i := len(bad) - 1; i >= 0; i-- {
		node := bad[i]
		parent := node.Parent()
		if al, ok := node.(*ast.AutoLink); ok {
			label := ast.NewString(al.Label(source))
			label.SetRaw(true) // autolink labels are literal: no entities or backslash escapes
			parent.ReplaceChild(parent, node, label)
			continue
		}
		for c := node.FirstChild(); c != nil; {
			next := c.NextSibling()
			parent.InsertBefore(parent, node, c)
			c = next
		}
		parent.RemoveChild(parent, node)
	}
}

// nodeRenderer overrides the default image and fenced code renderers.
// budget is the number of code bytes still allowed to be highlighted in this document.
type nodeRenderer struct{ budget int }

func (r *nodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, renderImage)
	reg.Register(ast.KindFencedCodeBlock, r.renderFenced)
}

func plainText(n ast.Node, source []byte) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			sb.Write(c.Segment.Value(source))
			if c.SoftLineBreak() || c.HardLineBreak() {
				sb.WriteByte(' ')
			}
		case *ast.String:
			sb.Write(c.Value)
		case *ast.AutoLink:
			sb.Write(c.Label(source))
		default:
			sb.WriteString(plainText(c, source))
		}
	}
	return sb.String()
}

func hasLinkAncestor(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if _, ok := p.(*ast.Link); ok {
			return true
		}
	}
	return false
}

func renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	dest := emittedURL(n.Destination)
	alt := []byte(plainText(n, source))
	href := string(util.EscapeHTML([]byte(dest)))
	// Alt and title are raw source; goldmark's writer resolves entities and backslash escapes
	// and then escapes, as the default renderer does.
	switch {
	case sameOriginRelative(dest):
		_, _ = w.WriteString(`<img src="` + href + `" alt="`)
		gmhtml.DefaultWriter.Write(w, alt)
		_ = w.WriteByte('"')
		if n.Title != nil {
			_, _ = w.WriteString(` title="`)
			gmhtml.DefaultWriter.Write(w, n.Title)
			_ = w.WriteByte('"')
		}
		_, _ = w.WriteString(">")
	default:
		inLink := hasLinkAncestor(n)
		if !inLink {
			_, _ = w.WriteString(`<a href="` + href + `">`)
		}
		if len(alt) == 0 {
			_, _ = w.Write(util.EscapeHTML([]byte(dest)))
		} else {
			gmhtml.DefaultWriter.Write(w, alt)
		}
		if !inLink {
			_, _ = w.WriteString("</a>")
		}
	}
	return ast.WalkSkipChildren, nil
}

func (r *nodeRenderer) renderFenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	var code bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		code.Write(seg.Value(source))
	}
	if lexer := lookupLexer(string(n.Language(source))); lexer != nil && code.Len() <= r.budget {
		r.budget -= code.Len()
		if highlighted, ok := highlight(lexer, code.String()); ok {
			_, _ = w.WriteString(highlighted)
			return ast.WalkSkipChildren, nil
		}
	}
	_, _ = w.WriteString("<pre><code>")
	_, _ = w.Write(util.EscapeHTML(code.Bytes()))
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// lookupLexer returns nil for an absent or unknown language.
func lookupLexer(lang string) chroma.Lexer {
	if lang == "" {
		return nil
	}
	return lexers.Get(lang)
}

// highlight reports false when highlighting fails, e.g. on a regexp timeout; the caller then
// shows the plain escaped block.
func highlight(lexer chroma.Lexer, code string) (string, bool) {
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return "", false
	}
	var buf strings.Builder
	if err := formatter.Format(&buf, styleLight, it); err != nil {
		return "", false
	}
	return buf.String() + "\n", true
}
