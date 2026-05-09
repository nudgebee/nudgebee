import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { NubiLocators } from "./nubiLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

function generateRandomAgentName(): string {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";
  let result = "";
  for (let i = 0; i < 6; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return result;
}

test("Create Custom Tool for Container", async ({ page }) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new NubiLocators(page);

  const randomSuffix = generateRandomAgentName();
  const dynamicToolName = `Tool_${randomSuffix}`;

  console.log(`Creating Tool with Name: ${dynamicToolName}`);

  await loginPage.doFullLogin();

  await expect(locators.askNudgebeeBtn).toBeVisible();
  await locators.askNudgebeeBtn.click();

  await expect(locators.settingsBtn).toBeVisible();
  await locators.settingsBtn.click();
  console.log("Navigated to Settings");

  await expect(locators.ToolButton).toBeVisible();
  await locators.ToolButton.click();

  await expect(locators.CreateToolButton).toBeVisible();
  await locators.CreateToolButton.click();

  await locators.ToolName.fill(dynamicToolName);
  await locators.ToolDescription.fill(
    "Container-- This is a test tool created for Automation testing only."
  );

  await locators.ContainerImage.fill("docker run hello-world");

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.SubmitButton.click();
      await expect(
        locators.toolCreatedMessage.or(locators.toolCreationFailureMessage)
      ).toBeVisible({ timeout: 10000 });
    },
    {
      testName: "Create Custom Tool for Container",
      operationNames: "AiCreateTool",
      timeoutMs: 30000,
    }
  );

  console.log(`Created Tool with Name: ${dynamicToolName}`);

  if (await locators.toolCreationFailureMessage.isVisible()) {
    throw new Error(
      `Test Failed: Tool name '${dynamicToolName}' already exists.`
    );
  }

  console.log("Assertion Passed: Success message displayed.");

  await expect(locators.SubmitButton).not.toBeVisible({ timeout: 15000 });

  await locators.searchToolInput.click();
  await locators.searchToolInput.fill(dynamicToolName);
  await expect(
    page.getByText("Container-- This is a test tool created for Automation testing only.")
  ).toBeVisible({ timeout: 20000 });

  console.log(`Verified Tool '${dynamicToolName}' is present in the list.`);
});
