// Package api implements the JSON API and the raw-bytes route.
package api

import (
	"net/http"

	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/config"
)

// MaxContentTypeLen caps the stored Content-Type header in bytes.
const MaxContentTypeLen = 255

// Register attaches POST /api/artifacts, GET /api/artifacts/{id}, DELETE /api/artifacts/{id},
// GET /a/{id}/raw (RawHandler(store)) and GET /healthz.
//
// Errors are JSON {"error": "..."} with Content-Type application/json.
//
// Token routes (POST, DELETE) need "Authorization: Bearer <token>", compared constant-time
// over the sha256 of both sides. A missing or wrong token gives 401 with
// "WWW-Authenticate: Bearer", checked before the body is read or the id is looked up.
//
// POST: the title comes from ?title=; invalid UTF-8 or more than artifact.MaxTitleLen runes
// gives 400. An empty Content-Type becomes artifact.DefaultContentType; one longer than
// MaxContentTypeLen gives 400; any other value is stored verbatim. The body is wrapped in
// http.MaxBytesReader(cfg.MaxSize): *http.MaxBytesError gives 413, artifact.ErrEmpty gives
// 400. Success is 201 with Location = cfg.ArtifactURL(id) and a body of the Artifact plus
// "url" and "raw_url".
//
// GET metadata: 200 with the same shape; artifact.ErrNotFound gives 404.
// DELETE: 204, or 404.
// GET /healthz: 200 {"status":"ok"}.
func Register(mux *http.ServeMux, cfg config.Config, store artifact.Store) {
	panic("not implemented")
}

// RawHandler serves the exact bytes of the artifact named by r.PathValue("id"). It sets the
// stored Content-Type, "Content-Security-Policy: sandbox allow-scripts", nosniff,
// "Cache-Control: public, max-age=31536000, immutable" and `ETag: "<sha256>"`, then calls
// http.ServeContent (Range, HEAD, If-None-Match give 304). An unknown or invalid id,
// including a missing blob, gives a 404 JSON error. It works under any pattern whose wildcard
// is named {id}. package web calls it when GET /a/{id} lacks Accept text/html.
func RawHandler(store artifact.Store) http.Handler {
	panic("not implemented")
}
