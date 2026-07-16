# Project Setup Plan

Implement [Project Setup](specs/000-basic-webapp/000-project-setup/SPEC.md) in small, reviewable chunks.

## Pull Request Plan

### Chunk 1: Go scaffolding

- `go mod init` (module `github.com/ogbbc-webmasters/Sermon-Scribe`)
- SQLite (modernc.org/sqlite, CGO-free) with hand-rolled embedded migrations (`migrations/*.sql`, `schema_version` table, applied at startup)
- Migration 001: `sermons` table - id (uuid text), original_filename, uploaded_at, uploaded_by (nullable), stage, status
- HTTP server on port 8000 serving a placeholder index page
- `justfile` with `build`, `run`, `test` recipes; `.gitignore` (binary, `web/elm.js`, `*.db`, `uploads/`)

### Chunk 2: Sermon API

- `POST /api/sermons` - multipart upload streamed to `uploads/{sermon_id}/original.<ext>` (extension preserved from filename), 2 GB cap via `http.MaxBytesReader`; creates row with stage `upload`, status `done`; records `X-Exedev-Email` as uploaded_by if present
- `GET /api/sermons` - list, newest first
- `DELETE /api/sermons/{id}` - hard delete row + `uploads/{id}/` directory
- Tests with httptest covering upload, list, delete

### Chunk 3: Elm frontend

- Elm 0.19.2 app (`elm/http`, `elm/json`, `elm/file`): file picker upload with progress (Http.track), sermon list showing filename + upload date + stage/status, delete with confirmation
- Large fonts, high contrast, minimal steps (50+ audience)
- Served via `//go:embed web/` (index.html + elm.js); `just build` compiles Elm with `--optimize` then `go build`

### Chunk 4: Deployment and CI

- systemd unit `sermon-scribe.service` (WorkingDirectory holds `sermons.db` and `uploads/`), install + enable on this VM
- GitHub Actions workflow: setup Go + Elm + just, `just build`, `go test ./...`
- README updates: build/run/deploy instructions

## Implementation Notes

- No auth code whatsoever - the exe.dev private proxy is the boundary (see SPEC.md)
- Stage/status values must match the vocabulary in [Processing Pipeline](specs/000-basic-webapp/001-processing-pipeline/SPEC.md); this spec only produces `upload`/`done` (no job queue yet - that is spec 0.1)
- Keep the upload handler streaming (io.Copy to file), never buffering the body in memory
- Verify with a large (~1 GB) test upload through the proxy before calling the upload flow done
