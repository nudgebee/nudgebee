import { test, expect, Page } from "@playwright/test";
import {
  navigateToIntegrationsPage,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
} from "./util";
import { IntegrationLocators } from "./IntegrationLocators";

const clusterName = process.env.CLUSTER ?? process.env.CLUSTER_NAME ?? "";

const requiredEnv = ["MCP_INTEGRATION_CONFIG_NAME", "MCP_URL"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.MCP_INTEGRATION_CONFIG_NAME!;

// MCP's section card is only revealed after searching for it, so opening its
// list page needs the search step before the card click.
async function openMcpList(page: Page, locators: IntegrationLocators): Promise<void> {
  const searchToggle = page.locator("#search-toggle-button");
  const isToggleVisible = await searchToggle.isVisible({ timeout: 3000 }).catch(() => false);
  if (isToggleVisible) {
    await searchToggle.click();
  }

  const searchInput = page
    .locator("#search-input-text")
    .or(page.getByPlaceholder("Search integrations..."))
    .first();
  await expect(searchInput).toBeVisible({ timeout: 10000 });
  await searchInput.fill("mcp");

  await locators.mcpBtn.waitFor({ state: "visible", timeout: 10000 });
  await locators.mcpBtn.click();
}

test.describe.serial("MCP Account Integration", () => {
  let integrationExists = false;

  test("Check if MCP integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToIntegrationsPage(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: () => openMcpList(page, locators),
      configName,
      serviceName: "MCP",
      statusFilterId: "mcp-status-filter",
    });
  });

  test("Delete MCP integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "MCP integration not present — nothing to delete");
    const locators = await navigateToIntegrationsPage(page);
    await deleteIntegration(page, {
      openList: () => openMcpList(page, locators),
      configName,
      serviceName: "MCP",
      statusFilterId: "mcp-status-filter",
    });
  });

  test("Add MCP Account Integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToIntegrationsPage(page);

    await openMcpList(page, locators);

    await locators.addMcpAccountBtn.waitFor({ state: "visible", timeout: 10000 });
    await locators.addMcpAccountBtn.click();

    await locators.mcpConfigNameInput.waitFor({ state: "visible", timeout: 10000 });
    await locators.mcpConfigNameInput.fill(configName);

    await locators.mcpAccountIdDropdown.waitFor({ state: "visible", timeout: 10000 });
    await locators.mcpAccountIdDropdown.click();
    const clusterOption = locators.mcpAccountIdOption(clusterName).first();
    const isOptionVisible = await clusterOption.isVisible({ timeout: 5000 }).catch(() => false);
    if (!isOptionVisible) {
      console.log(`Cluster option '${clusterName}' not visible yet — retrying dropdown click`);
      await locators.mcpAccountIdDropdown.click();
    }
    await clusterOption.waitFor({ state: "visible", timeout: 10000 });
    await clusterOption.click();
    await locators.mcpAccountIdDropdown.press("Escape");

    await locators.mcpUrlInput.waitFor({ state: "visible", timeout: 10000 });
    await locators.mcpUrlInput.fill(process.env.MCP_URL!);

    if (process.env.MCP_LLM_INSTRUCTIONS) {
      await locators.mcpLlmInstructionsInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.mcpLlmInstructionsInput.fill(process.env.MCP_LLM_INSTRUCTIONS);
    }

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.mcpSuccessToast,
      testName: "Add MCP Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: [`integration config name '${configName}' already exists`],
      onSuccess: async () => {
        await expect(locators.getIntegrationByName(configName)).toBeVisible();
      },
    });
  });

  test("Disable MCP integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToIntegrationsPage(page);
    await openMcpList(page, locators);
    await disableIntegration(page, { configName, serviceName: "MCP" });
  });

  test("Enable MCP integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToIntegrationsPage(page);
    await openMcpList(page, locators);
    await enableIntegration(page, {
      configName,
      serviceName: "MCP",
      statusFilterId: "mcp-status-filter",
    });
  });
});
