package ui

import (
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/render"
)

const testID = "Zk3vXq9mT2c8RwLp0aBn4g"

var created = time.Date(2026, 11, 24, 14, 2, 11, 0, time.UTC)

var hostile = []string{
	`<script>alert(1)</script>`,
	`"><img src=x onerror=alert(1)>`,
	`' onmouseover='alert(1)' x='`,
	`javascript:alert(1)`,
	`</title></pre></textarea><svg onload=alert(1)>`,
	`{{.ID}}`,
	`&amp;&lt;`,
	"\u202eRTL",
	strings.Repeat("😀", 200),
}

func page(kind render.Kind) ArtifactPage {
	return ArtifactPage{
		Artifact: artifact.Artifact{ID: testID, Title: "Latency report", ContentType: "text/html", Size: 38211, CreatedAt: created},
		URL:      "https://trove.example/a/" + testID,
		Kind:     kind,
	}
}

func get(t *testing.T, fn func(w http.ResponseWriter) error) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := fn(rec); err != nil {
		t.Fatal(err)
	}
	return rec, rec.Body.String()
}

func artifactBody(t *testing.T, p ArtifactPage) string {
	t.Helper()
	rec, body := get(t, func(w http.ResponseWriter) error { return RenderArtifact(w, p) })
	if rec.Code != 200 || rec.Header().Get("Content-Security-Policy") != PageCSP || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("bad response: %d %v", rec.Code, rec.Header())
	}
	assertSafe(t, body)
	return body
}

var (
	tagRe      = regexp.MustCompile(`<([a-zA-Z][\w-]*)((?:\s+[\w:-]+(?:="[^"]*")?)*)\s*/?>`)
	attrRe     = regexp.MustCompile(`\s([\w:-]+)(?:="([^"]*)")?`)
	quotedRe   = regexp.MustCompile(`"[^"]*"`)
	unquotedRe = regexp.MustCompile(`<[a-zA-Z][^>]*\s[\w-]+=[^"\s>]`)
)

type tag struct {
	name  string
	attrs map[string]string
}

func tags(body string) []tag {
	var out []tag
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		tg := tag{name: m[1], attrs: map[string]string{}}
		for _, a := range attrRe.FindAllStringSubmatch(m[2], -1) {
			tg.attrs[a[1]] = html.UnescapeString(a[2])
		}
		out = append(out, tg)
	}
	return out
}

// assertSafe fails on anything the page CSP would block or that escaping should have prevented.
func assertSafe(t *testing.T, body string) {
	t.Helper()
	for i := 0; i < len(body); i++ {
		if body[i] != '<' {
			continue
		}
		rest := body[i:]
		if strings.HasPrefix(rest, "</") || strings.HasPrefix(rest, "<!doctype") || tagRe.FindStringIndex(rest) != nil && tagRe.FindStringIndex(rest)[0] == 0 {
			continue
		}
		t.Fatalf("stray < at %d: %.40q", i, rest)
	}
	if unquotedRe.MatchString(quotedRe.ReplaceAllString(body, `""`)) {
		t.Error("unquoted attribute")
	}
	if strings.Contains(body, "<style") {
		t.Error("<style>")
	}
	for _, tg := range tags(body) {
		for k, v := range tg.attrs {
			if k == "style" || strings.HasPrefix(k, "on") {
				t.Errorf("forbidden attribute %s on <%s>", k, tg.name)
			}
			if (k == "href" || k == "src" || k == "action") && (!strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//")) {
				t.Errorf("non-local %s=%q on <%s>", k, v, tg.name)
			}
		}
		if tg.name == "script" && (tg.attrs["src"] != "/static/app.js") {
			t.Errorf("unexpected script %v", tg.attrs)
		}
	}
	if strings.Count(body, "<script") != strings.Count(body, "<script src=\"/static/app.js\" defer></script>") {
		t.Error("inline script")
	}
}

func TestTemplatesDefined(t *testing.T) {
	for _, n := range []string{"artifact", "landing", "notfound"} {
		if pages.Lookup(n) == nil {
			t.Errorf("template %s not defined", n)
		}
	}
}

var scriptRe = regexp.MustCompile(`<script[^>]*>`)

func TestTemplateSourcesCSPSafe(t *testing.T) {
	bad := regexp.MustCompile(`style=|<style|\son[a-z]+=|javascript:|(src|href|action)="(https?:)?//`)
	files, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatal(err, files)
	}
	for _, f := range files {
		b, err := fs.ReadFile(templateFS, f)
		if err != nil {
			t.Fatal(err)
		}
		if m := bad.Find(b); m != nil {
			t.Errorf("%s contains %q", f, m)
		}
		for _, m := range scriptRe.FindAll(b, -1) {
			if !strings.Contains(string(m), " src=") {
				t.Errorf("%s has inline script %q", f, m)
			}
		}
	}
}

func TestStaticAssets(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(Static(), name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, f := range []string{"fonts/Geist-Variable.woff2", "fonts/GeistMono-Variable.woff2"} {
		if !strings.HasPrefix(read(f), "wOF2") {
			t.Errorf("%s is not woff2", f)
		}
	}
	if !strings.Contains(read("fonts/OFL.txt"), "SIL Open Font License") {
		t.Error("licence missing")
	}
	read("favicon.svg")
	if _, err := fs.Stat(Static(), "highlight.css"); err == nil {
		t.Error("highlight.css is served by package web, not shipped by ui")
	}
	css, js := read("app.css"), read("app.js")
	for _, s := range []string{"prefers-color-scheme: dark", "prefers-reduced-motion", "[hidden] { display: none !important; }"} {
		if !strings.Contains(css, s) {
			t.Errorf("app.css lacks %q", s)
		}
	}
	if strings.Contains(css, "@import") || regexp.MustCompile(`url\(["']?(/|https?:)`).MatchString(css) {
		t.Error("app.css loads something non-relative")
	}
	for name, src := range map[string]string{"app.css": css, "app.js": js} {
		if strings.Contains(src, "http://") || strings.Contains(src, "https://") {
			t.Errorf("%s has an external URL", name)
		}
	}
	for _, s := range []string{"innerHTML", "eval(", "fetch("} {
		if strings.Contains(js, s) {
			t.Errorf("app.js uses %s", s)
		}
	}
}

func TestStaticReferencesResolve(t *testing.T) {
	ref := regexp.MustCompile(`/static/([\w./-]+)`)
	files, _ := fs.Glob(templateFS, "templates/*.html")
	for _, f := range files {
		b, _ := fs.ReadFile(templateFS, f)
		for _, m := range ref.FindAllStringSubmatch(string(b), -1) {
			if m[1] == "highlight.css" {
				continue
			}
			if _, err := fs.Stat(Static(), m[1]); err != nil {
				t.Errorf("%s references missing %s", f, m[1])
			}
		}
	}
}

func TestRenderArtifactKinds(t *testing.T) {
	const mdBody = `<h1>Plan</h1><pre class="hl-chroma"><code><span class="hl-k">for</span></code></pre><table><tr><td>1</td></tr></table>`
	const jsonBody = `<div class="json-tree"><details open><summary><span class="dim">{ 1 key }</span></summary><div><span class="key">"a"</span>: <span class="num">1</span></div></details></div>`
	cases := []struct {
		kind  render.Kind
		ct    string
		label string
		want  []string
		not   []string
	}{
		{render.KindHTML, "text/html", "Open full page",
			[]string{`<body class="page-html">`, `<iframe sandbox="allow-scripts" src="/a/` + testID + `/raw" title="Latency report">`},
			[]string{"reading", `class="foot"`, "highlight.css"}},
		{render.KindMarkdown, "text/markdown", "Open raw",
			[]string{`<article class="reading">` + mdBody + `</article>`, `<link rel="stylesheet" href="/static/highlight.css">`, `class="foot"`}, nil},
		{render.KindJSON, "application/json", "Open raw",
			[]string{`<div class="json-view">` + jsonBody + `</div>`}, []string{"highlight.css"}},
		{render.KindText, "text/plain", "Open raw", []string{`<pre class="text">`}, nil},
		{render.KindBinary, "application/zip", "Open raw",
			[]string{`<a class="btn primary" href="/a/` + testID + `/raw" download="Latency report">Download</a>`}, nil},
		{"", "application/x-weird", "Open raw", []string{"Download"}, nil},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			p := page(c.kind)
			p.ContentType = c.ct
			p.Body = template.HTML(mdBody) //nolint:gosec // trusted test fixture
			if c.kind == render.KindJSON {
				p.Body = template.HTML(jsonBody) //nolint:gosec // trusted test fixture
			}
			p.Text = "hello"
			body := artifactBody(t, p)
			for _, w := range append(c.want,
				`<h1><bdi>Latency report</bdi></h1>`,
				`<bdi>`+c.ct+`</bdi>`, `<span>38 kB</span>`,
				`<time datetime="2026-11-24T14:02:11Z">24 Nov 2026, 14:02 UTC</time>`,
				`<a class="btn" href="/a/`+testID+`/raw">`, `<span class="txt">`+c.label+`</span>`,
				`readonly value="https://trove.example/a/`+testID+`"`, `<script src="/static/app.js" defer></script>`) {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q", w)
				}
			}
			for _, n := range c.not {
				if strings.Contains(body, n) {
					t.Errorf("unexpected %q", n)
				}
			}
			if c.kind == render.KindHTML && !regexp.MustCompile(`</header>\s*<iframe`).MatchString(body) {
				t.Error("iframe must directly follow the header")
			}
			if c.kind == render.KindMarkdown && strings.Index(body, "highlight.css") > strings.Index(body, "app.css") {
				t.Error("highlight.css must precede app.css")
			}
		})
	}
}

func TestTextKeepsLeadingNewlines(t *testing.T) {
	for _, text := range []string{"a\n\tb  c", "\n\nfirst", "<b>&"} {
		p := page(render.KindText)
		p.Text = text
		body := artifactBody(t, p)
		i := strings.Index(body, "<pre class=\"text\">\n") + len("<pre class=\"text\">\n")
		got := html.UnescapeString(body[i:strings.Index(body, "</pre>")])
		if got != text {
			t.Errorf("text %q rendered as %q", text, got)
		}
	}
}

func TestTitleFallsBackToID(t *testing.T) {
	p := page(render.KindBinary)
	p.Title = ""
	body := artifactBody(t, p)
	for _, w := range []string{"<title>" + testID + "</title>", "<h1><bdi>" + testID + "</bdi></h1>", `download="` + testID + `"`} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func between(s, open, end string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	s = s[i+len(open):]
	return html.UnescapeString(s[:strings.Index(s, end)])
}

func TestHostileStringsArtifact(t *testing.T) {
	for _, h := range hostile {
		for _, kind := range []render.Kind{render.KindHTML, render.KindMarkdown, render.KindJSON, render.KindText, render.KindBinary} {
			p := page(kind)
			p.Title, p.ContentType, p.URL, p.Text, p.SHA256 = h, h, h, h, h
			body := artifactBody(t, p)
			if got := between(body, "<h1><bdi>", "</bdi></h1>"); got != h {
				t.Errorf("%q/%s: title round-trip %q", h, kind, got)
			}
			if got := between(body, `<span class="type type-`+string(kind)+`"><bdi>`, "</bdi>"); got != h {
				t.Errorf("%q/%s: type round-trip %q", h, kind, got)
			}
			if kind == render.KindText && between(body, "<pre class=\"text\">\n", "</pre>") != h {
				t.Errorf("%q: text round-trip", h)
			}
			for _, tg := range tags(body) {
				if tg.name == "img" || tg.name == "textarea" {
					t.Errorf("%q/%s: injected <%s>", h, kind, tg.name)
				}
			}
			for _, tg := range tags(body) {
				if tg.name == "input" && tg.attrs["value"] != h {
					t.Errorf("%q: input value %q", h, tg.attrs["value"])
				}
			}
		}
	}
	// hostile ID and kind land only in quoted attribute values
	p := page(render.KindHTML)
	p.ID, p.Kind = `x" onclick="alert(1)`, `x" onclick="alert(1)`
	assertSafe(t, artifactBody(t, p))
}

func entries() []IndexEntry {
	mk := func(id, title, ct string, k render.Kind, size int64) IndexEntry {
		return IndexEntry{Artifact: artifact.Artifact{ID: id, Title: title, ContentType: ct, Size: size, CreatedAt: created}, Kind: k}
	}
	return []IndexEntry{
		mk("a1", "First", "text/markdown", render.KindMarkdown, 4200),
		mk("b2", "", "text/html", render.KindHTML, 38211),
		mk("c3", "Third", "application/zip", render.KindBinary, 5),
	}
}

func landing(t *testing.T, p LandingPage) string {
	t.Helper()
	rec, body := get(t, func(w http.ResponseWriter) error { return RenderLanding(w, p) })
	if rec.Code != 200 || rec.Header().Get("Content-Security-Policy") != PageCSP {
		t.Fatalf("bad response %d %v", rec.Code, rec.Header())
	}
	assertSafe(t, body)
	return body
}

func TestLandingIndexOff(t *testing.T) {
	body := landing(t, LandingPage{BaseURL: "https://trove.example", Artifacts: entries()})
	if !strings.Contains(between(body, `<pre id="curl">`, "</pre>"), "\"") || !strings.Contains(html.UnescapeString(body), `https://trove.example/api/artifacts?title=Latency+report`) {
		t.Error("curl example missing")
	}
	for _, s := range []string{`class="list"`, `class="filters"`, "First"} {
		if strings.Contains(body, s) {
			t.Errorf("index leaked: %q", s)
		}
	}
	if !strings.Contains(body, `class="btn copy-curl" hidden`) {
		t.Error("copy button must ship hidden")
	}
}

func TestLandingIndexOn(t *testing.T) {
	body := landing(t, LandingPage{BaseURL: "https://trove.example", Index: true, Artifacts: entries()})
	for _, w := range []string{"3 artifacts", `<div class="filters" hidden>`, `id="empty" hidden`, `class="home"`,
		`<li data-kind="markdown"><a href="/a/a1">`, `<bdi>b2</bdi>`, `data-kind="binary" aria-pressed="false">other`} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Index(body, `"/a/a1"`) > strings.Index(body, `"/a/b2"`) || strings.Index(body, `"/a/b2"`) > strings.Index(body, `"/a/c3"`) {
		t.Error("rows out of order")
	}
	one := landing(t, LandingPage{Index: true, Artifacts: entries()[:1]})
	if !strings.Contains(one, ">1 artifact<") {
		t.Error("singular count")
	}
	empty := landing(t, LandingPage{Index: true})
	if !strings.Contains(empty, "No artifacts yet.") || strings.Contains(empty, `class="list"`) {
		t.Error("empty index")
	}
}

func TestHostileStringsLanding(t *testing.T) {
	for _, h := range hostile {
		e := entries()[0]
		e.Title, e.ContentType, e.ID = h, h, h
		body := landing(t, LandingPage{BaseURL: h, Index: true, Artifacts: []IndexEntry{e, {Artifact: e.Artifact, Kind: render.Kind(h)}}})
		if got := between(body, `<span class="t"><bdi>`, "</bdi>"); got != h {
			t.Errorf("%q: title round-trip %q", h, got)
		}
		for _, tg := range tags(body) {
			if tg.name == "img" || tg.name == "svg" && tg.attrs["onload"] != "" {
				t.Errorf("%q: injected %s", h, tg.name)
			}
			if tg.name == "a" && tg.attrs["class"] == "mark" && tg.attrs["href"] != "/" {
				t.Errorf("mark href %q", tg.attrs["href"])
			}
		}
	}
}

func TestRenderNotFound(t *testing.T) {
	rec, body := get(t, RenderNotFound)
	if rec.Code != 404 || rec.Header().Get("Content-Security-Policy") != PageCSP || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("bad response %d %v", rec.Code, rec.Header())
	}
	assertSafe(t, body)
	if !strings.Contains(body, "Not found") || !strings.Contains(body, `<a href="/">`) {
		t.Error("content missing")
	}
}

func TestCallerHeadersKept(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Vary", "Accept")
	rec.Header().Set("Cache-Control", "no-store")
	if err := RenderArtifact(rec, page(render.KindText)); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get("Vary") != "Accept" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("caller headers lost")
	}
}

type spyWriter struct {
	httptest.ResponseRecorder
	wrote bool
}

func (s *spyWriter) WriteHeader(int)             { s.wrote = true }
func (s *spyWriter) Write(b []byte) (int, error) { s.wrote = true; return len(b), nil }

func TestExecuteErrorWritesNothing(t *testing.T) {
	w := &spyWriter{ResponseRecorder: *httptest.NewRecorder()}
	if err := execute(w, "missing", 200, nil); err == nil {
		t.Fatal("want error")
	}
	if w.wrote || w.Header().Get("Content-Type") != "" || w.Header().Get("Content-Security-Policy") != "" {
		t.Error("error path touched the response")
	}
}

func TestHumanSize(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0 B", 1: "1 B", 999: "999 B", 1000: "1.0 kB", 4200: "4.2 kB", 9949: "9.9 kB", 9950: "10 kB",
		38211: "38 kB", 999499: "999 kB", 999500: "1.0 MB", 10485760: "10 MB", 5e9: "5.0 GB",
	} {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTimeIsUTCAndDeterministic(t *testing.T) {
	p := page(render.KindText)
	p.CreatedAt = time.Date(2026, 11, 24, 16, 2, 11, 0, time.FixedZone("x", 2*3600))
	a := artifactBody(t, p)
	if !strings.Contains(a, `<time datetime="2026-11-24T14:02:11Z">24 Nov 2026, 14:02 UTC</time>`) {
		t.Error("not UTC")
	}
	if a != artifactBody(t, p) {
		t.Error("output not deterministic")
	}
}
