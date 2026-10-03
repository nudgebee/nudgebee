// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { SessionsLocators } from "./sessionsLocators";

// The three type labels the tab can filter by. SESSION_FILTERS in
// app/src/ee/components/memory2/mocks.js carries "All" as well, but "All" is the
// unfiltered view rather than a type a row can be tagged with.
export const SESSION_TYPES = ["General", "Investigation", "Automation"];

// SessionsTab renders no spinner: while the fetch is in flight it shows the list card
// with no rows in it, which is indistinguishable from a genuinely empty list. Waiting
// for either a row or the empty state is the only end-of-load signal there is, and it
// is why every case below reaches the list through this.
export async function waitForSessionsLoaded(locators: SessionsLocators): Promise<void> {
  await expect(locators.rowToggles.first().or(locators.emptyState)).toBeVisible({ timeout: 30000 });
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Memory > Sessions. Memory is
// b-Cortex's default landing group, so Sessions is a real, clickable tab as soon as
// the modal opens — no separate top-level-group click first.
export async function openSessionsTab(page: Page): Promise<SessionsLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new SessionsLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.bcortexBtn.click();
  await locators.sessionsTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.sessionsTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the tab,
  // which would otherwise surface as every control below being absent. Raced rather
  // than asserted straight away: checking for the placeholder on its own right after
  // the click passes against the not-yet-rendered tab and proves nothing, so wait
  // until whichever of the two rendered, then name which it was.
  await expect(locators.recentActivityHeader.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 30000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForSessionsLoaded(locators);
  return locators;
}

// Leaves the tab and comes back, which unmounts SessionsTabContent and makes it
// refetch — the only way to tell reloaded state from state still held in React.
export async function remountSessionsTab(locators: SessionsLocators): Promise<void> {
  await locators.patternsTab.click();
  await locators.recentActivityHeader.waitFor({ state: "detached", timeout: 20000 });
  await locators.sessionsTab.click();
  await locators.recentActivityHeader.waitFor({ state: "visible", timeout: 30000 });
  await waitForSessionsLoaded(locators);
}

// Fails with a message naming what was missing rather than letting a dev tenant with
// no session memory surface as a locator timeout halfway down a case. Sessions are
// produced by chatting with Nubi, so this is a data precondition, not a product bug.
export async function requireSessionRows(locators: SessionsLocators, minimum: number): Promise<number> {
  const rows = await locators.rowToggles.count();
  if (rows < minimum) {
    throw new Error(
      `this case needs at least ${minimum} session(s) in b-Cortex > Memory > Sessions, found ${rows} — the signed-in user has no recent Nubi conversations on this environment`
    );
  }
  return rows;
}

// Selects one option on the Session type toggle and waits for the filtered list to
// settle. The filter is client-side (SessionsTab derives `visible` from state it
// already holds), so the settle signal is the list itself, not a request.
// aria-checked is what makes this a real barrier rather than a race: ds/ToggleGroup
// derives it from the same `filter` state SessionsTab's `visible` useMemo reads, so
// React commits the checked option and the filtered rows in one pass — the toggle
// cannot read checked while the old rows are still on screen. Callers that go on to
// read a row count still assert the filtered content web-first first (see
// expectOnlyType), so a count is never taken from a list mid-update.
export async function selectSessionType(locators: SessionsLocators, label: string): Promise<void> {
  await locators.typeOption(label).click();
  await expect(locators.typeOption(label)).toHaveAttribute("aria-checked", "true", { timeout: 15000 });
  await waitForSessionsLoaded(locators);
}

// Asserts the filtered list holds nothing but the given type, as a retrying web-first
// assertion so it doubles as the settle point before a case reads a count off the DOM.
// Phrased as "no chip that is not this type" rather than "every chip is this type"
// because a count of non-matching chips is what Playwright can retry on. The three
// type labels share no substring, so hasNotText cannot exclude the wrong rows.
export async function expectOnlyType(locators: SessionsLocators, type: string): Promise<void> {
  await expect(locators.typeChips.filter({ hasNotText: type })).toHaveCount(0, { timeout: 15000 });
}

// Reads the working-memory count the row advertises on its secondary line, so a case
// can assert the expanded panel against the number the collapsed row promised rather
// than against a number the test picked.
export async function advertisedItemCount(locators: SessionsLocators, index: number): Promise<number> {
  await expect(locators.workingMemoryCounts.nth(index)).toBeVisible({ timeout: 15000 });
  const text = (await locators.workingMemoryCounts.nth(index).textContent()) ?? "";
  const parsed = Number.parseInt(text.trim(), 10);
  if (Number.isNaN(parsed)) {
    throw new Error(`could not read a working-memory count from the row line "${text}"`);
  }
  return parsed;
}
