// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { AutomationsLocators } from "./automationsLocators";

// WorkflowListing seeds every filter from localStorage when the URL carries no
// query for it, so a filter one test picks would still be applied in the next.
// Clearing the two automation keys before the app boots is what makes these
// specs order-free; the prefix covers both the per-tab filter key and the
// account key the Automations and Executions tabs share.
const FILTER_STORAGE_PREFIX = "nudgebee:automation:";

// A term no automation name can contain, so the empty-state assertion is about
// the search working rather than about what the dev tenant happens to hold.
export const NO_MATCH_TERM = "zz-no-such-automation-e2e";

export async function resetPersistedFilters(page: Page): Promise<void> {
  await page.addInitScript((prefix) => {
    try {
      Object.keys(window.localStorage)
        .filter((key) => key.startsWith(prefix))
        .forEach((key) => window.localStorage.removeItem(key));
    } catch {
      // localStorage unavailable — the app's own reads no-op the same way.
    }
  }, FILTER_STORAGE_PREFIX);
}

// Logs in and lands on /automation#automations with the listing settled.
export async function openAutomations(page: Page): Promise<AutomationsLocators> {
  const locators = new AutomationsLocators(page);

  await resetPersistedFilters(page);
  await new LoginPage(page).doFullLogin();

  await locators.automationSidenavBtn.waitFor({
    state: "visible",
    timeout: 30000,
  });
  await locators.automationSidenavBtn.click();
  // Anchored: an unanchored /automation also matches the builder at
  // /automation/<id>, so a stray navigation there would satisfy this wait.
  // toHaveURL polls the URL rather than waiting on a load event, so it holds
  // whether the rail navigates with a full load or a client-side push.
  await expect(page).toHaveURL(/\/automation(\?|#|$)/, { timeout: 30000 });

  await locators.automationsTab.waitFor({ state: "visible", timeout: 30000 });
  await locators.automationsTab.click();
  // Park the cursor off the tab strip: AnchorComponent opens a hover popover over
  // the toolbar underneath, and the next click lands on that instead.
  await page.mouse.move(640, 500);
  await expectAutomationsTabSelected(locators);

  await expect(locators.listingBox).toBeVisible({ timeout: 30000 });
  await expectListingSettled(locators);

  return locators;
}

// AnchorComponent marks the open tab with data-tab-selected, which is the app's
// own notion of which tab is rendered and, unlike the URL, always in step with it.
export async function expectAutomationsTabSelected(locators: AutomationsLocators): Promise<void> {
  await expect(locators.automationsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 15000 });
}

// The listing always resolves into one of two states, so waiting for either is
// what separates "the fetch has landed" from "the skeleton is still up".
// The alternation ends in .first(): if a render ever had both a row and the
// empty state mounted, the two-way match would be a strict-mode violation
// rather than the "either one" wait this is meant to be.
export async function expectListingSettled(locators: AutomationsLocators): Promise<void> {
  await expect(locators.nameLinks.first().or(locators.emptyState).first()).toBeVisible({
    timeout: 60000,
  });
}

// Search is Enter-committed and refetches server-side (committedSearchName in
// WorkflowListing), so the value landing in the box is not yet the new listing.
export async function searchByName(locators: AutomationsLocators, term: string): Promise<void> {
  await locators.nameSearch.click();
  await locators.nameSearch.fill(term);
  await expect(locators.nameSearch).toHaveValue(term, { timeout: 10000 });
  await locators.nameSearch.press("Enter");
  await expectListingSettled(locators);
}

export async function clearNameSearch(locators: AutomationsLocators): Promise<void> {
  await locators.nameSearch.fill("");
  await expect(locators.nameSearch).toHaveValue("", { timeout: 10000 });
  await locators.nameSearch.press("Enter");
  await expectListingSettled(locators);
}

export async function pickFilterOption(page: Page, locators: AutomationsLocators, trigger: Locator, optionLabel: string): Promise<void> {
  await trigger.click();
  const option = locators.filterOption(optionLabel);
  await expect(option).toBeVisible({ timeout: 15000 });
  await option.click();
  // The panel is a portaled overlay; Escape closes it without re-triggering the
  // dropdown, which a click on the page body can do.
  await page.keyboard.press("Escape");
  await expect(option).toBeHidden({ timeout: 15000 });
  await expectListingSettled(locators);
}

// Every automation name currently listed. Read through expect.poll at the call
// sites so a refetch in flight is retried rather than snapshotted.
export async function listedNames(locators: AutomationsLocators): Promise<string[]> {
  const names = await locators.nameLinks.allInnerTexts();
  return names.map((name) => name.trim());
}

export async function columnTexts(locators: AutomationsLocators, headerName: string): Promise<string[]> {
  const index = await locators.columnIndex(headerName);
  const texts = await locators.columnCellsAt(index).allInnerTexts();
  return texts.map((text) => text.trim());
}

// Leaves the automation builder by its Back button, clearing the exit guard if
// the app raises one. The builder blocks routeChangeStart whenever the
// automation carries unsaved or unpublished changes, so on some automations Back
// opens a confirmation instead of navigating — and which one you get depends on
// the automation the listing happened to return, not on anything the test did.
// "Leave page" only abandons the navigation block; it discards nothing server-side.
export async function leaveBuilder(page: Page, locators: AutomationsLocators): Promise<void> {
  await locators.builderBackBtn.click();

  // Probe, not an assertion: an automation with nothing pending navigates
  // straight away and never renders this dialog, which is the normal case.
  const guarded = await locators.exitConfirmDialog
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);

  if (guarded) {
    await locators.exitLeaveBtn.click();
    await expect(locators.exitConfirmDialog).toBeHidden({ timeout: 15000 });
  }
}

// Opens one row's three-dot menu and clicks an item in it.
export async function chooseRowMenuItem(locators: AutomationsLocators, row: Locator, itemId: string, itemLabel: string): Promise<void> {
  const trigger = locators.rowMenuTrigger(row);
  await expect(trigger).toBeVisible({ timeout: 15000 });
  await trigger.click();

  const item = locators.openMenuItem(itemId, itemLabel);
  await expect(item).toBeVisible({ timeout: 15000 });
  await item.click();
}

// Deletes one automation by name and waits for the listing to stop carrying it.
// Used for cleanup, so it re-runs the search first: the row it must act on is the
// one the server has now, not the one the table held before the create.
export async function deleteAutomationByName(locators: AutomationsLocators, name: string): Promise<void> {
  await searchByName(locators, name);
  const row = locators.rowFor(name);
  await expect(row).toBeVisible({ timeout: 30000 });

  await chooseRowMenuItem(locators, row, "delete", "Delete");
  await expect(locators.deleteModalConfirmBtn).toBeVisible({ timeout: 15000 });
  await locators.deleteModalConfirmBtn.click();
  await expect(locators.deleteModalConfirmBtn).toBeHidden({ timeout: 30000 });
}

// How many rows currently carry exactly this name. The listing refetches on
// search, so this is read after searchByName rather than off a stale table.
export async function countNamed(locators: AutomationsLocators, name: string): Promise<number> {
  return locators.nameLink(name).count();
}

// The account whose automation configs the Configs modal is opened against. Same
// key the Task Runner specs use for their account picker.
export const CONFIG_ACCOUNT = process.env.CLUSTER || "";

// Opens the Configs modal and makes sure it is scoped to an account, which is what
// unlocks Add Config (ConfigurationManager's canEdit is account-scoped).
export async function openConfigsModal(locators: AutomationsLocators): Promise<void> {
  await locators.configsBtn.click();
  await expect(locators.configsModal).toBeVisible({ timeout: 30000 });

  // Probe, not an assertion: a tenant with exactly one writable account has it
  // pre-selected, so there is no picker interaction left to perform.
  const alreadyEnabled = await locators.addConfigBtn
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);
  if (alreadyEnabled) return;

  if (!CONFIG_ACCOUNT) {
    throw new Error("CLUSTER is not set — add it to .env / .env.dev so the Configs modal can be scoped to an account");
  }

  await locators.configAccountSelect.click();
  await expect(locators.configOverlaySearch).toBeVisible({ timeout: 15000 });
  await locators.configOverlaySearch.fill(CONFIG_ACCOUNT);

  const option = locators.filterOption(CONFIG_ACCOUNT);
  await expect(option).toBeVisible({ timeout: 15000 });
  await option.click();

  // Read back: a picker that committed a different account would scope the whole
  // test to someone else's configs.
  await expect(locators.configAccountSelect).toContainText(CONFIG_ACCOUNT, {
    timeout: 15000,
  });
  await expect(locators.addConfigBtn).toBeVisible({ timeout: 30000 });
}
