// Package web implements the human-facing routes: landing page, artifact view, static files.
package web

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/config"
	"github.com/niekcandaele/artitrove/internal/render"
	"github.com/niekcandaele/artitrove/internal/ui"
)

const staticCacheControl = "public, max-age=3600"

// maxTextPreview bounds the text shown inline: the browser lays out one wrapped block very slowly
// beyond a few hundred KiB. The raw route has the whole file.
const maxTextPreview = 512 << 10

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
// content is opened, and invalid JSON or Markdown too large to render (render.ErrTooLarge)
// becomes KindText. An unknown or invalid id gives ui.RenderNotFound.
//
// With cfg.Index true, the landing page and its 401 carry "Cache-Control: no-store" because
// the list holds credentials. Basic auth accepts any username with the token as password,
// compared constant-time; failure gives 401 with `WWW-Authenticate: Basic realm="Artitrove",
// charset="UTF-8"` and a JSON error body. With the index off there is no auth and no list.
//
// Internal errors are logged with slog and return 500 with a JSON error body.
func Register(mux *http.ServeMux, cfg config.Config, store artifact.Store, raw http.Handler) {
	mux.Handle("GET /{$}", landing(cfg, store))
	mux.Handle("GET /a/{id}", view(cfg, store, raw))
	mux.Handle("GET /static/highlight.css", highlightCSS())
	mux.Handle("GET /static/", staticFiles())
}

// wantsHTML reports whether Accept names text/html with a non-zero quality.
func wantsHTML(r *http.Request) bool {
	for _, rng := range strings.Split(strings.Join(r.Header.Values("Accept"), ","), ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(rng))
		if err != nil || mt != "text/html" {
			continue
		}
		q, ok := params["q"]
		if !ok {
			return true
		}
		if v, err := strconv.ParseFloat(q, 64); err == nil && v > 0 {
			return true
		}
	}
	return false
}

func view(cfg config.Config, store artifact.Store, raw http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept")
		if !wantsHTML(r) {
			raw.ServeHTTP(w, r)
			return
		}
		id := r.PathValue("id")
		a, err := store.Get(r.Context(), id)
		if err != nil {
			viewError(w, err)
			return
		}
		kind := render.Detect(a.ContentType)
		page := ui.ArtifactPage{Artifact: a, URL: cfg.ArtifactURL(id)}
		if kind == render.KindMarkdown || kind == render.KindJSON || kind == render.KindText {
			src, err := readContent(r, store, id)
			if err != nil {
				viewError(w, err)
				return
			}
			switch kind {
			case render.KindMarkdown:
				switch page.Body, err = render.Markdown(r.Context(), src); {
				case errors.Is(err, render.ErrTooLarge):
					kind = render.KindText
				case err != nil:
					viewServerError(w, "render markdown", err)
					return
				}
			case render.KindJSON:
				if tree, ok := render.JSON(src); ok {
					page.Body = tree
				} else {
					kind = render.KindText
				}
			}
			if kind == render.KindText {
				if len(src) > maxTextPreview {
					n := maxTextPreview
					for n > maxTextPreview-utf8.UTFMax && !utf8.RuneStart(src[n]) {
						n--
					}
					src, page.Truncated = src[:n], true
				}
				page.Text = strings.ToValidUTF8(string(src), "�")
			}
		}
		page.Kind = kind
		if err := ui.RenderArtifact(w, page); err != nil {
			viewServerError(w, "render artifact", err)
		}
	})
}

func readContent(r *http.Request, store artifact.Store, id string) ([]byte, error) {
	_, f, err := store.Open(r.Context(), id)
	if err != nil {
		return nil, err
	}
	src, err := io.ReadAll(f)
	return src, errors.Join(err, f.Close())
}

func viewError(w http.ResponseWriter, err error) {
	if !errors.Is(err, artifact.ErrNotFound) {
		viewServerError(w, "load artifact", err)
		return
	}
	if err := ui.RenderNotFound(w); err != nil {
		serverError(w, "render not found", err)
	}
}

// viewServerError answers a browser on the human view with an HTML page instead of JSON.
func viewServerError(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, "error", err)
	if err := ui.RenderServerError(w); err != nil {
		serverError(w, "render server error", err)
	}
}

func landing(cfg config.Config, store artifact.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Index {
			if err := ui.RenderLanding(w, ui.LandingPage{BaseURL: cfg.BaseURL}); err != nil {
				serverError(w, "render landing", err)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if !authorized(r, cfg.Token) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artitrove", charset="UTF-8"`)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		list, err := store.List(r.Context())
		if err != nil {
			serverError(w, "list artifacts", err)
			return
		}
		entries := make([]ui.IndexEntry, len(list))
		for i, a := range list {
			entries[i] = ui.IndexEntry{Artifact: a, Kind: render.Detect(a.ContentType)}
		}
		page := ui.LandingPage{BaseURL: cfg.BaseURL, Index: true, Artifacts: entries}
		if err := ui.RenderLanding(w, page); err != nil {
			serverError(w, "render landing", err)
		}
	})
}

func authorized(r *http.Request, token string) bool {
	_, pass, ok := r.BasicAuth()
	return ok && config.TokenMatches(pass, token)
}

func highlightCSS() http.Handler {
	css := []byte(render.HighlightCSS())
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", staticCacheControl)
		_, _ = w.Write(css)
	})
}

func staticFiles() http.Handler {
	files := http.StripPrefix(ui.StaticPath, http.FileServerFS(ui.Static()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", staticCacheControl)
		name := strings.TrimPrefix(r.URL.Path, ui.StaticPath)
		if info, err := fs.Stat(ui.Static(), name); err != nil || info.IsDir() {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		files.ServeHTTP(w, r)
	})
}

func serverError(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Error("write response", "error", err)
	}
}
