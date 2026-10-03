// Not for OSS
import { Locator, Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { PatternsLocators } from "./patternsLocators";

// The five filter chips PatternsTab renders, in the order PATTERN_FILTERS
// declares them in app/src/ee/components/memory2/mocks.js. "All" is the
// unfiltered view rather than a state a row can be in, which is why the cases
// below treat it as the default rather than as one of the states.
export const PATTERN_FILTERS = ["All", "Active", "Fading", "Stale", "Pinned"];

// A token no inferred pattern can contain, so "this search matches nothing" is a
// property of the query rather than of whatever the tenant happens to hold.
// Generated per call so a stale render from an earlier case cannot satisfy it.
export function unmatchableQuery(): string {
  return `nb_e2e_no_such_pattern_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`;
}

// PatternsTab renders no spinner: while the fetch is in flight it shows the
// header, the search box and the chips with an empty list underneath, which is
// indistinguishable from a genuinely empty list. Waiting for either a row or the
// empty panel is the only end-of-load signal there is, and it is why every case
// below reaches the list through this.
export async function waitForPatternsLoaded(locators: PatternsLocators): Promise<void> {
  await expect(locators.rowActionButtons.first().or(locators.emptyPanel)).toBeVisible({ timeout: 30000 });
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Memory > Patterns.
// Memory is b-Cortex's default landing group and Patterns is Memory's own
// default sub-tab, so the tab is already showing once the modal is up — the
// click is there so a case still lands on Patterns if an earlier one left the
// modal on a sibling tab within the same browser context.
export async function openPatternsTab(page: Page): Promise<PatternsLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new PatternsLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openBCortex(locators.patternsTab);
  await locators.patternsTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the
  // tab, which would otherwise surface as every control below being absent.
  // Raced rather than asserted straight away: checking for the placeholder on
  // its own right after the click passes against the not-yet-rendered tab and
  // proves nothing, so wait until whichever of the two rendered, then name which
  // it was.
  await expect(locators.tabHeaderTitle.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 30000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForPatternsLoaded(locators);
  return locators;
}

// Leaves the tab and comes back, which unmounts PatternsTabContent and makes it
// refetch — the only way to tell a persisted pin from one still held in React
// state, and the only way to prove the search box and the chips reset. The
// unmount is waited on via the tab's own header rather than via a row: a sibling
// tab renders rows of its own, so a row locator would never detach.
export async function remountPatternsTab(locators: PatternsLocators): Promise<void> {
  await locators.sessionsTab.click();
  await locators.tabHeaderTitle.waitFor({ state: "detached", timeout: 20000 });
  await locators.patternsTab.click();
  await locators.tabHeaderTitle.waitFor({ state: "visible", timeout: 30000 });
  await waitForPatternsLoaded(locators);
}

// Fails with a message naming what was missing rather than letting a dev tenant
// with no inferred patterns surface as a locator timeout halfway down a case.
// Patterns are produced by a background job over the signed-in user's Nubi
// conversations, so this is a data precondition, not a product bug.
export async function requirePatternRows(locators: PatternsLocators, minimum: number): Promise<number> {
  const rows = await locators.rowActionButtons.count();
  if (rows < minimum) {
    throw new Error(
      `this case needs at least ${minimum} pattern(s) in b-Cortex > Memory > Patterns, found ${rows} — the signed-in user has no inferred patterns on this environment`
    );
  }
  return rows;
}

// Same contract as requirePatternRows, for the narrower precondition a pin
// round-trip needs: a row that is not pinned already. A tenant whose every
// pattern is pinned is a valid state, so this reports itself by name rather than
// letting the case click a button that is not there.
export async function requireUnpinnedRow(locators: PatternsLocators): Promise<void> {
  const unpinned = await locators.pinButtons.count();
  if (unpinned < 1) {
    throw new Error("this case needs at least one unpinned pattern in b-Cortex > Memory > Patterns, found none — every pattern is already pinned");
  }
}

// Reads a row's rendered description, which is how a row is identified again
// after a refetch. Blank is rejected rather than returned: PatternsTab always
// falls back to "<kind>: <subject>", so an empty string means the walk landed on
// the wrong element, and an empty needle would go on to match every row.
export async function readRowDescription(locators: PatternsLocators, actionButton: Locator): Promise<string> {
  const description = ((await locators.rowDescriptionFor(actionButton).textContent()) ?? "").trim();
  if (!description) {
    throw new Error("could not read the description of the pattern row under test — the row layout in PatternsTab.jsx has changed");
  }
  return description;
}

// Selects one filter chip and waits for the client-side filter to settle. The
// filter never leaves the browser (PatternsTab derives `visible` from state it
// already holds), so the settle signal is the chip's own aria-pressed, not a
// request.
export async function selectFilter(locators: PatternsLocators, label: string): Promise<void> {
  await locators.filterChip(label).click();
  await expect(locators.filterChip(label)).toHaveAttribute("aria-pressed", "true", { timeout: 15000 });
}

// Types a query and waits for the list to re-render against it. The search is
// client-side too, so the input having committed the value is the only signal
// that the filter it drives has been applied.
export async function search(locators: PatternsLocators, query: string): Promise<void> {
  await locators.searchInput.fill(query);
  await expect(locators.searchInput).toHaveValue(query, { timeout: 15000 });
}

// Puts a row back to unpinned after a case pinned it, so a shared dev user does
// not accumulate this suite's pins. Scoped to the one row the case touched and
// asserted afterwards, so a cleanup that silently did nothing cannot read as a
// success. Best-effort by design: a case that failed before it pinned has
// nothing to undo, and a teardown throwing would mask the real failure.
export async function restoreUnpinned(locators: PatternsLocators, description: string): Promise<void> {
  try {
    await selectFilter(locators, "All");
    await search(locators, "");
    const rowActions = locators.rowActionsForDescription(description);
    const unpinBtn = rowActions.getByRole("button", { name: "Unpin", exact: true });
    const pinBtn = rowActions.getByRole("button", { name: "Pin", exact: true });

    // A probe, not an assertion: a case that failed before its pin landed leaves
    // the row already unpinned, and cleanup has to return from that rather than
    // throw on it. Racing the two states settles the moment either button
    // renders, so "nothing to undo" costs one render instead of a full click
    // timeout on a button that was never going to appear. Both branches carry
    // their own catch so the losing waitFor cannot reject unhandled after the
    // race has already been decided.
    const settled = await Promise.race([
      unpinBtn
        .waitFor({ state: "visible", timeout: 20000 })
        .then(() => true)
        .catch(() => null),
      pinBtn
        .waitFor({ state: "visible", timeout: 20000 })
        .then(() => false)
        .catch(() => null),
    ]);
    if (settled !== true) {
      return;
    }

    await unpinBtn.click();
    await expect(pinBtn).toBeVisible({ timeout: 20000 });
  } catch (error) {
    console.warn(`[cleanup] the pattern this test pinned could not be put back to unpinned: ${error}`);
  }
}
