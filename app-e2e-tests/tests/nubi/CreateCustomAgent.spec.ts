import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { NubiLocators } from "./nubiLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

function generateRandomAgentName(): string {
  return `Agent_${Math.random().toString(36).slice(2, 8).toUpperCase()}`;
}

test("Create Custom Agent", async ({ page }) => {
  test.setTimeout(120000);
  const loginPage = new LoginPage(page);
  const locators = new NubiLocators(page);
  const agentName = generateRandomAgentName();
  console.log(`Creating Agent with Name: ${agentName}`);

  await loginPage.doFullLogin();
  await locators.askNudgebeeBtn.click();
  await locators.settingsBtn.click();
  console.log("Navigated to Settings");

  await locators.customAgentTab.click();
  await locators.createCustomAgentBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.createCustomAgentBtn.click();
  await locators.ageentIdentityButton.waitFor({ state: "visible", timeout: 30000 });
  await locators.ageentIdentityButton.click();

  await locators.agentNameInput.fill(agentName);
  await locators.agentDescriptionInput.fill("Test agent created by automation.");

  await locators.agentSetAgentBehaviorAndGuidelines.click();
  await locators.agenRole.fill("You are a helpful assistant.");
  await locators.agentInstructionsInput.fill("Testing Only");

  await locators.ageentToolsOrAgentselectionButton.click();
  await locators.selectAgentOrTool.click();
  // Group headers render as 'TOOL' via CSS but DOM text is 'Tool' (snakeToTitleCase)
  const toolGroupBtn = page.getByText("Tool", { exact: true });
  await toolGroupBtn.waitFor({ state: "visible", timeout: 10000 });
  await toolGroupBtn.click();
  await locators.listOfAgentsOrTools.click({ timeout: 15000 });
  await page.keyboard.press("Escape");
  await locators.agentToolUsage.fill("Used for automated testing.");

  await locators.agentKnoowledgeAndExample.click();

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.submitCreateAgentBtn.click();
      await expect(locators.successMessage.or(locators.failureMessage)).toBeVisible({ timeout: 15000 });
    },
    {
      testName: "Create Custom Agent",
      operationNames: "AiCreateAgent",
      timeoutMs: 30000,
    }
  );

  if (await locators.failureMessage.isVisible()) {
    throw new Error(`Agent creation failed for '${agentName}': name may already exist.`);
  }
  console.log(`Created Agent: ${agentName}`);

  await locators.searchAgentInput.fill(agentName);
  await expect(page.getByText(agentName)).toBeVisible({ timeout: 10000 });
  console.log(`Verified Agent '${agentName}' is present in the list.`);
});
