package e2e

import (
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"testing"
)

const firefoxAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"

func htmlView(t *testing.T, s *server, id string, accept ...string) string {
	t.Helper()
	if len(accept) == 0 {
		accept = []string{"text/html"}
	}
	var hdr []string
	for _, a := range accept {
		hdr = append(hdr, "Accept", a)
	}
	resp, b := s.get(t, "/a/"+id, hdr...)
	assertPage(t, resp, http.StatusOK)
	eq(t, "Vary", resp.Header.Get("Vary"), "Accept")
	return string(b)
}

func TestNegotiation(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	body := []byte("<p>negotiate</p>")
	a := s.upload(t, body, "text/html", "")

	for name, accept := range map[string][]string{
		"text/html":       {"text/html"},
		"firefox":         {firefoxAccept},
		"upper case":      {"TEXT/HTML"},
		"low quality":     {"text/html;q=0.1"},
		"wildcard first":  {"*/*, text/html"},
		"separate header": {"application/json", "text/html"},
	} {
		t.Run("view/"+name, func(t *testing.T) {
			page := htmlView(t, s, a.ID, accept...)
			contains(t, "page", page, "<iframe")
		})
	}

	for name, accept := range map[string][]string{
		"no accept":    nil,
		"star":         {"*/*"},
		"json":         {"application/json"},
		"zero quality": {"text/html;q=0"},
		"xhtml":        {"application/xhtml+xml"},
	} {
		t.Run("raw/"+name, func(t *testing.T) {
			var hdr []string
			for _, v := range accept {
				hdr = append(hdr, "Accept", v)
			}
			resp, b := s.get(t, "/a/"+a.ID, hdr...)
			assertRaw(t, resp, b, body, a)
			contains(t, "Vary", resp.Header.Get("Vary"), "Accept")
		})
	}

	for _, accept := range []string{"text/html", firefoxAccept} {
		resp, b := s.get(t, "/a/"+a.ID+"/raw", "Accept", accept)
		assertRaw(t, resp, b, body, a)
	}

	resp, _ := s.get(t, "/a/"+unknownID, "Accept", "text/html")
	assertPage(t, resp, http.StatusNotFound)
	eq(t, "Vary html 404", resp.Header.Get("Vary"), "Accept")
	resp, b := s.get(t, "/a/"+unknownID)
	assertJSONError(t, resp, b, http.StatusNotFound)
	eq(t, "Vary json 404", resp.Header.Get("Vary"), "Accept")
	resp, b = s.get(t, "/a/"+unknownID, "Accept", "*/*")
	assertJSONError(t, resp, b, http.StatusNotFound)
	eq(t, "Vary star 404", resp.Header.Get("Vary"), "Accept")
}

func TestViewHTML(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	body := []byte("<script>window.pwned=1</script><p>x</p>")
	for _, ctype := range []string{"text/html", "TEXT/HTML; Charset=UTF-8"} {
		a := s.upload(t, body, ctype, "")
		for _, accept := range []string{"text/html", "TEXT/HTML; Charset=UTF-8"} {
			page := htmlView(t, s, a.ID, accept)
			contains(t, ctype+" iframe", page, `<iframe sandbox="allow-scripts" src="/a/`+a.ID+`/raw"`)
			lacks(t, ctype, page, "allow-same-origin")
			lacks(t, ctype, page, "window.pwned")
		}
		resp, b := s.get(t, "/a/"+a.ID+"/raw")
		assertRaw(t, resp, b, body, a)
	}
}

func TestViewHeader(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	a := s.upload(t, []byte(strings.Repeat("x", 2500)), "text/plain", "Weekly report")
	page := htmlView(t, s, a.ID)
	text := html.UnescapeString(page)
	contains(t, "title element", text, "<title>Weekly report</title>")
	contains(t, "h1", text, "Weekly report")
	contains(t, "type", text, "text/plain")
	contains(t, "size", text, "2.5 kB")
	contains(t, "time", page, `<time datetime="`+a.CreatedAt+`"`)
	contains(t, "raw link", page, `href="/a/`+a.ID+`/raw"`)
	contains(t, "share url", page, a.URL)
	contains(t, "copy link", text, "Copy link")
	contains(t, "mobile viewport", page, `<meta name="viewport" content="width=device-width`)

	bare := s.upload(t, []byte("x"), "text/plain", "")
	text = html.UnescapeString(htmlView(t, s, bare.ID))
	contains(t, "id fallback title", text, "<title>"+bare.ID+"</title>")
}

func TestViewMarkdown(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	src := "# Hello\n\n```go\nfunc main() {}\n```\n\n" +
		"![a](/static/favicon.svg) ![r](pics/r.png)\n\n" +
		"![x](https://evil.test/p.png)\n\n![y](//evil.test/p.png)\n\n" +
		"<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n" +
		"[a](javascript:alert(1)) [b](JaVaScRiPt:alert(1)) [c](vbscript:x) [d](data:text/html,x)\n\n" +
		"[e](&#106;avascript:alert(1)) [f](java&#x09;script:alert(1))\n\n<javascript:alert(1)>\n\n" +
		"![j](javascript:alert(1)) ![k](data:image/svg+xml,x)\n\n" +
		"| l | c | r |\n|:--|:-:|--:|\n| 1 | 2 | 3 |\n"
	for _, ctype := range []string{"text/markdown", "text/markdown; charset=utf-8"} {
		a := s.upload(t, []byte(src), ctype, "")
		page := htmlView(t, s, a.ID)
		contains(t, "heading", page, "<h1")
		contains(t, "highlight", page, "hl-chroma")
		contains(t, "highlight css link", page, `href="/static/highlight.css"`)
		contains(t, "local image", page, `<img src="/static/favicon.svg"`)
		contains(t, "relative image", page, `<img src="pics/r.png"`)
		eq(t, "img count", strings.Count(page, "<img"), 2)
		contains(t, "external image as link", page, `<a href="https://evil.test/p.png"`)
		lacks(t, "page", page, "<script>alert")
		lacks(t, "page", page, "<img src=x")
		assertSafeURLs(t, page)
		assertCSPCompliant(t, page)
	}
	resp, b := s.get(t, "/static/highlight.css")
	eq(t, "highlight.css status", resp.StatusCode, http.StatusOK)
	contains(t, "highlight.css type", resp.Header.Get("Content-Type"), "text/css")
	assertCommonHeaders(t, resp)
	if len(b) == 0 {
		t.Error("highlight.css is empty")
	}
}

func TestViewJSON(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	valid := []byte(`{"a":[1,2,{"b":"</script><script>alert(1)</script>"}]}`)
	for _, ctype := range []string{"application/json", "application/json; charset=utf-8", "application/ld+json"} {
		page := htmlView(t, s, s.upload(t, valid, ctype, "").ID)
		contains(t, ctype, page, `class="json-tree"`)
		contains(t, ctype, page, "<details")
		contains(t, ctype, page, "&lt;script&gt;")
		lacks(t, ctype, page, "<script>alert")
		assertCSPCompliant(t, page)
	}
	page := htmlView(t, s, s.upload(t, []byte(`{"a":`), "application/json", "").ID)
	contains(t, "invalid json", page, "<pre")
	lacks(t, "invalid json", page, "json-tree")
	if !regexp.MustCompile(`\{(&#34;|")a(&#34;|"):`).MatchString(page) {
		t.Error("invalid JSON text not shown")
	}
}

func TestViewText(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	page := htmlView(t, s, s.upload(t, []byte("</pre><script>alert(1)</script>"), "text/plain", "").ID)
	contains(t, "text", page, "<pre")
	contains(t, "text", page, "&lt;/pre&gt;&lt;script&gt;alert(1)&lt;/script&gt;")
	lacks(t, "text", page, "<script>alert")
	assertCSPCompliant(t, page)

	for _, ctype := range []string{"application/javascript", "text/x-go"} {
		contains(t, ctype, htmlView(t, s, s.upload(t, []byte("var x = 1 < 2\n"), ctype, "").ID), "<pre")
	}
	htmlView(t, s, s.upload(t, []byte("ok \xff\xfe bad"), "text/plain", "").ID)
}

func TestViewBinary(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>window.pwned=1</script></svg>`)
	cases := []struct {
		ctype string
		body  []byte
	}{
		{"image/png", []byte("\x89PNG\r\n\x1a\nbinary")},
		{"application/octet-stream", []byte{0, 1, 2, 3, 4}},
		{"image/svg+xml", svg},
	}
	for _, c := range cases {
		a := s.upload(t, c.body, c.ctype, "")
		page := htmlView(t, s, a.ID)
		contains(t, c.ctype, html.UnescapeString(page), c.ctype)
		contains(t, c.ctype, page, " B")
		if !regexp.MustCompile(`<a[^>]*href="/a/` + a.ID + `/raw"[^>]*\sdownload`).MatchString(page) {
			t.Errorf("%s: no download link", c.ctype)
		}
		lacks(t, c.ctype, page, "xmlns=")
		lacks(t, c.ctype, page, "window.pwned")
		assertCSPCompliant(t, page)
	}
}

func TestHostileTitle(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_INDEX=true")
	title := `<img src=x onerror=alert(1)>"'`
	a := s.upload(t, []byte("x"), "text/plain", title)
	page := htmlView(t, s, a.ID)
	lacks(t, "view", page, "<img src=x")
	contains(t, "view title", page, "<title>&lt;img src=x onerror=alert(1)&gt;")
	contains(t, "view h1", page, "&lt;img src=x onerror=alert(1)&gt;")
	assertCSPCompliant(t, page)

	hostileType := `text/plain; x="<img src=x onerror=alert(1)>"`
	typed := s.upload(t, []byte("x"), hostileType, "")
	eq(t, "hostile content_type", typed.ContentType, hostileType)
	bin := s.upload(t, []byte{0}, `application/octet-stream; x="<img src=x onerror=alert(1)>"`, "")
	for _, id := range []string{typed.ID, bin.ID} {
		page := htmlView(t, s, id)
		lacks(t, "hostile type view", page, "<img src=x")
		assertCSPCompliant(t, page)
	}

	resp, b := s.get(t, "/", basic("", testToken)...)
	assertPage(t, resp, http.StatusOK)
	lacks(t, "index", string(b), "<img src=x")
	contains(t, "index", string(b), "&lt;img src=x onerror=alert(1)&gt;")
	assertCSPCompliant(t, string(b))
}

var (
	linkHrefRe = regexp.MustCompile(`(?i)<link\b[^>]*\shref="([^"]*)"`)
	srcRe      = regexp.MustCompile(`(?i)\ssrc="([^"]*)"`)
	cssURLRe   = regexp.MustCompile(`url\(\s*["']?([^)"']+)`)
	jsExtRe    = regexp.MustCompile("https?://|['\"`]//[^/]")
)

func TestNoExternalRequests(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_INDEX=true")
	type page struct {
		srv  *server
		html string
	}
	pages := map[string]page{}
	for ctype, body := range map[string]string{
		"text/html":                "<p>x</p>",
		"text/markdown":            "# hi\n\n```go\nx := 1\n```\n\n![i](/static/favicon.svg)\n\n[ext](https://example.com)\n",
		"application/json":         `{"a":1}`,
		"text/plain":               "plain",
		"application/octet-stream": "\x00\x01",
	} {
		pages["view "+ctype] = page{s, htmlView(t, s, s.upload(t, []byte(body), ctype, "t").ID)}
	}
	resp, b := s.get(t, "/", basic("", testToken)...)
	assertPage(t, resp, http.StatusOK)
	pages["index on"] = page{s, string(b)}
	resp, b = s.get(t, "/a/"+unknownID, "Accept", "text/html")
	assertPage(t, resp, http.StatusNotFound)
	pages["not found"] = page{s, string(b)}

	off := newServer(t)
	resp, b = off.get(t, "/")
	assertPage(t, resp, http.StatusOK)
	pages["index off"] = page{off, string(b)}

	fetch := func(srv *server, ref string) {
		t.Helper()
		if !strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "//") {
			t.Errorf("non-local reference %q", ref)
			return
		}
		p, err := url.Parse(ref)
		if err != nil {
			t.Fatal(err)
		}
		resp, _ := srv.get(t, p.RequestURI())
		eq(t, "GET "+ref, resp.StatusCode, http.StatusOK)
		assertCommonHeaders(t, resp)
	}
	for name, pg := range pages {
		t.Run(name, func(t *testing.T) {
			assertCSPCompliant(t, pg.html)
			for _, re := range []*regexp.Regexp{linkHrefRe, srcRe} {
				for _, m := range re.FindAllStringSubmatch(pg.html, -1) {
					fetch(pg.srv, html.UnescapeString(m[1]))
				}
			}
		})
	}

	_, css := s.get(t, "/static/app.css")
	contains(t, "app.css follows the system theme", string(css), "prefers-color-scheme: dark")
	for _, m := range cssURLRe.FindAllStringSubmatch(string(css), -1) {
		ref := m[1]
		if !strings.Contains(ref, ":") && !strings.HasPrefix(ref, "/") {
			ref = path.Join("/static", ref)
		}
		fetch(s, ref)
	}
	_, js := s.get(t, "/static/app.js")
	if m := jsExtRe.FindString(string(js)); m != "" {
		t.Errorf("app.js contains external URL literal %q", m)
	}
}
