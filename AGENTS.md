# AGENTS.md

Guidance for AI agents working on this project.

## Project Overview

Sermon Scribe is a web app that turns raw sermon recordings into publish-ready audio with transcriptions and metadata — no external editing tools required.

## Repository

- **GitHub**: https://github.com/ogbbc-webmasters/Sermon-Scribe
- **Branch**: `main`
- **Commits**: Use conventional commits (e.g., `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`)
- **Push access**: Configured via `gh auth setup-git` with personal access token

## Commit Guidelines

- **Commit frequently** — after each working change, not at the end of a session
- Use conventional commits: `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`
- Keep commits small and focused
- Don't wait until asked to commit

## Specture System

This project uses the [Specture System](https://github.com/specture-system/specture) for managing specifications and design records. See `specs/README.md` for full guidelines.

### Quick Reference

- `specture list` — find active specs
- `specture list -d all` — include the full spec tree
- `specture new --title "Spec Title"` — create a top-level spec
- `specture new --title "Child Title" --parent 2` — create a child spec
- `specture validate` — validate the full tree after spec or plan edits

Before implementing a spec, read its `SPEC.md` and any `PLAN.md`. Commit each focused, verified chunk before starting the next. Keep implementation progress out of `SPEC.md`. Do not modify durable design decisions without explicit user permission.
