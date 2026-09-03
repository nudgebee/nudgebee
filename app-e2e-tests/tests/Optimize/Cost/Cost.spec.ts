// Not for OSS
import { test, expect } from "@playwright/test";
import { parkCursor, expectSelectedTab } from "../optimizeModuleHelper";
import { CATEGORY_CARDS, COST_HEADERS, SAVINGS_BUCKET, SORT_HIGHEST_SAVINGS, SORT_SEVERE, noMatchRule } from "./costConstants";
import {
  expectListSettled,
  firstEnabledCategory,
  firstEnabledSafetyBand,
  firstNamedRow,
  hasPageBeyond,
  hasRecommendations,
  openCostTab,
  waitForCategoryCards,
} from "./costHelper";

// Cost — the tenant-level Optimize tab at /optimise#cost (app/src/pages/optimise/index.jsx,
// filterOptions index 1). It is OptimizeNewPage.tsx mounted with no lockedCategory, which
// is what gives it the three surfaces the Configuration tab withholds and this suite is
// about: the category summary card strip, the Savings filter, and a Sort by control that
// is always offered because showConfigRollup can never become true here.
//
// OptimizeModule.spec.ts already covers this tab's chip and filter rendering, its no-match
// search, that search surviving a reload, and Clear all — none of that is repeated here.
//
// Everything here is read-only. Following tests/Optimize/OptimizeModule.spec.ts and the
// Configuration suite, this suite never opens a write modal — not even to cancel out of
// it, since a stray click inside Dismiss or Resolve is one button away from a permanent
// change to a shared tenant, and every writable record on this tab is a pre-existing
// fixture rather than one the run created. That leaves the tab with no create/submit
// surface this suite can reach; written up in the PR's Follow-ups.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Optimize Cost", () => {
  test(
    "Optimize Cost sanity - open the Cost tab, verify the summary strip renders the Total Savings card alongside clickable All Recommendations, Right Sizing, Infra Upgrade and Spot Instance cards with All selected",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);
      await waitForCategoryCards(locators);

      await test.step("The card strip the locked Configuration tab withholds is rendered here", async () => {
        // Rendered under `!lockedCategory` in OptimizeNewPage.tsx: the cards exist to
        // switch between categories, so a tab that is already one category has nothing
        // to offer and drops them. Their presence is this tab's defining contract.
        await expect(locators.summaryCardSavings).toBeVisible();
        await expect(locators.summaryCardAll).toBeVisible();
        for (const card of CATEGORY_CARDS) {
          await expect(locators.categoryCard(card.testId)).toBeVisible();
        }
      });

      await test.step("The strip opens on All Recommendations, with no category card selected", async () => {
        // aria-pressed on the All card is `filters.category.length === 0`, so this is the
        // app's own statement that it arrived unfiltered by category.
        await expect(locators.summaryCardAll).toHaveAttribute("aria-pressed", "true");
        for (const card of CATEGORY_CARDS) {
          await expect(locators.categoryCard(card.testId)).toHaveAttribute("aria-pressed", "false");
        }
      });

      await test.step("The per-resource list is the body, under every column it declares", async () => {
        await expectListSettled(locators);
        if (await hasRecommendations(locators)) {
          for (const header of COST_HEADERS) {
            await expect(locators.recommendationsTable.locator("th", { hasText: header }).first()).toBeVisible();
          }
        } else {
          // A tenant with nothing open is a legitimate pass — the listing says so in its
          // own copy rather than rendering a bare table.
          await expect(locators.recommendationsNoMatchText.or(locators.recommendationsNoDataText).first()).toBeVisible();
        }
      });
    }
  );

  test(
    "Optimize Cost - click the first category card that has recommendations, verify the card reports itself selected, the chosen category reaches the URL and the list re-settles under that one category",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);

      const card = await firstEnabledCategory(locators);
      test.skip(!card, "The dev tenant reports no open recommendations in any of the three card categories.");
      // test.skip above ends the test; this guard only narrows the type for TypeScript.
      if (!card) return;

      await test.step(`Clicking the ${card.label} card selects it and clears the All card`, async () => {
        await locators.categoryCard(card.testId).click();
        await parkCursor(page);
        await expect(locators.categoryCard(card.testId)).toHaveAttribute("aria-pressed", "true", { timeout: 60000 });
        await expect(locators.summaryCardAll).toHaveAttribute("aria-pressed", "false");
      });

      await test.step("The category reaches the URL, so the list re-queried rather than re-rendering", async () => {
        // updateUrl writes filters.category only when it is non-empty, so the parameter
        // appearing is the app's own statement that the narrower filter reached the query.
        await expect(page).toHaveURL(new RegExp(`category=${card.category}`));
      });

      await test.step("The list settles again on rows or on its filtered empty message, never on a skeleton", async () => {
        await expectListSettled(locators);
        await expectSelectedTab(locators.RecommendationsTab);
      });
    }
  );

  test(
    "Optimize Cost - select a category card then click All Recommendations, verify the category leaves the URL, every category card returns to unselected and the full list comes back",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);

      const card = await firstEnabledCategory(locators);
      test.skip(!card, "The dev tenant reports no open recommendations in any of the three card categories.");
      // test.skip above ends the test; this guard only narrows the type for TypeScript.
      if (!card) return;

      await test.step("A category card is selected first, so All has something to undo", async () => {
        await locators.categoryCard(card.testId).click();
        await parkCursor(page);
        await expect(page).toHaveURL(new RegExp(`category=${card.category}`), { timeout: 60000 });
      });

      await test.step("Clicking All Recommendations drops the category from the URL", async () => {
        // handleAllCardClick always selects the full category set — there is nothing
        // narrower to fall back to — so the parameter disappearing is the whole effect.
        await locators.summaryCardAll.click();
        await parkCursor(page);
        await expect(page).not.toHaveURL(/[?&]category=/, { timeout: 60000 });
      });

      await test.step("Every category card reads unselected again and All reads selected", async () => {
        await expect(locators.summaryCardAll).toHaveAttribute("aria-pressed", "true", { timeout: 60000 });
        for (const each of CATEGORY_CARDS) {
          await expect(locators.categoryCard(each.testId)).toHaveAttribute("aria-pressed", "false");
        }
        await expectListSettled(locators);
      });
    }
  );

  test(
    `Optimize Cost - open the Savings filter and pick the ${SAVINGS_BUCKET.label} bucket, verify the trigger shows the bucket, its key reaches the URL and the list re-settles under the narrower filter`,
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);
      await expectListSettled(locators);

      await test.step("The Savings filter is offered here, unlike on the Configuration tab", async () => {
        // Rendered under `!isConfigurationOnly`. No card on this strip selects
        // Configuration, so on the Cost tab the filter is always present.
        await expect(locators.recommendationsSavingsFilter).toBeVisible({ timeout: 60000 });
      });

      await test.step(`Committing ${SAVINGS_BUCKET.label} writes its bucket key to the URL`, async () => {
        await locators.chooseFilterOption(locators.recommendationsSavingsFilter, SAVINGS_BUCKET.label);
        // savingsBucketToParams turns this key into the savingsGte the backend query
        // carries, so the key in the URL is the proof the selection reached the request.
        await expect(page).toHaveURL(new RegExp(`savings=${SAVINGS_BUCKET.key}`), { timeout: 60000 });
      });

      await test.step("The list settles again on rows or on its filtered empty message", async () => {
        await expectListSettled(locators);
        await expectSelectedTab(locators.RecommendationsTab);
      });
    }
  );

  test(
    `Optimize Cost - open Sort by and pick ${SORT_HIGHEST_SAVINGS.label}, verify the trigger relabels from ${SORT_SEVERE.label} and the list re-settles under the new order`,
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);
      await expectListSettled(locators);

      await test.step(`The tab opens sorted by ${SORT_SEVERE.label}`, async () => {
        // sortField/sortDirection start at severity/asc, which activeSortValue resolves
        // to the `severe` preset — so the trigger carries that label before anything is
        // touched, and is what the change below is measured against.
        await expect(locators.recommendationsSortTrigger).toContainText(SORT_SEVERE.label, { timeout: 60000 });
      });

      await test.step(`Picking ${SORT_HIGHEST_SAVINGS.label} relabels the trigger`, async () => {
        await locators.recommendationsSortTrigger.click();
        await expect(locators.sortOption(SORT_HIGHEST_SAVINGS.value, SORT_HIGHEST_SAVINGS.label)).toBeVisible({ timeout: 30000 });
        await locators.sortOption(SORT_HIGHEST_SAVINGS.value, SORT_HIGHEST_SAVINGS.label).click();
        await parkCursor(page);
        // The trigger reads back activeSortValue, so its label is the app's own statement
        // that both sortField and sortDirection moved to the preset.
        await expect(locators.recommendationsSortTrigger).toContainText(SORT_HIGHEST_SAVINGS.label, { timeout: 60000 });
        await expect(locators.recommendationsSortTrigger).not.toContainText(SORT_SEVERE.label);
      });

      await test.step("The list settles again under the new order, never on a skeleton", async () => {
        await expect(locators.openMenu).toHaveCount(0, { timeout: 30000 });
        await expectListSettled(locators);
      });
    }
  );

  test(
    "Optimize Cost - click the first safety chip that has findings, verify it reports itself selected and its band reaches the URL, then click it again and verify the band leaves the URL",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);
      await expect(locators.severityBar).toBeVisible({ timeout: 60000 });

      const band = await firstEnabledSafetyBand(locators);
      test.skip(!band, "The dev tenant reports no open recommendations in any safety band.");
      // test.skip above ends the test; this guard only narrows the type for TypeScript.
      if (!band) return;

      const chip = locators.safetyChip(band);

      await test.step(`The ${band} band starts unselected`, async () => {
        // Only severity is seeded on first load (DEFAULT_SEVERITY), so the safety row
        // arrives with nothing pressed and the click below is a real state change.
        await expect(chip).toHaveAttribute("aria-pressed", "false", { timeout: 60000 });
      });

      await test.step(`Clicking the ${band} chip selects it and writes the band to the URL`, async () => {
        await chip.click();
        await parkCursor(page);
        await expect(chip).toHaveAttribute("aria-pressed", "true", { timeout: 60000 });
        await expect(page).toHaveURL(new RegExp(`safety=${band}`), { timeout: 60000 });
        await expectListSettled(locators);
      });

      await test.step(`Clicking it again drops the ${band} band from the URL`, async () => {
        // handleSafetyClick toggles the band in and out of the filter set, so the second
        // click is the app's own undo — and the URL is where it is observable.
        await chip.click();
        await parkCursor(page);
        await expect(chip).toHaveAttribute("aria-pressed", "false", { timeout: 60000 });
        await expect(page).not.toHaveURL(new RegExp(`safety=${band}`), { timeout: 60000 });
        await expectListSettled(locators);
      });
    }
  );

  test(
    "Optimize Cost - click a recommendation row, verify the detail panel opens naming that row's resource, then close it and verify the list comes back with the same row still listed",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);

      test.skip(!(await hasRecommendations(locators)), "The dev tenant reports no open recommendations, so there is no row to open.");

      const target = await firstNamedRow(locators);
      test.skip(!target, "No recommendation in the first five rows names its resource, so there is nothing to match the panel against.");
      // test.skip above ends the test; this guard only narrows the type for TypeScript.
      if (!target) return;

      await test.step("Clicking the Resource cell opens that recommendation's detail panel", async () => {
        // The trailing cell holds RowActions, which stops propagation — the Resource cell
        // is the one that carries the row click through to the panel.
        await locators.rowResourceCell(target.row).click();
        await expect(locators.detailPanel).toBeVisible({ timeout: 60000 });
      });

      await test.step("The panel names the resource from the row that was clicked, not some other row", async () => {
        // The header renders getResourceDisplayName(rec), the same call the table's
        // Resource cell renders — so the name matching is what proves the panel opened on
        // the clicked recommendation rather than merely opening.
        await expect(locators.detailPanel).toContainText(target.name, { timeout: 60000 });
      });

      await test.step("Closing the panel returns to the list with that row still listed", async () => {
        await locators.detailPanelClose.click();
        await expect(locators.detailPanel).toHaveCount(0, { timeout: 30000 });
        await expect(locators.rowResourceCell(target.row)).toContainText(target.name, { timeout: 60000 });
      });
    }
  );

  test(
    "Optimize Cost - page the recommendations list to its second page, verify the Showing range advances and page 2 reads as current, then page back and verify the first range returns",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openCostTab(page);

      test.skip(!(await hasRecommendations(locators)), "The dev tenant reports no open recommendations, so the footer renders no pagination.");

      await expect(locators.paginationSummary).toBeVisible({ timeout: 60000 });
      // Normalised the way toHaveText normalises, so the comparison below is about the
      // range moving rather than about how the footer wrapped its whitespace.
      const firstRange = (await locators.paginationSummary.innerText()).replace(/\s+/g, " ").trim();

      test.skip(!hasPageBeyond(firstRange), "The dev tenant's open recommendations fit on one page, so there is no second page to reach.");

      await test.step("Paging forward advances the Showing range and leaves rows on screen", async () => {
        await locators.nextPage.click();
        await parkCursor(page);
        // Comparing against the captured range rather than a hardcoded one: how many rows
        // a page holds is the tenant's rowsPerPage, not something this test pins.
        await expect(locators.paginationSummary).not.toHaveText(firstRange, { timeout: 60000 });
        await expect(locators.recommendationsRows.first()).toBeVisible({ timeout: 60000 });
      });

      await test.step("The footer reports page 2 as the one being shown", async () => {
        // MUI relabels the current page from "Go to page 2" to "page 2", so the label flip
        // is the component's own statement of which page landed.
        await expect(locators.currentPageButton(2)).toBeVisible({ timeout: 60000 });
      });

      await test.step("Paging back restores the first range", async () => {
        await locators.previousPage.click();
        await parkCursor(page);
        await expect(locators.paginationSummary).toHaveText(firstRange, { timeout: 60000 });
        await expect(locators.currentPageButton(1)).toBeVisible({ timeout: 60000 });
      });
    }
  );

  test(
    "Optimize Cost - open the Rules filter and search its options for a rule name that cannot exist, verify the panel reports No results found and offers no option to select",
    { tag: ["@dev", "@regression", "@negative", "@validation", "@search"] },
    async ({ page }) => {
      const locators = await openCostTab(page);
      await expectListSettled(locators);

      await locators.recommendationsRulesFilter.click();
      await expect(locators.visibleFilterOptions().first()).toBeVisible({ timeout: 60000 });

      // FilterDropdown renders its option search only above eight options, so a tenant
      // with a short rule list has no search box to exercise — an expected shape of the
      // panel rather than a failure, which is why this is a probe and not an assertion.
      const searchOffered = await locators.filterSearchOffered("Search rules…");
      test.skip(!searchOffered, "The dev tenant's rule list is eight options or fewer, so the panel renders no option search.");

      const term = noMatchRule();

      await test.step("A rule name that cannot exist empties the option list and names the rejection", async () => {
        await locators.filterPanelSearch("Search rules…").fill(term);
        // The settled empty state is asserted before the count: the option rows can still
        // be on screen for a render while the filter is applied, so a count taken first
        // would be measuring the transition rather than the result. Both OptionsList and
        // GroupedOptionsList render this copy in place of the rows, so it is the app
        // stating that the search ran and matched nothing.
        await expect(locators.filterNoOptions()).toBeVisible({ timeout: 30000 });
        await expect(locators.visibleFilterOptions()).toHaveCount(0);
      });

      await test.step("Escape closes the panel with no filter applied", async () => {
        // The no-results copy lives inside the popover and nowhere else, so it going away
        // is what proves the panel closed — the option count was already 0 by this point
        // and would have read as a pass whether the panel closed or not.
        await page.keyboard.press("Escape");
        await expect(locators.filterNoOptions()).toHaveCount(0, { timeout: 30000 });
        await expect(page).not.toHaveURL(/[?&]rules=/);
        await expectListSettled(locators);
      });
    }
  );
});
