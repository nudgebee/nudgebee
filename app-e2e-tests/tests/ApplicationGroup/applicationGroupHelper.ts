// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { ApplicationGroupLocators } from "./applicationGroupLocators";

// The Application Grouping listing is the second tab of /dashboards — it reads
// window.location.hash on mount and shows KubernetesApplicationGrouping for
// '#groups' (app/src/pages/dashboards/index.tsx). The detail view is its own
// route, /grouping?groupId=<id>.
export const GROUPING_LISTING_PATH = "/dashboards#groups";

// Precondition shared by every test here: the listing tab has to render at all.
// Named so a environment that never reached the tab fails with the reason rather
// than with an unexplained missing element.
const NO_LISTING_HINT =
  "The Application Grouping listing (#k8s-grouping) never rendered. /dashboards#groups selects it from " +
  "window.location.hash on mount — if the page settled on the dashboard list instead, the hash was lost.";

// Tests that need a group to read cannot invent one: the module has no delete,
// so this suite never creates one. See the PR's Follow-ups.
export const NO_GROUP_HINT =
  "This tenant has no application groups, and this suite deliberately does not create any (the module " +
  "offers no delete, so anything it created would be permanent). Create one by hand to cover this case.";

// A term no group name can match, so the "no results" assertion is about the
// filter working rather than about what the shared tenant happens to hold.
export function noMatchTerm(): string {
  return `zz-no-such-group-${Date.now().toString(36)}`;
}

// A name this run alone can produce. Only ever typed into the form and then
// cancelled — nothing here submits it — but kept unique so that if a cancel path
// ever did leak a record, the leak would be attributable rather than anonymous.
export function uniqueGroupName(): string {
  return `e2e auto group ${Date.now().toString(36)}`;
}

// Logs in (session reused via global-setup) and lands on the grouping listing.
export async function setup(page: Page): Promise<ApplicationGroupLocators> {
  const locators = new ApplicationGroupLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(GROUPING_LISTING_PATH);
  await expect(locators.listingRoot, NO_LISTING_HINT).toBeVisible({ timeout: 60000 });
  await locators.waitForTableSettled();
  return locators;
}

// Opens the Create Grouping modal from the listing toolbar. The button is gated on
// hasWriteAccess(), so its absence means the run's user is read-only rather than
// that the toolbar is broken — asserted with that reason.
export async function openCreateModal(locators: ApplicationGroupLocators): Promise<void> {
  await expect(locators.createGroupBtn, "Create Application Group is gated on hasWriteAccess() — this user appears to be read-only.").toBeVisible({
    timeout: 30000,
  });
  await locators.createGroupBtn.click();
  await expect(locators.dialog).toBeVisible({ timeout: 30000 });
  await expect(locators.dialogTitle).toHaveText("Create Grouping");
}

// Reads the name of the first group in the listing, or null when there are none.
// Absence is a normal outcome on an empty tenant — the callers assert on it with
// NO_GROUP_HINT — so it comes back as null rather than throwing.
export async function firstGroupName(locators: ApplicationGroupLocators): Promise<string | null> {
  await locators.waitForTableSettled();
  if ((await locators.rows.count()) === 0) return null;
  const name = (await locators.groupLinks.first().textContent()) ?? "";
  return name.trim() || null;
}
