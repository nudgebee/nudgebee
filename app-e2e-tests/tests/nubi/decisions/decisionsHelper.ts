// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { DecisionsLocators } from "./decisionsLocators";
import { CONTROL_TIMEOUT, LOAD_TIMEOUT, MOUNT_TIMEOUT } from "./decisionsConstants";

// The two labels the history button carries, in the order it toggles through them.
export const HISTORY_OFF_LABEL = "Show all history";
export const HISTORY_ON_LABEL = "Show current only";

// DecisionsTab renders no spinner: while the fetch is in flight it renders the row
// card with no rows in it, which is indistinguishable from a genuinely empty list.
// In Personal scope readOnly is always false, so every row carries an Edit button —
// "an Edit button or the empty state" is the only end-of-load signal there is, and
// it is why every Personal-scope case below reaches the list through this.
export async function waitForPersonalDecisionsLoaded(locators: DecisionsLocators): Promise<void> {
  await expect(locators.editBtns.first().or(locators.emptyState)).toBeVisible({ timeout: LOAD_TIMEOUT });
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Memory > Decisions. Memory
// is b-Cortex's default landing group (BCortexModal.jsx DEFAULT_TAB_STATE), so
// Decisions is a real, clickable tab as soon as the modal opens — no separate
// top-level-group click first. The modal is opened through NubiLocators.openBCortex
// rather than a bare rail click: that helper retries the click and the tab wait as a
// pair, which is what keeps the open from failing on CI while passing locally.
export async function openDecisionsTab(page: Page): Promise<DecisionsLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new DecisionsLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openBCortex(locators.decisionsTab);
  await locators.decisionsTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the tab,
  // which would otherwise surface as every control below being absent. Raced rather
  // than asserted straight away: checking for the placeholder on its own right after
  // the click passes against the not-yet-rendered tab and proves nothing, so wait
  // until whichever of the two rendered, then name which it was.
  await expect(locators.personalHeader.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: LOAD_TIMEOUT });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForPersonalDecisionsLoaded(locators);
  return locators;
}

// Switches the Memory scope and waits until the toggle has committed the new value.
// aria-checked is what makes this a real barrier rather than a race: ds/ToggleGroup
// derives it from the same `scope` state DecisionsTab's header and its readOnly
// resolution read, so React commits the checked option and the re-scoped tab in one
// pass. Callers that go on to read the list still wait for it themselves.
export async function selectScope(locators: DecisionsLocators, label: string): Promise<void> {
  await locators.scopeOption(label).click();
  await expect(locators.scopeOption(label)).toHaveAttribute("aria-checked", "true", { timeout: CONTROL_TIMEOUT });
}

// Clicks the history toggle and waits for it to relabel. The button's label IS the
// state it holds (showSuperseded), so the relabel is the commit signal; the list
// refetch it triggers is waited for separately by the caller, since only Personal
// scope renders the button at all.
export async function toggleHistory(locators: DecisionsLocators, expectedLabel: string): Promise<void> {
  await locators.historyToggleBtn.click();
  await expect(locators.historyToggleBtn).toHaveText(expectedLabel, { timeout: CONTROL_TIMEOUT });
}

// Leaves the tab for Patterns and comes back, which unmounts DecisionsTabContent and
// makes it refetch — the only way to tell reloaded state from state still held in
// React. The unmount is waited on via the tab's own description rather than a header:
// the header text names the current scope, so a case that switched to Global would
// find the personal header already detached and never wait for anything.
export async function remountDecisionsTab(locators: DecisionsLocators): Promise<void> {
  await locators.patternsTab.click();
  await locators.immutabilityDescription.waitFor({ state: "detached", timeout: MOUNT_TIMEOUT });
  await locators.decisionsTab.click();
  await locators.immutabilityDescription.waitFor({ state: "visible", timeout: LOAD_TIMEOUT });
  await waitForPersonalDecisionsLoaded(locators);
}
