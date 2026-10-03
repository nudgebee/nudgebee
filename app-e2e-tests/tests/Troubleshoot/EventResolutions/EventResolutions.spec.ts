// Not for OSS
import { test, expect } from "@playwright/test";
import {
  ACCOUNT_QUERY_PARAM,
  ALL_EVENTS_FRAGMENT,
  COLUMN_HEADINGS,
  EMPTY_STATE_HEADING,
  EVENT_RESOLUTIONS_FRAGMENT,
  RESOLUTION_COLUMN,
  RESOLVER_COLUMN,
  RESOLVER_OPTIONS,
  STATUS_COLUMN,
  STATUS_OPTIONS,
  TYPE_OPTIONS,
  noMatchAccountId,
  typeCellText,
  typeOptionFromCell,
} from "./eventResolutionsConstants";
import {
  accountIdsInUrl,
  expectColumnToRead,
  expectListingSettled,
  openAccountFilterOptions,
  openEventResolutions,
  pickPopulatedOption,
  readPaginationSummary,
  selectFilterOption,
  selectFirstAccount,
  settleListing,
} from "./eventResolutionsHelper";
import { TroubleshootTabs } from "../TroubleshootLocators";

// Troubleshoot -> Event Resolutions: the tenant-level listing at /troubleshoot#event-resolutions
// (app/src/components/troubleshoot/EventResolutions.jsx), which records what was done about an
// event across every account. Not /optimise#resolutions — that is a different component
// (optimise-new/ResolutionsView.tsx), covered by tests/Optimize/Resolutions.
//
// The module is READ-ONLY. Its whole interactive surface is four filters, a CSV download and
// pagination: there is no create, edit or delete control, and so no form and no field
// validation a test could drive. The nearest thing it owns to persisted state is the Account
// filter, which writes `accountIds` to the URL and reads it back on mount — that round trip is
// asserted across a real reload below, and the negative case drives an account id nothing can
// match instead of an invalid form field. Written up in the PR.
//
// A shared dev tenant may legitimately hold no resolutions at all, and may hold none of a given
// status or type. Every case therefore asserts the empty branch when the listing has nothing in
// it, rather than skipping or demanding rows the environment does not owe it — an empty
// environment reads as a pass, never as a locator that never appeared.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Troubleshoot Event Resolutions", () => {
  test(
    "Event Resolutions sanity - open the Troubleshoot Event Resolutions tab, verify the Account, Status, Type and Resolver filters and the Download action render above the listing",
    { tag: ["@dev", "@test", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);

      await test.step("The tab strip has Event Resolutions selected", async () => {
        await expect(locators.EventResolutionsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
      });

      await test.step("The toolbar carries all four filters and the download action", async () => {
        await expect(locators.accountFilter).toBeVisible({ timeout: 30000 });
        await expect(locators.statusFilter).toBeVisible();
        await expect(locators.typeFilter).toBeVisible();
        await expect(locators.resolverFilter).toBeVisible();
        await expect(locators.downloadButton).toBeEnabled();
      });

      await test.step("The listing reaches a terminal state", async () => {
        await expectListingSettled(locators);
      });
    }
  );

  test(
    "Event Resolutions sanity - open the Event Resolutions tab, verify the populated listing declares all eight columns, or that an empty one shows No Data Available instead",
    { tag: ["@dev", "@test", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);
      await expectListingSettled(locators);

      // CustomTable's empty branch replaces the whole <table>, headings included
      // (CustomTable.jsx:941), so which of the two shapes is on screen decides what can be
      // claimed. Both are settled outcomes of the same fetch.
      if ((await locators.rows.count()) === 0) {
        await expect(locators.emptyState).toHaveText(EMPTY_STATE_HEADING, { timeout: 30000 });
        return;
      }

      for (const heading of COLUMN_HEADINGS) {
        await expect(locators.columnHeading(heading), `the ${heading} column should render`).toBeVisible();
      }
    }
  );

  test(
    "Event Resolutions - open the Event Resolutions tab, read the pagination summary, verify its stated range accounts for exactly the rows on screen",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);
      await expectListingSettled(locators);

      const rowCount = await locators.rows.count();
      if (rowCount === 0) {
        // CustomTable withholds the pagination block entirely at zero rows
        // (CustomTable.jsx:969), so there is no range to reconcile and its absence is the claim.
        await expect(locators.emptyState).toHaveText(EMPTY_STATE_HEADING, { timeout: 30000 });
        await expect(locators.paginationSummary).toHaveCount(0);
        return;
      }

      const summary = await readPaginationSummary(locators);
      expect(summary.start, "the first page should start at row 1").toBe(1);
      expect(summary.end - summary.start + 1, "the summary range should span exactly the rendered rows").toBe(rowCount);
      expect(summary.total, "the total cannot be smaller than what one page shows").toBeGreaterThanOrEqual(rowCount);
    }
  );

  test(
    "Event Resolutions - open the Event Resolutions tab, pick a Status the listing already holds, verify every Status cell reads that status",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);
      const status = await pickPopulatedOption(locators, STATUS_COLUMN, STATUS_OPTIONS);

      if (!status) {
        await expect(locators.emptyState, "with no rows to filter the listing should show its empty state").toHaveText(
          EMPTY_STATE_HEADING,
          { timeout: 30000 }
        );
        return;
      }

      await selectFilterOption(locators, locators.statusFilter, status);
      await settleListing(locators);
      await expectColumnToRead(locators, STATUS_COLUMN, status);
      await expect(locators.statusFilter, "the trigger should report the status it is filtering on").toContainText(status);
    }
  );

  test(
    "Event Resolutions - open the Event Resolutions tab, pick a Type the listing already holds, verify every Resolution cell reads that resolution type",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);
      const type = await pickPopulatedOption(locators, RESOLUTION_COLUMN, TYPE_OPTIONS, typeOptionFromCell);

      if (!type) {
        await expect(locators.emptyState, "with no rows to filter the listing should show its empty state").toHaveText(
          EMPTY_STATE_HEADING,
          { timeout: 30000 }
        );
        return;
      }

      await selectFilterOption(locators, locators.typeFilter, type);
      await settleListing(locators);
      // The option reads "PullRequest" while the cell it selects reads "Pull Request", so the
      // whole-column claim is made on the option's spelling and the cell's own spelling is
      // pinned separately.
      await expectColumnToRead(locators, RESOLUTION_COLUMN, type, typeOptionFromCell);
      await expect(locators.cellsInColumn(RESOLUTION_COLUMN).first()).toHaveText(typeCellText(type));
    }
  );

  test(
    "Event Resolutions - open the Event Resolutions tab, pick a Resolver the listing already holds, verify every Resolver cell reads that resolver",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);
      const resolver = await pickPopulatedOption(locators, RESOLVER_COLUMN, RESOLVER_OPTIONS);

      if (!resolver) {
        await expect(locators.emptyState, "with no rows to filter the listing should show its empty state").toHaveText(
          EMPTY_STATE_HEADING,
          { timeout: 30000 }
        );
        return;
      }

      await selectFilterOption(locators, locators.resolverFilter, resolver);
      await settleListing(locators);
      await expectColumnToRead(locators, RESOLVER_COLUMN, resolver);
      await expect(locators.resolverFilter, "the trigger should report the resolver it is filtering on").toContainText(resolver);
    }
  );

  test(
    "Event Resolutions - open the Event Resolutions tab, select the first account in the Account filter, reload the page, verify the accountIds query parameter persisted and the account is still selected",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);

      expect(accountIdsInUrl(page, ACCOUNT_QUERY_PARAM), "no account scope should be applied on a plain open").toBe("");

      const accountLabel = await selectFirstAccount(locators, page);

      // applyFiltersOnRouter pushes the selected ids into the query string
      // (EventResolutions.jsx:310), so this is the value the reload has to bring back.
      await expect(page).toHaveURL(new RegExp(`[?&]${ACCOUNT_QUERY_PARAM}=`), { timeout: 30000 });
      const selectedIds = accountIdsInUrl(page, ACCOUNT_QUERY_PARAM);
      expect(selectedIds, "the Account filter should write the account id it selected").not.toBe("");
      await expect(locators.accountFilter).toContainText(accountLabel);

      await page.reload();
      await expect(locators.listing).toBeVisible({ timeout: 60000 });
      await settleListing(locators);

      await test.step("The reloaded page comes back scoped to the same account", async () => {
        expect(accountIdsInUrl(page, ACCOUNT_QUERY_PARAM), "the account scope should survive the reload").toBe(selectedIds);
        await expect(locators.accountFilter).toContainText(accountLabel);

        // The rehydrated selection, read off the panel itself rather than off the trigger: this
        // is what proves the component parsed the query parameter back into filter state
        // (EventResolutions.jsx:37-40) rather than merely leaving the label on screen.
        const panel = await openAccountFilterOptions(locators);
        await expect(locators.selectedFilterOptions(panel)).toHaveCount(1, { timeout: 30000 });
        await page.keyboard.press("Escape");
      });
    }
  );

  test(
    "Event Resolutions - deep-link the Event Resolutions tab with an accountIds value no account can match, verify the listing renders no rows and shows the No Data Available empty state",
    { tag: ["@dev", "@test", "@regression", "@negative"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page, `?${ACCOUNT_QUERY_PARAM}=${noMatchAccountId()}`);

      await expect(locators.rows).toHaveCount(0, { timeout: 60000 });
      await expect(locators.emptyState).toHaveText(EMPTY_STATE_HEADING, { timeout: 60000 });
      // CustomTable withholds the pagination block entirely at zero rows (CustomTable.jsx:969),
      // so its absence is part of the empty contract rather than a missing locator.
      await expect(locators.paginationSummary).toHaveCount(0);
    }
  );

  test(
    "Event Resolutions - open the Event Resolutions tab, page through the listing, verify the summary advances to the next range when a second page exists and covers the whole set when it does not",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);
      await expectListingSettled(locators);

      const rowCount = await locators.rows.count();
      if (rowCount === 0) {
        await expect(locators.emptyState).toHaveText(EMPTY_STATE_HEADING, { timeout: 30000 });
        return;
      }

      const firstPage = await readPaginationSummary(locators);

      // One page of results is a legitimate state of a shared tenant, and it has its own
      // contract: the range covers everything there is and MUI leaves the next control disabled.
      if (firstPage.total <= rowCount) {
        expect(firstPage.end, "a single page should account for every result").toBe(firstPage.total);
        await expect(locators.nextPageButton).toBeDisabled();
        return;
      }

      await expect(locators.nextPageButton).toBeEnabled({ timeout: 30000 });
      await locators.nextPageButton.click();
      await settleListing(locators);

      await expect(async () => {
        const secondPage = await readPaginationSummary(locators);
        expect(secondPage.start, "the second page should start after the first page ends").toBeGreaterThan(firstPage.end);
        expect(secondPage.total, "paging must not change the size of the result set").toBe(firstPage.total);
      }).toPass({ timeout: 60000, intervals: [1000, 2000, 4000] });

      await expect(locators.rows.first()).toBeVisible();
    }
  );

  test(
    "Event Resolutions - open the Event Resolutions tab, switch to All Events and back, verify the Event Resolutions listing comes back as the selected tab",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openEventResolutions(page);

      await test.step("All Events takes over the pane", async () => {
        await locators.gotoTab(TroubleshootTabs.allEvents);
        // Today the bare top-level hash is the end state, not a step towards a canonicalized
        // one — see the note on ALL_EVENTS_FRAGMENT. Accepting an optional sub-fragment keeps
        // the assertion on what it means to test — that All Events took the pane — rather than
        // on whether the app appends a segment, while still refusing a different fragment that
        // merely starts with the same text.
        await expect(page).toHaveURL(new RegExp(`#${ALL_EVENTS_FRAGMENT}(/|$)`), { timeout: 30000 });
        // The pane is conditionally mounted on `selectedTab === 2`
        // (pages/troubleshoot/index.jsx:326), so leaving the tab unmounts the listing outright.
        await expect(locators.listing).toHaveCount(0, { timeout: 30000 });
      });

      await test.step("Event Resolutions comes back with its listing and filters", async () => {
        await locators.gotoTab(TroubleshootTabs.eventResolutions);
        await expect(page).toHaveURL(new RegExp(`#${EVENT_RESOLUTIONS_FRAGMENT}`), { timeout: 30000 });
        await expect(locators.EventResolutionsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
        await expect(locators.listing).toBeVisible({ timeout: 60000 });
        await expect(locators.accountFilter).toBeVisible();
        await expectListingSettled(locators);
      });
    }
  );
});
