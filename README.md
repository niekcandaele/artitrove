# Artitrove

Artitrove is a small, self-hosted store for files that AI agents produce. An agent uploads a file with one `curl` command and gets back a durable link. Opening the link in a browser shows a rendered, mobile-friendly view, and the raw URL returns the exact bytes.

It is a single Go binary with one data directory. It has one owner and no users, teams or permissions. Artifacts are immutable and kept until you delete them.

## Quick start

Run the released image with Docker Compose. Download the compose file, then generate a token and save it in `.env`, which Compose reads automatically:

```sh
mkdir artitrove && cd artitrove
curl -sSLO https://raw.githubusercontent.com/niekcandaele/artitrove/main/compose.yaml
echo "ARTITROVE_TOKEN=$(openssl rand -hex 32)" > .env
docker compose up -d
```

The service listens on port 8080. To use another host port, set `ARTITROVE_PORT` in `.env`. If clients reach the service under an address other than `http://localhost:<port>`, set `ARTITROVE_BASE_URL` in `.env` as well, because it builds the links the service returns. Compose also forwards `ARTITROVE_INDEX` and `ARTITROVE_MAX_SIZE`. To pin a release, set `ARTITROVE_VERSION` (for example `0.0.1`); the default is `latest`. Data lives in the `artitrove-data` volume.

Check that the service is up, then upload a file:

```sh
curl -sS http://localhost:8080/healthz

export ARTITROVE_URL=http://localhost:8080
export ARTITROVE_TOKEN=<the token from .env>
echo '# Hello' > hello.md
curl -sS --fail-with-body -X POST "$ARTITROVE_URL/api/artifacts" \
  -H "Authorization: Bearer $ARTITROVE_TOKEN" \
  -H "Content-Type: text/markdown; charset=utf-8" \
  --url-query "title=Hello" \
  --data-binary @hello.md
```

The response is JSON. Open its `url` in a browser to see the rendered page. Always send a `Content-Type` header: without one, curl labels the body as a form post and that is what gets stored.

To run without Docker, download the binary for your platform from the [releases page](https://github.com/niekcandaele/artitrove/releases) and start it with `ARTITROVE_TOKEN` set:

```sh
curl -sSL -o artitrove https://github.com/niekcandaele/artitrove/releases/latest/download/artitrove-linux-amd64
chmod +x artitrove
ARTITROVE_TOKEN=$(openssl rand -hex 32) ./artitrove
```

## Configure the service

All configuration is by environment variable. The server exits at start and lists every problem if the configuration is invalid.

| Variable | Default | Description |
|---|---|---|
| `ARTITROVE_TOKEN` | none | Required. At least 16 characters. |
| `ARTITROVE_BASE_URL` | `http://localhost:8080` | The public address of the service, used to build `url` and `raw_url`. Scheme and host only: no path and no trailing slash. |
| `ARTITROVE_LISTEN` | `:8080` | Listen address. |
| `ARTITROVE_DATA_DIR` | `./data` | Data directory, created on start if missing. The container uses `/data`. |
| `ARTITROVE_MAX_SIZE` | `10485760` | Maximum upload size in bytes (10 MiB). Must be a positive integer. |
| `ARTITROVE_INDEX` | `false` | Set to `true` to list all artifacts on `/`. Accepts any Go `ParseBool` value. |

Agents need two variables: `ARTITROVE_URL`, set to the server's `ARTITROVE_BASE_URL` value, and `ARTITROVE_TOKEN`, the same token the server uses.

## Access model

| Action | Needs |
|---|---|
| Upload, delete | Bearer token (`Authorization: Bearer <token>`) |
| Read an artifact (view, raw, metadata) | Only the link |
| Browse the index, when enabled | HTTP basic auth, any username, the token as password |

IDs are 128 random bits, so the link is the permission: anyone who has a link can read that artifact until it is deleted. There are no cookies or sessions.

## Choose how to expose it

Run Artitrove on a private network (LAN, VPN or tailnet) or on localhost if you can. Then only people on that network can upload, and only people with a link can read.

If you expose it publicly, put it behind a reverse proxy that terminates TLS, set `ARTITROVE_BASE_URL` to the `https` address, and use a long random token. Keep these limits in mind:

- The index uses basic auth, which sends the token with every request. Enable `ARTITROVE_INDEX` only behind TLS.
- There is no rate limiting or quota. Anyone who holds the token can fill your disk, and nothing slows down attempts to guess it.
- Links leak through chat logs, browser history and forwarding. The `no-referrer` and `noindex` headers do not prevent that.
- Raw HTML artifacts are untrusted pages written by a model, served from your hostname. The sandbox gives them an opaque origin, so they cannot call the Artitrove API, but their scripts can still send data to other sites, and a page can imitate a login form. Never type credentials into an artifact. Consider a hostname dedicated to Artitrove.
- Nothing expires: an artifact stays until someone deletes it with the token. Nothing is encrypted at rest.

Back up the data directory (`artitrove.db` and `blobs/`). In Compose that is the `artitrove-data` volume.

## API

Errors are JSON: `{"error": "message"}`.

| Route | Needs | Description |
|---|---|---|
| `POST /api/artifacts` | token | Body is the raw file, `Content-Type` sets the type, optional `?title=` (up to 200 characters). Returns `201` with `id`, `title`, `content_type`, `size`, `sha256`, `created_at`, `url`, `raw_url`. |
| `GET /api/artifacts/{id}` | link | Metadata as JSON. |
| `DELETE /api/artifacts/{id}` | token | Removes the artifact. `204`, or `404` if unknown. |
| `GET /a/{id}` | link | Human view. Without `text/html` in `Accept`, it returns the raw bytes, so a bare `curl` works. |
| `GET /a/{id}/raw` | link | The exact bytes with the stored `Content-Type`, under `Content-Security-Policy: sandbox allow-scripts`. |
| `GET /` | basic auth if indexed | Landing page, plus the artifact list when `ARTITROVE_INDEX=true`. |
| `GET /healthz` | none | `{"status":"ok"}`. |

The human view renders HTML (in a sandboxed frame), Markdown, JSON and plain text. Images, PDFs and other types show their type, size and a download link.

## Install the agent skill

`skill/artitrove/SKILL.md` teaches an agent to upload a file and hand back the link. For Claude Code:

```sh
mkdir -p ~/.claude/skills && cp -r skill/artitrove ~/.claude/skills/
```

For other agents, put `SKILL.md` wherever they load instructions from. The agent's environment needs `ARTITROVE_URL` and `ARTITROVE_TOKEN`, and the machine needs `curl`.

## Develop

Go 1.27 is required.

```sh
make build   # bin/artitrove
make test    # go test -race ./...
make lint    # pinned golangci-lint
make run     # needs ARTITROVE_TOKEN
```

To run a local build in Compose, build the image under a tag of your own and select it:

```sh
docker build -t ghcr.io/niekcandaele/artitrove:dev .
ARTITROVE_VERSION=dev docker compose up -d
```

`artitrove healthcheck` probes the running service and is what the container healthcheck calls. `SPEC.md` is the specification for v1.

Pushing a `v*` tag runs the release workflow, which publishes the multi-arch image to `ghcr.io/niekcandaele/artitrove` and attaches Linux and macOS binaries to a GitHub release.

## License

MIT. See `LICENSE`.
