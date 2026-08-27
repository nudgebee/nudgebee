// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { TroubleshootTabs } from "../TroubleshootLocators";
import { KnowledgeGraphLocators } from "./knowledgeGraphLocators";

// Base URL is what TroubleshootLocators builds its fallback navigation from, so a missing
// value silently turns every goto into a relative no-op instead of failing.
export function requireBaseUrl(): string {
  const baseUrl = process.env.BASE_URL;
  if (!baseUrl) throw new Error("BASE_URL is not set — add it to .env / .env.dev");
  return baseUrl;
}

// Opens Troubleshoot > Knowledge Graph and waits until the graph fetch has settled into one
// of its four terminal canvas states. Reuses the shared login and the Troubleshoot tab strip
// helpers rather than re-driving either.
export async function openKnowledgeGraph(page: Page): Promise<KnowledgeGraphLocators> {
  requireBaseUrl();

  const locators = new KnowledgeGraphLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.navigateToTroubleshoot();
  await locators.gotoTab(TroubleshootTabs.knowledgeGraph);

  await expect(locators.filterPanel).toBeVisible({ timeout: 60000 });
  await waitForCanvasSettled(locators);

  return locators;
}

// The canvas renders a loader until the fetch resolves, then exactly one of: the ReactFlow
// surface, the tenant-empty panel, the filtered-empty panel, or the node-limit notice.
// Waiting on the alternation is the only honest "the graph finished" signal the module gives.
export async function waitForCanvasSettled(locators: KnowledgeGraphLocators, timeout = 90000): Promise<void> {
  await expect(locators.canvasSettled).toBeVisible({ timeout });
}

// Opens a FilterDropdown and waits for that dropdown's own panel.
//
// The cursor is parked first: hovering the Troubleshoot tab strip mounts AnchorComponent's
// popover over the sidebar, which can swallow the click.
export async function openFilterDropdown(page: Page, trigger: Locator, panel: Locator): Promise<void> {
  await page.mouse.move(0, 0);
  await trigger.click();
  await expect(panel).toBeVisible({ timeout: 20000 });
}

export async function closeFilterDropdown(page: Page, panel: Locator): Promise<void> {
  await page.keyboard.press("Escape");
  await expect(panel).toBeHidden({ timeout: 20000 });
}
