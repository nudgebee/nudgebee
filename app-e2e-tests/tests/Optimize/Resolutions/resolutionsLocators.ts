// Not for OSS
import { Page, Locator } from "@playwright/test";
import { OptimizeModuleLocators, RESOLUTIONS_TABLE } from "../optimizeModuleLocators";

// Optimize -> Resolutions, the tenant-level resolution listing at /optimise#resolutions
// (app/src/components/optimise-new/ResolutionsView.tsx). The toolbar filters, the listing
// and the table already live on OptimizeModuleLocators, so this class adds only what that
// base does not carry: the four status cards above the listing, the row-click detail panel
// (ResolutionDetailPanel.tsx) and the ?id= deep-link banner.
//
// Rung choices below were measured against what those two files actually render:
//   ResolutionsView.tsx 4 data-testid   ResolutionDetailPanel.tsx 3 data-testid
// So rung 1 exists for every container here and leads. The status cards additionally take
// role="button" from WidgetCard with an accessible name composed of the Stat label and its
// value, which is a real rung-2 fallback; the panel's close control has an aria-label.

// The status values ResolutionsView's STATUS_CARDS declares, paired with the card testid it
// derives from each (`resolutions-card-${status.toLowerCase()}`) and the label the Stat
// renders. The label is also what the accessible-name fallback matches on.
export const RESOLUTION_STATUSES = {
  success: { status: "Success", testId: "resolutions-card-success", label: "Success", rowText: "Success" },
  inProgress: { status: "InProgress", testId: "resolutions-card-inprogress", label: "In Progress", rowText: "In Progress" },
  failed: { status: "Failed", testId: "resolutions-card-failed", label: "Failed", rowText: "Failed" },
} as const;

export type ResolutionStatus = keyof typeof RESOLUTION_STATUSES;

// The Status column's position in RESOLUTION_HEADERS (Account, Recommendation, Severity,
// Status, ...) — 1-based, as nth-child counts.
export const STATUS_COLUMN = 4;

// The copy CustomTable's default empty branch renders under this table, and the link the
// deep-link banner offers back to the unscoped listing.
export const VIEW_ALL_LINK_TEXT = "View all resolutions";

export class ResolutionsLocators extends OptimizeModuleLocators {
  readonly resolutionsPage: Locator;

  readonly allResolutionsCard: Locator;
  readonly successCard: Locator;
  readonly inProgressCard: Locator;
  readonly failedCard: Locator;

  readonly resolutionsDownload: Locator;

  readonly detailPanel: Locator;
  readonly detailPanelClose: Locator;
  readonly detailPanelRetry: Locator;
  readonly detailAppliedChanges: Locator;
  readonly detailActionBar: Locator;

  readonly deepLinkBanner: Locator;
  readonly viewAllResolutionsLink: Locator;

  constructor(page: Page) {
    super(page);

    this.resolutionsPage = page.getByTestId("optimize-resolutions-page");

    this.allResolutionsCard = this.statusCardByTestId("resolutions-card-all", "All Resolutions");
    this.successCard = this.statusCard("success");
    this.inProgressCard = this.statusCard("inProgress");
    this.failedCard = this.statusCard("failed");

    // DownloadButton puts aria-label="Download" on the IconButton and forwards the id, so
    // role leads. Scoped to the listing because the Recommendations tab renders a download
    // control with the same accessible name and `.or()` resolves in document order.
    this.resolutionsDownload = this.resolutionsListing
      .getByRole("button", { name: "Download" })
      .or(page.locator(`#${RESOLUTIONS_TABLE}-download`))
      .first();

    this.detailPanel = page.getByTestId("resolution-detail-panel");

    // The close control is an IconButton carrying aria-label="Close" plus an id, so role
    // leads and the id backs it up. Scoped to the panel: CustomDrawer is not the only
    // dismissible surface on this page.
    this.detailPanelClose = this.detailPanel
      .getByRole("button", { name: "Close" })
      .or(page.locator("#resolution-panel-close"))
      .first();

    // Rendered only for a Failed resolution (ResolutionDetailPanel gates the action bar's
    // button on the status), so its absence on a Success row is a real state rather than a
    // broken locator. Never clicked — see the header comment on the spec.
    this.detailPanelRetry = this.detailPanel.getByRole("button", { name: /^Retry/ }).or(page.locator("#resolution-panel-retry")).first();

    this.detailAppliedChanges = page.getByTestId("resolution-applied-changes");
    this.detailActionBar = page.getByTestId("resolution-action-bar");

    // The ?id= banner is a plain Box with neither a testid nor a role, so its own copy is
    // the only handle; scoped to the page root so it cannot match a heading elsewhere. The
    // link beside it is a next/link anchor with visible text, which is rung 2.
    this.deepLinkBanner = this.resolutionsPage.getByText(/Showing resolutions for the recommendation from your notification/);
    this.viewAllResolutionsLink = this.resolutionsPage
      .getByRole("link", { name: VIEW_ALL_LINK_TEXT })
      .or(this.resolutionsPage.getByText(VIEW_ALL_LINK_TEXT, { exact: true }))
      .first();
  }

  // The Status cell of one listed row, by row index. Used to prove a status filter kept only
  // the rows it claims to, rather than trusting the row count alone.
  statusCellAt(index: number): Locator {
    return this.resolutionsRows.nth(index).locator(`td:nth-child(${STATUS_COLUMN})`);
  }

  cardFor(key: ResolutionStatus): Locator {
    switch (key) {
      case "success":
        return this.successCard;
      case "inProgress":
        return this.inProgressCard;
      case "failed":
        return this.failedCard;
    }
  }

  private statusCard(key: ResolutionStatus): Locator {
    const { testId, label } = RESOLUTION_STATUSES[key];
    return this.statusCardByTestId(testId, label);
  }

  // WidgetCard spreads its props onto the Box, so the testid, role="button" and aria-pressed
  // all reach the DOM. The accessible name is the Stat's label followed by its value, hence
  // the prefix match rather than an exact one. Scoped to the page root so the fallback
  // cannot escape onto another tab's cards.
  private statusCardByTestId(testId: string, label: string): Locator {
    return this.page
      .getByTestId(testId)
      .or(this.resolutionsPage.getByRole("button", { name: new RegExp(`^${label}`) }))
      .first();
  }
}
