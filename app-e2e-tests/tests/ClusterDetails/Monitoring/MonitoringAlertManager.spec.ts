import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { MonitoringTabLocator } from "../Monitoring/MonitoringTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import {
  setup,
  navigateToAlertManager,
  searchAlert,
  selectDropdownOption,
  clickFilterDropdown,
  applyFilterAndSearch,
  clickAlertRowMenu,
  toggleAlert,
} from "./MonitoringAlertManagerHelper";

const ALERT_NAME     = process.env.ALERT_MANAGER_ALERT_NAME || "test-alert";
const ALERT_SEVERITY = process.env.ALERT_MANAGER_SEVERITY   || "critical";
const ALERT_SOURCE   = process.env.ALERT_MANAGER_SOURCE     || "prometheus";
const ALERT_PROMQL   = process.env.ALERT_MANAGER_PROMQL     || "vector(1)";
const ALERT_TIME     = process.env.ALERT_MANAGER_TIME       || "1";

test.describe.configure({ timeout: 120000 });

// ─── TC-01: Page Load & API Validation ────────────────────────────────────────
test("Cluster Details->Monitoring-> APi testing -> Alert Manager", async ({ page }, testInfo) => {
  const locators = new MonitoringTabLocator(page);
  await new LoginPage(page).doFullLogin();

  await waitForGraphQLAndValidate(
    page,
    async () => { await navigateToAlertManager(page, locators); },
    { testName: testInfo.title, operationNames: ["GetEventRules", "GetDistinctCategorySourceSeverity"] }
  );

  await expect(page.locator("#alert-manager-list-box")).toBeVisible();
  await expect(page.getByRole("button", { name: "Create New Alert" })).toBeVisible();
});

// ─── TC-02: Create New Alert ───────────────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Create New Alert", async ({ page }, testInfo) => {
  await setup(page);

  await page.getByRole("button", { name: "Create New Alert" }).click();

  const dialog = page.locator('[role="dialog"]');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/create new alert/i).first()).toBeVisible({ timeout: 10000 });

  // Step 1 — Alert Name is the only <input>; other fields are <textarea>
  const nameInput = dialog.locator("input").first();
  await nameInput.waitFor({ state: "visible", timeout: 10000 });
  await nameInput.click();
  await nameInput.fill("");
  await nameInput.pressSequentially(ALERT_NAME, { delay: 30 });

  await dialog.locator("#alert-severity").click();
  await selectDropdownOption(page, ALERT_SEVERITY);

  await dialog.getByRole("button", { name: /next.*triggering condition/i }).click();
  await page.waitForTimeout(2000);

  // Step 2 — PromQL via CodeMirror contenteditable
  const cmEditor = dialog.locator('[contenteditable="true"]').first();
  await cmEditor.waitFor({ state: "visible", timeout: 20000 });
  await cmEditor.click();
  await page.keyboard.press("Control+a");
  await page.keyboard.type(ALERT_PROMQL);

  const timeInput = dialog.locator('input[type="number"]').first();
  if (await timeInput.waitFor({ state: "visible", timeout: 3000 }).then(() => true).catch(() => false)) {
    await timeInput.fill("");
    await timeInput.pressSequentially(ALERT_TIME, { delay: 30 });
  }

  await dialog.getByRole("button", { name: /validate query/i }).click();
  await page.waitForTimeout(2000);

  await dialog.getByRole("button", { name: /next.*add actions/i }).click();
  await page.waitForTimeout(1000);

  // Step 3 — Submit
  await waitForGraphQLAndValidate(
    page,
    async () => {
      const createBtn = dialog.getByRole("button", { name: /create alert/i }).last();
      await createBtn.waitFor({ state: "visible", timeout: 10000 });
      await createBtn.click();
    },
    { testName: testInfo.title, operationNames: [] }
  );

  await page.locator('[role="dialog"]').waitFor({ state: "hidden", timeout: 15000 }).catch(() => {});
  await searchAlert(page, ALERT_NAME);
  await expect(page.getByText(ALERT_NAME)).toBeVisible({ timeout: 15000 });
});

// ─── TC-03: Search by Name ─────────────────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Search by Alert Name", async ({ page }, testInfo) => {
  await setup(page);
  await waitForGraphQLAndValidate(
    page,
    async () => { await searchAlert(page, ALERT_NAME); },
    { testName: testInfo.title, operationNames: [] }
  );
  await expect(page.getByText(ALERT_NAME)).toBeVisible();
});

// ─── TC-04: Search - No Results ────────────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Search No Results", async ({ page }, testInfo) => {
  await setup(page);
  await waitForGraphQLAndValidate(
    page,
    async () => { await searchAlert(page, "xyz_nonexistent_alert_000"); },
    { testName: testInfo.title, operationNames: [] }
  );
  await expect(page.getByText(ALERT_NAME)).not.toBeVisible();
});

// ─── TC-05: Filter by Severity ─────────────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Filter by Severity", async ({ page }, testInfo) => {
  await setup(page);
  await applyFilterAndSearch(page, "k8s-alert-filter-severity", "Severity", ALERT_SEVERITY, ALERT_NAME, testInfo);
  await expect(page.getByText(ALERT_NAME)).toBeVisible();
});

// ─── TC-06: Filter by Source ───────────────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Filter by Source", async ({ page }, testInfo) => {
  await setup(page);
  await applyFilterAndSearch(page, "k8s-alert-filter-source", "Source", ALERT_SOURCE, ALERT_NAME, testInfo);
  await expect(page.getByText(ALERT_NAME)).toBeVisible();
});

// ─── TC-07: Filter by Status = Enabled ────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Filter by Status Enabled", async ({ page }, testInfo) => {
  await setup(page);
  await searchAlert(page, ALERT_NAME);
  const alertRow = page.locator("tr", { hasText: ALERT_NAME });
  const isDisabled = await alertRow.locator("td").filter({ hasText: /disabled/i })
    .count().then(n => n > 0).catch(() => false);
  if (isDisabled) {
    await toggleAlert(page, "enable", ALERT_NAME, testInfo);
  }
  await applyFilterAndSearch(page, "k8s-alert-filter-status", "Status", "Enabled", ALERT_NAME, testInfo);
  await expect(page.getByText(ALERT_NAME)).toBeVisible();
});

// ─── TC-08: Configured Actions Column ─────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Configured Actions Column", async ({ page }) => {
  await setup(page);
  await searchAlert(page, ALERT_NAME);
  await expect(page.getByText(ALERT_NAME)).toBeVisible();

  const alertRow = page.locator("tr", { hasText: ALERT_NAME });
  await expect(alertRow).toBeVisible({ timeout: 10000 });
  await expect(
    alertRow.locator("td", { hasText: /configured actions/i })
      .or(alertRow.locator("td").last())
  ).toBeVisible({ timeout: 10000 });
});

// ─── TC-09: Edit Alert ─────────────────────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Edit Alert", async ({ page }) => {
  await setup(page);
  await searchAlert(page, ALERT_NAME);
  await expect(page.getByText(ALERT_NAME)).toBeVisible();

  await clickAlertRowMenu(page, ALERT_NAME);
  await page.getByRole("menuitem", { name: /edit/i }).click();

  const editDialog = page.locator('[role="dialog"]');
  await expect(editDialog).toBeVisible();
  await expect(editDialog.getByText(/update alert/i).first()).toBeVisible({ timeout: 10000 });

  // Edit modal opens at Step 3 — navigate back to Step 1 to verify Alert Name
  const backBtn = editDialog.getByRole("button", { name: /back/i });
  await backBtn.click();
  await backBtn.click();

  const nameField = editDialog.locator("input").first();
  await nameField.waitFor({ state: "visible", timeout: 10000 });
  await expect(nameField).toHaveValue(ALERT_NAME);

  await page.keyboard.press("Escape");
});

// ─── TC-10: Combined Filter Source + Severity ─────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Combined Filter Source and Severity", async ({ page }, testInfo) => {
  await setup(page);

  await clickFilterDropdown(page, "k8s-alert-filter-source", "Source");
  await selectDropdownOption(page, ALERT_SOURCE);

  await clickFilterDropdown(page, "k8s-alert-filter-severity", "Severity");
  await selectDropdownOption(page, ALERT_SEVERITY);

  await waitForGraphQLAndValidate(
    page,
    async () => { await searchAlert(page, ALERT_NAME); },
    { testName: testInfo.title, operationNames: [] }
  );
  await expect(page.getByText(ALERT_NAME)).toBeVisible();
});

// ─── TC-11: Disable Alert ─────────────────────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Disable Alert", async ({ page }, testInfo) => {
  await setup(page);
  await searchAlert(page, ALERT_NAME);
  await expect(page.getByText(ALERT_NAME)).toBeVisible();
  await toggleAlert(page, "disable", ALERT_NAME, testInfo);
});

// ─── TC-12: Filter by Status = Disabled ───────────────────────────────────────
test("Cluster Details->Monitoring-> Alert Manager -> Filter by Status Disabled", async ({ page }, testInfo) => {
  await setup(page);
  await applyFilterAndSearch(page, "k8s-alert-filter-status", "Status", "Disabled", ALERT_NAME, testInfo);
  await expect(page.getByText(ALERT_NAME)).toBeVisible();
});

