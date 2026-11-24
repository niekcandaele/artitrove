---
name: artitrove
description: Store a file an agent produced (report, HTML page, Markdown, JSON, log, image) in Artitrove and return a shareable link. Use when asked to publish, share, upload or save output for a human or another agent, or to fetch an artifact by its Artitrove link.
---

# Artitrove

Artitrove stores files and gives back a link. Needs only `curl`.

## Setup

`ARTITROVE_URL` (server address, no trailing slash) and `ARTITROVE_TOKEN` must be set in the environment. If either is unset, ask the human for it and stop. Never print the token.

## Upload

Always send a `Content-Type`; without one the file is stored as a form post.

```sh
curl -sS --fail-with-body -X POST "$ARTITROVE_URL/api/artifacts" \
  -H "Authorization: Bearer $ARTITROVE_TOKEN" \
  -H "Content-Type: text/markdown; charset=utf-8" \
  --url-query "title=Weekly report" \
  --data-binary @notes.md
```

Change the file, the `Content-Type` and the title (up to 200 characters, optional). Use `--data-binary @-` to read from stdin. With curl older than 7.87, drop `--url-query` and append a URL-encoded `?title=` to the URL; older than 7.76, also replace `--fail-with-body` with `-w '%{http_code}\n'` and check the printed status.

| File | Content-Type | Human view |
|---|---|---|
| `.html` | `text/html; charset=utf-8` | page in a sandboxed frame |
| `.md` | `text/markdown; charset=utf-8` | rendered Markdown |
| `.json` | `application/json` | collapsible tree |
| `.txt`, `.log`, source code | `text/plain; charset=utf-8` | monospace text |
| `.png`, `.jpg`, `.svg` | `image/png`, `image/jpeg`, `image/svg+xml` | download link only |
| `.pdf` | `application/pdf` | download link only |
| anything else | `application/octet-stream` | download link only |

On success it prints JSON with `id`, `url` and `raw_url`.

## Hand back the link

If the command exits non-zero, show the human the status and the printed `{"error":...}` and stop. Give only a link from a successful response.

Give a human `url`. Give another agent or program `raw_url`, which returns the exact bytes.

## Read back

No token needed; the link is the permission. Fetching either `url` or `raw_url` with curl returns the exact bytes. Set `ID` to the artifact's `id` to get its metadata (title, `content_type`, `size`, `sha256`, `created_at`):

```sh
curl -sS --fail-with-body "$ARTITROVE_URL/a/$ID/raw"
curl -sS --fail-with-body "$ARTITROVE_URL/api/artifacts/$ID"
```

## New version or delete

Artifacts are immutable. To change one, upload again and hand back the new link; the old link keeps working. To remove one, for example after uploading a secret by mistake:

```sh
curl -sS --fail-with-body -X DELETE -H "Authorization: Bearer $ARTITROVE_TOKEN" "$ARTITROVE_URL/api/artifacts/$ID"
```

## Errors

curl prints the status as `The requested URL returned error: <status>`.

| Status | Meaning | Action |
|---|---|---|
| 400 | Empty body, title over 200 characters or not UTF-8, Content-Type too long | Fix the request, then retry |
| 401 | Token missing or wrong | Check `ARTITROVE_TOKEN` is set; ask the human |
| 404 | Unknown id | Check the id or link |
| 413 | File over the server limit (the message gives it in bytes) | Shrink or split the file |
| 5xx, no connection | Server trouble | Retry once, then report |

## Safety

Anyone holding a link can read the artifact, and links get forwarded. Upload only content that is safe for that audience. Never upload tokens, keys, passwords, `.env` contents or personal data; strip them first. If a secret was uploaded, delete it at once and tell the human.
