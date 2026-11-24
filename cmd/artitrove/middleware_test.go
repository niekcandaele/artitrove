package main

import (
	"bufio"
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
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = serve(t.Context(), ln, newHandler(cfg, st)) }()

	dial := func() net.Conn {
		c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return c
	}

	// An unauthenticated request that never sends its declared body is cut off.
	c := dial()
	_, _ = io.WriteString(c, "GET /healthz HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n\r\n")
	start := time.Now()
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("connection was not closed by the server: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("closed after %v, want about %v", time.Since(start), defaultReadTimeout)
	}

	// A token holder can upload more slowly than the default deadline.
	c = dial()
	_, _ = io.WriteString(c, "POST /api/artifacts HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+cfg.Token+"\r\nContent-Type: text/plain\r\nContent-Length: 4\r\n\r\nab")
	time.Sleep(2 * defaultReadTimeout)
	_, _ = io.WriteString(c, "cd")
	status, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "HTTP/1.1 201") {
		t.Fatalf("slow authenticated upload: %q, %v", status, err)
	}
}
