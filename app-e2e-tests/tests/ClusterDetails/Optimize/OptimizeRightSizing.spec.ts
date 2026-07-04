import { test, expect, Page, Locator } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { OptimizeTabLocator, OptimizeSections } from "./OptimizeTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test.describe.configure({ mode: "serial" });

async function applyNamespaceFilter(page: Page, locators: OptimizeTabLocator, namespace: string): Promise<void> {
  await expect(locators.namespacedropdown).toBeEnabled({ timeout: 30000 });
  await locators.namespacedropdown.click();
  await page.locator('input[placeholder="Search..."]').fill(namespace);
  await page.locator('[role="option"]').filter({ hasText: new RegExp(`^${namespace}$`) }).click();
}

async function selectNamespaceAndRandomRow(page: Page, locators: OptimizeTabLocator): Promise<Locator> {
  const namespace = process.env.KUBECTL_NAMESPACE;
  if (!namespace) throw new Error('KUBECTL_NAMESPACE env variable is not set');
  await applyNamespaceFilter(page, locators, namespace);

  const dataRowLocator = page
    .locator('tr.MuiTableRow-root:not(.MuiTableRow-head)')
    .filter({ has: page.locator('button[aria-label="Expand row"], img[alt="arrow"]') });
  await dataRowLocator.first().waitFor({ state: 'visible', timeout: 30000 });
  const allRows = await dataRowLocator.all();
  const visibleRows: Locator[] = [];
  for (const row of allRows) {
    if (await row.isVisible()) visibleRows.push(row);
  }
  if (visibleRows.length === 0) throw new Error('No recommendation rows found after namespace filter');
  return visibleRows[0];
}


test("Graphql testing Cluster Details->Optimize-> Right Sizing", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.gotoOptimizeSection(OptimizeSections.rightSizing);
    },
    { testName: testInfo.title, operationNames: [] }
  );
});

test("Optimize-> Right Sizing Recommendation Dropdown", async ({ page }, testInfo) => {
  test.setTimeout(180000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();
  await locators.gotoOptimizeSection(OptimizeSections.rightSizing);
  await expect(locators.namespacedropdown).toBeVisible({ timeout: 30000 });
  const selectedRow = await selectNamespaceAndRandomRow(page, locators);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await selectedRow.locator('button[aria-label="Expand row"], img[alt="arrow"]').first().click();
    },
    { testName: testInfo.title, operationNames: [] }
  );
});

test("Optimize-> Right Sizing Recommendation Dropdown -> Resolution", async ({ page }, testInfo) => {
  test.setTimeout(180000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();
  await locators.gotoOptimizeSection(OptimizeSections.rightSizing);
  await expect(locators.namespacedropdown).toBeVisible({ timeout: 30000 });
  const selectedRow = await selectNamespaceAndRandomRow(page, locators);
  await selectedRow.locator('button[aria-label="Expand row"], img[alt="arrow"]').first().click();

  const resolutionsTab = page.getByRole('tab', { name: 'Resolutions' });
  await expect(resolutionsTab).toBeVisible();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await resolutionsTab.click();
    },
    { testName: testInfo.title, operationNames: [] }
  );
});

test("Download CSV from Right Sizing tab", async ({ page }) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();
  await locators.gotoOptimizeSection(OptimizeSections.rightSizing);

  await expect(locators.DownlaodBtn).toBeVisible({ timeout: 30000 });
  await locators.DownlaodBtn.click();

  await expect(locators.DownloadCSVBtn).toBeVisible();
  await locators.DownloadCSVBtn.click();
  await expect(locators.DownloadCSVSuccessMaggage).toBeVisible();
});

test("Download Excel from Right Sizing tab", async ({ page }) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);

  await loginPage.doFullLogin();
  await locators.navigateToClusterDetails();
  await locators.gotoOptimizeSection(OptimizeSections.rightSizing);

  await expect(locators.DownlaodBtn).toBeVisible({ timeout: 30000 });
  await locators.DownlaodBtn.click();

  await expect(locators.DownloadExcelBtn).toBeVisible();
  await locators.DownloadExcelBtn.click();
  await expect(locators.DownloadExcelSuccessMaggage).toBeVisible();
});
