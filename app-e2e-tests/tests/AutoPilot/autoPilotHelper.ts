// Not for OSS
import { Page, expect } from "@playwright/test";
import { openAutoOptimize, parkCursor } from "../AutoOptimize/autoOptimizeHelper";
import { AutoPilotLocators } from "./autoPilotLocators";

// Every goto below is relative, resolved against `baseURL` in playwright.config.ts. That
// config reads BASE_URL without validating it, so an unset key does not fail here — it
// fails later as a goto against "about:blank". Asserted up front so the run names the
// missing key instead of reporting an unexplained navigation error.
if (!process.env.BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Precondition named once, so an environment holding no configurations fails with the
// reason rather than with an unexplained missing element on every test.
const NO_CONFIGS_HINT =
  "The Auto Optimize Optimizations listing rendered no rows, so there is no task to open. " +
  "AutoPilot task details has no other entry point — point the run at a tenant that holds " +
  "at least one auto optimize configuration.";

export interface OpenedTask {
  locators: AutoPilotLocators;
  taskName: string;
  taskId: string;
  accountId: string;
}

// Signs in, opens Auto Optimize, and follows the first configuration's name link into its
// task details page.
//
// Walked rather than deep-linked: an auto pilot id is tenant data that would go stale, and
// the listing link is the only in-product route to this page (AutoOptimizeListingTable.jsx
// renders it as /auto-pilot/task/<id>?accountId=<account>). Taking the id from the link the
// app itself built is also what proves the row that was clicked is the record that opened.
export async function openFirstAutoPilotTask(page: Page): Promise<OpenedTask> {
  const { locators: listing } = await openAutoOptimize(page, "optimizations");

  await expect(listing.optimizationsRows.first(), NO_CONFIGS_HINT).toBeVisible({ timeout: 90000 });

  const locators = new AutoPilotLocators(page);
  const link = locators.taskLinkIn(listing.optimizationsRows.first());
  await expect(link, NO_CONFIGS_HINT).toBeVisible({ timeout: 30000 });

  const taskName = (await link.innerText()).trim();
  const href = (await link.getAttribute("href")) ?? "";
  await link.click();

  await page.waitForURL(/\/auto-pilot\/task\/[^/?#]+/, { timeout: 60000 });
  await settleTasksTab(locators);

  // Read back from the URL the app actually navigated to rather than from the href, so a
  // redirect or a rewritten query cannot leave the test asserting against the wrong record.
  const opened = new URL(page.url());
  const taskId = opened.pathname.split("/").filter(Boolean).pop() ?? "";
  const accountId = opened.searchParams.get("accountId") ?? new URL(href, opened.origin).searchParams.get("accountId") ?? "";

  return { locators, taskName, taskId, accountId };
}

// The Tasks tab is ready when its listing card is up and the table has either landed its
// rows or rendered its empty heading in their place. Which of the two shows depends on
// what the configuration has actually run, so both are settled outcomes.
//
// Settled on a rendered row or the empty heading, not on the tbody being attached:
// CustomTable commits the id'd tbody a beat before its rows, so a caller that counts
// straight after an attachment-only wait can read 0 against a table that does have rows
// and pin a wrong baseline. `.first()` closes the alternation, since both sides can be
// briefly present at once and would otherwise trip strict mode.
export async function settleTasksTab(locators: AutoPilotLocators): Promise<void> {
  await expect(locators.tasksListing).toBeVisible({ timeout: 60000 });
  await expect(locators.tasksRows.first().or(locators.tasksEmpty).first()).toBeVisible({ timeout: 60000 });
}

// Opens one tab the way a person does — by clicking the strip. The tabs are Next links
// (Tabs.jsx behavior='router'), so the click also writes the fragment the page reads back;
// asserting the selected tab rather than the URL keeps this about the tab that rendered.
export async function openTaskTab(page: Page, tab: "tasks" | "details", locators: AutoPilotLocators): Promise<void> {
  const target = tab === "tasks" ? locators.tasksTab : locators.detailsTab;
  await expect(target).toBeVisible({ timeout: 30000 });
  await target.click();
  // Park the cursor off the strip: left on a tab, the sidebar rail's hover flyout is a
  // Popover with an invisible page-wide backdrop that swallows the next click.
  await parkCursor(page);
  await expect(target).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
}
