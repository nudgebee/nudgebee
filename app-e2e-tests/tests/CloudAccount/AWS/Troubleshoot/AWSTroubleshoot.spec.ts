// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openTroubleshootSubTab,
  goToSubTab,
  rootOf,
  ruleRowCount,
  suggestionRowCount,
  eventRowCount,
  uniqueRuleName,
  deleteRuleNamed,
  TROUBLESHOOT_TIMEOUT_MS,
} from "./awsTroubleshootHelper";
import {
  EVENTS_TOOLBAR,
  RULES_TOOLBAR,
  SUGGESTIONS_TOOLBAR,
  EVENTS_FILTER,
  RULES_FILTER,
  SUGGESTIONS_FILTER,
  EVENTS_HEADERS,
  RULES_HEADERS,
  SUGGESTIONS_HEADERS,
  RULES_COLUMN,
  RULE_STATUS_OPTIONS,
  CONFIDENCE_OPTIONS,
  NO_CRITERIA_ERROR,
} from "./awsTroubleshootLocators";

// Cloud Account > AWS > Troubleshoot — Events, Triage Rules and Alert Tuning
// (app/src/pages/cloud-account/details/[CloudAccountDetails].jsx:647-649).
//
// Read-only apart from one create-and-delete round trip. That rule is given a name no other
// suite uses and an alert-name pattern nothing can match, so it is inert while it exists, and
// it is removed in the same test — including when a later step fails.

// A string no event message or rule name on the shared account can carry, so the empty-listing
// assertions are about the filtering rather than about what dev happens to hold.
const NO_MATCH_TERM = `zz-no-such-record-${Date.now()}`;

test(
  "Cloud Troubleshoot sanity - open AWS Troubleshoot, verify the Events listing renders its search, six filters and column headers",
  { tag: ["@dev", "@test", "@oss", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "events");

    await test.step("The toolbar and its event search are present and empty", async () => {
      await expect(t.toolbar(EVENTS_TOOLBAR)).toBeVisible();
      await expect(t.eventsSearch).toBeVisible();
      await expect(t.eventsSearch).toHaveValue("");
    });

    await test.step("All six toolbar filters are offered", async () => {
      for (const key of Object.keys(EVENTS_FILTER) as (keyof typeof EVENTS_FILTER)[]) {
        await expect(t.eventsFilterTrigger(key), `the ${key} filter should render`).toBeVisible();
      }
    });

    await test.step("The listing declares its column contract", async () => {
      // The seventh header is deliberately blank (the row-action column), so it is not asserted.
      for (const header of EVENTS_HEADERS) {
        await expect(t.eventsRoot.locator("th", { hasText: header }).first()).toBeVisible();
      }
    });
  }
);

test(
  "Cloud Troubleshoot - search AWS events for a message no event carries, verify the listing reports no data",
  { tag: ["@dev", "@test", "@oss", "@regression", "@search", "@negative"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "events");

    await eventRowCount(t);

    // The search commits on Enter, not on keystroke — onEnterPress is what promotes the typed
    // text to appliedSearchByMessage and refetches (CloudAccountEvents.tsx:302-306).
    await t.eventsSearch.fill(NO_MATCH_TERM);
    await expect(t.eventsSearch).toHaveValue(NO_MATCH_TERM);
    await t.eventsSearch.press("Enter");

    // The empty state is required rather than a row count of 0: the table renders no <tbody>
    // while a fetch is in flight, so "no rows" is also what the refetch itself looks like.
    // renderEmptyState returns null while loading (CustomTable.jsx:917), so this node appears
    // only once the search has come back and genuinely returned nothing.
    await expect(t.eventsEmptyState).toBeVisible({ timeout: 60000 });
    await expect(t.eventRows).toHaveCount(0);
  }
);

test(
  "Cloud Troubleshoot - move from Events to Triage Rules and back to Events, verify each sub-tab replaces the other",
  { tag: ["@dev", "@test", "@oss", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "events");

    await test.step("Triage Rules replaces the events listing", async () => {
      await goToSubTab(t, page, "triage-rules");
      await expect(page).toHaveURL(t.TroubleshootTriageRulesUrl);
      await expect(rootOf(t, "events")).toBeHidden();
    });

    await test.step("Going back restores the events listing and unmounts the rules panel", async () => {
      await goToSubTab(t, page, "events");
      await expect(page).toHaveURL(t.TroubleshootEventsUrl);
      await expect(rootOf(t, "triage-rules")).toBeHidden();
      await expect(t.toolbar(EVENTS_TOOLBAR)).toBeVisible();
    });
  }
);

test(
  "Cloud Troubleshoot sanity - open Triage Rules, verify the listing renders its name search, Rule Type and Status filters, System Rules toggle and column headers",
  { tag: ["@dev", "@test", "@oss", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "triage-rules");

    await test.step("The toolbar and its name search are present and empty", async () => {
      await expect(t.toolbar(RULES_TOOLBAR)).toBeVisible();
      await expect(t.rulesSearch).toBeVisible();
      await expect(t.rulesSearch).toHaveValue("");
    });

    await test.step("Both filters and the System Rules toggle are offered", async () => {
      for (const key of Object.keys(RULES_FILTER) as (keyof typeof RULES_FILTER)[]) {
        await expect(t.rulesFilterTrigger(key), `the ${key} filter should render`).toBeVisible();
      }
      // Checked by default — includeSystemRules starts true (TriageRulesManager.tsx:76).
      await expect(t.systemRulesToggle).toBeChecked();
    });

    await test.step("The listing declares its column contract", async () => {
      const rows = await ruleRowCount(t);
      test.skip(rows === 0, "the dev AWS account has no triage rules, so the table renders its empty state instead of headers");

      // The ninth header is deliberately blank (the row-menu column), so it is not asserted.
      for (const header of RULES_HEADERS) {
        await expect(t.rulesRoot.locator("th", { hasText: header }).first()).toBeVisible();
      }
    });
  }
);

test(
  "Cloud Troubleshoot - search Triage Rules by an existing rule name, verify only rules matching that name are listed",
  { tag: ["@dev", "@test", "@oss", "@regression", "@search"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "triage-rules");

    const baseline = await ruleRowCount(t);
    test.skip(baseline === 0, "the dev AWS account has no triage rules, so there is no name for the search to match");

    // Taken from the listing rather than hardcoded, so the assertion is about the filtering and
    // not about a rule this environment happens to carry. The Name cell also holds a "System"
    // chip for system rules (TriageRulesManager.tsx:315-319), so the searchable name is the
    // first text line, not the whole cell.
    const firstCell = ((await t.ruleColumnCells("name").first().innerText()) ?? "").trim();
    const ruleName = firstCell.split("\n")[0].trim();
    expect(ruleName, "the first rule row should carry a name to search for").not.toBe("");

    await t.rulesSearch.fill(ruleName);
    await expect(t.rulesSearch).toHaveValue(ruleName);

    // The search filters client-side on name (TriageRulesManager.tsx:97-100), so no refetch is
    // in flight and the listing settles as soon as React re-renders. Polled rather than read
    // once so the assertion grades the filtered rows, not the pre-filter ones.
    await expect
      .poll(
        async () => {
          const names = await t.ruleColumnCells("name").allTextContents();
          return names.length > 0 && names.every((n) => n.toLowerCase().includes(ruleName.toLowerCase()));
        },
        { timeout: 30000, message: `every listed rule should carry "${ruleName}" in its name` }
      )
      .toBe(true);

    await expect(t.ruleRows).not.toHaveCount(0);
  }
);

test(
  "Cloud Troubleshoot - filter Triage Rules by Status Enabled, verify every listed rule reports Enabled",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "triage-rules");

    const baseline = await ruleRowCount(t);
    test.skip(baseline === 0, "the dev AWS account has no triage rules, so there is no row for the filter to keep or drop");

    await t.chooseFilterOption(t.rulesFilterTrigger("status"), RULE_STATUS_OPTIONS[0]);

    // Exact match, not "contains": a system rule disabled for this account renders
    // "Disabled (Override)" (TriageRulesManager.tsx:304), which a substring check on "Enabled"
    // would not catch but a substring check on "Disabled" would wrongly flag.
    //
    // An empty listing is a legitimate outcome here (no rule is enabled), so the poll accepts
    // it only once the empty state confirms the filter finished rather than that rows are
    // mid-render.
    await expect
      .poll(
        async () => {
          const statuses = await t.ruleColumnCells("status").allTextContents();
          if (statuses.length === 0) {
            return await t.rulesEmptyState.isVisible();
          }
          return statuses.every((s) => s.trim() === RULE_STATUS_OPTIONS[0]);
        },
        { timeout: 30000, message: "every listed rule should report Enabled, or the listing should be empty" }
      )
      .toBe(true);
  }
);

test(
  "Cloud Troubleshoot - filter Triage Rules by Rule Type Suppression, verify every listed rule reports Suppression",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "triage-rules");

    const baseline = await ruleRowCount(t);
    test.skip(baseline === 0, "the dev AWS account has no triage rules, so there is no row for the filter to keep or drop");

    await t.chooseFilterOption(t.rulesFilterTrigger("ruleType"), "Suppression");

    // Unlike Status, this filter is served by the API — rule_type is passed to getTriageRules
    // (TriageRulesManager.tsx:265), so a refetch is in flight and a single read would grade the
    // pre-filter rows. The empty branch requires the empty state for the same reason as above.
    await expect
      .poll(
        async () => {
          const types = await t.ruleColumnCells("type").allTextContents();
          if (types.length === 0) {
            return await t.rulesEmptyState.isVisible();
          }
          return types.every((v) => v.trim() === "Suppression");
        },
        { timeout: 30000, message: "every listed rule should report Suppression, or the listing should be empty" }
      )
      .toBe(true);
  }
);

test(
  "Cloud Troubleshoot - turn the System Rules toggle off, verify no listed rule is tagged System and that turning it back on restores them",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "triage-rules");

    const baseline = await ruleRowCount(t);
    test.skip(baseline === 0, "the dev AWS account has no triage rules, so there is nothing for the toggle to include or exclude");

    // System rules are the ones the Name cell tags with a "System" chip
    // (TriageRulesManager.tsx:315-319) — the only place the table distinguishes them.
    const namesBefore = await t.ruleColumnCells("name").allTextContents();
    const systemCount = namesBefore.filter((n) => n.includes("System")).length;
    test.skip(systemCount === 0, "the dev AWS account carries no system triage rules, so the toggle has nothing to hide");

    await t.systemRulesToggle.uncheck();
    await expect(t.systemRulesToggle).not.toBeChecked();

    // Filtered client-side (TriageRulesManager.tsx:89), so no refetch is in flight.
    await expect
      .poll(
        async () => {
          const names = await t.ruleColumnCells("name").allTextContents();
          if (names.length === 0) {
            return await t.rulesEmptyState.isVisible();
          }
          return names.every((n) => !n.includes("System"));
        },
        { timeout: 30000, message: "no listed rule should be tagged System once the toggle is off" }
      )
      .toBe(true);

    await t.systemRulesToggle.check();
    await expect(t.systemRulesToggle).toBeChecked();
    await expect(t.ruleRows).toHaveCount(baseline, { timeout: 30000 });
  }
);

test(
  "Cloud Troubleshoot - open Create Rule and submit with no match criterion, verify the match criterion error and that cancelling adds no rule",
  { tag: ["@dev", "@test", "@oss", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "triage-rules");

    const baseline = await ruleRowCount(t);
    test.skip(!(await t.createRuleBtn.isVisible()), "the run user has no write access to the AWS account, so Create Rule is not offered");

    await t.createRuleBtn.click();
    const dialog = t.createRuleDialog;
    await expect(dialog).toBeVisible({ timeout: 20000 });

    // Rule Type is pre-selected as Suppression (TriageRuleModal.tsx:81), so the earlier
    // "Please select a rule type" branch is unreachable from the UI and the match criterion is
    // the first refusal a person can actually trigger. Asserted here so the default is part of
    // the contract rather than an assumption.
    await expect(dialog.getByRole("tab", { name: "Suppression" })).toHaveAttribute("aria-selected", "true");

    await t.dialogSubmit(dialog, /^Create Rule$/).click();

    await expect(t.toast("alert", new RegExp(NO_CRITERIA_ERROR))).toBeVisible({ timeout: 20000 });
    // The refusal happens before the API call, so the form must still be open to correct.
    await expect(dialog).toBeVisible();

    await t.dialogCancel(dialog).click();
    await expect(dialog).toBeHidden({ timeout: 20000 });

    // The cancel path must leave the listing exactly as it was, not merely close the form.
    await expect(t.ruleRows).toHaveCount(baseline, { timeout: 30000 });
  }
);

test(
  "Cloud Troubleshoot - create a suppression rule with a unique name and an alert-name pattern, verify it persists in the listing, then delete it and verify the row is gone",
  { tag: ["@dev", "@test", "@oss", "@regression", "@crud"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "triage-rules");

    await ruleRowCount(t);
    test.skip(!(await t.createRuleBtn.isVisible()), "the run user has no write access to the AWS account, so Create Rule is not offered");

    const ruleName = uniqueRuleName();

    try {
      await t.createRuleBtn.click();
      const dialog = t.createRuleDialog;
      await expect(dialog).toBeVisible({ timeout: 20000 });

      await t.dialogField(dialog, "Rule Name").fill(ruleName);
      // An alert-name regex that matches nothing real, so the rule suppresses no event on this
      // shared account for the few seconds it exists.
      await t.dialogField(dialog, "Alert Name (regex)").fill(`NudgebeeE2ENeverMatches-${ruleName}`);

      await t.dialogSubmit(dialog, /^Create Rule$/).click();
      await expect(dialog).toBeHidden({ timeout: 60000 });

      // Searched rather than looked for on the open page, because the listing paginates at the
      // user's page size: on an account whose first page is already full, the new rule lands on
      // a later page and a total-row count never grows. The search filters the whole rule set
      // before pagination (TriageRulesManager.tsx:97-100), so this puts the row on page 1
      // wherever it would otherwise have sorted.
      await t.rulesSearch.fill(ruleName);
      await expect(t.rulesSearch).toHaveValue(ruleName);

      // The success toast is not the proof — the row is. onSuccess refetches the list for a
      // created rule (TriageRulesManager.tsx:495-500), so the rule having survived the round
      // trip to the server is what puts it here.
      await expect(t.ruleRowNamed(ruleName)).toHaveCount(1, { timeout: 60000 });

      // Read back what the server stored, rather than trusting the values just typed.
      await expect(t.ruleRowNamed(ruleName).first().locator(`td:nth-child(${RULES_COLUMN.type + 1})`)).toHaveText("Suppression");
      await expect(t.ruleRowNamed(ruleName).first().locator(`td:nth-child(${RULES_COLUMN.status + 1})`)).toHaveText("Enabled");

      await deleteRuleNamed(t, ruleName);

      // Still filtered to this rule's name, so an empty listing here means the delete landed —
      // and the empty state proves the refetch finished rather than that rows are mid-render.
      await expect(t.ruleRowNamed(ruleName)).toHaveCount(0, { timeout: 60000 });
      await expect(t.rulesEmptyState).toBeVisible({ timeout: 30000 });
    } finally {
      // A failure with the create dialog still open leaves ds/Modal's backdrop over the page,
      // and every cleanup click below would be intercepted by it — so the rule would survive on
      // the shared account precisely when the test went wrong. Dismiss it first.
      if (await t.createRuleDialog.isVisible()) {
        await t
          .dialogCancel(t.createRuleDialog)
          .click()
          .catch(() => {});
      }

      // The delete above is the assertion; this is the safety net for a failure between the
      // create and it, so a broken run does not strand a rule on the shared account. Both
      // swallows are cleanup on an already-failing path: neither may mask the real error.
      await deleteRuleNamed(t, ruleName).catch(() => {});
    }
  }
);

test(
  "Cloud Troubleshoot sanity - open Alert Tuning, verify the listing renders its Source and Confidence filters and column headers",
  { tag: ["@dev", "@test", "@oss", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "threshold-suggestions");

    await test.step("The toolbar and both filters are offered", async () => {
      await expect(t.toolbar(SUGGESTIONS_TOOLBAR)).toBeVisible();
      for (const key of Object.keys(SUGGESTIONS_FILTER) as (keyof typeof SUGGESTIONS_FILTER)[]) {
        await expect(t.suggestionsFilterTrigger(key), `the ${key} filter should render`).toBeVisible();
      }
    });

    await test.step("The listing declares its column contract", async () => {
      const rows = await suggestionRowCount(t);
      test.skip(
        rows === 0,
        "the dev AWS account has no alert tuning suggestions, so the table renders its empty state instead of headers"
      );

      for (const header of SUGGESTIONS_HEADERS) {
        await expect(t.suggestionsRoot.locator("th", { hasText: header }).first()).toBeVisible();
      }
    });
  }
);

test(
  "Cloud Troubleshoot - filter Alert Tuning by Confidence High, verify every listed suggestion reports High",
  { tag: ["@dev", "@test", "@oss", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(TROUBLESHOOT_TIMEOUT_MS);
    const t = await openTroubleshootSubTab(page, "threshold-suggestions");

    const baseline = await suggestionRowCount(t);
    test.skip(baseline === 0, "the dev AWS account has no alert tuning suggestions, so there is no row for the filter to keep or drop");

    await t.chooseFilterOption(t.suggestionsFilterTrigger("confidence"), CONFIDENCE_OPTIONS[0]);

    // Compared case-insensitively: the filter commits the option's value ('high') while the
    // cell renders the raw API value through ds/Label (ThresholdSuggestionsManager.tsx:256),
    // which is not title-cased on the way out.
    //
    // Served by the API (confidence is passed to the fetch, line 167), so a refetch is in
    // flight and the empty branch needs the empty state rather than a bare count of 0.
    await expect
      .poll(
        async () => {
          const values = await t.suggestionColumnCells("confidence").allTextContents();
          if (values.length === 0) {
            return await t.suggestionsEmptyState.isVisible();
          }
          return values.every((v) => v.trim().toLowerCase() === CONFIDENCE_OPTIONS[0].toLowerCase());
        },
        { timeout: 30000, message: "every listed suggestion should report High confidence, or the listing should be empty" }
      )
      .toBe(true);
  }
);
