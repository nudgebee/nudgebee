// Not for OSS
import { test, expect } from "@playwright/test";
import { RESOLUTIONS_TABLE, SAFETY_BANDS, SEVERITY_BANDS } from "./optimizeModuleLocators";
import { expectSelectedTab, landOnOptimize, noMatchTerm, openOptimizeTab, parkCursor, waitForRecommendations } from "./optimizeModuleHelper";

// Optimize — the tenant-level module at /optimise (app/src/pages/optimise/index.jsx),
// not the per-cluster Optimize tabs already covered under tests/ClusterDetails.
//
// Everything here is read-only. Every write the module offers acts on shared dev data
// and cannot be undone from the UI: Dismiss/Snooze takes a recommendation out of the
// open list with no un-dismiss control, Resolve runs a real remediation, and an Auto
// Optimize configuration is a live scheduled job. This suite therefore never opens a
// write modal — not even to cancel out of it, since a stray click inside Dismiss is one
// button away from a permanent change to a shared tenant. That also means the module
// has no form-validation surface this suite can reach; written up in the PR.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Optimize", () => {
  test(
    "Optimize sanity - land on the Optimize page with no fragment, verify the tab strip lists Summary, Cost, Resolutions, Security and Auto Optimize and opens on Summary",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await landOnOptimize(page);

      await test.step("Every tab the module declares is on the strip", async () => {
        // filterOptions in app/src/pages/optimise/index.jsx. LLM Analyser and AI
        // Gateway are excluded — both are per-tenant feature flags, so asserting them
        // would test the flag rather than the module.
        const strip = [
          { tab: locators.SummaryTab, name: "Summary" },
          // The strip labels this tab "Cost"; its id and fragment stay
          // `recommendations`, which is what RecommendationsTab locates it by.
          { tab: locators.RecommendationsTab, name: "Cost" },
          { tab: locators.securityTab, name: "Security" },
          { tab: locators.ResolutionsTab, name: "Resolutions" },
          { tab: locators.AutoOptimizeTab, name: "Auto Optimize" },
        ];
        for (const { tab, name } of strip) {
          await expect(tab).toBeVisible();
          await expect(tab).toContainText(name);
        }
      });

      await test.step("A bare /optimise opens Summary, and only Summary", async () => {
        await expectSelectedTab(locators.SummaryTab);
        for (const tab of [locators.RecommendationsTab, locators.ResolutionsTab, locators.securityTab, locators.AutoOptimizeTab]) {
          await expect(tab).toHaveAttribute("data-tab-selected", "false");
        }
      });
    }
  );

  test(
    "Optimize Summary - open the Summary tab, verify the savings card reports a findings count and the Category, Provider and Account filters render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openOptimizeTab(page, "summary");

      await test.step("The headline card resolves from its loading state to a real findings count", async () => {
        await expect(locators.summarySavingsCard).toBeVisible({ timeout: 60000 });
        // StatusIndicator's label is `${filtered.length} findings…` once the fetch
        // lands, and the literal "Loading…" until then — so matching a digit is what
        // separates "the card rendered its data" from "the card is still a skeleton".
        await expect(locators.summarySavingsCard).toContainText(/\d+\s+findings/, { timeout: 60000 });
        await expect(locators.summarySavingsCard).toContainText("Potential savings");
      });

      await test.step("Both filter facets and the account picker are on screen", async () => {
        await expect(locators.summaryCategoryFacet).toBeVisible();
        await expect(locators.summaryCategoryFacet).toContainText("Category");
        await expect(locators.summaryProviderFacet).toBeVisible();
        await expect(locators.summaryProviderFacet).toContainText("Provider");
        await expect(locators.summaryAccountFilter).toBeVisible();
      });
    }
  );

  test(
    "Optimize Cost - open the Cost tab, verify the five severity chips, the four safety chips and all five listing filters render",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openOptimizeTab(page, "recommendations");
      await waitForRecommendations(locators);

      await test.step("The severity row renders one chip per band", async () => {
        await expect(locators.severityBar).toBeVisible();
        // SEVERITY_ORDER is mapped through processSummaryResults, which emits an entry
        // for every band whether or not it holds findings — so all five are always in
        // the DOM, muted rather than absent when their count is zero.
        for (const band of SEVERITY_BANDS) {
          await expect(page.getByTestId(`severity-chip-${band}`)).toBeVisible();
        }
      });

      await test.step("The safety row renders one chip per band", async () => {
        for (const band of SAFETY_BANDS) {
          await expect(page.getByTestId(`safety-chip-${band}`)).toBeVisible();
        }
      });

      await test.step("The toolbar exposes search, five filters, sort and download", async () => {
        await expect(locators.recommendationsSearch).toBeVisible();
        await expect(locators.recommendationsAccountFilter).toBeVisible();
        await expect(locators.recommendationsRulesFilter).toBeVisible();
        await expect(locators.recommendationsSavingsFilter).toBeVisible();
        await expect(locators.recommendationsLastSeenFilter).toBeVisible();
        await expect(locators.recommendationsStatusFilter).toBeVisible();
        await expect(locators.recommendationsSortTrigger).toBeVisible();
        await expect(locators.recommendationsDownload).toBeVisible();
      });
    }
  );

  test(
    "Optimize Cost - search for a resource name that cannot exist, verify the table empties and the listing reports that no recommendations match these filters",
    { tag: ["@dev", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      const locators = await openOptimizeTab(page, "recommendations");
      await waitForRecommendations(locators);

      const term = noMatchTerm();
      await locators.searchAndApply(locators.recommendationsSearch, term);

      await test.step("The table comes back with no rows", async () => {
        // Retrying assertions rather than a snapshot count: the search refetches, and
        // reading the table once would race the in-flight request and see the old rows.
        await expect(locators.recommendationsRows).toHaveCount(0, { timeout: 60000 });
      });

      await test.step("The listing names the rejection instead of going blank", async () => {
        // CustomTable picks this string over the "well-optimised" one precisely because
        // a filter is active, so the copy is the proof the search reached the query.
        await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });
        await expect(locators.recommendationsNoDataText).toHaveCount(0);
      });

      await test.step("The search reached the URL, so the filter is real and shareable", async () => {
        await expect(page).toHaveURL(new RegExp(`search=${term}`));
      });
    }
  );

  test(
    "Optimize Cost - search for a resource name that cannot exist, reload the page, verify the search term and its filtered empty result both survive the reload",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      const locators = await openOptimizeTab(page, "recommendations");
      await waitForRecommendations(locators);

      const term = noMatchTerm();
      await locators.searchAndApply(locators.recommendationsSearch, term);
      await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });

      await page.reload();

      // This is the module's only persistence: OptimizeNewPage writes every filter into
      // router.query (updateUrl) and re-seeds its state from router.query on mount, so a
      // reload is what proves the filter was stored rather than held in React state.
      await test.step("The reloaded page comes back on Cost with the term still applied", async () => {
        await expectSelectedTab(locators.RecommendationsTab);
        await waitForRecommendations(locators);
        await expect(locators.recommendationsSearch).toHaveValue(term, { timeout: 60000 });
      });

      await test.step("The stored term is applied to the query, not just echoed in the field", async () => {
        await expect(locators.recommendationsRows).toHaveCount(0, { timeout: 60000 });
        await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });
      });
    }
  );

  test(
    "Optimize Cost - apply a no-match search on top of the default severity filter, click Clear all, verify the search leaves the field, the URL and the empty-state message",
    { tag: ["@dev", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      const locators = await openOptimizeTab(page, "recommendations");
      await waitForRecommendations(locators);

      await test.step("The tab opens with its default Critical and High severity filter already applied", async () => {
        // OptimizeNewPage seeds filters.severity with DEFAULT_SEVERITY on first load
        // (['Critical','High']), so hasActiveFilter is true and Clear all is on screen
        // before anything is touched. Asserted rather than assumed away: an earlier
        // draft of this test expected no active filter here and failed in CI.
        await expect(page.getByTestId("severity-chip-critical")).toHaveAttribute("aria-pressed", "true", { timeout: 60000 });
        await expect(page.getByTestId("severity-chip-high")).toHaveAttribute("aria-pressed", "true");
        await expect(locators.recommendationsClearFilters).toBeVisible();
      });

      const term = noMatchTerm();

      await test.step("Searching for a resource that cannot exist empties the table", async () => {
        await locators.searchAndApply(locators.recommendationsSearch, term);
        await expect(locators.recommendationsRows).toHaveCount(0, { timeout: 60000 });
        await expect(locators.recommendationsNoMatchText).toBeVisible({ timeout: 60000 });
        await expect(page).toHaveURL(new RegExp(`search=${term}`));
      });

      await test.step("Clear all drops every filter, not only the one that was typed", async () => {
        await locators.recommendationsClearFilters.click();
        await expect(locators.recommendationsSearch).toHaveValue("", { timeout: 60000 });
        // Clear all is rendered on `hasActiveFilter`, which is the OR of all nine
        // filters — so its disappearance is the app's own statement that the seeded
        // severity filter went with the search, rather than only the search clearing.
        await expect(locators.recommendationsClearFilters).toHaveCount(0, { timeout: 60000 });
      });

      await test.step("The listing leaves its filtered-empty state and the term leaves the URL", async () => {
        await expect(locators.recommendationsNoMatchText).toHaveCount(0, { timeout: 60000 });
        await expect(page).not.toHaveURL(/[?&]search=/);
      });
    }
  );

  test(
    "Optimize Resolutions - open the Resolutions tab, verify its Account, Severity, Type and Resolver filters render and the table shows either resolution rows or its empty state",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openOptimizeTab(page, "resolutions");

      await test.step("The resolutions toolbar renders all four filters", async () => {
        await expect(locators.resolutionsToolbar).toBeVisible({ timeout: 60000 });
        await expect(locators.resolutionsAccountFilter).toBeVisible();
        await expect(locators.resolutionsSeverityFilter).toBeVisible();
        await expect(locators.resolutionsRecommendationFilter).toBeVisible();
        await expect(locators.resolutionsResolverFilter).toBeVisible();
        // Status is filtered by the stat cards above the listing, not by a
        // dropdown — the toolbar carries no Status control to assert.
      });

      await test.step("The table settles on rows or on its empty state, never on a skeleton", async () => {
        const rows = await locators.rowCount(RESOLUTIONS_TABLE, locators.resolutionsEmpty);

        if (rows > 0) {
          // Column contract from RESOLUTION_HEADERS in ResolutionsView.tsx.
          for (const header of ["Account", "Recommendation", "Severity", "Status", "Est. Savings", "Resolver", "Type", "Updated At"]) {
            await expect(locators.resolutionsTable.locator("th", { hasText: header }).first()).toBeVisible();
          }
        } else {
          // This table takes CustomTable's default empty branch, which ids its heading
          // as `${tableId}-no-data` — a tenant that has resolved nothing yet is a
          // legitimate pass, not a missing locator.
          await expect(locators.resolutionsEmpty).toBeVisible();
        }
      });
    }
  );

  test(
    "Optimize Security - open the Security tab, verify it renders either its findings view or the message naming which accounts are missing",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openOptimizeTab(page, "security");

      await test.step("Security resolves out of its loading skeleton", async () => {
        // SecurityView returns exactly one of three testid'd states. Which one depends
        // on whether the tenant has connected accounts in scope, so both settled states
        // are accepted — but the skeleton is not, since that would mean it never loaded.
        await expect(locators.securityView.or(locators.securityEmpty).first()).toBeVisible({ timeout: 60000 });
        await expect(locators.securityLoading).toHaveCount(0);
      });

      await test.step("Whichever state rendered, it says something concrete", async () => {
        if (await locators.securityView.isVisible()) {
          // The populated branch mounts a sub-view, which lays out SecurityView's own
          // Account filter beside its findings table — so one of the two is present.
          await expect(locators.securityAccountFilter.or(locators.securityView.locator("table")).first()).toBeVisible({ timeout: 60000 });
        } else {
          await expect(locators.securityEmpty).toContainText(/Findings appear here once/);
        }
      });
    }
  );

  test(
    "Optimize - click Cost on the tab strip then click back to Summary, verify each click moves both the selected tab and the URL fragment",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await landOnOptimize(page);
      await expectSelectedTab(locators.SummaryTab);

      await test.step("Clicking Cost opens it and writes its fragment", async () => {
        await locators.RecommendationsTab.click();
        await parkCursor(page);
        await expectSelectedTab(locators.RecommendationsTab);
        await expect(page).toHaveURL(/#recommendations\b/);
        await waitForRecommendations(locators);
      });

      await test.step("Clicking Summary brings the first tab back, with its own content", async () => {
        await locators.SummaryTab.click();
        await parkCursor(page);
        await expectSelectedTab(locators.SummaryTab);
        await expect(page).toHaveURL(/#summary\b/);
        await expect(locators.summarySavingsCard).toBeVisible({ timeout: 60000 });
        // The Recommendations body is unmounted, not merely hidden — the page renders
        // one tab at a time, so this is what proves the click swapped the view.
        await expect(locators.recommendationsRoot).toHaveCount(0);
      });
    }
  );
});
