// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { PrivacyLocators } from "./privacyLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// app/src/api1/memory/index.ts wraps every layer call as Memory_<action>, and
// the consent toggles ride the generic get/upsert pair rather than an endpoint
// of their own — consentGet is ai_memory_get, consentSet is ai_memory_upsert.
export const OP_PRIVACY_LOAD = "Memory_ai_memory_get";
export const OP_PRIVACY_SAVE = "Memory_ai_memory_upsert";

// Everything this suite writes goes to the signed-in user's own consent rows
// (scope 'user'), never to the tenant rows, and every case puts back the state
// it found. The Global scope cases below only read: a tenant consent write would
// change what every user in the tenant gets, which is out of bounds for an
// unattended suite.

// PrivacyTab renders no spinner — while the consent fetch is in flight every row
// is already on screen with its switch disabled. The master switch becoming
// interactive is the only observable end-of-load signal, and it is the one
// switch the master cascade never disables.
export async function waitForPrivacyLoaded(locators: PrivacyLocators): Promise<void> {
  await expect(locators.masterSwitch).toBeEnabled({ timeout: 30000 });
}

// Logs in, opens the Nubi panel and lands on Settings > Privacy.
export async function openPrivacyTab(page: Page): Promise<PrivacyLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new PrivacyLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.settingsBtn.click();
  await locators.privacyTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.privacyTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the
  // consent rows, which would otherwise surface as every switch below being
  // absent. Raced rather than asserted straight away: checking for the
  // placeholder on its own right after the click passes against the
  // not-yet-rendered tab and proves nothing, so wait until whichever of the two
  // rendered, then name which it was.
  await expect(locators.personalDescription.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 30000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForPrivacyLoaded(locators);
  return locators;
}

// Leaves the tab and comes back, which unmounts PrivacyTabContent and makes it
// refetch — the only way to tell a persisted toggle from one still held in React
// state. The unmount is waited on via the master row's help line, which is
// unique to this tab, so a matching string on the tab being switched to cannot
// keep it from ever detaching.
export async function remountPrivacyTab(page: Page, locators: PrivacyLocators, testName: string): Promise<void> {
  await locators.agentsTab.click();
  await locators.masterRow.waitFor({ state: "detached", timeout: 20000 });

  // Asserting the consent read actually left the browser is what makes the
  // persistence cases mean something: without it a remount that replayed cached
  // state would look identical to one that re-read the committed row.
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.privacyTab.click();
      await expect(locators.personalDescription).toBeVisible({ timeout: 30000 });
    },
    { testName, operationNames: OP_PRIVACY_LOAD, timeoutMs: 45000 }
  );
  await waitForPrivacyLoaded(locators);
}

// Flips one consent toggle and proves the write landed. setLayer optimistically
// paints the new state, then POSTs, then refetches — so the switch settling on
// the requested state *after* the upsert came back clean is the signal the
// backend row committed, rather than that the click was merely received. The
// toast alone would not be: a failed write shows an error toast and silently
// reverts the switch.
export async function setLayerEnabled(
  page: Page,
  locators: PrivacyLocators,
  toggle: Locator,
  enabled: boolean,
  testName: string
): Promise<void> {
  await expect(toggle).toBeEnabled({ timeout: 15000 });
  const expectedToast = enabled ? "Layer enabled" : "Layer disabled";
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await toggle.click();
      await expect(locators.toastWithText(expectedToast)).toBeVisible({ timeout: 30000 });
    },
    { testName, operationNames: OP_PRIVACY_SAVE, timeoutMs: 45000 }
  );
  await expect(toggle).toBeChecked({ checked: enabled, timeout: 30000 });
}

// A run that failed midway can leave a layer switched off, and a case that opens
// by asserting the layer is on would then fail on that leftover rather than on
// its own journey. Reading the state first and writing only when it differs is
// what makes every case below safe to run twice.
export async function ensureLayerEnabled(page: Page, locators: PrivacyLocators, toggle: Locator, testName: string): Promise<void> {
  await expect(toggle).toBeEnabled({ timeout: 15000 });
  if (await toggle.isChecked()) return;
  await setLayerEnabled(page, locators, toggle, true, testName);
}

// Puts one toggle back to the state the test found, so a shared dev user does
// not accumulate this suite's writes. Best-effort by design: a case that failed
// before it wrote anything has nothing to undo, and a teardown that threw would
// mask the real failure. Skipped when the switch already reads the wanted state,
// so a cleanup that silently did nothing cannot read as a success.
export async function restoreLayer(
  page: Page,
  locators: PrivacyLocators,
  toggle: Locator,
  wasEnabled: boolean,
  testName: string
): Promise<void> {
  try {
    await expect(toggle).toBeEnabled({ timeout: 15000 });
    if ((await toggle.isChecked()) === wasEnabled) return;
    await setLayerEnabled(page, locators, toggle, wasEnabled, testName);
  } catch (error) {
    console.warn(`[cleanup] a consent toggle could not be restored to the state this test found: ${error}`);
  }
}
