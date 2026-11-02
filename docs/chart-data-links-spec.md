# Click a chart point to open the related page — spec

Fixes #39367. Part of the Grafana-parity epic #39358 (Tier 2, "drill-down from charts").

## Problem

A spike on a timeseries panel is where an investigation starts, but clicking it
does nothing. Table panels already carry per-row links (`renderRowUrl` in
`app/src/components/k8s/dashboards/panelColumns.ts`, validated server-side by
`validateTableOptions` in `api-server/services/dashboard/table_columns.go`);
chart panels don't. Grafana users expect "click a point → open this pod's logs
at this time", and it shortens the path from a dashboard to Nudgebee's own
investigation pages.

## Goal / how we know it worked

An author configures links (title + relative in-app URL template) on a
timeseries panel. In view mode, clicking a point opens a small menu of those
links with the clicked **series' labels**, the **account**, and the **time
range** substituted into each URL.

## Decisions (from /challenge: alternative + adversary passes)

- **D1. Links are a panel-wide `options.data_links` facet, not per-series.**
  Mirrors Grafana (one link list applies to every point) and the existing
  `options.columns` facet. `Panel.options` is an open intersection ending in
  `Record<string, unknown>`, so an unknown `data_links` on an old client is
  carried through, not dropped (forward-compat). A separate facet (not reusing
  `options.columns`) is **forced**: `validateTableOptions` rejects column
  settings on non-table panels, and charts have no result columns.

- **D2. Token resolution lives in `DashboardPanel`, not in the shared
  `LineCharts`.** (adversary #2 + alternative) `DashboardPanel` is the only
  place that holds all three inputs a real destination needs: the clicked
  series' label map, its account id, and the panel's time range. `LineCharts`
  stays a generic chart — it only *surfaces* the click. We do **not** generalize
  the `onAskNubi` pinned-point overlay (five separate `onAskNubi` gates, a
  `label.includes('usage')` hack, app-wide blast radius) and we do **not**
  hand-roll a dropdown (DS non-negotiable) — the menu is a DS `Menu` of DS
  `Link`s owned by `DashboardPanel`.

- **D3. The token set is derived from the real destination URLs, not from
  Grafana's vocabulary.** (adversary #1 — the load-bearing fix) Verified: every
  workload/detail/logs destination embeds the **account UUID in the path**
  (`/kubernetes/details/{accountId}?…`, e.g.
  `KubernetesClusterSummaryUtilization.jsx:322`), which PromQL series labels
  never carry; and time-reading pages consume a **range pair** (`start_time`
  **and** `end_time`, e.g. `troubleshoot/index.jsx:199-200`), not an instant.
  A `{{label}}`-only + single-`{{__time}}` token set cannot build any of the
  three advertised links. So the token set is:

  | Token | Source | Reaches |
  |---|---|---|
  | `{{<labelname>}}` (`{{pod}}`, `{{namespace}}`, `{{instance}}`, …) | clicked series `metric` map | the workload a line is about |
  | `{{__account_id}}` | `idByLabel.get(series.accountLabel)`, or `accounts[0].value` on a single-account panel | `/kubernetes/details/{{__account_id}}` path |
  | `{{__from}}` / `{{__to}}` | panel's rendered `startTime` / `endTime` | logs / traces range params |
  | `{{__time}}` | clicked point epoch (ms) | point-specific deep links |

  Example an author can now write:
  `/kubernetes/details/{{__account_id}}?namespace={{namespace}}&pod={{pod}}&start_time={{__from}}&end_time={{__to}}#logs`

- **D4. No `renderChartLinkUrl` — reuse `renderRowUrl`.** (alternative) The
  `{{…}}` regex matches `\w`, so `__account_id` / `__from` resolve as ordinary
  tokens. The reserved tokens are injected as synthetic "columns":
  `renderRowUrl(link.url, [...Object.keys(metric), '__account_id', '__from', '__to', '__time'], [...Object.values(metric), accountId, from, to, time])`.
  The security-critical fill + `isSafeLinkUrl` guard stays in exactly one place;
  null-on-missing-token and percent-encoding come for free.

- **D5. Facet lifecycle is explicit.** (adversary #3) `changeType` in
  `PanelEditorModal.tsx` **strips `data_links`** when a panel leaves
  `timeseries` (mirroring the `thresholds`/`combine` strip), so the editor never
  holds a facet the panel can't render. Server-side `validateDataLinks`
  **rejects** `data_links` on a non-timeseries panel (symmetric to the table
  rule) — defense-in-depth for imported/hand-edited definitions. Editor-strip +
  server-reject are consistent: you cannot save a stat panel carrying links.

- **D6. Metrics datasource only.** Of the datasources that can be drawn as a
  timeseries (`metrics`, `traces`), only `metrics` returns a real series with
  points — `runTracePanel` (`usePanelData.ts`) always returns table data with
  `series: []`, so a traces "timeseries" chart is empty and a data link there
  could never fire. The editor only shows the card on a `metrics` + `timeseries`
  panel, and `validateDataLinks` rejects links outside that pair.

- **D6. Scope limits are stated, not silently dropped.** Grafana import
  (`grafanaImport.ts`) does **not** translate Grafana's `fieldConfig.defaults.links`
  (a different macro dialect) — imported dashboards don't auto-gain chart links;
  this spec claims editor-authored parity only. The editor shows no
  unknown-placeholder warning (chart label names aren't knowable until the query
  runs), so a link whose token is absent simply doesn't appear (renderRowUrl →
  null).

## Data flow

```
provider payload (series.metric)                       panel startTime/endTime
        │ toRawSeries (preserve metric)                         │
        ▼                                                       │
   RawSeries.metric ─ snapToStep/alignSeries (forward) ─┐       │
        │                                               ▼       │
   shapeMetrics: idByLabel → PanelSeries.accountId    PanelSeries{metric,accountId}
        │                                               │       │
        ▼                                               ▼       ▼
   DashboardPanel: drawn[datasetIndex] ───────────► build token arrays
        ▲                                               │
   onDataPointClick{datasetIndex,index,epoch,x,y} ◄─ LineCharts (surface only)
        │                                               ▼
        └──────────────────────────► renderRowUrl per data_link → DS Menu of DS Links
```

## File-by-file

| File | Change |
|---|---|
| `app/src/api1/dashboards/types.ts` | `PanelDataLink{title,url}` + `data_links?: PanelDataLink[]` on the `options` intersection (~line 269). |
| `app/src/components/k8s/dashboards/panelSeries.ts` | `metric?: Record<string,string>` on `RawSeries`; preserve in `toRawSeries` (268); forward in `alignSeries` (335). |
| `app/src/components/k8s/dashboards/usePanelData.ts` | `metric?` + `accountId?` on `PanelSeries`; set `accountId` per series in `shapeMetrics` from existing `idByLabel` (687). |
| `app/src/components/common/charts/LineCharts.jsx` | Extend dormant `onDataPointClick` payload: `datasetIndex,index,epochTimestamp,x,y`; add `onDataPointClick` to the `pointHitRadius` bump (dashboard lines are `pointRadius:0`). **No touch** to `onAskNubi`/pinned-point. |
| `app/src/components/k8s/dashboards/DashboardPanel.tsx` | Pass `onDataPointClick` to the timeseries `Chart.Line` (~751); map `datasetIndex`→`drawn[i]`→metric+accountId; build token arrays; `renderRowUrl` per link; render anchored DS `Menu` of DS `Link`s; filter null hrefs. |
| `app/src/components/k8s/dashboards/PanelEditorModal.tsx` | New "Data links" card gated on a `metrics` `timeseries` panel, `patchDataLinks` like `patchThresholds` (563); strip `data_links` in `changeType` (580-590); help text lists tokens. |
| `api-server/services/dashboard/table_columns.go` (or sibling) | `validateDataLinks(p)` reusing `validLinkUrl` (200-210): each link needs non-empty title + safe relative url; reject `data_links` outside a metrics timeseries panel. |
| `api-server/services/dashboard/service.go` | Call `validateDataLinks(p)` from `ValidateDefinition` (~86). |

## Known hazards

- **Single-point off-by-one.** `handleClick` reads `timestamps?.[index]` against
  the unpadded prop while single-point charts pad labels/data — `{{__time}}`
  would be off by one there. Mitigation: range tokens (`{{__from}}`/`{{__to}}`)
  are the primary time mechanism; index `{{__time}}` against the aligned axis, or
  omit it for single-point series.
- **Nearest-point ambiguity.** With `interaction.mode='index'` the tooltip shows
  all series; `getElementAtEvent` picks the nearest within 10px, which may differ
  from the line the user thought they clicked. Acceptable (same as the existing
  Ask-Nubi path); the menu header names the series so the binding is visible.

## Acceptance criteria

1. Add a data link in a timeseries panel editor → in view mode a point click
   opens a menu listing the link(s).
2. On a **multi-account** panel, the clicked series' link carries its **own**
   account id (not the first account's).
3. A logs link using `{{__from}}`/`{{__to}}` opens a non-empty time window.
4. Saving a link with a `javascript:` or off-site URL is rejected by
   `validateDataLinks` (same error shape as a bad table link).
5. Switching a panel from `timeseries` to `stat` strips `data_links` — no
   stranded JSON in the saved definition (verify via `EditDashboardJsonModal`).

## Validation & e2e obligations (Definition of Done)

- `cd app && npm run lint2 && npm run type-check`; `api-server/services`: `make validate`.
- `cd app-e2e-tests && node scripts/e2e-impact.js` after the editor card lands;
  reconcile locators for the new card's testids; run the touched spec area
  against dev and put output in the PR.
- Observe the feature once (screenshot of the point-click menu + a working
  drill-down) in the PR body.
