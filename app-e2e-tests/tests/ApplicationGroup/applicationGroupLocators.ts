// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Handles come straight from the components that render this module:
//   app/src/components/k8s/landing/k8sGrouping/KubernetesApplicationGrouping.jsx
//     ListingLayout id='k8s-grouping', DownloadButton id='k8s-grouping-download',
//     CustomTable id='k8sGrouping', SearchInput label='Search Grouping'
//   app/src/components/k8s/landing/k8sGrouping/KubernetesInsertApplicationGroupingModal.tsx
//     Input id='grouping-name' / id='short-description', Selects labelled Cluster / Namespaces
//   app/src/pages/grouping/index.jsx
//     TabsForDrilldown — Summary / Events / Applications / Monitoring
//
// None of these files renders a single data-testid (verified with
// `grep -c data-testid` over all five — every one returns 0), so rung 1 of the
// locator ladder does not exist here and role/accessible-name is the highest
// rung available. Where the app renders neither, a scoped id is used and said so.
const DATA_ROW = "tr:has(td:nth-child(2))";

// CustomTable puts the caller's id on the <table> and `${id}-body` on the <tbody>;
// with no rows it renders EmptyData in their place, which ids its heading
// `${id}-no-data` (app/src/components/common/EmptyData.jsx). The grouping table
// does not pass showUpdatedEmptyData, so it takes that id'd branch — unlike the
// VM tables, which fall into the fixed "All good here!" panel.
export const GROUPING_TABLE = "k8sGrouping";

export class ApplicationGroupLocators extends CommonLocators {
  // Listing (rendered as the Application Grouping tab of /dashboards).
  readonly listingRoot: Locator;
  readonly searchInput: Locator;
  readonly createGroupBtn: Locator;
  readonly downloadBtn: Locator;
  readonly table: Locator;
  readonly rows: Locator;
  readonly emptyState: Locator;
  readonly groupLinks: Locator;

  // Create / Update modal.
  readonly dialog: Locator;
  readonly dialogTitle: Locator;
  readonly nameInput: Locator;
  readonly nameError: Locator;
  readonly descriptionInput: Locator;
  readonly clusterSelect: Locator;
  readonly namespacesSelect: Locator;
  readonly selectedApplicationsLabel: Locator;
  readonly totalSelectedLabel: Locator;
  readonly dialogCancelBtn: Locator;
  readonly dialogCreateBtn: Locator;

  // Group detail page (/grouping?groupId=...).
  readonly summaryTab: Locator;
  readonly eventsTab: Locator;
  readonly applicationsTab: Locator;
  readonly editGroupBtn: Locator;

  constructor(page: Page) {
    super(page);

    // ListingLayout renders the id on a plain Box with no role of its own, so there
    // is no wider handle to degrade to — an unscoped fallback here would match the
    // whole page body.
    this.listingRoot = page.locator("#k8s-grouping");

    // This SearchInput callsite passes no `id`, so the placeholder (SearchInput
    // forwards `label` as the placeholder) is the highest rung available. Scoped to
    // the listing rather than given a `getByRole("textbox")` fallback, which would
    // resolve to the global account autocomplete in the header.
    this.searchInput = this.listingRoot.getByPlaceholder("Search Grouping");

    this.createGroupBtn = this.listingRoot
      .getByRole("button", { name: "Create Application Group" })
      .or(this.listingRoot.locator('button:has-text("Create Application Group")'))
      .first();
    // DownloadButton renders an icon-only control with no accessible name and no
    // testid, so this id is the only handle; a text or role fallback would match
    // every other icon button in the toolbar.
    this.downloadBtn = page.locator("#k8s-grouping-download");

    this.table = page.locator(`#${GROUPING_TABLE}`);
    this.rows = page.locator(`#${GROUPING_TABLE}-body ${DATA_ROW}`);
    this.emptyState = page.locator(`#${GROUPING_TABLE}-no-data`).or(this.listingRoot.getByRole("heading", { name: "No Data Available" })).first();
    this.groupLinks = page.locator(`#${GROUPING_TABLE}-body a[href^="/grouping?groupId="]`);

    // ds/Modal renders through MUI Dialog, so the paper carries role="dialog" and
    // the title sits on #alert-dialog-title. Filtered on that title node rather than
    // on hasText: the modal's own body contains "Grouping" in several places, so a
    // text filter would also be satisfied by any wrapper that happens to enclose it.
    this.dialog = page
      .getByRole("dialog")
      .filter({ has: page.locator("#alert-dialog-title", { hasText: /Grouping/ }) })
      .first();
    // ds/Modal hardcodes this id on its title node for aria-labelledby. Scoped to the
    // dialog and left without a fallback deliberately: a heading/text match would also
    // hit the "Details" and "Application Selection" section headers inside the body.
    this.dialogTitle = this.dialog.locator("#alert-dialog-title");

    // ds/Input forwards the caller's id to the <input> itself and pairs it with a
    // <label htmlFor>, so the accessible name is the visible label.
    this.nameInput = this.dialog.getByRole("textbox", { name: "Grouping Name" }).or(this.dialog.locator("#grouping-name")).first();
    this.descriptionInput = this.dialog.getByRole("textbox", { name: "Short Description" }).or(this.dialog.locator("#short-description")).first();
    // ds/Input ids its error span `${inputId}-error` and gives it role="alert".
    this.nameError = this.dialog.locator("#grouping-name-error").or(this.dialog.getByRole("alert")).first();

    this.clusterSelect = this.selectTriggerByLabel("Cluster");
    this.namespacesSelect = this.selectTriggerByLabel("Namespaces");

    // The two selection counters the modal keeps: what is picked, and the footer total.
    this.selectedApplicationsLabel = this.dialog.getByText(/Applications selected -/);
    this.totalSelectedLabel = this.dialog.getByText(/Total Application Selected/);

    // This modal supplies its own actionButtons, so ds/Modal's built-in #cancel /
    // #submit are never rendered — these DsButtons carry no id at all. Exact names
    // keep "Create" off the listing's "Create Application Group" button.
    this.dialogCancelBtn = this.dialog.getByRole("button", { name: "Cancel", exact: true }).first();
    this.dialogCreateBtn = this.dialog.getByRole("button", { name: "Create", exact: true }).first();

    // TabsForDrilldown -> ds Tabs -> MUI Tab, so each is role="tab". a11yProps ids
    // them `tab-${value}` and this page's tabOptions use 0..3, giving the fallback
    // its handle; scoped to the tablist because `#tab-0` is not unique app-wide.
    this.summaryTab = this.tabByName("Summary", 0);
    this.eventsTab = this.tabByName("Events", 1);
    this.applicationsTab = this.tabByName("Applications", 2);
    this.editGroupBtn = page
      .getByRole("button", { name: "Edit Application Group" })
      .or(page.locator('button:has-text("Edit Application Group")'))
      .first();
  }

  // ds/Select renders its trigger as a <button> whose id is a React useId value when
  // the callsite passes none — unusable from a test. Both Selects here are in that
  // state, so the primary is the accessible name the <label htmlFor> supplies, narrowed
  // with .and() to the element that actually carries aria-haspopup="listbox" — a
  // name-only match can otherwise resolve to the <label> that supplies the name. The
  // fallback walks from that same label to its sibling trigger (a labelled anchor, not
  // an index chain).
  private selectTriggerByLabel(label: string): Locator {
    const trigger = this.dialog.locator('button[aria-haspopup="listbox"]');
    return this.dialog
      .getByRole("button", { name: label })
      .and(trigger)
      .or(this.dialog.getByText(label, { exact: true }).locator('xpath=following-sibling::button[@aria-haspopup="listbox"][1]'))
      .first();
  }

  // Both branches are scoped to the tablist: role="tab" is page-wide otherwise, and
  // `#tab-${index}` is not unique app-wide either — a11yProps numbers each strip from
  // its own tabOptions, so several pages render a #tab-0.
  private tabByName(name: string, index: number): Locator {
    const tablist = this.page.getByRole("tablist");
    return tablist
      .getByRole("tab", { name })
      .or(tablist.locator(`#tab-${index}`))
      .first();
  }

  // A table either has rows or shows its empty panel in their place, never both, and
  // which one depends on what the shared tenant happens to hold. Every list assertion
  // goes through this so a legitimately empty tenant reads as a pass rather than a
  // locator that "did not appear". While `loading` is true CustomTable swaps in a
  // skeleton <tbody> carrying no id, so the id'd body being attached means the
  // response has landed.
  async waitForTableSettled(timeout = 60000): Promise<void> {
    await this.page.locator(`#${GROUPING_TABLE}-body`).or(this.emptyState).first().waitFor({ state: "attached", timeout });
  }

  // Only for a baseline taken before any interaction. After an action that refetches,
  // assert the end state with a retrying expect(rows).toHaveCount(n) instead — a plain
  // count races the in-flight request and reads the stale table.
  async rowCount(): Promise<number> {
    await this.waitForTableSettled();
    return this.rows.count();
  }

  // SearchInput commits on Enter (onEnterPress) — typing alone filters nothing.
  async searchAndApply(term: string): Promise<void> {
    await this.searchInput.click();
    await this.searchInput.fill(term);
    await this.searchInput.press("Enter");
  }

  // The X only renders while the field holds a value, so clearing goes through the
  // field itself and re-commits, matching SearchInput's onChange('') fast path.
  async clearSearch(): Promise<void> {
    await this.searchInput.click();
    await this.searchInput.fill("");
    await this.searchInput.press("Enter");
  }

  // The name is the anchor's own text; the cell also holds an "Account: <name>"
  // caption, so reading the whole cell would return both.
  rowLinkByName(name: string): Locator {
    return this.groupLinks.filter({ hasText: name }).first();
  }
}
