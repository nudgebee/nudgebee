import { test, expect, Page, Locator } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { OptimizeTabLocator } from "../OptimizeTabLocator";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";
import { createTicketFromButton, TicketToolConfig, TICKET_TOOLS } from "../../../utils/createTicketHelper";

async function navigateToOptimize(locators: OptimizeTabLocator): Promise<void> {
  await locators.openClusterFromConfig();
  await expect(locators.OptimizeTab).toBeVisible();
  await locators.OptimizeTab.click();
  await expect(locators.OptimizedropdownRightSizeButton).toBeVisible();
}

async function navigateToRightSizing(locators: OptimizeTabLocator): Promise<void> {
  await navigateToOptimize(locators);
  await locators.OptimizedropdownRightSizeButton.click();
}

async function applyNamespaceFilter(page: Page, locators: OptimizeTabLocator, namespace: string): Promise<void> {
  await expect(locators.namespacedropdown).toBeEnabled({ timeout: 30000 });
  await locators.namespacedropdown.click();
  await page.locator('input[placeholder="Search..."]').fill(namespace);
  await page.locator('[role="option"]').filter({ hasText: new RegExp(`^${namespace}$`) }).click();
}

async function selectNamespaceAndRandomRow(page: Page, locators: OptimizeTabLocator): Promise<Locator> {
  await applyNamespaceFilter(page, locators, process.env.KUBECTL_NAMESPACE!);

  const dataRowLocator = page
    .locator('tr.MuiTableRow-root:not(.MuiTableRow-head)')
    .filter({ has: page.locator('img[alt="arrow"]') });
  await dataRowLocator.first().waitFor({ state: 'visible' });
  const allRows = await dataRowLocator.all();
  const visibleRows: Locator[] = [];
  for (const row of allRows) {
    if (await row.isVisible()) visibleRows.push(row);
  }
  if (visibleRows.length === 0) throw new Error('No recommendation rows found after namespace filter');
  return visibleRows[0];
}

async function scanRowsForEnabledCreateTicket(page: Page, maxRows = 10): Promise<Locator | null> {
  const resolveModal = page.locator('[role="dialog"]').filter({ hasText: 'Resolve this issue' });
  const optimizeButtons = page.locator('table td button').filter({ hasText: 'Optimize' });
  const count = await optimizeButtons.count();

  for (let i = 0; i < Math.min(count, maxRows); i++) {
    const btn = optimizeButtons.nth(i);
    if (!(await btn.isVisible())) continue;

    await btn.click();
    const appeared = await resolveModal.waitFor({ state: "visible", timeout: 10000 }).then(() => true).catch(() => false);
    if (!appeared) continue;

    const createTicketBtn = resolveModal.getByRole('button', { name: 'Create Ticket' });
    const isEnabled = await createTicketBtn.isEnabled().catch(() => false);
    if (isEnabled) return createTicketBtn;

    await page.keyboard.press('Escape');
    await resolveModal.waitFor({ state: "hidden", timeout: 3000 }).catch(() => {});
  }
  return null;
}

async function findEnabledCreateTicketButton(page: Page, locators: OptimizeTabLocator): Promise<Locator | null> {
  const result = await scanRowsForEnabledCreateTicket(page);
  if (result) return result;

  const namespace = process.env.KUBECTL_NAMESPACE;
  if (!namespace) return null;

  await applyNamespaceFilter(page, locators, namespace);
  await page.waitForTimeout(1000);

  return scanRowsForEnabledCreateTicket(page);
}

test("Graphql testing Cluster Details->Optimize-> Right Sizing", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await navigateToOptimize(locators);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.OptimizedropdownRightSizeButton.click();
    },
    { testName: testInfo.title, operationNames: [] }
  );
});

test("Optimize-> Right Sizing Recommendation Dropdown", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await navigateToRightSizing(locators);
  const selectedRow = await selectNamespaceAndRandomRow(page, locators);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await selectedRow.locator('img[alt="arrow"]').click();
    },
    { testName: testInfo.title, operationNames: [] }
  );
});

test("Optimize-> Right Sizing Recommendation Dropdown -> Resolution", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);
  await loginPage.doFullLogin();
  await navigateToRightSizing(locators);
  const selectedRow = await selectNamespaceAndRandomRow(page, locators);
  await selectedRow.locator('img[alt="arrow"]').click();

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

for (const tool of TICKET_TOOLS) {
  test(`Optimize-> Right Sizing -> Create Ticket - ${tool.label}`, async ({ page }, testInfo) => {
    test.setTimeout(180000);

    const configName = process.env[tool.configEnv];
    if (!configName) {
      test.skip(true, `Skipped: env var ${tool.configEnv} is not set`);
      return;
    }

    const config: TicketToolConfig = {
      configName,
      projectName: tool.projectEnv ? process.env[tool.projectEnv] : undefined,
      issueType: tool.issueEnv ? process.env[tool.issueEnv] : undefined,
      toolLabel: tool.label,
    };

    const loginPage = new LoginPage(page);
    const locators = new OptimizeTabLocator(page);
    await loginPage.doFullLogin();
    await navigateToRightSizing(locators);

    const createTicketBtn = await findEnabledCreateTicketButton(page, locators);
    if (!createTicketBtn) {
      test.skip(true, 'All scanned rows already have tickets — no row available for ticket creation');
      return;
    }

    const result = await createTicketFromButton(page, createTicketBtn, config, testInfo.title);

    test.skip(
      result === "config_not_found",
      `Config "${configName}" not found — ${tool.label} integration may not be configured in this environment`
    );
  });
}
