# Sermon Scribe

A web app that turns raw sermon recordings into publish-ready audio with transcriptions and metadata — no external editing tools required.

The app is a single Go binary: an Elm frontend is compiled to `web/elm.js` and embedded via `go:embed`, alongside a SQLite database and per-sermon file storage on disk.

## Prerequisites

- [Go](https://go.dev/) (version per `go.mod`)
- [Elm 0.19.2](https://github.com/elm/compiler/releases/tag/0.19.2)
- [just](https://github.com/casey/just)
- ffmpeg (not needed yet — required by the upcoming audio-normalization stage)

## Build, Run, Test

```sh
just build   # compile Elm (web/elm.js) then go build -> ./sermon-scribe
just run     # build and run locally on :8000
just test    # go test ./...
```

Server flags: `-addr` (default `:8000`), `-db` (default `sermons.db`), `-uploads` (default `uploads`).

## Deploy

Deployment is a systemd service on the exe.dev VM:

```sh
just deploy
```

This builds the binary, installs it to `/usr/local/bin/sermon-scribe`, installs `deploy/sermon-scribe.service` to `/etc/systemd/system/`, and enables/restarts the service. The service runs as user `exedev` with `WorkingDirectory=/home/exedev/sermon-scribe-data`, which holds `sermons.db` and `uploads/`.

## Authentication

The app contains no auth code. Authentication and authorization are provided entirely by the exe.dev private proxy: unauthenticated visitors never reach the app, and the VM's share list is the authorization boundary. The proxy's `X-Exedev-Email` header is recorded as `uploaded_by` for attribution only.

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs on pull requests and pushes to `main`: it installs Go, Elm 0.19.2, and just, then runs `just build` and `go test ./...`. CI validates only; deployment stays manual on the VM.
