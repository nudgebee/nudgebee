// Not for OSS
import { test, expect } from "@playwright/test";
import { openAuditsTab, withAuditsCapture, pickPresentValue, pickPresentPair, predicate } from "./auditsHelper";
import {
  COLUMN,
  COLUMN_HEADERS,
  FILTER_LABEL,
  IMPOSSIBLE_PAIR,
  toDisplayLabel,
} from "./auditsConstants";

// The Audits tab of /user-management (app/src/components/audits/index.jsx).
//
// Read-only by construction: the module renders a filtered listing of audit
// records and has no create, edit or delete surface at all. Nothing here writes
// tenant state, so every case is safe to run twice and safe to run beside
// anything else on the shared dev cluster. The one preference it does persist —
// the table page size — lives in this browser context's localStorage, so it
// cannot leak into another test.
//
// The dev cluster's audit log is live and its contents are not fixed, so nothing
// below asserts a particular record. The assertions are invariants instead: the
// rendered table agrees with the payload it rendered from, a filter reaches the
// query and narrows the rows to match, and an impossible filter pair empties the
// table. Where a case needs a value to filter on, it takes one the unfiltered
// page actually returned rather than hardcoding one that may have no rows.
test.describe.configure({ timeout: 180000 });
test.beforeEach(() => {
  test.setTimeout(180000);
});

test(
  "Audits sanity - open the Audits tab of user management, verify the tab is selected and the listing renders its six filters and seven column headers",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const { locators } = await openAuditsTab(page);

    await test.step("The listing card and its table are mounted", async () => {
      await expect(locators.listingCard).toBeVisible();
      await expect(locators.table).toBeVisible();
    });

    await test.step("Every toolbar filter the module declares is offered", async () => {
      for (const key of Object.keys(FILTER_LABEL) as Array<keyof typeof FILTER_LABEL>) {
        await expect(locators.filterTrigger(key), `the ${FILTER_LABEL[key]} filter should render`).toBeVisible();
      }
    });

    await test.step("The table declares the seven audit columns in order", async () => {
      for (const header of COLUMN_HEADERS) {
        await expect(locators.columnHeader(header), `the ${header} column should render`).toBeVisible();
      }
    });
  }
);

test(
  "Audits sanity - open the Audits tab, verify the rendered rows match the audits_v2 payload the table rendered from, row for row",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const { locators, capture } = await openAuditsTab(page);

    expect(
      capture.rows.length,
      "the dev cluster should have logged at least one audit in the default 24h window — every case below needs a row to assert against"
    ).toBeGreaterThan(0);

    // Compared against the very response this render came from, not a second
    // query: a fresh call could return different rows on a live cluster and the
    // mismatch would be the test's own doing.
    await expect(locators.dataRows).toHaveCount(capture.rows.length);
    await expect(locators.columnCells(COLUMN.action)).toHaveText(capture.rows.map((row) => toDisplayLabel(row.event_action)));
    await expect(locators.columnCells(COLUMN.status)).toHaveText(capture.rows.map((row) => toDisplayLabel(row.event_status)));
  }
);

test(
  "Audits - filter the log by an action the log actually holds, verify the query carries the event_action predicate and every Action cell reads that action",
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    const { locators, capture } = await openAuditsTab(page);
    const action = pickPresentValue(capture.rows, "event_action");

    const filtered = await withAuditsCapture(page, () => locators.chooseFilter("action", toDisplayLabel(action)));

    expect(filtered.queryText, "the action filter must reach the audits query").toContain(predicate("event_action", "_eq", action));
    expect(filtered.rows.length, `filtering on ${action} must not empty a log that contained it`).toBeGreaterThan(0);
    expect(filtered.rows.every((row) => row.event_action === action)).toBe(true);

    await expect(locators.columnCells(COLUMN.action)).toHaveText(filtered.rows.map(() => toDisplayLabel(action)));
  }
);

test(
  "Audits - filter the log by a status the log actually holds, verify the query carries the event_status predicate and every Status chip reads that status",
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    const { locators, capture } = await openAuditsTab(page);
    const status = pickPresentValue(capture.rows, "event_status");

    const filtered = await withAuditsCapture(page, () => locators.chooseFilter("status", toDisplayLabel(status)));

    expect(filtered.queryText, "the status filter must reach the audits query").toContain(predicate("event_status", "_eq", status));
    expect(filtered.rows.length, `filtering on ${status} must not empty a log that contained it`).toBeGreaterThan(0);
    expect(filtered.rows.every((row) => row.event_status === status)).toBe(true);

    await expect(locators.columnCells(COLUMN.status)).toHaveText(filtered.rows.map(() => toDisplayLabel(status)));
  }
);

test(
  "Audits - apply an action filter and a status filter that co-occur in the log, verify the query carries both predicates and every row satisfies both",
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    const { locators, capture } = await openAuditsTab(page);
    // Taken as a pair rather than independently: two values that each exist but
    // never appear together would return nothing and prove nothing.
    const { action, status } = pickPresentPair(capture.rows);

    await withAuditsCapture(page, () => locators.chooseFilter("action", toDisplayLabel(action)));
    const filtered = await withAuditsCapture(page, () => locators.chooseFilter("status", toDisplayLabel(status)));

    expect(filtered.queryText).toContain(predicate("event_action", "_eq", action));
    expect(filtered.queryText).toContain(predicate("event_status", "_eq", status));
    expect(filtered.rows.length, `${action}/${status} co-occurred on the unfiltered page, so the filtered page must hold rows`).toBeGreaterThan(0);
    expect(filtered.rows.every((row) => row.event_action === action && row.event_status === status)).toBe(true);

    await expect(locators.columnCells(COLUMN.action)).toHaveText(filtered.rows.map(() => toDisplayLabel(action)));
    await expect(locators.columnCells(COLUMN.status)).toHaveText(filtered.rows.map(() => toDisplayLabel(status)));
  }
);

test(
  "Audits - filter by a category and an event type that cannot occur on one record, verify the query carries both predicates and the table shows the No Data Available empty state",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    const { locators } = await openAuditsTab(page);

    await withAuditsCapture(page, () => locators.chooseFilter("category", IMPOSSIBLE_PAIR.categoryLabel));
    const filtered = await withAuditsCapture(page, () => locators.chooseFilter("eventType", IMPOSSIBLE_PAIR.eventTypeLabel));

    expect(filtered.queryText).toContain(predicate("event_category", "_ilike", IMPOSSIBLE_PAIR.categoryValue));
    expect(filtered.queryText).toContain(predicate("event_type", "_ilike", IMPOSSIBLE_PAIR.eventTypeValue));
    expect(filtered.rows, "a ticket record can never carry a K8s relay event type").toHaveLength(0);

    await expect(locators.emptyState).toBeVisible();
    await expect(locators.emptyState).toHaveText("No Data Available");
    await expect(locators.dataRows).toHaveCount(0);
    // CustomTable.jsx:939 drops the footer entirely once the table has no rows,
    // so the row-range summary must be gone rather than reading zero.
    await expect(locators.paginationSummary).toHaveCount(0);
  }
);

test(
  "Audits - search the Category filter for a prefix, verify only categories containing it are offered and an unmatchable query shows No results found",
  { tag: ["@dev", "@regression", "@search", "@negative"] },
  async ({ page }) => {
    const { locators } = await openAuditsTab(page);

    await locators.filterTrigger("category").click();
    // Category holds 24 options, which is above FilterDropdown's eight-option
    // threshold, so this panel is one of the two that render a search box.
    const search = locators.filterSearchInput();
    await expect(search).toBeVisible();

    await test.step("A prefix narrows the list to matching categories only", async () => {
      await search.fill("auto");
      // Polled rather than read once: the unfiltered list is already on screen
      // when the query is typed, so a single read can capture all 24 options
      // before the panel re-renders and pass on the wrong list.
      await expect
        .poll(
          async () => {
            const labels = await locators.visibleOptions().allTextContents();
            return labels.length > 0 && labels.every((label) => label.toLowerCase().includes("auto"));
          },
          { message: "the panel should settle on Auto pilot, Auto runbook and Automation" }
        )
        .toBe(true);
    });

    await test.step("A query no category can match reports no results instead of an empty panel", async () => {
      await search.fill("no-such-audit-category");
      await expect(locators.noOptionsMessage()).toBeVisible();
      await expect(locators.visibleOptions()).toHaveCount(0);
    });

    // Escape is FilterDropdown's own close path (FilterDropdown.jsx:1029-1032),
    // so the panel is dismissed without committing a value.
    await page.keyboard.press("Escape");
    await expect(locators.filterSearchInput()).toHaveCount(0);
  }
);

test(
  "Audits - apply a status filter then clear it from the trigger, verify the trigger drops the value and the reissued query carries no event_status predicate",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const { locators, capture } = await openAuditsTab(page);
    const status = pickPresentValue(capture.rows, "event_status");
    const label = toDisplayLabel(status);

    const filtered = await withAuditsCapture(page, () => locators.chooseFilter("status", label));
    expect(filtered.queryText).toContain(predicate("event_status", "_eq", status));

    const cleared = await withAuditsCapture(page, () => locators.clearFilterValue("status"));

    // Asserted against the predicate, not the bare field name: event_status is
    // also one of the fields LIST_AUDIT_EVENTS selects, so it is present in
    // every query string whether or not the filter is applied.
    expect(cleared.queryText, "clearing the filter must drop its predicate, not just blank the trigger").not.toContain(
      predicate("event_status", "_eq", status)
    );
    await expect(locators.filterTrigger("status")).not.toContainText(label);
    // Compared as an inequality, not an equality against the first load: the
    // audit log is live, so the unfiltered total may legitimately have grown.
    expect(cleared.count).toBeGreaterThanOrEqual(filtered.count);
  }
);

test(
  "Audits - expand the first audit row, verify it reports itself expanded and reveals the Diff State panel, then collapse it back",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const { locators, capture } = await openAuditsTab(page);
    expect(capture.rows.length, "expanding a row needs at least one row").toBeGreaterThan(0);

    const toggle = locators.expandToggle(0);
    await expect(toggle).toHaveAttribute("aria-expanded", "false");

    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    // 'Diff State' is the tab AuditsTable declares for every audit row
    // (audits/index.jsx:707); the Command tab is filtered out unless the row is
    // a CLI_EXECUTE, so it is the only one guaranteed here.
    await expect(locators.listingCard.getByText("Diff State", { exact: true }).first()).toBeVisible();

    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
  }
);

test(
  "Audits - set the table page size to 5 and reload the page, verify the preference survives the reload and the listing requests and renders five rows",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const { locators, capture } = await openAuditsTab(page);
    expect(capture.rows.length, "paging needs a populated log").toBeGreaterThan(0);

    const resized = await withAuditsCapture(page, async () => {
      await locators.pageSizeTrigger.click();
      await locators.pageSizeOption("5").click();
    });

    expect(resized.variables.limit, "choosing 5 rows must reissue the query with limit 5").toBe(5);
    const expectedRows = Math.min(5, resized.count);
    await expect(locators.dataRows).toHaveCount(expectedRows);
    await expect(locators.paginationSummary).toHaveText(new RegExp(`Showing\\s+1-${expectedRows}\\s+of\\b`));

    // The page size is stored under nudgebee.userPreferences
    // (app/src/api1/user/index.js:515), so a reload proves it persisted rather
    // than living in component state.
    //
    // reload(), not goto(AUDITS_PATH): the page is already on that exact URL, so
    // a goto to it is a same-document fragment navigation — nothing remounts and
    // no query is ever reissued, which is what made this wait time out.
    const reloaded = await withAuditsCapture(page, async () => {
      await page.reload({ waitUntil: "domcontentloaded" });
    });

    expect(reloaded.variables.limit, "the stored page size must be what the reloaded table asks for").toBe(5);
    await expect(locators.dataRows).toHaveCount(Math.min(5, reloaded.count));
  }
);

test(
  "Audits - leave Audits for the Users tab and return, verify Audits is reselected and the audit listing is rebuilt",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const { locators } = await openAuditsTab(page);

    await test.step("The Users tab replaces the audit listing", async () => {
      await locators.usersTab.click();
      await expect(locators.usersTab).toHaveAttribute("data-tab-selected", "true");
      await expect(locators.table).toHaveCount(0);
    });

    await test.step("Returning reselects Audits and refetches the log", async () => {
      const returned = await withAuditsCapture(page, () => locators.auditsTab.click());
      await expect(locators.auditsTab).toHaveAttribute("data-tab-selected", "true");
      await expect(locators.table).toBeVisible();
      await expect(locators.dataRows).toHaveCount(returned.rows.length);
    });
  }
);
