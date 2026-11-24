package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestUploadResponseShape(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	body := []byte("<h1>hi</h1>")
	before := time.Now().UTC()
	resp, b := s.do(t, http.MethodPost, "/api/artifacts?title=Latency+report", bytes.NewReader(body),
		append(bearer(), "Content-Type", "text/html")...)
	after := time.Now().UTC()
	eq(t, "status", resp.StatusCode, http.StatusCreated)
	eq(t, "Content-Type", resp.Header.Get("Content-Type"), "application/json")
	assertCommonHeaders(t, resp)

	var raw map[string]any
	decode(t, b, &raw)
	eq(t, "keys", strings.Join(slices.Sorted(maps.Keys(raw)), ","), "content_type,created_at,id,raw_url,sha256,size,title,url")

	var a artifactJSON
	decode(t, b, &a)
	if !idRe.MatchString(a.ID) {
		t.Errorf("id %q does not match the id format", a.ID)
	}
	eq(t, "title", a.Title, "Latency report")
	eq(t, "content_type", a.ContentType, "text/html")
	eq(t, "size", a.Size, int64(len(body)))
	eq(t, "sha256", a.SHA256, sha(body))
	created, err := time.Parse(time.RFC3339, a.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(a.CreatedAt, "Z") {
		t.Errorf("created_at %q is not UTC", a.CreatedAt)
	}
	if created.Before(before.Add(-time.Second)) || created.After(after.Add(time.Second)) {
		t.Errorf("created_at %v outside [%v, %v]", created, before, after)
	}
	eq(t, "url", a.URL, baseURL+"/a/"+a.ID)
	eq(t, "raw_url", a.RawURL, a.URL+"/raw")
	eq(t, "Location", resp.Header.Get("Location"), a.URL)
}

func TestUploadContentKinds(t *testing.T) {
	t.Parallel()
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	cases := []struct {
		name, ctype, stored string
		body                []byte
	}{
		{"html", "text/html", "", []byte("<p>hello</p>")},
		{"markdown with params", "text/markdown; charset=utf-8", "", []byte("# Title\n")},
		{"json", "application/json", "", []byte(`{"a":[1,2]}`)},
		{"invalid json", "application/json", "", []byte(`{"a":`)},
		{"ld+json", "application/ld+json", "", []byte(`{"@id":"x"}`)},
		{"plain", "text/plain", "", []byte("just text\n")},
		{"javascript", "application/javascript", "", []byte("console.log(1)")},
		{"go source", "text/x-go", "", []byte("package main\n")},
		{"png all bytes", "image/png", "", allBytes},
		{"svg with script", "image/svg+xml", "", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>window.pwned=1</script></svg>`)},
		{"no content type", "", "application/octet-stream", []byte{0, 1, 2, 3}},
		{"mixed case type", "TEXT/HTML; Charset=UTF-8", "", []byte("<b>x</b>")},
	}
	s := newServer(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up := s.upload(t, c.body, c.ctype, "")
			want := c.stored
			if want == "" {
				want = c.ctype
			}
			eq(t, "content_type", up.ContentType, want)
			eq(t, "size", up.Size, int64(len(c.body)))
			eq(t, "sha256", up.SHA256, sha(c.body))

			resp, b := s.get(t, "/api/artifacts/"+up.ID)
			eq(t, "metadata status", resp.StatusCode, http.StatusOK)
			eq(t, "metadata Content-Type", resp.Header.Get("Content-Type"), "application/json")
			assertCommonHeaders(t, resp)
			var got artifactJSON
			decode(t, b, &got)
			eq(t, "metadata", got, up)

			resp, b = s.get(t, "/a/"+up.ID+"/raw")
			assertRaw(t, resp, b, c.body, up)
		})
	}
}

func TestUploadSameContentTwice(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	a := s.upload(t, []byte("same"), "text/plain", "")
	b := s.upload(t, []byte("same"), "text/plain", "")
	if a.ID == b.ID {
		t.Errorf("same id %s for two uploads", a.ID)
	}
	eq(t, "sha256", a.SHA256, b.SHA256)
}

func TestUploadConcurrent(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	const n = 10
	replies := make([]reply, n)
	bodies := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			replies[i], bodies[i], errs[i] = s.tryDo(t, http.MethodPost, "/api/artifacts",
				strings.NewReader(fmt.Sprintf("body number %d", i)), append(bearer(), "Content-Type", "text/plain")...)
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if replies[i].StatusCode != http.StatusCreated {
			t.Fatalf("upload %d status = %d, body %s", i, replies[i].StatusCode, bodies[i])
		}
		var a artifactJSON
		decode(t, bodies[i], &a)
		if seen[a.ID] {
			t.Errorf("duplicate id %s", a.ID)
		}
		seen[a.ID] = true
		_, b := s.get(t, "/a/"+a.ID+"/raw")
		eq(t, "raw body", string(b), fmt.Sprintf("body number %d", i))
	}
}

func TestTitle(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	hdr := append(bearer(), "Content-Type", "text/plain")
	post := func(query string) (reply, []byte) {
		return s.do(t, http.MethodPost, "/api/artifacts"+query, strings.NewReader("x"), hdr...)
	}

	a := s.upload(t, []byte("x"), "text/plain", "")
	eq(t, "default title", a.Title, "")
	resp, b := s.get(t, "/api/artifacts/"+a.ID)
	eq(t, "status", resp.StatusCode, http.StatusOK)
	contains(t, "metadata", string(b), `"title":""`)

	max := strings.Repeat("é", 200)
	eq(t, "200 runes title", s.upload(t, []byte("x"), "text/plain", max).Title, max)

	resp, b = post("?title=" + strings.Repeat("%C3%A9", 201))
	assertJSONError(t, resp, b, http.StatusBadRequest)
	resp, b = post("?title=%ff")
	assertJSONError(t, resp, b, http.StatusBadRequest)

	uni := "Zürich + Köln & 東京 ✓"
	eq(t, "unicode title", s.upload(t, []byte("x"), "text/plain", uni).Title, uni)

	eq(t, "blobs after rejects", len(dataDirEntries(t, s.dataDir)), 3)
}

func TestEmptyBody(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	resp, b := s.do(t, http.MethodPost, "/api/artifacts", bytes.NewReader(nil), bearer()...)
	assertJSONError(t, resp, b, http.StatusBadRequest)

	c, br := s.rawConn(t)
	writeAll(t, c, "POST /api/artifacts HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+testToken+
		"\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n")
	resp, b = readResponse(t, br)
	assertJSONError(t, resp, b, http.StatusBadRequest)

	eq(t, "stored blobs", len(dataDirEntries(t, s.dataDir)), 0)
}

func TestSizeLimit(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_MAX_SIZE=1024")
	ok := s.upload(t, bytes.Repeat([]byte("a"), 1024), "text/plain", "")

	resp, b := s.do(t, http.MethodPost, "/api/artifacts", bytes.NewReader(bytes.Repeat([]byte("a"), 1025)), bearer()...)
	assertJSONError(t, resp, b, http.StatusRequestEntityTooLarge)

	c, br := s.rawConn(t)
	writeAll(t, c, "POST /api/artifacts HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+testToken+
		"\r\nTransfer-Encoding: chunked\r\n\r\n401\r\n"+strings.Repeat("a", 1025)+"\r\n0\r\n\r\n")
	resp, b = readResponse(t, br)
	assertJSONError(t, resp, b, http.StatusRequestEntityTooLarge)

	got := dataDirEntries(t, s.dataDir)
	if len(got) != 1 || got[0] != ok.ID {
		t.Errorf("blobs = %v, want only %s", got, ok.ID)
	}
}

func TestSizeLimitEnforcedWhileReading(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_MAX_SIZE=1024")
	c, br := s.rawConn(t)
	writeAll(t, c, "POST /api/artifacts HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+testToken+
		"\r\nContent-Length: 104857600\r\n\r\n"+strings.Repeat("a", 1025))
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	resp, b := readResponse(t, br)
	assertJSONError(t, resp, b, http.StatusRequestEntityTooLarge)
}

func TestDefaultSizeLimit(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.upload(t, bytes.Repeat([]byte("a"), 10485760), "application/octet-stream", "")
	resp, b := s.do(t, http.MethodPost, "/api/artifacts", bytes.NewReader(bytes.Repeat([]byte("a"), 10485761)), bearer()...)
	assertJSONError(t, resp, b, http.StatusRequestEntityTooLarge)
}

func TestAuthMatrix(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	target := s.upload(t, []byte("keep me"), "text/plain", "")
	tok := testToken
	rejected := map[string][]string{
		"none":            nil,
		"wrong":           {"Authorization", "Bearer wrong"},
		"empty token":     {"Authorization", "Bearer "},
		"token plus x":    {"Authorization", "Bearer " + tok + "x"},
		"token minus one": {"Authorization", "Bearer " + tok[:len(tok)-1]},
		"bare token":      {"Authorization", tok},
		"basic":           basic("x", tok),
		"token scheme":    {"Authorization", "Token " + tok},
	}
	for name, hdr := range rejected {
		t.Run("rejected/"+name, func(t *testing.T) {
			resp, b := s.do(t, http.MethodPost, "/api/artifacts", strings.NewReader("data"), hdr...)
			assertJSONError(t, resp, b, http.StatusUnauthorized)
			if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
			}
			resp, b = s.do(t, http.MethodDelete, "/api/artifacts/"+target.ID, nil, hdr...)
			assertJSONError(t, resp, b, http.StatusUnauthorized)
			if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
			}
		})
	}
	if got := dataDirEntries(t, s.dataDir); len(got) != 1 {
		t.Errorf("rejected uploads stored blobs: %v", got)
	}
	resp, _ := s.get(t, "/a/"+target.ID+"/raw")
	eq(t, "target after rejected deletes", resp.StatusCode, http.StatusOK)

	for _, scheme := range []string{"Bearer", "bearer"} {
		resp, _ := s.do(t, http.MethodPost, "/api/artifacts", strings.NewReader("data"), "Authorization", scheme+" "+tok)
		eq(t, scheme+" upload", resp.StatusCode, http.StatusCreated)
	}

	wrong := []string{"Authorization", "Bearer wrong"}
	for name, hdr := range map[string][]string{"no auth": nil, "wrong bearer": wrong} {
		for _, path := range []string{"/api/artifacts/" + target.ID, "/a/" + target.ID + "/raw", "/a/" + target.ID} {
			resp, _ := s.get(t, path, hdr...)
			eq(t, name+" GET "+path, resp.StatusCode, http.StatusOK)
		}
		resp, _ := s.get(t, "/a/"+target.ID, append(hdr, "Accept", "text/html")...)
		eq(t, name+" GET view", resp.StatusCode, http.StatusOK)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	keep := s.upload(t, []byte("keep"), "text/plain", "")
	gone := s.upload(t, []byte("gone"), "text/plain", "")
	if got := dataDirEntries(t, s.dataDir); len(got) != 2 {
		t.Fatalf("blobs = %v", got)
	}

	resp, b := s.delete(t, gone.ID)
	eq(t, "status", resp.StatusCode, http.StatusNoContent)
	eq(t, "body length", len(b), 0)
	assertCommonHeaders(t, resp)

	resp, b = s.get(t, "/api/artifacts/"+gone.ID)
	assertJSONError(t, resp, b, http.StatusNotFound)
	resp, b = s.get(t, "/a/"+gone.ID+"/raw")
	assertJSONError(t, resp, b, http.StatusNotFound)
	resp, _ = s.get(t, "/a/"+gone.ID, "Accept", "text/html")
	assertPage(t, resp, http.StatusNotFound)
	resp, b = s.get(t, "/a/"+gone.ID)
	assertJSONError(t, resp, b, http.StatusNotFound)

	resp, b = s.delete(t, gone.ID)
	assertJSONError(t, resp, b, http.StatusNotFound)
	if got := dataDirEntries(t, s.dataDir); len(got) != 1 || got[0] != keep.ID {
		t.Errorf("blobs = %v, want only %s", got, keep.ID)
	}

	resp, b = s.delete(t, unknownID)
	assertJSONError(t, resp, b, http.StatusNotFound)
	resp, b = s.delete(t, "not-an-id")
	assertJSONError(t, resp, b, http.StatusNotFound)
	resp, b = s.do(t, http.MethodDelete, "/api/artifacts/not-an-id", nil)
	assertJSONError(t, resp, b, http.StatusUnauthorized)
}

func TestUnknownAndHostileIDs(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	known := s.upload(t, []byte("still here"), "text/plain", "")
	ids := []string{
		unknownID,
		strings.Repeat("A", 21),
		strings.Repeat("A", 23),
		"AAAAAAAAAAA.AAAAAAAAAA",
		"AAAAAAAAAAA~AAAAAAAAAA",
		".%2F.%2F.%2F%2F..%2Fartitrove.db",
		"..%2F..%2F..%2Fetc%2Fpasswd",
	}
	notSensitive := func(b []byte) {
		t.Helper()
		lacks(t, "body", string(b), "SQLite format 3")
		lacks(t, "body", string(b), "root:")
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			for _, path := range []string{"/api/artifacts/" + id, "/a/" + id + "/raw", "/a/" + id} {
				resp, b := s.get(t, path)
				assertJSONError(t, resp, b, http.StatusNotFound)
				notSensitive(b)
			}
			resp, b := s.get(t, "/a/"+id, "Accept", "text/html")
			assertPage(t, resp, http.StatusNotFound)
			notSensitive(b)
			resp, b = s.delete(t, id)
			assertJSONError(t, resp, b, http.StatusNotFound)
			notSensitive(b)
		})
	}
	resp, b := s.get(t, "/a/"+known.ID+"/raw")
	assertRaw(t, resp, b, []byte("still here"), known)
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	resp, b := s.get(t, "/healthz")
	eq(t, "status", resp.StatusCode, http.StatusOK)
	eq(t, "Content-Type", resp.Header.Get("Content-Type"), "application/json")
	assertCommonHeaders(t, resp)
	var got map[string]string
	decode(t, b, &got)
	if len(got) != 1 || got["status"] != "ok" {
		t.Errorf("body = %s", b)
	}
}

func TestCommonHeadersEverywhere(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_MAX_SIZE=1024", "ARTITROVE_INDEX=true")
	a := s.upload(t, []byte("<p>x</p>"), "text/html", "t")
	gone := s.upload(t, []byte("gone"), "text/plain", "")
	idx := basic("", testToken)
	hdrPost := append(bearer(), "Content-Type", "text/plain")

	requests := []struct {
		name, method, path string
		body               string
		hdr                []string
		status             int
	}{
		{"healthz", "GET", "/healthz", "", nil, 200},
		{"landing", "GET", "/", "", idx, 200},
		{"landing 401", "GET", "/", "", nil, 401},
		{"app.css", "GET", "/static/app.css", "", nil, 200},
		{"app.js", "GET", "/static/app.js", "", nil, 200},
		{"highlight.css", "GET", "/static/highlight.css", "", nil, 200},
		{"static 404", "GET", "/static/nope.css", "", nil, 404},
		{"static dir 404", "GET", "/static/", "", nil, 404},
		{"upload 201", "POST", "/api/artifacts", "x", hdrPost, 201},
		{"upload 400", "POST", "/api/artifacts", "", hdrPost, 400},
		{"upload 401", "POST", "/api/artifacts", "x", nil, 401},
		{"upload 413", "POST", "/api/artifacts", strings.Repeat("a", 1025), hdrPost, 413},
		{"metadata 200", "GET", "/api/artifacts/" + a.ID, "", nil, 200},
		{"metadata 404", "GET", "/api/artifacts/" + unknownID, "", nil, 404},
		{"raw 200", "GET", "/a/" + a.ID + "/raw", "", nil, 200},
		{"raw 404", "GET", "/a/" + unknownID + "/raw", "", nil, 404},
		{"view 200", "GET", "/a/" + a.ID, "", []string{"Accept", "text/html"}, 200},
		{"view 404", "GET", "/a/" + unknownID, "", []string{"Accept", "text/html"}, 404},
		{"view as raw", "GET", "/a/" + a.ID, "", nil, 200},
		{"delete 204", "DELETE", "/api/artifacts/" + gone.ID, "", bearer(), 204},
		{"mux 404", "GET", "/nope", "", nil, 404},
		{"mux 405", "PUT", "/api/artifacts/x", "", nil, 405},
	}
	for _, r := range requests {
		t.Run(r.name, func(t *testing.T) {
			resp, _ := s.do(t, r.method, r.path, strings.NewReader(r.body), r.hdr...)
			eq(t, "status", resp.StatusCode, r.status)
			assertCommonHeaders(t, resp)
		})
	}
}
