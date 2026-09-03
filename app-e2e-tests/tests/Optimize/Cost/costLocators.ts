// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { OptimizeModuleLocators } from "../optimizeModuleLocators";
import { RESOURCE_COLUMN } from "./costConstants";

// Cost sub-tab of the tenant-level Optimize module (/optimise#cost).
//
// The tab is OptimizeNewPage.tsx mounted with NO lockedCategory, so it shares that
// page's toolbar, chip rows, flat table and empty states with the Configuration tab —
// every one of those locators is inherited from OptimizeModuleLocators rather than
// re-declared here. What is unique to this tab is what the locked tab withholds: the
// category summary card strip, the Savings filter, and the Sort by / Download controls
// that only exist while the per-resource list is showing (showConfigRollup is
// permanently false here, because no card on this strip selects Configuration).
//
// Rung choices below were measured, not assumed:
//   OptimizeNewPage.tsx  data-testid=6   RecommendationDetailPanel.tsx data-testid=1
//   ActionBar.tsx        data-testid=1   RowActions.tsx                data-testid=0
//   FilterDropdown.jsx   data-testid=0   CustomTablePagination.jsx     data-testid=0
// So the cards, the chip rows and the detail panel are reached by testid; the filter
// panel, the sort menu and the pagination footer are reached by the role or the id
// their own primitives render.

export class CostLocators extends OptimizeModuleLocators {
  readonly summaryCardSavings: Locator;
  readonly summaryCardAll: Locator;
  readonly detailPanel: Locator;
  readonly detailPanelClose: Locator;
  readonly openMenu: Locator;
  readonly paginationSummary: Locator;
  readonly nextPage: Locator;
  readonly previousPage: Locator;

  constructor(page: Page) {
    super(page);

    // WidgetCard forwards an id and no data-testid for the savings card, and the card
    // itself carries no role — so the id leads, and the fallback is its own label text
    // scoped to the page root rather than a page-wide match.
    this.summaryCardSavings = page
      .locator("#optimize-card-savings")
      .or(this.recommendationsRoot.getByText("Total Savings", { exact: true }))
      .first();

    // Testid-only, deliberately. These cards are role='button' but their accessible
    // name is computed from a Stat whose value is the live recommendation count, so a
    // role fallback would go stale the moment the tenant's numbers move.
    this.summaryCardAll = page.getByTestId("optimize-card-all");

    this.detailPanel = page.getByTestId("recommendation-detail-panel");

    // The close button carries aria-label="Close", so role leads; scoped to the panel
    // because "Close" also names the buttons on any modal the page can open.
    this.detailPanelClose = this.detailPanel
      .getByRole("button", { name: "Close" })
      .or(page.locator("#detail-panel-close"))
      .first();

    // ds/DropdownMenu portals its surface to the body (internal/Overlay.tsx renders
    // role='menu'), so the sort menu is not inside the toolbar it belongs to. Scoped to
    // the one visible menu rather than matched page-wide: the tab strip and the sidenav
    // flyout render their own popovers.
    this.openMenu = page.locator('[role="menu"]:visible');

    // CustomTablePagination.jsx renders this summary as bare Typography with no id and
    // no role, and only while the table has rows — so the copy is the handle, scoped to
    // the listing. .first() because getByText also resolves every ancestor whose
    // textContent matches, which would be a strict-mode violation.
    this.paginationSummary = this.recommendationsListing.getByText(/Showing\s+[\d,]+-[\d,]+\s+of\s+[\d,]+\s+results/).first();

    // MUI Pagination's own getAriaLabel, so role leads and there is no id to fall back
    // to. Scoped to the listing: the detail panel's history table paginates too.
    this.nextPage = this.recommendationsListing.getByRole("button", { name: "Go to next page" }).first();
    this.previousPage = this.recommendationsListing.getByRole("button", { name: "Go to previous page" }).first();
  }

  // One category summary card. Testid-only for the same reason as the All card above.
  categoryCard(testId: string): Locator {
    return this.page.getByTestId(testId);
  }

  // One safety filter chip. SAFETY_ORDER in OptimizeNewPage.tsx is already lowercase,
  // so the band is the testid suffix verbatim.
  safetyChip(band: string): Locator {
    return this.page.getByTestId(`safety-chip-${band}`);
  }

  // The same chip, but only when it can actually be clicked. ds/Chip renders its
  // interactive branch as a ButtonBase and its muted branch as a <span> with
  // pointerEvents:none — and only the interactive branch sets aria-disabled, so on a
  // muted chip that attribute is absent rather than "true". The button role is
  // therefore the only reliable read of whether the band is clickable.
  enabledSafetyChip(band: string): Locator {
    return this.safetyChip(band).and(this.page.getByRole("button"));
  }

  // A Sort by preset. ds/DropdownMenu gives each item role='menuitem' and the id
  // OptimizeNewPage passes, so the id is unambiguous — and getByRole("menuitem", {name})
  // is the form that matches nothing in this app, hence the hasText fallback scoped to
  // the open menu.
  sortOption(value: string, label: string): Locator {
    return this.page
      .locator(`[role="menuitem"]#optimize-sort-${value}:visible`)
      .or(this.openMenu.locator('[role="menuitem"]').filter({ hasText: label }))
      .first();
  }

  // The pagination button for the page currently on screen. MUI relabels the showing
  // page from "Go to page n" to "page n", so this name is the component's own statement
  // of which page landed — and `exact` is what keeps it off the other pages' buttons.
  currentPageButton(pageNumber: number): Locator {
    return this.recommendationsListing.getByRole("button", { name: `page ${pageNumber}`, exact: true }).first();
  }

  // The Resource cell of one row — both the cell whose text names the resource and the
  // cell that carries the row click through to the detail panel.
  rowResourceCell(row: Locator): Locator {
    return row.locator("td").nth(RESOURCE_COLUMN);
  }

  // Options are role='option' boxes inside the open MUI Popover (FilterDropdown.jsx:199).
  // Scoped by :visible rather than by the popover's MUI class: only one panel is ever
  // open and every other dropdown's options are unmounted. Same pattern the merged
  // admin/Audits suite runs on.
  visibleFilterOptions(): Locator {
    return this.page.locator('[role="option"]:visible');
  }

  filterOption(label: string): Locator {
    return this.visibleFilterOptions().filter({ hasText: label }).first();
  }

  // The panel's own option search. FilterDropdown renders it only above eight options,
  // so callers must treat its absence as an expected outcome rather than a failure.
  // Scoped to the popover root — the panel has no id or role of its own, and an
  // unscoped match would reach into any other open dropdown.
  filterPanelSearch(placeholder: string): Locator {
    return this.page.locator(".MuiPopover-root").getByPlaceholder(placeholder).first();
  }

  // A probe, not an assertion: a panel of eight options or fewer renders no search box
  // at all, so absence is a normal shape of the panel and must come back as false rather
  // than throw. The timeout is short because the box renders in the same pass as the
  // options the caller has already waited for — it covers the paint, not a fetch.
  async filterSearchOffered(placeholder: string, timeoutMs = 3000): Promise<boolean> {
    return this.filterPanelSearch(placeholder)
      .waitFor({ state: "visible", timeout: timeoutMs })
      .then(() => true)
      .catch(() => false);
  }

  // Rendered by both OptionsList and GroupedOptionsList when a search matches nothing
  // (FilterDropdown.jsx:362, 563).
  filterNoOptions(): Locator {
    return this.page.getByText("No results found", { exact: true }).first();
  }

  // Opens a toolbar filter and commits one option, coping with both panel shapes: a
  // list of eight or fewer renders no search box at all.
  async chooseFilterOption(trigger: Locator, label: string, searchPlaceholder?: string): Promise<void> {
    await trigger.click();

    // Wait on the panel's options, not on its search box: the box only exists above
    // eight options, so waiting for it would burn a full timeout on every short list
    // just to conclude it was never coming.
    await expect(this.visibleFilterOptions().first()).toBeVisible({ timeout: 30000 });

    if (searchPlaceholder && (await this.filterSearchOffered(searchPlaceholder))) {
      await this.filterPanelSearch(searchPlaceholder).fill(label);
    }

    await this.filterOption(label).click();
    // The trigger shows its label plus the committed value, so this is the signal that
    // the selection landed rather than a fixed pause.
    await expect(trigger).toContainText(label, { timeout: 30000 });
  }
}
