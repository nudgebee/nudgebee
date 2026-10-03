// Not for OSS

import { Page, expect } from "@playwright/test";
import { UsageLimitsLocators } from "./usageLimitsLocators";
import { LIMIT_MONTHLY_COST, MAX_ACCOUNT_ATTEMPTS, MODULE_LABEL_EVENT_ANALYSIS } from "./usageLimitsConstants";

// The tenant this run is pointed at. Read from the environment rather than
// written into the suite: the same specs run against dev and test, which carry
// different tenant names, and a hardcoded one would assert the wrong thing on
// whichever environment it was not copied from. Same contract
// tests/admin/TenantSettings already reads this key under.
export function requiredTenantName(): string {
  const tenant = process.env.SWITCH_TENANT || "";
  if (!tenant) throw new Error("SWITCH_TENANT is not set — add it to .env / .env.dev");
  return tenant;
}

// Lands on Admin -> AI & Tools -> Budgets & Limits with the Usage & Limits
// toggle selected, which is the sub-tab's own initial state.
export async function openUsageLimits(page: Page): Promise<UsageLimitsLocators> {
  const locators = new UsageLimitsLocators(page);
  await locators.open();
  return locators;
}

// Reloads the page and waits for the tab body to come back. This is what
// separates a persisted config from one still held in React state: the reload
// refetches listBudgetConfigs from the backend, and the hash survives it so the
// sub-tab resolves again on mount.
export async function reloadUsageLimits(page: Page): Promise<UsageLimitsLocators> {
  await page.reload({ waitUntil: "domcontentloaded" });
  const locators = new UsageLimitsLocators(page);
  await locators.waitForUsageBody();
  return locators;
}

// Walks the Account select until it finds an account with no Event Analysis
// budget yet, leaving the dialog set to that account. Returns the account's
// label, which is the same `${account_name} (${cloud_provider})` string the
// configs list renders as its scope heading.
//
// The dialog's own duplicate guard is the oracle here rather than scraping the
// configs list: BudgetEditModal disables Create whenever the picked
// (scope, entity, module) already has a config, so an enabled Create button is
// the app itself saying this account is free.
export async function selectAccountWithoutEventAnalysisBudget(locators: UsageLimitsLocators): Promise<string> {
  const labels = await locators.accountOptionLabels();
  if (labels.length === 0) throw new Error("the Account select offered no accounts — this tenant has none to write a budget against");

  const candidates = labels.slice(0, MAX_ACCOUNT_ATTEMPTS);
  for (const label of candidates) {
    await locators.chooseAccount(label);
    // hasConflict is derived during render from configs fetched before the
    // dialog opened, so the button's state is final as soon as the trigger
    // shows the picked account — there is no request to wait on.
    if (await locators.createBtn.isEnabled()) return label;
  }

  throw new Error(
    `the first ${candidates.length} accounts all already have an ${MODULE_LABEL_EVENT_ANALYSIS} budget — ` +
      "the create case needs one free account to write against"
  );
}

// Saves a budget whose every limit is switched off. Turning Monthly Cost off is
// deliberate: the config still persists as a real record, but it imposes no cost
// or count ceiling, so a run against the shared dev tenant cannot throttle
// anyone's LLM calls while the row is alive. It also sidesteps the per-limit max
// caps, which differ per environment.
export async function createBudgetWithNoLimits(locators: UsageLimitsLocators): Promise<void> {
  await locators.limitToggle(LIMIT_MONTHLY_COST).uncheck();
  await expect(locators.limitInput(LIMIT_MONTHLY_COST)).toBeDisabled();
  await locators.createBtn.click();
  await locators.budgetDialog.waitFor({ state: "detached", timeout: 60000 });
}

// Best-effort teardown of the budget this suite created. Scoped to that
// account's own Event Analysis chip rather than a loose text match, and it
// asserts the chip is gone so a silently failed cleanup cannot look like a
// success.
export async function deleteAccountBudgetIfPresent(locators: UsageLimitsLocators, accountLabel: string): Promise<void> {
  try {
    // A run that failed before the save leaves no account config at all, and the
    // section is not rendered without one — so its absence is a normal outcome
    // here and the cheapest possible answer to "is there anything to remove".
    if (!(await locators.otherAccountsSectionPresent())) return;
    await locators.expandOtherAccounts();
    const chip = locators.moduleChip(accountLabel, MODULE_LABEL_EVENT_ANALYSIS);
    const deleteBtn = locators.deleteChipBtn(accountLabel, MODULE_LABEL_EVENT_ANALYSIS);
    // A run that never got as far as saving leaves nothing to remove, so
    // absence here is a normal outcome and must come back as false.
    const present = await deleteBtn
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
    if (!present) return;
    await deleteBtn.click();
    await locators.confirmDeleteBtn.waitFor({ state: "visible", timeout: 20000 });
    await locators.confirmDeleteBtn.click();
    await expect(chip).toHaveCount(0, { timeout: 60000 });
  } catch (error) {
    console.warn(`[cleanup] the generated account budget could not be removed: ${error}`);
  }
}
