// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { MyUsageLocators, KPI_TENANT_SPEND, PERIOD_HEADER_PATTERN } from "./myUsageLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// app/src/api1/budget/index.ts names the per-account budget read this way; it is
// issued on every mount of LLMConsumptionTab and again whenever the account changes.
export const OP_BUDGET_STATUS = "AIBudgetStatus";

// The tenant this run is pointed at, read from the environment rather than written
// into the suite: dev and test carry different tenant names. Same key
// tests/admin/UsageLimits reads for the same tile.
export function requiredTenantName(): string {
  const tenant = process.env.SWITCH_TENANT || "";
  if (!tenant) throw new Error("SWITCH_TENANT is not set — add it to .env / .env.dev");
  return tenant;
}

// The header interpolates the budget period's month, so a loaded tab must name the
// current year — a "—" there means the read never landed.
export function currentYear(): string {
  return String(new Date().getFullYear());
}

// The tab shows "Usage for —" and no tiles until AIBudgetStatus resolves, then the
// KPI strip; the tenant spend tile is unconditional, so it is the end-of-load signal.
export async function waitForUsageLoaded(locators: MyUsageLocators): Promise<void> {
  await expect(locators.usageHeader).toHaveText(PERIOD_HEADER_PATTERN, { timeout: 40000 });
  await expect(locators.kpiTile(KPI_TENANT_SPEND)).toBeVisible({ timeout: 40000 });
}

// Selects the My Usage sub-tab and waits for its budget-status read to come back
// clean; Insights must already be the selected group.
export async function selectMyUsageTab(page: Page, locators: MyUsageLocators, testName: string): Promise<void> {
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.myUsageTab.click();
    },
    { testName, operationNames: OP_BUDGET_STATUS, timeoutMs: 45000 }
  );
  await waitForUsageLoaded(locators);
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Insights > My Usage. The
// modal inherits the page's own account (NubiBrainNav passes resolvedAccountId), and
// the login lands on a cluster, so the header filter opens narrowed to that account.
// Every case here works in that account-scoped view and switches BETWEEN accounts.
export async function openMyUsage(page: Page): Promise<MyUsageLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new MyUsageLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openBCortex(locators.insightsGroupTab);
  await locators.insightsGroupTab.click();
  // A tenant with the b-Cortex module off renders the placeholder instead of the
  // strip; raced rather than asserted straight away so it reports itself by name.
  await expect(locators.myUsageTab.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 40000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await selectMyUsageTab(page, locators, "My Usage - open the tab");
  return locators;
}

// Leaves My Usage for Digests and comes back, which unmounts LLMConsumptionTab and
// makes it refetch — the only way to tell a re-read view from one still held in
// React state.
export async function remountMyUsage(page: Page, locators: MyUsageLocators): Promise<void> {
  await locators.digestsTab.click();
  await locators.usageHeader.waitFor({ state: "detached", timeout: 30000 });
  await selectMyUsageTab(page, locators, "My Usage - remount the tab");
}

// The label the header filter currently shows — an account name on this page.
export async function selectedAccountLabel(locators: MyUsageLocators): Promise<string> {
  return ((await locators.accountSelect.innerText()) ?? "").trim();
}

// Opens the picker (if it is not already open) and expands its groups until the
// option is on screen. Every group renders collapsed behind a header button and
// each header toggles its group, so expansion stops as soon as the option is
// reachable. Attribute selectors throughout: the open panel makes MUI aria-hide the
// dialog around it.
async function revealOption(locators: MyUsageLocators, option: Locator): Promise<void> {
  if (!(await locators.openListbox.isVisible())) {
    await locators.accountSelect.click();
    await locators.openListbox.waitFor({ state: "visible", timeout: 15000 });
  }
  const groupHeaders = locators.openListbox.locator('[role="button"]');
  const options = locators.openListbox.locator('[role="option"]');
  // A single-provider tenant renders a flat list: no headers, options visible directly.
  await expect
    .poll(async () => (await groupHeaders.count()) + (await options.count()), {
      timeout: 15000,
      message: "the account picker never listed a group or an account",
    })
    .toBeGreaterThan(0);
  const groupCount = await groupHeaders.count();
  for (let i = 0; i < groupCount; i++) {
    // "This group does not hold the option" is a normal branch, so a short probe
    // rather than the assertion timeout — and the probe must come back false.
    if (await option.isVisible()) break;
    await groupHeaders.nth(i).click();
    await option.waitFor({ state: "visible", timeout: 2000 }).catch(() => {});
  }
  await expect(option).toBeVisible({ timeout: 15000 });
}

// Switches the header filter to the first real account other than the one in
// scope and waits for the account-scoped budget read that the change issues.
// Returns the new account's label, which the account tile, the scope chip and the
// matrix's Account row all render (with the provider appended).
export async function switchToAnotherAccount(page: Page, locators: MyUsageLocators): Promise<string> {
  const current = await selectedAccountLabel(locators);
  const option = locators.otherAccountOption(current);
  await revealOption(locators, option);
  const label = ((await option.innerText()) ?? "").trim();
  expect(label, "the account picker offers no account other than the one in scope — this case needs two accounts").not.toBe("");
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await option.click();
      await locators.openListbox.waitFor({ state: "detached", timeout: 15000 });
    },
    { testName: "My Usage - switch to another account", operationNames: OP_BUDGET_STATUS, timeoutMs: 45000 }
  );
  await expect(locators.accountSelect).toContainText(label, { timeout: 15000 });
  await waitForUsageLoaded(locators);
  return label;
}

// Opens the picker without choosing anything and expands groups until a real
// account is on screen.
export async function peekAccountPicker(locators: MyUsageLocators): Promise<void> {
  await revealOption(locators, locators.realAccountOption);
}

// Escape is routed by MUI to the topmost modal — the panel — so b-Cortex stays open.
export async function closeAccountPicker(page: Page, locators: MyUsageLocators): Promise<void> {
  await page.keyboard.press("Escape");
  await locators.openListbox.waitFor({ state: "detached", timeout: 15000 });
  await expect(locators.bCortexDialog).toBeVisible();
}
