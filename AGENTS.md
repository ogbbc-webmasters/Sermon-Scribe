# Guidelines

## Always Do (without asking)

- Use conventional commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`)
- Commit after each working change — keep commits small and focused
- Follow the Styling Convention below for any CSS/UI changes

## Ask First (pause)

- Modifying spec design decisions in `SPEC.md` files
- Destructive operations (deleting files, dropping data)

## Never Do (hard stop)

- Commit API keys, secrets, or `.env` files
- Batch unrelated changes into a single commit

## Long Term Memory

- **Project**: Sermon Scribe — a web app that turns raw sermon recordings into publish-ready audio with transcriptions and metadata
- **Repo**: https://github.com/ogbbc-webmasters/Sermon-Scribe (branch: `main`)

### Styling Convention

All CSS lives in `web/styles.css` (served from the embedded `web/` dir).

- **Structure**: sections ordered tokens → base/reset → components (alphabetical) → utilities
- **Design tokens**: every color, radius, shadow, and spacing constant is a `:root` custom property (`--ink`, `--paper`, `--green`, `--shadow`, …); no raw hex in component rules — add a token if one is missing. Warm palette: paper background, dark ink, green accent, muted red for destructive/error states
- **Naming (BEM-lite)**: component = one flat class (`.sermon-card`); part = double underscore (`.sermon-card__name`); modifier = double dash (`.button--primary`)
- **State ownership**: a base class owns layout, shape, typography, and its own neutral colors only; each modifier fully owns its colors including `:hover`/`:active`/`:disabled` — redeclare every state it changes so no base rule needs overriding. No `:not()` chains, no `!important`
- **Specificity budget**: at most one class + one pseudo-class per selector (`.button--danger:hover` is the ceiling)
- **Ui.elm**: `src/Ui.elm` centralizes class names for shared components as `Html.Attribute` helpers (`Ui.primaryButton`, `Ui.card`, …). Use these instead of raw class literals for anything reusable; page-specific one-off classes may stay inline. Add the CSS and the helper together
