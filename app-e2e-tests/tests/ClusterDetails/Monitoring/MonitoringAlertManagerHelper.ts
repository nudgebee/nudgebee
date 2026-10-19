import { Page, TestInfo, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "./MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

export async function setup(page: Page): Promise<MonitoringTabLocator> {
  const locators = new MonitoringTabLocator(page);
  await new LoginPage(page).doFullLogin();
  await navigateToAlertManager(page, locators);
  return locators;
}

export async function navigateToAlertManager(page: Page, locators: MonitoringTabLocator): Promise<void> {
  await page.keyboard.press("Escape");
  await page.mouse.move(0, 0);
  await locators.navigateToMonitoringTab();
  for (let attempt = 1; attempt <= 3; attempt++) {
    const isVisible = await locators.MonitoringDropdownAlertManager.isVisible().catch(() => false);
    if (!isVisible) {
      await page.mouse.move(0, 0);
      await page.keyboard.press("Escape");
      await locators.AnchorTabMonitoring.hover({ force: true });
      await page.waitForTimeout(400);
    }

    const clicked = await locators.MonitoringDropdownAlertManager
      .waitFor({ state: "visible", timeout: 5000 })
      .then(() => locators.MonitoringDropdownAlertManager.click())
      .then(() => true)
      .catch(() => false);

    if (clicked) break;
    if (attempt === 3) await locators.MonitoringDropdownAlertManager.click({ timeout: 10000 });
  }

  await page.locator("#k8s-alert-filter-severity")
    .or(page.locator("#alert-manager-list-box"))
    .first()
    .waitFor({ state: "visible", timeout: 30000 });
}

export async function searchAlert(page: Page, name: string): Promise<void> {
  const primary = page.locator("#k8s-alert-name-search");
  const input   = await primary.waitFor({ state: "visible", timeout: 3000 }).then(() => true).catch(() => false)
    ? primary
    : page.getByPlaceholder(/search by name/i);
  await input.fill(name);
  await page.keyboard.press("Enter");
}

export async function selectDropdownOption(page: Page, optionText: string): Promise<void> {
  await page.locator("li[role='option'], [role='option']")
    .filter({ hasText: new RegExp(`^${optionText}$`, "i") })
    .first()
    .click({ timeout: 15000 });
}

export async function clickFilterDropdown(page: Page, primaryId: string, labelText: string): Promise<void> {
  const primary    = page.locator(`#${primaryId}`);
  const usePrimary = await primary.waitFor({ state: "visible", timeout: 8000 }).then(() => true).catch(() => false);
  if (usePrimary) {
    await primary.click();
    return;
  }
  const fallback = page
    .locator(`[id*="alert-filter-${labelText.toLowerCase()}"]`)
    .or(page.getByRole("button", { name: new RegExp(labelText, "i") }).first())
    .first();
  await fallback.waitFor({ state: "visible", timeout: 10000 });
  await fallback.click();
}

export async function applyFilterAndSearch(
  page: Page,
  filterId: string,
  filterLabel: string,
  filterValue: string,
  alertName: string,
  testInfo: { title: string }
): Promise<void> {
  await waitForGraphQLAndValidate(
    page,
    async () => {
      // Filter select is inside the window: re-searching an unchanged name issues no refetch.
      await clickFilterDropdown(page, filterId, filterLabel);
      await selectDropdownOption(page, filterValue);
      await searchAlert(page, alertName);
    },
    // Named, not auto-capture. Auto mode only ever waits 10s regardless of the timeout
    // asked for, so on a slow runner the refetch lands after the window closes and the
    // watcher reports "No operations captured" for a search that did fire. GetEventRules
    // is the query the listing re-issues on search.
    { testName: testInfo.title, operationNames: ["GetEventRules"] }
  );
}

export async function clickAlertRowMenu(page: Page, alertName: string): Promise<void> {
  const row = page.locator("tr", { hasText: alertName });
  await row.waitFor({ state: "visible", timeout: 10000 });
  await row.locator("button").last().click();
}

export async function clickDialogSubmit(page: Page): Promise<void> {
  const dialog    = page.locator('[role="dialog"]');
  await dialog.waitFor({ state: "visible", timeout: 10000 });
  const primary   = dialog.locator("#submit");
  const useSubmit = await primary.waitFor({ state: "visible", timeout: 3000 }).then(() => true).catch(() => false);
  if (useSubmit) {
    await primary.click();
  } else {
    await dialog.getByRole("button", { name: /confirm|ok|yes/i }).first().click();
  }
}

// Puts the alert in the wanted state from whatever an earlier test or run left, so no test depends on another one passing.
export async function ensureAlertState(
  page: Page,
  state: "enabled" | "disabled",
  alertName: string,
  testInfo: TestInfo
): Promise<void> {
  await searchAlert(page, alertName);
  const alertRow = page.locator("tr", { hasText: alertName });
  await expect(alertRow).toBeVisible({ timeout: 30000 });
  const isDisabled = await alertRow.locator("td").filter({ hasText: /disabled/i })
    .count().then(n => n > 0).catch(() => false);
  if (state === "disabled" && !isDisabled) await toggleAlert(page, "disable", alertName, testInfo);
  if (state === "enabled" && isDisabled) await toggleAlert(page, "enable", alertName, testInfo);
}

// Enable/disable changes the PrometheusRule on the cluster through the relay (create_or_replace_alert_rule), whose default timeout is 180s (relay.Execute).
const RULE_CHANGE_TIMEOUT_MS = 190000;

export async function toggleAlert(
  page: Page,
  action: "disable" | "enable",
  alertName: string,
  testInfo: TestInfo
): Promise<void> {
  // Each toggle may take as long as the relay allows, so the test budget grows by that much per toggle.
  testInfo.setTimeout(testInfo.timeout + RULE_CHANGE_TIMEOUT_MS);
  await clickAlertRowMenu(page, alertName);
  const toggleItem = page
    .locator("#toggle-enabled:visible")
    .or(page.locator('[role="menuitem"]:visible', { hasText: new RegExp(action, "i") }))
    .first();
  await expect(toggleItem).toBeVisible();
  // The row menu labels the toggle by the alert's real state, so a stale status cell fails here instead of toggling the wrong way.
  await expect(toggleItem).toHaveText(new RegExp(`^\\s*${action}\\s*$`, "i"));
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await toggleItem.click();
      await clickDialogSubmit(page);
    },
    // Enable and disable share this mutation.
    { testName: testInfo.title, operationNames: ["DisableAlertManagerRule"], timeoutMs: RULE_CHANGE_TIMEOUT_MS }
  );
  // KubernetesAlertManager.handleSubmit toasts "Rule <name> disabled|enabled", or "Failed to disable|enable <name>" and reverts the row.
  const escaped = alertName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const success = page.getByText(new RegExp(`Rule ${escaped} ${action}d`, "i"));
  const failure = page.getByText(new RegExp(`Failed to ${action} ${escaped}`, "i"));
  await expect(success.or(failure).first()).toBeVisible({ timeout: 15000 });
  expect(await failure.count(), `the app reported: ${await failure.first().innerText().catch(() => "")}`).toBe(0);
}
