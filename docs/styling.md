# Styling Convention

All CSS lives in `web/styles.css` (served from the embedded `web/` dir).
Follow these rules for any CSS/UI change.

## Stylesheet structure

Section order: **tokens → base/reset → components (alphabetical) → utilities**.

## Design tokens

Every color, radius, shadow, and spacing constant is a `:root` custom
property (`--ink`, `--paper`, `--green`, `--radius-md`, `--shadow`, …).
**No raw hex values in component rules** — add a token if one is missing.
The palette is warm: paper background, dark ink, green accent, muted red
for destructive/error states.

## Naming: BEM-lite

- Component = one flat class: `.sermon-card`, `.button`
- Part = double underscore: `.sermon-card__name`, `.confirm-box__buttons`
- Modifier = double dash: `.button--primary`, `.badge--failed`

## State ownership

A base class owns **layout, shape, typography, and its own (neutral)
colors only**. Each modifier **fully owns its colors, including
`:hover`/`:active`/`:disabled`** — it must redeclare background, border,
and text color for every state it changes, so no base state rule ever
needs to be overridden. Consequently: **no `:not()` chains, no
`!important`**.

## Specificity budget

At most **one class + one pseudo-class** per selector
(`.button--danger:hover` is the ceiling).

## Ui.elm

`src/Ui.elm` centralizes class names for shared components as
`Html.Attribute` helpers (`Ui.primaryButton`, `Ui.badgeFailed`,
`Ui.card`, …). Main.elm and future pages must use these instead of raw
class string literals for anything reusable; page-specific one-off
classes (e.g. `masthead`) may stay inline. When adding a shared
component, add the CSS in `styles.css` and the helper in `Ui.elm`
together.
