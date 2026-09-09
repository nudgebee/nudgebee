// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { TroubleshootTabs } from "./TroubleshootLocators";
import {
  TroubleshootEventsLocators,
  EventSubTab,
  EventSubTabs,
  InvestigationSubTab,
} from "./troubleshootEventsLocators";

// Fail fast and name the key, rather than letting a blank BASE_URL turn every
// navigation into a goto("") that reports as an unrelated timeout.
export function requiredEnv(key: string): string {
  const value = process.env[key] || "";
  if (!value) {
    throw new Error(`${key} is not set — add it to .env / .env.dev`);
  }
  return value;
}

// CLUSTER is consumed by LoginPage.selectConfiguredCluster during doFullLogin, so
// its absence otherwise surfaces as a cluster-dropdown timeout deep inside login.
export function assertTroubleshootEnv(): void {
  requiredEnv("BASE_URL");
  requiredEnv("CLUSTER");
}

// The page paints this loader while the shell boots. Absence is the expected
// outcome — on a warm navigation it is already gone before this runs — so the
// wait must come back as a no-op rather than throw.
async function settle(page: Page): Promise<void> {
  // A loader that was never there is the expected outcome on a warm navigation, so
  // this probe swallows the miss rather than failing the navigation it guards.
  await page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 60000 }).catch(() => undefined);
}

// Lands on /troubleshoot the way a person does — sign in, then the sidebar button.
// doFullLogin already seeds the tour-suppression preferences, so no tour dialog
// mounts over the tab strip.
export async function openTroubleshoot(page: Page): Promise<TroubleshootEventsLocators> {
  assertTroubleshootEnv();
  const locators = new TroubleshootEventsLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.navigateToTroubleshoot();
  await expect(locators.eventTabsBox).toBeVisible({ timeout: 60000 });
  return locators;
}

// Opens /troubleshoot at an arbitrary hash. Used for the deep-link cases, where
// the hash itself — not the click that would normally produce it — is under test.
export async function deepLinkTroubleshoot(page: Page, hash: string): Promise<TroubleshootEventsLocators> {
  assertTroubleshootEnv();
  const locators = new TroubleshootEventsLocators(page);
  await new LoginPage(page).doFullLogin();
  await page.goto(`/troubleshoot#${hash}`);
  await settle(page);
  return locators;
}

// MUI Tab marks the open tab with aria-selected. That is the app's own record of
// which sub-tab is rendered and, unlike the URL, it is never mid-rewrite.
export async function expectSelectedSubTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("aria-selected", "true", { timeout: 20000 });
}

// Clicks an All Events sub-tab and waits until the app agrees it is open.
export async function openEventSubTab(locators: TroubleshootEventsLocators, tab: EventSubTab): Promise<void> {
  const target = locators.eventSubTab(tab);
  await expect(target).toBeVisible({ timeout: 30000 });

  // Triage Inbox is sub-tab 0, which a bare /troubleshoot already opens — clicking
  // it would make the "it is the default" assertion observe its own click.
  if (tab !== EventSubTabs.triageInbox) {
    await target.click();
    // Park the cursor in open content: left on the strip, AnchorComponent opens a
    // hover popover over the toolbar below and the next click lands on that.
    await locators.parkCursor();
  }

  await expectSelectedSubTab(target);
}

// Opens the Investigations parent tab, then one of its two sub-tabs.
export async function openInvestigationSubTab(
  locators: TroubleshootEventsLocators,
  tab: InvestigationSubTab
): Promise<void> {
  await locators.gotoTab(TroubleshootTabs.investigations);
  const target = locators.investigationSubTab(tab);
  await expect(target).toBeVisible({ timeout: 30000 });
  await target.click();
  await locators.parkCursor();
  await expectSelectedSubTab(target);
}
