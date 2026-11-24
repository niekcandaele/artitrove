package render

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var (
	tagRe       = regexp.MustCompile(`<(/?)([a-z0-9]+)((?:\s+[a-z]+(?:="[^"<>]*")?)*)\s*>`)
	attrValueRe = regexp.MustCompile(`="[^"]*"`)
	attrRe      = regexp.MustCompile(`\s+([a-z]+)(?:="([^"<>]*)")?`)
	backstopRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<\s*/?\s*(script|style|iframe|svg|object|embed)`),
		regexp.MustCompile(`(?i)[\s/"']on[a-z]+\s*=`),
		regexp.MustCompile(`(?i)[\s/"']style\s*=`),
	}
	allowedTags = setOf("p a img em strong del code pre span h1 h2 h3 h4 h5 h6 ul ol li blockquote table thead tbody tr th td hr br input details summary div")
	allowedAttr = setOf("href src alt title class align checked disabled type open start")
)

func setOf(s string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(s) {
		m[f] = true
	}
	return m
}

// unsafeReason returns why s is not a safe fragment, or "".
func unsafeReason(s string) string {
	if !utf8.ValidString(s) {
		return "invalid utf-8"
	}
	rest := strings.ReplaceAll(s, "<!-- raw HTML omitted -->", "")
	for _, m := range tagRe.FindAllStringSubmatch(rest, -1) {
		if !allowedTags[m[2]] {
			return "tag " + m[2]
		}
		for _, a := range attrRe.FindAllStringSubmatch(m[3], -1) {
			if !allowedAttr[a[1]] {
				return "attribute " + a[1]
			}
			v := a[2]
			if a[1] == "type" && v != "checkbox" {
				return "input type " + v
			}
			if a[1] == "href" || a[1] == "src" {
				if dangerous(html.UnescapeString(v)) {
					return "url " + v
				}
			}
		}
	}
	// Backstops look at markup only: escaped text may legitimately say "onerror=".
	markup := attrValueRe.ReplaceAllString(strings.Join(tagRe.FindAllString(rest, -1), ""), `=""`)
	rest = tagRe.ReplaceAllString(rest, "")
	if strings.ContainsAny(rest, "<>") {
		return "stray angle bracket"
	}
	for _, re := range backstopRes {
		if re.MatchString(markup) {
			return "backstop " + re.String()
		}
	}
	return ""
}

func assertSafe(t testing.TB, s string) {
	t.Helper()
	if r := unsafeReason(s); r != "" {
		t.Fatalf("unsafe output (%s): %s", r, s)
	}
}

func TestAssertSafeSelf(t *testing.T) {
	for _, bad := range []string{
		`<img/src/onerror=x>`, `<a href=javascript:x>`, `<a href="javascript:x">`, `<script`,
		`<p style="x">`, `<div onclick="x">`, `<a href="&#106;avascript:x">`, "a\xff",
	} {
		if unsafeReason(bad) == "" {
			t.Errorf("accepted %q", bad)
		}
	}
	good := "<table><tr><td align=\"right\">x</td></tr></table>\n<ul><li><input checked=\"\" disabled=\"\" type=\"checkbox\"> a</li></ul>\n" +
		`<pre class="hl-chroma"><code><span class="hl-k">func</span></code></pre>`
	if r := unsafeReason(good); r != "" {
		t.Errorf("rejected good fragment: %s", r)
	}
}

var markdownCorpus = []string{
	"<script>alert(1)</script>", "<img src=x onerror=alert(1)>", "text <img src=x onerror=alert(1)> text",
	"<img/src=x/onerror=alert(1)>", "<svg onload=alert(1)>", "<iframe src=javascript:alert(1)>",
	"<style>a{}</style>", `<a href="javascript:alert(1)">x</a>`, "<!-- <script> -->",
	"[x](javascript:alert(1))", "[x](JaVaScRiPt:alert(1))", "[x](<java\tscript:alert(1)>)", "[x](< javascript:alert(1)>)",
	"[x](&#106;avascript:alert(1))", "[x](javascript&#58;alert(1))", "[x](&#x6A;avascript&colon;alert(1))",
	"[x](vbscript:alert(1))", "[x](file:///etc/passwd)", "[x](data:text/html;base64,PHNjcmlwdD4=)",
	"[x](data:image/png;base64,AAAA)", "[x][r]\n\n[r]: javascript:alert(1)", "<javascript:alert(1)>",
	"[a](javascript:1) [b](javascript:2) [c](javascript:3)", "*[a](javascript:1)*", "**x [a](javascript:1) [b](javascript:2)**",
	"|h|\n|-|\n|[a](javascript:1)|", "- [a](javascript:1)\n- b", "- [ ] [a](javascript:1)",
	"[![a](javascript:1)](javascript:2)", "![[x](javascript:1)](y.png)",
	`[x](https://e "t\" onmouseover=\"alert(1)")`, "[x](https://e/?q=<script>)", `# h {onclick="alert(1)" style="x" id="y"}`,
	"![a](javascript:alert(1))", "![a](data:image/svg+xml,<svg onload=alert(1)>)", "![a](https://evil/x.png)", "![](https://evil/x.png)",
	"![a](//evil/x.png)", `![a](/\evil/x.png)`, `![a](\\\\evil/x.png)`, "![a](< //evil/x.png>)",
	"![a](img/x.png)", "![a](/a/ID/raw)", `![a" onerror="alert(1)](x.png)`, "![**b**](x.png)", "![[x](https://e)](y.png)",
	"[![a](https://evil/x.png)](https://o)", "[![a](img.png)](https://o)", `![a](x.png "t\" onerror=\"x")`,
	"```html\n<script>alert(1)</script>\n```", "```\"><script>\ncode\n```", "```nosuchlang\n<b>\n```", "```\n<b>\n```",
	"```go {style=\"x\"}\nfunc main() {}\n```", "    <script>x</script>", "`<script>`", "\xff\xfe<script>",
	"www.example.com", "https://x.example/<script>", "a@b.example", "- [x] done",
	"|a|b|\n|:-:|--:|\n|1|2|", "~~x~~", "> <b>q</b>", "[x]: <javascript:1>\n[x]",
}

func TestMarkdownCorpusSafe(t *testing.T) {
	for _, src := range markdownCorpus {
		out, err := Markdown([]byte(src))
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if r := unsafeReason(string(out)); r != "" {
			t.Errorf("%q -> %s: %s", src, r, out)
		}
	}
}

func FuzzMarkdown(f *testing.F) {
	for _, s := range markdownCorpus {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		out, err := Markdown(src)
		if err != nil {
			t.Fatal(err)
		}
		assertSafe(t, string(out))
	})
}

var jsonCorpus = []string{
	`{"<script>":"<img src=x onerror=alert(1)>"}`, `"javascript:alert(1)"`, "{\"a\xff\":\"b\xfe\"}", `"\ud800"`, `"\u0000"`,
	`{"b":1,"a":2,"b":3}`, `[1.0,1e400,-0,12345678901234567890123,0.1000]`, `{"a":{"b":[],"c":{}}}`, `[true,false,null]`,
	`{"a\"b":"x\ny"}`, `{"a":}`, `{} {}`, ``,
}

func TestJSONCorpusSafe(t *testing.T) {
	for _, src := range jsonCorpus {
		out, ok := JSON([]byte(src))
		if ok != json.Valid([]byte(src)) {
			t.Errorf("%q: ok=%v", src, ok)
		}
		assertSafe(t, string(out))
	}
}

func FuzzJSON(f *testing.F) {
	for _, s := range jsonCorpus {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		out, ok := JSON(src)
		if ok != json.Valid(src) {
			t.Fatalf("ok=%v", ok)
		}
		assertSafe(t, string(out))
	})
}
