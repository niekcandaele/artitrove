// Package artifact holds the types and interfaces shared by every other package.
package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	// MaxTitleLen is the maximum title length in runes.
	MaxTitleLen = 200
	// DefaultContentType is stored when an upload carries no Content-Type.
	DefaultContentType = "application/octet-stream"
)

var (
	// ErrNotFound means no artifact with the given id exists.
	ErrNotFound = errors.New("artifact not found")
	// ErrInvalidID means the id is malformed. It wraps ErrNotFound, so
	// errors.Is(err, ErrNotFound) is the only check handlers need.
	ErrInvalidID = fmt.Errorf("invalid artifact id: %w", ErrNotFound)
	// ErrEmpty means the uploaded content had zero bytes.
	ErrEmpty = errors.New("artifact content is empty")
)

// Artifact is the stored metadata of one uploaded file.
type Artifact struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"` // may be empty
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`     // lowercase hex
	CreatedAt   time.Time `json:"created_at"` // UTC, truncated to whole seconds
}

// ValidID reports whether id is exactly 22 bytes, each in [A-Za-z0-9_-].
// It guards every use of an id as a file path or lookup key.
func ValidID(id string) bool {
	if len(id) != 22 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// Store persists artifacts. Implementations must be safe for concurrent use.
type Store interface {
	// Create reads content to EOF and persists it: temp file in the data dir (0o600), fsync,
	// rename to blobs/<id>, then insert the row. It returns the new metadata. title and
	// contentType are stored verbatim; callers validate them and apply defaults.
	// Errors: ErrEmpty for zero bytes. Read errors are wrapped with %w, so callers can
	// errors.As *http.MaxBytesError. On any error nothing is stored and no temp file remains.
	Create(ctx context.Context, title, contentType string, content io.Reader) (Artifact, error)
	// Get returns metadata. ErrInvalidID for a malformed id (no disk or DB access);
	// ErrNotFound if the id is unknown.
	Get(ctx context.Context, id string) (Artifact, error)
	// Open returns metadata and content; the caller must Close the content. Errors are as for
	// Get. A missing blob file (errors.Is(err, fs.ErrNotExist)), for example from a concurrent
	// Delete after the row was read, is returned wrapped as ErrNotFound, never as a raw fs error.
	Open(ctx context.Context, id string) (Artifact, io.ReadSeekCloser, error)
	// Delete removes the row first, then the blob file. Errors are as for Get. A file
	// removal error is returned.
	Delete(ctx context.Context, id string) error
	// List returns all artifacts newest first (created_at DESC, then insertion order DESC).
	// The result may be empty.
	List(ctx context.Context) ([]Artifact, error)
}
