package e2e

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIndexOff(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	a := s.upload(t, []byte("x"), "text/plain", "Secret title")
	for _, path := range []string{"/", "/?index=true"} {
		for _, hdr := range [][]string{nil, basic("", testToken)} {
			resp, b := s.get(t, path, hdr...)
			assertPage(t, resp, http.StatusOK)
			contains(t, "landing", string(b), "/api/artifacts")
			contains(t, "landing", string(b), baseURL)
			lacks(t, "landing", string(b), a.ID)
			lacks(t, "landing", string(b), "Secret title")
		}
	}
	resp, b := s.get(t, "/api/artifacts", bearer()...)
	if resp.StatusCode == http.StatusOK {
		t.Error("GET /api/artifacts answered 200")
	}
	lacks(t, "listing", string(b), a.ID)
}

func TestIndexAuth(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_INDEX=true")
	rejected := map[string][]string{
		"none":                nil,
		"wrong password":      basic("user", "wrong"),
		"token as username":   basic(testToken, ""),
		"bearer":              bearer(),
		"password plus x":     basic("user", testToken+"x"),
		"password minus last": basic("user", testToken[:len(testToken)-1]),
	}
	for name, hdr := range rejected {
		t.Run("rejected/"+name, func(t *testing.T) {
			resp, b := s.get(t, "/", hdr...)
			assertJSONError(t, resp, b, http.StatusUnauthorized)
			if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic") {
				t.Errorf("WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
			}
		})
	}
	for _, user := range []string{"", "admin", "x"} {
		resp, _ := s.get(t, "/", basic(user, testToken)...)
		assertPage(t, resp, http.StatusOK)
	}
}

var indexIDRe = regexp.MustCompile(`href="/a/([A-Za-z0-9_-]{22})"`)

func indexIDs(t *testing.T, s *server) []string {
	t.Helper()
	resp, b := s.get(t, "/", basic("", testToken)...)
	assertPage(t, resp, http.StatusOK)
	var ids []string
	for _, m := range indexIDRe.FindAllStringSubmatch(string(b), -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func TestIndexListing(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_INDEX=true")
	if ids := indexIDs(t, s); len(ids) != 0 {
		t.Errorf("empty store lists %v", ids)
	}
	// created_at has whole-second precision; the pauses make newest-first independent of tie-breaking.
	a := s.upload(t, []byte("a"), "text/plain", "alpha")
	time.Sleep(1100 * time.Millisecond)
	b := s.upload(t, []byte("# b"), "text/markdown", "bravo")
	time.Sleep(1100 * time.Millisecond)
	c := s.upload(t, []byte("{}"), "application/json", "")
	want := []string{c.ID, b.ID, a.ID}
	if got := indexIDs(t, s); !slices.Equal(got, want) {
		t.Errorf("order = %v, want newest first %v", got, want)
	}

	_, body := s.get(t, "/", basic("", testToken)...)
	page := string(body)
	for _, want := range []string{"alpha", "text/plain", "bravo", "text/markdown", "application/json", c.ID} {
		contains(t, "index", page, want)
	}
	contains(t, "filter", page, `type="search"`)

	if resp, _ := s.delete(t, b.ID); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	want = []string{c.ID, a.ID}
	if got := indexIDs(t, s); !slices.Equal(got, want) {
		t.Errorf("after delete = %v, want %v", got, want)
	}
}

func TestIndexDoesNotGateReads(t *testing.T) {
	t.Parallel()
	s := newServer(t, "ARTITROVE_INDEX=true")
	a := s.upload(t, []byte("open"), "text/plain", "")
	resp, _ := s.get(t, "/api/artifacts/"+a.ID)
	eq(t, "metadata", resp.StatusCode, http.StatusOK)
	resp, b := s.get(t, "/a/"+a.ID+"/raw")
	assertRaw(t, resp, b, []byte("open"), a)
	htmlView(t, s, a.ID)
}
