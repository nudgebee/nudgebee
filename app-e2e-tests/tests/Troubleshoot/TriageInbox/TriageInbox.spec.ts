// Not for OSS
import { test, expect } from "@playwright/test";
import { openEventSubTab } from "../troubleshootEventsHelper";
import { EventSubTabs } from "../troubleshootEventsLocators";
import { openTriageInbox, requireRows, returnToTriageInbox } from "./triageInboxHelper";
import {
  INBOX_COLUMNS,
  ISSUE_TYPE_ALL,
  ISSUE_TYPE_NEW,
  ISSUE_TYPE_RECURRING,
  SEVERITY_HIGH_LABEL,
  SEVERITY_HIGH_VALUE,
  SORT_BY_EVENT_COUNT,
  CLASSIFY_OPTIONS,
} from "./triageInboxConstants";

test(
  "Triage Inbox sanity - open Troubleshoot from the sidebar, verify Triage Inbox is the selected sub-tab and its Severity, Status, Triage Priority, Issue Type, Sort By and Triage Status filters and download control render",
  { tag: ["@dev", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);

    // The sub-tab's own aria-selected is the assertion, not the URL: a bare
    // /troubleshoot opens Triage Inbox WITHOUT writing #all-events/fingerprint, which
    // only appears once a tab is explicitly picked or a deep link is rewritten.
    await expect(locators.eventSubTab(EventSubTabs.triageInbox)).toHaveAttribute("aria-selected", "true");
    await expect(page).toHaveURL(/\/troubleshoot/);

    await expect(locators.eventTypeFilter).toBeVisible();
    await expect(locators.sourceFilter).toBeVisible();
    await expect(locators.severityFilter).toBeVisible();
    await expect(locators.statusFilter).toBeVisible();
    await expect(locators.triagePriorityFilter).toBeVisible();
    await expect(locators.issueTypeFilter).toBeVisible();
    await expect(locators.sortByFilter).toBeVisible();
    await expect(locators.triageStatusFilter).toBeVisible();
    await expect(locators.downloadBtn).toBeVisible();

    // The listing resolved to one of its two real states. waitForListing already
    // proved the id'd body attached, so this reads the outcome rather than a spinner.
    await expect(locators.tableBody.or(locators.emptyState).first()).toBeVisible();
  }
);

test(
  "Triage Inbox sanity - open the tab, verify the table exposes the Severity, Application, Event Type, Count, Triage Score, Triage Status, Alert Status and Action columns",
  { tag: ["@dev", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    await requireRows(locators);

    const headers = await locators.headerNames();
    for (const column of INBOX_COLUMNS) {
      expect(headers, `The Triage Inbox table is missing its "${column}" column`).toContain(column);
    }
  }
);

test(
  "Triage Inbox - filter Issue Type by New Issues, verify the issueType=new query param is added while the Triage Inbox sub-tab stays selected and every listed row carries the NEW chip",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    await requireRows(locators);

    await locators.waitForListRequest(async () => {
      await locators.selectFilterOption(locators.issueTypeFilter, ISSUE_TYPE_NEW);
    });

    // Issue Type is the one inbox filter written back to the router, so the query param
    // is the durable outcome here. The sub-tab must survive that rewrite, and its own
    // aria-selected is what proves it — applyFiltersOnRouter rebuilds the URL, and a
    // default landing carries no hash for it to preserve in the first place.
    await expect(page).toHaveURL(/[?&]issueType=new\b/);
    await expect(locators.eventSubTab(EventSubTabs.triageInbox)).toHaveAttribute("aria-selected", "true");

    // is_new_issue drives both the filter and the NEW chip, so with the filter on,
    // chip count and row count must agree. Compared as counts rather than asserted per
    // row so a partially rendered listing cannot pass by matching only its first rows.
    const rowCount = await locators.rows.count();
    await expect(locators.tableBody.getByTestId("new-issue-chip")).toHaveCount(rowCount);
    if (rowCount === 0) {
      await expect(locators.emptyState, "New Issues returned no rows, so the empty panel must be what replaced the table").toBeVisible();
    }
  }
);

test(
  "Triage Inbox - filter Issue Type by Recurring Issues, verify no listed row carries the NEW chip, reset to All Issues, verify the issueType param is dropped and the original row count returns",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    const baseline = await requireRows(locators);

    await locators.waitForListRequest(async () => {
      await locators.selectFilterOption(locators.issueTypeFilter, ISSUE_TYPE_RECURRING);
    });

    await expect(page).toHaveURL(/[?&]issueType=recurring\b/);
    // Recurring is the exact complement of New (is_new_issue: false), so the chip that
    // marks a new issue must not appear on a single row.
    await expect(locators.tableBody.getByTestId("new-issue-chip")).toHaveCount(0);
    expect(await locators.rows.count(), "Recurring Issues is a subset of All Issues").toBeLessThanOrEqual(baseline);

    await locators.waitForListRequest(async () => {
      await locators.selectFilterOption(locators.issueTypeFilter, ISSUE_TYPE_ALL);
    });

    // applyFiltersOnRouter drops empty values, so All Issues removes the param outright
    // rather than writing issueType=all.
    await expect(page).not.toHaveURL(/[?&]issueType=/);
    await expect(locators.rows).toHaveCount(baseline);
  }
);

test(
  "Triage Inbox - filter Severity by High, verify every listed row's severity marker reads HIGH, reload the page, verify the unfiltered listing returns",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    const baseline = await requireRows(locators);

    await locators.waitForListRequest(async () => {
      await locators.selectFilterOption(locators.severityFilter, SEVERITY_HIGH_LABEL);
    });

    const rowCount = await locators.rows.count();
    expect(rowCount, "Filtering Severity by High returned no rows, so there is nothing to check the marker on").toBeGreaterThan(0);
    expect(rowCount, "High is a subset of the unfiltered listing").toBeLessThanOrEqual(baseline);

    // Every returned group matched priority=HIGH, and HIGH takes top precedence in the
    // rendered severity, so each row must show the HIGH marker — one per row.
    await expect(locators.rows.getByRole("img", { name: SEVERITY_HIGH_VALUE, exact: true })).toHaveCount(rowCount);

    // Severity is held in component state and never written to the router, unlike Issue
    // Type — so a reload is what proves it was not persisted.
    await page.reload();
    await locators.waitForListing();
    await expect(page).not.toHaveURL(/[?&]priority=/);
    await expect(locators.rows).toHaveCount(baseline);
  }
);

test(
  "Triage Inbox - sort by Event Count, verify the Count column comes back in descending order",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    const baseline = await requireRows(locators);
    expect(baseline, "Ordering cannot be observed on a single row").toBeGreaterThan(1);

    await locators.waitForListRequest(async () => {
      await locators.selectFilterOption(locators.sortByFilter, SORT_BY_EVENT_COUNT);
    });

    // onSortByChange always applies 'desc', and the Event Count option maps to
    // fingerprint_event_count — exactly the value the Count column renders.
    const rendered = await locators.columnValues("Count");
    expect(rendered.length, "The sorted listing rendered no Count cells to compare").toBeGreaterThan(1);

    // An event count is a non-negative integer, so digits are stripped out rather than
    // digits-plus-sign-and-point: a cell that rendered a dash or a spinner then cleans
    // to "" and is caught here. Number("") is 0, which would otherwise slide through as
    // a real count and make the ordering check meaningless.
    const counts = rendered.map((value) => {
      const cleaned = value.replace(/[^0-9]/g, "");
      expect(cleaned, `The Count column rendered a non-numeric value: ${rendered.join(", ")}`).not.toBe("");
      return Number(cleaned);
    });
    const descending = [...counts].sort((a, b) => b - a);
    expect(counts).toEqual(descending);
  }
);

// The per-row drill-down is deliberately NOT covered here. Runs 519-521 each showed the
// first row expanding (aria-expanded flips to "true") while the panel below it mounts
// nothing at all — no tab strip and no listing — on every attempt and every retry. That
// looks like a defect in the app rather than in a locator, so no test is shipped
// asserting it either way; it is written up under Follow-ups in the PR instead.

test(
  "Triage Inbox - open the first row's Classify menu, verify it offers True Positive and False Positive, press Escape, verify the menu closes and the row's Triage Status is unchanged",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    await requireRows(locators);

    const firstRow = locators.rows.first();
    const before = await locators.cellValue(firstRow, "Triage Status");

    const trigger = locators.rowClassifyTrigger(firstRow);
    await expect(trigger, "The Classify action is disabled — this account cannot classify events on this cluster").toBeEnabled();
    await trigger.click();

    for (const option of CLASSIFY_OPTIONS) {
      await expect(locators.menuItem(option), `The Classify menu did not offer "${option}"`).toBeVisible({ timeout: 30000 });
    }

    // ds/DropdownMenu closes through the overlay's own key handler, so the key has to
    // land on a node inside the menu — a page-level press leaves it open.
    await locators.menuItem(CLASSIFY_OPTIONS[0]).press("Escape");
    await expect(locators.menuItem(CLASSIFY_OPTIONS[0])).toBeHidden({ timeout: 30000 });

    // The cancel path must leave no side effect: no classification was chosen, so the
    // row's triage status has to be byte-identical to what it was before the menu opened.
    expect(await locators.cellValue(firstRow, "Triage Status")).toBe(before);
  }
);

test(
  "Triage Inbox - switch to the Triage Rules sub-tab and back to Triage Inbox, verify the inbox table unmounts and then reloads with its original row count",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    const baseline = await requireRows(locators);

    await openEventSubTab(locators, EventSubTabs.triageRules);
    await expect(page).toHaveURL(/#all-events\/triage-rules\b/);
    // The inbox table is unmounted, not hidden — its absence is what proves the sub-tab
    // really swapped rather than layering a second listing on top.
    await expect(locators.table).toHaveCount(0, { timeout: 30000 });

    await returnToTriageInbox(locators);
    await expect(page).toHaveURL(/#all-events\/fingerprint\b/);
    await expect(locators.rows).toHaveCount(baseline);
  }
);

test(
  "Triage Inbox - open the first row's Investigate action, verify the investigate page opens for that row's event, go back, verify the Triage Inbox listing is restored",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTriageInbox(page);
    const baseline = await requireRows(locators);

    const action = locators.rowInvestigateLink(locators.rows.first());
    await expect(action).toBeVisible();
    const href = (await action.getAttribute("href")) ?? "";
    const eventId = new URLSearchParams(href.split("?")[1] ?? "").get("id") ?? "";
    expect(eventId, `The row's Investigate action carried no event id — href was "${href}"`).not.toBe("");

    await action.click();
    // The action is an ordinary <a>, so the assertion is that the app routed to the
    // investigate page for the very event the row was about, not merely that it moved.
    await expect(page).toHaveURL(new RegExp(`/investigate\\?[^#]*\\bid=${eventId}\\b`), { timeout: 60000 });

    await page.goBack();
    await locators.waitForListing();
    await expect(page).toHaveURL(/\/troubleshoot/);
    await expect(locators.eventSubTab(EventSubTabs.triageInbox)).toHaveAttribute("aria-selected", "true");
    await expect(locators.rows).toHaveCount(baseline);
  }
);
