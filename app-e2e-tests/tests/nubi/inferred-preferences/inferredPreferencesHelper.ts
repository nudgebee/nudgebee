// Not for OSS
import { Locator, Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { ensureInferredPreferences, skipWhenMissing } from "../memoryFixtures";
import { InferredPreferencesLocators } from "./inferredPreferencesLocators";

// The tab sits three round trips deep — login, the Nubi panel, then the b-Cortex modal — and CI
// is materially slower than a local run, so the budgets are named once here instead of being
// repeated as literals down the spec.
export const CASE_TIMEOUT = 180000;
export const CONTROL_TIMEOUT = 15000;
export const MOUNT_TIMEOUT = 20000;
export const LOAD_TIMEOUT = 30000;

// InferredPreferencesTab renders no spinner: while the fetch is in flight it renders the row card
// with nothing in it, which is indistinguishable from a genuinely empty queue — the empty state
// only replaces the card once `loading` has gone false. Waiting for either a row control or that
// empty state is therefore the only end-of-load signal there is, and it is why every case below
// reaches the list through this.
export async function waitForInferredLoaded(locators: InferredPreferencesLocators): Promise<void> {
  await expect(locators.rowActionButtons.first().or(locators.emptyState)).toBeVisible({ timeout: LOAD_TIMEOUT });
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Preferences > Inferred. Memory is
// b-Cortex's default landing group, so this takes one group click; Inferred is the Preferences
// group's own default sub-tab (BCortexModal.jsx DEFAULT_TAB_STATE), so the sub-tab click after it
// is there only so a case still lands on Inferred if an earlier one left the group on Typed within
// the same browser context. The modal is opened through NubiLocators.openBCortex rather than a bare
// rail click: that helper retries the click and the tab wait as a pair, which is what keeps the
// open from failing on CI while passing locally.
export async function openInferredTab(page: Page): Promise<InferredPreferencesLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new InferredPreferencesLocators(page);

  await loginPage.doFullLogin();
  // Before the tab mounts so its first load already sees the preference. Skips the case, with the
  // reason, if the backend infers none from what Nubi is told.
  await ensureInferredPreferences(page, 1);
  await nubi.openPanel();
  await nubi.openBCortex(locators.preferencesGroupTab);
  await locators.preferencesGroupTab.click();
  await locators.inferredTab.waitFor({ state: "visible", timeout: MOUNT_TIMEOUT });
  await locators.inferredTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the tab, which would
  // otherwise surface as every control below being absent. Raced rather than asserted straight
  // away: checking for the placeholder on its own right after the click passes against the
  // not-yet-rendered tab and proves nothing, so wait until whichever of the two rendered, then
  // name which it was.
  await expect(locators.tabHeaderTitle.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: LOAD_TIMEOUT });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForInferredLoaded(locators);
  return locators;
}

// Leaves the Inferred tab for its Typed sibling and comes back, which unmounts
// InferredPreferencesTabContent and makes it refetch — the only way to tell a stored Keep from one
// still held in React state. The unmount is waited on via the tab's own header rather than via a
// row: Typed renders controls of its own, and the header is the one string only Inferred shows.
export async function remountInferredTab(locators: InferredPreferencesLocators): Promise<void> {
  await locators.typedTab.click();
  await locators.tabHeaderTitle.waitFor({ state: "detached", timeout: MOUNT_TIMEOUT });
  await locators.inferredTab.click();
  await locators.tabHeaderTitle.waitFor({ state: "visible", timeout: LOAD_TIMEOUT });
  await waitForInferredLoaded(locators);
}

// Fails with a message naming what was missing rather than letting a dev user with no inferred
// preferences surface as a locator timeout halfway down a case. Inferred preferences are written by
// the backend as it reads the signed-in user's own conversations, and nothing in the product creates
// one on demand, so this is a data precondition and not a product bug.
export async function requireInferredRows(locators: InferredPreferencesLocators, minimum: number): Promise<number> {
  const rows = await locators.rowActionButtons.count();
  if (rows < minimum) {
    throw new Error(
      `this case needs at least ${minimum} inferred preference(s) in b-Cortex > Preferences > Inferred, found ${rows} — the signed-in user has none on this environment`
    );
  }
  return rows;
}

// Same contract as requireInferredRows, for the narrower precondition the Keep cases need: a row
// that is not kept already. A queue whose every preference has been kept is a valid state, so it
// reports itself by name rather than letting a case click a button that is not there.
export async function requireUnkeptRow(locators: InferredPreferencesLocators): Promise<void> {
  const unkept = await locators.keepButtons.count();
  skipWhenMissing(unkept, 1, "unkept inferred preference in b-Cortex > Preferences > Inferred (every preference is already kept)");
}

// Reads the value line of the row under test, which is how that row is identified again after a
// refetch. Blank is rejected rather than returned: the value is what the row exists to show, so an
// empty string means the walk landed on the wrong element, and an empty needle would go on to match
// every row.
export async function readRowValue(locators: InferredPreferencesLocators, actionButton: Locator): Promise<string> {
  const value = ((await locators.valueOf(actionButton).textContent()) ?? "").trim();
  if (!value) {
    throw new Error("could not read the value of the inferred preference under test — the row layout in InferredPreferencesTab.jsx has changed");
  }
  return value;
}

// Puts a preference back to unkept after a case kept it, so a shared dev user does not accumulate
// this suite's Keeps. Unkeep is the product's own undo for Keep and takes no confirmation, so the
// restore is one click. Scoped to the row the case touched and asserted afterwards, so a cleanup
// that silently did nothing cannot read as a success. Best-effort by design: a case that failed
// before its Keep landed has nothing to undo, and a teardown throwing would mask the real failure.
export async function restoreUnkept(locators: InferredPreferencesLocators, value: string): Promise<void> {
  try {
    const row = locators.rowWithValue(value);
    const unkeepBtn = locators.unkeepButtonsIn(row);
    const keepBtn = locators.keepButtonsIn(row);

    // Settles the moment either control renders, so "nothing to undo" costs one render instead of a
    // full click timeout on a button that was never going to appear. Waited as one `.or()` locator
    // rather than two raced waitFors: racing leaves the losing wait pending in the background until
    // it times out, and this runs in a finally, where the page can close under it. The row holds
    // exactly one of the two, so the `.or()` is never a strict-mode violation.
    await unkeepBtn.or(keepBtn).waitFor({ state: "visible", timeout: MOUNT_TIMEOUT });
    // A case that failed before its Keep landed leaves the row already unkept, and cleanup has to
    // return from that rather than undo something that never happened.
    if (!(await unkeepBtn.isVisible())) {
      return;
    }

    await unkeepBtn.click();
    await expect(keepBtn).toBeVisible({ timeout: MOUNT_TIMEOUT });
  } catch (error) {
    console.warn(`[cleanup] the inferred preference this test kept could not be put back to unkept: ${error}`);
  }
}
