// Not for OSS
import { Page, Locator } from "@playwright/test";
import { KgManualDependenciesLocators } from "../kg-manual-dependencies/kgManualDependenciesLocators";

// Nubi > b-Cortex > Knowledge Graph > Settings > Manual Declarations > Import JSON
// (app/src/components/knowledge-graph/JsonImportDialog.jsx). Everything up to the
// dialog is the Manual Declarations tab, so its locators are reused wholesale.

export class KgJsonImportLocators extends KgManualDependenciesLocators {
  readonly importDialog: Locator;
  readonly importDialogCloseBtn: Locator;
  readonly formatToggleBtn: Locator;
  readonly formatCodeBlock: Locator;
  readonly typeValuesPanel: Locator;
  readonly sampleDownloadBtn: Locator;
  readonly uploadBtn: Locator;
  readonly fileInput: Locator;
  readonly fileName: Locator;
  readonly jsonEditor: Locator;
  readonly jsonEditorPlaceholder: Locator;
  readonly importCancelBtn: Locator;
  readonly importBtn: Locator;
  readonly resultSummary: Locator;
  readonly resultRows: Locator;
  readonly doneBtn: Locator;
  readonly nodeDeleteMenuItem: Locator;
  readonly confirmNodeDeleteDialog: Locator;
  readonly confirmNodeDeleteBtn: Locator;

  constructor(page: Page) {
    super(page);
    // ds/Modal portals every dialog to <body> and stamps #alert-dialog-title on each,
    // so the import dialog is matched on its own title element, by attribute rather
    // than getByRole("dialog"): while it is open MUI aria-hides the dialogs under it.
    this.importDialog = page
      .locator('[role="dialog"]')
      .filter({ has: page.locator("#alert-dialog-title").filter({ hasText: /^Import Manual Declarations \(JSON\)$/ }) })
      .first();
    this.importDialogCloseBtn = this.importDialog
      .getByRole("button", { name: "Close", exact: true })
      .or(this.importDialog.locator("#close-modal-btn"))
      .first();

    // One button that reads "View format" while the panel is closed and "Hide format"
    // while it is open, so its label doubles as the panel's state.
    this.formatToggleBtn = this.importDialog
      .locator("#manual-declarations-format-toggle")
      .or(this.importDialog.locator("button").filter({ hasText: /^(View format|Hide format)$/ }))
      .first();
    this.formatCodeBlock = this.importDialog.getByText("Document format", { exact: true }).first();
    this.typeValuesPanel = this.importDialog.getByTestId("manual-nodes-type-values");
    this.sampleDownloadBtn = this.importDialog
      .locator("#manual-declarations-sample-download")
      .or(this.importDialog.getByRole("button", { name: "Download" }))
      .first();
    this.uploadBtn = this.importDialog.getByTestId("manual-declarations-upload-btn");
    this.fileInput = this.importDialog.getByTestId("manual-declarations-file-input");
    this.fileName = this.importDialog.getByTestId("manual-declarations-file-name");
    // ds/CodeEditor is CodeMirror; its editable surface is `.cm-content`, inside the
    // wrapper that carries the testid.
    this.jsonEditor = this.importDialog.getByTestId("manual-declarations-json-editor").locator(".cm-content");
    // CodeMirror renders its placeholder inside .cm-content, and only while the
    // document is empty, so the editor's text is never "" — this is the empty signal.
    this.jsonEditorPlaceholder = this.jsonEditor.locator(".cm-placeholder");

    this.importCancelBtn = this.importDialog
      .getByRole("button", { name: "Cancel", exact: true })
      .or(this.importDialog.locator("button").filter({ hasText: /^Cancel$/ }))
      .first();
    // "Import" exact, so the tab toolbar's "Import JSON" underneath can never match.
    this.importBtn = this.importDialog
      .getByRole("button", { name: "Import", exact: true })
      .or(this.importDialog.locator("button").filter({ hasText: /^Import$/ }))
      .first();
    this.resultSummary = this.importDialog.getByTestId("manual-declarations-result-summary");
    this.resultRows = this.importDialog.getByTestId("manual-declarations-result-row");
    this.doneBtn = this.importDialog
      .getByRole("button", { name: "Done", exact: true })
      .or(this.importDialog.locator("button").filter({ hasText: /^Done$/ }))
      .first();

    // The Nodes section's row menu shares ThreeDotsMenu's item ids with the
    // Dependencies section; only the open menu's items are visible.
    this.nodeDeleteMenuItem = page
      .locator('[role="menuitem"]#delete:visible')
      .or(page.locator('[role="menuitem"]:visible').filter({ hasText: /^Delete$/ }))
      .first();
    this.confirmNodeDeleteDialog = page
      .locator('[role="dialog"]')
      .filter({ has: page.locator("#alert-dialog-title").filter({ hasText: /^Delete manual node\?$/ }) })
      .first();
    this.confirmNodeDeleteBtn = this.confirmNodeDeleteDialog
      .getByRole("button", { name: "Delete", exact: true })
      .or(this.confirmNodeDeleteDialog.locator("button").filter({ hasText: /^Delete$/ }))
      .first();
  }

  // Result rows by outcome; each carries data-outcome created / existing / rejected.
  resultRowsWith(outcome: "created" | "existing" | "rejected"): Locator {
    return this.resultRows.and(this.page.locator(`[data-outcome="${outcome}"]`));
  }

  nodeRow(name: string): Locator {
    return this.nodesTableBody.getByRole("row").or(this.nodesTableBody.locator("tr")).filter({ hasText: name }).first();
  }

  nodeRowMenuBtn(name: string): Locator {
    const row = this.nodeRow(name);
    return row.getByRole("button", { name: "More actions" }).or(row.locator("#three-dot-menu")).first();
  }

  // SnackbarComponent renders an error toast as role="alert" in a region labelled
  // "Notifications"; matched by attribute because MUI aria-hides the app root
  // while a dialog is open, and every toast this suite reads fires over the dialog.
  errorToast(text: string | RegExp): Locator {
    return this.page
      .locator('[aria-label="Notifications"] [role="alert"]')
      .filter({ hasText: text })
      .or(this.page.locator('[aria-label="Notifications"]').getByText(text))
      .first();
  }
}
