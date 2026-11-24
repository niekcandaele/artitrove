package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/niekcandaele/artitrove/internal/config"
	"github.com/niekcandaele/artitrove/internal/store"
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
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), c.method, c.path, nil))
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
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ok", nil))
	if got := rec.Header().Get("Content-Type"); got != "text/x-test" {
		t.Errorf("handler Content-Type lost: %q", got)
	}
}

func TestReadDeadlines(t *testing.T) {
	defaultReadTimeout = 300 * time.Millisecond
	t.Cleanup(func() { defaultReadTimeout = 30 * time.Second })
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Config{Token: "test-token-0123456789abcdef", BaseURL: "http://x", MaxSize: 1 << 20}
	addr := serveForTest(t, newHandler(cfg, st))

	dial := func() net.Conn {
		c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return c
	}

	// An unauthenticated request that never sends its declared body is cut off, including an
	// upload with a wrong token: only a valid token extends the deadline.
	for _, req := range []string{
		"GET /healthz HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n\r\n",
		"POST /api/artifacts HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer wrong-token-0123456789\r\nContent-Length: 1000\r\n\r\n",
	} {
		c := dial()
		_, _ = io.WriteString(c, req)
		start := time.Now()
		if _, err := io.ReadAll(c); err != nil {
			t.Fatalf("%q: connection was not closed by the server: %v", req, err)
		}
		if time.Since(start) > 3*time.Second {
			t.Errorf("%q: closed after %v, want about %v", req, time.Since(start), defaultReadTimeout)
		}
	}

	// A token holder can upload more slowly than the default deadline.
	c := dial()
	_, _ = io.WriteString(c, "POST /api/artifacts HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+cfg.Token+"\r\nContent-Type: text/plain\r\nContent-Length: 4\r\n\r\nab")
	time.Sleep(2 * defaultReadTimeout)
	_, _ = io.WriteString(c, "cd")
	status, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "HTTP/1.1 201") {
		t.Fatalf("slow authenticated upload: %q, %v", status, err)
	}
}

// serveForTest serves h on a free loopback port until the test ends, and waits for serve to
// return before the test's earlier cleanups run.
func serveForTest(t *testing.T, h http.Handler) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- serve(ctx, ln, h) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return ln.Addr().String()
}

func TestReadDeadlineLeavesBodylessRequestContext(t *testing.T) {
	defaultReadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { defaultReadTimeout = 30 * time.Second })
	addr := serveForTest(t, securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * defaultReadTimeout)
		if err := r.Context().Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("slow bodyless response: %d %s", resp.StatusCode, body)
	}
}
