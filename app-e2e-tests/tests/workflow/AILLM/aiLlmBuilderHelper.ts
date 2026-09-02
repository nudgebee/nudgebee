// Not for OSS
import { Page, expect } from "@playwright/test";
import { AiLlmBuilderLocators } from "./aiLlmBuilderLocators";
import { generateWorkflowName } from "../workflowHelper";

// generateWorkflowName only appends a 2-digit random suffix, so two runs of the same case on a
// shared dev tenant can collide and the listing search would then match a stranger's automation.
export function uniqueAiLlmWorkflowName(caseName: string): string {
  return `${generateWorkflowName(`E2E AI LLM ${caseName}`)} ${Date.now()}`;
}

export function requireCluster(): string {
  const cluster = process.env.CLUSTER || "";
  if (!cluster) throw new Error("CLUSTER is not set — add it to .env / .env.dev");
  return cluster;
}

export async function openNodePalette(page: Page, locators: AiLlmBuilderLocators): Promise<void> {
  // The add-edge button only renders on hover of the node it hangs off, so hover before clicking.
  const firstNode = locators.canvasNodes.first();
  await firstNode.waitFor({ state: "visible", timeout: 30000 });
  await firstNode.hover();
  await locators.addNodeFromTriggerBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.addNodeFromTriggerBtn.click();
  await expect(locators.nodePaletteHeading).toBeVisible({ timeout: 15000 });
}

export async function expandLlmCategory(locators: AiLlmBuilderLocators): Promise<void> {
  await locators.llmCategoryAccordion.waitFor({ state: "visible", timeout: 15000 });
  const expanded = await locators.llmCategoryAccordion.getAttribute("class");
  // The accordion carries Mui-expanded once open; clicking an already-open category would collapse it.
  if (!expanded?.includes("Mui-expanded")) {
    await locators.llmCategoryAccordion.click();
  }
  await expect(locators.llmActionButtons.first()).toBeVisible({ timeout: 15000 });
}

export async function reopenWorkflowFromListing(
  page: Page,
  locators: AiLlmBuilderLocators,
  workflowName: string
): Promise<void> {
  await locators.backBtn.click();
  const leavePageBtn = page.getByRole("button", { name: "Leave page" });
  // An unsaved-changes prompt only appears when the builder holds edits, so its absence is the normal case here.
  await leavePageBtn.click({ timeout: 3000 }).catch(() => {});

  await locators.nameSearchInput.waitFor({ state: "visible", timeout: 20000 });
  await locators.nameSearchInput.fill(workflowName);
  await locators.nameSearchInput.press("Enter");

  const row = page.getByText(workflowName, { exact: true }).first();
  await expect(row).toBeVisible({ timeout: 20000 });
  await row.click();
  await page.waitForURL(/\/automation\/[0-9a-fA-F-]{36}/, { timeout: 30000 });
}
