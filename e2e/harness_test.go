// Package e2e holds black-box tests that build and run the real artitrove binary.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	testToken = "e2e-test-token-0123456789abcdef" //nolint:gosec // not a credential
	baseURL   = "http://trove.test:8443"
	pageCSP   = "default-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'"
)

var binPath string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "artitrove-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := touchSources(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	binPath = filepath.Join(dir, "artitrove")
	build := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-o", binPath, "./cmd/artitrove") //nolint:gosec // binPath is a temp path
	build.Dir = ".."
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// touchSources reads every app source file. This package imports no app code, so the reads are
// what make `go test` invalidate its cached result when the app changes.
func touchSources() error {
	for _, root := range []string{"../cmd", "../internal", "../go.mod", "../go.sum"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			_, err = os.ReadFile(p) //nolint:gosec // paths come from walking the repo
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// reply is a response whose body has already been read and closed. It is not *http.Response so
// that the bodyclose linter does not demand a second close.
type reply struct{ *http.Response }

type server struct {
	addr, dataDir string
	cmd           *exec.Cmd
	stderr        *syncBuffer
	exited        chan struct{}
	exitErr       error
}

// envFor returns the process environment: no inherited ARTITROVE_* variables, test defaults,
// then overrides given as KEY=VALUE.
func envFor(dataDir string, overrides ...string) []string {
	vars := map[string]string{
		"ARTITROVE_TOKEN":    testToken,
		"ARTITROVE_LISTEN":   "127.0.0.1:0",
		"ARTITROVE_DATA_DIR": dataDir,
		"ARTITROVE_BASE_URL": baseURL,
	}
	for _, o := range overrides {
		k, v, _ := strings.Cut(o, "=")
		vars[k] = v
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ARTITROVE_") {
			env = append(env, kv)
		}
	}
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	return env
}

// launch starts the binary without waiting for it to listen.
func launch(t *testing.T, dataDir string, overrides ...string) *server {
	t.Helper()
	s := &server{dataDir: dataDir, stderr: &syncBuffer{}, exited: make(chan struct{})}
	s.cmd = exec.CommandContext(context.Background(), binPath) //nolint:gosec // runs the binary this test just built
	s.cmd.Env = envFor(dataDir, overrides...)
	s.cmd.Stderr = s.stderr
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		s.exitErr = s.cmd.Wait()
		close(s.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-s.exited:
		default:
			_ = s.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-s.exited:
			case <-time.After(10 * time.Second):
				_ = s.cmd.Process.Kill()
				<-s.exited
			}
		}
		if t.Failed() {
			t.Logf("server stderr:\n%s", s.stderr.String())
		}
	})
	return s
}

var listeningRe = regexp.MustCompile(`msg=listening addr=(127\.0\.0\.1:\d+)`)

// start launches the binary on a free port and waits until /healthz answers.
func start(t *testing.T, dataDir string, overrides ...string) *server {
	t.Helper()
	s := launch(t, dataDir, overrides...)
	deadline := time.Now().Add(10 * time.Second)
	for s.addr == "" {
		if m := listeningRe.FindStringSubmatch(s.stderr.String()); m != nil {
			s.addr = m[1]
			break
		}
		select {
		case <-s.exited:
			t.Fatalf("server exited before listening: %v\n%s", s.exitErr, s.stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not listen in time\n%s", s.stderr.String())
		}
	}
	for {
		resp, _, err := s.tryDo(t, http.MethodGet, "/healthz", nil)
		if err == nil && resp.StatusCode == http.StatusOK {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("healthz never became ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// newServer starts a server on a fresh data directory.
func newServer(t *testing.T, overrides ...string) *server {
	t.Helper()
	return start(t, t.TempDir(), overrides...)
}

// exitCode waits for the process to exit and returns its exit code.
func (s *server) exitCode(t *testing.T) int {
	t.Helper()
	select {
	case <-s.exited:
	case <-time.After(10 * time.Second):
		t.Fatalf("server did not exit within 10s\n%s", s.stderr.String())
	}
	var ee *exec.ExitError
	if errors.As(s.exitErr, &ee) {
		return ee.ExitCode()
	}
	if s.exitErr != nil {
		t.Fatal(s.exitErr)
	}
	return 0
}

// stop sends sig and asserts a clean exit.
func (s *server) stop(t *testing.T, sig os.Signal) {
	t.Helper()
	if err := s.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	if code := s.exitCode(t); code != 0 {
		t.Fatalf("exit code after %v = %d, want 0\n%s", sig, code, s.stderr.String())
	}
}

var client = &http.Client{
	Timeout:       15 * time.Second,
	Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (s *server) tryDo(t *testing.T, method, path string, body io.Reader, hdr ...string) (reply, []byte, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, "http://"+s.addr+path, body)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Add(hdr[i], hdr[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		return reply{}, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return reply{resp}, b, err
}

// do sends a request; hdr is a flat list of key, value pairs (a key may repeat).
func (s *server) do(t *testing.T, method, path string, body io.Reader, hdr ...string) (reply, []byte) {
	t.Helper()
	resp, b, err := s.tryDo(t, method, path, body, hdr...)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func (s *server) get(t *testing.T, path string, hdr ...string) (reply, []byte) {
	t.Helper()
	return s.do(t, http.MethodGet, path, nil, hdr...)
}

func bearer() []string { return []string{"Authorization", "Bearer " + testToken} }

func basic(user, pass string) []string {
	return []string{"Authorization", "Basic " + b64(user+":"+pass)}
}

func (s *server) delete(t *testing.T, id string) (reply, []byte) {
	t.Helper()
	return s.do(t, http.MethodDelete, "/api/artifacts/"+id, nil, bearer()...)
}

// rawConn dials the server for tests that need control over the wire format.
func (s *server) rawConn(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", s.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return c, bufio.NewReader(c)
}

func readResponse(t *testing.T, br *bufio.Reader) (reply, []byte) {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{resp}, b
}

func writeAll(t *testing.T, c net.Conn, s string) {
	t.Helper()
	if _, err := io.WriteString(c, s); err != nil {
		t.Fatal(err)
	}
}

type artifactJSON struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
	RawURL      string `json:"raw_url"`
}

func uploadPath(title string) string {
	if title == "" {
		return "/api/artifacts"
	}
	return "/api/artifacts?title=" + url.QueryEscape(title)
}

// upload stores body and returns the decoded 201 reply. An empty ctype sends no Content-Type.
func (s *server) upload(t *testing.T, body []byte, ctype, title string) artifactJSON {
	t.Helper()
	hdr := bearer()
	if ctype != "" {
		hdr = append(hdr, "Content-Type", ctype)
	}
	resp, b := s.do(t, http.MethodPost, uploadPath(title), bytes.NewReader(body), hdr...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, body %s", resp.StatusCode, b)
	}
	var a artifactJSON
	decode(t, b, &a)
	return a
}

func decode(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func contains(t *testing.T, what, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%s: missing %q in %.600q", what, sub, s)
	}
}

func lacks(t *testing.T, what, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("%s: unexpected %q in %.600q", what, sub, s)
	}
}

func assertCommonHeaders(t *testing.T, resp reply) {
	t.Helper()
	h := resp.Header
	eq(t, "X-Content-Type-Options", h.Get("X-Content-Type-Options"), "nosniff")
	eq(t, "Referrer-Policy", h.Get("Referrer-Policy"), "no-referrer")
	eq(t, "X-Robots-Tag", h.Get("X-Robots-Tag"), "noindex")
	if c := h.Values("Set-Cookie"); len(c) > 0 {
		t.Errorf("Set-Cookie present: %v", c)
	}
}

// assertPage checks a 2xx/4xx app HTML page: status, type and the page CSP.
func assertPage(t *testing.T, resp reply, status int) {
	t.Helper()
	eq(t, "status", resp.StatusCode, status)
	eq(t, "Content-Type", resp.Header.Get("Content-Type"), "text/html; charset=utf-8")
	eq(t, "Content-Security-Policy", resp.Header.Get("Content-Security-Policy"), pageCSP)
	assertCommonHeaders(t, resp)
}

func assertRaw(t *testing.T, resp reply, body, want []byte, a artifactJSON) {
	t.Helper()
	eq(t, "status", resp.StatusCode, http.StatusOK)
	eq(t, "Content-Type", resp.Header.Get("Content-Type"), a.ContentType)
	eq(t, "Content-Security-Policy", resp.Header.Get("Content-Security-Policy"), "sandbox allow-scripts")
	eq(t, "Cache-Control", resp.Header.Get("Cache-Control"), "public, max-age=31536000, immutable")
	eq(t, "ETag", resp.Header.Get("ETag"), `"`+a.SHA256+`"`)
	assertCommonHeaders(t, resp)
	if !bytes.Equal(body, want) {
		t.Errorf("raw body differs: got %d bytes, want %d", len(body), len(want))
	}
}

func assertJSONError(t *testing.T, resp reply, body []byte, status int) {
	t.Helper()
	eq(t, "status", resp.StatusCode, status)
	eq(t, "Content-Type", resp.Header.Get("Content-Type"), "application/json")
	var e map[string]string
	decode(t, body, &e)
	if len(e) != 1 || e["error"] == "" {
		t.Errorf("error body = %s, want {\"error\": non-empty}", body)
	}
	assertCommonHeaders(t, resp)
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)

var attrRe = regexp.MustCompile(`(?i)\s(?:href|src)\s*=\s*"([^"]*)"`)

// assertSafeURLs fails on any href or src whose decoded value has a scheme other than http, https or mailto.
func assertSafeURLs(t *testing.T, page string) {
	t.Helper()
	for _, m := range attrRe.FindAllStringSubmatch(page, -1) {
		v := strings.Map(func(r rune) rune {
			if r <= 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, html.UnescapeString(m[1]))
		end := strings.IndexAny(v, "/?#")
		if end < 0 {
			end = len(v)
		}
		scheme, _, hasScheme := strings.Cut(v[:end], ":")
		if !hasScheme || !schemeRe.MatchString(scheme) { // without valid scheme syntax a browser reads a relative URL
			continue
		}
		switch strings.ToLower(scheme) {
		case "http", "https", "mailto":
		default:
			t.Errorf("unsafe URL %q (decoded %q)", m[1], v)
		}
	}
}

var (
	scriptTagRe = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	tagStyleRe  = regexp.MustCompile(`(?i)<[^>]*\sstyle\s*=`)
	tagEventRe  = regexp.MustCompile(`(?i)<[^>]*\son[a-z]+\s*=`)
)

// assertCSPCompliant fails on markup the page CSP would block: inline script or style, event handlers.
func assertCSPCompliant(t *testing.T, page string) {
	t.Helper()
	for _, tag := range scriptTagRe.FindAllString(page, -1) {
		if !strings.Contains(strings.ToLower(tag), "src=") {
			t.Errorf("inline script: %s", tag)
		}
	}
	if strings.Contains(strings.ToLower(page), "<style") {
		t.Error("inline <style> element")
	}
	if m := tagStyleRe.FindString(page); m != "" {
		t.Errorf("style attribute: %.200s", m)
	}
	if m := tagEventRe.FindString(page); m != "" {
		t.Errorf("event handler attribute: %.200s", m)
	}
}

var storedNames = map[string]bool{"artitrove.db": true, "artitrove.db-wal": true, "artitrove.db-shm": true, "blobs": true}

// dataDirEntries fails on stray top-level entries and returns the sorted names inside blobs/.
func dataDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	top, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range top {
		if !storedNames[e.Name()] {
			t.Errorf("stray entry in data dir: %s", e.Name())
		}
	}
	blobs, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(blobs))
	for _, e := range blobs {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
