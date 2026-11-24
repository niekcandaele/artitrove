// Package api implements the JSON API and the raw-bytes route.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	mux.HandleFunc("POST /api/artifacts", requireToken(cfg.Token, func(w http.ResponseWriter, r *http.Request) {
		create(w, r, cfg, store)
	}))
	mux.HandleFunc("GET /api/artifacts/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, err := store.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			storeError(w, "get artifact", err)
			return
		}
		writeJSON(w, http.StatusOK, newResponse(cfg, a))
	})
	mux.HandleFunc("DELETE /api/artifacts/{id}", requireToken(cfg.Token, func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(r.Context(), r.PathValue("id")); err != nil {
			storeError(w, "delete artifact", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("GET /a/{id}/raw", RawHandler(store))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

func create(w http.ResponseWriter, r *http.Request, cfg config.Config, store artifact.Store) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed query string")
		return
	}
	title := q.Get("title")
	if !utf8.ValidString(title) {
		writeError(w, http.StatusBadRequest, "title is not valid UTF-8")
		return
	}
	if utf8.RuneCountInString(title) > artifact.MaxTitleLen {
		writeError(w, http.StatusBadRequest, "title exceeds 200 characters")
		return
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = artifact.DefaultContentType
	}
	if len(contentType) > MaxContentTypeLen {
		writeError(w, http.StatusBadRequest, "content type too long")
		return
	}
	body := http.MaxBytesReader(w, r.Body, cfg.MaxSize)
	a, err := store.Create(r.Context(), title, contentType, body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "content exceeds "+strconv.FormatInt(tooLarge.Limit, 10)+" bytes")
		return
	case errors.Is(err, artifact.ErrEmpty):
		writeError(w, http.StatusBadRequest, "empty body")
		return
	case err != nil:
		internalError(w, "create artifact", err)
		return
	}
	w.Header().Set("Location", cfg.ArtifactURL(a.ID))
	writeJSON(w, http.StatusCreated, newResponse(cfg, a))
}

// RawHandler serves the exact bytes of the artifact named by r.PathValue("id"). It sets the
// stored Content-Type, "Content-Security-Policy: sandbox allow-scripts", nosniff,
// "Cache-Control: public, max-age=31536000, immutable" and `ETag: "<sha256>"`, then calls
// http.ServeContent (Range, HEAD, If-None-Match give 304). An unknown or invalid id,
// including a missing blob, gives a 404 JSON error. It works under any pattern whose wildcard
// is named {id}. package web calls it when GET /a/{id} lacks Accept text/html.
func RawHandler(store artifact.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, f, err := store.Open(r.Context(), r.PathValue("id"))
		if err != nil {
			storeError(w, "open artifact", err)
			return
		}
		defer func() {
			if err := f.Close(); err != nil {
				slog.Error("close blob", "error", err)
			}
		}()
		h := w.Header()
		h.Set("Content-Type", a.ContentType)
		h.Set("Content-Security-Policy", "sandbox allow-scripts")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("ETag", `"`+a.SHA256+`"`)
		http.ServeContent(w, r, "", time.Time{}, f)
	})
}

type response struct {
	artifact.Artifact
	URL    string `json:"url"`
	RawURL string `json:"raw_url"`
}

func newResponse(cfg config.Config, a artifact.Artifact) response {
	return response{Artifact: a, URL: cfg.ArtifactURL(a.ID), RawURL: cfg.RawURL(a.ID)}
}

func requireToken(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scheme, given, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		ok = ok && strings.EqualFold(scheme, "Bearer") // auth schemes are case-insensitive (RFC 9110 §11.1)
		if !ok || !config.TokenMatches(given, token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next(w, r)
	}
}

func storeError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, artifact.ErrNotFound) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	internalError(w, op, err)
}

// internalError logs only the syscall detail of path errors, so blob paths (which contain ids) stay out of logs.
func internalError(w http.ResponseWriter, op string, err error) {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		slog.Error(op, "op", pe.Op, "error", pe.Err)
	} else {
		slog.Error(op, "error", err)
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "error", err)
	}
}
