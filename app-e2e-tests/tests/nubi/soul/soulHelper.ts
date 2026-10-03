// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { SoulLocators } from "./soulLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// app/src/api1/memory/index.ts wraps every layer call as Memory_<action>, and
// Soul rides the shared memory layer actions with layer:'soul' in the request.
export const OP_SOUL_SAVE = "Memory_ai_memory_upsert";

// Every write this suite makes goes to the signed-in user's own Soul row
// (scope 'user'), never to the tenant profile, and the one case that saves puts
// back the text it found. The token only has to be recognisable in a failure
// message, so it is generated per test rather than pinned.
export function uniqueShorthand(): string {
  return `\`nb_e2e_soul_${Date.now()}_${Math.random().toString(36).slice(2, 6)}\` = e2e fixture alias`;
}

// SoulTab renders no spinner — while the fetch is in flight it shows the same
// form with empty values, its option cards non-interactive and Save/Reset
// disabled. Save going enabled is the only observable end-of-load signal, and it
// is why the option-card locators can double as proof the fetch returned.
// Personal scope only: the tenant profile hides both buttons for a non-admin.
export async function waitForSoulLoaded(locators: SoulLocators): Promise<void> {
  await expect(locators.saveBtn).toBeEnabled({ timeout: 30000 });
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Memory > Soul
// (relocated from Settings, docs/ia-consolidation-plan.md PR 4). Memory is
// b-Cortex's default landing group, so Soul is a real, clickable tab as soon
// as the modal opens — no separate top-level-group click first.
export async function openSoulTab(page: Page): Promise<SoulLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new SoulLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.bcortexBtn.click();
  await locators.soulTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.soulTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the
  // editor, which would otherwise surface as every control below being absent.
  // Raced rather than asserted straight away: checking for the placeholder on its
  // own right after the click passes against the not-yet-rendered tab and proves
  // nothing, so wait until whichever of the two rendered, then name which it was.
  await expect(locators.personalHeader.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 30000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForSoulLoaded(locators);
  return locators;
}

// Leaves the tab and comes back, which unmounts SoulTabContent and makes it
// refetch — the only way to tell a persisted value from one still held in React
// state. The unmount is waited on via the tab's own header rather than the
// textarea: the textarea locator falls back to any textarea inside the dialog,
// so a textarea on the tab being switched to would keep it from ever detaching.
export async function remountSoulTab(page: Page, locators: SoulLocators): Promise<void> {
  await locators.patternsTab.click();
  await locators.personalHeader.waitFor({ state: "detached", timeout: 20000 });
  await locators.soulTab.click();
  await locators.personalHeader.waitFor({ state: "visible", timeout: 30000 });
  await waitForSoulLoaded(locators);
}

export async function setDomainShorthand(locators: SoulLocators, text: string): Promise<void> {
  await locators.domainShorthandTextarea.fill(text);
  await expect(locators.domainShorthandTextarea).toHaveValue(text, { timeout: 10000 });
}

// Commits the pending edits, asserting the upsert actually left the browser and
// came back clean. SoulTab does not refetch on save and never gates Save on the
// form being dirty, so the button's state says nothing about whether the write
// landed — the captured mutation and the toast are the only signals there are.
export async function saveSoul(page: Page, locators: SoulLocators, testName: string): Promise<void> {
  await expect(locators.saveBtn).toBeEnabled({ timeout: 10000 });
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.saveBtn.click();
      await expect(locators.toastWithText(/will use this from your next message/)).toBeVisible({ timeout: 30000 });
    },
    { testName, operationNames: OP_SOUL_SAVE, timeoutMs: 45000 }
  );
  await expect(locators.saveBtn).toBeEnabled({ timeout: 30000 });
}

// Puts the shorthand back to the text the test found and commits it, so a shared
// dev user does not accumulate this suite's edits. Scoped to the one field the
// case touched and asserted afterwards, so a cleanup that silently did nothing
// cannot read as a success. Best-effort by design: a case that failed before it
// saved has nothing to undo, and a teardown throwing would mask the real failure.
export async function restoreDomainShorthand(page: Page, locators: SoulLocators, original: string): Promise<void> {
  try {
    await setDomainShorthand(locators, original);
    await saveSoul(page, locators, "Nubi Soul - restore domain shorthand");
    await remountSoulTab(page, locators);
    await expect(locators.domainShorthandTextarea).toHaveValue(original, { timeout: 30000 });
  } catch (error) {
    console.warn(`[cleanup] the domain shorthand could not be restored to the text this test found: ${error}`);
  }
}
