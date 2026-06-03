import { test, expect } from "@playwright/test";
import { navigateToIntegrationsPage, submitWithDuplicateHandling } from "./util";

const clusterName = process.env.CLUSTER ?? process.env.CLUSTER_NAME ?? "";

const requiredEnv = ["MCP_INTEGRATION_CONFIG_NAME", "MCP_URL"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add MCP Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToIntegrationsPage(page);

  // Open the search box and filter for MCP
  await page.locator("#search-toggle-button").click();
  const searchInput = page.locator("#search-input-text");
  await expect(searchInput).toBeVisible({ timeout: 5000 });
  await searchInput.fill("mcp");

  await locators.mcpBtn.click();
  await locators.addMcpAccountBtn.click();

  await locators.mcpConfigNameInput.fill(process.env.MCP_INTEGRATION_CONFIG_NAME!);

  await locators.mcpAccountIdDropdown.click();
  await locators.mcpAccountIdOption(clusterName).first().click();
  await locators.mcpAccountIdDropdown.press("Escape");

  await locators.mcpUrlInput.fill(process.env.MCP_URL!);

  if (process.env.MCP_LLM_INSTRUCTIONS) {
    await locators.mcpLlmInstructionsInput.fill(process.env.MCP_LLM_INSTRUCTIONS);
  }

  await submitWithDuplicateHandling(page, {
    saveButton: locators.saveBtn,
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
