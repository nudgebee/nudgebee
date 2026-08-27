import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { NubiLocators } from "./nubiLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

function generateRandomAgentName(): string {
  return `Agent_${Math.random().toString(36).slice(2, 8).toUpperCase()}`;
}

test("CRUD Custom Agent", { tag: ["@dev", "@test", "@smoke", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);
  const loginPage = new LoginPage(page);
  const locators = new NubiLocators(page);
  const agentName = generateRandomAgentName();
  console.log(`Creating Agent with Name: ${agentName}`);

  await loginPage.doFullLogin();
  await locators.askNudgebeeBtn.click();
  // SettingsModal builds its tab strip from an async hasFeatureAccess('LLM_FUNCTION')
  // round trip, and a Settings click that lands while the nubi panel is still animating
  // in opens nothing at all — leaving the panel on screen with no tabs to click. Retry
  // the pair until the tabs are actually there.
  await expect(async () => {
    if (!(await locators.customAgentTab.isVisible().catch(() => false))) {
      await locators.settingsBtn.click();
    }
    await locators.customAgentTab.waitFor({ state: "visible", timeout: 5000 });
  }).toPass({ timeout: 60000, intervals: [1000, 2000, 3000] });
  console.log("Navigated to Settings");

  await locators.customAgentTab.click();
  await locators.createCustomAgentBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.createCustomAgentBtn.click();
  // The first field is the readiness signal: every card on this form is rendered
  // and expanded from the start, so there is no step to open first — the left
  // rail only scrolls, and each fill() scrolls its own field into view anyway.
  await locators.agentNameInput.waitFor({ state: "visible", timeout: 30000 });

  await locators.agentNameInput.fill(agentName);
  await locators.agentDescriptionInput.fill("Test agent created by automation.");

  await locators.agenRole.fill("You are a helpful assistant.");
  await locators.agentInstructionsInput.fill("Testing Only");

  await locators.selectAgentOrTool.click();
  await locators.listOfAgentsOrTools.waitFor({ state: "visible", timeout: 15000 });
  await locators.listOfAgentsOrTools.click({ timeout: 15000 });
  // The picker is a multiple Select — it stays open after a pick, and its backdrop
  // would swallow the next click.
  await locators.closeSelectPopover();
  await locators.agentToolUsage.fill("Used for automated testing.");

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

  // ── Read ──────────────────────────────────────────────────────────────────
  await locators.searchAgentInput.fill(agentName);
  await expect(page.getByText(agentName)).toBeVisible({ timeout: 10000 });
  console.log(`[Read] Agent '${agentName}' verified in list.`);

  // ── Update ────────────────────────────────────────────────────────────────
  await locators.agentMoreActionsBtn.click();
  await locators.editAgentMenuItem.waitFor({ state: "visible", timeout: 10000 });
  await locators.editAgentMenuItem.click();
  await locators.agentDescriptionInput.waitFor({ state: "visible", timeout: 30000 });
  await locators.agentDescriptionInput.clear();
  await locators.agentDescriptionInput.fill("Updated description by automation.");
  console.log(`[Update] Filled updated description.`);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.updateAgentBtn.click();
      await expect(locators.updateAgentSuccessMessage).toBeVisible({ timeout: 15000 });
    },
    {
      testName: "Update Custom Agent",
      operationNames: "AiUpdateAgent",
      timeoutMs: 30000,
    }
  );
  console.log(`[Update] Agent '${agentName}' updated successfully.`);

  // ── Delete ────────────────────────────────────────────────────────────────
  await locators.searchAgentInput.clear();
  await locators.searchAgentInput.fill(agentName);
  await expect(page.getByText(agentName)).toBeVisible({ timeout: 10000 });

  await locators.agentMoreActionsBtn.click();
  await locators.deleteAgentMenuItem.waitFor({ state: "visible", timeout: 10000 });
  await locators.deleteAgentMenuItem.click();
  await locators.confirmDeleteAgentBtn.waitFor({ state: "visible", timeout: 10000 });

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.confirmDeleteAgentBtn.click();
      await expect(locators.deleteAgentSuccessMessage).toBeVisible({ timeout: 15000 });
    },
    {
      testName: "Delete Custom Agent",
      operationNames: "AiDeleteAgent",
      timeoutMs: 30000,
    }
  );
  console.log(`[Delete] Agent '${agentName}' deleted successfully.`);

  // Verify agent is no longer in the list
  await locators.searchAgentInput.clear();
  await locators.searchAgentInput.fill(agentName);
  await expect(page.getByText(agentName)).not.toBeVisible({ timeout: 10000 });
  console.log(`[Delete] Verified Agent '${agentName}' is removed from the list.`);
});
