package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"strings"
)

type jsonNode struct {
	kind     string // object, array, str, num, lit
	text     string // escaped scalar text
	key      string // escaped re-encoded key, empty when none
	hasKey   bool
	children []*jsonNode
}

// JSON renders src as a collapsible tree inside one <div class="json-tree">. Objects and
// arrays use nested <details open><summary>. Spans use the classes key, str, num, lit
// (true/false/null) and dim (counts), meaningful only under .json-tree. Key order, literal
// number text and duplicate keys are preserved. All text is escaped. It returns ("", false)
// when json.Valid(src) is false, which also bounds the nesting depth.
//
// Strings and keys are shown in re-encoded JSON form, so quotes, newlines and control
// characters are visible; bidi override characters stay raw. There is no size or depth limit
// beyond json.Valid; very deep trees may be flattened by the browser.
func JSON(src []byte) (html template.HTML, ok bool) {
	if !json.Valid(src) {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	root := parseJSON(dec, nil)
	var sb strings.Builder
	sb.WriteString(`<div class="json-tree">`)
	writeJSON(&sb, root)
	sb.WriteString("</div>")
	return template.HTML(sb.String()), true //nolint:gosec // every piece is escaped above
}

func encodeString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic(err)
	}
	return template.HTMLEscapeString(strings.TrimSuffix(buf.String(), "\n"))
}

func mustToken(dec *json.Decoder) json.Token {
	tok, err := dec.Token()
	if err != nil {
		panic(fmt.Sprintf("render: token error after json.Valid: %v", err))
	}
	return tok
}

// parseJSON reads one value; key is the already-read key it belongs to, if any.
func parseJSON(dec *json.Decoder, key *string) *jsonNode {
	n := &jsonNode{}
	if key != nil {
		n.key, n.hasKey = encodeString(*key), true
	}
	switch tok := mustToken(dec).(type) {
	case json.Delim:
		if tok == '{' {
			n.kind = "object"
			for dec.More() {
				k := mustToken(dec).(string)
				n.children = append(n.children, parseJSON(dec, &k))
			}
		} else {
			n.kind = "array"
			for dec.More() {
				n.children = append(n.children, parseJSON(dec, nil))
			}
		}
		mustToken(dec)
	case string:
		n.kind, n.text = "str", encodeString(tok)
	case json.Number:
		n.kind, n.text = "num", template.HTMLEscapeString(tok.String())
	case bool:
		n.kind, n.text = "lit", fmt.Sprint(tok)
	default: // nil is the only token left
		n.kind, n.text = "lit", "null"
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func writeJSON(w io.StringWriter, n *jsonNode) {
	keyHTML := ""
	if n.hasKey {
		keyHTML = `<span class="key">` + n.key + `</span>: `
	}
	if (n.kind == "object" || n.kind == "array") && len(n.children) > 0 {
		open, closing, count := "{", "}", plural(len(n.children), "key", "keys")
		if n.kind == "array" {
			open, closing, count = "[", "]", plural(len(n.children), "item", "items")
		}
		_, _ = w.WriteString("<details open><summary>" + keyHTML + open + ` <span class="dim">` + count + "</span></summary>")
		for _, c := range n.children {
			writeJSON(w, c)
		}
		_, _ = w.WriteString(closing + "</details>")
		return
	}
	var value string
	switch n.kind {
	case "object":
		value = "{}"
	case "array":
		value = "[]"
	default:
		value = `<span class="` + n.kind + `">` + n.text + "</span>"
	}
	_, _ = w.WriteString("<div>" + keyHTML + value + "</div>")
}
