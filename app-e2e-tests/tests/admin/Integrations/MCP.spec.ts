import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { IntegrationLocators } from "./IntegrationLocators";
import { submitWithDuplicateHandling } from "./util";
import "dotenv/config";

const clusterName = process.env.CLUSTER ?? process.env.CLUSTER_NAME ?? "iteration-test";

const requiredEnv = ["MCP_INTEGRATION_CONFIG_NAME", "MCP_URL"];
for (const key of requiredEnv) {
  if (process.env[key] === undefined || process.env[key] === null) {
    throw new Error(`Missing required env variable: ${key}`);
  }
}

test("Add MCP Account Integration", async ({ page }) => {
  const loginPage = new LoginPage(page);
  const locators = new IntegrationLocators(page);

  await loginPage.doFullLogin();
  await locators.adminBtn.waitFor({ state: "visible" });
  await locators.adminBtn.click();
  await locators.integrationsTab.click();

  // Open the search box and filter for MCP
  await page.locator("#search-toggle-button").click();
  const searchInput = page.locator("#search-input-text");
  await expect(searchInput).toBeVisible({ timeout: 5000 });
  await searchInput.fill("mcp");

  await locators.mcpBtn.click();
  await locators.addMcpAccountBtn.click();

  // Fill the MCP integration form
  await locators.mcpConfigNameInput.fill(
    process.env.MCP_INTEGRATION_CONFIG_NAME!,
  );

  // Select account (cluster)
  await locators.mcpAccountIdDropdown.click();
  await locators.mcpAccountIdOption(clusterName).first().click();
  await locators.mcpAccountIdDropdown.press("Escape");

  // Fill URL (transport defaults to http)
  await locators.mcpUrlInput.fill(process.env.MCP_URL!);

  // Fill LLM instructions if provided
  if (process.env.MCP_LLM_INSTRUCTIONS) {
    await locators.mcpLlmInstructionsInput.fill(
      process.env.MCP_LLM_INSTRUCTIONS,
    );
  }

  await submitWithDuplicateHandling(page, {
    jiraSaveButton: locators.saveBtn,
    successToast: locators.mcpSuccessToast,
    duplicateErrorToast: locators.mcpDuplicateErrorToast,
    testName: "Add MCP Account Integration",
    operationNames: ["AddIntegrations"],
    onSuccess: async () => {
      await expect(
        locators.getIntegrationByName(process.env.MCP_INTEGRATION_CONFIG_NAME!),
      ).toBeVisible();
    },
  });
});
