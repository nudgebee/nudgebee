import { Page, expect } from "@playwright/test";
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
  await clickFilterDropdown(page, filterId, filterLabel);
  await selectDropdownOption(page, filterValue);
  await waitForGraphQLAndValidate(
    page,
    async () => { await searchAlert(page, alertName); },
    { testName: testInfo.title, operationNames: [] }
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

export async function toggleAlert(
  page: Page,
  action: "disable" | "enable",
  alertName: string,
  testInfo: { title: string }
): Promise<void> {
  await clickAlertRowMenu(page, alertName);
  await expect(page.getByRole("menuitem", { name: new RegExp(action, "i") })).toBeVisible();
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await page.getByRole("menuitem", { name: new RegExp(action, "i") }).click();
      await clickDialogSubmit(page);
    },
    { testName: testInfo.title, operationNames: [] }
  );
  const toastText    = action === "disable" ? /disabled successful/i : /enabled successful/i;
  const toastVisible = await page.getByText(toastText).waitFor({ state: "visible", timeout: 5000 }).then(() => true).catch(() => false);
  if (!toastVisible) await expect(page.getByText(alertName)).toBeVisible({ timeout: 10000 });
}
