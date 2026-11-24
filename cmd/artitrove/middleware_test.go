package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/x-test")
		_, _ = w.Write([]byte("body"))
	})
	mux.HandleFunc("GET /empty", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := securityHeaders(mux)
	cases := []struct {
		method, path string
		status       int
	}{
		{"GET", "/ok", 200},
		{"GET", "/empty", 204},
		{"GET", "/missing", 404},
		{"POST", "/ok", 405},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.status {
			t.Errorf("%s %s: status %d, want %d", c.method, c.path, rec.Code, c.status)
		}
		want := map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
			"X-Robots-Tag":           "noindex",
		}
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s %s: %s = %q, want %q", c.method, c.path, k, got, v)
			}
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/ok", nil))
	if got := rec.Header().Get("Content-Type"); got != "text/x-test" {
		t.Errorf("handler Content-Type lost: %q", got)
	}
}
