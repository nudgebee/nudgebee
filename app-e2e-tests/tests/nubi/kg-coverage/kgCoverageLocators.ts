// Not for OSS
import { Page, Locator } from "@playwright/test";
import { KgManualDependenciesLocators } from "../kg-manual-dependencies/kgManualDependenciesLocators";

// Nubi > b-Cortex > Knowledge Graph > Settings > Coverage (app/src/components/knowledge-graph/KGCoverageTab.jsx).
// The b-Cortex dialog, Knowledge Graph tab, Settings button, Settings dialog and its two tabs come from the parent class.
// `grep -c data-testid` returns 0 for KGCoverageTab.jsx and KGSettings.jsx, so nothing here is reachable at rung 1.

// KGCoverageTab's FLOW_SOURCES labels, in render order.
export const FLOW_SOURCE_LABELS = ["eBPF", "Traces", "Datadog APM", "New Relic APM"] as const;

function exactText(label: string): RegExp {
  return new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
}

export class KgCoverageLocators extends KgManualDependenciesLocators {
  readonly accountsColumn: Locator;
  readonly flowSourcesColumn: Locator;
  readonly accountSearchInput: Locator;
  readonly accountSearchClearBtn: Locator;
  readonly accountCheckboxes: Locator;
  readonly flowSourceCheckboxes: Locator;
  readonly accountsLoading: Locator;
  readonly noAccountsState: Locator;
  readonly noAccountsMatch: Locator;
  readonly coverageCancelBtn: Locator;
  readonly coverageSaveBtn: Locator;
  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // Single-rung on purpose: the heading is a column's only distinguishing mark, and every scoped locator below hangs off it.
    this.accountsColumn = this.settingsDialog.getByText("Cloud accounts", { exact: true }).locator("xpath=../..");
    this.flowSourcesColumn = this.settingsDialog.getByText("Flow sources", { exact: true }).locator("xpath=..");

    // SearchInput passes its label as the placeholder, which Chromium uses as the textbox's accessible name.
    this.accountSearchInput = this.accountsColumn
      .getByRole("textbox", { name: "Search accounts" })
      .or(this.accountsColumn.locator("#kg-coverage-account-search"))
      .first();
    // MUI's CloseIcon is aria-hidden despite its aria-label, so no role lookup reaches it; the attribute is the only handle.
    this.accountSearchClearBtn = this.accountsColumn.locator('[aria-label="clear search"]').first();

    // ds/Checkbox wraps a native checkbox in a <label>, so role + name works and checked state is native.
    this.accountCheckboxes = this.accountsColumn.getByRole("checkbox").or(this.accountsColumn.locator('input[type="checkbox"]'));
    this.flowSourceCheckboxes = this.flowSourcesColumn.getByRole("checkbox").or(this.flowSourcesColumn.locator('input[type="checkbox"]'));

    // Status copy has no role or id, so rung 4 scoped to the accounts column is the highest rung available.
    this.accountsLoading = this.accountsColumn.getByText("Loading…", { exact: true }).first();
    this.noAccountsState = this.accountsColumn.getByText("No active cloud accounts configured for this tenant.", { exact: true }).first();
    this.noAccountsMatch = this.accountsColumn.getByText(/^No accounts match/).first();

    // Manual Declarations is unmounted while Coverage shows, so these are the only Cancel and Save in the Settings dialog.
    this.coverageCancelBtn = this.settingsDialog
      .getByRole("button", { name: "Cancel", exact: true })
      .or(this.settingsDialog.locator("button").filter({ hasText: /^Cancel$/ }))
      .first();
    this.coverageSaveBtn = this.settingsDialog
      .getByRole("button", { name: "Save", exact: true })
      .or(this.settingsDialog.locator("button").filter({ hasText: /^Save$/ }))
      .first();

    // The snackbar root is aria-hidden while a dialog is open, so it is matched by attribute rather than role.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  flowSourceCheckbox(label: string): Locator {
    return this.flowSourcesColumn
      .getByRole("checkbox", { name: label, exact: true })
      .or(this.flowSourcesColumn.locator("label").filter({ hasText: exactText(label) }).locator('input[type="checkbox"]'))
      .first();
  }

  // Account labels are tenant data and need not be unique, hence `.first()`; the label wrapper is the fallback handle.
  accountCheckbox(label: string): Locator {
    return this.accountsColumn
      .getByRole("checkbox", { name: label, exact: true })
      .or(this.accountsColumn.locator("label").filter({ hasText: exactText(label) }).locator('input[type="checkbox"]'))
      .first();
  }

  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }
}
