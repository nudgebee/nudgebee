import { Page, Locator } from "@playwright/test";

const pagesWithTourHandler = new WeakSet<Page>();
const pagesWithOverlayGuard = new WeakSet<Page>();

// Backstop for a driver.js tour a test action itself launches (a "How to ..." button, the Guides
// catalog) — its overlay swallows every click outside the spotlit element. Idempotent per page.
export async function registerTourOverlayGuard(page: Page): Promise<void> {
  if (pagesWithOverlayGuard.has(page)) return;
  pagesWithOverlayGuard.add(page);

  const overlay = page.locator(".driver-overlay, .driver-popover").first();

  await page.addLocatorHandler(overlay, async () => {
    // Escape is driver.js's own close path, so it tears the tour down cleanly when it lands.
    await page.keyboard.press("Escape").catch(() => {});
    if (await overlay.isHidden().catch(() => true)) {
      console.log("Auto-closed active tour overlay via Escape");
      return;
    }

    const closeBtn = page.locator(".driver-popover-close-btn").first();
    if (await closeBtn.count()) {
      await closeBtn.click({ timeout: 3000, force: true }).catch(() => {});
      if (await overlay.isHidden().catch(() => true)) {
        console.log("Auto-closed active tour overlay via close button");
        return;
      }
    }

    // Last resort: strip the overlay out of the DOM so it can't intercept pointer events.
    await page
      .evaluate(() => {
        document.querySelectorAll(".driver-overlay, .driver-popover").forEach((el) => el.remove());
        document.documentElement.classList.remove("driver-active", "driver-fade");
        document.body.classList.remove("driver-active", "driver-fade");
      })
      .catch(() => {});
    console.log("Auto-closed active tour overlay via DOM removal");
  });
}

/**
 * Register a page-wide auto-dismiss for the "Welcome to <brand>" first-login
 * tour dialog (TourWelcomeDialog). Hooks Playwright's page.addLocatorHandler so
 * the tour is snoozed automatically WHENEVER it pops up — the tour re-appears on
 * route changes even after being snoozed, and its modal overlay intercepts
 * pointer events on whatever is behind it (e.g. the automation listing's row
 * menus). Register once per page, before login; it stays active for the page's
 * lifetime and is safe when the tour never shows. Idempotent per page, so
 * repeated calls (retries, multiple logins) never stack duplicate handlers.
 */
export async function registerWelcomeTourAutoDismiss(page: Page): Promise<void> {
  if (pagesWithTourHandler.has(page)) return;
  pagesWithTourHandler.add(page);

  const snoozeBtn = page.locator("#tour-welcome-snooze");

  // Returns true once the tour dialog is gone (Snooze button detached/hidden).
  const dialogDismissed = () =>
    snoozeBtn
      .waitFor({ state: "hidden", timeout: 2500 })
      .then(() => true)
      .catch(() => false);

  // Click a control directly in the DOM. Fires React's onClick synchronously via
  // HTMLElement.click(), bypassing every Playwright actionability / stability /
  // hit-test wait — so it lands even while the dialog runs its pop-up keyframe or
  // its overlay is intercepting pointer events. Returns which control it clicked.
  const domClick = () =>
    page
      .evaluate(() => {
        const snooze = document.querySelector<HTMLElement>("#tour-welcome-snooze");
        if (snooze) {
          snooze.click();
          return "Snooze";
        }
        const close = document.querySelector<HTMLElement>("#close-modal-btn");
        if (close) {
          close.click();
          return "Close";
        }
        return null;
      })
      .catch(() => null);

  await page.addLocatorHandler(snoozeBtn, async () => {
    // The dialog (DS Modal) runs a ~480ms pop-up keyframe plus a height transition
    // that re-fires when tenant branding resolves async, so the Snooze button keeps
    // repositioning and Playwright clicks time out fighting the animation. The tour
    // can also re-mount on route changes. Strategy per attempt, most-reliable-first:
    //   1. DOM-level click (domClick) — immune to animation/overlay/hit-testing.
    //   2. dispatchEvent on Snooze — synthetic React onClick, no stability wait.
    //   3. force click on Snooze — bypasses actionability if the event didn't land.
    //   4. Escape key — closes the DS Modal via its own keydown handler.
    for (let attempt = 1; attempt <= 6; attempt++) {
      const domTarget = await domClick();
      if (domTarget && (await dialogDismissed())) {
        console.log(`Auto-dismissed Welcome tour popup via ${domTarget} (DOM click)`);
        return;
      }

      try {
        await snoozeBtn.dispatchEvent("click", {}, { timeout: 3000 });
        if (await dialogDismissed()) {
          console.log("Auto-dismissed Welcome tour popup via Snooze");
          return;
        }
      } catch {
        /* fall through to the next fallback */
      }

      try {
        await snoozeBtn.click({ timeout: 3000, force: true });
        if (await dialogDismissed()) {
          console.log("Auto-dismissed Welcome tour popup via Snooze (force click)");
          return;
        }
      } catch {
        /* fall through to the next fallback */
      }

      try {
        await page.keyboard.press("Escape");
        if (await dialogDismissed()) {
          console.log("Auto-dismissed Welcome tour popup via Escape");
          return;
        }
      } catch {
        /* retry the whole sequence */
      }
    }

    // Last resort: the tour ignored every control. Neutralize its overlay in the
    // DOM so the MUI backdrop can't intercept pointer events and wedge the flow
    // behind it. The tour re-mounts on route changes, so this handler re-fires
    // and re-hides the fresh overlay as needed.
    const neutralized = await page
      .evaluate(() => {
        const snooze = document.querySelector("#tour-welcome-snooze");
        const root = snooze?.closest<HTMLElement>(".MuiModal-root, .MuiDialog-root, [role='presentation']");
        if (root) {
          root.style.display = "none";
          return true;
        }
        return false;
      })
      .catch(() => false);
    if (neutralized && (await dialogDismissed())) {
      console.log("Auto-dismissed Welcome tour popup via overlay neutralization");
      return;
    }

    console.error("Failed to auto-dismiss Welcome tour popup after 6 attempts");
  });
}

// Cluster shown in the global dropdown, or "" if none resolves. Polls because the
// accounts list loads async. Pass `expected` after a fresh selection: the input keeps
// the previous label briefly, so the first non-empty read would be a false mismatch.
export async function readGlobalClusterValue(page: Page, timeout = 30000, expected?: string): Promise<string> {
  const clusterInput = page.locator("#auto-complete-global-cluster");
  const visible = await clusterInput
    .waitFor({ state: "visible", timeout })
    .then(() => true)
    .catch(() => false);
  if (!visible) return "";

  const deadline = Date.now() + timeout;
  let lastValue = "";
  while (Date.now() < deadline) {
    const value = (await clusterInput.inputValue().catch(() => "")).trim();
    if (value) {
      lastValue = value;
      if (!expected || value === expected) return value;
    }
    await page.waitForTimeout(250);
  }
  return lastValue;
}

export async function ensureSwitchEnabled(
  page: Page,
  selector: string,
  timeout = 5000
): Promise<boolean> {
  const toggle = page.locator(selector);

  await toggle.waitFor({ state: "visible", timeout });

  const standardChecked = await toggle.isChecked().catch(() => false);
  const ariaChecked = await toggle.getAttribute("aria-checked");
  const classList = await toggle.getAttribute("class");
  const classChecked = classList?.includes("checked") ?? false;

  const isChecked =
    standardChecked || ariaChecked === "true" || classChecked;

  if (!isChecked) {
    await toggle.click();
  }

  return true;
}

/**
 * Select an option from a CustomDropdown (MUI Autocomplete).
 * Clicks the dropdown, waits for options to load, types to filter, then selects.
 */
export async function selectDropdownOption(
  page: Page,
  dropdown: Locator,
  searchText: string
): Promise<void> {
  await dropdown.click();
  // Wait for options to load (listbox appears)
  await page.locator('[role="listbox"]').waitFor({ state: "visible", timeout: 15000 });
  await page.keyboard.press("Control+a");
  await page.keyboard.type(searchText);
  // Wait for filtered option to appear and click it
  await page.getByRole("option", { name: searchText }).first().waitFor({ state: "visible", timeout: 10000 });
  await page.getByRole("option", { name: searchText }).first().click({ force: true });
}
