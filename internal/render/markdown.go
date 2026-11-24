package render

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The extensions are listed one by one: extension.GFM would register a second table renderer
// that emits style= attributes. Attribute syntax and auto heading IDs stay off, so
// "{onclick=...}" remains text and headings get no id (no DOM clobbering).
var (
	mdHighlighted = newMarkdown(true)
	mdPlain       = newMarkdown(false)
)

// maxHighlightSize bounds the Markdown source that gets syntax highlighting: chroma output is
// about 15 times its input and takes about 2 µs per byte, and every page view renders anew.
const maxHighlightSize = 512 << 10

func newMarkdown(highlight bool) goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
			extension.Strikethrough,
			extension.Linkify,
			extension.TaskList,
		),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(linkSafety{}, 100))),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(nodeRenderer{highlight}, 100))),
	)
}

// Markdown renders CommonMark + GFM to an HTML fragment that is safe to embed in an app page:
//   - Raw HTML is dropped (no html.WithUnsafe).
//   - javascript:, vbscript:, file: and data: link schemes are stripped.
//   - Tables use extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute); the
//     default emits style=, which the page CSP blocks.
//   - Fenced code is highlighted with chroma using CSS classes only: html.WithClasses(true),
//     HighlightClassPrefix, and a <pre class="hl-chroma"> wrapper. An unknown or absent
//     language gives an escaped plain <pre><code>. If highlighting fails, the block falls
//     back to the escaped plain <pre><code>. Sources over 512 KiB are not highlighted at all.
//   - An <img> is emitted only when the destination is a same-origin relative URL (no scheme
//     and not starting with "//"). Any other image is rendered as <a href="dest">alt text</a>
//     (or dest when alt is empty), because the page CSP blocks external and data: images.
//     Inside a link such an image renders its alt text only.
//     The scheme stripping above still applies.
//
// The output contains no style attributes and no script. Errors: only those from goldmark's Convert.
func Markdown(src []byte) (template.HTML, error) {
	src = bytes.ToValidUTF8(src, []byte("�"))
	md := mdHighlighted
	if len(src) > maxHighlightSize {
		md = mdPlain
	}
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil //nolint:gosec // raw HTML is disabled and links are filtered
}

// normalizeURL mimics what browsers ignore before parsing a URL: tabs and newlines anywhere,
// and leading control characters and spaces.
func normalizeURL(dest string) string {
	dest = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(dest)
	return strings.TrimLeftFunc(dest, func(r rune) bool { return r <= 0x20 })
}

// decodeDest resolves what goldmark's renderer would: the AST keeps destinations raw, so
// "&#106;avascript:" is still an entity here.
func decodeDest(dest []byte) string {
	return string(util.UnescapePunctuations(util.ResolveNumericReferences(util.ResolveEntityNames(dest))))
}

func dangerous(dest string) bool {
	u := strings.ToLower(normalizeURL(dest))
	for _, p := range []string{"javascript:", "vbscript:", "file:", "data:"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// sameOriginRelative is stricter than "//": browsers treat a backslash as a slash.
func sameOriginRelative(dest string) bool {
	u := normalizeURL(dest)
	if schemeRe.MatchString(u) {
		return false
	}
	return len(u) < 2 || !strings.ContainsRune("/\\", rune(u[0])) || !strings.ContainsRune("/\\", rune(u[1]))
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
			if dangerous(decodeDest(n.Destination)) {
				bad = append(bad, n)
			}
		case *ast.Image:
			if dangerous(decodeDest(n.Destination)) {
				bad = append(bad, n)
			}
		case *ast.AutoLink:
			if dangerous(decodeDest(n.URL(source))) {
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
			parent.ReplaceChild(parent, node, ast.NewString(al.Label(source)))
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
type nodeRenderer struct{ highlight bool }

func (r nodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, renderImage)
	reg.Register(ast.KindFencedCodeBlock, r.renderFenced)
}

func plainText(n ast.Node, source []byte) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			sb.Write(c.Segment.Value(source))
		case *ast.String:
			sb.Write(c.Value)
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
	dest := decodeDest(n.Destination)
	alt := plainText(n, source)
	href := string(util.EscapeHTML(util.URLEscape(n.Destination, true)))
	switch {
	case sameOriginRelative(dest):
		_, _ = w.WriteString(`<img src="` + href + `" alt="` + string(util.EscapeHTML([]byte(alt))) + `"`)
		if n.Title != nil {
			_, _ = w.WriteString(` title="` + string(util.EscapeHTML(n.Title)) + `"`)
		}
		_, _ = w.WriteString(">")
	default:
		label := alt
		if label == "" {
			label = dest
		}
		label = string(util.EscapeHTML([]byte(label)))
		if hasLinkAncestor(n) {
			_, _ = w.WriteString(label)
		} else {
			_, _ = w.WriteString(`<a href="` + href + `">` + label + `</a>`)
		}
	}
	return ast.WalkSkipChildren, nil
}

func (r nodeRenderer) renderFenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	var code bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		code.Write(seg.Value(source))
	}
	if r.highlight {
		if highlighted, ok := highlight(string(n.Language(source)), code.String()); ok {
			_, _ = w.WriteString(highlighted)
			return ast.WalkSkipChildren, nil
		}
	}
	_, _ = w.WriteString("<pre><code>")
	_, _ = w.Write(util.EscapeHTML(code.Bytes()))
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// highlight reports false when the language is unknown or highlighting fails, e.g. on a
// regexp timeout; the caller then shows the plain escaped block.
func highlight(lang, code string) (string, bool) {
	if lang == "" {
		return "", false
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return "", false
	}
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
