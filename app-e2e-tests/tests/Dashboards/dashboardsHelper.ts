import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { DashboardsLocators } from "./dashboardsLocators";

/**
 * Prefix every dashboard this suite creates carries. Kept distinctive so the
 * cleanup sweep can never match a dashboard a human owns on the shared dev
 * tenant.
 */
export const E2E_TITLE_PREFIX = "e2e-auto-dashboard";

/**
 * A title no other run — or a re-run of this one — can collide with. The suite
 * drives a shared environment, so uniqueness is what makes these tests safe to
 * run twice and safe to run beside somebody else.
 */
export function uniqueDashboardTitle(): string {
  const stamp = Date.now().toString(36);
  const salt = Math.random().toString(36).slice(2, 8);
  return `${E2E_TITLE_PREFIX}-${stamp}-${salt}`;
}

/** Logs in (session reused via global-setup) and lands on the dashboard list. */
export async function setup(page: Page): Promise<DashboardsLocators> {
  const locators = new DashboardsLocators(page);
  await new LoginPage(page).doFullLogin();
  await page.goto("/dashboards");
  await locators.waitForListSettled();
  return locators;
}

/**
 * Creates a dashboard through the UI and returns once it is saved.
 *
 * The blank "New dashboard" path opens DashboardView already in edit mode with
 * an empty panel list, so a title is the only thing Save needs.
 */
export async function createDashboard(locators: DashboardsLocators, title: string, description = ""): Promise<void> {
  await locators.startBlankDashboard();
  await expect(locators.titleInput).toBeVisible();
  await locators.titleInput.fill(title);
  if (description) await locators.descriptionInput.fill(description);
  await locators.saveBtn.click();
  await expect(locators.createdToast).toBeVisible();
}

/**
 * Removes a dashboard by title if it is still there, and does nothing when it is
 * not — this runs from `finally` blocks whose test may have failed before
 * anything was created.
 */
export async function deleteDashboardIfPresent(page: Page, locators: DashboardsLocators, title: string): Promise<void> {
  // Get back to the listing from wherever the test ended up.
  await page.goto("/dashboards");
  await locators.waitForListSettled();
  await locators.search(title);

  // "Not there" is a normal outcome here, not a failure — a test that died before
  // creating anything still runs this. So the absent case is resolved by the empty
  // state arriving rather than by the row's wait timing out, which would burn the
  // full 10s on every such cleanup. Racing is safe from unhandled rejections:
  // Promise.race subscribes to both inputs, so the loser's later timeout is
  // already handled even though the race has settled.
  const present = await Promise.race([
    locators.rowByTitle(title).first().waitFor({ state: "visible", timeout: 10000 }).then(() => true),
    locators.emptyStateHeading.waitFor({ state: "visible", timeout: 10000 }).then(() => false),
  ]).catch(() => false);

  if (!present) {
    console.log(`Cleanup: "${title}" was not in the list — nothing to remove.`);
    return;
  }

  await locators.deleteBtnForRow(title).first().click();
  await expect(locators.dialogTitle).toHaveText("Delete dashboard?");
  await locators.dialogConfirmBtn.click();
  await expect(locators.deletedToast).toBeVisible();
  await expect(locators.rowByTitle(title)).toHaveCount(0);
  console.log(`Cleanup: removed "${title}".`);
}
