// Package store implements artifact.Store on SQLite (metadata) and flat files (content).
package store

import (
	"context"
	"io"

	"github.com/niekcandaele/artitrove/internal/artifact"
)

// Store is the SQLite and filesystem backed artifact.Store.
type Store struct{}

var _ artifact.Store = (*Store)(nil)

// Open creates dataDir and dataDir/blobs with mode 0o700 if missing. It opens
// <dataDir>/artitrove.db (modernc.org/sqlite), creating the file 0o600 first if absent, and
// creates the artifacts table if absent. Blob and temp files are 0o600. It returns any
// filesystem or SQL error.
func Open(dataDir string) (*Store, error) {
	panic("not implemented")
}

// Close releases the database.
func (s *Store) Close() error {
	panic("not implemented")
}

// Create implements artifact.Store.Create. IDs are 16 crypto/rand bytes, base64url without padding.
func (s *Store) Create(ctx context.Context, title, contentType string, content io.Reader) (artifact.Artifact, error) {
	panic("not implemented")
}

// Get implements artifact.Store.Get.
func (s *Store) Get(ctx context.Context, id string) (artifact.Artifact, error) {
	panic("not implemented")
}

// Open implements artifact.Store.Open.
func (s *Store) Open(ctx context.Context, id string) (artifact.Artifact, io.ReadSeekCloser, error) {
	panic("not implemented")
}

// Delete implements artifact.Store.Delete.
func (s *Store) Delete(ctx context.Context, id string) error {
	panic("not implemented")
}

// List implements artifact.Store.List.
func (s *Store) List(ctx context.Context) ([]artifact.Artifact, error) {
	panic("not implemented")
}
