# Multi-account dashboard panels

How a dashboard panel scoped to several accounts decides what to query and what
to show — what shipped, what was rejected, and why.

> Status: built and validated on branch `feat/stat-panel-per-account-values`,
> **not yet merged**. Scoped to the `metrics` datasource. No panel-schema field
> was added — the behaviour is hardcoded by datasource and type, so it stays
> reversible.

---

## Background: accounts are clusters

A Nudgebee `cloud_account` and a Kubernetes cluster are 1:1 — the UI's "cluster"
dropdown is literally a cloud-accounts list. There is **no central metrics store
for customer telemetry**: each cluster runs its own Prometheus/VictoriaMetrics,
and services-server pushes the PromQL down to that cluster's agent over the
relay, routed by a per-account queue. A newer path lets an account point at a
hosted Prometheus-compatible backend directly.

Two consequences drive everything below:

- **There is no federation layer.** No Thanos querier, no promxy, no VM
  multi-level select in the product path. A query cannot span clusters.
- **Fanning out across N accounts means N independent requests**, merged — if at
  all — in the browser.

The `__CLUSTER__` placeholder is often mistaken for a cluster *filter*. It is
not: it expands server-side to a comma-joined set of **equality** matchers built
from the account's `prometheusAdditionalLabels`, and exists to *narrow* one
account's series when several accounts share a backend. It cannot express
`cluster=~"a|b|c"`.

## What changed

Scoped to the **metrics** datasource only. `logs`, `traces`, the command
datasources and the `nudgebee` query engine are untouched — they already merge
per-account results their own way, or take an account list in one call.

### 1. Account selection

A panel scoped to N accounts queried exactly **one** — the first. Now:

- **One account configured** → that account is used, no choice to wait for.
- **Several configured, none picked** → every one of them is queried.
- **One picked from the panel's Account filter** → narrows to it; clearing goes
  back to all.

### The non-obvious part: there are two gates, and the obvious one is dead

Account scoping is decided in two places, and changing the obvious one has no
effect:

| | Where | Behaviour |
|---|---|---|
| Looks like the gate | `panelQueryAccounts()`'s `!filterIds.length` branch (`scoped.slice(0, 1)`) | **Dead on the render path** |
| Actually the gate | `effectiveFilterAccount()`, called from `DashboardPanel` | Returns `scoped[0].value` whenever nothing is picked |

`DashboardPanel` builds `accountFilter = [effectiveAccountId]`, which is never
empty — so `panelQueryAccounts` always takes the *non*-empty-filter branch. Its
unit tests pass `[]` directly, so they exercise a branch the app never reaches.

**Both must change, or nothing happens.** This bit twice during implementation:
the second time, `stat` fanned out correctly while `timeseries` silently kept
querying one account, because only the hook had been updated.

### 2. Stat and gauge add up, and show their working

A stat is one number, so a panel over four clusters has to either pick one —
which reads as the total and is not — or add them. It adds them.

```
Account A = 90, Account B = 0, Account C = 10   →   card shows 100
```

**Hovering the number breaks it down by account.** That is not a nicety; it is
the correctness mitigation. A total nobody can take apart is a number nobody can
check, and the adversarial review that rejected an earlier version of this
feature rejected it precisely because it had no breakdown.

Three rules hold the arithmetic honest:

- **A real zero counts.** Account B reporting `0` is an answer, and appears in
  both the total and the breakdown.
- **A failed account is never counted as zero.** It contributes nothing, is
  listed in the breakdown as "no answer", and the card's count reads
  "2 of 3 accounts" instead of "3 accounts". The hover names the account. Stat
  and gauge panels do not repeat it in the banner above the card (it first
  shipped as a bare asterisk, which nobody could read without hovering).
  A cluster that could not answer and a cluster that answered zero are the same
  arithmetic and completely different facts.
- **Several series from one account are summed**, not reduced to the first.

`gauge` shares this path — the dial is a stat with a bounded scale.

### 3. Charts merge, and every account keeps a share

`timeseries`, `bar` and `table` draw every account's series together, with the
account name prefixed onto the legend label — and, on a table, split out into
its own **Account** column, with the Series cell the metric alone.

The series cap changed from **global to per-account**. `capSeries` ranked one
pool by magnitude, which on a multi-account panel is a popularity contest
between accounts: a cluster whose numbers are an order of magnitude larger takes
every slot, and the quieter accounts vanish from the chart with nothing to say
they were ever queried.

`capSeriesByAccount` divides the chart's budget evenly and ranks each account
inside its own share, with a floor of one series per account:

| Account | Series matched | Drawn |
|---|---|---|
| A | 177 | 6 |
| B | 200 | 6 |
| C | 350 | 6 |

> Showing 6 of 177 from A, 6 of 200 from B and 6 of 350 from C. Add an
> aggregation such as sum by (…), or filter to one account, to see the rest.

Each trimmed account is named with its real count, so the message cannot claim
an account was trimmed when it showed everything it had — 177 and 5 against a
share of 10 draws 10 and 5, and only the first is trimmed. Past three trimmed
accounts the list stops being readable and it summarises instead.

A single-account panel degenerates to the old behaviour and keeps the old
message; `"each of 1 accounts"` is both wrong and worse.

### 4. Line charts draw the consolidated view alongside the parts

Per-account lines answer "how is each cluster doing". They do not answer "how
much is there in all", which is what a panel scoped to every cluster is usually
asking — and the stat panel already answers with a sum. So a `timeseries` panel
spanning several accounts also draws the series that adds them up, **dashed and
heavier**, on the **same axis**:

| Query shape | Per-account lines | Consolidated lines |
|---|---|---|
| `sum(...)` — one unlabelled series per account | one per account | one, `All accounts` |
| `sum by (namespace) (...)` | one per account × namespace | one per namespace, `All accounts · <namespace>` |
| per-pod — labels differ per cluster | one per pod | none: a total of one line is that line twice |

`consolidatedSeries()` in `panelSeries.ts` groups the aligned series by the
label the accounts share once the account prefix is stripped, and sums each
group that at least two accounts contribute to. It runs in `DashboardPanel`'s
drawing, not in `usePanelData`, so `statTotal` never sees a total and cannot
count it twice.

Two decisions worth knowing:

- **Same axis, not a second one.** The total is the same unit as its parts; a
  second y-axis would let it be read against a different scale than the lines
  it is the sum of.
- **A gap in one account is skipped, not fatal.** 95 + gap + 10 is 105 — the
  sum of what was reported. The total is `null` only where every account has a
  gap. (It first shipped breaking on any gap; on fleets where clusters miss the
  odd scrape that left the line full of holes.)

The total is computed from the series the cap **kept**, so on a capped chart
it is the sum of what is drawn, which the cap warning already qualifies.

`bar` panels need no total: the bar chart is stacked, so each account is a
segment and the stack's height already is the consolidated view. A total series
would double it.

The total's colour is the chart's automatic palette, like every other line —
naming one colour on one dataset switches Chart.js's `Colors` plugin off for the
rest. The dash and weight are what mark it out.

### 5. One account filter for the whole dashboard

Each panel's header had its own single-select Account picker, and nothing
above them. Comparing clusters meant picking the same account in every panel
in turn. The dashboard toolbar now carries one **Accounts** filter, applied to
every panel at once:

- **Options are the union of the panels' scopes**, resolved against what the
  viewer can see — not every account the viewer has. An account no panel here
  queries could only blank the page. Hidden when the union is one account.
- **Multi-select, empty = no filter** (the toolbar-filter convention). Every
  panel then shows all of its accounts, as before.
- **Each panel's own picker survives and narrows within the selection.** Its
  options are the panel's scope less whatever the dashboard filter left out, so
  the two compose rather than compete. When the dashboard filter leaves a panel
  exactly one account, the picker shows that account as its selection — the
  viewer sees which account the panel is showing and why. Two or more is not a
  selection a single-select picker can show, so it stays empty.
- **The panel's chip names what it is showing.** Unfiltered it reads the scope
  (`All K8S`, `3 accounts`); narrowed it reads the account name, two names, or
  `n of N accounts` past two, with the full scope on hover.
- **A panel none of the picked accounts belong to** shows the existing filter
  message, and its "Show all accounts" action clears the *dashboard* filter —
  unless the panel's own pick caused the miss, in which case it clears that.
- Client state only, like the time range: not stored on the dashboard, not in
  the URL.

Wiring: `DashboardView` holds `accountFilter: string[]` and passes it to every
`SortablePanel` → `DashboardPanel` as `dashboardAccountIds`, with one stable
`onClearDashboardFilter`. `DashboardPanel` applies it before its own picker
(`applyAccountFilter(resolvePanelAccounts(...), dashboardAccountIds)`) and hands
`usePanelData` the panel's pick when there is one, else the dashboard's list —
so a miss surfaces through the hook's existing `filter` error rather than a
panel that quietly queries nothing.

### Files

| File | Change |
|---|---|
| `panelAccounts.ts` | `effectiveFilterAccount` gains `allAccounts` — an unmade choice stays empty rather than defaulting to the first account. `singleCall` renamed `allAccounts` |
| `usePanelData.ts` | metrics fans out to every scoped account; `PanelData` carries `failedAccounts`; per-account capping; "No **answer** from …" replaces "No **data** from …", which implied the account replied |
| `panelSeries.ts` | `statTotal()` — the sum, the per-account rows behind it, and a `partial` flag. Series carry a structured `accountLabel` |
| `panelBounds.ts` | `capSeriesByAccount()` + `accountCappedWarning()` |
| `DashboardPanel.tsx` | stat and gauge render one total with a hover breakdown and an on-card "n of N accounts" caveat; a timeseries draws `consolidatedSeries()` dashed beside the per-account lines |
| `DashboardView.tsx`, `SortablePanel.tsx` | the dashboard-level Accounts filter, threaded to every panel as `dashboardAccountIds` |

### Validation

- `npm run lint2` clean (oxlint, tsc, check-tdz, prettier)
- `jest src/components/k8s/dashboards` — 23 suites / 335 tests pass
- Full suite: **42 failing suites on the untouched base, 40 with the change** —
  zero new failures (that baseline is red for unrelated reasons)
- Tests cover the 90/0/10 sum, the hover breakdown, a real zero, a failed
  account excluded from the total and named in the hover, the 177/200/350
  per-account cap, single-account panels unchanged, and `logs` **not** fanning out

## What the review rejected, and what survived

An earlier design proposed a general `account_mode: 'first' | 'split' |
'combined'` field on the `Panel` contract. The adversarial review returned
**REVISE** and the field was **not built** — the behaviour above is hardcoded by
datasource and type instead, so nothing new is written into tenant JSONB,
`dashboard_versions`, or exported dashboard files.

That matters, because the review's third objection was that a stored setting
whose absent value means two different things is effectively permanent: changing
it later would require rewriting every saved dashboard and every exported file
in the wild. Hardcoding the behaviour keeps it reversible.

### Objections the current design answers

- **"A combined total hides a failed account."** The hover breakdown plus the
  asterisk make the missing account visible. This was the strongest objection
  and the design now has an answer to it.
- **"Time grids across independent backends won't line up."** Stat and gauge use
  *instant* queries — one point, nothing to align. And the range queries that do
  merge send an identical `start` / `end` / `step` to every account, so the
  Prometheus-family backends return the same grid.
- **"One loud account crowds out the others."** Fixed by per-account capping —
  which was a pre-existing bug in the global cap, not something this work
  introduced.

### Objections that remain live

**Summing assumes the parts add.** True of the `sum(...)` / `count(...)` an
aggregate stat query is written as. **Not** true of an average, a percentile, or
a ratio — `avg` across four clusters summed gives a meaningless number roughly
four times too large. Nothing enforces this; the frontend has no PromQL parser,
and ES, Datadog, CloudWatch and Dynatrace have no comparable "outer aggregation"
to inspect. The breakdown makes it *visible* — the parts sit next to the total —
but visible is not prevented.

**The request bound no longer holds.** `panelQueue` caps 4 *panels*
concurrently; each admitted metrics panel now fires one request per account. A
30-cluster tenant opening a type-scoped dashboard could see 4 × 30 = 120
concurrent requests, and `PANEL_TIMEOUT_MS` is one `AbortController` per panel —
if the tail queues past 30s it aborts all of them, discarding answers that had
already arrived. Datadog bills per call. **No cap was added**, because
requirement 3 says explicitly to query every configured account; capping which
accounts are asked would contradict it. Capping *concurrency* would not, and is
the obvious follow-up.

**Per-account failures are still under-detected.** See the gaps table below.

## Known gaps this work surfaced

Each is separate from the change above and none is fixed by it.

| Gap | Detail |
|---|---|
| **Per-account query failures are invisible** | A 200 with `Payload: []` + `Error` set is read as "no data". Affects the shipped `split` work too: an account that *failed* looks identical to one with nothing to report |
| **Mixed precision in one card** | `formatValue` switches at `abs >= 100`, so `272` and `37.00` sit side by side. Pre-existing, but only visible once values are adjacent |
| **`cappedSeriesWarning` advice is wrong for split** | It says "add an aggregation such as `sum by (…)`", which is wrong when the aggregation is already there and the fan-out is the cause |
| **The `nudgebee` rollup exception is neutered the same way** | The query-engine `singleCall` exception is defeated by the same `effectiveFilterAccount` default, so a rollup panel scoped to five accounts may show one account's rows under a title claiming all five — exactly the failure its own doc comment warns about. Unverified; worth checking on its own |

## Follow-ups worth deciding

1. **Cap the concurrency, not the accounts.** Move the per-account requests into
   `panelQueue` so its 4-slot bound counts requests rather than panels. This is
   the highest-value follow-up and does not contradict requirement 3.
2. **Warn on a non-additive stat query.** Even a regex that recognises
   `avg(` / `histogram_quantile(` / a `/` at the top level and shows "these
   accounts may not be summable" would stop the worst silent case.
3. **Close the failed-account detection gap** (gaps table above) — it now feeds
   a total, so it matters more than it did.
