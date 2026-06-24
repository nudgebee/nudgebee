import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { NubiLocators } from "./nubiLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

function generateRandomToolName(): string {
  return `Tool_${Math.random().toString(36).slice(2, 8).toUpperCase()}`;
}

const TOOL_DESCRIPTION = "Container-- This is a test tool created for Automation testing only.";
const TOOL_DESCRIPTION_UPDATED = "Container-- Updated description by automation.";
const CONTAINER_IMAGE = "alpine:latest";
const CONTAINER_COMMAND = "/bin/sh";
const CONTAINER_ARGS = '-c "echo hello-world"';

test("CRUD Custom Tool for Container", async ({ page }) => {
  test.setTimeout(180000);

  const loginPage = new LoginPage(page);
  const locators = new NubiLocators(page);
  const dynamicToolName = generateRandomToolName();

  console.log(`Creating Tool with Name: ${dynamicToolName}`);

  await loginPage.doFullLogin();
  await locators.askNudgebeeBtn.click();
  await locators.settingsBtn.click();
  console.log("Navigated to Settings");

  await locators.ToolButton.waitFor({ state: "visible", timeout: 15000 });
  await locators.ToolButton.click();
  console.log("Clicked Tools tab");

  // ── Create ──────────────────────────────────────────────────────────────
  await locators.CreateToolButton.waitFor({ state: "visible", timeout: 15000 });
  await locators.CreateToolButton.click();

  await locators.ToolName.waitFor({ state: "visible", timeout: 10000 });
  await locators.ToolName.fill(dynamicToolName);
  await locators.ToolDescription.fill(TOOL_DESCRIPTION);
  await locators.ContainerImage.fill(CONTAINER_IMAGE);
  await locators.ContainerCommand.fill(CONTAINER_COMMAND);
  await locators.ContainerArguments.fill(CONTAINER_ARGS);

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

  if (await locators.toolCreationFailureMessage.isVisible()) {
    throw new Error(`[Create] Tool '${dynamicToolName}' creation failed.`);
  }

  await expect(locators.SubmitButton).not.toBeVisible({ timeout: 15000 });
  console.log(`[Create] Tool '${dynamicToolName}' created successfully.`);

  // ── Read ──────────────────────────────────────────────────────────────────
  await locators.searchToolInput.click();
  await locators.searchToolInput.fill(dynamicToolName);
  await expect(page.getByText(TOOL_DESCRIPTION)).toBeVisible({ timeout: 20000 });
  console.log(`[Read] Tool '${dynamicToolName}' verified in list.`);

  // ── Update ────────────────────────────────────────────────────────────────
  await locators.editToolBtn.click();
  await locators.ToolDescription.waitFor({ state: "visible", timeout: 10000 });
  await locators.ToolDescription.clear();
  await locators.ToolDescription.fill(TOOL_DESCRIPTION_UPDATED);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.updateToolBtn.click();
      await expect(locators.updateToolSuccessMessage).toBeVisible({ timeout: 15000 });
    },
    {
      testName: "Update Custom Tool for Container",
      operationNames: "AiUpdateTool",
      timeoutMs: 30000,
    }
  );
  console.log(`[Update] Tool '${dynamicToolName}' updated successfully.`);

  // ── Disable (UI has no delete; disable the tool via Status field in edit form) ──
  await locators.searchToolInput.clear();
  await locators.searchToolInput.fill(dynamicToolName);
  await expect(page.getByText(TOOL_DESCRIPTION_UPDATED)).toBeVisible({ timeout: 10000 });

  await locators.editToolBtn.click();
  await locators.toolStatusSelect.waitFor({ state: "visible", timeout: 10000 });

  // Clear existing status and pick Disabled
  await locators.toolStatusSelect.click();
  await locators.toolStatusDisabledOption.click({ timeout: 5000 });

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.updateToolBtn.click();
      await expect(locators.toolDisabledSuccessMessage).toBeVisible({ timeout: 15000 });
    },
    {
      testName: "Disable Custom Tool for Container",
      operationNames: "AiUpdateTool",
      timeoutMs: 30000,
    }
  );
  console.log(`[Disable] Tool '${dynamicToolName}' disabled successfully.`);

  // Verify status shows Disabled in the list
  await locators.searchToolInput.clear();
  await locators.searchToolInput.fill(dynamicToolName);
  await expect(page.getByText('Disabled')).toBeVisible({ timeout: 10000 });
  console.log(`[Disable] Verified Tool '${dynamicToolName}' status is Disabled.`);
});
