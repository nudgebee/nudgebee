import { Page, Locator } from "@playwright/test";

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
