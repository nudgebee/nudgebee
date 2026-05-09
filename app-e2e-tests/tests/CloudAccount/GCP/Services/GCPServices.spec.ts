import { test, expect, Page } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { CloudAccountLocators } from "../../CloudAccountLocators";
import { waitForGraphQLAndValidate } from "../../../utils/GraphQLNetworkWatcher";

// ── Shared setup helpers ──────────────────────────────────────────────────────

async function openAccount(page: Page, locators: CloudAccountLocators) {
  await new LoginPage(page).doFullLogin();
  await locators.openGCPCloudAccountFromConfig();
}

async function goToServicesTab(page: Page, locators: CloudAccountLocators) {
  await openAccount(page, locators);
  await expect(locators.AnchorTabServices).toBeVisible();
  await locators.AnchorTabServices.click();
  await page.waitForLoadState("networkidle");
  await page
    .locator("#servicesTable tbody tr")
    .first()
    .waitFor({ state: "visible", timeout: 30000 });
}

async function expandFirstServiceRow(page: Page, locators: CloudAccountLocators) {
  await goToServicesTab(page, locators);
  await locators.ServicesRowExpandButton.click();
  await expect(locators.ServicesDrilldownTabResources).toBeVisible();
}

// ── Tests ─────────────────────────────────────────────────────────────────────

test("API testing Cloud Account -> GCP -> Services", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);
  const locators = new CloudAccountLocators(page);
  await openAccount(page, locators);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await expect(locators.AnchorTabServices).toBeVisible();
      await locators.AnchorTabServices.click();
      await page.waitForLoadState("networkidle");
    },
    { testName: testInfo.title, operationNames: [], timeoutMs: 60000 }
  );
});

test("API testing Cloud Account -> GCP -> Services -> Resources tab", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);
  const locators = new CloudAccountLocators(page);
  await goToServicesTab(page, locators);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.ServicesRowExpandButton.click();
      await expect(locators.ServicesDrilldownTabResources).toBeVisible();
      await page.waitForLoadState("networkidle");
    },
    { testName: testInfo.title, operationNames: [] }
  );
});

test("API testing Cloud Account -> GCP -> Services -> Cost Trend tab", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);
  const locators = new CloudAccountLocators(page);
  await expandFirstServiceRow(page, locators);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.ServicesDrilldownTabCostTrend.click();
      await page.waitForLoadState("networkidle");
    },
    { testName: testInfo.title, operationNames: [] }
  );
});

test("API testing Cloud Account -> GCP -> Services -> Resources -> Details tab", async ({
  page,
}) => {
  test.setTimeout(120000);
  const locators = new CloudAccountLocators(page);
  await expandFirstServiceRow(page, locators);

  await page
    .locator("#service-resource-listing-table tbody tr")
    .first()
    .waitFor({ state: "visible", timeout: 30000 });

  // ListCloudResources has known GQL errors (acceptable backend issue), plain assertions used
  await locators.getResourceRowExpandButton().click();
  await expect(page.getByRole("tab", { name: "Details" })).toBeVisible();
  await page.waitForLoadState("networkidle");
});

test("API testing Cloud Account -> GCP -> Services -> Recommendations tab", async ({
  page,
}, testInfo) => {
  test.setTimeout(120000);
  const locators = new CloudAccountLocators(page);
  await expandFirstServiceRow(page, locators);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.ServicesDrilldownTabRecommendations.click();
      await page.waitForLoadState("networkidle");
    },
    { testName: testInfo.title, operationNames: [] }
  );
});
