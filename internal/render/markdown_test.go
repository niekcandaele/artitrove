package render

import (
	"strings"
	"sync"
	"testing"
)

func md1(t *testing.T, src string) string {
	t.Helper()
	out, err := Markdown([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	assertSafe(t, string(out))
	return string(out)
}

func TestMarkdownUnsafeSchemesKeepText(t *testing.T) {
	for _, src := range []string{
		"[x](javascript:alert(1))", "[x](JaVaScRiPt:alert(1))", "[x](&#106;avascript:alert(1))", "[x](javascript&#58;alert(1))",
		"[x](vbscript:1)", "[x](file:///etc/passwd)", "[x](data:text/html;base64,AAAA)", "[x](data:image/png;base64,AAAA)",
		"[x][r]\n\n[r]: javascript:alert(1)", "[x](< javascript:alert(1)>)",
		"[a](javascript:1) [x](javascript:2) [c](javascript:3)", "*[x](javascript:1)*", "**a [x](javascript:1) [b](javascript:2)**",
		"|h|\n|-|\n|[x](javascript:1)|", "- [x](javascript:1)", "- [ ] [x](javascript:1)", "[![x](javascript:1)](javascript:2)",
		"![x](javascript:alert(1))", "![x](data:image/svg+xml,<svg onload=alert(1)>)", "![[x](javascript:1)](y.png)",
	} {
		out := md1(t, src)
		if strings.Contains(out, "<a") {
			t.Errorf("%q kept a link: %s", src, out)
		}
		if !strings.Contains(out, "x") {
			t.Errorf("%q lost text: %s", src, out)
		}
	}
	if out := md1(t, "<javascript:alert(1)>"); strings.Contains(out, "<a") || !strings.Contains(out, "javascript:alert(1)") {
		t.Errorf("autolink: %s", out)
	}
}

func TestMarkdownRawHTMLDropped(t *testing.T) {
	for _, src := range []string{"<script>alert(1)</script>", "<img src=x onerror=alert(1)>", "<svg onload=alert(1)>", "<style>a{}</style>"} {
		out := md1(t, src)
		if strings.Contains(out, "alert") && !strings.Contains(out, "omitted") {
			t.Errorf("%q: %s", src, out)
		}
	}
}

func TestMarkdownAttributesAndIDs(t *testing.T) {
	out := md1(t, `# h {onclick="alert(1)" style="x" id="y"}`)
	if !strings.HasPrefix(out, "<h1>h {") {
		t.Errorf("heading: %s", out)
	}
	out = md1(t, `[x](https://e "t\" onmouseover=\"alert(1)")`)
	if strings.Contains(out, `" onmouseover=`) {
		t.Errorf("title: %s", out)
	}
}

func TestMarkdownImages(t *testing.T) {
	linkOnly := func(src, href string) {
		t.Helper()
		out := md1(t, src)
		if strings.Contains(out, "<img") || !strings.Contains(out, `<a href="`+href+`">`) {
			t.Errorf("%q: %s", src, out)
		}
	}
	linkOnly("![a](https://evil/x.png)", "https://evil/x.png")
	linkOnly("![a](//evil/x.png)", "//evil/x.png")
	linkOnly(`![a](/\evil/x.png)`, `/%5Cevil/x.png`)
	linkOnly(`![a](\\\\evil/x.png)`, `%5C%5Cevil/x.png`)
	linkOnly("![a](< //evil/x.png>)", "%20//evil/x.png")
	if out := md1(t, "![](https://evil/x.png)"); !strings.Contains(out, ">https://evil/x.png</a>") {
		t.Errorf("empty alt: %s", out)
	}
	for _, src := range []string{"![a](img/x.png)", "![a](/a/ID/raw)"} {
		if out := md1(t, src); !strings.Contains(out, "<img src=") {
			t.Errorf("%q: %s", src, out)
		}
	}
	if out := md1(t, "![**b**](x.png)"); !strings.Contains(out, `alt="b"`) || strings.Contains(out, "strong") || strings.Count(out, "b") != 1 {
		t.Errorf("alt: %s", out)
	}
	if out := md1(t, `![a" onerror="alert(1)](x.png)`); !strings.Contains(out, "&quot;") {
		t.Errorf("alt escape: %s", out)
	}
	if out := md1(t, "![[x](https://e)](y.png)"); strings.Count(out, "<img") != 1 || strings.Contains(out, "<a") {
		t.Errorf("nested: %s", out)
	}
	if out := md1(t, "[![a](https://evil/x.png)](https://o)"); strings.Count(out, "<a") != 1 || strings.Contains(out, "<img") || !strings.Contains(out, ">a</a>") {
		t.Errorf("image in link: %s", out)
	}
	if out := md1(t, "[![a](img.png)](https://o)"); !strings.Contains(out, "<a href=\"https://o\"><img") {
		t.Errorf("relative image in link: %s", out)
	}
}

func TestMarkdownFeatures(t *testing.T) {
	out := md1(t, "|a|b|\n|:-:|--:|\n|1|2|")
	if !strings.Contains(out, `align="center"`) || !strings.Contains(out, `align="right"`) || strings.Contains(out, "style") || strings.Count(out, "<table>") != 1 {
		t.Errorf("table: %s", out)
	}
	if out := md1(t, "~~x~~"); !strings.Contains(out, "<del>x</del>") {
		t.Errorf("strike: %s", out)
	}
	if out := md1(t, "www.example.com"); !strings.Contains(out, `<a href="http://www.example.com">`) {
		t.Errorf("linkify: %s", out)
	}
	if out := md1(t, "- [x] done"); !strings.Contains(out, `checked="" disabled="" type="checkbox"`) {
		t.Errorf("task: %s", out)
	}
	if out := md1(t, "```go\nfunc main() {}\n```"); !strings.Contains(out, `<pre class="hl-chroma">`) || !strings.Contains(out, `class="hl-k`) {
		t.Errorf("go fence: %s", out)
	}
	if out := md1(t, "```html\n<script>alert(1)</script>\n```"); !strings.Contains(out, "&lt;") || !strings.Contains(out, "hl-chroma") {
		t.Errorf("html fence: %s", out)
	}
	for _, src := range []string{"```nosuchlang\n<b>\n```", "```\n<b>\n```", "```\"><script>\n<b>\n```", "```go {style=\"x\"}\nx\n```"} {
		out := md1(t, src)
		if strings.Contains(out, "nosuchlang") || strings.Contains(out, "script") && strings.Contains(out, "<script") {
			t.Errorf("%q: %s", src, out)
		}
	}
	if out := md1(t, "```nosuchlang\n<b>\n```"); out != "<pre><code>&lt;b&gt;\n</code></pre>\n" {
		t.Errorf("plain block: %q", out)
	}
	if out := md1(t, "\xff\xfe<script>"); !strings.Contains(out, "�") {
		t.Errorf("utf8: %s", out)
	}
}

func TestMarkdownConcurrent(t *testing.T) {
	src := []byte("```go\nfunc main() {}\n```\n\n```html\n<b>x</b>\n```\n\n|a|b|\n|:-:|--:|\n|1|2|\n\n[a](https://e) [b](javascript:1)\n")
	want, err := Markdown(src)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				got, err := Markdown(src)
				if err != nil || got != want {
					t.Error("concurrent result differs")
					return
				}
			}
		})
	}
	wg.Wait()
}
