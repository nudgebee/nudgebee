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

| Component                         | Where   | Use when                                                                                                                                                                                   |
| --------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `ListingLayout`                   | ds/     | The card shell for any table/listing screen — toolbar + body + footer slots                                                                                                                |
| `Card`                            | ds/     | **Canonical content card** — `variant` (elevated/outlined/accent) × `size` (sm/md/lg) × `elevation` (raised/flat). Slots: `header` / `footer` / `children`. Use for all new card surfaces. |
| `WidgetCard` · `CustomBorderCard` | ds/     | Legacy plain content cards — consolidated into `Card`. Co-exist; **don't introduce new uses.**                                                                                             |
| `CollapsableCard`                 | ds/     | A single collapsible card (one unit — _not_ an accordion). Composes `Card` for the surface.                                                                                                |
| `Divider` · `List`                | ds/     | Rules and simple item lists                                                                                                                                                                |
| `BoxLayout2`                      | common/ | Legacy filter-bar + content shell — prefer `ListingLayout` for new work                                                                                                                    |

### Tables & data display

| Component                                                                                    | Where   | Use when                                                                                                                      |
| -------------------------------------------------------------------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `Table` · `TableCell`                                                                        | ds/     | A **simple** table — plain columns, sorting, no grouped headers                                                               |
| `CustomTable2`                                                                               | common/ | A **complex** table — grouped headers, expandable rows, resizable/sticky columns, pivot, column selector, built-in pagination |
| `Pagination`                                                                                 | ds/     | Pager for a `ds/Table` (CustomTable2 paginates itself)                                                                        |
| `Stat` · `Trend` · `CostCallout` · `Comparison`                                              | ds/     | Metric / KPI / cost / before-after display                                                                                    |
| `Label` · `Chip` · `NBStatusBadge` · `StatusIndicator` · `SeverityIcon` · `IntegrationBadge` | ds/     | Tags, status pills, severity and integration markers                                                                          |

### Content & formatting

| Component                                             | Where | Use when                          |
| ----------------------------------------------------- | ----- | --------------------------------- |
| `Format` (Currency/Number/Memory/Datetime/Text)       | ds/   | Render a typed value consistently |
| `Markdown` · `DiffViewer` · `ConsoleOutput` · `Chart` | ds/   | Rich content blocks               |

### Forms & inputs

| Component                             | Where | Use when                                                                                                                                                                                                        |
| ------------------------------------- | ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Input`                               | ds/   | All text entry — single line, textarea, password, email, URL. Supports `prefix`/`suffix`/`leadingIcon`/`trailingIcon`. Replaces the deleted `TextField` + `SearchInput` stubs and the legacy `CustomTextField`. |
| `Checkbox` · `Switch` · `ToggleGroup` | ds/   | Boolean / segmented controls                                                                                                                                                                                    |
| `Select`                              | ds/   | Value picker for a **form field** — single by default, multi via `multiple` prop. Built-in search auto-shows at >8 options. Field-shaped trigger that matches `Input` chrome.                                   |
| `Autocomplete`                        | ds/   | Searchable, async, free-typing value picker                                                                                                                                                                     |
| `DateRangePicker`                     | ds/   | Date and date-range input                                                                                                                                                                                       |
| `FilterDropdown`                      | ds/   | Value picker for a **toolbar / filter bar** — inline pill trigger with clear-X. See §3 for "form vs filter" rule.                                                                                               |

**Deprecated / removed in the May-2026 form-primitive consolidation:**

- `ds/TextField` — **deleted**. Use `Input`.
- `ds/SearchInput` — **deleted**. Use `<Input leadingIcon={<SearchIcon />} />`.
- `ds/MultiSelect` — **deprecated re-export**. Use `<Select multiple value={[…]} onChange={…} />`.
- `common/CustomTextField` (legacy `components1/common/`) — still in use; migrate to `Input` opportunistically.
- `common/CustomDropdown` — **stays** as the cluster / cloud-account picker domain composition. Don't use for new generic dropdowns.

### Navigation & filtering

| Component                        | Where | Use when                                                                                                                                                   |
| -------------------------------- | ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Tabs`                           | ds/   | In-page tab switching (state-only)                                                                                                                         |
| `PageTabs`                       | ds/   | Route-aware top-of-page tabs                                                                                                                               |
| `Toggle`                         | ds/   | Compact button-row switcher — 2-4 narrow choices visible at once (e.g. "Yours" / "Team"). State-only, not a form input. Sizes: `default` / `large` / `sm`. |
| `Stepper`                        | ds/   | Multi-step progress indicator                                                                                                                              |
| `Link`                           | ds/   | Navigation link                                                                                                                                            |
| `FilterGroup` · `FilterDropdown` | ds/   | Filter controls for a listing toolbar                                                                                                                      |
| `AutoRefresh`                    | ds/   | The auto-refresh interval control                                                                                                                          |

### Actions, feedback, overlays

| Component                                                                      | Where   | Use when                                                                                                                                           |
| ------------------------------------------------------------------------------ | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Button` · `DropdownMenu`                                                      | ds/     | Buttons and action menus                                                                                                                           |
| `CopyButton`                                                                   | common/ | Icon-only button that copies `text` to clipboard, shows check-icon feedback, and optionally toasts — props: `text`, `size`, `tone`, `toastMessage` |
| `Banner` · `Toast`                                                             | ds/     | Inline page banners / transient notifications                                                                                                      |
| `EmptyState` · `Skeleton` · `ProgressBar` · `ProgressLinear` · `ErrorBoundary` | ds/     | Empty / loading / progress / error states                                                                                                          |
| `Modal` · `Dialog` · `Popover` · `Tooltip` · `Inspector`                       | ds/     | Overlays (see §3 for which is which)                                                                                                               |
| `DiffCard` · `SourceCitation` · `StreamingIndicator` · `ConfidenceIndicator`   | ds/     | AI / agentic surfaces                                                                                                                              |

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

### 2.2 Other recipes — to be filled in

Same shape as §2.1 (anatomy → components → skeleton → don'ts). Add each as the pattern is first
built in a real redesign:

| Recipe                   | Likely components                                                                              |
| ------------------------ | ---------------------------------------------------------------------------------------------- |
| Dashboard summary row    | `Stat` / `CostCallout` / `Trend` as siblings above a `ListingLayout`                           |
| Detail / inspector panel | `Inspector` + `Stat` + `Label` + `Table`                                                       |
| Filter bar               | `FilterGroup` / `FilterDropdown` + `<Input leadingIcon={<SearchIcon/>} />` + `DateRangePicker` |
| Form layout              | `WidgetCard` + `Input` / `Select` / `Switch` + `Button`                                        |
| Empty & loading states   | `Skeleton` (loading) · `EmptyState` (no data) · `ErrorBoundary` (error)                        |
| Tabbed page              | `PageTabs` (route-aware) wrapping per-tab content                                              |
| AI / agentic surface     | `DiffCard` + `SourceCitation` + `StreamingIndicator` + `ConfidenceIndicator`                   |

---

## 3. Decision rules — overlapping components

When two components could fit, this is which to pick.

| Situation                                                                                          | Use                                                                                  | Not                                                                                           |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------- |
| Simple table — plain columns, sorting                                                              | `ds/Table`                                                                           | `CustomTable2` (overkill)                                                                     |
| Complex table — grouped headers, expandable rows, resizable/sticky columns, pivot, column selector | `CustomTable2`                                                                       | `ds/Table` (lacks these — covers ~30% of the surface)                                         |
| One collapsible unit                                                                               | `CollapsableCard`                                                                    | `Accordion` ("Don't use Accordion for < 3 rows")                                              |
| 3+ sibling collapsibles                                                                            | `Accordion`                                                                          | stacking `CollapsableCard`s                                                                   |
| In-page tab switch (state only)                                                                    | `ds/Tabs`                                                                            | `PageTabs`                                                                                    |
| Tabs that drive the URL / route                                                                    | `ds/PageTabs`                                                                        | `ds/Tabs`                                                                                     |
| Transient confirmation message                                                                     | `Toast`                                                                              | `Banner`                                                                                      |
| Persistent inline page-level message                                                               | `Banner`                                                                             | `Toast`                                                                                       |
| Small overlay anchored to an element                                                               | `Popover` (rich) · `Tooltip` (text)                                                  | `Modal`                                                                                       |
| Centered blocking task                                                                             | `Modal` / `Dialog`                                                                   | `Inspector`                                                                                   |
| Side-drawer detail view                                                                            | `Inspector`                                                                          | `Modal`                                                                                       |
| Pick one value in a **form**                                                                       | `Select`                                                                             | `DropdownMenu` (action menu) · `FilterDropdown` (toolbar pill)                                |
| Pick **multiple** values in a form                                                                 | `<Select multiple>`                                                                  | `MultiSelect` (deprecated — re-exports Select)                                                |
| Pick value(s) for a **toolbar filter**                                                             | `FilterDropdown`                                                                     | `Select` (wrong context — full-width field chrome, no clear-X, no active color)               |
| Trigger an action from a menu                                                                      | `DropdownMenu`                                                                       | `Select`                                                                                      |
| Single-line text input (any kind — text/email/password/url/textarea/number)                        | `Input`                                                                              | `TextField` (deleted) · MUI `<TextField>` directly                                            |
| Search-style input with magnifier icon                                                             | `<Input leadingIcon={<SearchIcon/>} />`                                              | `SearchInput` (deleted)                                                                       |
| Generic value picker with cluster / cloud-account chrome                                           | `common/CustomDropdown`                                                              | `ds/Select` (doesn't model `groupByCloudProvider` / status indicators / auto-direction popup) |
| Content-shaped loading placeholder                                                                 | `Skeleton`                                                                           | `ProgressLinear`                                                                              |
| Determinate progress (a measured %)                                                                | `ProgressLinear` / `ProgressBar`                                                     | `Skeleton`                                                                                    |
| New DS-clean button                                                                                | `ds/Button`                                                                          | `common/NewCustomButton` (legacy, app-styled — co-exists, don't introduce new uses)           |
| Situation                                                                                          | Use                                                                                  | Not                                                                                           |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ | -----------------------------------------------------------------------------------           |
| Read-only status pill in a table cell (`Active` / `Failed` / `Pending`) — 5 status tones, no click | `ds/Label` (or `common/CustomLabels` for legacy call sites with auto-tone-from-text) | `ds/Chip` (Chip is for interactive / categorical use)                                         |
| Interactive or categorical pill — filter, dismissible tag, count, categorical hue, avatar          | `ds/Chip`                                                                            | `ds/Label` (Label is read-only Status-axis only)                                              |
| Simple table — plain columns, sorting                                                              | `ds/Table`                                                                           | `CustomTable2` (overkill)                                                                     |
| Complex table — grouped headers, expandable rows, resizable/sticky columns, pivot, column selector | `CustomTable2`                                                                       | `ds/Table` (lacks these — covers ~30% of the surface)                                         |
| One collapsible unit                                                                               | `CollapsableCard`                                                                    | `Accordion` ("Don't use Accordion for < 3 rows")                                              |
| 3+ sibling collapsibles                                                                            | `Accordion`                                                                          | stacking `CollapsableCard`s                                                                   |
| In-page tab switch (state only)                                                                    | `ds/Tabs`                                                                            | `PageTabs`                                                                                    |
| Tabs that drive the URL / route                                                                    | `ds/PageTabs`                                                                        | `ds/Tabs`                                                                                     |
| Transient confirmation message                                                                     | `Toast`                                                                              | `Banner`                                                                                      |
| Persistent inline page-level message                                                               | `Banner`                                                                             | `Toast`                                                                                       |
| Small overlay anchored to an element                                                               | `Popover` (rich) · `Tooltip` (text)                                                  | `Modal`                                                                                       |
| Centered blocking task                                                                             | `Modal` / `Dialog`                                                                   | `Inspector`                                                                                   |
| Side-drawer detail view                                                                            | `Inspector`                                                                          | `Modal`                                                                                       |
| Pick one value                                                                                     | `Select`                                                                             | `DropdownMenu` (that's an _action_ menu, not a value picker)                                  |
| Trigger an action from a menu                                                                      | `DropdownMenu`                                                                       | `Select`                                                                                      |
| Content-shaped loading placeholder                                                                 | `Skeleton`                                                                           | `ProgressLinear`                                                                              |
| Determinate progress (a measured %)                                                                | `ProgressLinear` / `ProgressBar`                                                     | `Skeleton`                                                                                    |
| New DS-clean button                                                                                | `ds/Button`                                                                          | `common/NewCustomButton` (legacy, app-styled — co-exists, don't introduce new uses)           |
| Plain content card on any new screen                                                               | `ds/Card`                                                                            | `ds/WidgetCard` / `ds/CustomBorderCard` (legacy; consolidated into `Card`)                    |
| Card needs a coloured left-edge for tone (info/success/warning/danger)                             | `ds/Card` with `variant="accent"` + `tone`                                           | A hand-rolled `borderLeft` on a `WidgetCard`                                                  |
| Card needs disclosure (open / closed body)                                                         | `ds/CollapsableCard`                                                                 | `ds/Card` with manual state                                                                   |
| Clickable card row (picker, drillable surface)                                                     | `ds/Card` with `interactive` + `onClick`                                             | A raw `<Box onClick>` around card content (loses focus ring + a11y role)                      |
| 2-4 narrow choices, all visible, switching a view (not a form value)                               | `ds/Toggle`                                                                          | `ds/Tabs` (Tabs are heavier and full-width) · `ds/Select` (dropdown hides choices)            |
| Picking a value to submit in a form                                                                | `ds/Select`                                                                          | `ds/Toggle` ("Don't use Toggle as a form-value picker" — per its JSDoc)                       |
| More than 4 narrow choices                                                                         | `ds/Select` / `ds/Tabs`                                                              | `ds/Toggle` (row gets too crowded — per its JSDoc "Don't")                                    |
| Segmented multi/single-select form input                                                           | `ds/ToggleGroup`                                                                     | `ds/Toggle` (different primitive — ToggleGroup is the form-input cousin)                      |

---

## 4. Maintenance

- This doc tracks **patterns**, not props — never copy a prop table here; link the component file.
- Add a recipe to §2 the first time a multi-component view is built in a redesign PR.
- Add a row to §3 whenever a redesign surfaces a "which one do I use?" question.
- Each `ds/*` file should keep an "Anatomy" / "Don't" JSDoc block (like `ListingLayout`) — that
  block is the per-component source of truth this guide points at.

---

_End of guide._
