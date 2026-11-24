// Package store implements artifact.Store on SQLite (metadata) and flat files (content).
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/niekcandaele/artitrove/internal/artifact"
)

const dbName = "artitrove.db"

const schema = `CREATE TABLE IF NOT EXISTS artifacts (
	seq          INTEGER PRIMARY KEY,
	id           TEXT NOT NULL UNIQUE,
	title        TEXT NOT NULL,
	content_type TEXT NOT NULL,
	size         INTEGER NOT NULL,
	sha256       TEXT NOT NULL,
	created_at   INTEGER NOT NULL
)`

const columns = "id, title, content_type, size, sha256, created_at"

// Store is the SQLite and filesystem backed artifact.Store.
type Store struct {
	db   *sql.DB
	root *os.Root // rooted at the data directory: every file operation stays inside it
}

var _ artifact.Store = (*Store)(nil)

// Open creates dataDir and dataDir/blobs with mode 0o700 if missing. It opens
// <dataDir>/artitrove.db (modernc.org/sqlite), creating the file 0o600 first if absent, and
// creates the artifacts table if absent. Blob and temp files are 0o600. It returns any
// filesystem or SQL error.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	// SQLite gives the -wal and -shm files the mode of the db file.
	f, err := root.OpenFile(dbName, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	if err := f.Close(); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, dbName)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		return nil, errors.Join(err, db.Close(), root.Close())
	}
	return &Store{db: db, root: root}, nil
}

// Close releases the database.
func (s *Store) Close() error {
	return errors.Join(s.db.Close(), s.root.Close())
}

func blobName(id string) string { return "blobs/" + id }

// syncBlobsDir makes a rename into blobs/ durable before the row that points at it is committed.
func (s *Store) syncBlobsDir() error {
	d, err := s.root.Open("blobs")
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails on supported platforms
	return base64.RawURLEncoding.EncodeToString(b)
}

// Create implements artifact.Store.Create. IDs are 16 crypto/rand bytes, base64url without padding.
func (s *Store) Create(ctx context.Context, title, contentType string, content io.Reader) (a artifact.Artifact, err error) {
	id := newID()
	tmp := ".upload-" + newID() // the id is a credential, so it must not show in a directory listing
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return artifact.Artifact{}, err
	}
	closed, renamed := false, false
	defer func() {
		if err == nil {
			return
		}
		if !closed {
			err = errors.Join(err, f.Close())
		}
		if renamed {
			err = errors.Join(err, s.root.Remove(blobName(id)))
		} else {
			err = errors.Join(err, s.root.Remove(tmp))
		}
	}()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), content)
	if err != nil {
		return artifact.Artifact{}, fmt.Errorf("store content: %w", err)
	}
	if n == 0 {
		return artifact.Artifact{}, artifact.ErrEmpty
	}
	if err = f.Sync(); err != nil {
		return artifact.Artifact{}, err
	}
	closed = true // a failed Close must not be retried
	if err = f.Close(); err != nil {
		return artifact.Artifact{}, err
	}
	if err = s.root.Rename(tmp, blobName(id)); err != nil {
		return artifact.Artifact{}, err
	}
	renamed = true
	if err = s.syncBlobsDir(); err != nil {
		return artifact.Artifact{}, err
	}

	a = artifact.Artifact{
		ID:          id,
		Title:       title,
		ContentType: contentType,
		Size:        n,
		SHA256:      hex.EncodeToString(h.Sum(nil)),
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO artifacts ("+columns+") VALUES (?, ?, ?, ?, ?, ?)",
		a.ID, a.Title, a.ContentType, a.Size, a.SHA256, a.CreatedAt.Unix())
	if err != nil {
		return artifact.Artifact{}, err
	}
	return a, nil
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (artifact.Artifact, error) {
	var a artifact.Artifact
	var created int64
	if err := r.Scan(&a.ID, &a.Title, &a.ContentType, &a.Size, &a.SHA256, &created); err != nil {
		return artifact.Artifact{}, err
	}
	a.CreatedAt = time.Unix(created, 0).UTC()
	return a, nil
}

// Get implements artifact.Store.Get.
func (s *Store) Get(ctx context.Context, id string) (artifact.Artifact, error) {
	if !artifact.ValidID(id) {
		return artifact.Artifact{}, artifact.ErrInvalidID
	}
	a, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM artifacts WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.Artifact{}, artifact.ErrNotFound
	}
	return a, err
}

// Open implements artifact.Store.Open.
func (s *Store) Open(ctx context.Context, id string) (artifact.Artifact, io.ReadSeekCloser, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return artifact.Artifact{}, nil, err
	}
	f, err := s.root.Open(blobName(id))
	if errors.Is(err, fs.ErrNotExist) {
		return artifact.Artifact{}, nil, fmt.Errorf("blob missing: %w", artifact.ErrNotFound)
	}
	if err != nil {
		return artifact.Artifact{}, nil, err
	}
	return a, f, nil
}

// Delete implements artifact.Store.Delete.
func (s *Store) Delete(ctx context.Context, id string) error {
	if !artifact.ValidID(id) {
		return artifact.ErrInvalidID
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM artifacts WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return artifact.ErrNotFound
	}
	return s.root.Remove(blobName(id))
}

// List implements artifact.Store.List.
func (s *Store) List(ctx context.Context) (out []artifact.Artifact, err error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM artifacts ORDER BY created_at DESC, seq DESC")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
