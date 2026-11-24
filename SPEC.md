# Artitrove v1 specification

Artitrove is a small, self-hosted store for files produced by AI agents. An agent uploads a file and gets back a durable link. Opening the link in a browser shows a rendered, mobile-friendly view; fetching the raw URL returns the exact bytes.

This file is the single source of truth for v1. If code and spec disagree, one of them is a bug.

## Principles

- Small and opinionated: one job, one binary, one data directory.
- Single owner: no users, teams or permissions.
- Upload is one `curl` command, so any agent can use it.
- Artifacts are immutable. A new version is a new upload with a new link.
- Everything is kept forever. Retention is out of scope for v1.

## Access model

| Action | Needs |
|---|---|
| Upload, delete | Bearer token (`Authorization: Bearer <token>`) |
| Read an artifact (view, raw, metadata) | Only the link. The ID is unguessable, so the link is the permission. |
| Browse the index (when enabled) | HTTP basic auth, any username, token as password |

There are no cookies and no sessions anywhere in the app.

Token comparison is constant-time. A missing or wrong token returns `401` with a `WWW-Authenticate` header.

## Artifacts

| Field | Type | Notes |
|---|---|---|
| `id` | string | 16 bytes from `crypto/rand`, base64url without padding (22 characters) |
| `title` | string | Optional. The human view falls back to the ID. Max 200 characters. |
| `content_type` | string | The media type sent on upload, parameters preserved. Defaults to `application/octet-stream`. |
| `size` | integer | Bytes |
| `sha256` | string | Hex digest of the content |
| `created_at` | string | RFC 3339, UTC |

## HTTP API

Errors are JSON: `{"error": "human-readable message"}` with a fitting status code.

### `POST /api/artifacts` (token)

The request body is the raw file. `Content-Type` sets the artifact's type. An optional `?title=` query parameter sets the title.

```sh
curl -sS --data-binary @report.html \
  -H "Authorization: Bearer $ARTITROVE_TOKEN" \
  -H "Content-Type: text/html" \
  "$ARTITROVE_URL/api/artifacts?title=Latency+report"
```

Response `201 Created`, with a `Location` header set to `url`:

```json
{
  "id": "Zk3vXq9mT2c8RwLp0aBn4g",
  "title": "Latency report",
  "content_type": "text/html",
  "size": 38211,
  "sha256": "9f2c…",
  "created_at": "2026-11-24T14:02:11Z",
  "url": "https://trove.example/a/Zk3vXq9mT2c8RwLp0aBn4g",
  "raw_url": "https://trove.example/a/Zk3vXq9mT2c8RwLp0aBn4g/raw"
}
```

- Empty body: `400`.
- Body larger than `ARTITROVE_MAX_SIZE`: `413`. The limit is enforced while reading, not after.

### `GET /a/{id}/raw` (link)

Returns the exact bytes with the stored `Content-Type`, plus:

- `Content-Security-Policy: sandbox allow-scripts`
- `X-Content-Type-Options: nosniff`
- `Cache-Control: public, max-age=31536000, immutable`
- `ETag` set to the SHA-256 digest

### `GET /a/{id}` (link)

The human view (see below). If the request's `Accept` header does not include `text/html`, the server responds exactly as `/a/{id}/raw` does, so a bare `curl` gets the content.

### `GET /api/artifacts/{id}` (link)

Metadata as JSON, in the same shape as the upload response.

### `DELETE /api/artifacts/{id}` (token)

Removes the artifact's metadata and content. `204` on success, `404` if unknown.

### `GET /`

The landing page: what Artitrove is and how to upload. When `ARTITROVE_INDEX=true`, this page requires basic auth and also lists all artifacts, newest first, with a client-side filter by title and type. When the index is off, no route lists artifacts.

### `GET /healthz`

`200` with `{"status":"ok"}`.

Unknown IDs return `404` on every route.

## Human view

A slim header shows the title, content type, size, creation time, and two actions: open raw, copy link. Below it, the content:

| Content type | Rendering |
|---|---|
| `text/html` | `<iframe sandbox="allow-scripts" src="/a/{id}/raw">`, filling the viewport below the header |
| `text/markdown` | Rendered to HTML server-side, with syntax-highlighted code blocks |
| `application/json` | Pretty-printed, collapsible tree. Invalid JSON falls back to plain text. |
| `text/*` and common code types | Monospace, with line wrapping |
| Anything else | Type, size and a download link to the raw URL |

The view is mobile-first, follows the system light or dark theme, and makes zero external requests: all CSS, JS and fonts are embedded in the binary.

## Security rules

Artifacts are untrusted content written by a model. These rules are not negotiable.

1. **Artifact-authored markup is only ever served by the raw route**, with the sandbox CSP. The sandbox gives the content an opaque origin, so it cannot call the Artitrove API as the app.
2. **App pages never inject artifact content as HTML.** Markdown is rendered with raw HTML disabled and unsafe link schemes stripped. JSON and text go through `html/template` escaping.
3. App pages send `Content-Security-Policy: default-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'`.
4. Every response carries `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. The second matters because the URL is the credential.
5. Artifact pages carry `X-Robots-Tag: noindex`.
6. IDs are never used to build file paths without validation against the ID format.

## Configuration

All configuration is by environment variable.

| Variable | Default | Notes |
|---|---|---|
| `ARTITROVE_TOKEN` | none | Required. The server exits with a clear error if it is unset or shorter than 16 characters. |
| `ARTITROVE_BASE_URL` | `http://localhost:8080` | Used to build `url` and `raw_url`. No trailing slash. |
| `ARTITROVE_LISTEN` | `:8080` | Listen address |
| `ARTITROVE_DATA_DIR` | `./data` | Created on start if missing |
| `ARTITROVE_MAX_SIZE` | `10485760` | Max upload size in bytes (10 MB) |
| `ARTITROVE_INDEX` | `false` | Enables the browsable index on `/` |

## Storage

- `<data>/artitrove.db`: SQLite, metadata only, one `artifacts` table.
- `<data>/blobs/<id>`: one file per artifact, the exact uploaded bytes.

On upload, the content is written to a temp file in the data directory, fsynced, renamed into place, and only then is the metadata row inserted. On delete, the row is removed first, then the file.

## Technology

- Go 1.27, standard library `net/http` with method-and-pattern routing, `log/slog` for logs.
- `modernc.org/sqlite` (pure Go, no cgo).
- `github.com/yuin/goldmark` for Markdown, `github.com/alecthomas/chroma/v2` for highlighting.
- `html/template` with templates and static assets embedded via `embed`.
- No JS build step. Client-side JS is small, hand-written, and optional for reading.
- Graceful shutdown on SIGINT and SIGTERM.

## Repository layout

Each package has one owner task. Tasks do not edit outside their package.

| Path | Contents |
|---|---|
| `cmd/artitrove/` | `main.go`: load config, open store, register routes, serve |
| `internal/artifact/` | Shared types and interfaces: the seams other packages code against |
| `internal/config/` | Environment parsing and validation |
| `internal/store/` | SQLite metadata, blob files, ID generation |
| `internal/render/` | Markdown, JSON and text to safe HTML fragments |
| `internal/ui/` | Embedded templates, CSS, JS, fonts |
| `internal/api/` | `/api/*`, `/a/{id}/raw`, `/healthz`, token check, size limit |
| `internal/web/` | `/a/{id}` human view, `/` landing page and index |
| `e2e/` | Black-box tests that run the real binary |
| `skill/artitrove/` | The agent skill (`SKILL.md`) |
| root | `Dockerfile`, `compose.yaml`, `README.md`, `.github/workflows/ci.yml` |

## Git conventions

- `main` always builds and passes tests.
- One branch per task, named `feat/<task>`, merged into `main` with `--no-ff`.
- Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `chore:`).

## Out of scope for v1

Multi-file artifacts, versioning, expiry, OAuth or OIDC, an MCP server, rate limiting, search over content, editing.
