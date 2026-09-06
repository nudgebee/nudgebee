// Not for OSS
import { test, expect } from "@playwright/test";
import {
  MODULE_ELASTICSEARCH,
  MODULE_LOKI,
  MODULE_PROMETHEUS,
  MODULE_TROUBLESHOOT,
  hasAnyModuleFilter,
  hasModuleFilter,
  hasUsefulFilter,
  nextFeedbackQuery,
  nextFeedbackQueryMatching,
  openUserFeedbackTab,
  remountUserFeedbackTab,
  windowOf,
} from "./userFeedbackHelper";

// Nubi > Settings > User Feedback (app/src/components/llm/UserFeedbackTab.jsx).
// The tab is read-only, so nothing here writes to the shared dev tenant. How
// much feedback that tenant holds is not ours to control either, which is why
// every filter case asserts the committed filter on the outgoing ListAiFeedback
// query and treats the rows on screen as the secondary check.

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

test.describe("Nubi User Feedback Tab", () => {
  test(
    "User Feedback sanity - open Nubi b-Cortex, select the Insights Feedback tab, verify the listing renders the feedback columns, both filters and the date range control",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const { locators } = await openUserFeedbackTab(page);

      await expect(locators.listingTitle).toBeVisible({ timeout: 30000 });
      await expect(locators.table).toBeVisible({ timeout: 30000 });

      // Column order is the contract the per-column assertions below depend on:
      // Module and Useful are read by position, so a reordered header set has to
      // fail here rather than silently re-point those reads at another column.
      for (const header of ["Module", "Useful", "Question", "AdditionalDetails", "User"]) {
        await expect(locators.columnHeader(header)).toBeVisible({ timeout: 20000 });
      }

      await expect(locators.moduleFilterTrigger).toBeVisible({ timeout: 15000 });
      await expect(locators.usefulFilterTrigger).toBeVisible({ timeout: 15000 });
      await expect(locators.dateRangeTrigger).toBeVisible({ timeout: 15000 });
    }
  );

  test(
    "User Feedback sanity - open the tab, verify the first listing query asks for the last 7 days and that its account scope matches the columns rendered",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const { locators, log } = await openUserFeedbackTab(page);

      const query = await nextFeedbackQuery(log, 0);
      const { spanMs, endMs } = windowOf(query);

      // DEFAULT_DATE_RANGE_MS is 7 days and the end bound is "now", so both are
      // asserted with enough slack for the round trip and none for a wrong default.
      expect(spanMs).toBeGreaterThan(6.9 * DAY_MS);
      expect(spanMs).toBeLessThan(7.1 * DAY_MS);
      expect(Date.now() - endMs).toBeLessThan(10 * 60 * 1000);

      // The tab has two shapes: account-scoped (Settings opened with an account)
      // sends cloud_account_id and renders five columns, tenant-wide sends no
      // account filter and adds an Account column to disambiguate the rows. Which
      // one dev serves is not ours to pin, but the query and the columns must
      // agree — a scoped query with an Account column means rows are labelled
      // with an account the query already fixed, and the reverse leaks
      // cross-account rows into an unlabelled table.
      const accountScoped = /cloud_account_id:\{_eq:/.test(query);
      const accountColumns = await locators.columnHeader("Account").count();
      expect(accountScoped).toBe(accountColumns === 0);
    }
  );

  test(
    "User Feedback sanity - open the Module filter, verify it offers the four query modules plus the Ask-Nubi module and nothing else",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const { locators } = await openUserFeedbackTab(page);

      await locators.moduleFilterTrigger.click();
      await locators.visibleOptions().first().waitFor({ state: "visible", timeout: 15000 });
      await expect(locators.visibleOptions()).toHaveCount(5, { timeout: 15000 });

      for (const module of [MODULE_TROUBLESHOOT, MODULE_LOKI, MODULE_PROMETHEUS, MODULE_ELASTICSEARCH]) {
        await expect(locators.filterOption(module.label)).toBeVisible({ timeout: 15000 });
      }

      // The fifth is `Ask ${DEFAULT_TITLE}`, so its tail is tenant branding and
      // only the prefix is a fixed part of the contract.
      await expect(locators.visibleOptions().filter({ hasText: /^Ask\s\S+/ })).toHaveCount(1, { timeout: 15000 });
    }
  );

  test(
    "User Feedback - filter by the Troubleshoot module, verify the listing query carries that module and no row from another module is listed",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openUserFeedbackTab(page);

      const before = log.queries.length;
      await locators.chooseFilter(locators.moduleFilterTrigger, MODULE_TROUBLESHOOT.label);
      const query = await nextFeedbackQuery(log, before);

      expect(hasModuleFilter(query, MODULE_TROUBLESHOOT.value)).toBe(true);
      expect(hasUsefulFilter(query, true)).toBe(false);
      expect(hasUsefulFilter(query, false)).toBe(false);

      await locators.waitForRowsSettled();
      await expect(locators.firstRowOrEmptyState()).toBeVisible({ timeout: 30000 });
      // "investigate" is one of the two modules the tab links, so its cell shows
      // the display name rather than the raw module value.
      await expect(locators.moduleCells().filter({ hasNotText: MODULE_TROUBLESHOOT.label })).toHaveCount(0, { timeout: 30000 });
    }
  );

  test(
    "User Feedback - filter by Useful = No, verify the listing query carries useful false and that no row is labelled Yes",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openUserFeedbackTab(page);

      const before = log.queries.length;
      await locators.chooseFilter(locators.usefulFilterTrigger, "No");
      const query = await nextFeedbackQuery(log, before);

      expect(hasUsefulFilter(query, false)).toBe(true);
      expect(hasUsefulFilter(query, true)).toBe(false);

      await locators.waitForRowsSettled();
      // Without this the cell count could be taken on the skeleton body, whose
      // cells are empty and so match no "Yes" — a pass that proves nothing.
      await expect(locators.firstRowOrEmptyState()).toBeVisible({ timeout: 30000 });
      await expect(locators.usefulCells().filter({ hasText: "Yes" })).toHaveCount(0, { timeout: 30000 });
    }
  );

  test(
    "User Feedback - apply the Loki Query module and Useful = Yes together, verify the listing query carries both conditions in one request",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openUserFeedbackTab(page);

      const beforeModule = log.queries.length;
      await locators.chooseFilter(locators.moduleFilterTrigger, MODULE_LOKI.label);
      // Settle the module refetch before the second filter is applied, so the
      // read below cannot land on it instead of the two-condition query.
      await nextFeedbackQueryMatching(log, beforeModule, (q) => hasModuleFilter(q, MODULE_LOKI.value), `the ${MODULE_LOKI.label} module filter`);

      const afterModule = log.queries.length;
      await locators.chooseFilter(locators.usefulFilterTrigger, "Yes");
      const query = await nextFeedbackQueryMatching(log, afterModule, (q) => hasUsefulFilter(q, true), "the committed useful=yes filter");

      // Both triggers keep their selection, so the second filter must narrow the
      // first rather than replace it.
      expect(hasModuleFilter(query, MODULE_LOKI.value)).toBe(true);
      await expect(locators.moduleFilterTrigger).toContainText(MODULE_LOKI.label);
      await expect(locators.usefulFilterTrigger).toContainText("Yes");
    }
  );

  test(
    "User Feedback - filter by the Prometheus Query module then clear the filter, verify the trigger returns to its unset label and the listing query drops the module condition",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openUserFeedbackTab(page);

      const beforeModule = log.queries.length;
      await locators.chooseFilter(locators.moduleFilterTrigger, MODULE_PROMETHEUS.label);
      // Settle the module refetch first: the clear-filter assertion is a
      // negative one, and a still-in-flight filtered response would satisfy
      // "the next query" and fail it.
      await nextFeedbackQueryMatching(log, beforeModule, (q) => hasModuleFilter(q, MODULE_PROMETHEUS.value), `the ${MODULE_PROMETHEUS.label} module filter`);
      const afterModule = log.queries.length;

      await locators.clearControl(locators.moduleFilterTrigger).click();
      const query = await nextFeedbackQuery(log, afterModule);

      expect(hasAnyModuleFilter(query)).toBe(false);
      await expect(locators.moduleFilterTrigger).toHaveText(/^Module$/, { timeout: 15000 });
    }
  );

  test(
    "User Feedback - open the date range and pick the Last 24 Hours shortcut, verify the trigger shows the shortcut and the listing query window narrows from 7 days to 24 hours",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openUserFeedbackTab(page);

      const initial = windowOf(await nextFeedbackQuery(log, 0));
      const before = log.queries.length;

      await locators.dateRangeTrigger.click();
      await locators.dateShortcut("Last 24 Hours").click();

      const query = await nextFeedbackQuery(log, before);
      const narrowed = windowOf(query);

      expect(narrowed.spanMs).toBeGreaterThan(23.5 * HOUR_MS);
      expect(narrowed.spanMs).toBeLessThan(24.5 * HOUR_MS);
      expect(narrowed.spanMs).toBeLessThan(initial.spanMs);
      // toContainText, not toHaveText: the leading calendar glyph is an inline
      // SVG whose <title> would join the trigger's text content.
      await expect(locators.dateRangeTrigger).toContainText("Last 24 Hours", { timeout: 15000 });
    }
  );

  test(
    "User Feedback - apply a module filter, switch to the Digests tab and return, verify the tab remounts with the filter cleared and re-queries the default 7 day window",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openUserFeedbackTab(page);

      const beforeModule = log.queries.length;
      await locators.chooseFilter(locators.moduleFilterTrigger, MODULE_ELASTICSEARCH.label);
      // Settle the module refetch first, for the same reason as the clear case:
      // the assertion after the remount is a negative one.
      await nextFeedbackQueryMatching(log, beforeModule, (q) => hasModuleFilter(q, MODULE_ELASTICSEARCH.value), `the ${MODULE_ELASTICSEARCH.label} module filter`);
      const afterModule = log.queries.length;

      await remountUserFeedbackTab(locators);
      const query = await nextFeedbackQuery(log, afterModule);

      // Switching Insights sub-tabs swaps the tab body out entirely (different
      // component types, BCortexModal.jsx's renderBody), so the filter is not
      // merely hidden — it is gone, and the refetch must not still carry it.
      expect(hasAnyModuleFilter(query)).toBe(false);
      expect(windowOf(query).spanMs).toBeGreaterThan(6.9 * DAY_MS);
      await expect(locators.moduleFilterTrigger).toHaveText(/^Module$/, { timeout: 15000 });
    }
  );
});
