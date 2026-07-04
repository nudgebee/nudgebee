import { Page, Locator } from "@playwright/test";

const pagesWithTourHandler = new WeakSet<Page>();

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
  await page.addLocatorHandler(snoozeBtn, async () => {
    try {
      await snoozeBtn.click({ timeout: 5000 });
      console.log("Auto-dismissed Welcome tour popup via Snooze");
    } catch {
      try {
        await page.locator("#close-modal-btn").click({ timeout: 5000 });
        console.log("Auto-dismissed Welcome tour popup via Close");
      } catch (err) {
        console.error("Failed to auto-dismiss Welcome tour popup:", err);
      }
    }
  });
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
