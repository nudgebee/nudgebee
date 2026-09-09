// Not for OSS
import { test, expect } from "@playwright/test";
import {
  ALL_EVENT_SUB_TABS,
  EventSubTabs,
  InvestigationSubTabs,
} from "./troubleshootEventsLocators";
import {
  openTroubleshoot,
  openEventSubTab,
  openInvestigationSubTab,
  deepLinkTroubleshoot,
  expectSelectedSubTab,
} from "./troubleshootEventsHelper";

// Troubleshoot (app/src/pages/troubleshoot/index.jsx) — the All Events strip, the
// Investigations sub-tabs and the page's hash canonicalisation.
//
// Read-only throughout. The one write action this module offers, Create Rule, is
// not reachable from /troubleshoot at all: TriageRulesManager only renders that
// button when it is given an accountId, and this page mounts it without one
// (isMultiAccountView). See "Follow-ups" in the PR.

// A name no triage rule can carry, so the empty-state assertion is about the
// search filtering rather than about what the shared dev tenant happens to hold.
const NO_MATCH_RULE_NAME = `zz-no-such-rule-${Date.now()}`;

test(
  "Troubleshoot sanity - open Troubleshoot from the sidebar, verify all seven All Events sub-tabs render and Triage Inbox is the one selected",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);

    await test.step("The strip offers every sub-tab filterOptions declares", async () => {
      for (const tab of ALL_EVENT_SUB_TABS) {
        await expect(locators.eventSubTab(tab)).toBeVisible({ timeout: 30000 });
        await expect(locators.eventSubTab(tab)).toContainText(tab.label);
      }
    });

    await test.step("Triage Inbox is the landing sub-tab", async () => {
      await expectSelectedSubTab(locators.eventSubTab(EventSubTabs.triageInbox));
      await expect(locators.eventSubTab(EventSubTabs.triageRules)).toHaveAttribute("aria-selected", "false");
    });
  }
);

test(
  "Troubleshoot - open the Triage Rules sub-tab, verify the rules toolbar renders and the URL carries the all-events/triage-rules hash",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);
    await openEventSubTab(locators, EventSubTabs.triageRules);

    await expect(locators.triageRulesToolbar).toBeVisible({ timeout: 30000 });
    await expect(locators.triageRulesSearch).toBeVisible();
    await expect(locators.triageRulesStatusFilter).toBeVisible();
    await expect(page).toHaveURL(/#all-events\/triage-rules\b/);
  }
);

test(
  "Troubleshoot - open Triage Rules, search for a rule name that cannot exist, verify the listing drops every row and shows the No Data Available empty state",
  { tag: ["@dev", "@regression", "@search"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);
    await openEventSubTab(locators, EventSubTabs.triageRules);
    await expect(locators.triageRulesToolbar).toBeVisible({ timeout: 30000 });

    await locators.triageRulesSearch.fill(NO_MATCH_RULE_NAME);
    // The search is applied client-side off this input's value (filteredRules in
    // TriageRulesManager.tsx), so the committed value is the signal that it ran.
    await expect(locators.triageRulesSearch).toHaveValue(NO_MATCH_RULE_NAME);

    await expect(locators.triageRulesEmptyState).toBeVisible({ timeout: 30000 });
    // No matches swaps the table out for <EmptyData>, so the body element goes away
    // entirely rather than merely rendering zero rows.
    await expect(locators.triageRulesTableRows).toHaveCount(0);
  }
);

test(
  "Troubleshoot - open Triage Rules, search for a rule name that cannot exist, clear the search, verify the listing returns to the row count it had before the search",
  { tag: ["@dev", "@regression", "@search"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);
    await openEventSubTab(locators, EventSubTabs.triageRules);
    await expect(locators.triageRulesToolbar).toBeVisible({ timeout: 30000 });
    // Settle the first render before the baseline is read — a count taken while the
    // rules are still loading would be compared against a fully-loaded table later.
    await expect(locators.triageRulesTableRows.first().or(locators.triageRulesEmptyState)).toBeVisible({
      timeout: 60000,
    });
    const baselineRows = await locators.triageRulesTableRows.count();

    await locators.triageRulesSearch.fill(NO_MATCH_RULE_NAME);
    await expect(locators.triageRulesEmptyState).toBeVisible({ timeout: 30000 });

    await locators.triageRulesSearch.fill("");
    await expect(locators.triageRulesSearch).toHaveValue("");

    await expect(locators.triageRulesTableRows).toHaveCount(baselineRows, { timeout: 30000 });
  }
);

test(
  "Troubleshoot - open the Alert Tuning sub-tab, verify the threshold suggestions toolbar renders with its Source and Confidence filters",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);
    await openEventSubTab(locators, EventSubTabs.alertTuning);

    await expect(locators.thresholdToolbar).toBeVisible({ timeout: 30000 });
    await expect(locators.thresholdSourceFilter).toBeVisible();
    await expect(locators.thresholdConfidenceFilter).toBeVisible();
    await expect(page).toHaveURL(/#all-events\/threshold-suggestions\b/);
  }
);

test(
  "Troubleshoot - open the Event Resolutions sub-tab, verify the resolutions listing renders with its CSV download control",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);
    await openEventSubTab(locators, EventSubTabs.eventResolutions);

    await expect(locators.eventResolutionsListBox).toBeVisible({ timeout: 30000 });
    await expect(locators.eventResolutionsDownload).toBeVisible();
    await expect(page).toHaveURL(/#all-events\/event-resolutions\b/);
  }
);

test(
  "Troubleshoot - open the Investigations tab, switch to Manual Investigated, verify that sub-tab is selected and the URL carries the investigations/manual-investigated hash",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);
    await openInvestigationSubTab(locators, InvestigationSubTabs.manualInvestigated);

    await expect(page).toHaveURL(/#investigations\/manual-investigated\b/);
    await expect(locators.investigationSubTab(InvestigationSubTabs.autoInvestigated)).toHaveAttribute(
      "aria-selected",
      "false"
    );
  }
);

test(
  "Troubleshoot - deep-link an unknown top-level hash, verify the page rejects it and rewrites the URL to all-events/fingerprint",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await deepLinkTroubleshoot(page, "no-such-tab");

    // troubleshoot/index.jsx canonicalises an unresolvable top-level fragment back
    // to the first tab and its first sub-tab, so a stale link cannot dead-end.
    await expect(page).toHaveURL(/#all-events\/fingerprint\b/, { timeout: 60000 });
    await expectSelectedSubTab(locators.eventSubTab(EventSubTabs.triageInbox));
  }
);

test(
  "Troubleshoot - deep-link the Investigations tab with an unknown sub-tab hash, verify the page rewrites the URL to investigations/auto-investigated",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await deepLinkTroubleshoot(page, "investigations/no-such-sub-tab");

    // A known parent with an unknown child falls back to that parent's own first
    // sub-tab, not to All Events — the parent selection has to survive.
    await expect(page).toHaveURL(/#investigations\/auto-investigated\b/, { timeout: 60000 });
    await expectSelectedSubTab(locators.investigationSubTab(InvestigationSubTabs.autoInvestigated));
  }
);

test(
  "Troubleshoot - open the Triage Rules sub-tab then go back in the browser, verify Triage Inbox is restored and the triage-rules hash is gone",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openTroubleshoot(page);
    await openEventSubTab(locators, EventSubTabs.triageRules);
    await expect(page).toHaveURL(/#all-events\/triage-rules\b/);

    await page.goBack();

    await expect(page).not.toHaveURL(/#all-events\/triage-rules\b/, { timeout: 60000 });
    await expectSelectedSubTab(locators.eventSubTab(EventSubTabs.triageInbox));
  }
);
