// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { SAFETY_BANDS } from "../optimizeModuleLocators";
import { expectSelectedTab, waitForRecommendations } from "../optimizeModuleHelper";
import { CostLocators } from "./costLocators";
import { CATEGORY_CARDS, CategoryCard, PAGINATION_SUMMARY_PATTERN } from "./costConstants";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does
// not fail here — it fails later as a goto against "about:blank". Asserted up front so
// the run says which key is missing instead of reporting an unexplained navigation
// error. No binding is kept: the goto below is relative and resolves through the
// config's baseURL, so the guard is the whole point of this block.
if (!process.env.BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Preconditions named once, so an environment that never rendered the tab fails with the
// reason rather than with an unexplained missing element on every test.
const NO_STRIP_HINT =
  "The Optimize tab strip never rendered. /optimise gates its tabs on the session's module grants " +
  "(insights / recommendations / autooptimize) — check the run user still has read access to them.";

const NO_BODY_HINT =
  "The Cost tab never mounted OptimizeNewPage. The tab is index 1 of filterOptions in " +
  "app/src/pages/optimise/index.jsx and is not feature-flagged — check the tab strip still renders it.";

// Opens the Cost tab by hash. The page resolves its tab from window.location.hash on
// every filterOptions change, and OptimizeNewPage's updateUrl re-attaches the current
// hash on each filter write, so a deep link here survives the page's own URL rewrites.
// Clicking the strip is exercised on its own by OptimizeModule.spec.ts, so the tests
// here do not all depend on that one interaction.
//
// The fragment is `cost` while the tab's anchor id is still `recommendations`, which is
// why the selected-tab assertion is made against RecommendationsTab.
export async function openCostTab(page: Page): Promise<CostLocators> {
  const locators = new CostLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/optimise#cost");
  await expect(locators.SummaryTab, NO_STRIP_HINT).toBeVisible({ timeout: 60000 });
  await expectSelectedTab(locators.RecommendationsTab);
  await expect(locators.recommendationsRoot, NO_BODY_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// The list has settled when it is showing rows or the empty copy that explains why it is
// not. Both empty strings are accepted because which one renders depends on whether a
// filter is active, and callers use this before and after filtering.
export async function expectListSettled(locators: CostLocators): Promise<void> {
  await waitForRecommendations(locators);
  await expect(
    locators.recommendationsRows.first().or(locators.recommendationsNoMatchText).or(locators.recommendationsNoDataText).first()
  ).toBeVisible({ timeout: 60000 });
}

// True when the tenant has at least one recommendation to act on. Tests that need a row
// branch on this rather than assuming the shared environment holds data — an empty
// tenant is a legitimate pass, asserted against the documented empty state.
export async function hasRecommendations(locators: CostLocators): Promise<boolean> {
  // Waiting for the first row or an empty state, not merely for the tbody to attach: the
  // container can attach a render before its rows do, and a count taken in that gap reads
  // 0 and would skip a test that had data to run against.
  await expect(
    locators.recommendationsRows.first().or(locators.recommendationsNoMatchText).or(locators.recommendationsNoDataText).first()
  ).toBeVisible({ timeout: 60000 });
  return (await locators.recommendationsRows.count()) > 0;
}

// The card strip has finished counting when the All card stops showing its loading
// placeholder. This matters before any card is read for its muted state: `muted` is
// `count === 0 && !cardsLoading`, so mid-load every card reports itself enabled and a
// chooser would happily pick an empty one.
export async function waitForCategoryCards(locators: CostLocators): Promise<void> {
  await expect(locators.summaryCardAll).toBeVisible({ timeout: 60000 });
  await expect(locators.summaryCardAll).not.toContainText("…", { timeout: 60000 });
}

// The first category card the tenant actually has recommendations for. A muted card is
// rendered with aria-disabled and no click handler at all, so picking one would assert
// against an interaction the app never wired up. Returns null when every category is
// empty, which is the genuinely-empty case the callers skip on.
export async function firstEnabledCategory(locators: CostLocators): Promise<CategoryCard | null> {
  await waitForCategoryCards(locators);

  for (const card of CATEGORY_CARDS) {
    const locator = locators.categoryCard(card.testId);
    await expect(locator).toBeVisible({ timeout: 30000 });
    if ((await locator.getAttribute("aria-disabled")) !== "true") {
      return card;
    }
  }
  return null;
}

// The first safety band with something in it. Same reasoning as the cards: a band whose
// count is 0 renders muted with pointerEvents:none, so a click on it would hang rather
// than filter. Read through enabledSafetyChip rather than off aria-disabled, because
// ds/Chip only sets that attribute on the branch that is already interactive.
export async function firstEnabledSafetyBand(locators: CostLocators): Promise<string | null> {
  for (const band of SAFETY_BANDS) {
    await expect(locators.safetyChip(band)).toBeVisible({ timeout: 60000 });
    if ((await locators.enabledSafetyChip(band).count()) > 0) {
      return band;
    }
  }
  return null;
}

// Whether the listing holds more rows than the page on screen, read off the settled
// "Showing a-b of n results" footer the caller has already asserted visible. Derived
// from that text rather than from the presence of a page-2 button, because count() does
// not wait and would read 0 while MUI Pagination is still rendering — skipping a test
// that had a second page to reach.
export function hasPageBeyond(summaryText: string): boolean {
  const match = summaryText.match(PAGINATION_SUMMARY_PATTERN);
  if (!match) {
    return false;
  }
  const toNumber = (value: string) => Number(value.replace(/,/g, ""));
  return toNumber(match[2]) > toNumber(match[1]);
}

// The resource name a row is showing. The Resource cell stacks the name over the account
// and resource type, so the first line is the name — which is also what the detail
// panel's header renders, both from getResourceDisplayName.
export async function resourceNameOf(locators: CostLocators, row: Locator): Promise<string> {
  const cell = locators.rowResourceCell(row);
  await expect(cell).toBeVisible({ timeout: 30000 });
  const text = await cell.innerText();
  return (text.split("\n")[0] || "").trim();
}

// The first row whose resource name is non-empty, so the detail-panel assertion has a
// real string to match on. getResourceDisplayName falls back to '' for the table, and a
// recommendation whose resource could not be named would give the test nothing to check.
export async function firstNamedRow(locators: CostLocators, limit = 5): Promise<{ row: Locator; name: string } | null> {
  const available = Math.min(limit, await locators.recommendationsRows.count());
  for (let index = 0; index < available; index++) {
    const row = locators.recommendationsRows.nth(index);
    const name = await resourceNameOf(locators, row);
    if (name) {
      return { row, name };
    }
  }
  return null;
}
