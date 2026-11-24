import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { GlobalChatLocators } from "./globalChatLocators";

// NubiGlobalChat.tsx hands CustomDrawer width='460px' docked and '50vw' expanded, and
// storageKey='nb.nubiGlobalChat.width' for the width a resize drag commits.
export const DOCKED_WIDTH_PX = 460;
export const PANEL_WIDTH_STORAGE_KEY = "nb.nubiGlobalChat.width";

// The Paper carries a 1px border and MUI's border-box sizing, so the measured box is a
// couple of pixels under the width the drawer was handed. Wide enough to absorb that and
// sub-pixel rounding, far narrower than any difference the cases below assert on.
const WIDTH_TOLERANCE_PX = 8;

// Lands the session and returns the account the global cluster dropdown committed to, which
// is what the panel's own account chip has to agree with.
export async function landOnApp(page: Page): Promise<{ locators: GlobalChatLocators; cluster: string }> {
  const loginPage = new LoginPage(page);
  const selection = await loginPage.doFullLogin();
  // Sign-in lands on /ask-nudgebee, where the launcher is not mounted, so move to Home first.
  if (page.url().includes("/ask-nudgebee")) {
    await page.goto("/home");
  }
  const locators = new GlobalChatLocators(page);
  await expect(locators.launcher).toBeVisible({ timeout: 60000 });
  return { locators, cluster: selection.cluster };
}

// The launcher is portalled to document.body and painted before the panel's own bundle has
// necessarily resolved, so a click landing early opens nothing at all. Click and wait are
// retried as a pair, re-clicking only while the panel is NOT up — retrying on "the panel is
// not visible yet" instead would keep firing clicks at a panel that is already opening.
export async function openGlobalChat(page: Page): Promise<{ locators: GlobalChatLocators; cluster: string }> {
  const landed = await landOnApp(page);
  await expect(async () => {
    // Probe: "the panel is not up yet" is the normal first-pass answer and must come back as
    // false rather than throw, since that is what drives the retry.
    if (!(await landed.locators.panel.isVisible().catch(() => false))) {
      await landed.locators.launcher.click();
    }
    await landed.locators.panel.waitFor({ state: "visible", timeout: 10000 });
  }).toPass({ timeout: 90000, intervals: [1000, 2000, 3000] });
  return landed;
}

// Width of the open panel, rounded to whole pixels.
export async function panelWidth(locators: GlobalChatLocators): Promise<number> {
  const box = await locators.panelPaper.boundingBox();
  if (!box) {
    throw new Error("The Nubi panel is open but has no layout box to measure");
  }
  return Math.round(box.width);
}

// Polled rather than read once: the width is React state, so it lands a render after the click
// or the drag that changed it.
export async function expectPanelWidth(locators: GlobalChatLocators, expected: number, label: string): Promise<void> {
  await expect
    .poll(() => panelWidth(locators), { timeout: 20000, message: `${label} lower bound` })
    .toBeGreaterThan(expected - WIDTH_TOLERANCE_PX);
  await expect
    .poll(() => panelWidth(locators), { timeout: 20000, message: `${label} upper bound` })
    .toBeLessThan(expected + WIDTH_TOLERANCE_PX);
}

// CustomDrawer sizes the drawer as `window.innerWidth - e.clientX` while a drag is live, so the
// x the pointer is released at IS the requested width. Read from the browser rather than
// viewportSize() because the scrollbar can put the two a few pixels apart.
export async function dragPanelWidthTo(page: Page, locators: GlobalChatLocators, target: number): Promise<void> {
  const handle = await locators.resizeHandle.boundingBox();
  if (!handle) {
    throw new Error("The panel's resize handle is not on screen — the drawer was rendered with resizable off");
  }
  const innerWidth = await page.evaluate(() => window.innerWidth);
  await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
  await page.mouse.down();
  await page.mouse.move(innerWidth - target, handle.y + handle.height / 2, { steps: 12 });
  await page.mouse.up();
}

// Half the viewport, the width NubiGlobalChat asks for when the panel is expanded.
export async function expandedWidth(page: Page): Promise<number> {
  return Math.round((await page.evaluate(() => window.innerWidth)) / 2);
}

// Drops the width a drag committed so the next run starts on the docked default. Reload is the
// caller's job — the open drawer keeps the dragged width in React state until it remounts.
export async function clearPersistedPanelWidth(page: Page): Promise<void> {
  await page.evaluate((key) => {
    try {
      window.localStorage.removeItem(key);
    } catch {
      // localStorage is unavailable on an opaque origin, where nothing was persisted anyway.
    }
  }, PANEL_WIDTH_STORAGE_KEY);
}

// The first account row the picker offers that the chat is not already on, with the position
// the case clicks it at. Throws by name rather than letting a single-account tenant surface as
// a click timeout halfway down the switch case.
//
// The picker opens collapsed — one row per cloud provider and no options at all — so every
// group is expanded first. Clicked back-to-front because expanding the group that holds the
// current selection inserts a "Clear" control inside that header row, shifting the index of
// every header after it but never of one already visited.
export async function pickOtherAccount(locators: GlobalChatLocators, current: string): Promise<{ label: string; index: number }> {
  await expect(locators.accountGroupHeaders.first()).toBeVisible({ timeout: 20000 });
  const groups = await locators.accountGroupHeaders.count();
  for (let group = groups - 1; group >= 0; group--) {
    await locators.accountGroupHeaders.nth(group).click();
  }

  await expect(locators.accountOptions.first()).toBeVisible({ timeout: 20000 });
  const labels = (await locators.accountOptions.allTextContents()).map((label) => label.trim());
  const index = labels.findIndex((label) => label && label !== current);
  if (index < 0) {
    throw new Error(`The chat account picker offered no account other than "${current}" — this case needs a tenant with at least two`);
  }
  return { label: labels[index], index };
}
