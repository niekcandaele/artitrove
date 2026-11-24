package e2e

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

type snapshot struct {
	meta, raw []byte
	headers   [4]string
}

func snap(t *testing.T, s *server, id string) snapshot {
	t.Helper()
	_, meta := s.get(t, "/api/artifacts/"+id)
	resp, raw := s.get(t, "/a/"+id+"/raw")
	h := resp.Header
	return snapshot{meta, raw, [4]string{h.Get("Content-Type"), h.Get("ETag"), h.Get("Content-Security-Policy"), h.Get("Cache-Control")}}
}

func TestRestartPersistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s1 := start(t, dir, "ARTITROVE_INDEX=true")
	png := []byte("\x89PNG\x00\x01\x02\xff")
	ids := []string{
		s1.upload(t, []byte("<p>one</p>"), "text/html", "First").ID,
		s1.upload(t, png, "image/png", "").ID,
		s1.upload(t, []byte("# three"), "text/markdown; charset=utf-8", "Third").ID,
		s1.upload(t, []byte("doomed"), "text/plain", "").ID,
	}
	if resp, _ := s1.delete(t, ids[3]); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	before := map[string]snapshot{}
	for _, id := range ids[:3] {
		before[id] = snap(t, s1, id)
	}
	order := indexIDs(t, s1)
	s1.stop(t, syscall.SIGTERM)

	s2 := start(t, dir, "ARTITROVE_INDEX=true")
	for _, id := range ids[:3] {
		got := snap(t, s2, id)
		want := before[id]
		if !bytes.Equal(got.meta, want.meta) || !bytes.Equal(got.raw, want.raw) || got.headers != want.headers {
			t.Errorf("artifact %s changed across restart", id)
		}
	}
	resp, b := s2.get(t, "/api/artifacts/"+ids[3])
	assertJSONError(t, resp, b, http.StatusNotFound)
	if got := indexIDs(t, s2); !slices.Equal(got, order) {
		t.Errorf("index order changed: %v -> %v", order, got)
	}
	s2.upload(t, []byte("after restart"), "text/plain", "")
}

func TestStorageLayout(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	body := []byte("exact \x00 bytes\r\n")
	a := s.upload(t, body, "application/octet-stream", "")
	if _, err := os.Stat(filepath.Join(s.dataDir, "artitrove.db")); err != nil {
		t.Error(err)
	}
	got, err := os.ReadFile(filepath.Join(s.dataDir, "blobs", a.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("blob = %q, want %q", got, body)
	}
	if names := dataDirEntries(t, s.dataDir); len(names) != 1 {
		t.Errorf("blobs = %v", names)
	}
}

func TestDataDirCreated(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	s := start(t, dir)
	s.upload(t, []byte("x"), "text/plain", "")
	if _, err := os.Stat(dir); err != nil {
		t.Error(err)
	}
}

func TestDefaultBaseURLAndDataDir(t *testing.T) {
	t.Parallel()
	s := start(t, "", "ARTITROVE_BASE_URL", "ARTITROVE_DATA_DIR")
	a := s.upload(t, []byte("x"), "text/plain", "")
	eq(t, "url", a.URL, "http://localhost:8080/a/"+a.ID)
	eq(t, "raw_url", a.RawURL, "http://localhost:8080/a/"+a.ID+"/raw")
	if _, err := os.Stat(filepath.Join(s.cmd.Dir, "data", "blobs", a.ID)); err != nil {
		t.Error(err)
	}
}

// waitRefused waits until addr stops accepting connections, which shows shutdown has begun.
func waitRefused(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", addr)
		if err != nil {
			return
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepts connections after the shutdown signal")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGracefulShutdown(t *testing.T) {
	t.Parallel()
	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String()+"/idle", func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			s.stop(t, sig)
			if c, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", s.addr); err == nil {
				_ = c.Close()
				t.Error("port still accepts connections after shutdown")
			}
		})
		t.Run(sig.String()+"/in-flight", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			s := start(t, dir)
			c, br := s.rawConn(t)
			writeAll(t, c, "POST /api/artifacts?title=inflight HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+testToken+
				"\r\nContent-Type: text/plain\r\nContent-Length: 2048\r\nExpect: 100-continue\r\n\r\n")
			line, err := br.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(line, "HTTP/1.1 100") {
				t.Fatalf("first reply line = %q, want 100 Continue", line)
			}
			if _, err := br.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			body := append(bytes.Repeat([]byte("a"), 1024), bytes.Repeat([]byte("b"), 1024)...)
			if _, err := c.Write(body[:1024]); err != nil {
				t.Fatal(err)
			}
			if err := s.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			waitRefused(t, s.addr)
			if _, err := c.Write(body[1024:]); err != nil {
				t.Fatal(err)
			}
			resp, b := readResponse(t, br)
			eq(t, "status", resp.StatusCode, http.StatusCreated)
			var a artifactJSON
			decode(t, b, &a)
			eq(t, "size", a.Size, int64(2048))
			eq(t, "exit code", s.exitCode(t), 0)

			s2 := start(t, dir)
			_, raw := s2.get(t, "/a/"+a.ID+"/raw")
			if !bytes.Equal(raw, body) {
				t.Error("in-flight upload did not survive intact")
			}
		})
	}
}

func TestStartupFailsWithoutValidToken(t *testing.T) {
	t.Parallel()
	for name, override := range map[string]string{
		"unset":         "ARTITROVE_TOKEN",
		"empty":         "ARTITROVE_TOKEN=",
		"15 ascii":      "ARTITROVE_TOKEN=" + strings.Repeat("a", 15),
		"15 multi-byte": "ARTITROVE_TOKEN=" + strings.Repeat("é", 15),
	} {
		t.Run("rejected/"+name, func(t *testing.T) {
			s := launch(t, t.TempDir(), override)
			if code := s.exitCode(t); code == 0 {
				t.Error("exit code 0, want non-zero")
			}
			contains(t, "stderr", s.stderr.String(), "ARTITROVE_TOKEN")
			lacks(t, "stderr", s.stderr.String(), "listening")
		})
	}
	for name, token := range map[string]string{
		"16 ascii":      strings.Repeat("a", 16),
		"16 multi-byte": strings.Repeat("é", 16),
	} {
		t.Run("accepted/"+name, func(t *testing.T) {
			start(t, t.TempDir(), "ARTITROVE_TOKEN="+token)
		})
	}
}

// runBin runs the binary to completion and returns its exit code.
func runBin(t *testing.T, env []string, args ...string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...) //nolint:gosec // runs the binary this test just built
	cmd.Env = env
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatal(err)
	return -1
}

func TestHealthcheckSubcommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := start(t, dir)
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatal(err)
	}
	for _, listen := range []string{s.addr, ":" + port} {
		eq(t, "healthy "+listen, runBin(t, envFor(dir, "ARTITROVE_LISTEN="+listen), "healthcheck"), 0)
	}

	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(unhealthy.Close)
	eq(t, "503", runBin(t, envFor(dir, "ARTITROVE_LISTEN="+unhealthy.Listener.Addr().String()), "healthcheck"), 1)

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	eq(t, "broken", runBin(t, envFor(dir, "ARTITROVE_LISTEN="+ln.Addr().String()), "healthcheck"), 1)

	eq(t, "no token", runBin(t, envFor(dir, "ARTITROVE_TOKEN=", "ARTITROVE_LISTEN="+s.addr), "healthcheck"), 1)
	eq(t, "extra arg", runBin(t, envFor(dir), "healthcheck", "extra"), 2)
	eq(t, "bogus arg", runBin(t, envFor(dir), "bogus"), 2)
}
