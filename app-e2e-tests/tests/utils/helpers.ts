import { Page, Locator } from "@playwright/test";

/**
 * Dismiss the "Welcome to Nudgebee" first-login tour dialog (TourWelcomeDialog)
 * if it appears. Best-effort and non-blocking: clicks Snooze, falling back to
 * the header close (x) button. Safe to call when no popup is present — it
 * simply returns after a short wait.
 */
export async function dismissWelcomeTour(page: Page, timeout = 4000): Promise<boolean> {
  const snoozeBtn = page.locator("#tour-welcome-snooze");
  const closeBtn = page.locator("#close-modal-btn");

  const appeared = await snoozeBtn
    .waitFor({ state: "visible", timeout })
    .then(() => true)
    .catch(() => false);

  if (!appeared) return false;

  try {
    await snoozeBtn.click();
  } catch {
    await closeBtn.click().catch(() => {});
  }

  await snoozeBtn.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
  console.log("Dismissed Welcome to Nudgebee tour popup");
  return true;
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
