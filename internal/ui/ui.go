// Package ui owns the embedded templates and static files and renders app pages.
//
// Binding constraints for the implementation of this package:
//
// CSP. App pages are served with PageCSP, so these are banned in every template: inline
// <script> bodies, style= attributes, on*= handlers, javascript: URLs, and external URLs in
// src, href or action. All CSS, JS and fonts are files under Static(). Fonts must be committed
// woff2 files or a system font stack; there are no Google Fonts.
//
// Highlighting. /static/highlight.css is served by package web from render.HighlightCSS(), so
// ui ships no copy of it. Every page that may contain Markdown links /static/highlight.css.
// ui's own CSS scopes JSON tree rules under .json-tree.
//
// KindHTML renders <iframe sandbox="allow-scripts" src="/a/{id}/raw"> filling the viewport
// below the header.
//
// KindBinary shows the type, the size, a primary <a href="/a/{id}/raw" download="{title or id}">
// Download</a>, and the header's plain "Open raw" link. The raw route sends no Content-Disposition.
//
// Times render as <time datetime="RFC3339">absolute UTC</time>. The FuncMap must not call
// time.Now, so output is deterministic. The external app.js may rewrite them to relative or
// local time. Reading must work without JS.
//
// Copy link needs a fallback where navigator.clipboard is unavailable (plain-http LAN), for
// example a readonly selectable input.
//
// A test must scan every embedded template and fail on style=, <script without src,
// \son[a-z]+=, javascript:, and (src|href|action)="(https?:)?//.
package ui

import (
	"html/template"
	"io/fs"
	"net/http"

	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/render"
)

// PageCSP is the Content-Security-Policy for every app page.
const PageCSP = "default-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'"

// StaticPath is the URL prefix for embedded assets.
const StaticPath = "/static/"

// ArtifactPage is the data for the human view of one artifact.
type ArtifactPage struct {
	artifact.Artifact               // title (falls back to ID), type, size, created time
	URL               string        // absolute share link (config.ArtifactURL)
	Kind              render.Kind   // effective kind: web passes KindText for invalid JSON
	Body              template.HTML // render.Markdown or render.JSON fragment; otherwise empty
	Text              string        // KindText content; web applies strings.ToValidUTF8
}

// IndexEntry is one row of the landing-page index.
type IndexEntry struct {
	artifact.Artifact
	Kind render.Kind
}

// LandingPage is the data for GET /.
type LandingPage struct {
	BaseURL   string       // used in the curl example
	Index     bool         // false: no list is rendered at all
	Artifacts []IndexEntry // newest first; read only when Index is true
}

// Static returns the embedded static files, rooted so that "app.css" is served at StaticPath+"app.css".
func Static() fs.FS {
	panic("not implemented")
}

// RenderArtifact executes the artifact template into a buffer. On success it sets
// Content-Type "text/html; charset=utf-8" and Content-Security-Policy PageCSP, writes status
// 200 and then the body. On error it writes nothing, so the caller can still send a 500.
// Callers may set other headers (Vary, Cache-Control) before calling.
func RenderArtifact(w http.ResponseWriter, p ArtifactPage) error {
	panic("not implemented")
}

// RenderLanding renders the landing page, with the same header and error behaviour as RenderArtifact.
func RenderLanding(w http.ResponseWriter, p LandingPage) error {
	panic("not implemented")
}

// RenderNotFound renders the not-found page with status 404, with the same header and error
// behaviour as RenderArtifact.
func RenderNotFound(w http.ResponseWriter) error {
	panic("not implemented")
}
