// Not for OSS
import { test, expect } from "@playwright/test";
import {
  OPTIMIZE_TIMEOUT_MS,
  chooseFilterOption,
  firstFilterOptionLabel,
  openOptimizeCategory,
  optimizeRowCount,
  switchOptimizeCategory,
  switchToResolution,
} from "./awsOptimizeHelper";
import {
  DEFAULT_STATUS_OVERFLOW_BADGE,
  DEFAULT_STATUS_VISIBLE_LABEL,
  DRILLDOWN_TABS,
  NO_SAVINGS_HEADERS,
  SAVINGS_HEADERS,
} from "./awsOptimizeLocators";

// Cloud Account > AWS > Optimize — the account-level recommendations module: Right Sizing,
// Configuration, Security and Infra Upgrade (four category views of one component,
// app/src/pages/cloud-account/details/[CloudAccountDetails].jsx:593-628) plus the
// Recommendation Resolution listing on the fifth sub-tab.
//
// Read-only by construction. The module's only two write actions both land outside the
// app: Create Ticket opens a real ticket in whichever tracker the shared dev tenant has
// wired up, and Create Alarm creates a real CloudWatch alarm in the shared AWS account.
// Neither is reversible from the UI, so both flows are opened and dismissed but never
// submitted — see the PR's Follow-ups.

test(
  "Cloud Optimize sanity - open the AWS account, select Optimize, verify Right Sizing renders its four filters, download action and both recommendation stat cards",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    await test.step("The toolbar offers the category's filters", async () => {
      await expect(o.toolbar("right-sizing")).toBeVisible();
      await expect(o.filterTrigger("right-sizing", "rule-name", "Rule Name")).toBeVisible();
      await expect(o.filterTrigger("right-sizing", "severity", "Severity")).toBeVisible();
      await expect(o.filterTrigger("right-sizing", "status", "Status")).toBeVisible();
      // Service Name renders only when the view is not already pinned to one service,
      // which is the case on the account-level tab (no serviceName prop is passed).
      await expect(o.filterTrigger("right-sizing", "service-name", "Service Name")).toBeVisible();
      await expect(o.downloadBtn("right-sizing")).toBeVisible();
    });

    await test.step("Right Sizing is a savings category, so both stat cards render", async () => {
      // showSavings is true for this category only among the first three, so Estimated
      // Savings existing here is a real branch of the component rather than page furniture.
      await expect(o.stat("Total Recommendations")).toBeVisible();
      await expect(o.stat("Estimated Savings")).toBeVisible();
    });
  }
);

test(
  "Cloud Optimize sanity - open Right Sizing, verify the listing declares its five columns and the Status filter opens pre-set to Open and In Progress",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    await test.step("The savings column set is on screen", async () => {
      // The sixth declared header is deliberately blank (the actions column), so the
      // contract asserted here is the five named ones.
      for (const header of SAVINGS_HEADERS) {
        await expect(
          o.categoryRoot("right-sizing").locator("th", { hasText: header }).first(),
          `Right Sizing should declare a ${header} column`
        ).toBeVisible();
      }
    });

    await test.step("Status carries the component's two seeded values", async () => {
      // selectedStatus is initialised to Open + InProgress, so an unfiltered landing is
      // already a filtered listing — worth pinning, because every row-count baseline the
      // other tests take is a baseline of these two statuses.
      //
      // The trigger shows the first value and collapses the second into a +1 badge
      // (limitTag=1), so both halves are asserted: the visible label and the overflow that
      // proves a second status is committed behind it.
      const status = o.filterTrigger("right-sizing", "status", "Status");
      await expect(status).toContainText(DEFAULT_STATUS_VISIBLE_LABEL);
      await expect(status).toContainText(DEFAULT_STATUS_OVERFLOW_BADGE);
    });
  }
);

test(
  "Cloud Optimize - filter Right Sizing by a rule name the account holds, verify every remaining row belongs to that one rule",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    const baseline = await optimizeRowCount(o, "right-sizing");
    test.skip(baseline === 0, "the dev AWS account has no open Right Sizing recommendations, so there is no row for the filter to keep or drop");

    const trigger = o.filterTrigger("right-sizing", "rule-name", "Rule Name");
    const ruleName = await firstFilterOptionLabel(o, page, trigger);
    expect(ruleName, "the Rule Name filter should offer at least one rule for an account that has rows").not.toBeNull();

    await chooseFilterOption(o, page, trigger, ruleName as string);

    // The Rule Name cell renders the recommendation's own title above the rule it came
    // from, and two resources can carry different titles under the same rule — so the
    // assertion is that one rule is left, not that a literal string matches. CustomTable
    // puts the raw rule_name on the cell as data-export-data, which is the exact value the
    // filter's option carries.
    //
    // Polled rather than read once: committing the filter refetches listRecommendation, and
    // a single read would race the in-flight request and grade the pre-filter rows.
    await expect
      .poll(
        async () => {
          const rules = await o
            .columnCells("right-sizing", "ruleName")
            .evaluateAll((cells) => cells.map((c) => c.getAttribute("data-export-data") ?? ""));
          // The empty branch requires the empty state to be on screen, not merely zero
          // rows: the component clears its rows before every fetch, so a bare count of 0 is
          // also what a refetch in flight looks like.
          if (rules.length === 0) {
            return o.emptyState("right-sizing").isVisible();
          }
          return new Set(rules).size === 1;
        },
        { message: `every row left after filtering to '${ruleName}' should belong to a single rule` }
      )
      .toBe(true);

    // The filter is a narrowing, so it can never return more rows than the unfiltered list.
    expect(await o.rows("right-sizing").count()).toBeLessThanOrEqual(baseline);
  }
);

test(
  "Cloud Optimize - filter Right Sizing by a severity the account holds, verify every remaining row reports that severity",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    const baseline = await optimizeRowCount(o, "right-sizing");
    test.skip(baseline === 0, "the dev AWS account has no open Right Sizing recommendations, so there is no row to grade a severity on");

    // Taken from a row rather than from the fixed five-option list: the Severity filter is
    // seeded with every level the product defines, not with the levels this account holds,
    // so a guessed value could legitimately match nothing and prove nothing.
    const severityLabel = (await o.severityChip("right-sizing", 0).getAttribute("aria-label")) ?? "";
    const severity = severityLabel.replace(/^Severity:\s*/, "").trim();
    expect(severity, "the first row's severity chip should name a level").not.toBe("");

    await chooseFilterOption(o, page, o.filterTrigger("right-sizing", "severity", "Severity"), severity);

    await expect
      .poll(
        async () => {
          const labels = await o
            .severityChips("right-sizing")
            .evaluateAll((chips) => chips.map((c) => c.getAttribute("aria-label") ?? ""));
          if (labels.length === 0) {
            return o.emptyState("right-sizing").isVisible();
          }
          return labels.every((l) => l.replace(/^Severity:\s*/, "").trim() === severity);
        },
        { message: `every row left after filtering to '${severity}' should report that severity` }
      )
      .toBe(true);

    expect(await o.rows("right-sizing").count()).toBeLessThanOrEqual(baseline);
  }
);

test(
  "Cloud Optimize - filter Right Sizing by a rule name then clear it, verify the listing returns to its unfiltered row count",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    const baseline = await optimizeRowCount(o, "right-sizing");
    test.skip(baseline === 0, "the dev AWS account has no open Right Sizing recommendations, so there is no listing to narrow and restore");

    const trigger = o.filterTrigger("right-sizing", "rule-name", "Rule Name");
    const ruleName = await firstFilterOptionLabel(o, page, trigger);
    expect(ruleName, "the Rule Name filter should offer at least one rule for an account that has rows").not.toBeNull();

    await test.step("Committing the rule narrows the listing", async () => {
      await chooseFilterOption(o, page, trigger, ruleName as string);
      await expect(trigger).toContainText(ruleName as string);
    });

    await test.step("Deselecting the same rule restores the original listing", async () => {
      // Rule Name is a multi-select, so clicking the committed option again is the clear
      // path — ds/FilterDropdown options are toggles, not radio choices.
      await chooseFilterOption(o, page, trigger, ruleName as string, "deselect");
      // Waits for the restored count rather than snapshotting a table that is still showing
      // the narrowed result from the step above.
      await expect(o.rows("right-sizing")).toHaveCount(baseline);
    });
  }
);

test(
  "Cloud Optimize - expand a Right Sizing recommendation, verify the Evidence, Description, Mitigation and Audit History tabs open, then collapse it and verify the panel closes",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    const baseline = await optimizeRowCount(o, "right-sizing");
    test.skip(baseline === 0, "the dev AWS account has no open Right Sizing recommendations, so there is no row to expand");

    const toggle = o.rowExpandToggle("right-sizing", 0);
    await expect(toggle).toHaveAttribute("aria-expanded", "false");

    await test.step("Expanding the row reveals all four drilldown tabs", async () => {
      await toggle.click();
      // aria-expanded is CustomTable's own state, so it is the panel's real open signal
      // rather than a guess at how long the Collapse transition takes.
      await expect(toggle).toHaveAttribute("aria-expanded", "true");
      for (const name of DRILLDOWN_TABS) {
        await expect(
          o.drilldownTab("right-sizing", name),
          `the expanded row should offer a ${name} tab`
        ).toBeVisible();
      }
    });

    await test.step("Selecting Mitigation switches the panel to it", async () => {
      const mitigation = o.drilldownTab("right-sizing", "Mitigation");
      await mitigation.click();
      // MUI marks the chosen tab with aria-selected, so this proves the switch landed
      // rather than that the tab is merely on screen — it was already visible above.
      await expect(mitigation).toHaveAttribute("aria-selected", "true");
      await expect(o.drilldownTab("right-sizing", "Evidence")).toHaveAttribute("aria-selected", "false");
    });

    await test.step("Collapsing the row hides the tab strip again", async () => {
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-expanded", "false");
      await expect(o.drilldownTab("right-sizing", "Evidence")).toBeHidden();
    });
  }
);

test(
  "Cloud Optimize - move from Right Sizing through Configuration and Security to Infra Upgrade, verify each sub-tab lands on its own URL fragment and replaces the previous listing",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");
    await expect(page).toHaveURL(o.OptimizeRightSizingUrl);

    await test.step("Configuration replaces the Right Sizing listing", async () => {
      await switchOptimizeCategory(o, page, "configuration");
      await expect(page).toHaveURL(o.OptimizeConfigurationUrl);
      // Each category is a distinct ListingLayout node, so the previous panel being gone is
      // what proves the switch rather than a restyle of one shared panel.
      await expect(o.categoryRoot("right-sizing")).toHaveCount(0);
      // Configuration is not a savings category, so it drops both the Savings column and
      // the Estimated Savings card — the clearest observable difference between the two.
      for (const header of NO_SAVINGS_HEADERS) {
        await expect(o.categoryRoot("configuration").locator("th", { hasText: header }).first()).toBeVisible();
      }
      await expect(o.stat("Estimated Savings")).toHaveCount(0);
    });

    await test.step("Security replaces Configuration", async () => {
      await switchOptimizeCategory(o, page, "security");
      await expect(page).toHaveURL(o.OptimizeSecurityTabUrl);
      await expect(o.categoryRoot("configuration")).toHaveCount(0);
      await expect(o.toolbar("security")).toBeVisible();
    });

    await test.step("Infra Upgrade replaces Security and brings the savings card back", async () => {
      await switchOptimizeCategory(o, page, "infra-upgrade");
      await expect(page).toHaveURL(o.OptimizeInfraUpgradeUrl);
      await expect(o.categoryRoot("security")).toHaveCount(0);
      await expect(o.stat("Estimated Savings")).toBeVisible();
    });
  }
);

test(
  "Cloud Optimize - open a Right Sizing row's actions menu, verify Resolve is offered disabled and Create Ticket enabled, dismiss it and verify no ticket form opens",
  { tag: ["@dev", "@regression", "@negative", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    const baseline = await optimizeRowCount(o, "right-sizing");
    test.skip(baseline === 0, "the dev AWS account has no open Right Sizing recommendations, so there is no row menu to open");

    await o.rowMenuTrigger("right-sizing", 0).click();
    await expect(o.openMenuItems.first()).toBeVisible({ timeout: 15000 });

    await test.step("The menu offers exactly the two non-alarm actions", async () => {
      // Right Sizing has showAlarmModal false, so getMenuItems returns the fixed pair
      // rather than the account-dependent Create Alarm branch — which is why this count is
      // a contract and not a property of the dev data.
      await expect(o.openMenuItems).toHaveCount(2);
      const resolve = o.menuItem(/^Resolve$/, "-0");
      const createTicket = o.menuItem(/^Create Ticket$/, "-1");
      await expect(resolve).toBeVisible();
      // Resolve ships disabled for every non-alarm category, so a menu that offers it as
      // clickable is a regression rather than a data difference.
      await expect(resolve).toHaveAttribute("aria-disabled", "true");
      await expect(createTicket).toBeVisible();
      await expect(createTicket).not.toHaveAttribute("aria-disabled", "true");
    });

    await test.step("Escape dismisses the menu without starting a ticket", async () => {
      // Deliberately never clicks Create Ticket: submitting it opens a real ticket in the
      // tracker the shared dev tenant has wired up, which no test can clean up afterwards.
      await page.keyboard.press("Escape");
      await expect(o.openMenuItems).toHaveCount(0);
      // The ticket form is a dialog, so its absence is what proves nothing was started.
      // Scoped to :visible rather than to every dialog node — MUI keeps some closed
      // dialogs mounted, and a hidden one would fail this without a ticket ever opening.
      await expect(page.locator('[role="dialog"]:visible')).toHaveCount(0);
      await expect(o.rows("right-sizing")).toHaveCount(baseline);
    });
  }
);

test(
  "Cloud Optimize - open the Recommendation Resolution sub-tab, verify its resolution listing and status filter render, then return to Right Sizing",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(OPTIMIZE_TIMEOUT_MS);
    const o = await openOptimizeCategory(page, "right-sizing");

    await test.step("Recommendation Resolution replaces the Right Sizing listing", async () => {
      await switchToResolution(o, page);
      await expect(page).toHaveURL(o.OptimizeRecommendationResolutionUrl);
      // A different component entirely, so the recommendations panel is gone rather than
      // re-rendered with new rows.
      await expect(o.categoryRoot("right-sizing")).toHaveCount(0);
      await expect(page.getByTestId("rr-filter-toolbar")).toBeVisible();
      await expect(
        page.getByTestId("rr-filter-toolbar").getByRole("button", { name: /^Status/ })
      ).toBeVisible();
    });

    await test.step("The Optimize flyout returns to Right Sizing", async () => {
      await switchOptimizeCategory(o, page, "right-sizing");
      await expect(page).toHaveURL(o.OptimizeRightSizingUrl);
      await expect(o.resolutionRoot).toHaveCount(0);
      await expect(o.toolbar("right-sizing")).toBeVisible();
    });
  }
);
