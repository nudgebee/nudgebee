# Component Usage & Composition Guide

**Audience:** developers and AI agents building or redesigning pages with the `component-new/` library.
**What this doc is:** the **"when & how"** layer — which component to reach for in a given UI
situation, and how components compose into complete views.
**What this doc is _not_:** a per-component API reference. Props, variants and "Don't" rules live
in each file's JSDoc header (e.g. [`ds/ListingLayout.tsx`](src/component-new/ds/ListingLayout.tsx)
has a full "Anatomy" block) and in the per-primitive pages of the design-system viewer.

---

## 0. Why this doc exists, and how it's kept current

The design-system viewer and `manifest.json` already answer **"what components exist."** They do
not answer **"which ones do I use for a table view, and how do they fit together."** That gap is
what this doc fills.

**Recommended approach (decided):** a **hand-written guide** (this file). A fully _generated_ doc
was considered and rejected for now — composition recipes ("how to build a table view") encode
human judgement that can't be derived from code, and there is no doc-generator in the repo today.
The longer-term target is a **hybrid**: a generated component index (harvested from the JSDoc
headers) plus these hand-written recipes. Until a generator exists, this file is hand-maintained.

**Keeping it in sync:** this doc only documents _patterns and decisions_, which change rarely.
Per-component props are **not** copied here — always read the component file's JSDoc for those.
When a new recurring composition appears, add a recipe (§2). When two components overlap, add a
decision rule (§3).

---

## 1. Component index — by purpose

`ds/` = design-system primitive (`@components1/ds/*`). `common/` = domain composition
(`@common-new/*`) — an app-specific component built from primitives.

### Layout & page shell

| Component                         | Where   | Use when                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| --------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ListingLayout`                   | ds/     | The card shell for any table/listing screen — toolbar + body + footer slots                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `Card`                            | ds/     | **Canonical content card** — `variant` (elevated/outlined/accent/**tinted**) × `size` (sm/md/lg) × `elevation` (raised/flat). Slots: `header` / `footer` / `children`. Use for all new card surfaces. **`variant='tinted'` + `tone`** gives a coloured-background panel (neutral→gray-100, info→blue-100, success→green-100, warning→amber-100, danger→red-100) — use for nested form panels inside modals, callout containers, and subtle visual grouping. Tinted is always flat (shadow on a coloured bg compounds visual weight). |
| `WidgetCard` · `CustomBorderCard` | ds/     | Legacy plain content cards — consolidated into `Card`. Co-exist; **don't introduce new uses.**                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `CollapsableCard`                 | ds/     | A single collapsible card (one unit — _not_ an accordion). Composes `Card` for the surface.                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `Divider` · `List`                | ds/     | Rules and simple item lists                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `BoxLayout2`                      | common/ | Legacy filter-bar + content shell — prefer `ListingLayout` for new work                                                                                                                                                                                                                                                                                                                                                                                                                                                              |

### Tables & data display

| Component                                                                                    | Where   | Use when                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| -------------------------------------------------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Table` · `TableCell`                                                                        | ds/     | A **simple** table — plain columns, sorting, no grouped headers                                                                                                                                                                                                                                                                                                                                                                                   |
| `CustomTable2`                                                                               | common/ | A **complex** table — grouped headers, expandable rows, resizable/sticky columns, pivot, column selector, built-in pagination                                                                                                                                                                                                                                                                                                                     |
| `Pagination`                                                                                 | ds/     | Pager for a `ds/Table` (CustomTable2 paginates itself)                                                                                                                                                                                                                                                                                                                                                                                            |
| `Stat` · `Trend` · `CostCallout` · `Comparison`                                              | ds/     | Metric / KPI / cost / before-after display                                                                                                                                                                                                                                                                                                                                                                                                        |
| `Label` · `Chip` · `NBStatusBadge` · `StatusIndicator` · `SeverityIcon` · `IntegrationBadge` | ds/     | Tags, status pills, severity and integration markers. `Chip` has 7 variants (`filter`/`tag`/`status`/`input`/`action`/`count`/`avatar`), 5 sizes (`micro`→`md`), 8 tones (incl. `savings`/`waste`/`agent`), and 8 categorical `hue` values for tag chips — use exported `hashHue(key)` for a stable string→hue mapping. `displayTooltip` + `tooltipCharLimit` auto-truncates long labels with a hover tooltip. See §3 for Chip vs Label decision. |

**Deprecated — labels & status:**

- `common/CustomLabels` — **deprecated**. Use `ds/Label`. `CustomLabels` auto-derived tone from text content; `ds/Label` requires an explicit `tone` prop (`default` / `info` / `success` / `warning` / `danger`). Migrate call sites opportunistically; do not introduce new uses.

### Content & formatting

| Component                                             | Where | Use when                          |
| ----------------------------------------------------- | ----- | --------------------------------- |
| `Format` (Currency/Number/Memory/Datetime/Text)       | ds/   | Render a typed value consistently |
| `Markdown` · `DiffViewer` · `ConsoleOutput` · `Chart` | ds/   | Rich content blocks               |

### Forms & inputs

| Component                                        | Where   | Use when                                                                                                                                                                                                                                                                                                                                                                         |
| ------------------------------------------------ | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Form` (+ `.Section`/`.Field`/`.Row`/`.Actions`) | ds/     | **Layout primitive for any form-shaped UI** — modals, settings pages, side panels. Container-agnostic; controls label placement (stacked vs split), field gap, section structure, related-field rows. See §2.3.                                                                                                                                                                  |
| `Input`                                          | ds/     | All text entry — single line, textarea, password, email, URL. Supports `prefix`/`suffix`/`leadingIcon`/`trailingIcon`. Replaces the deleted `TextField` + `SearchInput` stubs and the legacy `CustomTextField`.                                                                                                                                                                  |
| `Checkbox` · `Switch` · `ToggleGroup`            | ds/     | Boolean / segmented controls                                                                                                                                                                                                                                                                                                                                                     |
| `Select`                                         | ds/     | Value picker for a **form field** — single by default, multi via `multiple` prop. Built-in search auto-shows at >8 options. Field-shaped trigger that matches `Input` chrome.                                                                                                                                                                                                    |
| `Autocomplete`                                   | ds/     | Searchable, async, free-typing value picker                                                                                                                                                                                                                                                                                                                                      |
| `DateRangePicker`                                | ds/     | Date and date-range input                                                                                                                                                                                                                                                                                                                                                        |
| `CustomDateTimePicker`                           | common/ | Single date + time picker with DS-matched Input chrome. Props: `size` (`xs`/`sm`/`md`/`lg`), `label`, `error`, `helperText`, `required`, `disabled`, `minDate`, `maxDateTime`, `views`, `format`. Set `preventDirectInput` to block keyboard/paste so the user must interact via the calendar popup only. Use for single datetime fields; use `DateRangePicker` for date ranges. |
| `FilterDropdown`                                 | ds/     | Value picker for a **toolbar / filter bar** — inline pill trigger with clear-X. See §3 for "form vs filter" rule.                                                                                                                                                                                                                                                                |

**Deprecated / removed in the May-2026 form-primitive consolidation:**

- `ds/TextField` — **deleted**. Use `Input`.
- `ds/SearchInput` — **deleted**. Use `<Input leadingIcon={<SearchIcon />} />`.
- `ds/MultiSelect` — **deprecated re-export**. Use `<Select multiple value={[…]} onChange={…} />`.
- `common/CustomTextField` (legacy `components1/common/`) — still in use; migrate to `Input` opportunistically.
- `common/CustomDropdown` — **stays** as the cluster / cloud-account picker domain composition. Don't use for new generic dropdowns.

### Navigation & filtering

| Component                        | Where       | Use when                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| -------------------------------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `CustomTabs`                     | common-new/ | **Canonical tabs primitive for this project.** Import: `import CustomTabs from '@common-new/CustomTabs'` (NOT `@components1/common/CustomTabs`, which resolves to a different legacy MUI-styled file). Single-level tabs with sliding pill indicator. `behavior='filter'` for state-only (modals, in-page toggles); `behavior='router'` for URL-driven nav. `variant='primary'` (34px tab, brand-600 underline 2px below) for top-level nav; `secondary` (30px, no underline) for inner subtabs. Set `showSurface={false}` for naked tab strips. **Per-tab icon size:** pass `iconSize: <pixels>` on individual `tabOptions` entries to override the per-variant default (20px primary, 18px secondary). **Use this — not `ds/Tabs`.** |
| `Tabs`                           | ds/         | DS V2 tabs primitive. **Not the default in this project** — use `@common-new/CustomTabs` instead. Kept available for future re-evaluation when Phase 2 visual styling lands.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `PageTabs`                       | ds/         | Route-aware top-of-page tabs                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `AnchorComponent`                | common/     | Top-of-page **2-level** nav — parent tabs with optional hover-popover dropdowns of subtabs, hash-driven routing (`manageRoute`). Renders parent tabs above a `CustomTabs` row of subtabs.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `Toggle`                         | ds/         | Compact button-row switcher — 2-4 narrow choices visible at once (e.g. "Yours" / "Team"). State-only, not a form input. Sizes: `default` / `large` / `sm`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `Stepper`                        | ds/         | Multi-step progress indicator                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `Link`                           | ds/         | Inline navigation link. `openInNew` opens in a new tab and appends an external-link icon. `secondaryText` uses caption-size font for dense / small-print contexts. `maxWidth` truncates with ellipsis and shows the full text in a hover tooltip. Don't use for actions — use `<Button tone='link'>` instead.                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `CustomTicketLink`               | common/     | Domain composition for the **"Ticket - {id}"** inline pattern. Renders `ds/Link` when `ticketURL` is present, plain `Text` as fallback. Props: `ticketURL`, `ticketID`, `showAutoEllipsis` (default `true`), `maxWidth` (default `120px`). For new code that doesn't need the prefix label, compose inline: `<Text value='Ticket -' secondaryText /> <Link href={url} target='_blank' secondaryText>{id}</Link>`.                                                                                                                                                                                                                                                                                                                      |
| `FilterGroup` · `FilterDropdown` | ds/         | Filter controls for a listing toolbar                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `AutoRefresh`                    | ds/         | The auto-refresh interval control                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |

### Actions, feedback, overlays

| Component                                                                      | Where   | Use when                                                                                                                                                                                                                                                                                                                                                                                                  |
| ------------------------------------------------------------------------------ | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Button` · `DropdownMenu`                                                      | ds/     | Buttons and action menus                                                                                                                                                                                                                                                                                                                                                                                  |
| `CopyButton`                                                                   | common/ | Icon-only button that copies `text` to clipboard. Shows check-icon feedback for 2 s, then resets. Props: `text` (required), `size` (`xs`/`sm`/`md`/`lg`, default `md`), `tone` (default `ghost`), `toastMessage` (optional — shows a success toast).                                                                                                                                                      |
| `DownloadButton`                                                               | common/ | **Recommended** download trigger — wraps `ds/Button` (secondary, icon-only) with `file-saver` logic. Pass an `async onClick()` that returns one of: `{ data, fileType, fileName }` (blob), `{ canvasId }` (PNG screenshot), `{ table: { header, data }, fileName }` (CSV from array), or `{ tableId }` (CSV from a DOM table). Prefer this over wiring `ds/Button` + `saveAs` manually at each call site. |
| `Banner` · `Toast`                                                             | ds/     | Inline page banners / transient notifications                                                                                                                                                                                                                                                                                                                                                             |
| `EmptyState` · `Skeleton` · `ProgressBar` · `ProgressLinear` · `ErrorBoundary` | ds/     | Empty / loading / progress / error states                                                                                                                                                                                                                                                                                                                                                                 |
| `Modal` · `Dialog` · `Popover` · `Tooltip` · `Inspector`                       | ds/     | Overlays (see §3 for which is which)                                                                                                                                                                                                                                                                                                                                                                      |
| `DiffCard` · `SourceCitation` · `StreamingIndicator` · `ConfidenceIndicator`   | ds/     | AI / agentic surfaces                                                                                                                                                                                                                                                                                                                                                                                     |

**Deprecated — overlays:**

- `common/CustomTooltip` (including `component-new/common/CustomTooltip.tsx`) — **deprecated**. Use `ds/Tooltip`. `ds/Tooltip` is a drop-in replacement with an identical prop API (`title`, `desc`, `variant`, `placement`, `linkUrl`, `linkText`). All new code uses `ds/Tooltip`; migrate legacy call sites opportunistically.

---

## 1.5 Typography conventions

Two font families, picked by purpose:

| Use for                                              | Token                                            | Resolves to               |
| ---------------------------------------------------- | ------------------------------------------------ | ------------------------- |
| **Labels, headings, section titles**                 | `var(--ds-font-display)` _(explicit)_            | Poppins                   |
| **Body text, input values, table cells, paragraphs** | _inherit body default_ — set **no** `fontFamily` | Roboto (via MUI body)     |
| **Code, kbd, numeric monospace**                     | `var(--ds-font-mono)` _(explicit)_               | JetBrains Mono / Consolas |

Why split: a Poppins label above a Roboto input produces the "field has a clear label" affordance
users expect. Setting a single font on everything makes form fields, headings, and body text blur
into the same visual weight — that's the failure mode we're moving away from.

This is built into the new DS form primitives — `ds/Input`, `ds/Select`, `ds/FilterDropdown` —
their labels render in `--ds-font-display` automatically, their values inherit. If you build a new
field-shaped primitive, follow the same rule: **explicit display font on the label, inherit on
the value.** Don't set `fontFamily: var(--ds-font-sans)` on input elements — that forces Inter and
breaks the convention.

---

## 1.6 Form fields vs toolbar filters

`Select` and `FilterDropdown` look similar (both pick from a list) but they're different
affordances answering different user questions:

|                        | `Select` (form field)                                                | `FilterDropdown` (toolbar filter)                                                         |
| ---------------------- | -------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| **The question**       | "What value goes here?"                                              | "Narrow what I'm seeing"                                                                  |
| **User intent**        | Commit a value to a form                                             | Adjust the current view                                                                   |
| **What "empty" means** | "You haven't filled this in yet" (potentially an error)              | "No filter applied — showing everything" (valid)                                          |
| **Trigger shape**      | Field — full-width, label above, error below, matches `Input` chrome | Pill — inline-flex, content-width, blue-300 border when applied, clear-X visible when set |
| **Active affordance**  | None — empty vs filled is the only state                             | Trigger turns blue when a filter is set; clear-X to reset                                 |
| **Where it lives**     | Inside a `<form>` (UserModal, settings panels)                       | Inside a toolbar above a table (ListingLayout.Toolbar)                                    |
| **Value lifecycle**    | Form state — submitted with the form                                 | URL query params / view preferences — survives reload                                     |

**Same popup chrome** — both render their option list through the shared `OverlaySurface` and
`OverlayItem` primitives, so the rounded 10px radius, layered shadow, item-row geometry, hover wash,
and animation are byte-identical. Only the **trigger** differs.

**Picking between them is a UX call, not a code call.** If the same component had a `variant='form'`
vs `variant='filter'` prop, the API would balloon (required + error + helperText only apply to one,
clearable + active-color only to the other) and authors would pick wrong. The split keeps each API
honest.

---

## 1.7 Overlay primitives — what's shared

When a new component needs a popup / menu / dropdown surface, **reach for these instead of
re-styling MUI's Menu or Popover**:

- **`OverlaySurface`** — the popover surface (10px radius, layered shadow, anchor positioning, slide-in animation). Backed by MUI Menu.
- **`OverlayItem`** — one row inside a surface. `size` (`sm`/`md`), `tone` (`default`/`danger`), `selected` (for value pickers), `icon`/`kbd`/`badge` slots.
- **`OverlaySection`** / **`OverlaySeparator`** — section headers and dividers.
- **`OverlayCheckbox`** — the 16×16 blue-when-checked square used in multi-select rows.
- **`OverlayScrollBox`** — max-height + styled scrollbar wrapper for the items list.
- **`OverlaySearch`** — search input row pinned at the top of a surface.
- **`OverlaySelectAll`** — "Select All" / "Clear All" row for multi-select lists.

All live in `ds/internal/Overlay.tsx`. They're **not for app code** — only consumed by other `ds/*`
components. The visual tokens that drive them live in `--ds-overlay-*` (see `app/src/styles/theme-tokens.css`).
Changing a token retones every overlay consumer at once.

Components that already compose them: `DropdownMenu`, `Select` (single + multi), `FilterDropdown`
(checkbox only — full migration deferred).

---

## 1.8 Design system tokens (`--ds-*`)

All visual tokens live in [`app/src/styles/theme-tokens.css`](src/styles/theme-tokens.css) as CSS
custom properties. Use them — never hardcode a hex value, px size, or radius that has a token
equivalent.

### Token categories

| Category       | Prefix                                                                           | Scale                                                                                        |
| -------------- | -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Background     | `--ds-background-*`                                                              | `100` (#fff) · `200` · `300`                                                                 |
| Brand          | `--ds-brand-*`                                                                   | `100`–`700` (light → dark navy)                                                              |
| Gray           | `--ds-gray-*`                                                                    | `100`–`700` + `alpha-100`–`alpha-700` (rgba steps)                                           |
| Semantic color | `--ds-blue-*` · `--ds-red-*` · `--ds-green-*` · `--ds-amber-*` · `--ds-yellow-*` | `100`–`700` per hue                                                                          |
| Spacing        | `--ds-space-*`                                                                   | `0`=2px · `1`=4px · `2`=8px · `3`=12px · `4`=16px · `5`=24px · `6`=32px · `7`=48px           |
| Radius         | `--ds-radius-*`                                                                  | `sm`=4px · `md`=6px · `lg`=8px · `xl`=12px · `pill`=999px                                    |
| Font size      | `--ds-text-*`                                                                    | `caption`=11px · `small`=12px · `body`=13px · `body-lg`=14px · `title`=16px · `heading`=20px |
| Font weight    | `--ds-font-weight-*`                                                             | `regular`=400 · `medium`=500 · `semibold`=600                                                |
| Font family    | `--ds-font-*`                                                                    | `sans` (Inter) · `display` (Poppins) · `mono` (JetBrains Mono)                               |
| Overlay        | `--ds-overlay-*`                                                                 | Shadow, radius, padding, animation for all popover surfaces                                  |
| Motion         | `--ds-motion-*`                                                                  | `micro` · `panel` · `ease`                                                                   |

### Using tokens in code

**Option A — raw CSS variable** (anywhere a CSS value is accepted):

```tsx
<Box
  sx={{
    backgroundColor: 'var(--ds-background-100)',
    border: '1px solid var(--ds-brand-300)',
    borderRadius: 'var(--ds-radius-lg)',
    padding: 'var(--ds-space-3) var(--ds-space-4)',
    fontSize: 'var(--ds-text-body)',
    fontWeight: 'var(--ds-font-weight-semibold)',
  }}
/>
```

**Option B — `ds` object from `@utils/colors`** (typed, autocomplete-friendly — preferred in `.tsx`/`.ts`):

```tsx
import { ds } from '@utils/colors';

<Box
  sx={{
    backgroundColor: ds.background[100], // 'var(--ds-background-100)'
    color: ds.brand[600], // 'var(--ds-brand-600)'
    borderRadius: ds.radius.lg, // 'var(--ds-radius-lg)'
    padding: `${ds.space[3]} ${ds.space[4]}`, // 'var(--ds-space-3) var(--ds-space-4)'
    fontSize: ds.text.body, // 'var(--ds-text-body)'
    fontWeight: ds.weight.semibold, // 'var(--ds-font-weight-semibold)'
  }}
/>;
```

### `ds.space.mul` — multiplied spacing

When you need a multiple of a base step, use `ds.space.mul(step, multiplier)` instead of
hardcoding the computed value. Returns a CSS `calc()` string:

```tsx
ds.space.mul(2, 3)    // 'calc(var(--ds-space-2) * 3)'   → 24px
ds.space.mul(1, 2)    // 'calc(var(--ds-space-1) * 2)'   → 8px

// In practice
<Box sx={{ width: ds.space.mul(6, 2) }} />  // 64px
```

`step` is typed `0 | 1 | 2 | 3 | 4 | 5 | 6 | 7` — an out-of-range value is a compile error.

### Rules

- **Never hardcode** a color, spacing, radius, or font size that has a `--ds-*` token.
- **Prefer Option B** (`ds.*`) in `.tsx`/`.ts` — typed access catches typos at compile time.
- **Use raw `var(--ds-*)`** in `.css` files and Emotion template literals where the `ds` import would be awkward.
- `--ds-space-0` (2px) is the smallest legal spacing token — use for tight insets (scrollbar thumb radius, chip internal padding). Do not introduce sub-2px spacing.
- Do **not** use `--nb-*` tokens in new DS components. `--nb-*` are legacy Nudgebee tokens living in `theme-tokens.css`; new DS primitives reference only `--ds-*`.

---

## 2. Composition recipes

### 2.1 Recipe — Table / listing view ⭐ worked example

The standard table screen (recommendations, inventory, audit lists). Built from a **shell**
(`ListingLayout`) with primitives slotted in, plus a **table** in the body.

**Anatomy** (from [`ds/ListingLayout.tsx`](src/component-new/ds/ListingLayout.tsx) — read its JSDoc):

```
ListingLayout                     ← card chrome (WidgetCard inside)
├── ListingLayout.Toolbar         ← header: title + filters (left) + actions (right)
│     ├── FilterDropdown / SearchInput / Chip   (left, filter widgets)
│     ├── ListingLayout.ToolbarSpacer           (pushes the rest right)
│     └── Button / AutoRefresh / DropdownMenu   (right, action cluster)
├── ListingLayout.Body            ← the table
│     └── Table  *or*  CustomTable2
└── ListingLayout.Footer          ← Pagination  (omit when CustomTable2 paginates itself)
```

**Code skeleton:**

```tsx
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Button } from '@components1/ds/Button';
import { AutoRefresh } from '@components1/ds/AutoRefresh';
import FilterDropdownButton from '@components1/ds/FilterDropdown';
import CustomTable2 from '@common-new/tables/CustomTable2';

<ListingLayout id="recommendations">
  <ListingLayout.Toolbar
    title="Recommendations"
    actions={<><AutoRefresh ... /><Button>Export</Button></>}
  >
    <FilterDropdownButton ... />
    <FilterDropdownButton ... />
  </ListingLayout.Toolbar>

  <ListingLayout.Body>
    <CustomTable2 headers={...} tableData={...} loading={...} />
  </ListingLayout.Body>
</ListingLayout>
```

**Pick the table — `Table` vs `CustomTable2`:**

- **Simple table** → `ds/Table` in `Body` + `ds/Pagination` in `ListingLayout.Footer`.
- **Complex table** (grouped/`upperHeaders`, expandable rows, resizable or sticky columns, pivot,
  column selector) → `CustomTable2`. It brings its **own** pagination, empty-state and tabs, so
  put it in `Body` and **omit `ListingLayout.Footer`**. See [§3](#3-decision-rules).

**Don'ts** (from `ListingLayout`'s JSDoc):

- Don't put page-level `Stat` summary cards inside `ListingLayout` — they are siblings _above_ it.
- Don't paginate inside `Body` when using `ds/Table` — pagination is a `Footer` primitive.
- Don't grow `ListingLayout`'s prop API — compose primitives into the slots instead.

### 2.2 Recipe — Modal dialog ⭐ worked example

The unified `Modal` ([`ds/Modal.tsx`](src/component-new/ds/Modal.tsx)) covers both shapes the
legacy `Modal` + `NDialog` used to split: a plain modal shell (form, editor, settings panel) **and**
a decision dialog (confirm / cancel). Pick the **footer mode** based on the shape of the action —
the header chrome, loader behaviour, success state, backdrop guard, and a11y are identical either
way.

**Anatomy** (from [`ds/Modal.tsx`](src/component-new/ds/Modal.tsx) — read its JSDoc):

```
Modal                                ← Paper + 16px header + token-driven chrome
├── Header                           ← title + subtitle + rightComponentOnTitle + X close
├── Body
│     ├── children                   ← main body content (DialogContent)
│     └── additionalComponent        ← optional NDialog-parity block below DialogContent
└── Footer  — pick ONE mode:
      ├── actionButtons              ← freeform JSX (3+ buttons, special tones, custom layout)
      └── confirmText + onConfirm    ← standard Cancel + Confirm preset (NDialog parity)
```

**Footer mode A — `confirmText` preset (the default for decisions):**

```tsx
import { Modal } from '@components1/ds/Modal';

<Modal
  open={open}
  handleClose={onClose}
  title='Delete workflow?'
  confirmText='Delete'
  onConfirm={handleDelete}
  confirmDisabled={!isAuthorized}
  loader={isDeleting}
  backdropClickClose={false} // block backdrop + Escape mid-submit
>
  This action cannot be undone.
</Modal>;
```

What you get for free: DS `Button` rendering (primary-navy Confirm + brand-toned secondary Cancel),
140px min-width, right-aligned `DialogActions`, `loader` auto-disables both buttons, consistent
visual rhythm across every confirmation dialog. Knobs: `confirmDisabled`, `isConfirmRequired` /
`isCancelRequired` (hide one button), `loader`, `backdropClickClose`.

**Footer mode B — `actionButtons` (freeform):**

```tsx
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import Stack from '@mui/material/Stack';

<Modal
  open={open}
  handleClose={handleClose}
  title='Create Ticket'
  width='md'
  loader={isSubmitting}
  actionButtons={
    <Stack direction='row' gap='12px' sx={{ button: { minWidth: '140px' } }}>
      <Button tone='secondary' size='md' onClick={handleCancel} disabled={isSubmitting}>
        Cancel
      </Button>
      <Button size='md' onClick={handleSubmit} disabled={isSubmitting || account === 'demo'}>
        Create Ticket
      </Button>
    </Stack>
  }
>
  <TicketFormSection ... />
</Modal>
```

Use `actionButtons` whenever the preset can't express what you need: 3+ buttons, ghost / danger /
link tones, non-button content (links, helper text), split close paths (Cancel runs cleanup the X
button shouldn't), or custom layout. Render footer buttons through `ds/Button` so the freeform
footer matches the preset's chrome.

**Pick the footer — `confirmText` vs `actionButtons`:**

| Footer shape                                                               | Use                                                            |
| -------------------------------------------------------------------------- | -------------------------------------------------------------- |
| Cancel + one verb, single close path                                       | `confirmText` preset                                           |
| Single "Close" button (informational modal)                                | `confirmText='Close'` with `isCancelRequired={false}`          |
| Cancel needs cleanup the X / backdrop shouldn't run                        | `actionButtons` (two close paths can't be expressed in preset) |
| 3+ buttons, or ghost / danger / link tones in the footer                   | `actionButtons`                                                |
| Non-button content in the footer (helper link, dropdown, status indicator) | `actionButtons`                                                |
| No footer at all (dismissible only via X)                                  | omit both props                                                |

**Other useful modal patterns:**

- **Loader** — set `loader={isSubmitting}`. Renders `LinearLoader` at the top of the dialog,
  blurs the body, and (in the preset mode) auto-disables both footer buttons.
- **Backdrop / Escape guard** — set `backdropClickClose={false}` to block backdrop clicks AND
  Escape-key closes. Use alongside `loader={true}` to prevent users dismissing the modal
  mid-submit. Default `true`.
- **Full-bleed footer (tinted bg, status text + buttons)** — pass `actionButtonsFullBleed={true}`
  on Modal. This drops `DialogActions`'s default 8px padding + flex layout so the freeform
  `actionButtons` JSX can extend edge-to-edge. **Gotcha:** the outer Box inside `actionButtons`
  needs `boxSizing: 'border-box'` — otherwise `width: 100%` + own padding overflows the modal and
  clips the right-most button. Default behavior (no `actionButtonsFullBleed`) suits plain button
  clusters that should sit 8px inset from the modal edges.
- **`ds/Select` inside a Modal** — works by default. `ds/Select` defaults to `disablePortal={false}`
  so the popup escapes the Modal Paper's transformed subtree. Modal centers via `position: fixed` +
  `transform`, which would turn the Paper into the containing block for an otherwise-absolute
  Popover, breaking anchor alignment. Override with `disablePortal={true}` only when the caller
  needs the popup to live inside the trigger's DOM ancestry.
- **Success state** — set `onSuccess={true}` + `message='…'` + optional `icon`. Renders the
  legacy success layout (84×84 icon + centered message + "Close" button). `type='PASSWORD_CHANGE'`
  swaps to the key icon.
- **Tall content** — set `maxHeight='600px'`. Clamps both the Paper and the inner `DialogContent`,
  enabling internal scroll.
- **Right-side header slot** — `rightComponentOnTitle={<HelpLink />}` renders next to the close X.
- **NDialog-parity extra panel** — `additionalComponent={<OptionsList />}` renders a padded,
  scrollbar-hidden block **outside** `DialogContent`. Use for option lists or form panels that sit
  below the main body copy.

**Don'ts:**

- Don't pass both `actionButtons` and `confirmText` — `actionButtons` wins and the preset is
  silently ignored. Pick one.
- Don't render `common/NewCustomButton` inside `actionButtons` — use `ds/Button` so the freeform
  footer chrome matches the preset.
- Don't put two primary buttons in the same footer (DS `Button` rule: one primary per surface).
- Don't use `Modal` for a small overlay anchored to a trigger — that's `Popover`.
- Don't use `Modal` for a side-drawer detail view — that's `Inspector`.
- Don't auto-wrap modal body text in `DialogContentText` — pass plain JSX as `children`. If you
  need the alert-description a11y id, wrap the description yourself:
  `<DialogContentText id='alert-dialog-description'>…</DialogContentText>`.
- Don't introduce new uses of legacy `@components1/common/modal` or
  `@components1/common/modal/NDialog` — those stay live during migration but every new dialog goes
  through `@components1/ds/Modal`.

### 2.3 Recipe — Form layout ⭐ worked example

`Form` ([`ds/Form.tsx`](src/component-new/ds/Form.tsx)) is the layout primitive for **any**
form-shaped UI — inside a `Modal` body, inside a `Card`, on a settings page, in an `Inspector`,
or standalone. The container owns outer padding; `Form` owns internal layout (section spacing,
field spacing, label placement, row layout).

Two layout variants cover ~95% of real forms across top design systems (Stripe, GitHub, Linear,
Vercel, Figma, Atlassian, Carbon, Material). Multi-column **forms** are an anti-pattern at scale —
eye-tracking research shows users miss fields in dense grids. The legitimate use of side-by-side
fields is `Form.Row` for tightly-related pairs (first/last name, city/state/zip, date ranges).

**Anatomy:**

```
Form                                  ← OPTIONAL wrapper: only needed for a real <form onSubmit>
                                        or to set a default variant/density for all nested Sections
├── Form.Section                      ← labeled group: title + description + optional divider.
│                                       Accepts its OWN `density` / `variant` props that override
│                                       the inherited context. Standalone-usable.
│     ├── Form.Field                  ← label + description + control + helperText/error
│     ├── Form.Field
│     └── Form.Row                    ← side-by-side related fields
│           ├── Form.Field
│           └── Form.Field
├── Form.Section
│     └── Form.Field
└── Form.Actions                      ← submit/cancel — omit when inside a Modal (Modal owns footer)
```

**When do you wrap in `<Form>`?**

- ✅ You need a real `<form>` element with `onSubmit` — wrap once at the root.
- ✅ Every section should share the same `density` / `variant` — set it once on `<Form>`.
- ❌ The body is a mix of form sections and non-form UI (status cards, badges, dividers) — DON'T wrap. Use `Form.Section` directly with its own `density` prop, and let the outer container handle inter-section spacing.

```tsx
// ✅ Mixed body — no Form wrapper, each Section sets its own density
<Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
  <Form.Row ratio={[1, 2]}>...</Form.Row>           {/* uses default density */}

  <Divider />
  <StatusCard />                                    {/* non-form UI between sections */}

  <Form.Section title='Scope' density='compact'>    {/* per-section density */}
    <Form.Row ratio={[1, 1, 1]}>...</Form.Row>
  </Form.Section>

  <Form.Section title='Advanced' density='compact'>
    <Form.Field label='...'>...</Form.Field>
  </Form.Section>
</Box>

// ✅ Real form submission — wrap once at the root
<Form onSubmit={handleSubmit} density='comfortable'>
  <Form.Section title='Profile'>...</Form.Section>
  <Form.Section title='Notifications'>...</Form.Section>
  <Form.Actions align='right'>...</Form.Actions>
</Form>
```

**Variant A — `stacked` (default): label above the control, full-width.**
Use for modal forms, create flows, onboarding, mobile, and any primary form.

```tsx
import { Form } from '@components1/ds/Form';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { Modal } from '@components1/ds/Modal';

<Modal
  open={open}
  handleClose={onClose}
  title='Create Ticket'
  width='md'
  confirmText='Create'
  onConfirm={handleSubmit}
>
  <Form variant='stacked' density='default'>
    <Form.Field label='Title' required>
      <Input value={title} onChange={...} />
    </Form.Field>

    <Form.Row ratio={[1, 1]}>
      <Form.Field label='Project'><Select ... /></Form.Field>
      <Form.Field label='Priority'><Select ... /></Form.Field>
    </Form.Row>

    <Form.Field
      label='Description'
      description='Markdown supported.'
      helperText='Will appear in the ticket body.'
    >
      <Input multiline rows={4} ... />
    </Form.Field>
  </Form>
</Modal>
```

**Variant B — `split`: label + description on the left (35%), control on the right (65%).**
Use for settings pages, configuration screens, dense admin surfaces. The GitHub / Figma settings
pattern.

```tsx
<Card variant='outlined'>
  <Card.Body>
    <Form variant='split' density='default'>
      <Form.Section title='Profile' description='Public account information.'>
        <Form.Field label='Display name' description='How others see you across the app.' required>
          <Input ... />
        </Form.Field>
        <Form.Field label='Email digests' description='Receive a daily summary of activity.'>
          <Switch ... />
        </Form.Field>
      </Form.Section>

      <Form.Section title='Notifications' divider>
        <Form.Field label='Slack channel' description='Where workflow alerts are routed.'>
          <Select ... />
        </Form.Field>
      </Form.Section>
    </Form>
  </Card.Body>
  <Card.Footer>
    <Form.Actions align='right'>
      <Button tone='secondary'>Cancel</Button>
      <Button onClick={handleSave}>Save</Button>
    </Form.Actions>
  </Card.Footer>
</Card>
```

**`Form.Row` — related fields side-by-side.** Default is equal columns; `ratio` picks weight.
Collapses to a single column under 600px viewports.

```tsx
<Form.Row ratio={[1, 1]}>                            {/* 50 / 50 */}
  <Form.Field label='First name'><Input ... /></Form.Field>
  <Form.Field label='Last name'><Input ... /></Form.Field>
</Form.Row>

<Form.Row ratio={[2, 1, 1]}>                         {/* 50 / 25 / 25 */}
  <Form.Field label='Street'><Input ... /></Form.Field>
  <Form.Field label='State'><Select ... /></Form.Field>
  <Form.Field label='ZIP'><Input ... /></Form.Field>
</Form.Row>
```

**Density — same layout, different rhythm.**

| Density       | Field gap | Section gap | Row gap | Use when                               |
| ------------- | --------- | ----------- | ------- | -------------------------------------- |
| `comfortable` | 24px      | 48px        | 16px    | Primary onboarding flows, hero forms   |
| `default`     | 16px      | 32px        | 12px    | Most forms (the default)               |
| `compact`     | 12px      | 24px        | 8px     | Settings tables, admin pages, sidebars |

**Canonical labeling pattern — `Form.Field` owns the label:**

Inside `Form.Field`, the wrapped control must **not** set its own `label` prop. `Form.Field` is the single source of truth for label rendering (placement per variant, required asterisk, description, helperText / error). DS controls like `Input` and `Select` accept a `label` prop for standalone use, but **inside `Form.Field` it must be omitted** — otherwise you get two labels stacked with different visual specs.

```tsx
// ✅ Right — one label, owned by Form.Field
<Form.Field label='Email' required>
  <Input value={email} onChange={setEmail} />
</Form.Field>

// ❌ Wrong — two labels, different fonts/colors/sizes
<Form.Field label='Email' required>
  <Input label='Email' value={email} onChange={setEmail} />
</Form.Field>

// ✅ Also fine — bare control outside Form.Field uses its own label
<Form.Row>
  <Input label='Standalone' value={x} onChange={setX} />
</Form.Row>
```

A dev-mode `console.warn` fires when a child of `Form.Field` has its own `label` prop — surfaces double-label issues in PR review without breaking the build.

**Don'ts:**

- **Don't set `label` on a control wrapped in `Form.Field`** — Form.Field owns labels. Move the label string to `Form.Field label='…'` and remove it from the inner Input/Select.
- **Don't use `FilterDropdown` / `FilterDropdownButton` inside a Form** — those are toolbar-filter affordances (pill trigger, clear-X, blue-when-active). Inside a Form, value pickers must be `ds/Select` (single or multi via `multiple` prop) or `ds/Autocomplete` (async / free-typing). The field chrome on `ds/Select` matches `ds/Input` so Select rows align with Input rows in a form column. See §1.6 for the full "form field vs toolbar filter" rule.
- Don't use `Form` for non-form layouts (dashboards, listings) — use `Stack` / `Grid` / `ListingLayout` directly.
- Don't expose 3+ column layouts at the `Form` level. Use `Form.Row` for related field pairs/triples; if a layout needs a true grid, it isn't a form.
- Don't pair `Form.Actions` with a Modal's `confirmText` or `actionButtons` — the Modal owns the footer when used as a container. `Form.Actions` is for Card / page / Inspector contexts.
- Don't set `fontFamily` on the control inside a `Form.Field` — let the body default (Roboto via MUI) inherit. The label uses Poppins (`--ds-font-display`) automatically.
- Don't add manual `mb`/`mt` on `Form.Field` children to "tweak" spacing — change `density` or use `Form.Section` to group, instead of overriding tokens per-field.
- Don't put a `Form.Section` divider above the **first** section — section spacing alone is the visual separation. Use `divider` only between major groups.

### 2.4 Other recipes — to be filled in

Same shape as §2.1 (anatomy → components → skeleton → don'ts). Add each as the pattern is first
built in a real redesign:

| Recipe                   | Likely components                                                                              |
| ------------------------ | ---------------------------------------------------------------------------------------------- |
| Dashboard summary row    | `Stat` / `CostCallout` / `Trend` as siblings above a `ListingLayout`                           |
| Detail / inspector panel | `Inspector` + `Stat` + `Label` + `Table`                                                       |
| Filter bar               | `FilterGroup` / `FilterDropdown` + `<Input leadingIcon={<SearchIcon/>} />` + `DateRangePicker` |
| Empty & loading states   | `Skeleton` (loading) · `EmptyState` (no data) · `ErrorBoundary` (error)                        |
| Tabbed page              | `PageTabs` (route-aware) wrapping per-tab content                                              |
| AI / agentic surface     | `DiffCard` + `SourceCitation` + `StreamingIndicator` + `ConfidenceIndicator`                   |

---

## 3. Decision rules — overlapping components

When two components could fit, this is which to pick.

| Situation                                                                                          | Use                                                                                   | Not                                                                                               |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| Simple table — plain columns, sorting                                                              | `ds/Table`                                                                            | `CustomTable2` (overkill)                                                                         |
| Complex table — grouped headers, expandable rows, resizable/sticky columns, pivot, column selector | `CustomTable2`                                                                        | `ds/Table` (lacks these — covers ~30% of the surface)                                             |
| One collapsible unit                                                                               | `CollapsableCard`                                                                     | `Accordion` ("Don't use Accordion for < 3 rows")                                                  |
| 3+ sibling collapsibles                                                                            | `Accordion`                                                                           | stacking `CollapsableCard`s                                                                       |
| In-page tab switch (state only) — modals, panels, in-component toggles                             | `@common-new/CustomTabs` with `behavior='filter'`                                     | `ds/Tabs` (not the default in this project)                                                       |
| Tabs that drive the URL / route                                                                    | `@common-new/CustomTabs` with `behavior='router'`                                     | `ds/PageTabs` (page-level only) · `ds/Tabs` (not default here)                                    |
| Top-of-page **2-level** nav — parent tabs with hover-dropdown subtabs, URL-hash driven             | `common/AnchorComponent` (wraps `@common-new/CustomTabs` for the subtab row)          | `ds/PageTabs` (single-level only) · `ds/Tabs` (state-only, no dropdown)                           |
| Transient confirmation message                                                                     | `Toast`                                                                               | `Banner`                                                                                          |
| Persistent inline page-level message                                                               | `Banner`                                                                              | `Toast`                                                                                           |
| Small overlay anchored to an element                                                               | `Popover` (rich) · `Tooltip` (text)                                                   | `Modal`                                                                                           |
| Centered blocking task                                                                             | `Modal` / `Dialog`                                                                    | `Inspector`                                                                                       |
| Side-drawer detail view                                                                            | `Inspector`                                                                           | `Modal`                                                                                           |
| Pick one value in a **form**                                                                       | `Select`                                                                              | `DropdownMenu` (action menu) · `FilterDropdown` (toolbar pill)                                    |
| Pick **multiple** values in a form                                                                 | `<Select multiple>`                                                                   | `MultiSelect` (deprecated — re-exports Select)                                                    |
| Pick value(s) for a **toolbar filter**                                                             | `FilterDropdown`                                                                      | `Select` (wrong context — full-width field chrome, no clear-X, no active color)                   |
| Trigger an action from a menu                                                                      | `DropdownMenu`                                                                        | `Select`                                                                                          |
| Single-line text input (any kind — text/email/password/url/textarea/number)                        | `Input`                                                                               | `TextField` (deleted) · MUI `<TextField>` directly                                                |
| Search-style input with magnifier icon                                                             | `<Input leadingIcon={<SearchIcon/>} />`                                               | `SearchInput` (deleted)                                                                           |
| Generic value picker with cluster / cloud-account chrome                                           | `common/CustomDropdown`                                                               | `ds/Select` (doesn't model `groupByCloudProvider` / status indicators / auto-direction popup)     |
| Content-shaped loading placeholder                                                                 | `Skeleton`                                                                            | `ProgressLinear`                                                                                  |
| Determinate progress (a measured %)                                                                | `ProgressLinear` / `ProgressBar`                                                      | `Skeleton`                                                                                        |
| New DS-clean button                                                                                | `ds/Button`                                                                           | `common/NewCustomButton` (legacy, app-styled — co-exists, don't introduce new uses)               |
| Situation                                                                                          | Use                                                                                   | Not                                                                                               |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------  | -----------------------------------------------------------------------------------               |
| Read-only status pill in a table cell (`Active` / `Failed` / `Pending`) — 5 status tones, no click | `ds/Label` (or `common/CustomLabels` for legacy call sites with auto-tone-from-text)  | `ds/Chip` (Chip is for interactive / categorical use)                                             |
| Interactive or categorical pill — filter, dismissible tag, count, categorical hue, avatar          | `ds/Chip`                                                                             | `ds/Label` (Label is read-only Status-axis only)                                                  |
| Simple table — plain columns, sorting                                                              | `ds/Table`                                                                            | `CustomTable2` (overkill)                                                                         |
| Complex table — grouped headers, expandable rows, resizable/sticky columns, pivot, column selector | `CustomTable2`                                                                        | `ds/Table` (lacks these — covers ~30% of the surface)                                             |
| One collapsible unit                                                                               | `CollapsableCard`                                                                     | `Accordion` ("Don't use Accordion for < 3 rows")                                                  |
| 3+ sibling collapsibles                                                                            | `Accordion`                                                                           | stacking `CollapsableCard`s                                                                       |
| In-page tab switch (state only)                                                                    | `@common-new/CustomTabs` with `behavior='filter'`                                     | `ds/Tabs` (not the default in this project)                                                       |
| Tabs that drive the URL / route                                                                    | `@common-new/CustomTabs` with `behavior='router'`                                     | `ds/PageTabs` (page-level only)                                                                   |
| Transient confirmation message                                                                     | `Toast`                                                                               | `Banner`                                                                                          |
| Persistent inline page-level message                                                               | `Banner`                                                                              | `Toast`                                                                                           |
| Small overlay anchored to an element                                                               | `Popover` (rich) · `Tooltip` (text)                                                   | `Modal`                                                                                           |
| Centered blocking task                                                                             | `Modal` / `Dialog`                                                                    | `Inspector`                                                                                       |
| Side-drawer detail view                                                                            | `Inspector`                                                                           | `Modal`                                                                                           |
| Pick one value                                                                                     | `Select`                                                                              | `DropdownMenu` (that's an _action_ menu, not a value picker)                                      |
| Trigger an action from a menu                                                                      | `DropdownMenu`                                                                        | `Select`                                                                                          |
| Content-shaped loading placeholder                                                                 | `Skeleton`                                                                            | `ProgressLinear`                                                                                  |
| Determinate progress (a measured %)                                                                | `ProgressLinear` / `ProgressBar`                                                      | `Skeleton`                                                                                        |
| New DS-clean button                                                                                | `ds/Button`                                                                           | `common/NewCustomButton` (legacy, app-styled — co-exists, don't introduce new uses)               |
| Body of a form-shaped UI — fields stacked or laid out in sections                                  | `ds/Form` (with `.Section` / `.Field` / `.Row`)                                       | Raw `Stack` / `Grid` / `Box` (no label/field semantics; spacing scatters across call sites)       |
| Labeling a control inside `Form.Field`                                                             | `<Form.Field label='X'>` owns the label                                               | Inner `<Input label='X'/>` / `<Select label='X'/>` (double label — dev warning fires)             |
| Value picker inside a Form (single or multi)                                                       | `ds/Select` (or `ds/Autocomplete` for async / free-typing)                            | `ds/FilterDropdown` / `common/FilterDropdownButton` (toolbar affordance — wrong chrome in a form) |
| Form with label above the control (modal, create flow, onboarding)                                 | `<Form variant='stacked'>`                                                            | `<Form variant='split'>` (settings-style; wrong context for modals)                               |
| Form with label on the left + control on the right (settings, configuration)                       | `<Form variant='split'>`                                                              | `<Form variant='stacked'>` (wastes horizontal space on settings pages)                            |
| Two related fields side-by-side (first/last name, street/state/zip, date range)                    | `<Form.Row ratio={...}>`                                                              | Raw `<Grid container>` (loses field-level a11y wiring + breakpoint collapse)                      |
| Multi-step wizard form                                                                             | `ds/Form` inside a page or `Inspector`, paired with `ds/Stepper`                      | Building a wizard-specific primitive — `Form` + `Stepper` covers it                               |
| 3+ columns of unrelated fields in a "form"                                                         | Re-think — likely a dashboard config, not a form. Use `Card` grid + per-card `Form`s. | `<Form.Row ratio={[1,1,1,1]}>` (forms aren't dense grids — users miss fields)                     |
| Plain content card on any new screen                                                               | `ds/Card`                                                                             | `ds/WidgetCard` / `ds/CustomBorderCard` (legacy; consolidated into `Card`)                        |
| Card needs a coloured left-edge for tone (info/success/warning/danger)                             | `ds/Card` with `variant="accent"` + `tone`                                            | A hand-rolled `borderLeft` on a `WidgetCard`                                                      |
| Subtle bg grouping inside a modal/Card (nested form panel, callout container, light-coloured fill) | `ds/Card` with `variant="tinted"` + `tone` (neutral→gray-100 ... danger→red-100)      | Hand-rolled `<Box sx={{ backgroundColor }}>` (loses token consistency + border colour pairing)    |
| `ds/Select` opens at a detached offset inside a Modal                                              | Pass `disablePortal={false}` on the Select                                            | Leaving the default `disablePortal={true}` (Modal's `transform` breaks anchor positioning)        |
| Tinted full-bleed footer in a Modal (status text + buttons on coloured bg)                         | Modal `actionButtonsFullBleed={true}` + inner Box with `boxSizing: 'border-box'`      | Negative-margin hacks (`m: -1` + `calc(100% + 16px)`) — the math breaks with `display: inline`    |
| Card needs disclosure (open / closed body)                                                         | `ds/CollapsableCard`                                                                  | `ds/Card` with manual state                                                                       |
| Clickable card row (picker, drillable surface)                                                     | `ds/Card` with `interactive` + `onClick`                                              | A raw `<Box onClick>` around card content (loses focus ring + a11y role)                          |
| 2-4 narrow choices, all visible, switching a view (not a form value)                               | `ds/Toggle`                                                                           | `ds/Tabs` (Tabs are heavier and full-width) · `ds/Select` (dropdown hides choices)                |
| Picking a value to submit in a form                                                                | `ds/Select`                                                                           | `ds/Toggle` ("Don't use Toggle as a form-value picker" — per its JSDoc)                           |
| More than 4 narrow choices                                                                         | `ds/Select` / `ds/Tabs`                                                               | `ds/Toggle` (row gets too crowded — per its JSDoc "Don't")                                        |
| Segmented multi/single-select form input                                                           | `ds/ToggleGroup`                                                                      | `ds/Toggle` (different primitive — ToggleGroup is the form-input cousin)                          |
| Single date + time field in a form                                                                 | `common/CustomDateTimePicker`                                                         | `ds/DateRangePicker` (that's for date ranges, not a single datetime point)                        |
| Date range field in a form                                                                         | `ds/DateRangePicker`                                                                  | `common/CustomDateTimePicker` (single datetime only)                                              |
| "Ticket - {id}" inline link pattern in a table cell or header                                      | `common/CustomTicketLink`                                                             | Duplicating the prefix-label + conditional-link logic inline at every call site                   |
| Link that navigates away to an external URL                                                        | `<Link href={url} openInNew>`                                                         | A raw `<a>` tag (loses DS color, font-size token, and external-icon)                              |
| Long link text that must fit a constrained cell width                                              | `<Link href={url} maxWidth='120px'>`                                                  | Manual `overflow: hidden` + `textOverflow: 'ellipsis'` on a raw anchor (loses the hover tooltip)  |
| Download action button                                                                             | `common/DownloadButton`                                                               | A raw `ds/Button` + manual `saveAs` — `DownloadButton` already wires the blob/canvas/CSV logic    |

---

## 4. Maintenance

- This doc tracks **patterns**, not props — never copy a prop table here; link the component file.
- Add a recipe to §2 the first time a multi-component view is built in a redesign PR.
- Add a row to §3 whenever a redesign surfaces a "which one do I use?" question.
- Each `ds/*` file should keep an "Anatomy" / "Don't" JSDoc block (like `ListingLayout`) — that
  block is the per-component source of truth this guide points at.

---

_End of guide._
