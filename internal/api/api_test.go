package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niekcandaele/artitrove/internal/api"
	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/config"
	"github.com/niekcandaele/artitrove/internal/store"
)

const (
	token   = "test-token-0123456789"
	maxSize = 64
	unknown = "AAAAAAAAAAAAAAAAAAAAAA"
)

var cfg = config.Config{Token: token, BaseURL: "https://trove.example", MaxSize: maxSize}

type env struct {
	mux *http.ServeMux
	st  *store.Store
	dir string
}

func newEnv(t *testing.T) env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) // the 500 test closes it first
	mux := http.NewServeMux()
	api.Register(mux, cfg, st)
	return env{mux, st, dir}
}

func (e env) do(method, target string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, body)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func auth() map[string]string { return map[string]string{"Authorization": "Bearer " + token} }

func (e env) upload(t *testing.T, body, ct, title string) map[string]any {
	t.Helper()
	hdr := auth()
	if ct != "" {
		hdr["Content-Type"] = ct
	}
	target := "/api/artifacts"
	if title != "" {
		target += "?title=" + title
	}
	rec := e.do("POST", target, strings.NewReader(body), hdr)
	if rec.Code != 201 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status %d, want %d (%s)", rec.Code, code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m["error"] == "" || len(m) != 1 {
		t.Fatalf("bad error body %q", rec.Body)
	}
}

func (e env) assertNothingStored(t *testing.T) {
	t.Helper()
	list, err := e.st.List(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("list %v %v", list, err)
	}
	for _, pat := range []string{".upload-*", "blobs/*"} {
		m, _ := filepath.Glob(filepath.Join(e.dir, pat))
		if len(m) != 0 {
			t.Fatalf("leftover files %v", m)
		}
	}
}

var badIDs = []string{
	unknown, "short", "AAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAAA",
	"AAAAAAAAAAAAAAAAAAAA+A", "AAAAAAAAAAAAAAAAAAAA==", "AAAAAAAAAAAAAAAAAAAA.A", "AAAAAAAAAAAAAAAAAAAA%00",
	"..%2F..%2Fartitrove.db", unknown + "%2F", "%41AAAAAAAAAAAAAAAAAAAA%2B",
}

func TestAuthMatrix(t *testing.T) {
	e := newEnv(t)
	m := e.upload(t, "hello", "text/plain", "")
	id := m["id"].(string)
	headers := []string{
		"", "Basic dXNlcjp0b2tlbg==", "Basic " + token, "Bearer", "Bearer ", "Bearer" + token, "Bearer  " + token,
		"Bearer " + token + " ", "Bearer " + token[:len(token)-1], "Bearer " + token + "x", "Bearer wrong-token-of-same-",
	}
	for _, h := range headers {
		hdr := map[string]string{}
		if h != "" {
			hdr["Authorization"] = h
		}
		for _, rt := range []struct{ method, target string }{
			{"POST", "/api/artifacts"}, {"DELETE", "/api/artifacts/" + id},
			{"DELETE", "/api/artifacts/" + unknown}, {"DELETE", "/api/artifacts/short"},
		} {
			rec := e.do(rt.method, rt.target, strings.NewReader("x"), hdr)
			assertJSONError(t, rec, 401)
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("%q %s: no WWW-Authenticate", h, rt.method)
			}
		}
		for _, target := range []string{"/api/artifacts/" + id, "/a/" + id + "/raw", "/healthz"} {
			rec := e.do("GET", target, nil, hdr)
			if rec.Code != 200 || rec.Header().Get("WWW-Authenticate") != "" {
				t.Fatalf("%q GET %s: %d", h, target, rec.Code)
			}
		}
	}
	// the artifact survived every rejected delete
	if rec := e.do("GET", "/api/artifacts/"+id, nil, nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	// two Authorization headers: only the first counts
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/artifacts", strings.NewReader("x"))
	req.Header.Add("Authorization", "Bearer wrong")
	req.Header.Add("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	assertJSONError(t, rec, 401)
	// correct token; the scheme is case-insensitive
	if rec := e.do("POST", "/api/artifacts", strings.NewReader("x"), auth()); rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	if rec := e.do("POST", "/api/artifacts", strings.NewReader("x"), map[string]string{"Authorization": "bEARER " + token}); rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	if rec := e.do("DELETE", "/api/artifacts/"+id, nil, auth()); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	if rec := e.do("DELETE", "/api/artifacts/"+unknown, nil, auth()); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
	if rec := e.do("DELETE", "/api/artifacts/short", nil, auth()); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) {
	f.t.Error("body read before auth")
	return 0, io.EOF
}

func TestUnauthorizedNeverReadsBody(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/artifacts", failReader{t}, map[string]string{"Authorization": "Bearer nope"})
	rec := e.do("POST", "/api/artifacts", bytes.NewReader(make([]byte, 1<<20)), map[string]string{"Authorization": "Bearer nope"})
	assertJSONError(t, rec, 401)
	e.assertNothingStored(t)
}

func TestCreateHappyPath(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/api/artifacts?title=Hello+there", strings.NewReader("# hi"),
		map[string]string{"Authorization": "Bearer " + token, "Content-Type": "text/markdown; charset=utf-8"})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", rec.Code, rec.Header())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 8 {
		t.Fatalf("keys %v", m)
	}
	id := m["id"].(string)
	if !artifact.ValidID(id) {
		t.Fatal(id)
	}
	url := "https://trove.example/a/" + id
	if rec.Header().Get("Location") != url || m["url"] != url || m["raw_url"] != url+"/raw" {
		t.Fatalf("urls %v %s", m, rec.Header().Get("Location"))
	}
	if m["title"] != "Hello there" || m["content_type"] != "text/markdown; charset=utf-8" || m["size"] != float64(4) {
		t.Fatal(m)
	}
	if sum := sha256.Sum256([]byte("# hi")); m["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatal(m["sha256"])
	}
	ts := m["created_at"].(string)
	if _, err := time.Parse(time.RFC3339, ts); err != nil || !strings.HasSuffix(ts, "Z") {
		t.Fatal(ts)
	}
	get := e.do("GET", "/api/artifacts/"+id, nil, nil)
	if get.Code != 200 || get.Body.String() != rec.Body.String() {
		t.Fatalf("get differs:\n%s\n%s", get.Body, rec.Body)
	}
	if e.upload(t, "# hi", "", "")["id"] == id {
		t.Fatal("ids not distinct")
	}
}

func TestContentType(t *testing.T) {
	e := newEnv(t)
	if ct := e.upload(t, "x", "", "")["content_type"]; ct != artifact.DefaultContentType {
		t.Fatal(ct)
	}
	if ct := e.upload(t, "x", "application/x-www-form-urlencoded", "")["content_type"]; ct != "application/x-www-form-urlencoded" {
		t.Fatal(ct)
	}
	long := "text/" + strings.Repeat("a", api.MaxContentTypeLen-5)
	e.upload(t, "x", long, "")
	rec := e.do("POST", "/api/artifacts", strings.NewReader("x"), map[string]string{"Authorization": "Bearer " + token, "Content-Type": long + "a"})
	assertJSONError(t, rec, 400)
}

func TestTitle(t *testing.T) {
	e := newEnv(t)
	post := func(q string) *httptest.ResponseRecorder {
		return e.do("POST", "/api/artifacts"+q, strings.NewReader("x"), auth())
	}
	for q, want := range map[string]string{
		"": "", "?title=": "", "?title=a+b": "a b", "?title=a%20b": "a b", "?title=a&title=b": "a",
		"?title=" + strings.Repeat("%C3%A9", 200):       strings.Repeat("é", 200),
		"?title=%3Cscript%3Ealert(1)%3C%2Fscript%3E%00": "<script>alert(1)</script>\x00",
	} {
		rec := post(q)
		var m map[string]any
		if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m["title"] != want {
			t.Fatalf("%q: %d %s", q, rec.Code, rec.Body)
		}
	}
	for _, q := range []string{
		"?title=" + strings.Repeat("%C3%A9", 201), "?title=" + strings.Repeat("a", 201), "?title=%FF", "?title=%zz",
	} {
		assertJSONError(t, post(q), 400)
	}
}

func TestBodyLimits(t *testing.T) {
	e := newEnv(t)
	hdr := auth()
	big := strings.Repeat("a", maxSize+1)
	assertJSONError(t, e.do("POST", "/api/artifacts", strings.NewReader(""), hdr), 400)
	assertJSONError(t, e.do("POST", "/api/artifacts", strings.NewReader(big), hdr), 413)
	// unknown length
	assertJSONError(t, e.do("POST", "/api/artifacts", io.MultiReader(strings.NewReader(big)), hdr), 413)
	// understated Content-Length is not trusted
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/artifacts", strings.NewReader(big))
	req.Header.Set("Authorization", "Bearer "+token)
	req.ContentLength = 10
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	assertJSONError(t, rec, 413)
	e.assertNothingStored(t)
	if rec := e.do("POST", "/api/artifacts", strings.NewReader(big[:maxSize]), hdr); rec.Code != 201 {
		t.Fatal(rec.Code)
	}
}

func TestUnknownAndMalformedIDs(t *testing.T) {
	e := newEnv(t)
	for _, id := range badIDs {
		assertJSONError(t, e.do("GET", "/api/artifacts/"+id, nil, nil), 404)
		assertJSONError(t, e.do("GET", "/a/"+id+"/raw", nil, nil), 404)
		assertJSONError(t, e.do("DELETE", "/api/artifacts/"+id, nil, auth()), 404)
	}
	if rec := e.do("GET", "/api/artifacts/", nil, nil); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	id := e.upload(t, "bye", "text/plain", "")["id"].(string)
	rec := e.do("DELETE", "/api/artifacts/"+id, nil, auth())
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatal(rec.Code, rec.Body)
	}
	assertJSONError(t, e.do("GET", "/api/artifacts/"+id, nil, nil), 404)
	assertJSONError(t, e.do("GET", "/a/"+id+"/raw", nil, nil), 404)
	assertJSONError(t, e.do("DELETE", "/api/artifacts/"+id, nil, auth()), 404)
	e.assertNothingStored(t)
}

func TestRaw(t *testing.T) {
	e := newEnv(t)
	html := "<script>alert(1)</script>"
	m := e.upload(t, html, "text/plain; charset=utf-8", "")
	id, sha := m["id"].(string), m["sha256"].(string)
	rec := e.do("GET", "/a/"+id+"/raw", nil, nil)
	want := map[string]string{
		"Content-Type":            "text/plain; charset=utf-8",
		"Content-Security-Policy": "sandbox allow-scripts",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "public, max-age=31536000, immutable",
		"ETag":                    `"` + sha + `"`,
		"Content-Length":          "25",
	}
	if rec.Code != 200 || rec.Body.String() != html {
		t.Fatal(rec.Code, rec.Body)
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	// no sniffing: default type stays octet-stream
	id2 := e.upload(t, "<html><body>x</body></html>", "", "")["id"].(string)
	if ct := e.do("GET", "/a/"+id2+"/raw", nil, nil).Header().Get("Content-Type"); ct != artifact.DefaultContentType {
		t.Fatal(ct)
	}
	// HEAD
	head := e.do("HEAD", "/a/"+id+"/raw", nil, nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Security-Policy") == "" {
		t.Fatal(head.Code, head.Header())
	}
	// conditional
	for _, inm := range []string{`"` + sha + `"`, `W/"` + sha + `"`, "*"} {
		c := e.do("GET", "/a/"+id+"/raw", nil, map[string]string{"If-None-Match": inm})
		if c.Code != 304 || c.Body.Len() != 0 || c.Header().Get("ETag") == "" {
			t.Fatalf("%s: %d", inm, c.Code)
		}
	}
	if c := e.do("GET", "/a/"+id+"/raw", nil, map[string]string{"If-None-Match": `"other"`}); c.Code != 200 {
		t.Fatal(c.Code)
	}
	// range
	r := e.do("GET", "/a/"+id+"/raw", nil, map[string]string{"Range": "bytes=0-3"})
	if r.Code != 206 || r.Body.String() != "<scr" || r.Header().Get("Content-Range") == "" || r.Header().Get("Content-Security-Policy") == "" {
		t.Fatal(r.Code, r.Body, r.Header())
	}
	r = e.do("GET", "/a/"+id+"/raw", nil, map[string]string{"Range": "bytes=999-"})
	if r.Code != 416 || r.Header().Get("Content-Security-Policy") == "" || r.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(r.Code, r.Header())
	}
}

func TestRawBinaryRoundTrip(t *testing.T) {
	e := newEnv(t)
	b := make([]byte, 0x40)
	for i := range b {
		b[i] = byte(i)
	}
	id := e.upload(t, string(b), "application/octet-stream", "")["id"].(string)
	if got := e.do("GET", "/a/"+id+"/raw", nil, nil).Body.Bytes(); !bytes.Equal(got, b) {
		t.Fatalf("%x", got)
	}
}

func TestRawMissingBlobIs404(t *testing.T) {
	e := newEnv(t)
	id := e.upload(t, "x", "text/plain", "")["id"].(string)
	if err := os.Remove(filepath.Join(e.dir, "blobs", id)); err != nil {
		t.Fatal(err)
	}
	assertJSONError(t, e.do("GET", "/a/"+id+"/raw", nil, nil), 404)
}

func TestRawHandlerUnderOtherPattern(t *testing.T) {
	e := newEnv(t)
	id := e.upload(t, "x", "text/plain", "")["id"].(string)
	mux := http.NewServeMux()
	mux.Handle("GET /x/{id}", api.RawHandler(e.st))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/x/"+id, nil))
	if rec.Code != 200 || rec.Header().Get("Content-Security-Policy") != "sandbox allow-scripts" {
		t.Fatal(rec.Code, rec.Header())
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/healthz", nil, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := e.do("HEAD", "/healthz", nil, nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}

func TestInternalError(t *testing.T) {
	e := newEnv(t)
	if err := e.st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []struct{ method, target string }{
		{"GET", "/api/artifacts/" + unknown}, {"POST", "/api/artifacts"},
		{"DELETE", "/api/artifacts/" + unknown}, {"GET", "/a/" + unknown + "/raw"},
	} {
		rec := e.do(rt.method, rt.target, strings.NewReader("x"), auth())
		assertJSONError(t, rec, 500)
		if strings.TrimSpace(rec.Body.String()) != `{"error":"internal error"}` {
			t.Fatal(rec.Body)
		}
		for _, h := range []string{"Content-Security-Policy", "Cache-Control", "ETag"} {
			if rec.Header().Get(h) != "" {
				t.Fatalf("%s set on 500", h)
			}
		}
	}
}

func TestOverTCP(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.mux)
	defer srv.Close()
	do := func(method, path string, body string) *http.Response {
		req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := do("POST", "/api/artifacts", strings.Repeat("a", maxSize+1))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatal(resp.StatusCode)
	}
	resp = do("POST", "/api/artifacts", "hello")
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatal(err, resp.StatusCode)
	}
	_ = resp.Body.Close()
	id := m["id"].(string)
	resp = do("GET", "/a/"+id+"/raw", "")
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
	resp = do("DELETE", "/api/artifacts/"+id, "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatal(resp.StatusCode)
	}
}

func TestWrongMethodAndUnknownPathAreJSON(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ method, target, allow string }{
		{"GET", "/api/artifacts", "POST"},
		{"PUT", "/api/artifacts/" + unknown, "DELETE, GET, HEAD"},
		{"GET", "/api/nope", ""},
		{"GET", "/api/artifacts/", ""},
	} {
		rec := e.do(c.method, c.target, nil, nil)
		if c.allow == "" {
			assertJSONError(t, rec, 404)
			continue
		}
		assertJSONError(t, rec, 405)
		if got := rec.Header().Get("Allow"); got != c.allow {
			t.Errorf("%s %s: Allow %q", c.method, c.target, got)
		}
	}
}
