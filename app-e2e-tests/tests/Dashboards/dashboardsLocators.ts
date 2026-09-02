import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

/**
 * Locators for the Dashboards module (`/dashboards`).
 *
 * Every selector below is read out of app/src, not guessed:
 *   - listing chrome + row actions  -> components/k8s/dashboards/CustomDashboards.tsx
 *   - dashboard editor / viewer     -> components/k8s/dashboards/DashboardView.tsx
 *   - import + template modals      -> ImportDashboardModal.tsx / TemplateGalleryModal.tsx
 *   - #cancel / #submit / #alert-dialog-title -> components/common/ds/Modal.tsx standard footer
 *   - sidenav ids                   -> components/common/layout/index.jsx menuItems
 */
export class DashboardsLocators extends CommonLocators {
  // Listing
  readonly listingRoot: Locator;
  readonly searchInput: Locator;
  // The toolbar offers ONE button — a DropdownMenu trigger — and the three ways
  // to start a dashboard are its items, mounted only while the menu is open.
  readonly newDashboardBtn: Locator;
  readonly newBlankDashboardBtn: Locator;
  readonly importDashboardBtn: Locator;
  readonly templateGalleryBtn: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly emptyStateHeading: Locator;

  // Sidenav
  readonly dashboardsNavBtn: Locator;
  readonly sidenavDashboardList: Locator;
  readonly sidenavDashboardGroups: Locator;
  readonly groupingRoot: Locator;

  // Dashboard view / editor
  readonly dashboardView: Locator;
  readonly dashboardToolbar: Locator;
  readonly titleInput: Locator;
  readonly descriptionInput: Locator;
  readonly saveBtn: Locator;
  readonly discardBtn: Locator;
  readonly backBtn: Locator;
  readonly addPanelBtn: Locator;

  // "Unsaved changes" prompt — its own footer, not ds/Modal's #cancel / #submit.
  readonly exitDialogCancelBtn: Locator;
  readonly exitDialogDiscardBtn: Locator;
  readonly exitDialogSaveBtn: Locator;

  // Shared ds/Modal chrome
  readonly dialog: Locator;
  readonly dialogTitle: Locator;
  readonly dialogCancelBtn: Locator;
  readonly dialogConfirmBtn: Locator;

  // Import modal
  readonly importCancelBtn: Locator;
  readonly importSubmitBtn: Locator;
  readonly importParseError: Locator;
  readonly importBlockedReason: Locator;
  readonly importJsonEditor: Locator;

  // Template gallery modal
  readonly templateRoleToggle: Locator;
  readonly templateGallery: Locator;
  readonly templateCards: Locator;
  readonly templateEmpty: Locator;
  readonly templateCancelBtn: Locator;

  // Toasts (SnackbarComponent renders the message as plain text)
  readonly createdToast: Locator;
  readonly deletedToast: Locator;
  readonly titleRequiredToast: Locator;

  constructor(page: Page) {
    super(page);

    this.listingRoot = page.locator("#custom-dashboards");
    this.searchInput = page.locator("#dashboard-search");
    this.newDashboardBtn = page.locator("#new-dashboard-btn");
    this.newBlankDashboardBtn = page.locator("#new-blank-dashboard-btn");
    this.importDashboardBtn = page.locator("#open-import-dashboard-btn");
    this.templateGalleryBtn = page.locator("#open-template-gallery-btn");
    // CustomTable puts `id={`${id}-body`}` on the DATA <tbody> only — the skeleton
    // body it renders while `loading` carries no id at all. Matching on the
    // presence of the attribute is therefore how "rows have arrived" is asserted,
    // and it survives the app later passing CustomTable a real `id`.
    this.tableBody = page.locator("#custom-dashboards tbody[id]");
    // CustomDashboards passes no `expandable`, so CustomTable emits exactly one
    // <tr> per dashboard — no drill-down row to filter out.
    this.rows = this.tableBody.locator("tr");
    this.emptyStateHeading = this.listingRoot.getByRole("heading", { name: "No Data Available" });

    this.dashboardsNavBtn = page.locator("#dashboards-sidenavbutton");
    this.sidenavDashboardList = page.locator("#sidenav-dashboards-list");
    this.sidenavDashboardGroups = page.locator("#sidenav-dashboards-groups");
    this.groupingRoot = page.locator("#k8s-grouping");

    this.dashboardView = page.locator("[data-testid='dashboard-view']");
    this.dashboardToolbar = page.locator("#dashboard-toolbar");
    this.titleInput = page.locator("#dashboard-title-input");
    this.descriptionInput = page.locator("#dashboard-description-input");
    this.saveBtn = page.locator("#dashboard-save-btn");
    this.discardBtn = page.locator("#dashboard-discard-btn");
    this.backBtn = page.locator("#dashboard-back-btn");
    this.addPanelBtn = page.locator("#dashboard-add-panel-btn");

    this.exitDialogCancelBtn = page.locator("#dashboard-exit-cancel-btn");
    this.exitDialogDiscardBtn = page.locator("#dashboard-exit-discard-btn");
    this.exitDialogSaveBtn = page.locator("#dashboard-exit-save-btn");

    this.dialog = page.getByRole("dialog");
    this.dialogTitle = page.locator("#alert-dialog-title");
    this.dialogCancelBtn = this.dialog.locator("#cancel");
    this.dialogConfirmBtn = this.dialog.locator("#submit");

    this.importCancelBtn = page.locator("#import-cancel-btn");
    this.importSubmitBtn = page.locator("#import-dashboard-btn");
    this.importParseError = page.locator("[data-testid='import-parse-error']");
    this.importBlockedReason = page.locator("[data-testid='import-blocked-reason']");
    // ds/CodeEditor wraps @uiw/react-codemirror and passes it no id, so the
    // editable surface is CodeMirror's own `.cm-content`. Same handle
    // tests/workflow/workflowlocators.ts already uses for this component.
    this.importJsonEditor = this.dialog.locator(".cm-content");

    // The gallery filters by ROLE, not by a search term: a ds/ToggleGroup whose
    // options are <button role="radio"> carrying the role's label.
    this.templateRoleToggle = page.locator("#template-role-toggle");
    this.templateGallery = page.locator("[data-testid='template-gallery']");
    this.templateCards = page.locator("[data-testid^='template-card-']");
    this.templateEmpty = page.locator("[data-testid='template-empty']");
    this.templateCancelBtn = page.locator("#template-cancel-btn");

    this.createdToast = page.getByText("Dashboard created", { exact: true });
    this.deletedToast = page.getByText("Dashboard deleted", { exact: true });
    this.titleRequiredToast = page.getByText("Give the dashboard a title.", { exact: true });
  }

  /**
   * Waits until the listing has settled on a real answer — either a data <tbody>
   * or the empty state — and returns the row count. Reading the count without
   * this returns whatever the skeleton happened to be showing.
   */
  async waitForListSettled(): Promise<number> {
    await expect(this.listingRoot).toBeVisible({ timeout: 60000 });
    await expect(async () => {
      const settled = (await this.tableBody.count()) + (await this.emptyStateHeading.count());
      expect(settled).toBeGreaterThan(0);
    }).toPass({ timeout: 60000, intervals: [250, 500, 1000] });
    return this.rows.count();
  }

  /**
   * Opens the toolbar's "New dashboard" menu and returns once its items are
   * mounted. The three authoring entry points are DropdownMenu items, so none of
   * them is in the DOM until the trigger has been clicked — clicking one
   * straight off the toolbar waits out its full timeout on an element that was
   * never going to appear.
   *
   * Retried as a whole: the menu closes on an outside click, and the click that
   * opens it can land while the listing is still swapping its skeleton for rows.
   */
  async openAuthoringMenu(): Promise<void> {
    await expect(async () => {
      // Only click when the menu is shut. An open MUI menu lays its own invisible
      // backdrop over the page, including over the trigger, so a second click on an
      // already-open menu lands on the backdrop and waits out its timeout.
      if (!(await this.newBlankDashboardBtn.isVisible().catch(() => false))) {
        await this.newDashboardBtn.click({ timeout: 5000 });
      }
      await this.newBlankDashboardBtn.waitFor({ state: "visible", timeout: 3000 });
    }).toPass({ timeout: 30000, intervals: [500, 1000, 2000] });
  }

  /** Opens the blank-dashboard editor from the authoring menu. */
  async startBlankDashboard(): Promise<void> {
    await this.openAuthoringMenu();
    await this.newBlankDashboardBtn.click();
  }

  /** Opens the Import dashboard modal from the authoring menu. */
  async openImportModal(): Promise<void> {
    await this.openAuthoringMenu();
    await this.importDashboardBtn.click();
  }

  /** Opens the template gallery from the authoring menu. */
  async openTemplateGallery(): Promise<void> {
    await this.openAuthoringMenu();
    await this.templateGalleryBtn.click();
  }

  /** One option of the gallery's role filter, by its visible label. */
  templateRoleOption(label: string): Locator {
    return this.templateRoleToggle.getByRole("radio", { name: label, exact: true });
  }

  /** Submits a search term. The field only feeds the request on Enter. */
  async search(term: string): Promise<void> {
    await this.searchInput.fill(term);
    await this.searchInput.press("Enter");
  }

  /**
   * Empties the search field. CustomDashboards resets the submitted term on an
   * empty onChange, so no Enter is needed — and pressing one would be a second
   * request for the same result.
   */
  async clearSearch(): Promise<void> {
    await this.searchInput.fill("");
  }

  rowByTitle(title: string): Locator {
    return this.rows.filter({ hasText: title });
  }

  deleteBtnForRow(title: string): Locator {
    return this.rowByTitle(title).locator("[id^='delete-dashboard-']");
  }

  openLinkForRow(title: string): Locator {
    return this.rowByTitle(title).locator("[data-testid^='open-dashboard-']");
  }
}
