# Sermon Scribe

A web app that turns raw sermon recordings into publish-ready audio with transcriptions and metadata — no external editing tools required.

The app is a single Go binary: an Elm frontend is compiled to `web/elm.js` and embedded via `go:embed`, alongside a SQLite database and per-sermon file storage on disk. Design details (including the auth model) live in [`specs/`](specs/).

## Repository layout

```text
cmd/        Go executable entry point
internal/   Private Go server and storage packages
web/        Elm project, static assets, and embedded-file package
deploy/     Service configuration
specs/      Product and implementation specifications
```

## Prerequisites

- [Go](https://go.dev/) (version per `go.mod`)
- [Elm 0.19.2](https://github.com/elm/compiler/releases/tag/0.19.2)
- [just](https://github.com/casey/just)
- ffmpeg (not needed yet — required by the upcoming audio-normalization stage)

## Quickstart

```sh
just build    # compile Elm (web/elm.js) then go build -> ./sermon-scribe
just run      # build and run locally on :8000
just test     # go test ./...
just deploy   # build and (re)install the systemd service on this VM
```
