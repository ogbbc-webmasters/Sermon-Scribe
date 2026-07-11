# Specture Guidelines

This repository uses the [Specture System](https://github.com/specture-system/specture) for durable design records and optional agent execution plans.

## Layout

Each spec has a numbered, kebab-case directory containing a `SPEC.md`:

```text
specs/002-feature-name/SPEC.md
specs/002-feature-name/PLAN.md
specs/002-feature-name/001-child-feature/SPEC.md
```

The directory tree determines each spec's reference. Do not put a spec number in frontmatter.

## Durable Specs

`SPEC.md` records the problem, motivation, requirements, and design decisions. It starts with YAML frontmatter containing a valid `status`: `draft`, `approved`, `in-progress`, `completed`, or `rejected`. Optional metadata includes `author`, `creation_date`, `approved_by`, and `approval_date`.

Keep implementation progress, checklists, and temporary execution notes out of `SPEC.md`. Use plain-language, unnumbered headings and repo-root-relative links for cross-spec references.

## Execution Plans

An optional `PLAN.md` is a tactical handoff for implementing its sibling spec. Use it for reviewable work chunks, sequencing constraints, and temporary implementation notes. Plans may change as execution details evolve; durable design rationale belongs in `SPEC.md`.

During implementation, commit each focused, verified chunk before starting the next one.

## CLI Workflow

Use the CLI rather than manually scanning or creating spec paths:

```bash
specture list
specture list -d all
specture new --title "Feature Name"
specture new --title "Child Feature" --parent 2
specture validate
```

Run `specture validate` after changing or migrating `SPEC.md` or `PLAN.md` files. Use `specture <command> --help` to discover additional options.
