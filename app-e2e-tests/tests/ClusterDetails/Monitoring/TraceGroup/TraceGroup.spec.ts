import { test, expect } from "@playwright/test";
import {
  openTraceGroup,
  settleTraceGroup,
  openFilter,
  dismissFilter,
  searchResource,
  orderedByRequest,
  unmatchableResource,
  TRACE_GROUP_TAB_ID,
  TRACE_GROUP_FRAGMENT,
} from "./traceGroupHelper";

// Infra > K8s > cluster detail > Monitoring > Trace Group
// (#monitoring/grouping, app/src/components/k8s/details/KubernetesTracesGroupListing.tsx).
//
// The module is entirely read-only: it has no create, edit or delete surface. Every
// control here is a filter, a sort or a pagination change, all of which live in component
// state, so nothing in this spec writes to the shared dev tenant and the tests are
// order-free and safe to run twice. The one value that is typed in is a per-call unique
// string that cannot match a real span.
//
// Timeouts are set per test rather than once via test.describe.configure: the house
// standard (qa-automation-code-check P5) requires every spec to set its own
// test.setTimeout, so the per-test calls are the ones that stay.

test(
  "Trace Group sanity - open Infra > K8s, open the Monitoring section, click the Trace Group tab, verify the URL moves to the grouping fragment and the listing renders its toolbar and table",
  { tag: ["@dev", "@oss", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);

    await expect(page).toHaveURL(TRACE_GROUP_FRAGMENT);
    await expect(tg.TraceGroupBox).toBeVisible();
    await expect(tg.TraceGroupTable).toBeVisible();

    await test.step("The toolbar offers the span type filter and the resource search", async () => {
      await expect(tg.SpanTypeFilter).toBeVisible();
      await expect(tg.ResourceSearch).toBeVisible();
    });

    await test.step("The tab opens on the http span type, which is the component's default", async () => {
      await expect(tg.SpanTypeFilter).toHaveText(/http/i);
    });
  }
);

test(
  "Trace Group sanity - open the Trace Group tab, verify the listing renders the Total Request, Error Count, Source, Span, Target, Resource, Duration, P99, P95 and Max columns",
  { tag: ["@dev", "@oss", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);

    // Status Code is deliberately absent from this list: LISTING_HEADER drops that column
    // for an ES trace provider, so it is the one header that legitimately varies by env.
    for (const column of ["Total Request", "Error Count", "Source", "Span", "Target", "Resource", "Duration", "P99", "P95", "Max"]) {
      await expect(tg.columnHeader(column), `column "${column}" is missing from the trace group listing`).toBeVisible();
    }
  }
);

test(
  "Trace Group - open the Trace Group tab, search for a resource string no span can carry, verify the listing reports No Data Available and no results",
  { tag: ["@dev", "@oss", "@test", "@regression", "@search", "@negative"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);
    await searchResource(tg, unmatchableResource());

    // Both halves, because they come from different components. CustomTable swaps the
    // rows for its EmptyData heading, and renderPaginationOrViewAll returns null outright
    // at tableData.length === 0 — so the row-range caption going away IS the second
    // signal. (CustomTablePagination's own "No results found" branch never renders here;
    // see Follow-up 8.) A filter that silently kept the old rows fails on both.
    await expect(tg.NoDataHeading).toBeVisible({ timeout: 60000 });
    await expect(tg.ResultSummary).toHaveCount(0, { timeout: 60000 });
  }
);

test(
  "Trace Group - search for a resource string no span can carry, clear the search with the X, verify the listing returns to the result count it showed before the search",
  { tag: ["@dev", "@oss", "@test", "@regression", "@search"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);

    // Precondition, asserted rather than assumed: the round trip is only meaningful from
    // a listing that has rows to lose. On a window with no spans at all the caption is
    // absent — renderPaginationOrViewAll returns null at zero rows — and this fails here,
    // naming the reason, instead of further down on a confusing comparison.
    await expect(tg.ResultSummary, "no trace groups in the default window — nothing for the search to filter away").toBeVisible({
      timeout: 60000,
    });
    const before = (await tg.ResultSummary.innerText()).trim();

    await searchResource(tg, unmatchableResource());
    await expect(tg.NoDataHeading).toBeVisible({ timeout: 60000 });
    await expect(tg.ResultSummary).toHaveCount(0, { timeout: 60000 });

    await tg.ClearResourceSearch.click();

    await test.step("The field empties and the unfiltered listing comes back", async () => {
      await expect(tg.ResourceSearch).toHaveValue("");
      await expect(tg.NoDataHeading).toHaveCount(0, { timeout: 60000 });
      // Matched on the caption's FORM, not the count it held before: this runs against a
      // live tenant whose span count inside the fixed one-hour window can legitimately
      // move between the two reads, and pinning the exact number would flake on that.
      await expect(tg.ResultSummary).toHaveText(/of\s+[\d,]+\s+results$/, { timeout: 60000 });
      expect(before).toMatch(/of\s+[\d,]+\s+results$/);
    });
  }
);

test(
  "Trace Group - open the Span Type filter, switch from http to query, verify the trigger reads query and the listing refetches for the query span type",
  { tag: ["@dev", "@oss", "@test", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);
    await expect(tg.SpanTypeFilter).toHaveText(/http/i);

    await openFilter(page, tg.SpanTypeFilter);
    await tg.filterOption("query").click();

    await test.step("The trigger commits the new value and the listing settles on it", async () => {
      await expect(tg.SpanTypeFilter).toHaveText(/query/i, { timeout: 30000 });
      await expect(tg.SpanTypeFilter).not.toHaveText(/http/i);
      // Proof the change reached the data, not just the trigger label: the caption is
      // re-rendered from the new response's totalRows.
      await expect(tg.ResultSummary).toBeVisible({ timeout: 60000 });
    });
  }
);

test(
  "Trace Group - sort the listing by Duration, verify the trace grouping request is re-issued ordered by duration_ns instead of the default error_count",
  { tag: ["@dev", "@oss", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);

    // Armed before the click so the request cannot land before the listener attaches.
    const sorted = orderedByRequest(page, "duration_ns");
    await tg.columnHeader("Duration").click();
    await sorted;

    await expect(tg.ResultSummary).toBeVisible({ timeout: 60000 });
  }
);

test(
  "Trace Group - open the Destination Namespace filter, dismiss it with Escape, verify the panel closes and no destination namespace is applied",
  { tag: ["@dev", "@oss", "@test", "@regression", "@search"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);

    const before = (await tg.ResultSummary.innerText()).trim();

    await openFilter(page, tg.DestinationNamespaceFilter);
    await dismissFilter(page);

    await test.step("The cancel path leaves no side effect on the trigger or the listing", async () => {
      // The trigger shows its bare label while nothing is selected — ds/FilterDropdown
      // appends the chosen value to it only once hasSelection is true.
      await expect(tg.DestinationNamespaceFilter).toHaveText(/^\s*Destination Namespace\s*$/);
      await expect(tg.ResultSummary).toHaveText(before);
    });
  }
);

test(
  "Trace Group - move from Trace Group to the Traces sub-tab and back, verify the trace group listing is restored",
  { tag: ["@dev", "@oss", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);

    await tg.clickTab(tg.MonitoringDropdownTraces);

    await test.step("Leaving genuinely unmounts the grouping listing", async () => {
      // toHaveCount(0), not toBeHidden: the Monitoring body is keyed on the selected
      // sub-tab, so the listing is removed from the DOM rather than hidden.
      await expect(tg.TraceGroupBox).toHaveCount(0, { timeout: 60000 });
    });

    await tg.clickTab(TRACE_GROUP_TAB_ID);
    await settleTraceGroup(page, tg);

    await expect(tg.TraceGroupTable).toBeVisible();
    await expect(tg.SpanTypeFilter).toBeVisible();
  }
);

test(
  "Trace Group - reload the browser on the Trace Group URL, verify the Trace Group tab is still the open sub-tab after the reload",
  { tag: ["@dev", "@oss", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const tg = await openTraceGroup(page);
    const deepLink = page.url();

    await page.reload({ waitUntil: "domcontentloaded" });

    await test.step("The fragment survives the reload and the listing comes back behind it", async () => {
      await expect(page).toHaveURL(TRACE_GROUP_FRAGMENT, { timeout: 60000 });
      expect(page.url()).toBe(deepLink);
      await settleTraceGroup(page, tg);
      await expect(tg.TraceGroupTable).toBeVisible();
    });
  }
);
