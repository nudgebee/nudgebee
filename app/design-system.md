# Nudgebee Typography Design System

> **Version:** 2.0 (CSS Utility Classes)
> **Last Updated:** January 2026
> **Import:** `@import 'src/styles/globaltext.css';`

---

## Overview

This typography system provides CSS utility classes extracted from and recommended for the Nudgebee observability platform. All classes follow the naming convention `nb-text-[category]-[variant]`.

---

## Typography Coverage Summary

### Extracted from Codebase ✅

| Category            | Classes Count | Instances Found |
| ------------------- | ------------- | --------------- |
| Body Text           | 4             | ~880            |
| Headings (H1)       | 3             | ~160            |
| Headings (H2)       | 3             | ~150            |
| Headings (H3)       | 3             | ~770            |
| Subtext             | 3             | ~340            |
| Table               | 5             | ~200            |
| Labels              | 4             | ~150            |
| **Total Extracted** | **25**        | **~2,650**      |

### Recommended Additions 🆕

| Category              | Classes Count | Priority | Rationale                    |
| --------------------- | ------------- | -------- | ---------------------------- |
| Metrics & Numbers     | 6             | High     | Core dashboard functionality |
| Status & Alerts       | 5             | High     | Alert management essential   |
| Timestamps            | 3             | High     | Time-series data display     |
| Code & Technical      | 6             | Medium   | Log/trace viewing            |
| Charts & Legends      | 5             | Medium   | Data visualization           |
| Navigation            | 5             | Medium   | UI consistency               |
| Badges & Tags         | 3             | Medium   | Visual polish                |
| Tooltips & Hints      | 4             | Low      | UX enhancement               |
| Empty & Error States  | 4             | Low      | Edge case handling           |
| **Total Recommended** | **41**        |          |                              |

---

## Implementation Priority Guide

1. **High Priority:** Implement immediately - core observability features depend on these
2. **Medium Priority:** Implement in next sprint - improves data visualization and navigation
3. **Low Priority:** Implement when polishing UI - enhances overall user experience

---

## Body Text

### Body Primary ⭐ RECOMMENDED

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~880 instances found
**CSS Class:** `nb-text-body-primary`
**When to Use:** Main content, articles, descriptions, paragraphs

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 13px |
| Font Weight | 400 (normal) |
| Line Height | 1.4 |
| Default Color | #374151 |

**Color Variations:**
| Modifier Class | Color | Use Case |
|----------------|-------|----------|
| (none/default) | #374151 | Standard text |
| `nb-text-color-muted` | #9F9F9F | Secondary information |
| `nb-text-color-accent` | #3B82F6 | Emphasized text |

**Usage Examples:**

```html
<p class="nb-text-body-primary">Default body text content</p>
<p class="nb-text-body-primary nb-text-color-muted">Muted secondary text</p>
<span class="nb-text-body-primary nb-text-color-accent">Highlighted text</span>
```

**When NOT to Use:** Headers, labels, metric values, table headers

---

### Body Compact

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~685 instances found
**CSS Class:** `nb-text-body-compact`
**When to Use:** Dense UI areas, sidebars, compact lists

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 12px |
| Font Weight | 400 |
| Line Height | 1.4 |
| Default Color | #374151 |

**Usage Examples:**

```html
<p class="nb-text-body-compact">Compact body text for dense layouts</p>
```

---

### Body Large

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~635 instances found
**CSS Class:** `nb-text-body-large`
**When to Use:** Emphasized content, introductions, callouts

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 14px |
| Font Weight | 400 |
| Line Height | 1.4 |
| Default Color | #374151 |

---

## Headings

### H1 - Page Header ⭐ RECOMMENDED

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~164 instances found
**CSS Class:** `nb-text-h1-page`
**When to Use:** Main page titles (use once per page)

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 20px |
| Font Weight | 600 (semibold) |
| Line Height | 1.2 |
| Default Color | #374151 |

**Variants:**
| Class | Description |
|-------|-------------|
| `nb-text-h1-page` | Standard page header |
| `nb-text-h1-page-alt` | Bold variant with title color (#22304B) |
| `nb-text-h1-sub` | 18px sub-header |

**Usage Examples:**

```html
<h1 class="nb-text-h1-page">Dashboard Overview</h1>
<h1 class="nb-text-h1-page-alt">Important Page Title</h1>
<h2 class="nb-text-h1-sub">Page subtitle or description</h2>
```

---

### H2 - Section Header

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~146 instances found
**CSS Class:** `nb-text-h2-section`
**When to Use:** Major content sections within a page

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 16px |
| Font Weight | 600 |
| Line Height | 1.3 |
| Default Color | #374151 |

**Variants:**
| Class | Description |
|-------|-------------|
| `nb-text-h2-section` | Standard section header |
| `nb-text-h2-section-alt` | Bold with title color |
| `nb-text-h2-sub` | 14px sub-header |

---

### H3 - Widget Header ⭐ MOST COMMON

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~770 instances found (most common header)
**CSS Class:** `nb-text-h3-widget`
**When to Use:** Card/widget headers, compact headers

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 14px |
| Font Weight | 600 |
| Line Height | 1.4 |
| Default Color | #374151 |

---

## Subtext

### Subtext Primary

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~340 instances found
**CSS Class:** `nb-text-subtext-primary`
**When to Use:** Captions, helper text, metadata

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 12px |
| Font Weight | 400 |
| Line Height | 1.4 |
| Default Color | #737373 |

**Variants:**
| Class | Size | Color |
|-------|------|-------|
| `nb-text-subtext-primary` | 12px | #737373 |
| `nb-text-subtext-secondary` | 11px | #9F9F9F |
| `nb-text-subtext-tertiary` | 10px | #B9B9B9 |

---

## Table Text

### Table Header

**Status:** ✅ Extracted from codebase
**Usage Frequency:** ~200 instances found
**CSS Class:** `nb-text-table-header`
**When to Use:** Column headers in data tables

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 12px |
| Font Weight | 600 |
| Line Height | 1.4 |
| Default Color | #737373 |

**Variants:**
| Class | Description |
|-------|-------------|
| `nb-text-table-header` | Standard header |
| `nb-text-table-header-caps` | Uppercase with letter spacing |

---

### Table Row Primary ⭐ RECOMMENDED

**Status:** ✅ Extracted from codebase
**CSS Class:** `nb-text-table-row-primary`
**When to Use:** Main table cell content

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 13px |
| Font Weight | 400 |
| Line Height | 1.4 |
| Default Color | #374151 |

**Usage Examples:**

```html
<table>
  <thead>
    <tr>
      <th class="nb-text-table-header">Service</th>
      <th class="nb-text-table-header nb-text-align-right">Latency</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td class="nb-text-table-row-primary">api-gateway</td>
      <td class="nb-text-table-row-primary nb-text-align-right">142ms</td>
    </tr>
  </tbody>
</table>
```

---

## Labels

### Label Key

**Status:** ✅ Extracted from codebase
**CSS Class:** `nb-text-label-key`
**When to Use:** Form labels, key-value pair labels

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 12px |
| Font Weight | 500 |
| Line Height | 1.4 |
| Default Color | #737373 |

### Label Value

**Status:** ✅ Extracted from codebase
**CSS Class:** `nb-text-label-value`
**When to Use:** Data values in key-value displays

**Usage Examples:**

```html
<div class="key-value-pair">
  <span class="nb-text-label-key">Status:</span>
  <span class="nb-text-label-value">Active</span>
</div>
```

---

## Metrics & Numbers 🆕

### Metric Value

**Status:** 🆕 Recommended addition
**CSS Class:** `nb-text-metric-value`
**When to Use:** Large numeric values on dashboards, KPI cards

**Recommended Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 24px |
| Font Weight | 600 |
| Line Height | 1.2 |
| Default Color | #374151 |

**Variants:**
| Class | Size | Use Case |
|-------|------|----------|
| `nb-text-metric-value-sm` | 14px | Compact metrics |
| `nb-text-metric-value` | 24px | Standard metrics |
| `nb-text-metric-value-lg` | 28px | Hero/featured metrics |

**Color Variations:**
| Modifier Class | Color | Use Case |
|----------------|-------|----------|
| (none/default) | #374151 | Neutral metric |
| `nb-text-color-positive` | #16A34A | Positive change (+12.5%) |
| `nb-text-color-negative` | #DC2626 | Negative change (-8.3%) |
| `nb-text-color-currency` | #22C55E | Dollar amounts |

**Rationale:** Essential for observability dashboards displaying request counts, error rates, latency percentiles, throughput metrics.

**Usage Examples:**

```html
<div class="metric-card">
  <span class="nb-text-metric-label">Requests/sec</span>
  <span class="nb-text-metric-value">1,234,567</span>
  <span class="nb-text-metric-unit nb-text-color-muted">/s</span>
</div>

<div class="metric-card">
  <span class="nb-text-metric-label">Change</span>
  <span class="nb-text-metric-delta nb-text-color-positive">+12.5%</span>
</div>

<div class="metric-card">
  <span class="nb-text-metric-label">Savings</span>
  <span class="nb-text-metric-currency-lg">$24,500</span>
</div>
```

**Related Classes:** `nb-text-metric-label`, `nb-text-metric-unit`, `nb-text-metric-delta`, `nb-text-metric-currency`

---

## Status & Alerts 🆕

### Status Critical

**Status:** 🆕 Recommended addition
**CSS Class:** `nb-text-status-critical`
**When to Use:** Critical alerts, error states, failures

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 12px |
| Font Weight | 600 |
| Line Height | 1.4 |
| Default Color | #EF4444 |

**All Status Classes:**
| Class | Color | Use Case |
|-------|-------|----------|
| `nb-text-status-critical` | #EF4444 | Critical/Error |
| `nb-text-status-warning` | #EAB308 | Warning |
| `nb-text-status-healthy` | #16A34A | OK/Success |
| `nb-text-status-info` | #3B82F6 | Informational |
| `nb-text-status-unknown` | #9F9F9F | Unknown/Pending |

**Usage Examples:**

```html
<div class="alert-row">
  <span class="nb-text-status-critical">CRITICAL</span>
  <span class="nb-text-body-primary">Database connection timeout</span>
</div>
```

---

## Code & Technical 🆕

### Code Inline

**Status:** 🆕 Recommended addition
**CSS Class:** `nb-text-code-inline`
**When to Use:** Inline code snippets, technical values

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto Mono, monospace |
| Font Size | 13px |
| Font Weight | 400 |
| Line Height | 1.4 |
| Default Color | #313131 |
| Background | #f5f5f5 |
| Padding | 2px 6px |
| Border Radius | 4px |

**All Code Classes:**
| Class | Use Case |
|-------|----------|
| `nb-text-code-inline` | Inline code in sentences |
| `nb-text-code-block` | Multi-line code blocks |
| `nb-text-log-entry` | Log viewer lines |
| `nb-text-trace-id` | Distributed trace IDs |
| `nb-text-span-name` | Trace span names |
| `nb-text-query` | Database/API queries |

**Usage Examples:**

```html
<p>
  Set the environment variable
  <code class="nb-text-code-inline">NODE_ENV=production</code>
</p>

<div class="log-line">
  <span class="nb-text-timestamp-compact">14:32:01.234</span>
  <span class="nb-text-status-warning">WARN</span>
  <span class="nb-text-log-entry">Connection pool exhausted</span>
</div>
```

---

## Timestamps 🆕

### Timestamp Primary

**Status:** 🆕 Recommended addition
**CSS Class:** `nb-text-timestamp-primary`
**When to Use:** Full timestamps, event times

**Specifications:**
| Property | Value |
|----------|-------|
| Font Family | Roboto, sans-serif |
| Font Size | 12px |
| Font Weight | 400 |
| Line Height | 1.4 |
| Default Color | #B9B9B9 |

**All Timestamp Classes:**
| Class | Use Case | Example |
|-------|----------|---------|
| `nb-text-timestamp-primary` | Full timestamp | "Jan 15, 2025, 14:32:01" |
| `nb-text-timestamp-relative` | Relative time | "5 mins ago" |
| `nb-text-timestamp-compact` | Log timestamp | "14:32:01.234" |

---

## Charts & Legends 🆕

### Chart Title

**Status:** 🆕 Recommended addition
**CSS Class:** `nb-text-chart-title`
**When to Use:** Graph/chart headings

**All Chart Classes:**
| Class | Size | Use Case |
|-------|------|----------|
| `nb-text-chart-title` | 16px | Chart headings |
| `nb-text-chart-axis` | 11px | X/Y axis labels |
| `nb-text-chart-legend` | 12px | Legend items |
| `nb-text-chart-annotation` | 10px | Annotations |
| `nb-text-chart-data` | 11px | Data point labels |

---

## Empty & Error States 🆕

### Empty Title

**Status:** 🆕 Recommended addition
**CSS Class:** `nb-text-empty-title`
**When to Use:** "No data" headings, empty state titles

**All State Classes:**
| Class | Use Case |
|-------|----------|
| `nb-text-empty-title` | Empty state headings |
| `nb-text-empty-description` | Empty state explanations |
| `nb-text-error-title` | Error headings |
| `nb-text-error-message` | Error descriptions |

**Usage Examples:**

```html
<div class="empty-state">
  <h3 class="nb-text-empty-title">No data available</h3>
  <p class="nb-text-empty-description">Data will appear here once services start reporting metrics.</p>
</div>
```

---

## Color Modifier Classes

Apply these as additional classes to change text color:

| Modifier | Class                    | Hex     | Use Case                  |
| -------- | ------------------------ | ------- | ------------------------- |
| Default  | `nb-text-color-default`  | #374151 | Standard text             |
| Muted    | `nb-text-color-muted`    | #9F9F9F | Secondary, less important |
| Accent   | `nb-text-color-accent`   | #3B82F6 | Highlighted, links        |
| Positive | `nb-text-color-positive` | #16A34A | Success, improvements     |
| Negative | `nb-text-color-negative` | #DC2626 | Errors, declines          |
| Warning  | `nb-text-color-warning`  | #EAB308 | Caution states            |
| Info     | `nb-text-color-info`     | #3B82F6 | Informational             |
| White    | `nb-text-color-white`    | #FFFFFF | On dark backgrounds       |
| Title    | `nb-text-color-title`    | #22304B | Dark title color          |
| Currency | `nb-text-color-currency` | #22C55E | Money/savings             |

---

## Weight Modifier Classes

| Modifier | Class                     | Value |
| -------- | ------------------------- | ----- |
| Light    | `nb-text-weight-light`    | 300   |
| Normal   | `nb-text-weight-normal`   | 400   |
| Medium   | `nb-text-weight-medium`   | 500   |
| Semibold | `nb-text-weight-semibold` | 600   |
| Bold     | `nb-text-weight-bold`     | 700   |

---

## Utility Classes

| Class                   | Effect                  |
| ----------------------- | ----------------------- |
| `nb-text-truncate`      | Truncate with ellipsis  |
| `nb-text-nowrap`        | Prevent text wrapping   |
| `nb-text-uppercase`     | Transform to uppercase  |
| `nb-text-capitalize`    | Capitalize first letter |
| `nb-text-align-left`    | Left align text         |
| `nb-text-align-center`  | Center align text       |
| `nb-text-align-right`   | Right align text        |
| `nb-text-underline`     | Add underline           |
| `nb-text-no-decoration` | Remove decoration       |

---

## CSS Variables Reference

All typography classes use CSS custom properties for consistency:

```css
:root {
  /* Font Families */
  --nb-font-primary: 'Roboto', sans-serif;
  --nb-font-secondary: 'Poppins', sans-serif;
  --nb-font-mono: 'Roboto Mono', monospace;

  /* Font Sizes */
  --nb-text-xs: 10px;
  --nb-text-sm: 11px;
  --nb-text-base: 12px;
  --nb-text-md: 13px;
  --nb-text-lg: 14px;
  --nb-text-xl: 16px;
  --nb-text-2xl: 18px;
  --nb-text-3xl: 20px;
  --nb-text-4xl: 24px;
  --nb-text-5xl: 28px;

  /* Font Weights */
  --nb-font-light: 300;
  --nb-font-normal: 400;
  --nb-font-medium: 500;
  --nb-font-semibold: 600;
  --nb-font-bold: 700;
}
```

---

## Migration from Components

If migrating from the previous component-based system:

| Old Component       | New CSS Class               |
| ------------------- | --------------------------- |
| `<NbBodyText>`      | `nb-text-body-primary`      |
| `<NbBodyTextLarge>` | `nb-text-body-large`        |
| `<NbHeaderH1>`      | `nb-text-h1-page`           |
| `<NbHeaderH2>`      | `nb-text-h2-section`        |
| `<NbHeaderH3>`      | `nb-text-h3-widget`         |
| `<NbSubText>`       | `nb-text-subtext-primary`   |
| `<NbTableHeader>`   | `nb-text-table-header`      |
| `<NbTableCell>`     | `nb-text-table-row-primary` |
| `<NbLabel>`         | `nb-text-label-key`         |
| `<NbValue>`         | `nb-text-label-value`       |
| `<NbCurrency>`      | `nb-text-metric-currency`   |

---

## Components

> **Source:** `app/src/components1/common/` > **Total Files:** 110+ across root and subdirectories
> **Last Audited:** February 2026

### Complete File Inventory

Before documenting, every file in the `common/` folder was scanned. Here is the full list of files found:

<details>
<summary>Click to expand full file list (110+ files)</summary>

**Root .tsx files (27):** AccordionSmall, AutoRefreshControls, ButtonTabs, ConsoleLogOutput, CustomDropdownIcon, CustomListWithShowMore, CustomMultiDropdown, CustomStepper, CustomTextField, CustomTooltip, DevOpsTimelineMUI, DownloadTarFile, FieldRenderer, FilterGroup, LazyLoadComponent, Loader, NSnackbar, NewVerticalStepper, NubiChatSidebar, SnackbarComponent, SvgRenderer, TimePickerButtonsGroup, ValueWithHeading, VerticalStepNavigation

**Root .jsx files (76):** AnchorComponent, ApiTokens, ArgoCDAccountModal, BoxLayout2, ButtonMenu, ChartSwitcher, CloudIcon, CloudProviderIcon, ClusterDropDown, ClusterStatusIndicator, CopyButton, CopyableText, CostView, CreateTicketButton, CustomAccordion, CustomAutocomplete, CustomBackButton, CustomBorderCard, CustomButton, CustomButtonsGroup, CustomCheckbox, CustomCollapseable, CustomDivider, CustomDropdown, CustomIcon, CustomLink, CustomPill, CustomSearch, CustomSelectDropdown, CustomSwiperCarousel, CustomSwitch, CustomTableFilters, CustomTabs, CustomTabsForDrilldown, CustomTicketLink, DatadogAccountModal, DiffViewer, DownloadButton, DynamicForm, DynamicTitle, EmptyData, ExpandButton, ExpandableText, GithubAccountModal, InfographicList, IntegrationDynamicFormModal, InvestigateButton, JiraAccountModal, K8sAccountModal, LangTypeIcon, MarkDowns, NewCustomButton, NewReusabeFormComponents, NewShimmerloading, OptionMenu, PagerDutyAccountModal, PrimaryLink, ResolveButton, SecondaryLink, ServiceNowAccountModal, ShareButton, ShimmerLoading, SmallScreenBackdrop, SummarySkeletonLoader, TenantAccountCommonSettings, TenantSettings, TextWithBorder, TextWithToolTip, TextWithTooltipAndCopy, ThreeDotsMenu, ThumpsUpAndDown, Title, UpdateDataContext, UserHistory, WidgetCard, index

**Root .js files (4):** ThreeDotLoader, TitleBox, ScrollToTopBottom, ServiceNowAccountModal

**Root .ts files (1):** snackbarService

**Root .d.ts files (1):** BoxLayout2.d.ts

**Subdirectory files:**

- `charts/` (7): BarChart, ChartComponent, CustomHeatMap, DoughnutChart, DoughnutChartK8s, LineCharts, ShowPrometheusLineChart
- `format/` (5): Currency, Datetime, Memory, Number, Text
- `widgets/` (7): ColorDots, CustomDateTimePicker, CustomLabels, HighLights, ProgressBar, SummaryLabels, TrendArrowPercentage
- `modal/` (3): NDialog, style, welcomeModal
- `tables/` (1): TableCellValueWithHeading
- `header/` (1): Header1
- `layout/` (2): AskNudgebeeLayoutV2, UserMenuItems
- `inputs/` (1): AutoCompleteInput

</details>

---

### Barrel Exports (index.jsx)

The `index.jsx` re-exports these components for convenient importing:

```js
export { default as FilterGroup } from './FilterGroup';
export { default as NSnackbar } from './NSnackbar';
export { default as BoxLayout2 } from './BoxLayout2';
export { default as CustomButton } from './CustomButton';
export { default as ThreeDotsMenu } from './ThreeDotsMenu';
export { default as LineChart } from './charts/LineCharts';
export { default as Text } from './format/Text';
```

---

### Layout

#### BoxLayout2

- **Path:** `src/components1/common/BoxLayout2.jsx`
- **Description:** Complex layout container with optional heading, filters, date-time picker, sharing/download buttons, toggle buttons, refresh control, and side filter panel.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | id | string | No | - | Element ID |
  | showBorder | bool | No | `true` | Show card border |
  | heading | string | No | `''` | Section heading text |
  | marginTop | number \| string | No | `0` | Top margin |
  | marginBottom | number \| string | No | `'24px'` | Bottom margin |
  | children | ReactNode | No | - | Content |
  | filterOptions | FilterOption[] | No | `[]` | Array of filter configs |
  | dateTimeRange | DateTimeRange | No | `{enabled:false}` | Date-time range picker config |
  | sharingOptions | SharingOptions | No | `{sharing:{enabled:true}, download:{enabled:true}}` | Share/download buttons config |
  | toggleButtons | ToggleButtons | No | `{options:[]}` | Toggle button group config |
  | displaySideFilters | bool | No | `false` | Show side filter panel |
  | onRefresh | OnRefresh | No | `{enabled:false}` | Refresh button config |
  | sx | object | No | `{}` | Style overrides |
- **When to use:** Primary layout wrapper for dashboard sections. Use whenever you need a section with a heading, filters, and/or date range controls.
- **Dependencies:** Wraps many common components internally (CustomSearch, CustomButtonsGroup, DownloadButton, ShareButton, etc.)
- **Example:**

```tsx
<BoxLayout2
  id='metrics-section'
  heading='Service Metrics'
  dateTimeRange={{ enabled: true, onChange: handleDateChange }}
  filterOptions={[{ type: 'search', onChange: handleSearch }]}
>
  <MetricsChart />
</BoxLayout2>
```

#### AskNudgebeeLayoutV2

- **Path:** `src/components1/common/layout/AskNudgebeeLayoutV2.jsx`
- **Description:** Full-page layout for the "Ask Nudgebee" AI chat interface with side drawer navigation, user menu, settings, and tenant switching.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | node | Yes | - | Page content |
  | handleNewChat | func | No | - | New chat callback |
  | handleHomePage | func | No | - | Home navigation callback |
  | handleRecentChat | func | No | - | Recent chat callback |
  | handleToggle | func | No | - | Drawer toggle callback |
  | onAgentsRefreshed | func | No | - | Agent refresh callback |
- **When to use:** Layout wrapper for the AI assistant pages only.
- **Dependencies:** Wraps with `withAuth` HOC. Uses `TenantSettings`, `ApiTokens`, `CustomButton`.

#### SmallScreenBackdrop

- **Path:** `src/components1/common/SmallScreenBackdrop.jsx`
- **Description:** Fullscreen blurred backdrop warning when viewport is below 968px.
- **Props:** None
- **When to use:** Include in the root layout to warn users on small screens.
- **Dependencies:** MUI `Backdrop`, `useMediaQuery`

#### CustomDivider

- **Path:** `src/components1/common/CustomDivider.jsx`
- **Description:** Configurable horizontal divider line.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | margin | string | No | `'10px 0px'` | CSS margin |
  | borderWidth | string | No | `'0.5px'` | Border thickness |
  | borderType | string | No | `'solid'` | Border style |
  | maxWidth | string | No | `'auto'` | Max width |
  | borderColor | string | No | `'#EBEBEB'` | Border color |
- **When to use:** Use instead of raw `<hr>` or MUI `<Divider>` for consistent styling.
- **Example:**

```tsx
<CustomDivider margin='16px 0' borderColor='#D1D5DB' />
```

---

### Typography & Text Display

#### Title

- **Path:** `src/components1/common/Title.jsx`
- **Description:** Section title text with an optional colored underline bar.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | title | any | No | - | Title text |
  | isUnderline | bool | No | `true` | Show underline bar |
  | lightVariant | bool | No | `false` | Lighter, larger style |
  | sx | any | No | - | Style overrides |
- **Variants:** `lightVariant` toggles between bold dark and lighter larger text.

#### TitleBox

- **Path:** `src/components1/common/TitleBox.js`
- **Description:** Styled card header with title, optional icon, subtitle key-value pairs, and a right-side component.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | title | string | No | `''` | Header title |
  | originalTitle | string | No | - | Full title for tooltip |
  | startIcon | node | No | - | Icon before title |
  | rightComponent | node | No | - | Right-side content |
  | subTitleOptions | array | No | - | Array of {key, value, valueColor?, action?} |

#### DynamicTitle

- **Path:** `src/components1/common/DynamicTitle.jsx`
- **Description:** Sets the page `<title>` and favicon dynamically based on the tenant name from user session.
- **Props:** None
- **When to use:** Include once in the app layout for dynamic browser tab titles.

#### TextWithToolTip

- **Path:** `src/components1/common/TextWithToolTip.jsx`
- **Description:** Truncated text with a MUI Tooltip showing full text on hover.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | text | string | No | - | Text content |
  | size | number | No | `30` | Character limit before truncation |
  | enableTooltip | bool | No | `true` | Enable/disable tooltip |

#### TextWithTooltipAndCopy

- **Path:** `src/components1/common/TextWithTooltipAndCopy.jsx`
- **Description:** Truncated text with tooltip and copy-to-clipboard icon.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | string | No | - | Text to display |
  | maxSize | number | No | `30` | Truncation limit |
  | showCopyIcon | bool | No | `true` | Show copy icon |
  | tooltipPlacement | string | No | `'top'` | Tooltip position |
  | copyIconSize | number | No | `12` | Copy icon size |

#### TextWithBorder

- **Path:** `src/components1/common/TextWithBorder.jsx`
- **Description:** Displays text with a left colored border, optional release icon badge.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | string | No | `''` | Text content |
  | borderColor | string | No | `''` | Left border color |
  | borderWidth | string | No | `''` | Left border width |
  | padding | string | No | `'0px 10px'` | Padding |
  | fontSx | object | No | `{fontSize:'20px', fontWeight:'700'}` | Font styling |

#### ExpandableText

- **Path:** `src/components1/common/ExpandableText.jsx`
- **Description:** Text block that clamps to N lines with a "Show More/Less" toggle, auto-detecting overflow via ResizeObserver.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | text | string | No | `''` | Text content |
  | maxLines | number | No | `1` | Lines before clamping |
  | sx | object | No | `{}` | Style overrides |
  | secondaryText | bool | No | - | Lighter text style |
  | color | string | No | - | Text color |

#### MarkDowns

- **Path:** `src/components1/common/MarkDowns.jsx`
- **Description:** Renders markdown content as sanitized HTML with mermaid diagram support, copy-to-clipboard on code blocks, and optional run-code buttons.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | string | No | - | Markdown string |
  | sx | object | No | - | Style overrides |
  | allowExecutable | func | No | - | Callback for run-code |
  | canRunCode | bool | No | `true` | Show run-code buttons |
- **Dependencies:** `marked`, `dompurify`, `mermaid`

#### ValueWithHeading

- **Path:** `src/components1/common/ValueWithHeading.tsx`
- **Description:** Labeled value display with optional color dot icon, used for cost summaries and workload stats.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | iconColor | string | No | - | Color dot indicator |
  | heading | string | Yes | `''` | Label text |
  | value | string \| number | No | `''` | Value to display |
  | forCostSummary | bool | No | - | Cost summary layout mode |
  | forWorkload | bool | No | - | Workload layout mode |
  | hideLogo | bool | No | `false` | Hide the color dot |

---

### Navigation

#### AnchorComponent

- **Path:** `src/components1/common/AnchorComponent.jsx`
- **Description:** Top-level navigation bar with tabbed buttons, dropdown sub-tabs via popover, and URL routing.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | filterOptions | array | No | `[]` | Tab filter options |
  | onChangeFilter | func | No | - | Filter change callback |
  | buttonTitle | string | No | `''` | Action button text |
  | handleButtonAction | func | No | - | Button click handler |
  | showGroupedTabs | bool | No | `false` | Enable grouped tab layout |
  | p | string | No | `'8px 32px 0px 32px'` | Padding |
- **When to use:** Use for page-level tab navigation with URL routing.
- **Dependencies:** Uses `CustomTabs`, `CustomPill`, `CustomIconButton`.

#### CustomTabs

- **Path:** `src/components1/common/CustomTabs.jsx`
- **Description:** Styled tab navigation bar with router-based or filter-based behavior, pill counts, icons, and grouped tabs.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | No | - | Active tab value |
  | onChange | func | Yes | - | Tab change handler |
  | options | object | Yes | `{}` | Tab definitions |
  | variant | `'primary'` \| `'secondary'` | No | `'secondary'` | Visual style |
  | behavior | `'router'` \| `'filter'` | No | `'router'` | Navigation mode |
  | smallSize | bool | No | `false` | Compact tabs |
  | blueVariant | bool | No | `false` | Blue indicator color |
  | showGroupedTabs | bool | No | `false` | Grouped tabs layout |
- **Variants:** `primary` (filled active tab), `secondary` (underline). Behavior: `router` (Link navigation), `filter` (callback only).
- **Example:**

```tsx
<CustomTabs
  value={activeTab}
  onChange={handleTabChange}
  options={{ overview: { text: 'Overview' }, metrics: { text: 'Metrics', count: 5 } }}
  variant='primary'
  behavior='filter'
/>
```

#### CustomTabsForDrilldown

- **Path:** `src/components1/common/CustomTabsForDrilldown.jsx`
- **Description:** Scrollable tab bar with pill counts, alpha/beta icons, and optional right-side action button.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | number \| string | No | - | Active tab |
  | onChange | func | Yes | - | Tab change handler |
  | options | array | No | `[]` | Tab configs with text, count, icon, disabled |
  | rightButton | object | No | - | Right-side action button config |
  | smallSize | bool | No | `false` | Compact mode |
  | blueVariant | bool | No | `false` | Blue indicator |

#### ButtonTabs

- **Path:** `src/components1/common/ButtonTabs.tsx`
- **Description:** Horizontal toggle button group (tab-like) with customizable styles and active state tracking.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | buttons | ButtonConfig[] | Yes | - | Array of {id, label, value?} |
  | callBack | func | Yes | - | Selection callback |
  | selectedButton | string \| number | No | - | Active button ID |
  | fontSize | string | No | `'14px'` | Font size |
  | height | string \| number | No | `'31px'` | Button height |
  | borderRadius | string | No | `'6px'` | Corner radius |
- **Example:**

```tsx
<ButtonTabs
  buttons={[
    { id: 'day', label: 'Day' },
    { id: 'week', label: 'Week' },
  ]}
  selectedButton='day'
  callBack={(id) => setTimeRange(id)}
/>
```

#### CustomBackButton

- **Path:** `src/components1/common/CustomBackButton.jsx`
- **Description:** Back navigation button with optional new icon style; supports custom onClick, path, or browser back.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onClick | func | No | - | Custom click handler |
  | useNewIcon | bool | No | `false` | New styled icon variant |
  | backButtonPath | string | No | - | Explicit back path |

#### ScrollToTopBottom

- **Path:** `src/components1/common/ScrollToTopBottom.js`
- **Description:** Fixed floating buttons to scroll the page to top or bottom.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | alwaysShowBottomArrow | bool | No | `false` | Always show down arrow |
- **Dependencies:** `react-icons/fa` (FaArrowUp, FaArrowDown)

#### Header1

- **Path:** `src/components1/common/header/Header1.jsx`
- **Description:** Main application header bar with dynamic page title, cluster dropdown, agent version alerts, connect-account menus, and Nubi AI assistant button.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | showBorder | bool | No | `false` | Show bottom border |
- **When to use:** Main app header. Included once in the root layout.

---

### Data Display

#### CustomLabels

- **Path:** `src/components1/common/widgets/CustomLabels.tsx`
- **Description:** Status/tag label chip that auto-selects color based on text content (e.g., "error" → red, "active" → green) or explicit variant.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | text | string | Yes | - | Label text |
  | variant | string | No | `''` | Force color: `'red'` \| `'green'` \| `'grey'` \| `'yellow'` \| `'orange'` \| `'criticalRed'` \| `'blue'` |
  | height | string | No | `'20px'` | Label height |
  | textTransform | string | No | `'capitalize'` | CSS text-transform |
  | maxWidth | string | No | `'350px'` | Max width |
  | displayTooltip | bool | No | `false` | Show tooltip on truncation |
  | tooltipCharLimit | number | No | - | Chars before truncation |
  | showDropdownArrow | bool | No | `false` | Show dropdown arrow |
- **Auto-color mapping:** `error/firing/failed` → red, `active/resolved/success` → green, `pending/in progress` → yellow, `critical` → criticalRed, `low` → blue, else → grey
- **Example:**

```tsx
<CustomLabels text="Active" />           {/* auto: green */}
<CustomLabels text="Error" />            {/* auto: red */}
<CustomLabels text="Custom" variant="blue" />
```

#### CustomPill

- **Path:** `src/components1/common/CustomPill.jsx`
- **Description:** Small badge/pill label with customizable colors, border, and tooltip; caps display at "99+".
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | node | Yes | - | Badge content |
  | bgColor | string | No | primaryLightest | Background color |
  | borderRadius | string | No | `'4px'` | Corner radius |
  | font | string | No | `'12px'` | Font size |
  | showBorder | bool | No | `false` | Show border |
  | tooltip | string | No | `''` | Tooltip text |
- **Example:**

```tsx
<CustomPill value={42} bgColor='#EFF6FF' color='#3B82F6' />
```

#### CopyableText

- **Path:** `src/components1/common/CopyableText.jsx`
- **Description:** Wraps content with a click-to-copy icon, supports markdown format and hover-reveal icon.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | any | No | - | Display content |
  | copyableText | string | No | `''` | Text to copy |
  | iconPosition | `'start'` \| `'end'` | No | `'start'` | Icon placement |
  | format | string | No | `'text'` | `'text'` or `'markdown'` |
  | showCopyIconOnHover | bool | No | `false` | Show icon only on hover |

#### InfographicList

- **Path:** `src/components1/common/InfographicList.jsx`
- **Description:** Horizontal bar displaying a sequence of label-value pairs separated by dividers.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | sequence | array | No | - | Array of {text, value} |

#### CostView

- **Path:** `src/components1/common/CostView.jsx`
- **Description:** Displays a row of cost entries with currency formatting and trend arrow for forecast.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | array | No | - | Array of {name, cost} |
- **Composition:** Uses `TrendArrowPercentage`, `Currency`.

#### ClusterStatusIndicator

- **Path:** `src/components1/common/ClusterStatusIndicator.jsx`
- **Description:** Colored dot indicator (green/yellow/red) for cluster connection status.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | clusterData | any | No | `{}` | Cluster connection data |
  | showBorder | bool | No | `false` | Show border ring |
- **Variants:** Green (fully connected), Yellow (partially), Red (not connected).

#### ColorDots

- **Path:** `src/components1/common/widgets/ColorDots.jsx`
- **Description:** Small colored vertical bar indicator based on severity/status level.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | severity | string | Yes | - | `'highest'` \| `'high'` \| `'medium'` \| `'low'` \| `'lowest'` \| `'critical'` \| `'open'` \| `'done'` etc. |

#### TrendArrowPercentage

- **Path:** `src/components1/common/widgets/TrendArrowPercentage.jsx`
- **Description:** Percentage value with an up/down arrow indicating positive or negative trend.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | No | - | Percentage value |
  | sign | number | No | `1` | Trend direction multiplier |
  | width | string | No | `'50px'` | Container width |

#### SummaryLabels

- **Path:** `src/components1/common/widgets/SummaryLabels.jsx`
- **Description:** Colored badge/label with optional gray description text.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | variant | `'critical'` \| `'info'` \| `'savings'` | No | `'info'` | Color variant |
  | label | string | Yes | - | Badge text |
  | grayText | string | No | - | Secondary description |

#### CustomListWithShowMore

- **Path:** `src/components1/common/CustomListWithShowMore.tsx`
- **Description:** Bulleted string list with "Show more/less" toggle.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | string[] | Yes | - | List items |
  | initialCount | number | No | `5` | Items shown initially |
  | onItemClick | func | No | - | Item click handler |

#### HighLights

- **Path:** `src/components1/common/widgets/HighLights.jsx`
- **Description:** Renders highlighted text or custom component inside a padded box.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | text | any | No | - | Text content |
  | component | any | No | `null` | Custom component |

#### FieldRenderer

- **Path:** `src/components1/common/FieldRenderer.tsx`
- **Description:** Renders input/output fields of a task according to a schema definition, with type badges and copy-to-clipboard.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | any | Yes | - | Field data |
  | schema | any | Yes | - | Schema definition |
  | fieldType | `'input'` \| `'output'` | Yes | - | Field direction |
  | taskDefinitions | any[] | Yes | - | Task definitions |
  | copyToClipboard | func | Yes | - | Copy callback |

#### ConsoleLogOutput

- **Path:** `src/components1/common/ConsoleLogOutput.tsx`
- **Description:** Renders console/log output text with ANSI color stripping and red error highlighting.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | string | Yes | - | Log text |
  | sx | CSSProperties | No | - | Style overrides |

#### DevOpsTimelineMUI

- **Path:** `src/components1/common/DevOpsTimelineMUI.tsx`
- **Description:** Fetches and renders a vertical DevOps event timeline (alerts, commits, workloads) for a given event ID.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | eventId | string | Yes | - | Event identifier |

---

### Data Input / Forms

#### CustomButton (NewCustomButton) ⭐ PRIMARY

- **Path:** `src/components1/common/NewCustomButton.jsx`
- **Description:** Primary styled button with three visual variants, five sizes, optional tooltip, loading spinner, and start/end icons.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | text | any | No | - | Button label |
  | variant | `'primary'` \| `'secondary'` \| `'tertiary'` | No | `'primary'` | Visual style |
  | size | `'xSmall'` \| `'Small'` \| `'Medium'` \| `'Large'` \| `'xLarge'` | No | `'Small'` | Button size |
  | onClick | func | No | - | Click handler |
  | disabled | bool | No | `false` | Disabled state |
  | loading | bool | No | `false` | Show spinner |
  | startIcon | any | No | - | Icon before text |
  | endIcon | any | No | - | Icon after text |
  | showTooltip | bool | No | `false` | Show tooltip |
  | toolTipTitle | string | No | `''` | Tooltip text |
  | type | string | No | `''` | HTML button type |
- **Sizes:** `xSmall` (28px), `Small` (32px), `Medium` (36px), `Large` (40px), `xLarge` (44px)
- **When to use:** Use for all primary, secondary, and tertiary button actions. This is the preferred button component.
- **Example:**

```tsx
<CustomButton text="Save" variant="primary" size="Medium" onClick={handleSave} />
<CustomButton text="Cancel" variant="secondary" />
<CustomButton text="Saving..." variant="primary" loading={true} />
```

#### CustomButton (Legacy)

- **Path:** `src/components1/common/CustomButton.jsx`
- **Description:** Older button component with more variant options. Prefer `NewCustomButton` for new code.
- **Variants:** `primary`, `secondary`, `iconButton`, `link`, `link2`, `blueButton`, `cancelButton`, `lightButton`, `blueOutlineButton`

#### CustomTextField

- **Path:** `src/components1/common/CustomTextField.tsx`
- **Description:** Styled text field wrapper around MUI TextField with label, instruction text, focus/active states, and error handling.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | label | string | No | - | Field label |
  | instructionText | string | No | - | Helper instruction |
  | placeholder | string | No | - | Placeholder text |
  | value | string | No | - | Field value |
  | onChange | func | No | - | Change handler |
  | error | bool | No | `false` | Error state |
  | helperText | string | No | - | Error/helper text |
  | multiline | bool | No | `false` | Multiline mode |
  | size | `'small'` \| `'medium'` | No | `'small'` | Field size |
  | variant | `'outlined'` \| `'filled'` \| `'standard'` | No | `'outlined'` | Field variant |
  | required | bool | No | `false` | Required indicator |
  | showActiveState | bool | No | `true` | Show active border |
- **When to use:** Use for all text input fields. Wraps MUI TextField with consistent styling.
- **Dependencies:** Wraps MUI `TextField`.

#### CustomDropdown

- **Path:** `src/components1/common/CustomDropdown.jsx`
- **Description:** Full-featured autocomplete dropdown with cluster status indicator, cloud provider grouping, loading state, and extensive style customization.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | options | array | Yes | `[]` | Dropdown options |
  | value | any | No | - | Selected value |
  | onChange | func | No | - | Change handler |
  | label | string | No | `''` | Label text |
  | minWidth | string | No | `'180px'` | Minimum width |
  | isDisabled | bool | No | `false` | Disabled state |
  | isLoading | bool | No | `false` | Loading state |
  | groupByCloudProvider | bool | No | `false` | Group options by cloud |
  | openDirection | `'up'` \| `'down'` | No | `'down'` | Dropdown direction |
- **When to use:** Standard single-select dropdown. Use for most dropdown needs.
- **Dependencies:** Wraps MUI `Autocomplete`.

#### CustomSelectDropdown

- **Path:** `src/components1/common/CustomSelectDropdown.jsx`
- **Description:** MUI Select-based dropdown with optional "All" option.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | Yes | - | Selected value |
  | onChange | func | Yes | - | Change handler |
  | options | array | Yes | - | Options list |
  | label | string | No | `''` | Label |
  | showAll | bool | No | `false` | Add "All" option |
  | noBorder | bool | No | `false` | Borderless variant |
- **When to use:** Use when you need a native Select (not autocomplete). Prefer `CustomDropdown` for most cases.

#### CustomMultiDropdown

- **Path:** `src/components1/common/CustomMultiDropdown.tsx`
- **Description:** Multi-select dropdown with chip display, optional search filter, loading state, tag limiting, and clear-all.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | Value[] | Yes | - | Selected values |
  | onChange | func | Yes | - | Change handler |
  | options | Value[] | Yes | - | Available options |
  | handleCloseIcon | func | Yes | - | Chip remove handler |
  | label | string | No | `''` | Label |
  | enableSearch | bool | No | `false` | Enable search filter |
  | limitTags | number | No | `1` | Max visible chips |
  | isLoading | bool | No | `false` | Loading state |
  | isRequired | bool | No | `false` | Required indicator |
- **When to use:** Use for multi-select scenarios. Wraps MUI `Select` with chips.

#### CustomAutocomplete

- **Path:** `src/components1/common/CustomAutocomplete.jsx`
- **Description:** Feature-rich autocomplete supporting single/multi-select, grouped options, loading state, and custom chips.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | options | array | No | `[]` | Options list |
  | value | any | No | - | Selected value(s) |
  | onSelect | func | No | - | Selection handler |
  | multiple | bool | No | `false` | Multi-select mode |
  | grouped | bool | No | `false` | Grouped options |
  | label | string | No | - | Label text |
  | width | number \| string | No | `200` | Component width |
  | limitTags | number | No | `1` | Visible tag limit |
  | isOptionsLoading | bool | No | `false` | Loading state |

#### AutoCompleteInput

- **Path:** `src/components1/common/inputs/AutoCompleteInput.tsx`
- **Description:** Styled autocomplete input field with loading indicator and custom dropdown icon.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | label | string | Yes | - | Field label |
  | options | string[] | Yes | - | Options |
  | value | string \| null | Yes | - | Selected value |
  | onChange | func | Yes | - | Change handler |
  | toShowNoOption | bool | Yes | - | Show "no options" text |
  | width | number | Yes | - | Component width |
  | isLoading | bool | No | `false` | Loading state |

#### CustomCheckBox

- **Path:** `src/components1/common/CustomCheckbox.jsx`
- **Description:** Branded checkbox with optional label, start/end elements, and indeterminate state.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | checked | bool | No | - | Checked state |
  | onChange | func | No | - | Change handler |
  | text | string \| node | No | - | Label text |
  | disabled | bool | No | - | Disabled state |
  | indeterminate | bool | No | - | Indeterminate state |
  | startElement | node | No | - | Element before checkbox |
  | endElement | node | No | - | Element after label |

#### CustomSwitch

- **Path:** `src/components1/common/CustomSwitch.jsx`
- **Description:** Styled toggle switch (ant-style) wrapping MUI Switch.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | id | string | No | - | Element ID |
  | onChange | func | No | - | Toggle handler |
  | checked | bool | No | - | Checked state |
  | disabled | bool | No | `false` | Disabled state |

#### CustomSearch

- **Path:** `src/components1/common/CustomSearch.jsx`
- **Description:** Search input field with search icon, clear button, and Enter key support.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | label | string | No | `''` | Placeholder text |
  | onChange | func | No | - | Input change handler |
  | onEnterPress | func | No | - | Enter key handler |
  | onClear | func | No | - | Clear button handler |
  | value | string | No | - | Input value |
  | minWidth | string | No | `'150px'` | Minimum width |
  | maxWidth | string | No | `'260px'` | Maximum width |
  | disabled | bool | No | `false` | Disabled state |

#### CustomButtonsGroup

- **Path:** `src/components1/common/CustomButtonsGroup.jsx`
- **Description:** Segmented button group (tab-like selector) built on MUI ButtonGroup.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | selected | any | No | - | Selected value |
  | onClick | func | No | - | Selection handler |
  | options | array | No | `[]` | Array of {value, text, disabled?} |
  | borderColor | string | No | `'#97DCE4'` | Border color |
  | tabType | bool | No | - | Tab-like styling |

#### CustomDateTimePicker

- **Path:** `src/components1/common/widgets/CustomDateTimePicker.jsx`
- **Description:** Labeled date-time picker wrapping MUI X DateTimePicker with dayjs adapter.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | label | any | No | - | Label text |
  | value | any | No | - | Selected date-time |
  | onChange | func | No | - | Change handler |
  | views | array | No | `['day', 'hours', 'minutes']` | Visible views |
  | format | string | No | `'MM/DD/YYYY hh:mm A'` | Display format |
- **Dependencies:** Wraps MUI X `DateTimePicker`.

#### DynamicForm

- **Path:** `src/components1/common/DynamicForm.jsx`
- **Description:** Renders a dynamic form from a schema definition, supporting nested objects, arrays, maps, autocomplete, checkboxes, and conditional visibility.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | actionDetails | object | No | `{}` | Schema definition with params |
  | onChange | func | No | - | Form change handler |
  | errors | object | No | `{}` | Field errors |
  | initialValues | object | No | `{}` | Initial form values |

#### FormCard / FormField / FormBuilder (NewReusabeFormComponents)

- **Path:** `src/components1/common/NewReusabeFormComponents.jsx`
- **Description:** Reusable form building blocks — a card container, a polymorphic field component, and a declarative form builder.
- **Composition:** Three named exports:

**FormCard** — Container with title, description, and grid layout.
| Prop | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| title | string | No | - | Card title |
| children | node | Yes | - | Card content |
| columns | `1` \| `2` | No | `2` | Grid columns |
| expand | bool | No | `false` | Expandable |

**FormField** — Polymorphic form field.
| Prop | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| fieldType | `'textfield'` \| `'textarea'` \| `'dropdown'` \| `'autocomplete'` \| `'checkbox'` \| `'custom'` | No | `'textfield'` | Field type |
| label | string | No | - | Field label |
| value | any | No | - | Field value |
| onChange | func | No | - | Change handler |
| required | bool | No | `false` | Required |
| error | string | No | `''` | Error message |

**FormBuilder** — Declarative form from sections array.
| Prop | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| sections | array | Yes | - | Array of section configs |

#### FilterGroup

- **Path:** `src/components1/common/FilterGroup.tsx`
- **Description:** Configurable filter bar supporting dropdown, search, button group, custom component, and date-time range filters.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | filterOptions | any[] | No | `[]` | Filter configurations |
  | dateTimeRange | any | No | `{enabled:false}` | Date range config |
- **Filter types:** `'buttons'`, `'dropdown'`, `'search'`, `'custom'`

#### CustomTableFilters

- **Path:** `src/components1/common/CustomTableFilters.jsx`
- **Description:** Side panel filter system with accordion-based filter groups supporting multiple filter types.
- **Filter types:** `buttons`, `dropdown`, `multi-dropdown`, `multi-select`, `single-select`, `search`, `textfield`, `custom`, `switch`
- **When to use:** Use within `BoxLayout2` when `displaySideFilters={true}`.

#### TimePickerButtonsGroups

- **Path:** `src/components1/common/TimePickerButtonsGroup.tsx`
- **Description:** Button group for selecting time intervals with a selected state highlight.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | options | Option[] | Yes | - | Time options |
  | selected | string | Yes | - | Selected value |
  | onClick | func | Yes | - | Selection handler |

#### AutoRefreshControls

- **Path:** `src/components1/common/AutoRefreshControls.tsx`
- **Description:** Dropdown control for setting an auto-refresh interval (Off, 5s, 10s, 15s, 30s, 45s, 60s).
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | callBack | func | Yes | - | Interval change handler |

#### ClusterDropDown

- **Path:** `src/components1/common/ClusterDropDown.jsx`
- **Description:** Global cluster selector dropdown that syncs with URL, user preferences, and DataContext.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onChange | func | No | - | Selection handler |
  | disableRouteChanges | bool | No | `false` | Disable URL updates |
  | noLabel | bool | No | `false` | Hide label |
  | ...dropdownProps | any | No | - | Passed to CustomDropdown |

#### OptionMenu

- **Path:** `src/components1/common/OptionMenu.jsx`
- **Description:** Autocomplete dropdown for selecting from a list of account-like options, auto-selects first option.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | options | array | Yes | - | Options list |
  | callback | func | Yes | - | Selection handler |
  | labelValue | string | No | `'Select Account'` | Label |

---

### Feedback

#### SnackbarComponent

- **Path:** `src/components1/common/SnackbarComponent.tsx`
- **Description:** Global snackbar notification listener that subscribes to the snackbar service and displays alerts.
- **Props:** None (subscribes internally to `snackbar` service).
- **Severity:** success, info, warning, error. Auto-hides after 5000ms.
- **When to use:** Include once in the app root. Use `snackbar.success()` / `snackbar.error()` to trigger.

#### NSnackbar

- **Path:** `src/components1/common/NSnackbar.tsx`
- **Description:** Controlled snackbar notification component with configurable severity and message.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | message | any | Yes | - | Notification message |
  | severity | string | Yes | - | success \| info \| warning \| error |
  | open | bool | Yes | - | Visibility state |
  | handleClose | func | Yes | - | Close handler |
- **When to use:** Use when you need controlled (prop-driven) snackbar behavior. For fire-and-forget, use the `snackbar` service instead.

#### Loader

- **Path:** `src/components1/common/Loader.tsx`
- **Description:** Full-viewport loading spinner displaying an animated GIF.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | style | CSSProperties | No | - | Style overrides |

#### ThreeDotLoader

- **Path:** `src/components1/common/ThreeDotLoader.js`
- **Description:** Animated loading indicators — a CSS pulse dot and an animated "..." text.
- **Exports:** `ThreeDotLoader` (default), `ThreeDotsLoaderText` (named)
- **Props:** None

#### ShimmerLoading

- **Path:** `src/components1/common/ShimmerLoading.jsx`
- **Description:** Conditional shimmer/skeleton loading wrapper; renders animated shimmer lines or a single block while loading, then renders children.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | isLoading | bool | Yes | - | Loading state |
  | height | string | No | `'280px'` | Shimmer height |
  | width | string | No | `'100%'` | Shimmer width |
  | children | node | No | - | Content to show when loaded |
  | lines | number | No | - | Number of shimmer lines (multi-line mode) |
  | lineHeight | string | No | `'24px'` | Per-line height |
  | lineSpacing | string | No | `'12px'` | Spacing between lines |
- **Example:**

```tsx
<ShimmerLoading isLoading={loading} lines={3}>
  <ActualContent />
</ShimmerLoading>
```

#### NewShimmerLoading

- **Path:** `src/components1/common/NewShimmerloading.jsx`
- **Description:** Animated shimmer/skeleton loader with pulsing icon and wave skeleton bar.
- **Props:** None

#### SummarySkeletonLoader

- **Path:** `src/components1/common/SummarySkeletonLoader.jsx`
- **Description:** Full-page skeleton loader for a 3-column summary layout.
- **Props:** None

#### ProgressBar

- **Path:** `src/components1/common/widgets/ProgressBar.jsx`
- **Description:** Linear progress bar showing usage percentage with optional detailed tooltip.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | No | `0` | Percentage value |
  | blueVarient | bool | No | `false` | Blue vs green color |
  | capacity | any | No | `''` | Total capacity label |
  | tooltipRequired | bool | No | `false` | Show detailed tooltip |
  | showCapacity | bool | No | `true` | Show capacity text |
- **Variants:** `blueVarient`: blue (#60A5FA) vs green (#4caf50); turns red when usage > 90%.

#### EmptyData

- **Path:** `src/components1/common/EmptyData.jsx`
- **Description:** Empty state placeholder with optional image, heading, subheading, and children.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | img | any | No | - | Illustration image |
  | heading | string | No | - | Title text |
  | subHeading | string | No | - | Description text |
  | height | any | No | `'308px'` | Container height |
  | children | any | No | - | Custom content |

#### FeedbackComponent (ThumpsUpAndDown)

- **Path:** `src/components1/common/ThumpsUpAndDown.jsx`
- **Description:** Thumbs up/down feedback UI with a detailed feedback dialog for negative responses.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onFeedbackSubmit | func | Yes | - | Submit callback |
  | sentFeedback | object | No | `{}` | Previous feedback state |

---

### Overlay / Modals

#### NDialog

- **Path:** `src/components1/common/modal/NDialog.tsx`
- **Description:** Reusable modal dialog with title, content, additional component slot, loading indicator, and submit/cancel buttons.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | open | bool | Yes | - | Visibility |
  | dialogTitle | ReactNode | Yes | - | Dialog title |
  | dialogContent | ReactNode | Yes | - | Dialog body |
  | handleClose | func | No | - | Close handler |
  | handleSubmit | func | No | - | Submit handler |
  | buttonText | string | No | - | Submit button text |
  | loading | bool | No | `false` | Loading state |
  | isSubmitRequired | bool | No | `true` | Show submit button |
  | isCancelRequired | bool | No | `true` | Show cancel button |
  | additionalComponent | any | Yes | - | Extra slot above actions |
- **When to use:** Use for confirmation dialogs, form modals, and info dialogs.

#### WelcomeModal

- **Path:** `src/components1/common/modal/welcomeModal.jsx`
- **Description:** Welcome dialog shown to new users, offering options to add a K8s account or start with a demo cluster.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | open | any | No | - | Visibility |
  | handleClose | func | No | - | Close handler |
  | handleAddK8sAccount | func | No | - | Add account action |

#### ButtonMenu

- **Path:** `src/components1/common/ButtonMenu.jsx`
- **Description:** Dropdown button that opens a styled MUI Menu with selectable items.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | title | string | No | `'Options'` | Button text |
  | items | array | No | - | Menu items with {text, icon, onClick, disabled} |
  | variant | `'primary'` \| `'tertiary'` | No | - | Color variant |
  | size | `'small'` \| `'medium'` \| `'large'` \| `'xSmall'` | No | - | Button size |
  | sx | object | No | - | Style overrides |
- **When to use:** Use for buttons with dropdown menus (e.g., "Create → K8s Account / Jira Account").

#### ThreeDotsMenu

- **Path:** `src/components1/common/ThreeDotsMenu.jsx`
- **Description:** Vertical three-dot icon button that opens a dropdown menu with optional sub-menus.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | menuItems | array | No | `[]` | Items with {label, icon, disabled, subMenu} |
  | onMenuClick | func | No | - | Item click handler |
  | data | any | No | - | Context data passed to handler |
  | menuWidth | string \| number | No | - | Menu width |
- **When to use:** Use for context menus on cards, table rows, etc.

#### NubiChatSidebar

- **Path:** `src/components1/common/NubiChatSidebar.tsx`
- **Description:** Slide-in chat sidebar for the NuBi AI assistant, supporting overlay/fixed modes.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | isVisible | bool | Yes | - | Show/hide sidebar |
  | onClose | func | No | - | Close handler |
  | accountId | string | Yes | - | User account ID |
  | context | object | No | - | Chat context (type, data) |
  | position | `'left'` \| `'right'` | No | `'right'` | Side placement |
  | mode | `'overlay'` \| `'fixed'` | No | `'overlay'` | Display mode |
  | width | string | No | `'500px'` | Sidebar width |

---

### Actions

#### CopyButton

- **Path:** `src/components1/common/CopyButton.jsx`
- **Description:** Small icon button with a copy (FileCopy) icon.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onClick | func | No | - | Click handler |
  | sx | object | No | - | Style overrides |

#### DownloadButton

- **Path:** `src/components1/common/DownloadButton.jsx`
- **Description:** Icon button that downloads data as file (blob, canvas image, CSV from table).
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onClick | func | No | - | Async handler returning download options |
  | width | any | No | `'32px'` | Button width |
  | height | any | No | `'32px'` | Button height |
- **Dependencies:** `file-saver` (saveAs)

#### ShareButton

- **Path:** `src/components1/common/ShareButton.jsx`
- **Description:** Placeholder share button (tooltip says "Coming Soon").
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onClick | func | No | - | Click handler |

#### CreateTicketButton

- **Path:** `src/components1/common/CreateTicketButton.jsx`
- **Description:** Icon button with a ticket icon and tooltip for creating tickets.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onClick | func | No | - | Click handler |

#### InvestigateButton

- **Path:** `src/components1/common/InvestigateButton.jsx`
- **Description:** Icon button linking to a troubleshooting/investigation page.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | url | string | No | - | Navigation URL |
  | onClick | func | No | - | Click handler |
  | text | string | No | `'Investigate'` | Button text |
  | displayText | bool | No | `false` | Show text label |

#### ResolveButton

- **Path:** `src/components1/common/ResolveButton.jsx`
- **Description:** Icon button for triggering resolution actions, with an autopilot-configured state indicator.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | onClick | func | No | - | Click handler |
  | displayText | bool | No | `false` | Show text label |
  | isResolvedConfigured | bool | No | `false` | Autopilot indicator |

#### ExpandButton

- **Path:** `src/components1/common/ExpandButton.jsx`
- **Description:** Icon button with a rotating arrow to indicate expand/collapse state.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | expanded | bool | No | - | Expansion state |
  | onClick | func | No | - | Toggle handler |
  | isSmallSize | bool | No | - | Small variant (20x20 vs 28x28) |

#### PrimaryLink

- **Path:** `src/components1/common/PrimaryLink.jsx`
- **Description:** Inline clickable text styled as a primary (blue) link.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | node | Yes | - | Link content |
  | onClick | func | No | - | Click handler |

#### SecondaryLink

- **Path:** `src/components1/common/SecondaryLink.jsx`
- **Description:** Inline clickable text styled as a secondary (gray, hover-to-blue) link.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | node | Yes | - | Link content |
  | onClick | func | No | - | Click handler |

#### CustomLink

- **Path:** `src/components1/common/CustomLink.jsx`
- **Description:** Styled Next.js Link with optional "open in new tab" icon.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | href | string | Yes | - | Link URL |
  | children | node | Yes | - | Link content |
  | target | string | No | `'_self'` | Link target |
  | secondaryText | bool | No | `false` | Smaller text style |
  | openInNew | bool | No | `false` | Show external link icon |

#### CustomTicketLink

- **Path:** `src/components1/common/CustomTicketLink.jsx`
- **Description:** Displays a "Ticket - <ID>" label with an optional external link.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | ticketURL | string | Yes | - | Ticket URL |
  | ticketID | string | Yes | - | Ticket identifier |

---

### Media & Icons

#### CloudProviderIcon

- **Path:** `src/components1/common/CloudProviderIcon.jsx`
- **Description:** Displays icons for 30+ cloud providers and integrations (AWS, Azure, GCP, K8s, Datadog, ArgoCD, etc.).
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | cloud_provider | string | No | - | Provider name |
  | width | string | No | `'30px'` | Icon width |
  | height | string | No | `'24px'` | Icon height |

#### CloudIcon (compact)

- **Path:** `src/components1/common/CloudIcon.jsx`
- **Description:** Smaller set of cloud/integration provider icons (AWS, GCP, Azure, K8s, Snowflake, etc.).
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | cloud_provider | string | Yes | - | Provider name |
  | height | string | No | `'28px'` | Icon height |
  | width | string | No | `'28px'` | Icon width |

#### LangTypeIcon

- **Path:** `src/components1/common/LangTypeIcon.jsx`
- **Description:** Colored icon for a given programming language, database, messaging system, or cloud service name.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | appLang | string \| string[] | No | - | Language/service name |
  | size | number | No | `25` | Icon size |
- **Dependencies:** `react-icons/fa6`, `react-icons/si`, `react-icons/di`, etc.

#### CustomIcon

- **Path:** `src/components1/common/CustomIcon.jsx`
- **Description:** Displays an icon image inside a small rounded blue-tinted square badge.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | icon | any | No | - | Icon source |

#### CustomDropdownIcon

- **Path:** `src/components1/common/CustomDropdownIcon.tsx`
- **Description:** Wrapper around MUI KeyboardArrowDownIcon with customizable color.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | color | string | Yes | - | Icon color |

#### GetInsightIcon

- **Path:** `src/components1/common/GetInsightIcon.jsx`
- **Description:** Returns an appropriate icon asset based on insight source type.
- **Export:** Named export (utility function, not a component).
- **Usage:** `GetInsightIcon(item)` where `item.source` is `'Event'`, `'Recommendation'`, or `'Metric'`.

#### SvgRenderer

- **Path:** `src/components1/common/SvgRenderer.tsx`
- **Description:** Renders a raw SVG string into the DOM.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | svg | string | Yes | - | SVG markup string |
  | style | CSSProperties | No | - | Container style |

#### ChartSwitcher

- **Path:** `src/components1/common/ChartSwitcher.jsx`
- **Description:** Toggle button pair to switch between line chart and bar chart views.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | isBarChart | bool | Yes | - | Current chart type |
  | leftButtonClick | func | Yes | - | Line chart handler |
  | rightButtonClick | func | Yes | - | Bar chart handler |

---

### Charts

#### LineChart (Charts)

- **Path:** `src/components1/common/charts/LineCharts.jsx`
- **Description:** Feature-rich line chart with custom HTML tooltip, HTML legend with min/max/p99/avg metrics, dynamic height, data-point click handling.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | array | No | `[]` | Chart data arrays |
  | labels | array | No | `[]` | X-axis labels |
  | colors | array | No | `[]` | Line colors |
  | chartLabel | string \| array | No | `''` | Dataset labels |
  | id | string | No | `''` | Chart element ID |
  | loading | bool | No | `false` | Loading state |
  | onDataPointClick | func | No | - | Click handler |
  | dynamicHeight | bool | No | `true` | Auto-adjust height |
- **Dependencies:** `react-chartjs-2` (Line), `chart.js`

#### BarChart

- **Path:** `src/components1/common/charts/BarChart.jsx`
- **Description:** Stacked bar chart with auto-generated datasets, loading shimmer.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | array | No | `[[]]` | Chart data |
  | labels | array | No | `[]` | X-axis labels |
  | colors | string \| array | No | - | Bar colors |
  | chartLabel | string \| array | No | `''` | Dataset labels |
  | loading | bool | No | `false` | Loading state |
- **Dependencies:** `react-chartjs-2` (Bar), `chart.js`

#### DoughnutChart

- **Path:** `src/components1/common/charts/DoughnutChart.jsx`
- **Description:** Doughnut chart with center value display, custom legend, click handling.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | values | array | No | `[]` | Chart values |
  | labels | array | No | - | Segment labels |
  | size | number | No | `77` | Chart size in px |
  | colors | string \| array | No | `['#778899']` | Segment colors |
  | displayLegend | bool | No | `false` | Show legend |
  | displayValue | bool \| string \| number | No | `false` | Center value |
  | onItemClick | func | No | - | Segment click handler |
- **Dependencies:** `react-chartjs-2` (Doughnut), `chart.js`

#### DoughnutChartK8s

- **Path:** `src/components1/common/charts/DoughnutChartK8s.jsx`
- **Description:** Simple percentage doughnut chart for K8s metrics with center percentage display.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | No | `20` | Percentage value |
  | size | any | No | `'77px'` | Chart size |
  | color | string | No | `'#81D97F'` | Fill color |

#### ChartComponent

- **Path:** `src/components1/common/charts/ChartComponent.jsx`
- **Description:** Generic chart wrapper that renders Bar, Pie, or Line chart by type prop.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | type | `'bar'` \| `'pie'` \| `'line'` | Yes | - | Chart type |
  | data | object | Yes | - | Chart.js data object |
  | options | object | No | - | Chart.js options |
  | maxHeight | number | No | `200` | Max chart height |
  | loading | bool | Yes | - | Loading state |

#### CustomHeatMap

- **Path:** `src/components1/common/charts/CustomHeatMap.jsx`
- **Description:** Day-by-hour heatmap grid with color-coded cells and tooltips.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | data | array | No | `[]` | Heatmap data grid |
  | xAxisLabels | array | No | last 7 days | X-axis labels |
  | yAxisLabels | array | No | 24 hours | Y-axis labels |
  | showTooltip | bool | No | `true` | Show cell tooltips |
  | loading | bool | No | `true` | Loading state |

#### ShowPrometheusLineChart

- **Path:** `src/components1/common/charts/ShowPrometheusLineChart.jsx`
- **Description:** Fetches Prometheus query results from the API and renders them as line charts inside a BoxLayout.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | accountId | any | No | - | Account identifier |
  | query | string | No | `''` | Prometheus query |
  | selectedDateRange | object | No | `{startDate: 1h ago, endDate: now}` | Time range |

---

### Format Components

#### Text

- **Path:** `src/components1/common/format/Text.jsx`
- **Description:** Text display with auto-ellipsis (overflow detection via ResizeObserver), copyable tooltip, and optional markdown rendering.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | No | - | Text content |
  | showAutoEllipsis | bool | No | `false` | Enable auto-truncation |
  | copyableTooltip | bool | No | `false` | Copyable tooltip |
  | format | string | No | `'text'` | `'text'` or `'markdown'` |
  | defaultVal | any | No | `'-'` | Fallback value |
  | secondaryText | bool | No | - | Lighter style |

#### Currency

- **Path:** `src/components1/common/format/Currency.jsx`
- **Description:** Formats and displays a currency value with prefix/suffix, tooltip, and variant-based coloring.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | number \| string | No | - | Amount |
  | prefix | string | No | `'$'` | Currency prefix |
  | suffix | string | No | `''` | Suffix text |
  | varient | string | No | `'default'` | `'default'` \| `'savings'` (green) \| `'expense'` (red) |
  | withTooltip | bool | No | `true` | Show tooltip |
  | precison | number | No | `0` | Decimal places |
- **Also exports:** `formatCurrency` (named, utility function).

#### Memory

- **Path:** `src/components1/common/format/Memory.jsx`
- **Description:** Formats and displays a memory value with unit conversion and tooltip.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | No | - | Memory value |
  | sourceUnit | string | No | `'bytes'` | Input unit |
  | targetUnit | string | No | `'gb'` | Display unit |
  | suffix | bool | No | `true` | Show unit suffix |

#### NumberComponent

- **Path:** `src/components1/common/format/Number.jsx`
- **Description:** Formats and displays a number with configurable fraction digits, suffix, and tooltip.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | any | No | - | Number value |
  | minimumFractionDigits | number | No | `0` | Min decimals |
  | maximumFractionDigits | number | No | `2` | Max decimals |
  | suffix | string | No | `''` | Suffix text |

#### Datetime

- **Path:** `src/components1/common/format/Datetime.jsx`
- **Description:** Displays a relative time difference (e.g. "3h ago" or "in 2D") with configurable granularity.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | value | string \| Date \| number | No | - | Date/time value |
  | maxLevel | number | No | `1` | Granularity levels |
  | showTooltip | bool | No | `true` | Show full date tooltip |
  | prefix | string | No | `''` | Text prefix |
  | suffix | string | No | `''` | Text suffix |

---

### Chips

> **Component:** `src/components1/common/Chip.tsx`

The chip primitive. **One component, seven variants.** Use it anywhere you'd reach for a pill, badge, tag, status indicator, filter toggle, or count. Do **not** roll your own MUI `Chip` with custom `sx` — every existing pattern is covered by the variants below.

Import:

```tsx
import Chip from '@components1/common/Chip';
```

#### Variant overview

| Variant  | Purpose                         | Interactive?        | Examples                             |
| -------- | ------------------------------- | ------------------- | ------------------------------------ |
| `filter` | Toggleable filter selection     | ✅ click + keyboard | provider chips, severity facets      |
| `status` | Read-only state indicator       | ❌                  | "Healthy", "Failed", severity counts |
| `tag`    | Static categorical label        | ❌                  | resource types, owner labels         |
| `input`  | User-added value with ×         | ✅ click ×          | applied filters, multi-select tokens |
| `action` | Triggers an action when clicked | ✅ click + keyboard | "Add filter", "Retry"                |
| `count`  | Number-first display            | ❌                  | "23", "+3", "12 open"                |
| `avatar` | Entity (user/team/repo)         | ✅ click            | "AK Alice Kim"                       |

#### Sizes (3, fixed)

```tsx
<Chip variant='status' size='xs'>Healthy</Chip>  {/* 16px tall, dense rows */}
<Chip variant='status' size='sm'>Healthy</Chip>  {/* 20px tall, default */}
<Chip variant='status' size='md'>Healthy</Chip>  {/* 24px tall, hero surfaces */}
```

#### Tones (6 semantic)

`neutral` (default), `info`, `success`, `warning`, `danger`, `pending`. Each has its own pastel pill bg + dot color.

```tsx
<Chip variant='status' tone='success' leadingDot label='Resolved' />
<Chip variant='status' tone='warning' leadingDot label='Degraded' />
<Chip variant='status' tone='danger' leadingDot label='Failed' />
```

#### Common patterns

**Filter row with leading icon** (provider chips on the Cost tab):

```tsx
<Chip
  variant='filter'
  size='sm'
  leadingIcon={<CloudProviderIcon cloud_provider='aws' width='14px' height='14px' />}
  label='AWS'
  selected={provider === 'aws'}
  onClick={() => setProvider('aws')}
/>
```

Selection always renders as **indigo** (the spec's filter palette) regardless of `tone`. The `tone` prop is honored only for the leading dot color.

**Severity filter with leading dot** (severity facets):

```tsx
<Chip
  variant='filter'
  size='sm'
  tone='danger'
  leadingDot
  label={`Critical ${count}`}
  selected={severity === 'Critical'}
  onClick={() => setSeverity('Critical')}
/>
```

**Inline severity count with hollow-zero state** (subcategory rows):

```tsx
<Chip variant='status' size='xs' tone='danger'  leadingDot label='1 critical' />
<Chip variant='status' size='xs' tone='warning' leadingDot label='7 high' />
<Chip variant='status' size='xs' tone='neutral' label='16 open' />
```

For zero counts, pair `dotVariant='hollow'` with `disabled` to dim the chip without removing it:

```tsx
<Chip
  variant='status'
  size='xs'
  tone='danger'
  leadingDot
  dotVariant={count === 0 ? 'hollow' : 'filled'}
  disabled={count === 0}
  label={`${count} critical`}
/>
```

**Trend delta** (cost-up / cost-down):

```tsx
import TrendingUpIcon from '@mui/icons-material/TrendingUp';
import TrendingDownIcon from '@mui/icons-material/TrendingDown';

<Chip
  variant='status'
  size='xs'
  tone={delta > 0 ? 'danger' : 'success'}
  leadingIcon={delta > 0 ? <TrendingUpIcon /> : <TrendingDownIcon />}
  label={`${Math.abs(delta)}%`}
/>;
```

**Tag chip with categorical hue** (8-hue palette, hashable):

```tsx
import Chip, { hashHue } from '@components1/common/Chip';

<Chip variant='tag' size='xs' hue='blue' label='frontend' />
<Chip variant='tag' size='xs' hue={hashHue(tag.id)} label={tag.name} />
```

**Removable input chip** (applied filter pill):

```tsx
<Chip variant='input' size='sm' leadingIcon={<PersonIcon />} label='Owner: alice' onDismiss={() => removeFilter('owner')} />
```

**Count chip nested in a tab/button** (use `shape='rect'`):

```tsx
<Chip variant='count' size='xs' shape='rect' label='23' />
```

**Avatar chip** (entity reference):

```tsx
<Chip variant='avatar' size='sm' leadingAvatar={<Avatar>AK</Avatar>} label='Alice Kim' />
```

#### Don'ts

- ❌ Don't use raw MUI `<Chip>` with custom `sx` — every variant maps to one of the 7 above.
- ❌ Don't add background/border to a chip via `sx` to "make it match" — change the `tone` instead.
- ❌ Don't pass `tone` to `tag` chips — use `hue` for categorical color (8-hue palette).
- ❌ Don't pass `selected` to anything but `filter` chips.
- ❌ Don't pass `onClick` / `onDismiss` to `status` or `tag` chips — they're read-only.
- ❌ Don't use `leadingDot` on `tag` / `count` / `action` / `avatar` — only `status` and `filter`.
- ❌ Don't hardcode hex colors for chip backgrounds. Use `tone` (semantic) or `hue` (categorical).
- ❌ Don't reduce `xs` size below the 16px minimum to fit something — use a smaller surface or different layout.

#### Migrating from existing patterns

| If you currently use…                                 | Replace with                                                                         |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------ |
| MUI `<Chip variant='filled'>` for status              | `<Chip variant='status' tone='…'>`                                                   |
| MUI `<Chip variant='outlined' clickable>` for filters | `<Chip variant='filter' selected={…} onClick={…}>`                                   |
| Custom Box with dot + count text                      | `<Chip variant='status' size='xs' leadingDot tone='…' label='…'>`                    |
| `TrendArrowPercentage` component                      | `<Chip variant='status' tone='success'\|'danger' leadingIcon={<TrendingUp\|Down/>}>` |
| Custom severity badge                                 | `<Chip variant='status' tone='danger\|warning\|info\|success' leadingDot>`           |

#### Tests

Smoke tests live at `app/__tests__/components1/common/Chip.test.tsx` (44 tests covering variants, sizes, tones, hues, hollow-dot, leadingDot-on-filter, click + keyboard, dismiss, disabled).

---

### Cards & Containers

#### WidgetCard

- **Path:** `src/components1/common/WidgetCard.jsx`
- **Description:** Reusable white elevated card container with consistent shadow, border, and padding.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | node | No | - | Card content |
  | sx | object | No | `{}` | Style overrides |
- **When to use:** Use as a standard card wrapper for dashboard widgets.
- **Example:**

```tsx
<WidgetCard sx={{ p: 2 }}>
  <h3>Widget Title</h3>
  <p>Widget content</p>
</WidgetCard>
```

#### CustomBorderCard

- **Path:** `src/components1/common/CustomBorderCard.jsx`
- **Description:** Card container with configurable borders (bottom + optional left) and rounded corners.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | any | No | - | Card content |
  | borderColor | any | No | `'#BFDBFE'` | Bottom border color |
  | borderLeftColor | any | No | - | Left border color |
  | showLeftBorder | bool | No | `true` | Show left border |
  | padding | any | No | `'16px 25px 16px 16px'` | Card padding |

#### CustomCollapseable

- **Path:** `src/components1/common/CustomCollapseable.jsx`
- **Description:** Collapsible/expandable section using MUI Accordion.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | title | string | Yes | - | Section title |
  | children | node | No | - | Collapsible content |
  | icon | any | No | - | Custom expand icon |
  | defaultExpand | bool | No | `false` | Initial expanded state |

#### CustomAccordion

- **Path:** `src/components1/common/CustomAccordion.jsx`
- **Description:** Styled MUI accordion with optional icon, title, description, and custom style overrides.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | title | string | No | - | Accordion title |
  | description | string | No | - | Subtitle |
  | icon | any | No | - | Title icon |
  | children | any | No | - | Expandable content |
  | expanded | bool | No | - | Controlled expand state |
  | onChange | func | No | - | Expand toggle handler |

#### AccordionSmall

- **Path:** `src/components1/common/AccordionSmall.tsx`
- **Description:** Compact collapsible accordion with optional status label or status dropdown.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | ReactNode | Yes | - | Content |
  | header | ReactNode \| string | Yes | - | Header content |
  | status | string | No | - | Status label text |
  | enableStatusDropdown | bool | No | `false` | Show status dropdown |
  | onStatusChange | func | No | - | Status change handler |
  | expanded | bool | No | - | Controlled expand state |
  | onExpandedChange | func | No | - | Expand toggle |

---

### Steppers

#### CustomStepper

- **Path:** `src/components1/common/CustomStepper.tsx`
- **Description:** Horizontal multi-step wizard with navigation buttons, error states, and access control.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | steps | string[] | Yes | - | Step labels |
  | activeStep | number | Yes | - | Current step |
  | onStepChange | func | Yes | - | Step change handler |
  | onNext | func | Yes | - | Next button handler |
  | onBack | func | Yes | - | Back button handler |
  | children | ReactNode | Yes | - | Step content |
  | onSubmit | func | No | - | Final submit handler |
  | stepErrors | boolean[] | No | `[]` | Error states per step |
  | submitButtonText | string | No | `'Submit'` | Final button text |

#### NewVerticalStepper

- **Path:** `src/components1/common/NewVerticalStepper.tsx`
- **Description:** Vertical step navigation sidebar with collapsible task lists per step, status icons, and pending task counts.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | steps | Step[] | Yes | - | Step definitions with tasks |
  | activeStep | number | Yes | - | Current step |
  | onStepChange | func | Yes | - | Step change handler |
  | showTasks | bool | No | `false` | Show task sub-list |

#### VerticalStepNavigation

- **Path:** `src/components1/common/VerticalStepNavigation.tsx`
- **Description:** Simple vertical step navigation sidebar with numbered step circles, tooltips, and active state highlighting.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | steps | any[] | Yes | - | Step definitions |
  | activeStep | number | Yes | - | Current step |
  | onStepChange | func | Yes | - | Step change handler |
  | title | string | No | `'Upgrade Steps'` | Section title |

---

### Carousel

#### CustomSwiperCarousel

- **Path:** `src/components1/common/CustomSwiperCarousel.jsx`
- **Description:** Responsive carousel/slider wrapping Swiper.js with custom navigation arrows and pagination bullets.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | node | No | - | Slide content |
  | slidesToShow | number | No | `1` | Visible slides |
  | showArrows | bool | No | `true` | Show navigation arrows |
  | showBullets | bool | No | `false` | Show pagination dots |
  | breakpoints | object | No | `{}` | Responsive breakpoints |
- **Dependencies:** `swiper` (Swiper, Navigation, Pagination)

---

### Diff Viewer

#### CodeMirrorDiffViewer

- **Path:** `src/components1/common/DiffViewer.jsx`
- **Description:** Side-by-side code diff viewer using CodeMirror MergeView.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | originalCode | string | No | - | Original code |
  | newCode | string | No | - | Modified code |
- **Dependencies:** `codemirror` (basicSetup, EditorView), `@codemirror/merge`, `@codemirror/lang-javascript`

---

### Tooltip

#### CustomTooltip

- **Path:** `src/components1/common/CustomTooltip.tsx`
- **Description:** Styled MUI Tooltip wrapper with custom scrollable max-height and optional CSS class.
- **Props:** Extends `Omit<TooltipProps, 'children'>` plus:
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | children | ReactElement | Yes | - | Trigger element |
  | title | TooltipProps['title'] | Yes | - | Tooltip content |
  | tooltipStyle | CSSProperties | No | `{}` | Custom styles |
  | tooltipClassName | string | No | `''` | Custom CSS class |
  | placement | string | No | `'top'` | Tooltip position |
- **When to use:** Use instead of raw MUI Tooltip for consistent styling.
- **Dependencies:** Wraps MUI `Tooltip`.

---

### Utility

#### LazyLoadComponent

- **Path:** `src/components1/common/LazyLoadComponent.tsx`
- **Description:** Lazy-loads a React component when it becomes visible in the viewport using IntersectionObserver.
- **Props:**
  | Prop | Type | Required | Default | Description |
  |------|------|----------|---------|-------------|
  | component | () => Promise<{default: ComponentType}> | Yes | - | Dynamic import |
  | fallback | ReactNode | No | `<div>Loading...</div>` | Loading fallback |
  | props | Record<string, any> | No | `{}` | Props to pass to component |
  | threshold | number | No | `0.1` | Visibility threshold |
- **Example:**

```tsx
<LazyLoadComponent component={() => import('./HeavyChart')} props={{ data: chartData }} />
```

#### DownloadTarFile

- **Path:** `src/components1/common/DownloadTarFile.tsx`
- **Description:** Utility function (not a component) that downloads a base64-encoded string as a `.tar` file.
- **Usage:** `DownloadTarFile(base64String, filename)`

#### UserHistory

- **Path:** `src/components1/common/UserHistory.jsx`
- **Description:** Displays paginated user query history in a table, with popup and button trigger variants.
- **Exports:** `UserHistory` (named), `UserHistoryPopup` (named), `UserHistoryButton` (default)
- **Composition:** `UserHistoryButton` → `UserHistoryPopup` → `UserHistory`

---

### Integration Account Modals

These modals follow a common pattern: form dialog for adding/editing integration accounts.

| Component                   | Path                                                     | Integration                    |
| --------------------------- | -------------------------------------------------------- | ------------------------------ |
| K8sAccountModal             | `src/components1/common/K8sAccountModal.jsx`             | Kubernetes (multi-step wizard) |
| JiraAccountModal            | `src/components1/common/JiraAccountModal.jsx`            | Jira                           |
| GithubAccountModal          | `src/components1/common/GithubAccountModal.jsx`          | GitHub (App OAuth or token)    |
| ServiceNowAccountModal      | `src/components1/common/ServiceNowAccountModal.js`       | ServiceNow                     |
| DatadogAccountModal         | `src/components1/common/DatadogAccountModal.jsx`         | Datadog                        |
| ArgoCDAccountModal          | `src/components1/common/ArgoCDAccountModal.jsx`          | ArgoCD                         |
| PagerDutyAccountModal       | `src/components1/common/PagerDutyAccountModal.jsx`       | PagerDuty                      |
| IntegrationDynamicFormModal | `src/components1/common/IntegrationDynamicFormModal.jsx` | Generic (schema-driven)        |

**Common props pattern:**
| Prop | Type | Description |
|------|------|-------------|
| openModal | bool | Modal visibility |
| handleClose | func | Close handler |
| editConfig | object | Edit mode data (optional) |

---

### Settings Components

#### TenantSettings

- **Path:** `src/components1/common/TenantSettings.jsx`
- **Description:** Full tenant settings modal with Loki config, feature flags, domain login, and tenant name editing.

#### TenantAccountCommonSettings

- **Path:** `src/components1/common/TenantAccountCommonSettings.jsx`
- **Description:** Form section for editing Loki label mapper settings.

#### ApiTokens

- **Path:** `src/components1/common/ApiTokens.jsx`
- **Description:** Modal UI for creating, listing, and deleting user API tokens with usage instructions.

---

## Common Hooks

#### useUpdateAllClusterOption

- **Path:** `src/components1/common/UpdateDataContext.jsx`
- **Description:** Hook that fetches cloud accounts and updates the global cluster list in DataContext.
- **Also exports:** `transformClusters` (utility function for transforming cluster data).

---

## Utilities & Services

#### snackbarService

- **Path:** `src/components1/common/snackbarService.ts`
- **Description:** Pub/sub singleton service for showing snackbar notifications without requiring React context.
- **API:**

  ```ts
  import { snackbar } from '@components1/common/snackbarService';

  snackbar.success('Saved successfully');
  snackbar.error('Something went wrong');
  snackbar.warning('Approaching limit');
  snackbar.info('Update available');
  snackbar.show(message, severity, duration);
  ```

- **Type:** `SnackbarOptions = { message: string, severity: 'success' | 'info' | 'warning' | 'error', duration?: number }`
- **When to use:** Use for fire-and-forget notifications from anywhere (including non-React code). Pair with `SnackbarComponent` in the app root.

#### GetInsightIcon (utility function)

- **Path:** `src/components1/common/GetInsightIcon.jsx`
- **Description:** Returns an appropriate icon asset based on insight source type (`'Event'`, `'Recommendation'`, `'Metric'`).

#### UserMenuItems (factory functions)

- **Path:** `src/components1/common/layout/UserMenuItems.jsx`
- **Description:** Factory functions for generating user account menu items.
- **Exports:** `createGetMenuItem(handlers)`, `generateMenuItems({ switchAccountEnabled })`
