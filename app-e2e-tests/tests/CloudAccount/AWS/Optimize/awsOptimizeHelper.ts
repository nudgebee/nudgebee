// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AwsOptimizeLocators, OptimizeCategoryTab } from "./awsOptimizeLocators";

// Every test in this area drives a shared dev cloud account, so the timeout has to cover
// login, the integration check, account selection and the sub-tab's own first fetch.
export const OPTIMIZE_TIMEOUT_MS = 180000;

// The Optimize flyout entry for each category, and the URL fragment it lands on. Both are
// already declared on AWSLocators, so they are mapped here rather than re-declared.
function categoryTarget(locators: AwsOptimizeLocators, tab: OptimizeCategoryTab): { tab: Locator; url: RegExp } {
  const targets: Record<OptimizeCategoryTab, { tab: Locator; url: RegExp }> = {
    "right-sizing": { tab: locators.OptimizeRightSizing, url: locators.OptimizeRightSizingUrl },
    configuration: { tab: locators.OptimizeConfiguration, url: locators.OptimizeConfigurationUrl },
    security: { tab: locators.OptimizeSecurityTab, url: locators.OptimizeSecurityTabUrl },
    "infra-upgrade": { tab: locators.OptimizeInfraUpgrade, url: locators.OptimizeInfraUpgradeUrl },
  };
  return targets[tab];
}

// Opens one Optimize category the way a person does: log in, land on the AWS cloud
// account, then hover the Optimize anchor and pick the sub-tab from its flyout.
//
// Deliberately NOT a goto('...#optimize/security') deep link — the detail page derives
// selectedSubTab from its own tab state, and the sub-tabs live behind AnchorComponent's
// hover popover, which is the only route a user has. navigateToSubTab (AWSLocators) is the
// merged, retrying implementation of that hover-and-click, so it is reused here.
export async function openOptimizeCategory(page: Page, tab: OptimizeCategoryTab): Promise<AwsOptimizeLocators> {
  const locators = new AwsOptimizeLocators(page);

  // doFullLogin already suppresses the guided tours and installs the overlay guard.
  await new LoginPage(page).doFullLogin();
  await locators.openAWSCloudAccountFromConfig();

  await switchOptimizeCategory(locators, page, tab);
  return locators;
}

// Moves to another Optimize category within one test, without paying for a second login.
// Same hover-and-click route as the initial landing.
export async function switchOptimizeCategory(
  locators: AwsOptimizeLocators,
  page: Page,
  tab: OptimizeCategoryTab
): Promise<void> {
  const target = categoryTarget(locators, tab);
  await locators.navigateToSubTab(locators.AnchorTabOptimize, target.tab, target.url);
  await parkCursor(page);

  // The panel root mounting is the signal the sub-tab finished switching. Waiting on it
  // rather than on networkidle: each category is a distinct ListingLayout node, so its
  // presence is unambiguous, and the listing keeps fetching filter options after the
  // table itself has painted.
  await expect(locators.categoryRoot(tab)).toBeVisible({ timeout: 60000 });
}

// Opens the fifth Optimize sub-tab, which is a different component with a root of its own.
export async function switchToResolution(locators: AwsOptimizeLocators, page: Page): Promise<void> {
  await locators.navigateToSubTab(
    locators.AnchorTabOptimize,
    locators.OptimizeRecommendationResolution,
    locators.OptimizeRecommendationResolutionUrl
  );
  await parkCursor(page);
  await expect(locators.resolutionRoot).toBeVisible({ timeout: 60000 });
}

// Parks the cursor in open page content after a tab change. Left on the anchor strip,
// AnchorComponent keeps its flyout open over the toolbar and the next click hits that
// instead; parked at 0,0 it sits on the sidebar rail, whose own flyout is a Popover with an
// invisible page-wide backdrop. Mid-viewport is neither.
async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}

// Number of recommendations currently listed, once the table has settled.
//
// CustomTable only renders <tbody id={`${tableId}-body`}> when it holds rows, so a
// category with no recommendations resolves to 0 here instead of timing out — absence is
// the answer, not a failure, which is why the wait is allowed to come back false.
export async function optimizeRowCount(locators: AwsOptimizeLocators, tab: OptimizeCategoryTab): Promise<number> {
  // Races the first row against the empty state so a category with no recommendations
  // resolves as soon as CustomTable says so, instead of burning the full timeout on every
  // call — this runs in most of the tests here, so waiting it out would cost minutes.
  //
  // The swallow is the point: neither side arriving is still an answer callers handle (0),
  // so this must settle to a count rather than throw. Each side carries its own catch
  // rather than one around the race — the loser stays pending and would otherwise reject
  // unhandled after the winner has already returned.
  await Promise.race([
    locators.rows(tab).first().waitFor({ state: "visible", timeout: 45000 }).catch(() => {}),
    locators.emptyState(tab).waitFor({ state: "visible", timeout: 45000 }).catch(() => {}),
  ]);
  return locators.rows(tab).count();
}

// Opens `trigger` and toggles `label` on a multi-select FilterDropdown, then dismisses the
// panel.
//
// `action` says which way the toggle is expected to go, because the confirmation differs.
// ds/FilterDropdown options are toggles (`onClick={() => onToggle(opt)}`,
// FilterDropdown.jsx:202), so clicking an already-committed option clears it — and the
// trigger, which lists its committed labels (FilterDropdown.jsx:1135-1142), drops the label
// again. Asserting toContainText on that path would wait out the full timeout and fail.
//
// Waits on the options rather than on the search box: FilterDropdown renders the box only
// above eight options, so waiting for it would burn a full timeout on the five-option
// Severity list. Modelled on the merged AWS Monitoring chooseFilterOption — the shared
// selectDropdownOption in tests/utils/helpers.ts does not fit, as it waits for a
// role='listbox' that ds/FilterDropdown's Popover never renders.
//
// The Escape is what makes this usable for the multi-select filters: they keep their panel
// open after a selection so more values can be picked, and the open Popover's backdrop
// swallows the next click in the test.
export async function chooseFilterOption(
  locators: AwsOptimizeLocators,
  page: Page,
  trigger: Locator,
  label: string,
  action: "select" | "deselect" = "select"
): Promise<void> {
  await trigger.click();
  await locators.visibleOptions.first().waitFor({ state: "visible", timeout: 30000 });

  // Options and the search box render in the same pass, so by this point its absence is a
  // settled answer and needs no timeout of its own.
  const search = locators.filterSearchInput;
  if (await search.isVisible()) {
    await search.fill(label);
  }

  const option = locators.filterOption(label);
  await option.click();

  // The option carries the component's own aria-selected state (FilterDropdown.jsx:201), so
  // it is asserted first in both directions: it is the toggle's real outcome rather than a
  // rendering of it, and it settles before the trigger re-renders.
  await expect(option).toHaveAttribute("aria-selected", action === "select" ? "true" : "false");

  // Then the trigger, which is what the rest of the test reads. Committing shows the label
  // beside the filter's name; clearing removes it again.
  if (action === "select") {
    await expect(trigger).toContainText(label);
  } else {
    await expect(trigger).not.toContainText(label);
  }

  await page.keyboard.press("Escape");
  await expect(locators.visibleOptions).toHaveCount(0);
}

// Reads one filter option's label without committing it, so a test can filter by a value
// the account actually holds instead of a guessed one. Returns null when the filter offers
// nothing — an account with no recommendations of this category is the expected reason,
// and callers skip on it rather than fail.
export async function firstFilterOptionLabel(
  locators: AwsOptimizeLocators,
  page: Page,
  trigger: Locator
): Promise<string | null> {
  await trigger.click();
  // Absence is a real answer here: listRecommendationFilter returns an empty list for a
  // category the account holds nothing in, and FilterDropdown then renders no options at
  // all. The catch is what turns that into null instead of a throw.
  const appeared = await locators.visibleOptions
    .first()
    .waitFor({ state: "visible", timeout: 30000 })
    .then(() => true)
    .catch(() => false);

  const label = appeared ? ((await locators.visibleOptions.first().textContent()) ?? "").trim() : "";

  await page.keyboard.press("Escape");
  await expect(locators.visibleOptions).toHaveCount(0);

  return label === "" ? null : label;
}
