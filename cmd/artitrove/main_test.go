package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, ln, h) }()

	type result struct {
		code int
		err  error
	}
	resp := make(chan result, 1)
	go func() {
		r, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			resp <- result{err: err}
			return
		}
		r.Body.Close()
		resp <- result{code: r.StatusCode}
	}()

	<-entered
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned while a handler was in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)

	if r := <-resp; r.err != nil || r.code != http.StatusOK {
		t.Fatalf("in-flight request: %d, %v", r.code, r.err)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if c, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		c.Close()
		t.Error("listener still accepting after shutdown")
	}
}

func TestRunUsage(t *testing.T) {
	if code := run(t.Context(), []string{"bogus"}, func(string) string { return "" }); code != 2 {
		t.Errorf("run(bogus) = %d, want 2", code)
	}
}
