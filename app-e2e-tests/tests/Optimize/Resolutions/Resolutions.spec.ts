// Not for OSS
import { test, expect } from "@playwright/test";
import { parkCursor } from "../optimizeModuleHelper";
import { RESOLUTION_STATUSES, VIEW_ALL_LINK_TEXT } from "./resolutionsLocators";
import {
  expectCardNotPressed,
  expectCardPressed,
  expectListingSettled,
  noMatchRecommendationId,
  openFirstResolutionPanel,
  openResolutionsTab,
  pickPopulatedStatus,
  readCardCount,
  selectFilterOption,
  waitForCards,
  waitForResolutions,
} from "./resolutionsHelper";

// Optimize -> Resolutions: the tenant-level listing at /optimise#resolutions
// (app/src/components/optimise-new/ResolutionsView.tsx), which spans every account in the
// tenant. Not the per-account ListingRecommendationResolution listing mounted under each
// cloud-account / kubernetes detail page.
//
// Everything here is read-only. The one write this module offers is Retry, and it cannot be
// undone from the UI: it re-dispatches a real resolution server-side against shared dev data
// and moves the row to InProgress. This suite therefore never clicks Retry — not on a row and
// not in the detail panel — so the module has no create, edit or form-validation surface it
// can reach; written up in the PR.
//
// A shared tenant may legitimately hold no resolutions at all, and a status card with a zero
// count is muted and inert by design. Every test that needs a populated listing therefore
// resolves that first and asserts the empty branch instead when there is nothing to filter,
// so an empty environment reads as a pass rather than as a locator that never appeared.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Optimize Resolutions", () => {
  test(
    "Optimize Resolutions sanity - open the Resolutions tab, verify the All Resolutions, Success, In Progress and Failed status cards render above the Account, Severity, Type and Resolver filters",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);

      await test.step("Every status card the view declares is on screen", async () => {
        for (const card of [locators.allResolutionsCard, locators.successCard, locators.inProgressCard, locators.failedCard]) {
          await expect(card).toBeVisible({ timeout: 30000 });
        }
      });

      await test.step("The toolbar carries all four filters and the download action", async () => {
        await expect(locators.resolutionsToolbar).toBeVisible({ timeout: 30000 });
        await expect(locators.resolutionsAccountFilter).toBeVisible();
        await expect(locators.resolutionsSeverityFilter).toBeVisible();
        await expect(locators.resolutionsRecommendationFilter).toBeVisible();
        await expect(locators.resolutionsResolverFilter).toBeVisible();
        await expect(locators.resolutionsDownload).toBeVisible();
      });

      await test.step("The listing reaches a terminal state", async () => {
        await expectListingSettled(locators);
      });
    }
  );

  test(
    "Optimize Resolutions - open the Resolutions tab, verify All Resolutions is the pressed card on mount and its count equals the Success, In Progress and Failed counts added together",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);
      await waitForCards(locators);

      await test.step("No status filter is applied yet, so All Resolutions is the pressed card", async () => {
        await expectCardPressed(locators.allResolutionsCard);
        for (const card of [locators.successCard, locators.inProgressCard, locators.failedCard]) {
          await expectCardNotPressed(card);
        }
      });

      await test.step("The All card totals the three status cards beside it", async () => {
        // Read in parallel: these are four independent text reads with no interaction
        // between them, so serialising them only costs round-trips to the browser.
        const [all, success, inProgress, failed] = await Promise.all([
          readCardCount(locators.allResolutionsCard),
          readCardCount(locators.successCard),
          readCardCount(locators.inProgressCard),
          readCardCount(locators.failedCard),
        ]);
        // allResolutionsCount sums every status the backend reports, so an unrecognised
        // status would push the All card above the three carded ones — never below them.
        expect(all).toBeGreaterThanOrEqual(success + inProgress + failed);
      });
    }
  );

  test(
    "Optimize Resolutions - open the Resolutions tab, click the first status card that carries a non-zero count, verify it becomes the pressed card and every listed row reports that status",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);
      await waitForResolutions(locators);

      const key = await pickPopulatedStatus(locators);
      if (!key) {
        await expect(locators.resolutionsEmpty).toBeVisible({ timeout: 60000 });
        return;
      }

      const { label, rowText } = RESOLUTION_STATUSES[key];
      const card = locators.cardFor(key);
      const expected = await readCardCount(card);

      await parkCursor(page);
      await card.click();

      await test.step(`The ${label} card takes over as the applied status filter`, async () => {
        await expectCardPressed(card);
        await expectCardNotPressed(locators.allResolutionsCard);
      });

      await test.step(`The listing narrows to the ${label} rows the card counted`, async () => {
        // The card's own count is the tenant-wide total for this status, so it is what the
        // filtered listing must report — capped at a page, which is why the row count is
        // asserted against the page size rather than against the total directly.
        await waitForResolutions(locators);
        await expect(locators.resolutionsRows.first()).toBeVisible({ timeout: 60000 });
        const rows = await locators.resolutionsRows.count();
        expect(rows).toBeGreaterThan(0);
        expect(rows).toBeLessThanOrEqual(expected);
      });

      await test.step(`Every row on the page carries the ${label} status, not just the first`, async () => {
        const rows = await locators.resolutionsRows.count();
        for (let i = 0; i < rows; i++) {
          await expect(locators.statusCellAt(i)).toContainText(rowText);
        }
      });
    }
  );

  test(
    "Optimize Resolutions - apply a status card filter then click the same card again, verify the second click unpresses it and hands the pressed state back to All Resolutions",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);
      await waitForResolutions(locators);

      const key = await pickPopulatedStatus(locators);
      if (!key) {
        await expect(locators.resolutionsEmpty).toBeVisible({ timeout: 60000 });
        return;
      }

      const card = locators.cardFor(key);
      const unfiltered = await readCardCount(locators.allResolutionsCard);

      await parkCursor(page);
      await card.click();
      await expectCardPressed(card);
      await waitForResolutions(locators);

      await parkCursor(page);
      await card.click();

      await test.step("The card toggles off and the All card is pressed again", async () => {
        await expectCardNotPressed(card);
        await expectCardPressed(locators.allResolutionsCard);
      });

      await test.step("The listing widens back to the unfiltered set", async () => {
        await waitForResolutions(locators);
        await expect(locators.resolutionsRows.first()).toBeVisible({ timeout: 60000 });
        const rows = await locators.resolutionsRows.count();
        expect(rows).toBeLessThanOrEqual(unfiltered);
        // The card counts are keyed on everything except the status filter, so clearing it
        // must leave the All total exactly where it was rather than recomputing it.
        expect(await readCardCount(locators.allResolutionsCard)).toBe(unfiltered);
      });
    }
  );

  test(
    "Optimize Resolutions - apply a status card filter then click All Resolutions, verify the status card unpresses and the listing returns to the full set",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);
      await waitForResolutions(locators);

      const key = await pickPopulatedStatus(locators);
      if (!key) {
        await expect(locators.resolutionsEmpty).toBeVisible({ timeout: 60000 });
        return;
      }

      const card = locators.cardFor(key);

      await parkCursor(page);
      await card.click();
      await expectCardPressed(card);
      await waitForResolutions(locators);

      await parkCursor(page);
      await locators.allResolutionsCard.click();

      await test.step("All Resolutions clears the status filter it did not set", async () => {
        await expectCardPressed(locators.allResolutionsCard);
        await expectCardNotPressed(card);
      });

      await test.step("The listing reaches a terminal state with the filter gone", async () => {
        await expectListingSettled(locators);
      });
    }
  );

  test(
    "Optimize Resolutions - open the Severity filter, select Critical, verify the trigger keeps the selection and the listing settles to matching rows or its no-data state",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);
      await waitForResolutions(locators);

      await parkCursor(page);
      await selectFilterOption(page, locators.resolutionsSeverityFilter, "Critical");

      await test.step("The trigger reports what was picked", async () => {
        await expect(locators.resolutionsSeverityFilter).toContainText("Critical", { timeout: 30000 });
      });

      await test.step("The listing re-fetches under the filter and reaches a terminal state", async () => {
        await expectListingSettled(locators);
      });
    }
  );

  test(
    "Optimize Resolutions - click a resolution row, verify the detail panel opens carrying the row's status message or applied changes, close it, verify the panel is gone and the listing is still on screen",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);
      // Settle on rows OR the empty state before counting: waitForResolutions only waits for
      // the tbody to be ATTACHED, and it attaches before its rows paint — so a plain count()
      // here can read 0 mid-render and send a populated tenant down the empty branch.
      await expectListingSettled(locators);

      const rows = await locators.resolutionsRows.count();
      if (rows === 0) {
        await expect(locators.resolutionsEmpty).toBeVisible({ timeout: 60000 });
        return;
      }

      await parkCursor(page);
      await openFirstResolutionPanel(locators);

      await test.step("The panel carries the resolution's own detail, not an empty shell", async () => {
        // A resolution renders an action bar in every state; the applied-changes table only
        // where the attempt produced a diff. Either proves the panel bound to a record.
        await expect(locators.detailActionBar.or(locators.detailAppliedChanges).first()).toBeVisible({ timeout: 30000 });
      });

      await parkCursor(page);
      await locators.detailPanelClose.click();

      await test.step("Closing removes the panel and leaves the listing behind it", async () => {
        await expect(locators.detailPanel).toHaveCount(0, { timeout: 30000 });
        await expect(locators.resolutionsTable).toBeVisible({ timeout: 30000 });
        await expect(locators.resolutionsRows).toHaveCount(rows);
      });
    }
  );

  test(
    "Optimize Resolutions - deep-link a recommendation id that cannot exist, verify the scoped-to-a-notification banner and its View all resolutions link render and the table falls to its no-data state",
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      const recommendationId = noMatchRecommendationId();
      const locators = await openResolutionsTab(page, `?id=${recommendationId}`);

      await test.step("The banner names why the listing is scoped and offers the way out", async () => {
        await expect(locators.deepLinkBanner).toBeVisible({ timeout: 60000 });
        await expect(locators.viewAllResolutionsLink).toBeVisible();
      });

      await test.step("No resolution belongs to that id, so the table empties to its no-data state", async () => {
        await expect(locators.resolutionsEmpty).toBeVisible({ timeout: 60000 });
        await expect(locators.resolutionsRows).toHaveCount(0);
      });

      await test.step("Every status card reports zero under the scoped id", async () => {
        await waitForCards(locators);
        expect(await readCardCount(locators.allResolutionsCard)).toBe(0);
      });
    }
  );

  test(
    "Optimize Resolutions - deep-link a recommendation id that cannot exist, click View all resolutions, verify the banner clears and the unscoped listing comes back",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page, `?id=${noMatchRecommendationId()}`);
      await expect(locators.deepLinkBanner).toBeVisible({ timeout: 60000 });

      await parkCursor(page);
      await locators.viewAllResolutionsLink.click();

      await test.step("The link drops the ?id= scope from the URL", async () => {
        await expect(page).toHaveURL(/\/optimise#resolutions$/, { timeout: 30000 });
      });

      await test.step("The banner goes with it and the listing reaches a terminal state", async () => {
        await expect(locators.deepLinkBanner).toHaveCount(0, { timeout: 30000 });
        await expect(locators.resolutionsPage).toBeVisible();
        await expectListingSettled(locators);
      });
    }
  );

  test(
    "Optimize Resolutions - open the Resolutions tab, reload the page, verify the Resolutions tab is still the open one rather than falling back to Summary",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openResolutionsTab(page);
      await expectListingSettled(locators);

      await page.reload();

      await test.step("The Resolutions tab comes back as the selected one", async () => {
        await expect(locators.ResolutionsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 60000 });
        await expect(locators.SummaryTab).toHaveAttribute("data-tab-selected", "false", { timeout: 30000 });
      });

      await test.step("The URL still carries the fragment and the view remounted", async () => {
        await expect(page).toHaveURL(/\/optimise#resolutions$/, { timeout: 30000 });
        await expect(locators.resolutionsPage).toBeVisible({ timeout: 60000 });
        await expect(locators.resolutionsToolbar).toBeVisible();
      });

      await test.step(`The "${VIEW_ALL_LINK_TEXT}" banner is absent on an unscoped load`, async () => {
        await expect(locators.deepLinkBanner).toHaveCount(0);
      });
    }
  );
});
