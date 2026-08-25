// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { ExecutionsLocators } from "./executionsLocators";
import { resetPersistedFilters } from "../Automations/automationsHelper";

// playwright.config reads BASE_URL without validating it, so an unset key does
// not fail there — it fails later as a navigation to "undefined/automation".
if (!process.env.BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Statuses this suite filters by, taken from EXECUTION_STATUS_OPTIONS in
// app/src/components/workflow/execution-dashboard/constants.ts. The label is what
// the dropdown renders; the value is what reaches the URL and the Status column.
export const COMPLETED_STATUS = { label: "Completed", value: "COMPLETED" };
export const FAILED_STATUS = { label: "Failed", value: "FAILED" };

// The seven columns TABLE_HEADERS declares, in order.
export const EXPECTED_COLUMNS = ["Date/Time", "Automation", "Account", "Status", "User", "Duration", "Error"];

// Every row-dependent test needs at least one execution in the default 10-day
// window, and would otherwise fail on a missing row rather than on the behaviour
// it covers.
export const NO_EXECUTIONS_HINT =
  "The Executions table rendered no rows in the default window. These tests read " +
  "existing executions, so the tenant needs at least one recent run before this suite can run.";

// Logs in and lands on /automation#executions with the dashboard settled.
export async function openExecutions(page: Page): Promise<ExecutionsLocators> {
  const locators = new ExecutionsLocators(page);

  await resetPersistedFilters(page);
  await new LoginPage(page).doFullLogin();

  await locators.automationSidenavBtn.waitFor({ state: "visible", timeout: 30000 });
  await locators.automationSidenavBtn.click();
  // Anchored: an unanchored /automation also matches the builder at
  // /automation/<id>, so a stray navigation there would satisfy this wait.
  await expect(page).toHaveURL(/\/automation(\?|#|$)/, { timeout: 30000 });

  await locators.executionsTab.waitFor({ state: "visible", timeout: 30000 });
  await locators.executionsTab.click();
  // Park the cursor off the tab strip: AnchorComponent opens a hover popover over
  // the toolbar underneath, and the next click lands on that instead.
  await page.mouse.move(640, 500);
  await expectExecutionsTabSelected(locators);
  await expectDashboardSettled(locators);

  return locators;
}

// Logs in, then opens /automation#executions directly with `search` as the query
// string, so the dashboard seeds its filters from the URL rather than from a
// click. `search` is passed without a leading "?".
export async function gotoExecutions(page: Page, search = ""): Promise<ExecutionsLocators> {
  const locators = new ExecutionsLocators(page);

  await resetPersistedFilters(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/automation${search ? `?${search}` : ""}#executions`);
  await expectExecutionsTabSelected(locators);
  await expectDashboardSettled(locators);

  return locators;
}

// Logs in and opens /automation on an arbitrary hash, without asserting which tab
// resolves — the caller does that. `hash` is passed without a leading "#".
export async function gotoAutomationHash(page: Page, hash: string): Promise<ExecutionsLocators> {
  const locators = new ExecutionsLocators(page);

  await resetPersistedFilters(page);
  await new LoginPage(page).doFullLogin();
  await page.goto(`/automation#${hash}`);
  await expect(locators.automationsTab).toBeVisible({ timeout: 30000 });

  return locators;
}

// AnchorComponent marks the open tab with data-tab-selected, which is the app's
// own notion of which tab is rendered and, unlike the URL, always in step with it.
export async function expectExecutionsTabSelected(locators: ExecutionsLocators): Promise<void> {
  await expect(locators.executionsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
}

// The dashboard is settled when both of its independent requests have landed: the
// aggregate (whose loaded card carries a testid its skeleton does not) and the
// table, which resolves into rows or the empty panel. CustomTable returns null
// for its <tbody> at zero rows, so waiting on the body alone would hang on an
// empty window — the alternation is what covers both shapes. It ends in .first()
// so a render holding both would be an "either one" match rather than a
// strict-mode violation.
export async function expectDashboardSettled(locators: ExecutionsLocators): Promise<void> {
  await expect(locators.dashboardRoot).toBeVisible({ timeout: 60000 });
  await expect(locators.summaryCard).toBeVisible({ timeout: 60000 });
  await expect(locators.rows.first().or(locators.emptyState).first()).toBeVisible({ timeout: 60000 });
}

// Toggles one option in a multi-select FilterDropdown. The same call selects and
// deselects: FilterDropdown's handleToggle flips an already-selected option off.
export async function toggleFilterOption(page: Page, locators: ExecutionsLocators, triggerLabel: "Status" | "Automation", label: string): Promise<void> {
  const trigger = triggerLabel === "Status" ? locators.statusFilter : locators.automationFilter;
  await trigger.click();

  const option = locators.filterOption(label);
  await expect(option).toBeVisible({ timeout: 15000 });
  await option.click();

  // The panel is a portaled overlay; Escape closes it without re-triggering the
  // dropdown, which a click on the page body can do.
  await page.keyboard.press("Escape");
  await expect(option).toBeHidden({ timeout: 15000 });
  await expectDashboardSettled(locators);
}

// Every value currently rendered in one column, trimmed. Read through expect.poll
// at the call sites so a refetch in flight is retried rather than snapshotted.
export async function columnTexts(locators: ExecutionsLocators, headerName: string): Promise<string[]> {
  const index = await locators.columnIndex(headerName);
  const texts = await locators.columnCellsAt(index).allInnerTexts();
  return texts.map((text) => text.trim());
}

// The total the pagination line reports, which is the server's count rather than
// the page's row count. Returns 0 when the line is absent, which CustomTable does
// at zero rows.
export async function totalRows(locators: ExecutionsLocators): Promise<number> {
  // Probe, not an assertion: no pagination line is the app's own rendering at
  // zero rows, so its absence is an expected outcome rather than a failure. Short
  // timeout because callers settle the dashboard first, so the row count — and
  // with it whether this line exists — is already decided by the time we look.
  const present = await locators.rowRangeSummary
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);
  if (!present) return 0;

  const text = (await locators.rowRangeSummary.innerText()).trim();
  const match = text.match(/of\s+([\d,]+)\s+results/);
  if (!match) {
    throw new Error(`Could not read a total from the pagination line: "${text}"`);
  }
  return Number(match[1].replace(/,/g, ""));
}
