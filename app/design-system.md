# Component Usage & Composition Guide

**Audience:** developers and AI agents building or redesigning pages with the `components/` library.
**What this doc is:** two layers — (1) the **"when & how"** layer (which component to reach for in a
given UI situation, and how components compose into complete views), and (2) a **per-component API
reference** for every design-system primitive in `src/components/common/ds/` (§4).
**Source of truth for props:** §4 below is harvested from each component's JSDoc + `Props` interface.
Deeper detail (anatomy diagrams, "Don't" rules, interaction states) lives in each file's JSDoc header
(e.g. [`ds/ListingLayout.tsx`](src/components/common/ds/ListingLayout.tsx)) and in the per-primitive
pages of the design-system viewer (`app/design-system/primitives/**`).

---

## 0. Why this doc exists, and how it's kept current

The design-system viewer and `manifest.json` already answer **"what components exist."** They do
not answer **"which ones do I use for a table view, and how do they fit together."** That gap is
what §1–§3 of this doc fills; §4 adds the per-component prop/variant reference so an agent can pick
the right component **and** call it correctly without opening every file.

**Where components live:**

- `@ui/*` → `src/components/common/ds/*` — the **design-system primitives** (the in-scope set; §4
  documents all 45).
- `@shared/*` → `src/components/common/*` — **domain compositions** built from primitives
  (`CustomTable`, `MarkDowns`, `Form`, `CustomDropdown`, …). Referenced here where a decision rule
  needs them; not part of the §4 primitive reference.

**Keeping it in sync:** when you change a `ds/*` primitive's public props, variants, or "Don't"
rules, update its entry in §4 **and** its `app/design-system/primitives/**` spec in the same commit
(see [`app/CLAUDE.md`](CLAUDE.md) → "Keeping the spec in sync"). When a new recurring composition
appears, add a recipe (§2). When two components overlap, add a decision rule (§3).

---

## 1. Component index — by purpose

`ds/` = design-system primitive (`@ui/*`). `common/` = domain composition (`@shared/*`) — an
app-specific component built from primitives.

### Layout & page shell

| Component                         | Where | Use when                                                                                                                                                                                                                                                                                                                                                         |
| --------------------------------- | ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ListingLayout`                   | ds/   | The card shell for any table/listing screen — `Toolbar` + `Body` + `Footer` slots.                                                                                                                                                                                                                                                                               |
| `Card`                            | ds/   | **Canonical content card** — `variant` (elevated/outlined/accent/**tinted**) × `size` (sm/md/lg) × `elevation` (raised/flat). Slots: `header` / `footer` / `children`. Use for all new card surfaces. **`variant='tinted'` + `tone`** gives a coloured-background panel (neutral→gray-100, info→blue-100, success→green-100, warning→amber-100, danger→red-100). |
| `WidgetCard` · `CustomBorderCard` | ds/   | Legacy plain content cards — consolidated into `Card`. Co-exist; **don't introduce new uses.**                                                                                                                                                                                                                                                                   |
| `CollapsableCard`                 | ds/   | A single collapsible card (one unit — _not_ an accordion). Composes `Card` for the surface.                                                                                                                                                                                                                                                                      |
| `Accordion`                       | ds/   | 3+ sibling collapsibles as one group. For a single collapsible unit use `CollapsableCard`.                                                                                                                                                                                                                                                                       |
| `Divider` · `List`                | ds/   | Rules and simple item lists.                                                                                                                                                                                                                                                                                                                                     |

### Tables & data display

| Component                                             | Where   | Use when                                                                                                                                                                                                                                                     |
| ----------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `CustomTable`                                         | common/ | **The table** for this app — `@shared/tables/CustomTable` (grouped headers, expandable rows, resizable/sticky columns, column selector, built-in pagination via `CustomTablePagination`). There is no `ds/Table` primitive today.                            |
| `Stat` · `Trend` · `CostCallout` · `Comparison`       | ds/     | Metric / KPI / cost / before-after display.                                                                                                                                                                                                                  |
| `ProgressBar` · `ProgressLinear`                      | ds/     | Utilisation gauge (known max) vs indeterminate/determinate "something is happening" progress.                                                                                                                                                                |
| `Chart`                                               | ds/     | Line / series / time-series / bar / doughnut charts via the `Chart.*` namespace.                                                                                                                                                                             |
| `Label` · `Chip` · `StatusIndicator` · `SeverityIcon` | ds/     | Tags, status pills, resource-state read-outs, severity markers. `Chip` has 7 variants, 5 sizes (`micro`→`md`), 9 tones and 8 categorical `hue` values for tag chips — use exported `hashHue(key)` for a stable string→hue mapping. See §3 for Chip-vs-Label. |

**Deprecated — labels & status:**

- `common/CustomLabels` — **removed** (no longer in the tree). Use `ds/Label`. `CustomLabels` auto-derived tone from text content; `ds/Label` requires an explicit `tone` (`neutral`/`info`/`success`/`warning`/`critical`).
- `common/NBStatusBadge` (`@shared/widgets/NBStatusBadge`) — still in use for the K8s status-badge pattern; prefer `ds/StatusIndicator` / `ds/Label` for new code.

### Content & formatting

| Component    | Where   | Use when                                                                                                                                                                                                                                                                 |
| ------------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `Markdown`   | common/ | Prose / markdown that _contains_ fenced code. Import `@shared/viewers/MarkDowns`. (No `ds/Markdown` primitive.)                                                                                                                                                          |
| `CodeBlock`  | ds/     | **Display** a static code snippet, shell command, or config fragment with a copy button. `code` (required), `language`/`title` header, `inline` chip, `tone` (`light`/`dark`), `showLineNumbers`, `prompt`, `wrap`, `maxHeight`.                                         |
| `CodeEditor` | ds/     | **Edit** code, or display **read-only** code with syntax highlighting / line numbers / folding. CodeMirror wrapper sharing `CodeBlock`'s surface. `value` (required), `onChange`, `language`, `readOnly`, `tone`, `height`, `extensions` (escape hatch for PromQL/lint). |
| `DiffViewer` | ds/     | Show **what changed** between two versions. Engine inferred from input: `gitDiff` (unified string) → unified rows; `originalCode`+`newCode` → split side-by-side. `mode='unified'\|'split'` overrides.                                                                   |

### Forms & inputs

| Component                                        | Where   | Use when                                                                                                                                                                                                                                                   |
| ------------------------------------------------ | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Form` (+ `.Section`/`.Field`/`.Row`/`.Actions`) | common/ | **Layout primitive for any form-shaped UI** — modals, settings pages, side panels. Import `@shared/forms/Form`. Container-agnostic; controls label placement (stacked vs split), field gap, section structure, related-field rows. See §2.3.               |
| `Input`                                          | ds/     | All text entry — single line, textarea, password, email, URL, number. Supports `prefix`/`suffix`/`leadingIcon`/`trailingIcon`. Replaces the legacy `CustomTextField`.                                                                                      |
| `SearchInput`                                    | ds/     | Search-style toolbar input (Enter to search, X to clear). Thin wrapper over `ds/Input` — preserves the `onEnterPress` / `onClear` contract for its 15+ consumers.                                                                                          |
| `Checkbox` · `Switch` · `ToggleGroup`            | ds/     | Boolean / segmented controls.                                                                                                                                                                                                                              |
| `Select`                                         | ds/     | Value picker for a **form field** — single by default, multi via `multiple`. Built-in search auto-shows at >8 options. Field-shaped trigger matching `Input` chrome.                                                                                       |
| `FilterDropdown`                                 | ds/     | Value picker for a **toolbar / filter bar** — inline pill trigger with clear-X. See §1.6 for "form vs filter". An option's `icon` renders as a 16px leading `SafeIcon`. Panel width defaults to the trigger width (220px floor); `popoverWidth` overrides. |
| `FilterGroup`                                    | ds/     | A row of removable filter Chips with a leading "Filters" affordance. Composes `Chip` + `DropdownMenu`.                                                                                                                                                     |
| `CustomDateTimePicker`                           | common/ | Single date + time picker with DS-matched `Input` chrome. Import `@shared/widgets/CustomDateTimePicker`. Use for single datetime fields. (No `ds/DateRangePicker` primitive exists today.)                                                                 |

**Deprecated / removed (form-primitive consolidation):**

- `ds/TextField`, `ds/SearchInput` stubs, `ds/MultiSelect`, `ds/Autocomplete`, `ds/DateRangePicker` — **not present** in `ds/`. Use `Input` / `<Input leadingIcon>` / `<Select multiple>` / `Select` (searchable) / `common/CustomDateTimePicker`.
- `common/CustomTextField` (`@shared/forms/CustomTextField`) — still in use; migrate to `Input` opportunistically.
- `common/CustomDropdown` (`@shared/CustomDropdown`) — **stays** as the cluster / cloud-account picker domain composition. Don't use for new generic dropdowns.

### Navigation & filtering

| Component         | Where   | Use when                                                                                                                                                                                                                                                         |
| ----------------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Tabs`            | ds/     | DS tabs primitive (`@ui/Tabs`). `navigation='state'` for in-page toggles, `navigation='router'` (+ `routerMode='query'\|'hash'`) for URL-driven nav. Emits `onChange`; pages render content. **There is no `ds/PageTabs` or `CustomTabs`** — those were removed. |
| `Tabs` (shared)   | common/ | `@shared/navigation/Tabs` — the legacy tabs widget still used across most pages (and `TabsForDrilldown`). New work should prefer `ds/Tabs`.                                                                                                                      |
| `AnchorComponent` | common/ | Top-of-page **2-level** nav — parent tabs with optional hover-popover dropdowns of subtabs, hash-driven routing. Import `@shared/navigation/AnchorComponent`.                                                                                                    |
| `Toggle`          | ds/     | Compact button-row switcher — 2-4 narrow choices visible at once (e.g. "Yours" / "Team"). State-only, not a form input. Sizes: `default` / `large` / `sm`.                                                                                                       |
| `Stepper`         | ds/     | Multi-step progress indicator (vertical / horizontal).                                                                                                                                                                                                           |
| `Link`            | ds/     | Inline navigation link. `openInNew` opens in a new tab + external-link icon. `secondaryText` for dense/caption contexts. `maxWidth` truncates with a hover tooltip. Don't use for actions — use `<Button tone='link'>`.                                          |

### Actions, feedback, overlays

| Component                                                    | Where   | Use when                                                                                                                                                                                                                              |
| ------------------------------------------------------------ | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Button`                                                     | ds/     | All actions. `tone` primary/secondary/ghost/danger/link, `size` xs–lg. `trailingAccent` reserved for _the_ page CTA.                                                                                                                  |
| `DropdownMenu` · `ThreeDotsMenu`                             | ds/     | Action menus. `ThreeDotsMenu` is the kebab/overflow wrapper (`<DropdownMenu trigger={icon-only Button}>`). Per spec there is no separate `ButtonMenu` primitive — compose `DropdownMenu` with a labelled trigger.                     |
| `CopyButton` · `DownloadButton`                              | common/ | Icon-only copy-to-clipboard (`@shared/buttons/CopyButton`) and download trigger (`@shared/buttons/DownloadButton`, wraps `ds/Button` + `file-saver`). Prefer these over wiring `saveAs` manually.                                     |
| `Banner` · `Toast`                                           | ds/     | Inline page banners / transient toast notifications. **Toast is imperative** — `import { toast } from '@ui/Toast'`, mount `<Toast />` once in `_app.tsx`, then `toast.success('…')`. See the Toast note below.                        |
| `EmptyState` · `Skeleton` · `ProgressBar` · `ProgressLinear` | ds/     | Empty / loading / progress states. Error boundaries: `@shared/ErrorBoundary` (no `ds/ErrorBoundary`).                                                                                                                                 |
| `Modal`                                                      | ds/     | Centered overlay — plain shell **or** confirm/cancel decision dialog (pick the footer mode). The unified `Modal` absorbed the legacy `Dialog`/`NDialog`. There is no separate `ds/Dialog`, `ds/Popover`, or `ds/Inspector` primitive. |
| `Tooltip`                                                    | ds/     | Hover text / explainer / interactive tooltip. `variant` (`default`/`explainer`/`interactive`), `title`, `desc`, `placement`, `linkUrl`/`linkText`.                                                                                    |
| `SourceCitation` · `FeedbackVote`                            | ds/     | AI / agentic surfaces — inline source attribution and thumbs-up/down feedback.                                                                                                                                                        |

**Toast — imperative notifications (`@ui/Toast`):**

Fire transient feedback from anywhere — no JSX at the call site. `<Toast />` is mounted once in
`_app.tsx`; just call the singleton:

- **Severities:** `toast.default` (plain white card, no icon) · `toast.success` · `toast.info` · `toast.warning` · `toast.error`. Auto-dismiss timers: success 3000ms · info 4000ms · warning 5000ms · error 6000ms; **hover pauses**.
- **Options bag** — 2nd arg `{ … }`, or a bare number as a `duration` shorthand:
  - `description` — a smaller, gray sub-line under the message.
  - `action` — `{ label, onClick }` renders a `Button` before the close; clicking runs `onClick` **and then dismisses**.
  - `duration` — override the per-severity default (ms).
- Stacks **top-right**, newest on top, **max 3 visible**, fixed width. Motion respects `prefers-reduced-motion`.

```tsx
import { toast } from '@ui/Toast';

toast.success('Cluster saved.');
toast.default('Link copied.', { action: { label: 'Undo', onClick: restore } });
toast.warning('Rate-limited. Retrying in 30 s.', { duration: 5000 });
toast.error('Could not save.', { description: 'Name already in use.', action: { label: 'Retry', onClick: retry } });
```

**Don't** put the only copy of critical info in a Toast — it vanishes whether or not it's read (use
`Banner` / the notification centre).

**Deprecated / removed — overlays & misc:**

- `common/CustomTooltip` — **removed**. Use `ds/Tooltip` (identical prop API: `title`, `desc`, `variant`, `placement`, `linkUrl`, `linkText`).
- `common/CustomTicketLink`, `common/BoxLayout2`, `common/NewCustomButton`, `common/CustomTabs` — **removed**. Use, respectively: an inline `<Link>` for the "Ticket - {id}" pattern; `ds/ListingLayout` for filter-bar + content shells; `ds/Button`; `ds/Tabs` (or the still-present `@shared/navigation/Tabs`).

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
| **Where it lives**     | Inside a `<form>` (UserModal, settings panels)                       | Inside a toolbar above a table (`ListingLayout.Toolbar`)                                  |
| **Value lifecycle**    | Form state — submitted with the form                                 | URL query params / view preferences — survives reload                                     |

**Same popup chrome** — both render their option list through the shared `OverlaySurface` /
`OverlayItem` primitives, so the radius, shadow, item-row geometry, hover wash, and animation are
byte-identical. Only the **trigger** differs. Picking between them is a UX call, not a code call.

---

## 1.7 Overlay primitives — what's shared

When a new component needs a popup / menu / dropdown surface, **reach for these instead of
re-styling MUI's Menu or Popover**:

- **`OverlaySurface`** — the popover surface (10px radius, layered shadow, anchor positioning, slide-in animation). Backed by MUI Menu.
- **`OverlayItem`** — one row inside a surface. `size` (`sm`/`md`), `tone` (`default`/`danger`), `selected`, `icon`/`kbd`/`badge` slots.
- **`OverlaySection`** / **`OverlaySeparator`** — section headers and dividers.
- **`OverlayCheckbox`** — the 16×16 blue-when-checked square used in multi-select rows.
- **`OverlayScrollBox`** — max-height + styled scrollbar wrapper for the items list.
- **`OverlaySearch`** — search input row pinned at the top of a surface.
- **`OverlaySelectAll`** — "Select All" / "Clear All" row for multi-select lists.

All live in `ds/internal/Overlay.tsx`. They're **not for app code** — only consumed by other `ds/*`
components (`DropdownMenu`, `Select`, `FilterDropdown`). The visual tokens that drive them live in
`--ds-overlay-*` (see `app/src/styles/theme-tokens.css`).

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
<Box sx={{ backgroundColor: 'var(--ds-background-100)', borderRadius: 'var(--ds-radius-lg)', padding: 'var(--ds-space-3) var(--ds-space-4)' }} />
```

**Option B — `ds` object from `@utils/colors`** (typed, autocomplete-friendly — preferred in `.tsx`/`.ts`):

```tsx
import { ds } from '@utils/colors';

<Box
  sx={{
    backgroundColor: ds.background[100],
    color: ds.brand[600],
    borderRadius: ds.radius.lg,
    padding: `${ds.space[3]} ${ds.space[4]}`,
    fontSize: ds.text.body,
  }}
/>;
```

`ds.space.mul(step, multiplier)` returns a `calc()` string for multiplied spacing, e.g.
`ds.space.mul(2, 3)` → `'calc(var(--ds-space-2) * 3)'` (24px). `step` is typed `0 | 1 | … | 7`.

### Rules

- **Never hardcode** a color, spacing, radius, or font size that has a `--ds-*` token.
- **Prefer Option B** (`ds.*`) in `.tsx`/`.ts`; use raw `var(--ds-*)` in `.css` / Emotion literals.
- Do **not** use `--nb-*` legacy tokens in new DS components — reference only `--ds-*`.

---

## 2. Composition recipes

### 2.1 Recipe — Table / listing view ⭐ worked example

The standard table screen (recommendations, inventory, audit lists). Built from a **shell**
(`ListingLayout`) with primitives slotted in, plus a **table** in the body.

**Anatomy** (from [`ds/ListingLayout.tsx`](src/components/common/ds/ListingLayout.tsx) — read its JSDoc):

```
ListingLayout                     ← card chrome
├── ListingLayout.Toolbar         ← header: title + filters (left) + actions (right)
│     ├── FilterDropdown / SearchInput / Chip   (left, filter widgets)
│     ├── ListingLayout.ToolbarSpacer           (pushes the rest right)
│     └── Button / DropdownMenu                 (right, action cluster)
├── ListingLayout.Body            ← the table
│     └── CustomTable
└── ListingLayout.Footer          ← optional; omit when CustomTable paginates itself
```

**Code skeleton:**

```tsx
import { ListingLayout } from '@ui/ListingLayout';
import { Button } from '@ui/Button';
import FilterDropdownButton from '@ui/FilterDropdown';
import CustomTable from '@shared/tables/CustomTable';

<ListingLayout id="recommendations">
  <ListingLayout.Toolbar title="Recommendations" actions={<Button>Export</Button>}>
    <FilterDropdownButton ... />
    <FilterDropdownButton ... />
  </ListingLayout.Toolbar>

  <ListingLayout.Body>
    <CustomTable headers={...} tableData={...} loading={...} />
  </ListingLayout.Body>
</ListingLayout>;
```

`CustomTable` (`@shared/tables/CustomTable`) brings its **own** pagination (`CustomTablePagination`),
empty-state and tabs, so put it in `Body` and **omit `ListingLayout.Footer`**. There is no `ds/Table`
primitive today.

**Don'ts** (from `ListingLayout`'s JSDoc):

- Don't put page-level `Stat` summary cards inside `ListingLayout` — they are siblings _above_ it.
- Don't grow `ListingLayout`'s prop API — compose primitives into the slots instead.

### 2.2 Recipe — Modal dialog ⭐ worked example

The unified `Modal` ([`ds/Modal.tsx`](src/components/common/ds/Modal.tsx)) covers both shapes the
legacy `Modal` + `NDialog` used to split: a plain modal shell (form, editor, settings panel) **and**
a decision dialog (confirm / cancel). Pick the **footer mode** based on the shape of the action —
the header chrome, loader behaviour, success state, backdrop guard, and a11y are identical either way.

**Footer mode A — `confirmText` preset (the default for decisions):**

```tsx
import { Modal } from '@ui/Modal';

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

What you get for free: DS `Button` rendering (primary Confirm + secondary Cancel), 140px min-width,
right-aligned actions, `loader` auto-disables both buttons. Knobs: `confirmDisabled`,
`isConfirmRequired` / `isCancelRequired` (hide one button), `loader`, `backdropClickClose`.

**Footer mode B — `actionButtons` (freeform):**

```tsx
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import Stack from '@mui/material/Stack';

<Modal
  open={open}
  handleClose={handleClose}
  title='Create Ticket'
  width='md'
  loader={isSubmitting}
  actionButtons={
    <Stack direction='row' gap='12px' sx={{ button: { minWidth: '140px' } }}>
      <Button tone='secondary' onClick={handleCancel} disabled={isSubmitting}>Cancel</Button>
      <Button onClick={handleSubmit} disabled={isSubmitting}>Create Ticket</Button>
    </Stack>
  }
>
  <TicketFormSection ... />
</Modal>;
```

Use `actionButtons` whenever the preset can't express what you need: 3+ buttons, ghost / danger /
link tones, non-button content, split close paths, or custom layout. Render footer buttons through
`ds/Button` so the freeform footer matches the preset's chrome.

**Pick the footer:**

| Footer shape                                             | Use                                                            |
| -------------------------------------------------------- | -------------------------------------------------------------- |
| Cancel + one verb, single close path                     | `confirmText` preset                                           |
| Single "Close" button (informational modal)              | `confirmText='Close'` with `isCancelRequired={false}`          |
| Cancel needs cleanup the X / backdrop shouldn't run      | `actionButtons` (two close paths can't be expressed in preset) |
| 3+ buttons, or ghost / danger / link tones in the footer | `actionButtons`                                                |
| No footer at all (dismissible only via X)                | omit both props                                                |

**Other useful modal patterns:**

- **Loader** — `loader={isSubmitting}` renders a top progress bar, blurs the body, and (preset mode) disables both footer buttons.
- **Backdrop / Escape guard** — `backdropClickClose={false}` blocks backdrop clicks AND Escape. Default `true`.
- **Full-bleed footer** — `actionButtonsFullBleed={true}` drops `DialogActions` padding so freeform `actionButtons` can extend edge-to-edge. The inner Box needs `boxSizing: 'border-box'`.
- **`ds/Select` inside a Modal** — works by default (`disablePortal={false}` so the popup escapes the Modal Paper's transformed subtree).
- **Success state** — `onSuccess={true}` + `message='…'` + optional `icon`. `type='PASSWORD_CHANGE'` swaps to the key icon.
- **Tall content** — `maxHeight='600px'` clamps the Paper + inner content for internal scroll.
- **Right-side header slot** — `rightComponentOnTitle={<HelpLink />}`. **NDialog-parity extra panel** — `additionalComponent={<OptionsList />}` renders below the body, outside `DialogContent`.

**Don'ts:** don't pass both `actionButtons` and `confirmText` (`actionButtons` wins); render footer
buttons through `ds/Button`; one primary per footer; use `Modal` for trigger-anchored popups
(there is no `ds/Popover` — use `ds/Tooltip` for text or `DropdownMenu` for menus) or side-drawer
detail views.

### 2.3 Recipe — Form layout ⭐ worked example

`Form` ([`@shared/forms/Form`](src/components/common/forms/Form.tsx)) is the layout primitive for
**any** form-shaped UI — inside a `Modal` body, inside a `Card`, on a settings page, or standalone.
The container owns outer padding; `Form` owns internal layout (section spacing, field spacing, label
placement, row layout). _(It lives under `@shared/`, not `@ui/`, but is the canonical form layout.)_

**Anatomy:**

```
Form                                  ← OPTIONAL wrapper: only for a real <form onSubmit>
├── Form.Section                      ← labeled group: title + description + optional divider
│     ├── Form.Field                  ← label + description + control + helperText/error
│     └── Form.Row                    ← side-by-side related fields
│           ├── Form.Field
│           └── Form.Field
└── Form.Actions                      ← submit/cancel — omit when inside a Modal (Modal owns footer)
```

**Variant A — `stacked` (default):** label above the control, full-width. Modal forms, create flows.

```tsx
import { Form } from '@shared/forms/Form';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';

<Form variant='stacked' density='default'>
  <Form.Field label='Title' required>
    <Input value={title} onChange={setTitle} />
  </Form.Field>
  <Form.Row ratio={[1, 1]}>
    <Form.Field label='Project'><Select ... /></Form.Field>
    <Form.Field label='Priority'><Select ... /></Form.Field>
  </Form.Row>
</Form>;
```

**Variant B — `split`:** label + description on the left (35%), control on the right (65%). Settings
pages, configuration screens.

**Density** — same layout, different rhythm: `comfortable` (24/48/16px), `default` (16/32/12px),
`compact` (12/24/8px).

**Canonical labeling — `Form.Field` owns the label.** Inside `Form.Field`, the wrapped control must
**not** set its own `label` prop (a dev-mode `console.warn` fires when a child does). DS controls
like `Input`/`Select` accept a `label` for standalone use, but omit it inside `Form.Field`.

**Don'ts:**

- **Don't set `label` on a control wrapped in `Form.Field`** — move it to `Form.Field label='…'`.
- **Don't use `FilterDropdown` inside a Form** — those are toolbar-filter affordances. Inside a Form, use `ds/Select` (single or multi via `multiple`); for many options it auto-shows search. See §1.6.
- Don't pair `Form.Actions` with a Modal's `confirmText` / `actionButtons` — the Modal owns the footer.
- Don't expose 3+ column layouts at the `Form` level — `Form.Row` is for related pairs/triples.

### 2.4 Other recipes — to be filled in

Add each as the pattern is first built in a real redesign:

| Recipe                 | Likely components                                                                             |
| ---------------------- | --------------------------------------------------------------------------------------------- |
| Dashboard summary row  | `Stat` / `CostCallout` / `Trend` as siblings above a `ListingLayout`                          |
| Filter bar             | `FilterGroup` / `FilterDropdown` + `<Input leadingIcon={<SearchIcon/>} />` (or `SearchInput`) |
| Empty & loading states | `Skeleton` (loading) · `EmptyState` (no data) · `@shared/ErrorBoundary` (error)               |
| Tabbed page            | `Tabs` with `navigation='router'` wrapping per-tab content                                    |
| AI / agentic surface   | `SourceCitation` + `FeedbackVote` + `Markdown` (`@shared/viewers/MarkDowns`) + `DiffViewer`   |

---

## 3. Decision rules — overlapping components

When two components could fit, this is which to pick.

| Situation                                                                                          | Use                                                                | Not                                                                        |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ | -------------------------------------------------------------------------- |
| Read-only status pill in a table cell (`Active` / `Failed` / `Pending`) — 5 status tones, no click | `ds/Label`                                                         | `ds/Chip` (Chip is for interactive / categorical use)                      |
| Interactive or categorical pill — filter, dismissible tag, count, categorical hue, avatar          | `ds/Chip`                                                          | `ds/Label` (Label is read-only Status-axis only)                           |
| Resource-state read-out in a header / drawer / chat preamble (dot + text + subtext)                | `ds/StatusIndicator`                                               | `ds/Label` (Label is the inline cell tag)                                  |
| Any tabular data (this app)                                                                        | `@shared/tables/CustomTable`                                       | a hand-rolled `<table>` — there is no `ds/Table` primitive                 |
| One collapsible unit                                                                               | `ds/CollapsableCard`                                               | `ds/Accordion` ("Don't use Accordion for < 3 rows")                        |
| 3+ sibling collapsibles                                                                            | `ds/Accordion`                                                     | stacking `CollapsableCard`s                                                |
| In-page tab switch (state only) — modals, panels, in-component toggles                             | `ds/Tabs` with `navigation='state'` (or `@shared/navigation/Tabs`) | a hand-rolled toggle row                                                   |
| Tabs that drive the URL / route                                                                    | `ds/Tabs` with `navigation='router'`                               | `CustomTabs` / `ds/PageTabs` (both removed)                                |
| Top-of-page **2-level** nav — parent tabs with hover-dropdown subtabs, URL-hash driven             | `@shared/navigation/AnchorComponent`                               | `ds/Tabs` (single-level only)                                              |
| Transient confirmation message                                                                     | `ds/Toast`                                                         | `ds/Banner`                                                                |
| Persistent inline page-level message                                                               | `ds/Banner`                                                        | `ds/Toast`                                                                 |
| Small overlay anchored to an element                                                               | `ds/Tooltip` (text/explainer) · `DropdownMenu` (menu)              | `ds/Modal` (there is no `ds/Popover`)                                      |
| Centered blocking task / decision                                                                  | `ds/Modal`                                                         | a side-drawer (there is no `ds/Inspector`)                                 |
| Pick one value in a **form**                                                                       | `ds/Select`                                                        | `DropdownMenu` (action menu) · `FilterDropdown` (toolbar pill)             |
| Pick **multiple** values in a form                                                                 | `<Select multiple>`                                                | a hand-rolled multi-select (no `ds/MultiSelect`)                           |
| Pick value(s) for a **toolbar filter**                                                             | `ds/FilterDropdown`                                                | `ds/Select` (wrong context — full-width field chrome, no clear-X)          |
| Many options needing search / free-typing                                                          | `ds/Select` (auto-search > 8 options)                              | a hand-rolled autocomplete (no `ds/Autocomplete`)                          |
| Trigger an action from a menu                                                                      | `DropdownMenu` / `ThreeDotsMenu`                                   | `ds/Select`                                                                |
| Single-line text input (text/email/password/url/textarea/number)                                   | `ds/Input`                                                         | MUI `<TextField>` · `@shared/forms/CustomTextField` (legacy)               |
| Search-style toolbar input                                                                         | `ds/SearchInput` or `<Input leadingIcon={<SearchIcon/>} />`        | a raw input + manual clear/Enter wiring                                    |
| Generic value picker with cluster / cloud-account chrome                                           | `@shared/CustomDropdown`                                           | `ds/Select` (doesn't model `groupByCloudProvider` / status indicators)     |
| Content-shaped loading placeholder                                                                 | `ds/Skeleton`                                                      | `ds/ProgressLinear`                                                        |
| Determinate progress against a known max (utilisation)                                             | `ds/ProgressBar`                                                   | `ds/ProgressLinear` (unknown max)                                          |
| Indeterminate "something is happening"                                                             | `ds/ProgressLinear`                                                | `ds/Skeleton`                                                              |
| Plain content card on any new screen                                                               | `ds/Card`                                                          | `ds/WidgetCard` / `ds/CustomBorderCard` (legacy; consolidated into `Card`) |
| Card needs a coloured left-edge for tone                                                           | `ds/Card variant="accent" + tone`                                  | a hand-rolled `borderLeft` on a `WidgetCard`                               |
| Subtle bg grouping inside a modal/Card                                                             | `ds/Card variant="tinted" + tone`                                  | a hand-rolled `<Box sx={{ backgroundColor }}>`                             |
| Card needs disclosure (open / closed body)                                                         | `ds/CollapsableCard`                                               | `ds/Card` with manual state                                                |
| Clickable card row (picker, drillable surface)                                                     | `ds/Card interactive + onClick`                                    | a raw `<Box onClick>` (loses focus ring + a11y role)                       |
| 2-4 narrow choices, all visible, switching a view (not a form value)                               | `ds/Toggle`                                                        | `ds/Tabs` (heavier) · `ds/Select` (hides choices)                          |
| Segmented multi/single-select form input                                                           | `ds/ToggleGroup`                                                   | `ds/Toggle` (state-only view switcher, not a form input)                   |
| Picking a value to submit in a form                                                                | `ds/Select`                                                        | `ds/Toggle`                                                                |
| Single date + time field in a form                                                                 | `@shared/widgets/CustomDateTimePicker`                             | (no `ds/DateRangePicker` exists)                                           |
| Inline navigation link                                                                             | `ds/Link`                                                          | a raw `<a>` (loses DS color/font token + external-icon)                    |
| "Ticket - {id}" inline link pattern                                                                | inline `<Link href={url} openInNew>{id}</Link>`                    | `common/CustomTicketLink` (removed)                                        |
| Download action button                                                                             | `@shared/buttons/DownloadButton`                                   | a raw `ds/Button` + manual `saveAs`                                        |
| Copy-to-clipboard icon button                                                                      | `@shared/buttons/CopyButton`                                       | a raw `ds/Button` + manual clipboard wiring                                |
| Show a static code snippet / shell command with copy (read-only)                                   | `ds/CodeBlock`                                                     | a hand-rolled `<pre>` + copy button                                        |
| Inline `code` chip inside a sentence                                                               | `<CodeBlock inline code='…' />`                                    | a raw `<code>` tag                                                         |
| Prose / markdown that _contains_ fenced code blocks                                                | `Markdown` (`@shared/viewers/MarkDowns`)                           | `ds/CodeBlock` (shows one snippet; doesn't parse markdown)                 |
| Editable / language-aware code (YAML/JSON/SQL/JS/Shell/MD)                                         | `ds/CodeEditor`                                                    | a raw `<CodeMirror>` · `ds/CodeBlock` (display-only)                       |
| Read-only code needing syntax highlighting / line numbers / folding                                | `ds/CodeEditor` with `readOnly`                                    | `ds/CodeBlock` (structural mono only, no highlighting)                     |
| Show **what changed** between two versions                                                         | `ds/DiffViewer`                                                    | `ds/CodeBlock` (single version)                                            |
| New DS-clean button                                                                                | `ds/Button`                                                        | `common/NewCustomButton` (removed)                                         |

---

## 4. Component API reference (`ds/` primitives)

Per-component reference for all 45 primitives in `src/components/common/ds/`, harvested from each
file's JSDoc + `Props` interface. **Conventions for the tables below:**

- **Req** column: `✓` = required, `—` = optional.
- Every primitive also accepts standard passthroughs where its source declares them — `id`,
  `className`, `data-testid`, `sx`/`style`, and the relevant `aria-*`. The tables list the
  **semantic** props; trivial passthroughs are omitted unless behaviourally significant.
- Import path is `@ui/<Name>` unless noted. Default export vs named export matches the import shown.

### Layout & containers

#### `Card` — `import { Card } from '@ui/Card'`

Canonical content-card surface (consolidates `WidgetCard` + `CustomBorderCard`). Slots: `header`,
`footer`, `children`. **Use** for all new card surfaces; **not** for tinting message panels (use `Banner`).

```tsx
<Card variant='outlined' size='md' header={<b>Spend</b>}>
  Body content
</Card>
```

**Variants:** `variant` `elevated`·`outlined`·`accent`·`tinted` · `size` `sm`·`md`·`lg` · `elevation` `raised`·`flat` · `tone` `neutral`·`info`·`success`·`warning`·`danger`.

| Prop                             | Type                                                        | Default      | Req                                                   |
| -------------------------------- | ----------------------------------------------------------- | ------------ | ----------------------------------------------------- |
| `variant`                        | `'elevated' \| 'outlined' \| 'accent' \| 'tinted'`          | `'elevated'` | —                                                     |
| `size`                           | `'sm' \| 'md' \| 'lg'`                                      | `'md'`       | —                                                     |
| `elevation`                      | `'raised' \| 'flat'`                                        | `'raised'`   | —                                                     |
| `tone`                           | `'neutral' \| 'info' \| 'success' \| 'warning' \| 'danger'` | `'neutral'`  | — (meaningful only with `variant='accent'\|'tinted'`) |
| `interactive`                    | `boolean`                                                   | `false`      | — (required when `onClick` is set)                    |
| `selected`                       | `boolean`                                                   | `false`      | —                                                     |
| `header` / `footer` / `children` | `ReactNode`                                                 | —            | —                                                     |
| `onClick`                        | `(e) => void`                                               | —            | —                                                     |

**Don't:** tint bg by hand (use `variant='tinted'`); nest Cards >1 deep; use `sx` for padding/shadow (use `size`/`elevation`); pass `tone` without `accent`/`tinted`; pass `onClick` without `interactive`.

#### `WidgetCard` — `import WidgetCard from '@ui/WidgetCard'` (legacy)

Plain white elevated container (`Box` passthrough). **Consolidated into `Card`** — don't introduce
new uses. Props: `children`, `sx`, plus any MUI `BoxProps`.

```tsx
<WidgetCard>Body content</WidgetCard>
```

#### `CustomBorderCard` — `import { CustomBorderCard } from '@ui/CustomBorderCard'` (legacy)

Lightweight surface with a bottom hairline + optional coloured left accent. Prefer `Card`; reach
here only for that specific look.

```tsx
<CustomBorderCard showLeftBorder borderLeftColor={ds.red[500]}>
  Row
</CustomBorderCard>
```

| Prop              | Type               | Default          | Req                      |
| ----------------- | ------------------ | ---------------- | ------------------------ |
| `borderColor`     | `string`           | hairline default | —                        |
| `borderLeftColor` | `string`           | —                | —                        |
| `borderLeftWidth` | `string \| number` | —                | —                        |
| `showLeftBorder`  | `boolean`          | `true`           | —                        |
| `padding`         | `string \| number` | default          | —                        |
| `onClick`         | `(e) => void`      | —                | — (makes it interactive) |

#### `CollapsableCard` — `import { CollapsableCard } from '@ui/CollapsableCard'`

A single standalone collapsible card. Composes `Card` for the surface. For 3+ siblings use `Accordion`.

```tsx
<CollapsableCard header={<b>Details</b>} persist='local' id='details'>
  Body
</CollapsableCard>
```

**Variants:** `composition` `header+body`·`header+meta+body` · `persist` `none`·`local`·`url` · `elevation` `raised`·`flat`.

| Prop           | Type                                  | Default         | Req                              |
| -------------- | ------------------------------------- | --------------- | -------------------------------- |
| `header`       | `ReactNode`                           | —               | ✓                                |
| `children`     | `ReactNode`                           | —               | ✓                                |
| `meta`         | `ReactNode` (only `header+meta+body`) | —               | —                                |
| `footer`       | `ReactNode`                           | —               | —                                |
| `defaultOpen`  | `boolean`                             | `true`          | —                                |
| `composition`  | `'header+body' \| 'header+meta+body'` | `'header+body'` | —                                |
| `persist`      | `'none' \| 'local' \| 'url'`          | `'none'`        | — (needs `id` for `local`/`url`) |
| `elevation`    | `'raised' \| 'flat'`                  | `'raised'`      | —                                |
| `onOpenChange` | `(open: boolean) => void`             | —               | —                                |

#### `Accordion` — `import { Accordion } from '@ui/Accordion'`

Group of sibling collapsibles. **Use** for 3+ rows; **not** for < 3 (those are just Cards) or a critical closed-by-default setting.

```tsx
<Accordion selection='single' items={[{ id: 'cpu', label: 'CPU', body: <Metrics /> }]} />
```

**Variants:** `selection` `single`·`multi` · `density` `sm`·`md`.

| Prop                 | Type                       | Default | Req |
| -------------------- | -------------------------- | ------- | --- |
| `items`              | `AccordionItem[]`          | —       | ✓   |
| `selection`          | `'single' \| 'multi'`      | —       | —   |
| `density`            | `'sm' \| 'md'`             | —       | —   |
| `defaultExpandedIds` | `string[]` (uncontrolled)  | —       | —   |
| `expandedIds`        | `string[]` (controlled)    | —       | —   |
| `onExpandedChange`   | `(next: string[]) => void` | —       | —   |

`AccordionItem`: `{ id, label, body, description?, meta?, icon?, disabled? }`.

#### `ListingLayout` — `import { ListingLayout } from '@ui/ListingLayout'`

Shell for table-listing screens. Slots only — composes primitives, no filter/action/pagination
knowledge. Sub-components: `.Toolbar`, `.ToolbarSpacer`, `.Body`, `.Footer`.

```tsx
<ListingLayout id='recs'>
  <ListingLayout.Toolbar title='Recommendations' actions={<Button>Export</Button>} />
  <ListingLayout.Body>
    <CustomTable headers={headers} tableData={rows} />
  </ListingLayout.Body>
</ListingLayout>
```

| Component       | Key props                                                               |
| --------------- | ----------------------------------------------------------------------- |
| `ListingLayout` | `id?`, `children` (✓), `sx?`                                            |
| `.Toolbar`      | `title?`, `children?` (left filters), `actions?` (right cluster), `sx?` |
| `.Body`         | `children` (✓), `padding?` (default `0 ds.space[5]`), `sx?`             |
| `.Footer`       | `children` (✓), `align?` `start`·`end`·`between` (default `end`), `sx?` |

**Don't:** put page-level `Stat` cards inside (siblings above); paginate inside `Body`.

#### `Divider` — `import { Divider } from '@ui/Divider'`

Thin visual separator. **Variants:** `orientation` `horizontal`·`vertical` · `style` `solid`·`dashed`.

```tsx
<Divider label='or' />
```

| Prop          | Type                          | Default              | Req |
| ------------- | ----------------------------- | -------------------- | --- |
| `orientation` | `'horizontal' \| 'vertical'`  | `'horizontal'`       | —   |
| `style`       | `'solid' \| 'dashed'`         | `'solid'`            | —   |
| `label`       | `ReactNode` (horizontal only) | —                    | —   |
| `color`       | `string`                      | `var(--ds-gray-200)` | —   |
| `thickness`   | `number`                      | `1`                  | —   |

#### `List` — `import { List } from '@ui/List'`

Vertical list of label rows with optional "show N / show all" truncation. Generic over item type `T`.

```tsx
<List items={names} bullet maxItemLength={40} truncate={{ show: 5 }} />
```

| Prop            | Type                                       | Default     | Req                                         |
| --------------- | ------------------------------------------ | ----------- | ------------------------------------------- |
| `items`         | `T[]`                                      | —           | ✓                                           |
| `renderItem`    | `(item: T, i: number) => ReactNode`        | —           | — (omit to use built-in bullet composition) |
| `keyFor`        | `(item: T, i: number) => React.Key`        | index       | —                                           |
| `truncate`      | `{ show: number, label?, collapseLabel? }` | —           | —                                           |
| `divider`       | `'none' \| 'between'`                      | `'between'` | —                                           |
| `bordered`      | `boolean`                                  | `true`      | —                                           |
| `empty`         | `ReactNode`                                | —           | —                                           |
| `bullet`        | `boolean` (built-in composition)           | —           | —                                           |
| `maxItemLength` | `number` (truncate string items + tooltip) | —           | —                                           |
| `onItemClick`   | `(item: T, i: number) => void`             | —           | —                                           |

### Data display

#### `Stat` — `import { Stat } from '@ui/Stat'`

KPI / metric tile (label + value + optional delta/sub/icon). **Use** one `hero` per page max.

```tsx
<Stat label='Monthly spend' value='$1,240' delta={{ value: -12, period: 'vs last mo', tone: 'savings' }} />
```

**Variants:** `size` `sm`·`md`·`hero` · `align` `start`·`center` · delta `tone` `high-savings`·`savings`·`neutral`·`waste`·`high-waste`.

| Prop             | Type                                 | Default   | Req |
| ---------------- | ------------------------------------ | --------- | --- |
| `label`          | `string`                             | —         | ✓   |
| `value`          | `string \| number \| ReactNode`      | —         | ✓   |
| `size`           | `'sm' \| 'md' \| 'hero'`             | `'md'`    | —   |
| `align`          | `'start' \| 'center'`                | `'start'` | —   |
| `delta`          | `StatDelta`                          | —         | —   |
| `sub`            | `ReactNode`                          | —         | —   |
| `icon`           | `ReactNode`                          | —         | —   |
| `iconPlacement`  | `'rail' \| 'inline'`                 | `'rail'`  | —   |
| `deltaPlacement` | `'below' \| 'inline'`                | `'below'` | —   |
| `info`           | `{ tooltip, position? }`             | —         | —   |
| `headerRight`    | `ReactNode`                          | —         | —   |
| `format`         | `'plain' \| 'percent' \| 'currency'` | `'plain'` | —   |
| `onClick`        | `() => void`                         | —         | —   |

`StatDelta`: `{ value: number\|string, period: string (✓), tone?, direction?: 'up'\|'down'\|'flat' }`.
**Don't:** tone the value (tone goes on the delta); render a delta without a comparison anchor.

#### `Trend` — `import { Trend } from '@ui/Trend'`

Percentage trend arrow. Convention: `value*sign > 0` → down arrow + green (improvement); negative → up arrow + red.

```tsx
<Trend value={12} sign={-1} variant='chip' />
```

**Variants:** `variant` `inline`·`chip` · `size` `xs`·`sm`·`md`·`lg` (`'default'` accepted as alias for `md`).

| Prop      | Type                           | Default    | Req |
| --------- | ------------------------------ | ---------- | --- |
| `value`   | `number`                       | —          | ✓   |
| `sign`    | `1 \| -1`                      | `1`        | —   |
| `size`    | `'xs' \| 'sm' \| 'md' \| 'lg'` | —          | —   |
| `variant` | `'inline' \| 'chip'`           | `'inline'` | —   |
| `width`   | `string`                       | —          | —   |

#### `Comparison` — `import { Comparison, ComparisonGroup } from '@ui/Comparison'`

Compact "before → after, with delta" for dense cells. Pairs with `ComparisonGroup` for spacing rhythm.

```tsx
<Comparison before={{ value: '1.2 Core' }} after={{ value: '0.5 Core' }} polarity='lower-is-better' />
```

**Variants:** `size` `sm`·`md` · `layout` `stacked`·`inline` · `polarity` `lower-is-better`·`higher-is-better`·`neutral` · `deltaFormat` `percent`·`absolute`·`auto`.

| Prop            | Type                                                   | Default             | Req |
| --------------- | ------------------------------------------------------ | ------------------- | --- |
| `before`        | `ComparisonValue`                                      | —                   | ✓   |
| `after`         | `ComparisonValue`                                      | —                   | ✓   |
| `label`         | `ReactNode`                                            | —                   | —   |
| `polarity`      | `'lower-is-better' \| 'higher-is-better' \| 'neutral'` | `'lower-is-better'` | —   |
| `deltaFormat`   | `'percent' \| 'absolute' \| 'auto'`                    | `'auto'`            | —   |
| `size`          | `'sm' \| 'md'`                                         | `'sm'`              | —   |
| `layout`        | `'stacked' \| 'inline'`                                | `'stacked'`         | —   |
| `deltaOverride` | `ReactNode`                                            | —                   | —   |
| `showZeroDelta` | `boolean`                                              | `true`              | —   |

`ComparisonValue`: `{ value: number\|string\|null, unit? }`. `ComparisonGroup`: `{ children, spacing?: 'xs'\|'sm'\|'md', dividers? }`.
**Don't:** pick delta colour manually (use `polarity`); use `size='md'` in table cells.

#### `CostCallout` — `import { CostCallout } from '@ui/CostCallout'`

Inline currency figure + optional delta arrow + period. Always tabular-nums, locale-aware.

```tsx
<CostCallout value={890} arrow='down' tone='high-savings' period='/ mo' />
```

**Variants:** `tone` `high-savings`·`medium-savings`·`low-savings`·`neutral`·`waste` · `size` `sm`·`md`·`lg`·`display` · `arrow` `down`·`up`·`flat`·`none`.

| Prop             | Type                                 | Default     | Req |
| ---------------- | ------------------------------------ | ----------- | --- |
| `value`          | `number`                             | —           | ✓   |
| `currency`       | `string` (ISO)                       | `'USD'`     | —   |
| `locale`         | `string`                             | session     | —   |
| `tone`           | `CostTone`                           | `'neutral'` | —   |
| `size`           | `'sm' \| 'md' \| 'lg' \| 'display'`  | `'md'`      | —   |
| `period`         | `ReactNode` (e.g. "/ mo")            | —           | —   |
| `arrow`          | `'down' \| 'up' \| 'flat' \| 'none'` | `'none'`    | —   |
| `fractionDigits` | `number`                             | `0`         | —   |

**Don't:** pick tone manually; show an arrow without a comparison reference; use `size='display'` twice per page.

#### `ProgressBar` — `import { ProgressBar } from '@ui/ProgressBar'`

Utilisation gauge against a known max. Tone auto-resolves from `thresholds` + `value`.

```tsx
<ProgressBar value={72} thresholds={{ success: 60, warning: 80 }} showValue />
```

**Variants:** `size` `sm`·`md` · `tone` `neutral`·`success`·`warning`·`critical`.

| Prop          | Type                                              | Default   | Req |
| ------------- | ------------------------------------------------- | --------- | --- |
| `value`       | `number`                                          | —         | ✓   |
| `max`         | `number`                                          | `100`     | —   |
| `label`       | `ReactNode`                                       | —         | —   |
| `showValue`   | `boolean`                                         | —         | —   |
| `formatValue` | `(value, max, pct) => string`                     | `${pct}%` | —   |
| `thresholds`  | `{ success?, warning?, critical? }` (percentages) | —         | —   |
| `tone`        | `ProgressBarTone` (override)                      | auto      | —   |
| `size`        | `'sm' \| 'md'`                                    | `'sm'`    | —   |

**Don't:** pick tone manually (use `thresholds`); use for unknown maximums (that's `ProgressLinear`).

#### `ProgressLinear` — `import { ProgressLinear } from '@ui/ProgressLinear'`

Indeterminate or determinate horizontal progress at the top of a region.

```tsx
<ProgressLinear surface='page-top' />
```

**Variants:** `mode` `indeterminate`·`determinate` (auto-determinate when `value` set) · `tone` `neutral`·`info` · `surface` `page-top`·`section`·`inline`.

| Prop      | Type                                  | Default     | Req |
| --------- | ------------------------------------- | ----------- | --- |
| `mode`    | `'indeterminate' \| 'determinate'`    | auto        | —   |
| `value`   | `number` (→ determinate)              | —           | —   |
| `total`   | `number`                              | `100`       | —   |
| `tone`    | `'neutral' \| 'info'`                 | `'info'`    | —   |
| `surface` | `'page-top' \| 'section' \| 'inline'` | `'section'` | —   |

**Don't:** show for < 200ms actions; combine with a `Skeleton` on the same region.

#### `Chart` — `import Chart from '@ui/Chart'`

Namespace wrapper around the chart family. Members:

```tsx
<Chart.TimeSeries labels={labels} series={series} />
```

- `Chart.Line` — feature-rich Prometheus line chart (pinned points, Ask-Nubi, own tooltip).
- `Chart.Series` — lean line/area passthrough (caller supplies datasets + options).
- `Chart.TimeSeries` — multi-series bar/area/line from `{ labels, series }` + totals legend.
- `Chart.Bar` · `Chart.Doughnut` — bar / doughnut charts.

Legacy default-import paths (e.g. `@shared/charts/LineCharts`) resolve to the same instance.
Per-chart props are chart-specific data/options passthroughs — read the source for the exact shape.

#### `SeverityIcon` — `import { SeverityIcon } from '@ui/SeverityIcon'`

Letter-badge severity marker (C/H/M/L/I, fixed per level). Always pair with an accessible label.

```tsx
<SeverityIcon level='critical' label='3 critical' />
```

**Variants:** `level` `critical`·`high`·`medium`·`low`·`info` · `variant` `bar`·`square` · `size` `12`·`14`·`16`·`20`.

| Prop         | Type                                          | Default | Req                         |
| ------------ | --------------------------------------------- | ------- | --------------------------- |
| `level`      | `'critical'\|'high'\|'medium'\|'low'\|'info'` | —       | ✓                           |
| `variant`    | `'bar' \| 'square'`                           | `'bar'` | —                           |
| `size`       | `12 \| 14 \| 16 \| 20`                        | —       | —                           |
| `label`      | `ReactNode`                                   | —       | —                           |
| `count`      | `number`                                      | —       | —                           |
| `aria-label` | `string`                                      | —       | — (required when icon-only) |

**Don't:** override the letter/colour; use Severity to communicate Status.

#### `StatusIndicator` — `import { StatusIndicator } from '@ui/StatusIndicator'`

Resource-state read-out (dot/icon + text + optional subtext) for headers, drawers, chat preambles.

```tsx
<StatusIndicator tone='healthy' label='Running' subtext='12 nodes · 12s ago' />
```

**Variants:** `tone` `healthy`·`degraded`·`failed`·`pending`·`unknown` · `size` `sm`·`md`.

| Prop      | Type                                                    | Default | Req |
| --------- | ------------------------------------------------------- | ------- | --- |
| `tone`    | `'healthy'\|'degraded'\|'failed'\|'pending'\|'unknown'` | —       | ✓   |
| `label`   | `ReactNode`                                             | —       | —   |
| `subtext` | `ReactNode`                                             | —       | —   |
| `icon`    | `ReactNode` (incompatible with default dot)             | —       | —   |
| `size`    | `'sm' \| 'md'`                                          | —       | —   |

**Don't:** use inside a table cell (that's `Label`); put actions in the subtext; combine icon+dot.

### Labels & status

#### `Label` — `import { Label } from '@ui/Label'`

Read-only status-axis pill for table cells. No click (if interactive, use `Chip`).

```tsx
<Label tone='success'>Active</Label>
```

**Variants:** `tone` `neutral`·`info`·`success`·`warning`·`critical` · `size` `sm`·`md` · composition auto from `icon`/`dot`.

| Prop               | Type                                                  | Default     | Req           |
| ------------------ | ----------------------------------------------------- | ----------- | ------------- |
| `children`         | `ReactNode` (preferred)                               | —           | — (or `text`) |
| `text`             | `string` (legacy; feeds auto-tone + tooltip)          | —           | —             |
| `tone`             | `'neutral'\|'info'\|'success'\|'warning'\|'critical'` | auto-detect | —             |
| `size`             | `'sm' \| 'md'`                                        | `'sm'`      | —             |
| `icon`             | `ReactNode` (mutually exclusive with `dot`)           | —           | —             |
| `dot`              | `boolean`                                             | `false`     | —             |
| `displayTooltip`   | `boolean`                                             | `false`     | —             |
| `tooltipCharLimit` | `number`                                              | —           | —             |
| `tooltipPosition`  | `'top'\|'bottom'\|'left'\|'right'`                    | `'top'`     | —             |
| `maxWidth`         | `string`                                              | `'350px'`   | —             |

Tone precedence: explicit `tone` → legacy `variant` → auto-detect from `text`.
**Don't:** add a click handler; pick a tone outside the Status axis; combine `dot` + `icon`.

#### `Chip` — `import { Chip } from '@ui/Chip'`. Also exports `hashHue(key)`.

Interactive or categorical pill (filters, dismissible tags, counts, hues, avatars).

```tsx
<Chip tone='info' onDismiss={() => remove('prod')}>
  env: prod
</Chip>
```

**Variants:** `variant` `filter`·`tag`·`status`·`input`·`action`·`count`·`avatar` (auto from props) · `size` `micro`·`2xs`·`xs`·`sm`·`md` · `tone` `neutral`·`subtle`·`info`·`success`·`warning`·`critical`·`savings`·`waste`·`agent` · `shape` `pill`·`rect` · `hue` `slate`·`green`·`amber`·`red`·`blue`·`violet`·`pink`·`teal` (tag chips only).

| Prop                                  | Type                                    | Default | Req |
| ------------------------------------- | --------------------------------------- | ------- | --- |
| `children`                            | `ReactNode`                             | —       | —   |
| `variant` / `size` / `tone` / `shape` | unions above                            | derived | —   |
| `hue`                                 | `ChipHue` (tag chips; overrides tone)   | —       | —   |
| `icon` / `leadingAvatar`              | `ReactNode` (mutually exclusive)        | —       | —   |
| `count`                               | `number`                                | —       | —   |
| `dot` / `dotVariant`                  | `boolean` / `'filled'\|'hollow'`        | —       | —   |
| `trailingIcon` / `trailingChevron`    | `ReactNode` / `boolean`                 | —       | —   |
| `solid`                               | `boolean` (P0/critical only)            | —       | —   |
| `onClick`                             | `(e) => void` (⇒ clickable)             | —       | —   |
| `onDismiss`                           | `(e) => void` (⇒ dismissible, × button) | —       | —   |
| `pressed` / `selected`                | `boolean` (toggle/filter state)         | —       | —   |
| `loading` / `disabled`                | `boolean`                               | —       | —   |
| `displayTooltip` / `tooltipCharLimit` | `boolean` / `number` (default 30)       | —       | —   |

**Don't:** invent a tone; `solid` on non-critical/warning tones; `icon`-only at `size='md'` (that's a Button); `hue` on non-tag variants.

### Content & code

#### `CodeBlock` — `import { CodeBlock } from '@ui/CodeBlock'`

Display a static snippet/command with a copy affordance. **Variants:** `inline` `false`·`true` · `tone` `light`·`dark`.

```tsx
<CodeBlock code='kubectl get pods' language='bash' prompt='$' />
```

| Prop              | Type                | Default   | Req |
| ----------------- | ------------------- | --------- | --- |
| `code`            | `string`            | —         | ✓   |
| `language`        | `string` (header)   | —         | —   |
| `title`           | `string` (header)   | —         | —   |
| `inline`          | `boolean`           | `false`   | —   |
| `tone`            | `'light' \| 'dark'` | `'light'` | —   |
| `showLineNumbers` | `boolean`           | `false`   | —   |
| `showCopy`        | `boolean`           | `true`    | —   |
| `wrap`            | `boolean`           | `false`   | —   |
| `prompt`          | `string` (e.g. `$`) | —         | —   |
| `maxHeight`       | `number \| string`  | —         | —   |
| `copyToast`       | `string`            | —         | —   |

**Don't:** render markdown prose (that's `Markdown`); use as an editor (that's `CodeEditor`); bake the prompt char into `code`.

#### `CodeEditor` — `import { CodeEditor } from '@ui/CodeEditor'`

Editable / language-aware code (CodeMirror), or read-only viewer with highlighting. **Variants:** `tone` `light`·`dark` · `readOnly` `false`·`true`.

```tsx
<CodeEditor value={yaml} onChange={setYaml} language='yaml' />
```

| Prop                                            | Type                                                               | Default     | Req |
| ----------------------------------------------- | ------------------------------------------------------------------ | ----------- | --- |
| `value`                                         | `string`                                                           | —           | ✓   |
| `onChange`                                      | `(value: string) => void`                                          | —           | —   |
| `language`                                      | `'yaml'\|'json'\|'sql'\|'javascript'\|'shell'\|'markdown'\|'text'` | `'text'`    | —   |
| `readOnly`                                      | `boolean`                                                          | `false`     | —   |
| `tone`                                          | `'light' \| 'dark'`                                                | `'light'`   | —   |
| `height`                                        | `number \| string`                                                 | `'300px'`   | —   |
| `minHeight` / `maxHeight`                       | `number \| string`                                                 | —           | —   |
| `title` / `languageLabel` / `showLanguageLabel` | header controls                                                    | —           | —   |
| `showCopy`                                      | `boolean`                                                          | `=readOnly` | —   |
| `lineNumbers` / `foldGutter`                    | `boolean`                                                          | `true`      | —   |
| `error`                                         | `string \| boolean`                                                | —           | —   |
| `extensions`                                    | `Extension[]` (PromQL/lint escape hatch)                           | —           | —   |

**Don't:** use just to display a snippet (that's `CodeBlock`); pass markdown prose (that's `Markdown`).

#### `DiffViewer` — `import { DiffViewer } from '@ui/DiffViewer'`

Show what changed between two versions. Engine inferred from input (override with `mode`).

```tsx
<DiffViewer originalCode={prev} newCode={next} language='yaml' />
```

| Prop                              | Type                        | Default                     | Req              |
| --------------------------------- | --------------------------- | --------------------------- | ---------------- |
| `gitDiff`                         | `string` (→ unified)        | —                           | — (one of these) |
| `originalCode` + `newCode`        | `string` (→ split)          | —                           | —                |
| `mode`                            | `'unified' \| 'split'`      | inferred                    | —                |
| `language`                        | `CodeEditorLanguage`        | `'text'`                    | —                |
| `tone`                            | `'light' \| 'dark'`         | `'light'`                   | —                |
| `title` / `fileName`              | `string`                    | `'Code Changes'` / `'code'` | —                |
| `showHeader`                      | `boolean`                   | `true`                      | —                |
| `collapsible` / `defaultExpanded` | `boolean`                   | `true`                      | —                |
| `leftLabel` / `rightLabel`        | `ReactNode` (split columns) | —                           | —                |
| `maxHeight`                       | `number \| string`          | `400`                       | —                |

**Don't:** use for a single version (that's `CodeBlock`/`CodeEditor`); hand-roll diff colouring.

### Forms & inputs

#### `Input` — `import { Input } from '@ui/Input'`

Unified text-entry primitive. **Variants:** `size` `sm`·`md`·`lg` · `type` `text`·`number`·`email`·`password`·`url`·`textarea`.

```tsx
<Input label='Name' value={name} onChange={setName} required />
```

| Prop                                 | Type                                          | Default        | Req |
| ------------------------------------ | --------------------------------------------- | -------------- | --- |
| `value`                              | `string`                                      | —              | ✓   |
| `onChange`                           | `(next: string) => void`                      | —              | ✓   |
| `label`                              | `ReactNode`                                   | —              | —   |
| `instructionText`                    | `ReactNode` (between label and input)         | —              | —   |
| `help`                               | `ReactNode` (hidden when `error` set)         | —              | —   |
| `error`                              | `string` (presence ⇒ error; message required) | —              | —   |
| `prefix` / `suffix`                  | `ReactNode` (outside the input bounds)        | —              | —   |
| `leadingIcon` / `trailingIcon`       | `ReactNode` (inside bounds)                   | —              | —   |
| `size`                               | `'sm' \| 'md' \| 'lg'`                        | `'md'`         | —   |
| `type`                               | `InputType`                                   | `'text'`       | —   |
| `placeholder`                        | `string`                                      | —              | —   |
| `animatePlaceholder`                 | `boolean` / `typingSpeed` (ms/char)           | `false` / `60` | —   |
| `required` / `disabled` / `readOnly` | `boolean`                                     | —              | —   |
| `rows` / `minRows` / `maxRows`       | `number` (textarea)                           | — / `3` / `20` | —   |
| `onBlur` / `onFocus` / `onKeyDown`   | handlers                                      | —              | —   |
| `data-testid`                        | `string` (forwarded to the native input)      | —              | —   |

**Don't:** pass a boolean to `error`; combine `leadingIcon`+`prefix` (or `trailingIcon`+`suffix`) on the same side; combine `readOnly`+`disabled`.

#### `SearchInput` — `import SearchInput from '@ui/SearchInput'` (default export, `.jsx`)

Search-style toolbar input (Enter to search, X to clear). Thin wrapper over `ds/Input`.

```tsx
<SearchInput value={q} onChange={setQ} onEnterPress={runSearch} onClear={clear} />
```

| Prop                                                | Type                                        | Default | Req |
| --------------------------------------------------- | ------------------------------------------- | ------- | --- |
| `value`                                             | `string`                                    | —       | ✓   |
| `onChange`                                          | `(newValue) => void`                        | —       | ✓   |
| `label`                                             | `string` (placeholder)                      | `''`    | —   |
| `onEnterPress`                                      | `() => void`                                | —       | —   |
| `onClear`                                           | `() => void` (also re-fires `onEnterPress`) | —       | —   |
| `disabled`                                          | `boolean`                                   | `false` | —   |
| `minWidth` / `maxWidth` / `ml` / `mr` / `sx` / `id` | layout passthroughs                         | —       | —   |

#### `Select` — `import { Select } from '@ui/Select'`

Form-field value picker. Single by default; `multiple` discriminates a union. Built-in search auto-shows > 8 options. **Variants:** `size` `sm`·`md`·`lg`.

```tsx
<Select label='Project' options={projects} value={project} onChange={setProject} />
```

**Common props (both modes):**

| Prop                       | Type                         | Default               | Req                                                     |
| -------------------------- | ---------------------------- | --------------------- | ------------------------------------------------------- |
| `options`                  | `(string \| SelectOption)[]` | —                     | ✓                                                       |
| `label`                    | `ReactNode`                  | —                     | —                                                       |
| `instructionText` / `help` | `ReactNode`                  | —                     | —                                                       |
| `error`                    | `string`                     | —                     | —                                                       |
| `placeholder`              | `string`                     | —                     | —                                                       |
| `required`                 | `boolean`                    | —                     | —                                                       |
| `clearable`                | `boolean`                    | `true`                | — (suppressed when `required`)                          |
| `disabled`                 | `boolean`                    | —                     | —                                                       |
| `size`                     | `'sm' \| 'md' \| 'lg'`       | —                     | —                                                       |
| `minWidth`                 | `string \| number`           | —                     | —                                                       |
| `popoverWidth`             | `string \| number`           | trigger width         | — (widen panel for long labels under a compact trigger) |
| `searchable`               | `boolean`                    | `true` if > 8 options | —                                                       |
| `searchPlaceholder`        | `string`                     | `'Search…'`           | —                                                       |
| `loading`                  | `boolean`                    | —                     | —                                                       |
| `disablePortal`            | `boolean`                    | —                     | —                                                       |

**Single** (`multiple` omitted/`false`): `value: string \| null` (✓), `onChange: (next: string) => void` (✓).
**Multi** (`multiple: true`): `value: string[]` (✓), `onChange: (next: string[]) => void` (✓), `maxChips?` (default 2), `hideOptionCheckbox?`.
`SelectOption`: `{ value (✓), label?, icon?, disabled? }`.
**Don't:** use for actions (use `DropdownMenu`); inside a toolbar filter (use `FilterDropdown`); for binary choices (use `Switch`/`ToggleGroup`).

#### `Checkbox` — `import { Checkbox } from '@ui/Checkbox'`

Tri-state on/off/indeterminate. Always labelled. **Variants:** `size` `sm`·`md`.

```tsx
<Checkbox checked={on} onChange={setOn} label='Enable notifications' />
```

| Prop            | Type                      | Default | Req                                  |
| --------------- | ------------------------- | ------- | ------------------------------------ |
| `checked`       | `boolean`                 | —       | ✓                                    |
| `onChange`      | `(next: boolean) => void` | —       | ✓                                    |
| `label`         | `ReactNode`               | —       | —                                    |
| `description`   | `ReactNode`               | —       | —                                    |
| `indeterminate` | `boolean`                 | —       | —                                    |
| `disabled`      | `boolean`                 | —       | —                                    |
| `size`          | `'sm' \| 'md'`            | `'md'`  | —                                    |
| `aria-label`    | `string`                  | —       | — (required when no visible `label`) |
| `data-testid`   | `string`                  | —       | — (forwarded to the native `input`)  |

**Don't:** use for immediate "enable/disable" (that's `Switch`); render label-less in lists; use indeterminate for loading.

#### `Switch` — `import { Switch } from '@ui/Switch'`

Immediate on/off toggle (no submit step). Label always on the LEFT. **Variants:** `size` `sm`·`md`.

```tsx
<Switch checked={on} onChange={(_, c) => setOn(c)} label='Auto-refresh' />
```

| Prop                   | Type                                | Default | Req |
| ---------------------- | ----------------------------------- | ------- | --- |
| `checked`              | `boolean`                           | —       | ✓   |
| `onChange`             | `(event, checked: boolean) => void` | —       | ✓   |
| `label`                | `ReactNode` (left of switch)        | —       | —   |
| `description`          | `ReactNode`                         | —       | —   |
| `size`                 | `'sm' \| 'md'` (sm 28×16, md 36×20) | `'md'`  | —   |
| `disabled` / `loading` | `boolean`                           | `false` | —   |

**Don't:** use in a form with a submit button (use `Checkbox`); put the label on the right; pair with a confirmation Dialog for non-destructive changes.

#### `Toggle` — `import { Toggle } from '@ui/Toggle'`

Compact button-row view switcher (state-only). **Variants:** `size` `default`·`large`·`sm`.

```tsx
<Toggle
  options={[
    { value: 'mine', label: 'Yours' },
    { value: 'team', label: 'Team' },
  ]}
  activeValue={view}
  onChange={setView}
/>
```

| Prop          | Type                           | Default | Req |
| ------------- | ------------------------------ | ------- | --- |
| `options`     | `ToggleOption[]`               | —       | ✓   |
| `activeValue` | `string`                       | —       | ✓   |
| `onChange`    | `(value: string) => void`      | —       | ✓   |
| `width`       | `string`                       | —       | —   |
| `size`        | `'default' \| 'large' \| 'sm'` | —       | —   |
| `noShadow`    | `boolean`                      | —       | —   |

`ToggleOption`: `{ value, label, icon?, disabled? }` (`icon` accepts a SafeIcon src or a React element).
**Don't:** use as a form-value picker (use `Select`/`ToggleGroup`); disable the active option; pass > 4 options.

#### `ToggleGroup` — `import { ToggleGroup } from '@ui/ToggleGroup'`

Segmented single/multi-select form input. **Variants:** `selection` `single`·`multiple` · `size` `sm`·`md`.

```tsx
<ToggleGroup selection='single' options={units} value={unit} onChange={setUnit} />
```

| Prop        | Type                            | Default | Req |
| ----------- | ------------------------------- | ------- | --- |
| `options`   | `ToggleGroupOption<V>[]`        | —       | ✓   |
| `selection` | `'single' \| 'multiple'`        | —       | ✓   |
| `value`     | `V` (single) / `V[]` (multiple) | —       | ✓   |
| `onChange`  | `(next: V \| V[]) => void`      | —       | ✓   |
| `size`      | `'sm' \| 'md'`                  | —       | —   |
| `ariaLabel` | `string`                        | —       | —   |

`ToggleGroupOption`: `{ value (✓), label?, icon?, ariaLabel?, tooltip?, disabled? }`.
**Don't:** > 5 options (use `Select`); mix icon-only and icon+text in one group.

#### `FilterDropdown` — `import FilterDropdownButton from '@ui/FilterDropdown'` (default export, `.jsx`)

Toolbar/filter value picker (pill trigger, clear-X). **Variants:** `multiple`, `grouped`, `freeSolo` (booleans).

```tsx
<FilterDropdownButton options={severities} value={selected} onChange={setSelected} multiple />
```

| Prop                   | Type                       | Default                     | Req |
| ---------------------- | -------------------------- | --------------------------- | --- |
| `options`              | `array`                    | `[]`                        | —   |
| `value` / `onChange`   | controlled value + handler | —                           | —   |
| `multiple`             | `boolean`                  | `false`                     | —   |
| `grouped`              | `boolean`                  | `false`                     | —   |
| `selectionWithinGroup` | `boolean`                  | `false`                     | —   |
| `freeSolo`             | `boolean`                  | `false`                     | —   |
| `popoverAlign`         | `'left' \| 'right'`        | `'left'`                    | —   |
| `popoverWidth`         | `number \| string`         | trigger width (220px floor) | —   |
| `limitTag`             | `number`                   | `1`                         | —   |
| `size`                 | `string`                   | `'sm'`                      | —   |
| `required`             | `boolean`                  | `false`                     | —   |
| `disabled`             | `boolean`                  | `false`                     | —   |
| `isOptionsLoading`     | `boolean`                  | `false`                     | —   |

API preserved verbatim from the legacy component (see `__tests__/components/common/FilterDropdownButton.test.jsx`). An option's `icon` renders as a 16px leading `SafeIcon`.
**Don't:** use for form inputs (use `Select`); render single-select when the user must always pick exactly one (use `Select`).

#### `FilterGroup` — `import { FilterGroup } from '@ui/FilterGroup'`

A row of removable filter Chips with a leading "Filters" affordance. **Variants:** `overflow` `wrap`·`more-menu` · `size` `sm`·`md`.

```tsx
<FilterGroup filters={chips} onRemove={remove} onClear={clearAll} />
```

| Prop               | Type                           | Default  | Req                                   |
| ------------------ | ------------------------------ | -------- | ------------------------------------- |
| `filters`          | `FilterGroupChip[]`            | —        | ✓                                     |
| `onRemove`         | `(chip) => void`               | —        | ✓                                     |
| `onAdd`            | `(filter) => void`             | —        | — (required for `add+…` compositions) |
| `onClear`          | `() => void`                   | —        | — (required for `add+chips+clear`)    |
| `availableFilters` | `FilterGroupAvailableFilter[]` | —        | —                                     |
| `overflow`         | `'wrap' \| 'more-menu'`        | `'wrap'` | —                                     |
| `maxInline`        | `number`                       | `6`      | —                                     |
| `size`             | `'sm' \| 'md'`                 | `'md'`   | —                                     |

`FilterGroupChip`: `{ id, label }`. `FilterGroupAvailableFilter`: `{ id, label, disabled? }`.

### Navigation

#### `Tabs` — `import { Tabs } from '@ui/Tabs'`

DS tabs primitive — emits `onChange`; pages render content. **Variants:** `size` `sm`·`md` · `navigation` `state`·`router` · `routerMode` `query`·`hash` · `overflow` `scroll`·`more-menu`.

```tsx
<Tabs tabs={tabs} value={tab} onChange={setTab} navigation='router' />
```

| Prop              | Type                       | Default    | Req |
| ----------------- | -------------------------- | ---------- | --- |
| `tabs`            | `TabItem[]`                | —          | ✓   |
| `value`           | `TabId` (string)           | —          | ✓   |
| `onChange`        | `(next: TabId) => void`    | —          | ✓   |
| `size`            | `'sm' \| 'md'`             | `'md'`     | —   |
| `navigation`      | `'state' \| 'router'`      | `'state'`  | —   |
| `routerMode`      | `'query' \| 'hash'`        | `'query'`  | —   |
| `routerParam`     | `string`                   | `'tab'`    | —   |
| `overflow`        | `'scroll' \| 'more-menu'`  | `'scroll'` | —   |
| `visibleTabCount` | `number` (for `more-menu`) | —          | —   |
| `rightSlot`       | `ReactNode`                | —          | —   |

`TabItem`: `{ id, label, icon?, iconPosition?: 'start'\|'end', count?, countTone?, beta?, disabled?, hidden? }`.
**Don't:** tone the whole tab (only the count via `TabItem.countTone`); render tab content inside `Tabs`.

#### `Link` — `import { Link } from '@ui/Link'`

Inline navigation link (Next.js wrapper). For actions use `<Button tone='link'>`.

```tsx
<Link href={ticketUrl} openInNew>
  View ticket
</Link>
```

| Prop            | Type                          | Default   | Req |
| --------------- | ----------------------------- | --------- | --- |
| `href`          | `string`                      | —         | ✓   |
| `children`      | `ReactNode`                   | —         | ✓   |
| `target`        | `string`                      | `'_self'` | —   |
| `openInNew`     | `boolean` (new tab + icon)    | `false`   | —   |
| `secondaryText` | `boolean` (caption size)      | `false`   | —   |
| `maxWidth`      | `string` (truncate + tooltip) | —         | —   |
| `onClick`       | `(e) => void`                 | —         | —   |

**Don't:** use for actions; use with `onClick` alone and no `href` (use a `tone='link'` Button); add custom underline styles.

#### `Stepper` — `import { Stepper } from '@ui/Stepper'`

Multi-step progress indicator. **Variants:** `orientation` `vertical`·`horizontal` · `interactivity` `static`·`clickable-done`·`all-clickable`.

```tsx
<Stepper steps={steps} current={1} orientation='vertical' />
```

| Prop            | Type                                          | Default | Req |
| --------------- | --------------------------------------------- | ------- | --- |
| `steps`         | `StepperStep[]`                               | —       | ✓   |
| `current`       | `number` (0-based active step)                | —       | ✓   |
| `orientation`   | `'vertical' \| 'horizontal'`                  | —       | —   |
| `interactivity` | `'static'\|'clickable-done'\|'all-clickable'` | —       | —   |
| `onStepClick`   | `(id, index) => void`                         | —       | —   |

`StepperStep`: `{ id, label, sub?, meta?, state?: 'upcoming'\|'current'\|'done'\|'failed'\|'skipped' }`.
**Don't:** use for long-running task stages (use `ProgressLinear`); allow clicking into upcoming required steps.

### Actions

#### `Button` — `import { Button } from '@ui/Button'`

All actions. **Variants:** `size` `xs`·`sm`·`md`·`lg` · `tone` `primary`·`secondary`·`ghost`·`danger`·`link` · composition auto.

```tsx
<Button tone='primary' onClick={save}>
  Save
</Button>
```

| Prop                                 | Type                                                | Default     | Req                                         |
| ------------------------------------ | --------------------------------------------------- | ----------- | ------------------------------------------- |
| `tone`                               | `'primary'\|'secondary'\|'ghost'\|'danger'\|'link'` | `'primary'` | —                                           |
| `size`                               | `'xs' \| 'sm' \| 'md' \| 'lg'`                      | `'md'`      | —                                           |
| `composition`                        | `'text'\|'icon+text'\|'text+icon'\|'icon-only'`     | derived     | —                                           |
| `icon`                               | `ReactNode`                                         | —           | —                                           |
| `iconPlacement`                      | `'start' \| 'end'`                                  | `'start'`   | —                                           |
| `trailingAccent`                     | `ReactNode` (yellow-tile; _the_ page CTA only)      | —           | —                                           |
| `tooltip`                            | `ReactNode` (renders a `ds/Tooltip`)                | —           | —                                           |
| `tooltipPlacement`                   | `'top'\|'bottom'\|'left'\|'right'`                  | `'top'`     | —                                           |
| `loading` / `disabled` / `fullWidth` | `boolean`                                           | `false`     | —                                           |
| `type`                               | `'button' \| 'submit' \| 'reset'`                   | `'button'`  | —                                           |
| `href` / `target`                    | `string` (renders as a link)                        | —           | —                                           |
| `onClick`                            | `(e) => void`                                       | —           | —                                           |
| `aria-label`                         | `string`                                            | —           | — (required when `composition='icon-only'`) |

**Don't:** two primaries per surface; `danger` for cancel; introduce a "warning" tone; icon-only without `aria-label`; `link` tone for submit/destructive flows; `trailingAccent` with icon-only or link tone.

#### `DropdownMenu` — `import { DropdownMenu } from '@ui/DropdownMenu'`

Action menu (composes the shared overlay primitives). **Variants:** `align` `start`·`end` · `side` `bottom`·`top`·`left`·`right` · `size` `sm`·`md` · item `tone` `default`·`danger`.

```tsx
<DropdownMenu
  trigger={<Button tone='secondary'>Actions</Button>}
  items={[{ label: 'Edit', onSelect: edit }, { type: 'separator' }, { label: 'Delete', tone: 'danger', onSelect: del }]}
/>
```

| Prop                         | Type                               | Default         | Req |
| ---------------------------- | ---------------------------------- | --------------- | --- |
| `trigger`                    | `ReactElement`                     | —               | ✓   |
| `items`                      | `DropdownMenuItem[]`               | —               | ✓   |
| `align`                      | `'start' \| 'end'`                 | `'start'`       | —   |
| `side`                       | `'bottom'\|'top'\|'left'\|'right'` | `'bottom'`      | —   |
| `size`                       | `'sm' \| 'md'`                     | `'md'`          | —   |
| `minWidth`                   | `string \| number`                 | `200`           | —   |
| `itemsMaxHeight`             | `string \| number`                 | `'260px'`       | —   |
| `searchable`                 | `boolean`                          | `false`         | —   |
| `searchPlaceholder`          | `string`                           | `'Search…'`     | —   |
| `loading`                    | `boolean`                          | `false`         | —   |
| `onRefresh` / `refreshLabel` | `() => void` / `string`            | — / `'Refresh'` | —   |
| `headerActions`              | `ReactNode`                        | —               | —   |
| `onClose`                    | `() => void`                       | —               | —   |
| `className`                  | `string` (→ overlay root)          | —               | —   |
| `disablePortal`              | `boolean`                          | `false`         | —   |

`DropdownMenuItem` = action `{ label, onSelect (✓), icon?, description?, kbd?, tone?, disabled?, searchText? }` · `{ type: 'section', label }` · `{ type: 'separator' }`. `description` renders a dimmed second line under the label (two-line item) — e.g. a preset value or a short "what this does" hint.
**Don't:** > 7 items without sections; multi-step action behind an item (open a Modal); nest > 1 level.

#### `ThreeDotsMenu` — `import ThreeDotsMenu from '@ui/ThreeDotsMenu'` (default export, `.jsx`)

Kebab/overflow trigger backed by `DropdownMenu`. Preserves the legacy `menuItems[]` / `onMenuClick(item, data)` contract.

```tsx
<ThreeDotsMenu menuItems={menuItems} onMenuClick={(item, data) => handle(item, data)} />
```

| Prop          | Type                                            | Default | Req |
| ------------- | ----------------------------------------------- | ------- | --- |
| `menuItems`   | `{ id, label, icon?, disabled?, reactIcon? }[]` | `[]`    | —   |
| `onMenuClick` | `(item, data) => void`                          | —       | —   |

Submenus (legacy `subMenu`) are intentionally not carried over (DS caps nesting at one level).

### Feedback & overlays

#### `Banner` — `import { Banner } from '@ui/Banner'`

Page/section-level persistent message (one per surface, max). **Variants:** `tone` `info`·`success`·`warning`·`critical` · `surface` `page`·`section` · `actionsPlacement` `inline`·`below`.

```tsx
<Banner tone='warning' title='Quota almost full' message="You're at 90% of your node quota." />
```

| Prop               | Type                                       | Default   | Req |
| ------------------ | ------------------------------------------ | --------- | --- |
| `tone`             | `'info'\|'success'\|'warning'\|'critical'` | —         | ✓   |
| `message`          | `ReactNode`                                | —         | ✓   |
| `title`            | `ReactNode`                                | —         | —   |
| `actions`          | `BannerAction[]` (≤ 2)                     | —         | —   |
| `actionsPlacement` | `'inline' \| 'below'`                      | `'below'` | —   |
| `dismissible`      | `boolean`                                  | —         | —   |
| `onDismiss`        | `() => void`                               | —         | —   |
| `surface`          | `'page' \| 'section'`                      | `'page'`  | —   |

`BannerAction`: `{ label, onClick, tone?: 'secondary'\|'link' }`.
**Don't:** stack two banners on one surface; make a critical banner dismissible; > 2 actions; use for transient confirmations (that's `Toast`).

#### `Toast` — `import { toast } from '@ui/Toast'` (singleton) · `import Toast from '@ui/Toast'` (mount once)

Imperative transient notifications. Mount `<Toast />` once in `_app.tsx`. `toast` is also exported as `snackbar`.

```tsx
import { toast } from '@ui/Toast';

toast.success('Cluster saved.', { description: 'Changes applied.' });
```

**Methods:** `toast.default` · `toast.success` · `toast.info` · `toast.warning` · `toast.error`. Each: `(message, options?)` where `options` = `{ description?, action?: { label, onClick }, duration? }` (or a bare number = `duration`). Default durations: success 3000 · info 4000 · warning 5000 · error 6000 ms. See §1.6 / the Toast note in §1.

#### `Modal` — `import { Modal } from '@ui/Modal'`

Unified centered overlay — plain shell or confirm/cancel dialog. **Variants:** `width` `xs`·`sm`·`md`·`lg`·`xl`.

```tsx
<Modal open={open} handleClose={close} title='Delete workflow?' confirmText='Delete' onConfirm={del}>
  This action cannot be undone.
</Modal>
```

| Prop                                      | Type                                            | Default                  | Req |
| ----------------------------------------- | ----------------------------------------------- | ------------------------ | --- |
| `open`                                    | `boolean`                                       | —                        | ✓   |
| `handleClose` / `onClose`                 | `(event?, reason?) => void`                     | —                        | —   |
| `backdropClickClose`                      | `boolean` (blocks backdrop + Escape when false) | `true`                   | —   |
| `width`                                   | `'xs'\|'sm'\|'md'\|'lg'\|'xl'`                  | `'sm'`                   | —   |
| `maxHeight`                               | `string`                                        | —                        | —   |
| `title` / `subtitle`                      | `ReactNode` / `string`                          | —                        | —   |
| `rightComponentOnTitle`                   | `ReactNode`                                     | —                        | —   |
| `hideTitleBackground`                     | `boolean`                                       | `false`                  | —   |
| `children`                                | `ReactNode` (body)                              | —                        | —   |
| `additionalComponent`                     | `ReactNode` (outside DialogContent)             | —                        | —   |
| `loader`                                  | `boolean` (top bar + body blur)                 | `false`                  | —   |
| `onSuccess` / `message` / `icon` / `type` | success-state layout                            | `false` / `''` / — / `1` | —   |
| `actionButtons`                           | `ReactNode` (freeform footer)                   | —                        | —   |
| `actionButtonsFullBleed`                  | `boolean`                                       | `false`                  | —   |
| `confirmText`                             | `string` (renders Cancel + Confirm)             | —                        | —   |
| `onConfirm`                               | `() => void`                                    | —                        | —   |
| `confirmDisabled`                         | `boolean`                                       | `false`                  | —   |
| `isConfirmRequired` / `isCancelRequired`  | `boolean`                                       | `true`                   | —   |

See §2.2 for footer-mode selection and patterns. **Don't:** pass both `actionButtons` and `confirmText` (`actionButtons` wins); two primaries in the footer.

#### `Tooltip` — `import Tooltip from '@ui/Tooltip'` (default export)

Hover tooltip (V1 API preserved). **Variants:** `variant` `default`·`explainer`·`interactive`.

```tsx
<Tooltip title='Refresh interval' variant='explainer' desc='How often metrics reload.'>
  <InfoIcon />
</Tooltip>
```

| Prop                   | Type                                    | Default     | Req |
| ---------------------- | --------------------------------------- | ----------- | --- |
| `children`             | `ReactElement` (the trigger)            | —           | ✓   |
| `title`                | `ReactNode` (content / title)           | —           | ✓   |
| `variant`              | `'default'\|'explainer'\|'interactive'` | `'default'` | —   |
| `desc`                 | `ReactNode` (explainer/interactive)     | —           | —   |
| `placement`            | MUI placement                           | `'top'`     | —   |
| `linkUrl` / `linkText` | `string` (interactive variant)          | —           | —   |
| `disableFlip`          | `boolean`                               | `false`     | —   |

#### `EmptyState` — `import { EmptyState } from '@ui/EmptyState'`

Empty / no-data state. **Variants:** `size` `inline`·`section`·`page` · `illustration` `none`·`first-time`·`no-results`·`no-permissions`·`clear-skies` · `tone` `neutral`·`success` · `surface` `false`·`true`.

`surface` wraps the state in a **full-width, subtle-gray boxed panel** (`--ds-background-200` fill, hairline border) so it reads as a deliberate section rather than a gap — use it for a tab or section body's empty state (b-Cortex tabs, settings panels). Leave it off for empty states already inside a bordered `Card` or a table cell. When `action` is set it renders a **primary** `ds/Button`; pass `action.icon` for a leading glyph.

```tsx
<EmptyState title='No incidents in the last 7 days' illustration='clear-skies' tone='success' />

// Full-width panel with a primary CTA — e.g. a b-Cortex tab body
<EmptyState
  surface
  size='section'
  illustration='first-time'
  title='No knowledge bases found'
  description='Create a knowledge base to give the AI account-specific context.'
  action={{ label: 'Create Knowledge Base', onClick: handleCreate }}
/>
```

| Prop           | Type                                        | Default     | Req |
| -------------- | ------------------------------------------- | ----------- | --- |
| `title`        | `string`                                    | —           | ✓   |
| `description`  | `ReactNode`                                 | —           | —   |
| `size`         | `'inline' \| 'section' \| 'page'`           | `'section'` | —   |
| `illustration` | `EmptyStateIllustration`                    | `'none'`    | —   |
| `icon`         | `ReactNode` (overrides preset illustration) | —           | —   |
| `tone`         | `'neutral' \| 'success'`                    | `'neutral'` | —   |
| `surface`      | `boolean`                                   | `false`     | —   |
| `action`       | `{ label, onClick, icon? }`                 | —           | —   |

**Don't:** say "Empty"/"No data" — state what is empty and why; put two actions; render while loading (use `Skeleton`).

#### `Skeleton` — `import { Skeleton } from '@ui/Skeleton'`

Content-shaped loading placeholder. Presets: `Skeleton.TableRow`, `Skeleton.Card`, `Skeleton.ChatMessage`. **Variants:** `shape` `text`·`rect`·`circle` · `size` `caption`·`text`·`title`·`heading` (text only) · `animation` `shimmer`·`none`.

```tsx
<Skeleton.TableRow columns={5} />
```

| Prop               | Type                                                 | Default | Req |
| ------------------ | ---------------------------------------------------- | ------- | --- |
| `shape`            | `'text' \| 'rect' \| 'circle'`                       | —       | —   |
| `size`             | `'caption'\|'text'\|'title'\|'heading'` (text shape) | —       | —   |
| `width` / `height` | `number \| string`                                   | —       | —   |
| `animation`        | `'shimmer' \| 'none'`                                | —       | —   |

Presets: `Skeleton.TableRow { columns (✓), columnWidths?, rowHeight?, animation? }` · `Skeleton.Card { width?, height?, lines? }` · `Skeleton.ChatMessage { width?, lines? }`.
**Don't:** mismatch eventual content dimensions; render > 10 rows; use for slow operations (use `ProgressLinear`).

### AI / agentic

#### `SourceCitation` — `import { SourceCitation } from '@ui/SourceCitation'`

Inline attribution for an agent-generated claim. Always clickable, never dismissible. **Variants:** `source` registry (open-ended) · `composition` `name`·`name+timestamp`·`icon+name`·`icon+name+timestamp`·`number` · `size` `xs`·`sm`.

```tsx
<SourceCitation source='prometheus' timestamp={Date.now() - 120000} number={1} />
```

| Prop               | Type                                                          | Default | Req |
| ------------------ | ------------------------------------------------------------- | ------- | --- |
| `source`           | `SourceKey` (`prometheus`/`loki`/`k8s`/`aws`/… or any string) | —       | ✓   |
| `label`            | `string` (overrides registry label)                           | —       | —   |
| `timestamp`        | `Date \| number \| string`                                    | —       | —   |
| `number`           | `number` (footnote, for `composition='number'`)               | —       | —   |
| `composition`      | `SourceCitationComposition`                                   | auto    | —   |
| `size`             | `'xs' \| 'sm'`                                                | —       | —   |
| `href` / `onClick` | click target                                                  | —       | —   |

**Don't:** render an unsourced claim; tone by source health; deduplicate citations across a response.

#### `FeedbackVote` — `import FeedbackVote from '@ui/FeedbackVote'` (default export, `.jsx`)

Thumbs up/down feedback control.

```tsx
<FeedbackVote onFeedbackSubmit={submitFeedback} iconOnly />
```

| Prop               | Type                                           | Default | Req |
| ------------------ | ---------------------------------------------- | ------- | --- |
| `onFeedbackSubmit` | `(feedback) => void`                           | —       | ✓   |
| `sentFeedback`     | `{ submitted?, isPositive?, message? }`        | `{}`    | —   |
| `iconOnly`         | `boolean` (icon-only thumbs; aria-labels kept) | `false` | —   |

### `@shared` compositions referenced above (not `ds/` primitives)

These live under `@shared/*` and are kept here because §1–§3 reference them. Correct import paths:

| Component                | Import                                 | Note                                                                     |
| ------------------------ | -------------------------------------- | ------------------------------------------------------------------------ |
| `CustomTable`            | `@shared/tables/CustomTable`           | The app's table (own pagination via `CustomTablePagination`).            |
| `Tabs` (legacy)          | `@shared/navigation/Tabs`              | Widely-used tabs widget; prefer `ds/Tabs` for new work.                  |
| `AnchorComponent`        | `@shared/navigation/AnchorComponent`   | 2-level top-of-page nav.                                                 |
| `Form`                   | `@shared/forms/Form`                   | Form layout primitive (`.Section`/`.Field`/`.Row`/`.Actions`). See §2.3. |
| `Markdown` (`MarkDowns`) | `@shared/viewers/MarkDowns`            | Markdown/prose with fenced code.                                         |
| `CustomDropdown`         | `@shared/CustomDropdown`               | Cluster / cloud-account picker.                                          |
| `CustomDateTimePicker`   | `@shared/widgets/CustomDateTimePicker` | Single date+time field.                                                  |
| `CopyButton`             | `@shared/buttons/CopyButton`           | Copy-to-clipboard icon button.                                           |
| `DownloadButton`         | `@shared/buttons/DownloadButton`       | Download trigger (wraps `ds/Button` + `file-saver`).                     |
| `NBStatusBadge`          | `@shared/widgets/NBStatusBadge`        | K8s status badge; prefer `ds/StatusIndicator`/`ds/Label`.                |
| `CustomTextField`        | `@shared/forms/CustomTextField`        | **Legacy** → `ds/Input`.                                                 |
| `ErrorBoundary`          | `@shared/ErrorBoundary`                | Error boundary (no `ds/ErrorBoundary`).                                  |

**Removed (do not reach for these):** `CustomTabs`, `CustomTicketLink`, `BoxLayout2`, `NewCustomButton`,
`CustomTooltip`, `CustomTable2`, `CustomLabels`, and the never-shipped `ds/` placeholders `Table`/`TableCell`/`Pagination`/
`Autocomplete`/`DateRangePicker`/`Dialog`/`Popover`/`Inspector`/`DiffCard`/`StreamingIndicator`/
`ConfidenceIndicator`/`IntegrationBadge`/`Format`/`PageTabs`/`ConsoleOutput`/`MultiSelect`/`TextField`.

---

## 5. Maintenance

- §1–§3 track **patterns / decisions**; §4 tracks **per-component API**.
- When you change a `ds/*` primitive's public props/variants/Don't rules, update its §4 entry **and** its `app/design-system/primitives/**` spec in the same commit.
- Add a recipe to §2 the first time a multi-component view is built in a redesign PR.
- Add a row to §3 whenever a redesign surfaces a "which one do I use?" question.
- Each `ds/*` file keeps an "Anatomy" / "Don't" JSDoc block — that block is the deepest per-component source of truth this guide points at.

---

_End of guide._
