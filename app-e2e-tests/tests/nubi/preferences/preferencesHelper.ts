// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { PreferencesLocators } from "./preferencesLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// app/src/api1/memory/index.ts wraps every layer call as Memory_<action>.
export const OP_PREFS_LOAD = "Memory_ai_memory_list";
export const OP_PREFS_SAVE = "Memory_ai_memory_upsert";

// Everything this suite writes goes to the signed-in user's own preference rows
// (scope 'user'), never to tenant defaults, and every case restores what it
// found. The token only has to be recognisable in a failure message, so it is
// generated per test rather than pinned.
export function uniqueNote(): string {
  return `nb_e2e_pref_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`;
}

// PreferencesTab renders no spinner — while the fetch is in flight it shows the
// same form with empty values and readOnly inputs. Timezone is the one field
// that is never empty once loaded (fromBackend falls back to the browser zone),
// so a non-empty value there is the only observable end-of-load signal.
export async function waitForPreferencesLoaded(locators: PreferencesLocators): Promise<void> {
  await expect(locators.timezoneInput).not.toHaveValue("", { timeout: 30000 });
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Preferences > Typed
// (relocated from Settings, docs/ia-consolidation-plan.md PR 4). Preferences
// is not b-Cortex's default landing group, and Typed is not its default
// sub-tab, so this takes two clicks: the group tab, then the sub-tab.
export async function openPreferencesTab(page: Page): Promise<PreferencesLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new PreferencesLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.bcortexBtn.click();
  await locators.preferencesTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.preferencesTab.click();
  await locators.typedTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.typedTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the
  // editor, which would otherwise surface as every control below being absent.
  // Raced rather than asserted straight away: checking for the placeholder on its
  // own right after the click passes against the not-yet-rendered tab and proves
  // nothing, so wait until whichever of the two rendered, then name which it was.
  await expect(locators.personalHeader.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 30000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForPreferencesLoaded(locators);
  return locators;
}

// Leaves the tab and comes back, which unmounts PreferencesTabContent and makes
// it refetch — the only way to tell a persisted value from one still held in
// React state. The unmount is waited on via the tab's own header rather than the
// textarea: the textarea locator falls back to any textarea inside the dialog,
// so a textarea on the tab being switched to would keep it from ever detaching.
export async function remountPreferencesTab(locators: PreferencesLocators): Promise<void> {
  await locators.inferredTab.click();
  await locators.personalHeader.waitFor({ state: "detached", timeout: 20000 });
  await locators.typedTab.click();
  await locators.personalHeader.waitFor({ state: "visible", timeout: 30000 });
  await waitForPreferencesLoaded(locators);
}

export async function setManualInputs(locators: PreferencesLocators, text: string): Promise<void> {
  await locators.manualInputsTextarea.fill(text);
  await expect(locators.manualInputsTextarea).toHaveValue(text, { timeout: 10000 });
}

export async function setTimezone(locators: PreferencesLocators, zone: string): Promise<void> {
  await locators.timezoneInput.fill(zone);
  await expect(locators.timezoneInput).toHaveValue(zone, { timeout: 10000 });
}

// Commits the pending edits, asserting the upsert actually left the browser and
// came back clean. onSave refetches afterwards, which rebuilds the baseline and
// clears `dirty` — so Save going disabled again is the signal that the write
// committed rather than that the click was merely received.
export async function savePreferences(page: Page, locators: PreferencesLocators, testName: string): Promise<void> {
  await expect(locators.saveBtn).toBeEnabled({ timeout: 10000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.saveBtn.click();
      await expect(locators.toastWithText(/will use this from your next message/)).toBeVisible({ timeout: 30000 });
    },
    { testName, operationNames: OP_PREFS_SAVE, timeoutMs: 45000 }
  );
  await expect(locators.saveBtn).toBeDisabled({ timeout: 30000 });
}

// Puts a field back to the value the test found and commits it, so a shared dev
// user does not accumulate this suite's edits. Scoped to the one field each case
// touched and asserted afterwards, so a cleanup that silently did nothing cannot
// read as a success. Best-effort by design: a case that failed before it saved
// has nothing to undo, and a teardown throwing would mask the real failure.
export async function restoreManualInputs(page: Page, locators: PreferencesLocators, original: string): Promise<void> {
  try {
    await setManualInputs(locators, original);
    // Nothing to commit when the case never got as far as saving — Save stays
    // disabled because the field already matches the loaded baseline.
    const dirty = await locators.saveBtn.isEnabled();
    if (!dirty) return;
    await savePreferences(page, locators, "Nubi Preferences - restore manual inputs");
    await expect(locators.manualInputsTextarea).toHaveValue(original, { timeout: 15000 });
  } catch (error) {
    console.warn(`[cleanup] manual inputs could not be restored to the value this test found: ${error}`);
  }
}

export async function restoreTimezone(page: Page, locators: PreferencesLocators, original: string): Promise<void> {
  try {
    await setTimezone(locators, original);
    const dirty = await locators.saveBtn.isEnabled();
    if (!dirty) return;
    await savePreferences(page, locators, "Nubi Preferences - restore timezone");
    await expect(locators.timezoneInput).toHaveValue(original, { timeout: 15000 });
  } catch (error) {
    console.warn(`[cleanup] the timezone could not be restored to the value this test found: ${error}`);
  }
}
