// Package web implements the human-facing routes: landing page, artifact view, static files.
package web

import (
	"net/http"

	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/config"
)

// Register attaches:
//
//	GET /{$}                  landing page; basic-auth index when cfg.Index is true
//	GET /a/{id}               human view
//	GET /static/highlight.css render.HighlightCSS(), computed once at Register, Content-Type text/css; charset=utf-8
//	GET /static/              http.StripPrefix(ui.StaticPath, http.FileServerFS(ui.Static())); directory paths give 404
//
// Every /static/ response carries Cache-Control: public, max-age=3600.
//
// GET /a/{id} always sets "Vary: Accept". The Accept value is the join of every header line,
// strings.Join(r.Header.Values("Accept"), ","). If no media range equals text/html
// (case-insensitive, parameters stripped, q=0 counts as absent, */* does NOT count) it calls
// raw.ServeHTTP(w, r) and does nothing else; raw is api.RawHandler, which reads
// r.PathValue("id"). Otherwise it calls ui.RenderArtifact; only Markdown, JSON and Text
// content is opened, and invalid JSON becomes KindText. An unknown or invalid id gives
// ui.RenderNotFound.
//
// With cfg.Index true, the landing page and its 401 carry "Cache-Control: no-store" because
// the list holds credentials. Basic auth accepts any username with the token as password,
// compared constant-time; failure gives 401 with `WWW-Authenticate: Basic realm="Artitrove",
// charset="UTF-8"`. With the index off there is no auth and no list.
//
// Internal errors are logged with slog and return 500.
func Register(mux *http.ServeMux, cfg config.Config, store artifact.Store, raw http.Handler) {
	panic("not implemented")
}
