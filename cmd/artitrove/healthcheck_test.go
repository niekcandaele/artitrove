package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthURL(t *testing.T) {
	ok := map[string]string{
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"[::]:8080":      "http://127.0.0.1:8080/healthz",
		"localhost:8080": "http://localhost:8080/healthz",
		"[::1]:8080":     "http://[::1]:8080/healthz",
		"192.168.1.5:80": "http://192.168.1.5:80/healthz",
	}
	for in, want := range ok {
		got, err := healthURL(in)
		if err != nil || got != want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"8080", ":http", ""} {
		if got, err := healthURL(in); err == nil {
			t.Errorf("healthURL(%q) = %q, want error", in, got)
		}
	}
}

func listenOf(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if err := healthcheck(t.Context(), listenOf(srv)); err != nil {
		t.Errorf("200: %v", err)
	}

	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	if err := healthcheck(t.Context(), listenOf(srv)); err != nil {
		t.Errorf("with dead proxy: %v", err)
	}

	status = http.StatusServiceUnavailable
	if err := healthcheck(t.Context(), listenOf(srv)); err == nil {
		t.Error("503: want error")
	}
}

func TestHealthcheckUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := listenOf(srv)
	srv.Close()
	if err := healthcheck(t.Context(), addr); err == nil {
		t.Error("closed listener: want error")
	}
}

func TestHealthcheckDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := healthcheck(ctx, listenOf(srv)); err == nil {
		t.Error("slow server: want error")
	}
}
