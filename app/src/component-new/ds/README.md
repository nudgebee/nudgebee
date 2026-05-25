# `components1/ds/` — DS-conformant primitives

This folder is the canonical home for components implementing the new design system (`app/design-system/`).

## Conventions

- **Tokens only**: every CSS color, font size, spacing, radius, and motion value MUST be a `var(--ds-*)` reference. Raw hex / rgba / named colors are forbidden. Lint rule `no-raw-color-hex` enforces.
- **Sourced from `app/design-system/`**: every file in this folder has a corresponding `app/design-system/primitives/.../<name>.html` spec. The HTML is the source of truth for variants, states, props.
- **`<meta name="ds-last-reviewed-on">` bump required**: any PR landing here must bump the spec's review date in the same diff.
- **No bypass**: don't import legacy `colors.*`, `--nb-*` tokens, or MUI `theme.palette.*` directly. Compose from `--ds-*` only.

## File patterns

- **Net-new primitives** (Track B in `COMPONENT_IMPLEMENTATION_PLAN.md`): full implementation lives here.
- **Renamed legacy components** (API-MINOR rename, e.g. `IconTextBadge` → `IntegrationBadge`): file is a thin re-export of the legacy component until the legacy file is migrated in place. Once that migration lands, the implementation moves here and the legacy file becomes the re-export.
- **API-BREAKING V2** (e.g. `Button` consolidating 11 legacy buttons): full V2 implementation lives here, V1 keeps existing path with `console.warn` deprecation notice. 30-day clock to V1 deletion.

## Adoption

- New code MUST import from `@components1/ds/*`.
- Legacy imports from `@components1/common/*` and `@common/*` continue to resolve and are migrated incrementally per `MIGRATION_PLAN.md` Phase 1/2.
