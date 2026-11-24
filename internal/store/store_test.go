package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/niekcandaele/artitrove/internal/artifact"
)

var ctx = context.Background()

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func create(t *testing.T, s *Store, title, content string) artifact.Artifact {
	t.Helper()
	a, err := s.Create(ctx, title, "text/plain", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func sameMeta(t *testing.T, got, want artifact.Artifact) {
	t.Helper()
	if got.ID != want.ID || got.Title != want.Title || got.ContentType != want.ContentType ||
		got.Size != want.Size || got.SHA256 != want.SHA256 {
		t.Fatalf("metadata mismatch: got %+v want %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || got.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt mismatch: got %v want %v", got.CreatedAt, want.CreatedAt)
	}
}

func readFile(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(name)
}

func assertNoStrayFiles(t *testing.T, dir string) {
	t.Helper()
	blobs, err := os.ReadDir(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 0 {
		t.Fatalf("blobs/ not empty: %v", blobs)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"blobs", dbName, dbName + "-wal", dbName + "-shm"}
	for _, e := range entries {
		if !slices.Contains(allowed, e.Name()) {
			t.Fatalf("stray file in data dir: %s", e.Name())
		}
	}
}

func TestRoundTrip(t *testing.T) {
	s, dir := openStore(t)
	body := "hello, artitrove\n"
	a, err := s.Create(ctx, "Report", "text/plain; charset=utf-8", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	if !artifact.ValidID(a.ID) || a.Size != int64(len(body)) || a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("bad metadata: %+v", a)
	}
	if a.CreatedAt.Location() != time.UTC || a.CreatedAt.Nanosecond() != 0 || time.Since(a.CreatedAt) > 5*time.Second {
		t.Fatalf("bad CreatedAt: %v", a.CreatedAt)
	}
	got, err := s.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	sameMeta(t, got, a)

	_, f, err := s.Open(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		b, err := io.ReadAll(f)
		if err != nil || string(b) != body {
			t.Fatalf("content %q err %v", b, err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	onDisk, err := readFile(dir, "blobs/"+a.ID)
	if err != nil || string(onDisk) != body {
		t.Fatalf("blob %q err %v", onDisk, err)
	}
}

func TestRoundTripEmptyTitleAndBinary(t *testing.T) {
	s, _ := openStore(t)
	body := "a\x00b\xff\xfe\x80c"
	a := create(t, s, "", body)
	got, f, err := s.Open(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, _ := io.ReadAll(f)
	if got.Title != "" || string(b) != body {
		t.Fatalf("title %q body %q", got.Title, b)
	}
}

func TestPermissions(t *testing.T) {
	s, dir := openStore(t)
	a := create(t, s, "x", "data")
	for _, p := range []string{
		dir, filepath.Join(dir, "blobs"), filepath.Join(dir, dbName),
		filepath.Join(dir, dbName+"-wal"), filepath.Join(dir, dbName+"-shm"),
		filepath.Join(dir, "blobs", a.ID),
	} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v", p, fi.Mode().Perm())
		}
	}
}

func TestWALMode(t *testing.T) {
	s, _ := openStore(t)
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q", mode)
	}
}

func ids(as []artifact.Artifact) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}

func TestListOrderingSameSecond(t *testing.T) {
	s, _ := openStore(t)
	a, b, c := create(t, s, "a", "1"), create(t, s, "b", "2"), create(t, s, "c", "3")
	if _, err := s.db.ExecContext(ctx, "UPDATE artifacts SET created_at = 1700000000"); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{c.ID, b.ID, a.ID}; !slices.Equal(ids(got), want) {
		t.Fatalf("got %v want %v", ids(got), want)
	}
}

func TestListOrderingCreatedAtWins(t *testing.T) {
	s, _ := openStore(t)
	a, b := create(t, s, "a", "1"), create(t, s, "b", "2")
	if _, err := s.db.ExecContext(ctx, "UPDATE artifacts SET created_at = ? WHERE id = ?", b.CreatedAt.Unix()+60, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{a.ID, b.ID}; !slices.Equal(ids(got), want) {
		t.Fatalf("got %v want %v", ids(got), want)
	}
}

func TestListEmpty(t *testing.T) {
	s, _ := openStore(t)
	got, err := s.List(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestDeleteThenGet(t *testing.T) {
	s, dir := openStore(t)
	a := create(t, s, "a", "data")
	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", a.ID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("blob still present: %v", err)
	}
	if _, err := s.Get(ctx, a.ID); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Get: %v", err)
	}
	if _, _, err := s.Open(ctx, a.ID); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Delete(ctx, a.ID); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("second Delete: %v", err)
	}
	if got, _ := s.List(ctx); len(got) != 0 {
		t.Fatalf("List: %v", got)
	}
}

func TestUnknownID(t *testing.T) {
	s, _ := openStore(t)
	id := newID()
	_, getErr := s.Get(ctx, id)
	_, _, openErr := s.Open(ctx, id)
	delErr := s.Delete(ctx, id)
	for _, err := range []error{getErr, openErr, delErr} {
		if !errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrInvalidID) {
			t.Fatalf("got %v", err)
		}
	}
}

func invalidIDs() []string {
	return []string{
		"", strings.Repeat("a", 21), strings.Repeat("a", 23),
		"../../../../../etc/pas", strings.Repeat(".", 22),
		"/etc/passwd" + strings.Repeat("a", 11), "/" + strings.Repeat("a", 21),
		"a/../" + strings.Repeat("a", 17), strings.Repeat("a", 21) + "\x00",
		strings.Repeat("\\", 22), "%2e%2e" + strings.Repeat("a", 16),
		strings.Repeat("+", 22), strings.Repeat("=", 22), strings.Repeat("é", 11),
	}
}

func TestInvalidIDs(t *testing.T) {
	s, _ := openStore(t)
	for _, id := range invalidIDs() {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			_, getErr := s.Get(ctx, id)
			_, _, openErr := s.Open(ctx, id)
			delErr := s.Delete(ctx, id)
			for _, err := range []error{getErr, openErr, delErr} {
				if !errors.Is(err, artifact.ErrInvalidID) {
					t.Fatalf("got %v", err)
				}
			}
		})
	}
}

func TestTraversalTouchesNothing(t *testing.T) {
	s, dir := openStore(t)
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../victim" + strings.Repeat("a", 13), "..\\victim" + strings.Repeat("a", 13), "../victim"} {
		if err := s.Delete(ctx, id); !errors.Is(err, artifact.ErrInvalidID) {
			t.Fatalf("Delete %q: %v", id, err)
		}
		if _, _, err := s.Open(ctx, id); !errors.Is(err, artifact.ErrInvalidID) {
			t.Fatalf("Open %q: %v", id, err)
		}
	}
	if b, err := readFile(dir, "victim"); err != nil || string(b) != "keep" {
		t.Fatalf("victim damaged: %q %v", b, err)
	}
}

func TestOpenMissingBlob(t *testing.T) {
	s, dir := openStore(t)
	a := create(t, s, "a", "data")
	if err := os.Remove(filepath.Join(dir, "blobs", a.ID)); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Open(ctx, a.ID)
	if !errors.Is(err, artifact.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentCreates(t *testing.T) {
	s, _ := openStore(t)
	const n = 50
	created := make([]artifact.Artifact, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			created[i], errs[i] = s.Create(ctx, "t", "text/plain", strings.NewReader(fmt.Sprintf("content-%d", i)))
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		seen[created[i].ID] = true
	}
	if len(seen) != n {
		t.Fatalf("distinct ids = %d", len(seen))
	}
	if got, err := s.List(ctx); err != nil || len(got) != n {
		t.Fatalf("List len %d err %v", len(got), err)
	}
	for i, a := range created {
		_, f, err := s.Open(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(f)
		_ = f.Close()
		sum := sha256.Sum256(b)
		if string(b) != fmt.Sprintf("content-%d", i) || a.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("artifact %d mismatch", i)
		}
	}
}

func TestReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := create(t, s, "kept", "persisted")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, f, err := s.Open(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sameMeta(t, got, a)
	if b, _ := io.ReadAll(f); !bytes.Equal(b, []byte("persisted")) {
		t.Fatalf("content %q", b)
	}
	if l, _ := s.List(ctx); len(l) != 1 {
		t.Fatalf("List len %d", len(l))
	}
	create(t, s, "more", "x")
}

func TestCreateEmpty(t *testing.T) {
	s, dir := openStore(t)
	if _, err := s.Create(ctx, "t", "text/plain", strings.NewReader("")); !errors.Is(err, artifact.ErrEmpty) {
		t.Fatalf("got %v", err)
	}
	assertNoStrayFiles(t, dir)
}

func TestCreateReadErrorLeavesNoFiles(t *testing.T) {
	s, dir := openStore(t)
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(boom))
	if _, err := s.Create(ctx, "t", "text/plain", r); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	assertNoStrayFiles(t, dir)
	if l, _ := s.List(ctx); len(l) != 0 {
		t.Fatalf("List: %v", l)
	}
}

func TestCreateMaxBytesError(t *testing.T) {
	s, dir := openStore(t)
	r := http.MaxBytesReader(nil, io.NopCloser(strings.NewReader(strings.Repeat("x", 20))), 10)
	_, err := s.Create(ctx, "t", "text/plain", r)
	var mbe *http.MaxBytesError
	if !errors.As(err, &mbe) {
		t.Fatalf("got %v", err)
	}
	assertNoStrayFiles(t, dir)
}

func TestCreateInsertFailureRemovesBlob(t *testing.T) {
	s, dir := openStore(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "t", "text/plain", strings.NewReader("data")); err == nil {
		t.Fatal("expected error")
	}
	assertNoStrayFiles(t, dir)
}

func TestOpenRemovesStaleUploads(t *testing.T) {
	s, dir := openStore(t)
	stale := filepath.Join(dir, uploadPrefix+"abc")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := create(t, s, "kept", "content")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale upload still present: %v", err)
	}
	if _, err := s2.Get(ctx, a.ID); err != nil {
		t.Errorf("existing artifact lost: %v", err)
	}
}
