package web

import (
	"context"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/config"
	"github.com/niekcandaele/artitrove/internal/render"
	"github.com/niekcandaele/artitrove/internal/store"
	"github.com/niekcandaele/artitrove/internal/ui"
)

const token = "0123456789abcdef-token"

type env struct {
	t   *testing.T
	st  *store.Store
	mux *http.ServeMux
}

func newEnv(t *testing.T, index bool) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return newEnvWith(t, st, index)
}

func newEnvWith(t *testing.T, st *store.Store, index bool) *env {
	cfg := config.Config{Token: token, BaseURL: "https://trove.example", Index: index}
	raw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(299)
		_, _ = io.WriteString(w, "RAW:"+r.PathValue("id")) //nolint:gosec // test stub
	})
	mux := http.NewServeMux()
	Register(mux, cfg, st, raw)
	return &env{t: t, st: st, mux: mux}
}

func (e *env) create(title, ct, content string) string {
	e.t.Helper()
	a, err := e.st.Create(context.Background(), title, ct, strings.NewReader(content))
	if err != nil {
		e.t.Fatal(err)
	}
	return a.ID
}

func (e *env) do(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

var accHTML = map[string]string{"Accept": "text/html"}

func (e *env) page(target string) (*httptest.ResponseRecorder, string) {
	rec := e.do("GET", target, accHTML)
	return rec, rec.Body.String()
}

var (
	tagRe      = regexp.MustCompile(`<([a-zA-Z][\w-]*)((?:\s+[\w:-]+(?:="[^"]*")?)*)\s*/?>`)
	attrRe     = regexp.MustCompile(`\s([\w:-]+)(?:="([^"]*)")?`)
	quotedRe   = regexp.MustCompile(`"[^"]*"`)
	unquotedRe = regexp.MustCompile(`<[a-zA-Z][^>]*\s[\w-]+=[^"\s>]`)
)

// assertSafe fails on anything the page CSP would block or that escaping should have prevented.
func assertSafe(t *testing.T, body string) {
	t.Helper()
	for i := 0; i < len(body); i++ {
		if body[i] != '<' {
			continue
		}
		rest := body[i:]
		if loc := tagRe.FindStringIndex(rest); strings.HasPrefix(rest, "</") || strings.HasPrefix(rest, "<!doctype") || strings.HasPrefix(rest, "<!--") || loc != nil && loc[0] == 0 {
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
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		name := m[1]
		attrs := map[string]string{}
		for _, a := range attrRe.FindAllStringSubmatch(m[2], -1) {
			attrs[a[1]] = html.UnescapeString(a[2])
		}
		for k, v := range attrs {
			if k == "style" || strings.HasPrefix(k, "on") {
				t.Errorf("forbidden attribute %s on <%s>", k, name)
			}
			external := strings.HasPrefix(v, "//") || !strings.HasPrefix(v, "/")
			if name == "a" && k == "href" {
				external = external && !regexp.MustCompile(`^(https?:|mailto:)`).MatchString(v)
			}
			if (k == "href" || k == "src" || k == "action") && external {
				t.Errorf("non-local %s=%q on <%s>", k, v, name)
			}
		}
		if name == "script" && attrs["src"] != "/static/app.js" {
			t.Errorf("unexpected script %v", attrs)
		}
	}
	if strings.Count(body, "<script") != strings.Count(body, `<script src="/static/app.js" defer></script>`) {
		t.Error("inline script")
	}
}

func checkPage(t *testing.T, rec *httptest.ResponseRecorder, status int) string {
	t.Helper()
	body := rec.Body.String()
	h := rec.Header()
	if rec.Code != status || h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Content-Security-Policy") != ui.PageCSP {
		t.Fatalf("status %d, headers %v", rec.Code, h)
	}
	assertSafe(t, body)
	return body
}

func mustContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q", s)
		}
	}
}

func mustNotContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(body, s) {
			t.Errorf("body contains %q", s)
		}
	}
}

func TestViewKinds(t *testing.T) {
	e := newEnv(t, false)
	cases := []struct {
		name, ct, content string
		want, absent      []string
	}{
		{"html", "text/html", "<p>UNIQUEMARKER</p>", []string{`<iframe sandbox="allow-scripts" src="/a/{id}/raw"`}, []string{"UNIQUEMARKER"}},
		{"markdown", "text/markdown", "# Hi\n\n```go\nfunc main() {}\n```\n", []string{"<h1", "hl-chroma", "/static/highlight.css"}, nil},
		{"json", "application/json", `{"a":[1,2]}`, []string{`class="json-tree"`}, nil},
		{"text", "text/plain", "a < b", []string{`<pre class="text">`, "a &lt; b"}, nil},
		{"yaml", "application/x-yaml", "k: v", []string{`<pre class="text">`, "k: v"}, nil},
		{"png", "image/png", "\x89PNG", []string{"download=", `href="/a/{id}/raw"`}, []string{"<iframe"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := e.create("T", c.ct, c.content)
			rec, _ := e.page("/a/" + id)
			body := checkPage(t, rec, 200)
			if rec.Header().Get("Vary") != "Accept" {
				t.Error("Vary")
			}
			for _, w := range c.want {
				mustContain(t, body, strings.ReplaceAll(w, "{id}", id))
			}
			mustNotContain(t, body, c.absent...)
		})
	}
}

func TestViewJSONFallbacks(t *testing.T) {
	e := newEnv(t, false)
	id := e.create("T", "application/json", `{"a":<b>`)
	_, body := e.page("/a/" + id)
	mustContain(t, body, `<pre class="text">`, `{&#34;a&#34;:&lt;b&gt;`)
	mustNotContain(t, body, "json-tree")

	big := `{"LARGEMARKER":"` + strings.Repeat("x", 600<<10) + `"}`
	id = e.create("T", "application/json", big)
	_, body = e.page("/a/" + id)
	mustContain(t, body, `<pre class="text">`, "LARGEMARKER")
	mustNotContain(t, body, "json-tree")
}

func TestViewInvalidUTF8Text(t *testing.T) {
	e := newEnv(t, false)
	id := e.create("T", "text/plain", "\xff\xfe")
	rec, _ := e.page("/a/" + id)
	mustContain(t, checkPage(t, rec, 200), "�")
}

func TestViewTitleFallbackAndParams(t *testing.T) {
	e := newEnv(t, false)
	id := e.create("", "text/markdown; charset=utf-8", "# Hi")
	rec, _ := e.page("/a/" + id)
	body := checkPage(t, rec, 200)
	if !regexp.MustCompile(`<h1[^>]*>[^<]*(<bdi>)?` + id).MatchString(body) {
		t.Errorf("ID not in the heading")
	}
}

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

func TestViewHostileTitles(t *testing.T) {
	e := newEnv(t, false)
	for _, title := range hostile {
		for _, ct := range []string{"text/html", "text/markdown", "application/json", "text/plain", "image/png"} {
			id := e.create(title, ct, `{"a":1}`)
			rec, _ := e.page("/a/" + id)
			body := checkPage(t, rec, 200)
			if strings.ContainsAny(title, `<>"'&`) {
				mustNotContain(t, body, title)
			}
			if title == "{{.ID}}" {
				mustContain(t, body, "{{.ID}}")
			}
		}
	}
	rec, _ := e.page("/a/" + e.create(hostile[0], "text/plain", "x"))
	mustNotContain(t, rec.Body.String(), "<script>alert")
}

func TestViewHostileContent(t *testing.T) {
	e := newEnv(t, false)
	md := "<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[x](javascript:alert(1))\n\n![](https://evil/x.png)\n\n<iframe src=x></iframe>\n"
	cases := []struct{ ct, content string }{
		{"text/markdown", md},
		{"application/json", `{"</script><script>alert(1)</script>":"</script><script>alert(1)</script>"}`},
		{"text/plain", "</pre><script>alert(1)</script>"},
		{"text/plain; x=\"<script>\"", "hi"},
	}
	for _, c := range cases {
		rec, _ := e.page("/a/" + e.create("T", c.ct, c.content))
		body := checkPage(t, rec, 200)
		mustNotContain(t, body, "<script>alert", "<iframe", `<img src="https`, "javascript:")
	}
	rec, _ := e.page("/a/" + e.create("T", "text/markdown", md))
	mustContain(t, rec.Body.String(), `<a href="https://evil/x.png"`)
}

func TestViewHTMLOnlyIframe(t *testing.T) {
	e := newEnv(t, false)
	id := e.create("T", "text/html", "<script>alert(1)</script>")
	rec, _ := e.page("/a/" + id)
	body := checkPage(t, rec, 200)
	mustNotContain(t, body, "alert(1)", "allow-same-origin")
	if n := strings.Count(body, "<iframe"); n != 1 {
		t.Fatalf("%d iframes", n)
	}
	mustContain(t, body, `sandbox="allow-scripts"`, `src="/a/`+id+`/raw"`)
}

func TestViewAcceptNegotiation(t *testing.T) {
	e := newEnv(t, false)
	id := e.create("T", "text/plain", "x")
	rawCases := [][]string{
		nil, {"*/*"}, {"text/*"}, {"application/json"}, {"text/html;q=0"}, {"text/html; q=0.0"},
		{"text/htmlx"}, {"text/html;q=0;x"}, {"text/html;q=abc"},
	}
	htmlCases := [][]string{
		{"text/html"}, {"TEXT/HTML"}, {"text/html;q=0.1"}, {"text/html;level=1"},
		{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		{"application/json, text/html"}, {"application/json", "text/html"},
	}
	run := func(lines []string, wantRaw bool) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/a/"+id, nil)
		for _, l := range lines {
			req.Header.Add("Accept", l)
		}
		rec := httptest.NewRecorder()
		e.mux.ServeHTTP(rec, req)
		if (rec.Code == 299) != wantRaw || rec.Header().Get("Vary") != "Accept" {
			t.Errorf("Accept %q: status %d, Vary %q", lines, rec.Code, rec.Header().Get("Vary"))
		}
		if wantRaw && rec.Body.String() != "RAW:"+id {
			t.Errorf("Accept %q: body %q", lines, rec.Body.String())
		}
	}
	for _, c := range rawCases {
		run(c, true)
	}
	for _, c := range htmlCases {
		run(c, false)
	}
	rec := e.do("GET", "/a/"+strings.Repeat("A", 22), nil)
	if rec.Code != 299 {
		t.Errorf("unknown id without Accept: %d", rec.Code)
	}
}

func TestViewNotFound(t *testing.T) {
	e := newEnv(t, false)
	gone := e.create("T", "text/plain", "x")
	if err := e.st.Delete(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		strings.Repeat("A", 22), "a.b", strings.Repeat("A", 21), strings.Repeat("A", 23),
		strings.Repeat("A", 10) + "." + strings.Repeat("A", 11), strings.Repeat("A", 10) + "!" + strings.Repeat("A", 11),
		"..%2F..%2Fetc", gone,
	}
	for _, id := range ids {
		rec, _ := e.page("/a/" + id)
		mustContain(t, checkPage(t, rec, 404), "Not found")
		if rec.Header().Get("Vary") != "Accept" {
			t.Errorf("%s: Vary", id)
		}
	}
}

func TestLandingIndexOff(t *testing.T) {
	e := newEnv(t, false)
	id := e.create("SECRETTITLE", "text/plain", "x")
	for _, hdr := range []map[string]string{nil, {"Authorization": "Basic " + basic("u", token)}} {
		rec := e.do("GET", "/", hdr)
		body := checkPage(t, rec, 200)
		mustNotContain(t, body, id, "SECRETTITLE", `class="list"`)
		mustContain(t, body, "https://trove.example/api/artifacts")
		if strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Error("no-store with index off")
		}
	}
}

func TestNoRouteListsWhenIndexOff(t *testing.T) {
	e := newEnv(t, false)
	id := e.create("SECRETTITLE", "text/plain", "x")
	for _, p := range []string{"/", "/static/", "/static/fonts/", "/a/", "/a", "/?index=1"} {
		for _, hdr := range []map[string]string{nil, {"Authorization": "Basic " + basic("u", token)}} {
			if body := e.do("GET", p, hdr).Body.String(); strings.Contains(body, id) || strings.Contains(body, "SECRETTITLE") {
				t.Errorf("%s lists an artifact", p)
			}
		}
	}
}

func basic(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func TestLandingIndexAuth(t *testing.T) {
	e := newEnv(t, true)
	id := e.create("T", "text/plain", "x")
	bad := map[string]string{
		"none":          "",
		"wrong":         "Basic " + basic("u", "nope"),
		"empty":         "Basic " + basic("u", ""),
		"token as user": "Basic " + basic(token, ""),
		"bearer":        "Bearer " + token,
		"trailing":      "Basic " + basic("u", token+" "),
	}
	for name, auth := range bad {
		hdr := map[string]string{}
		if auth != "" {
			hdr["Authorization"] = auth
		}
		rec := e.do("GET", "/", hdr)
		if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != `Basic realm="Artitrove", charset="UTF-8"` ||
			rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), id) {
			t.Errorf("%s: %d %v", name, rec.Code, rec.Header())
		}
	}
	for _, user := range []string{"anything", ""} {
		rec := e.do("GET", "/", map[string]string{"Authorization": "Basic " + basic(user, token)})
		body := checkPage(t, rec, 200)
		mustContain(t, body, id)
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Error("no-store missing")
		}
	}
}

func TestLandingIndexNewestFirst(t *testing.T) {
	e := newEnv(t, true)
	auth := map[string]string{"Authorization": "Basic " + basic("u", token)}
	body := e.do("GET", "/", auth).Body.String()
	mustContain(t, body, "No artifacts yet.")

	a := e.create("<script>alert(1)</script>", "text/plain", "x")
	b := e.create("B", "text/markdown", "# b")
	c := e.create("C", "image/png", "png")
	rec := e.do("GET", "/", auth)
	body = checkPage(t, rec, 200)
	rows := regexp.MustCompile(`<li data-kind="(\w+)"><a href="/a/([^"]+)"`).FindAllStringSubmatch(body, -1)
	want := [][2]string{{"binary", c}, {"markdown", b}, {"text", a}}
	if len(rows) != 3 {
		t.Fatalf("rows: %v", rows)
	}
	for i, w := range want {
		if rows[i][1] != w[0] || rows[i][2] != w[1] {
			t.Errorf("row %d = %v, want %v", i, rows[i][1:], w)
		}
	}
}

func TestStatic(t *testing.T) {
	e := newEnv(t, false)
	const cc = "public, max-age=3600"
	for path, ct := range map[string]string{
		"/static/app.css": "text/css", "/static/app.js": "text/javascript", "/static/favicon.svg": "image/svg+xml",
		"/static/fonts/Geist-Variable.woff2": "", "/static/highlight.css": "text/css; charset=utf-8",
	} {
		rec := e.do("GET", path, nil)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != cc || !strings.HasPrefix(rec.Header().Get("Content-Type"), ct) {
			t.Errorf("%s: %d %v", path, rec.Code, rec.Header())
		}
	}
	if e.do("GET", "/static/highlight.css", nil).Body.String() != render.HighlightCSS() {
		t.Error("highlight.css body")
	}
	for _, p := range []string{"/static/", "/static/fonts/", "/static/fonts", "/static/nope.css"} {
		rec := e.do("GET", p, nil)
		if rec.Code != 404 || rec.Header().Get("Cache-Control") != cc || strings.Contains(rec.Body.String(), "woff2") {
			t.Errorf("%s: %d %v", p, rec.Code, rec.Header())
		}
	}
	if rec := e.do("POST", "/static/app.css", nil); rec.Code != 405 {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestServerErrorOnStoreFailure(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, st, true)
	id := e.create("T", "text/plain", "x")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(context.Background(), id); err == nil || errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("closed store error = %v", err)
	}
	if rec, _ := e.page("/a/" + id); rec.Code != 500 {
		t.Errorf("view: %d", rec.Code)
	}
	rec := e.do("GET", "/", map[string]string{"Authorization": "Basic " + basic("u", token)})
	if rec.Code != 500 {
		t.Errorf("landing: %d", rec.Code)
	}
}

func TestSecurityHeadersOnPages(t *testing.T) {
	// Global headers come from cmd/artitrove middleware; here every app page must carry the CSP.
	e := newEnv(t, false)
	id := e.create("T", "text/plain", "x")
	for _, p := range []string{"/", "/a/" + id, "/a/" + strings.Repeat("A", 22)} {
		if got := e.do("GET", p, accHTML).Header().Get("Content-Security-Policy"); got != ui.PageCSP {
			t.Errorf("%s: CSP %q", p, got)
		}
	}
}
