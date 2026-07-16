# Guidelines

## Always Do (without asking)

- Use conventional commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`)
- Commit after each working change — keep commits small and focused
- Push access is configured via `gh auth setup-git` with personal access token

## Ask First (pause)

- Modifying spec design decisions in `SPEC.md` files
- Destructive operations (deleting files, dropping data)

## Never Do (hard stop)

- Commit API keys, secrets, or `.env` files
- Batch unrelated changes into a single commit

## Long Term Memory

- **Project**: Sermon Scribe — a web app that turns raw sermon recordings into publish-ready audio with transcriptions and metadata
- **Repo**: https://github.com/ogbbc-webmasters/Sermon-Scribe (branch: `main`)
