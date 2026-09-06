import { test, expect } from "@playwright/test";
import { EventSubTabs } from "../troubleshootEventsLocators";
import { openEventSubTab, expectSelectedSubTab } from "../troubleshootEventsHelper";
import {
  ALERT_TUNING_COLUMNS,
  CONFIDENCE_LABELS,
  DRILLDOWN_TABS,
  SOURCE_LABELS,
} from "./alertTuningLocators";
import {
  ALERT_TUNING_HASH,
  deepLinkAlertTuning,
  expectExclusiveListingState,
  openAlertTuning,
  settleListing,
} from "./alertTuningHelper";

// Alert Tuning — the Troubleshoot > All Events sub-tab that lists threshold suggestions
// for noisy alerts (app/src/components/triage/ThresholdSuggestionsManager.tsx, mounted by
// app/src/pages/troubleshoot/index.jsx at selectedSubTab === 5).
//
// Read-only throughout, deliberately. The module's one write action — applying a suggested
// threshold, which opens a pull request against the customer's alert definition
// (ThresholdApplyPanel.tsx, reached from the expanded row's Evidence tab) — is not
// exercised: this suite drives a shared live dev tenant and must not raise PRs on it. See
// "Follow-ups" in the PR.
//
// Every case is written to hold whether or not the tenant currently has suggestions: the
// listing settles into a table body or into <EmptyData>, and both are a valid contract.

// Evidence is the manager's first expandable tab (value 0), Recent Events its second.
const EVIDENCE_PANEL = 0;
const RECENT_EVENTS_PANEL = 1;

test(
  "Alert Tuning sanity - open Troubleshoot, open the Alert Tuning sub-tab, verify the listing renders with its Account, Source and Confidence filters, the CSV download control and all six columns",
  { tag: ["@dev", "@test", "@oss", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);

    await test.step("The listing and its toolbar mount", async () => {
      await expect(locators.listBox).toBeVisible({ timeout: 30000 });
      await expect(locators.thresholdToolbar).toBeVisible();
    });

    await test.step("All three filters render, Account among them because the page mounts the manager without an accountId", async () => {
      await expect(locators.accountFilter).toBeVisible();
      await expect(locators.thresholdSourceFilter).toBeVisible();
      await expect(locators.thresholdConfidenceFilter).toBeVisible();
    });

    await test.step("The toolbar offers the CSV download", async () => {
      await expect(locators.downloadBtn).toBeVisible();
    });

    await test.step("The table declares every column, including the multi-account Account column", async () => {
      for (const column of ALERT_TUNING_COLUMNS) {
        await expect(locators.columnHeader(column)).toBeVisible({ timeout: 30000 });
      }
    });
  }
);

test(
  "Alert Tuning - open /troubleshoot straight at the all-events/threshold-suggestions hash, verify the Alert Tuning sub-tab is the selected one and the suggestions listing mounts rather than Triage Rules",
  { tag: ["@dev", "@test", "@oss", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await deepLinkAlertTuning(page);

    await expectSelectedSubTab(locators.eventSubTab(EventSubTabs.alertTuning));
    await expect(locators.listBox).toBeVisible({ timeout: 60000 });
    await expect(locators.thresholdToolbar).toBeVisible();

    // The neighbouring sub-tab mounts its own ListingLayout, so its absence is what
    // proves the hash selected this manager rather than merely landing on the page.
    await expect(locators.triageRulesListBox).toHaveCount(0);
    await expect(page).toHaveURL(new RegExp(`#${ALERT_TUNING_HASH}\\b`));
  }
);

test(
  "Alert Tuning - open the Source filter, verify it offers exactly the five alert sources the module supports while no account narrows the list",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);

    await locators.thresholdSourceFilter.click();
    await expect(locators.optionRows.first(), "The Source filter panel did not open").toBeVisible({ timeout: 30000 });

    for (const source of SOURCE_LABELS) {
      await expect(locators.filterOption(source)).toBeVisible();
    }
    // The manager narrows SOURCE_OPTIONS to the selected accounts' platforms; with no
    // account chosen every source must still be on offer, so the count is asserted too.
    await expect(locators.optionRows).toHaveCount(SOURCE_LABELS.length);
  }
);

test(
  "Alert Tuning - filter by High confidence, then switch the same filter to Low, verify the trigger reports each choice in turn and the listing re-resolves under both",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);
    await settleListing(locators);

    await locators.selectSingleFilter(locators.thresholdConfidenceFilter, CONFIDENCE_LABELS[0]);
    await expect(locators.thresholdConfidenceFilter).toContainText(CONFIDENCE_LABELS[0]);
    await expectExclusiveListingState(locators, await settleListing(locators));

    // Single-select replaces rather than accumulates, so the earlier choice must be gone
    // from the trigger — not merely joined by the new one.
    await locators.selectSingleFilter(locators.thresholdConfidenceFilter, CONFIDENCE_LABELS[2]);
    await expect(locators.thresholdConfidenceFilter).toContainText(CONFIDENCE_LABELS[2]);
    await expect(locators.thresholdConfidenceFilter).not.toContainText(CONFIDENCE_LABELS[0]);
    await expectExclusiveListingState(locators, await settleListing(locators));
  }
);

test(
  "Alert Tuning - pick the first account in the Account filter, verify the accountIds query param is written to the URL, then clear it and verify the param is dropped",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);
    await settleListing(locators);
    await expect(page, "accountIds should not be set before anything is picked").not.toHaveURL(/[?&]accountIds=/);

    await locators.openAccountPanel();
    const accountLabel = await locators.firstAccountOptionLabel();
    expect(accountLabel, "The Account filter offered no account to pick").not.toBe("");

    // No explicit dismissal anywhere in this test: the selection's own router.push remounts
    // the dropdown, so the panel shuts itself. pickFirstAccountOption waits for exactly that.
    await locators.pickFirstAccountOption();

    await expect(locators.accountFilter).toContainText(accountLabel);
    await expect(page, "Selecting an account must publish it to the URL").toHaveURL(/[?&]accountIds=[^&#]+/);
    await expect(page, "The sub-tab hash must survive the filter push").toHaveURL(
      new RegExp(`#${ALERT_TUNING_HASH}\\b`)
    );

    // Reopen from scratch — the panel is shut, and its groups are collapsed again. Grouped
    // mode never hoists a chosen row into a "Selected" section, so the same first group
    // still leads with the same account; asserted before clicking rather than assumed.
    await locators.openAccountPanel();
    await expect(locators.optionRows.first()).toContainText(accountLabel);
    await locators.pickFirstAccountOption();

    // applyFiltersOnRouter deletes a parameter whose value is empty, so deselecting the
    // last account must remove accountIds outright rather than leave accountIds=.
    await expect(page, "Clearing the account must drop the param, not blank it").not.toHaveURL(/[?&]accountIds=/);
    await expect(page).toHaveURL(new RegExp(`#${ALERT_TUNING_HASH}\\b`));
  }
);

test(
  "Alert Tuning - narrow the listing by both Source and Confidence, verify it settles into either matching rows or the Alert Tuning empty-state hint and never shows both at once",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);
    await settleListing(locators);

    await locators.selectSingleFilter(locators.thresholdSourceFilter, "Prometheus");
    await expect(locators.thresholdSourceFilter).toContainText("Prometheus");

    await locators.selectSingleFilter(locators.thresholdConfidenceFilter, CONFIDENCE_LABELS[1]);
    await expect(locators.thresholdConfidenceFilter).toContainText(CONFIDENCE_LABELS[1]);

    // The header row renders either way, so the table alone proves nothing — the body and
    // the empty state are the two states, and exactly one of them may be on screen.
    await expectExclusiveListingState(locators, await settleListing(locators));
  }
);

test(
  "Alert Tuning - expand the first suggestion, verify the row opens onto the Evidence and Recent Events drilldown tabs with the Evidence panel showing, or that an empty listing offers no row to expand",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);
    const hasRows = await settleListing(locators);

    if (!hasRows) {
      // The tenant holds no suggestion right now. The contract then is that the listing
      // offers nothing to expand at all — asserted rather than skipped, so the case still
      // fails if an empty listing were ever to render a stray chevron.
      await expectExclusiveListingState(locators, false);
      await expect(locators.expandToggles).toHaveCount(0);
      return;
    }

    const toggle = locators.expandToggles.first();
    await expect(toggle).toBeVisible({ timeout: 30000 });
    await toggle.click();

    // CustomTable relabels the chevron 'Collapse row' once open, so the toggle's own
    // aria-expanded is read off the row rather than the stale 'Expand row' handle.
    await expect(locators.tableBody.getByRole("button", { name: "Collapse row" }).first()).toHaveAttribute(
      "aria-expanded",
      "true",
      { timeout: 30000 }
    );

    await expect(locators.drilldownTab(DRILLDOWN_TABS.evidence)).toBeVisible({ timeout: 30000 });
    await expect(locators.drilldownTab(DRILLDOWN_TABS.recentEvents)).toBeVisible();
    await expect(locators.drilldownPanel(EVIDENCE_PANEL)).toBeVisible({ timeout: 30000 });
  }
);

test(
  "Alert Tuning - expand a suggestion and switch its drilldown to Recent Events, verify that panel opens and the Evidence panel closes behind it, or that an empty listing offers no row to expand",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);
    const hasRows = await settleListing(locators);

    if (!hasRows) {
      await expectExclusiveListingState(locators, false);
      await expect(locators.expandToggles).toHaveCount(0);
      return;
    }

    await locators.expandToggles.first().click();
    await expect(locators.drilldownPanel(EVIDENCE_PANEL)).toBeVisible({ timeout: 30000 });

    await locators.drilldownTab(DRILLDOWN_TABS.recentEvents).click();

    // TabPanel toggles `hidden` rather than unmounting, so the swap is only real if the
    // outgoing panel goes away as the incoming one arrives.
    await expect(locators.drilldownPanel(RECENT_EVENTS_PANEL)).toBeVisible({ timeout: 30000 });
    await expect(locators.drilldownPanel(EVIDENCE_PANEL)).toBeHidden();
    await expect(locators.drilldownTab(DRILLDOWN_TABS.recentEvents)).toHaveAttribute("aria-selected", "true");
  }
);

test(
  "Alert Tuning - set the confidence filter, leave for Triage Rules and come back, verify the suggestions listing remounts with the confidence filter reset to unselected",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAlertTuning(page);
    await settleListing(locators);

    await locators.selectSingleFilter(locators.thresholdConfidenceFilter, CONFIDENCE_LABELS[0]);
    await expect(locators.thresholdConfidenceFilter).toContainText(CONFIDENCE_LABELS[0]);

    await openEventSubTab(locators, EventSubTabs.triageRules);
    await expect(locators.triageRulesToolbar).toBeVisible({ timeout: 30000 });
    await expect(locators.listBox).toHaveCount(0);

    await openEventSubTab(locators, EventSubTabs.alertTuning);
    await expect(locators.listBox).toBeVisible({ timeout: 30000 });

    // The page unmounts the manager when the sub-tab changes, so its filter state is
    // component state that does not survive — unlike the account filter, which the URL
    // carries. The trigger must be back to its bare label.
    await expect(locators.thresholdConfidenceFilter).not.toContainText(CONFIDENCE_LABELS[0]);
    await expect(locators.thresholdConfidenceFilter).toContainText("Confidence");
    await expectExclusiveListingState(locators, await settleListing(locators));
  }
);
