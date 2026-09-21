// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { GROUPING_LISTING_PATH, NO_GROUP_HINT } from "../ApplicationGroup/applicationGroupHelper";
import { GroupingLocators } from "./groupingLocators";

// The group detail view — app/src/pages/grouping/index.jsx, reached as
// /grouping?groupId=<id> from the Application Grouping tab of /dashboards.
// GROUPING_LISTING_PATH and NO_GROUP_HINT come from the listing suite rather than
// being restated here; that file is imported, never edited.
export { NO_GROUP_HINT };

// Identifies the group this run opened. Null when the tenant holds none.
export interface OpenGroup {
  locators: GroupingLocators;
  groupId: string;
  groupName: string;
}

// A term no application name can match, so the "no results" assertion is about the
// filter working rather than about what the shared tenant happens to hold.
export function noMatchApplication(): string {
  return `zz-no-such-application-${Date.now().toString(36)}`;
}

// A name this run alone could produce. Only ever typed into the Update form and then
// cancelled — nothing here submits it — but kept unique so that if a cancel path ever
// did leak a write, the leak would be attributable rather than anonymous.
export function uniqueGroupName(): string {
  return `e2e auto detail ${Date.now().toString(36)}`;
}

// A name whose first character is a digit, which is what firstLetterAlpha rejects.
export function digitLeadingName(): string {
  return `9 e2e auto detail ${Date.now().toString(36)}`;
}

// Logs in (session reused via global-setup) and lands on the grouping listing.
async function openListing(page: Page): Promise<GroupingLocators> {
  const locators = new GroupingLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(GROUPING_LISTING_PATH);
  await expect(
    locators.listingRoot,
    "The Application Grouping listing (#k8s-grouping) never rendered — /dashboards#groups selects it from window.location.hash on mount.",
  ).toBeVisible({ timeout: 60000 });
  await locators.waitForTableSettled();
  return locators;
}

// The first group's name and detail href, or null on a tenant that holds none.
// Absence is a normal outcome on an empty tenant, so it comes back as null rather
// than throwing, and the callers name it with NO_GROUP_HINT.
async function readFirstGroupRef(locators: GroupingLocators): Promise<{ href: string; groupId: string; groupName: string } | null> {
  if ((await locators.rows.count()) === 0) return null;

  const link = locators.groupLinks.first();
  // The name is the anchor's own text; the cell also holds an "Account: <name>"
  // caption, so reading the whole cell would return both.
  const groupName = ((await link.textContent()) ?? "").trim();
  const href = (await link.getAttribute("href")) ?? "";
  // The href is app-relative, so URL needs a base; the origin is discarded and only
  // the query is read. Parsing this way rather than splitting on "?" keeps a hash
  // fragment out of the last parameter's value.
  const groupId = new URL(href, "http://a").searchParams.get("groupId") ?? "";
  if (!groupName || !groupId) return null;
  return { href, groupId, groupName };
}

// Opens the first group's detail view by clicking its row link, so the navigation
// the product actually offers is what gets exercised.
export async function openFirstGroupDetail(page: Page): Promise<OpenGroup | null> {
  const locators = await openListing(page);
  const ref = await readFirstGroupRef(locators);
  if (ref === null) return null;

  await locators.groupLinks.first().click();
  await expect(page).toHaveURL(/\/grouping\?groupId=[^&]+/, { timeout: 30000 });
  await expect(locators.summaryTab).toBeVisible({ timeout: 60000 });
  return { locators, groupId: ref.groupId, groupName: ref.groupName };
}

// Loads the detail route directly rather than by clicking — the deep-link path a
// bookmark or an emailed link takes, which mounts the page with groupId already in
// router.query instead of arriving through a client-side transition.
export async function deepLinkFirstGroup(page: Page): Promise<OpenGroup | null> {
  const locators = await openListing(page);
  const ref = await readFirstGroupRef(locators);
  if (ref === null) return null;

  await page.goto(ref.href);
  await expect(locators.summaryTab).toBeVisible({ timeout: 60000 });
  return { locators, groupId: ref.groupId, groupName: ref.groupName };
}

// Returns to the listing and asserts what it now holds, so a cancel path is checked
// against the server rather than against the detail view it just left. Both names are
// looked up through the listing's own search rather than by reading the first row:
// the listing is paginated and ordered by the server, so "still the first row" would
// be an assumption about ordering rather than about the group surviving unchanged.
export async function expectListingHolds(page: Page, presentName: string, absentName: string): Promise<void> {
  const locators = await openListing(page);

  await locators.searchAndApply(presentName);
  await expect(locators.rowLinkByName(presentName), "The group's original name is gone from the listing — the cancelled edit was written after all.").toBeVisible({
    timeout: 60000,
  });

  await locators.searchAndApply(absentName);
  await expect(locators.rows, "A group carrying the cancelled name exists — the cancel path leaked a write.").toHaveCount(0);
  await expect(locators.emptyState).toBeVisible({ timeout: 60000 });
}

// Moves to one of the detail view's tabs and waits for it to own the selection.
// aria-selected is the tab's own signal, so this is the wait as well as the check.
export async function openTab(locators: GroupingLocators, tab: "summary" | "events" | "applications"): Promise<void> {
  const target = tab === "summary" ? locators.summaryTab : tab === "events" ? locators.eventsTab : locators.applicationsTab;
  await target.click();
  await expect(target).toHaveAttribute("aria-selected", "true", { timeout: 60000 });
}

// Opens the edit modal from the detail view's right-hand tab-strip action. The button
// renders unconditionally here, unlike the listing's create action, so its absence is
// a real failure rather than a read-only user.
export async function openUpdateModal(locators: GroupingLocators): Promise<void> {
  await expect(locators.editGroupBtn).toBeVisible({ timeout: 60000 });
  await locators.editGroupBtn.click();
  await expect(locators.dialog).toBeVisible({ timeout: 30000 });
  await expect(locators.dialogTitle).toHaveText("Update Grouping");
}
