// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Escapes a literal string for use inside a RegExp.
function esc(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Automations tab of /automation — app/src/components/workflow/WorkflowListing.tsx.
//
// That component renders exactly one data-testid, on a delete warning none of these
// tests reach; every toolbar control, filter and row action carries an id and no
// testid, so rung 3 of the ladder is the highest one available for them. Controls
// that do expose an accessible name are reached by role first, id second.
export class AutomationsLocators extends CommonLocators {
  readonly automationSidenavBtn: Locator;
  readonly automationsTab: Locator;
  readonly executionsTab: Locator;

  readonly listingBox: Locator;
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly nameLinks: Locator;
  readonly emptyState: Locator;
  readonly rowRangeSummary: Locator;

  readonly nameSearch: Locator;
  readonly tagsSearch: Locator;
  readonly accountFilter: Locator;
  readonly statusFilter: Locator;
  readonly lastExecutionStatusFilter: Locator;
  readonly triggerTypeFilter: Locator;

  readonly refreshBtn: Locator;
  readonly configsBtn: Locator;
  readonly createBtn: Locator;

  readonly deleteModalConfirmBtn: Locator;
  readonly deleteModalCancelBtn: Locator;

  readonly configsModal: Locator;
  readonly configAccountSelect: Locator;
  readonly configOverlaySearch: Locator;
  readonly addConfigBtn: Locator;
  readonly configFormModal: Locator;
  readonly configKeyInput: Locator;
  readonly configValueInput: Locator;
  readonly configMetadataInput: Locator;
  readonly configKeyError: Locator;
  readonly configValueError: Locator;
  readonly configMetadataError: Locator;
  readonly saveConfigBtn: Locator;
  readonly cancelConfigBtn: Locator;

  readonly executionsRoot: Locator;
  readonly builderBackBtn: Locator;
  readonly exitConfirmDialog: Locator;
  readonly exitLeaveBtn: Locator;

  readonly toastRegion: Locator;

  constructor(page: Page) {
    super(page);

    // Id first: the rail renders the module name as an aria-label, but the hover
    // flyout repeats "Automations" as a sub-item, so role+name is ambiguous here.
    this.automationSidenavBtn = page.locator("#auto-pilot-sidenavbutton").or(page.locator('a[href="/automation"]')).first();
    this.automationsTab = page.locator("#anchor-tab-automations").or(page.locator('a[href="/automation#automations"]')).first();
    this.executionsTab = page.locator("#anchor-tab-executions").or(page.locator('a[href="/automation#executions"]')).first();

    // Deliberately id-only: the listing is an unlabelled ListingLayout box, and
    // every widened match (a bare div, the page body) would scope the toolbar
    // locators below to the whole page instead of to the listing.
    this.listingBox = page.locator("#workflow-listing-box");
    this.table = page.locator("#workflows-table").or(this.listingBox.getByRole("table")).first();
    this.tableBody = page.locator("#workflows-table-body").or(this.table.locator("tbody")).first();
    // One per data row, and absent from the loading skeleton — so this is the
    // signal for "the listing has rendered rows", not the <tr> count. No fallback
    // on purpose: any wider match (every link, every row cell) would also count
    // rows the skeleton renders and make the row assertions read high.
    this.nameLinks = page.locator('[id^="workflow-name-link-"]');
    this.emptyState = page
      .locator("#workflows-table-no-data")
      .or(this.listingBox.getByRole("heading", { name: "No Data Available" }))
      .first();
    // The "Showing a-b of n results" line. Absent at zero rows: CustomTable's
    // renderPaginationOrViewAll returns null then, so CustomTablePagination's
    // own "No results found" branch is unreachable from this table.
    // No fallback: the text is the only thing identifying this line, so a wider
    // match would return the surrounding toolbar instead.
    this.rowRangeSummary = this.listingBox.getByText(/No results found|Showing\s+[\d,]+-[\d,]+\s+of\s+[\d,]+\s+results/).first();

    this.nameSearch = page.locator("#workflow-name-search").or(this.listingBox.getByPlaceholder("Search by Automation Name")).first();
    this.tagsSearch = page.locator("#workflow-tags-search").or(this.listingBox.getByPlaceholder("Search by Tags")).first();

    // ds/FilterDropdown renders its trigger as `auto-complete-<kebab of the id
    // prop>`, not the id the call site passes — see FilterDropdown.jsx's inputId.
    this.accountFilter = this.filterTrigger("workflow-filter-account", "Account");
    this.statusFilter = this.filterTrigger("workflow-filter-status", "Status");
    this.lastExecutionStatusFilter = this.filterTrigger("workflow-filter-last-exec-status", "Last Exec. Status");
    this.triggerTypeFilter = this.filterTrigger("workflow-filter-trigger-type", "Trigger Type");

    this.refreshBtn = this.listingBox.getByRole("button", { name: "Refresh" }).or(page.locator("#workflow-listing-refresh-btn")).first();
    this.configsBtn = this.listingBox.getByRole("button", { name: "Configs" }).or(page.locator("#workflow-listing-configs-btn")).first();
    this.createBtn = this.listingBox.getByRole("button", { name: "Create Automation" }).or(page.locator("#workflow-listing-create-btn")).first();

    this.deleteModalConfirmBtn = page
      .locator("#workflow-delete-confirm-btn")
      .or(page.getByRole("button", { name: "Delete", exact: true }))
      .first();
    // Falls back to CommonLocators' shared Cancel rather than redeclaring it.
    this.deleteModalCancelBtn = page.locator("#workflow-delete-cancel-btn").or(this.cancelBtn).first();

    // ds/Modal points aria-labelledby at its title, so each dialog's accessible
    // name is its heading — rung 2, and unlike #alert-dialog-title it is unique.
    // No fallback on either: #alert-dialog-title is shared by every modal on the
    // page, so widening would match whichever dialog happens to be mounted.
    this.configsModal = page.getByRole("dialog", {
      name: "Automation Configurations",
    });
    this.configFormModal = page.getByRole("dialog", {
      name: "Add New Configuration",
    });
    this.configAccountSelect = page
      .locator("#config-account-select")
      .or(this.configsModal.getByRole("button", { name: /Account/ }))
      .first();
    // ds/Select shows its own search box inside the portaled listbox.
    this.configOverlaySearch = page
      .getByRole("listbox")
      .getByPlaceholder(/Search/)
      .first();
    this.addConfigBtn = this.configsModal.getByRole("button", { name: "Add Config" }).or(page.locator("#add-config-btn")).first();
    this.configKeyInput = page.locator("#config-key").or(this.configFormModal.getByPlaceholder("Enter configuration key")).first();
    this.configValueInput = page.locator("#config-value").or(this.configFormModal.getByPlaceholder("Enter configuration value")).first();
    this.configMetadataInput = page
      .locator("#config-metadata")
      .or(this.configFormModal.getByPlaceholder(/^\{"key"/))
      .first();
    // ds/Input renders its validation message as `<id>-error` with role=alert.
    this.configKeyError = page
      .locator("#config-key-error")
      .or(this.configFormModal.getByRole("alert").filter({ hasText: "Key is required" }))
      .first();
    this.configValueError = page
      .locator("#config-value-error")
      .or(this.configFormModal.getByRole("alert").filter({ hasText: "Value is required" }))
      .first();
    this.configMetadataError = page
      .locator("#config-metadata-error")
      .or(this.configFormModal.getByRole("alert").filter({ hasText: "Invalid JSON format" }))
      .first();
    this.saveConfigBtn = this.configFormModal.getByRole("button", { name: "Save Configuration" }).or(page.locator("#save-config")).first();
    this.cancelConfigBtn = page
      .locator("#cancel-config")
      .or(this.configFormModal.getByRole("button", { name: "Cancel" }))
      .first();

    this.executionsRoot = page.locator("#execution-dashboard").or(page.locator("#execution-dashboard-table")).first();
    this.builderBackBtn = page.getByRole("button", { name: "Go back" }).or(page.locator("#workflow-back-btn")).first();
    // useUnsavedChangesTracking aborts routeChangeStart and opens this when the
    // automation has unsaved canvas edits ("Unsaved changes") or a saved draft
    // ahead of the live version ("Unpublished changes") — both are reachable on
    // an automation the test never edited, so leaving has to handle either.
    this.exitConfirmDialog = page.getByRole("dialog", { name: /^(Unsaved|Unpublished) changes$/ });
    this.exitLeaveBtn = this.exitConfirmDialog.getByRole("button", { name: "Leave page" }).or(page.locator("#workflow-unsaved-leave-btn")).first();

    // SnackbarComponent renders its stack as a labelled region; items inside are
    // role=status (success) or role=alert (error). No fallback: this is asserted
    // with toHaveCount(0), and a wider match would make absence unprovable.
    this.toastRegion = page.getByRole("region", { name: "Notifications" });
  }

  // The trigger is a <button> whose text is "<label><selected value>", so an exact
  // role+name match is not stable — the id is, and the fallback narrows by label.
  private filterTrigger(id: string, label: string): Locator {
    return this.page
      .locator(`#auto-complete-${id}`)
      .or(this.page.locator("#workflow-listing-box").getByRole("button", { name: new RegExp(esc(label)) }))
      .first();
  }

  // The dropdown panel is portaled to body level, so options are page-scoped.
  filterOption(label: string): Locator {
    return this.page
      .getByRole("option", { name: label, exact: true })
      .or(this.page.locator('[role="option"]').filter({ hasText: new RegExp(`^${esc(label)}$`) }))
      .first();
  }

  // Matches on the automation's exact name so "Copy of X" never also selects "X".
  nameLink(name: string): Locator {
    return this.nameLinks.filter({ hasText: new RegExp(`^${esc(name)}$`) });
  }

  rowFor(name: string): Locator {
    return this.nameLink(name).first().locator("xpath=ancestor::tr[1]");
  }

  rowMenuTrigger(row: Locator): Locator {
    return row
      .locator('[id^="workflow-menu-"]')
      .or(row.getByRole("button", { name: "More actions" }))
      .first();
  }

  // DropdownMenu keeps every row's menu mounted (keepMounted), so `#delete` exists
  // once per row and only the open one is visible — hence the `:visible` filter on
  // both rungs rather than a row-scoped match.
  openMenuItem(itemId: string, label: string): Locator {
    return this.page
      .locator(`[role="menuitem"]#${itemId}:visible`)
      .or(this.page.locator('[role="menuitem"]:visible').filter({ hasText: new RegExp(`^${esc(label)}$`) }))
      .first();
  }

  // Trimmed, never matched with an anchored regex: CustomTable renders each header
  // as `capitalize(name){' '}` plus an empty secondary-text span, so the cell's text
  // is "Name " and a /^Name$/ hasText filter matches nothing.
  async headerNames(): Promise<string[]> {
    const headers = await this.table.locator("thead th").allInnerTexts();
    return headers.map((text) => text.trim()).filter((text) => text !== "");
  }

  // CustomTable renders no per-column attribute on its cells, so a column can only
  // be addressed by position. The position is resolved from the rendered header row
  // instead of hardcoded, so a column inserted upstream moves the read with it
  // rather than silently returning the neighbouring column's text.
  async columnIndex(headerName: string): Promise<number> {
    const headers = await this.table.locator("thead th").allInnerTexts();
    const index = headers.findIndex((text) => text.trim() === headerName);
    if (index < 0) {
      throw new Error(`Column "${headerName}" is not in the automations table header: [${headers.map((h) => h.trim()).join(", ")}]`);
    }
    return index + 1;
  }

  columnCellsAt(index: number): Locator {
    return this.tableBody.locator(`tr td:nth-child(${index})`);
  }

  toast(pattern: RegExp): Locator {
    return this.toastRegion.getByText(pattern).first();
  }
}
