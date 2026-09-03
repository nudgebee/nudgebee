// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > Settings > Egress Filter (app/src/ee/components/egress-filter/EgressFilterTab.tsx).
// The tab renders six data-testids — the excluded-agents empty note, the PII
// category chips, the per-row pattern edit/delete pair and the pattern dialog's
// save button — so those are the primary wherever they apply. Everything else is
// a ds primitive given an explicit id: ds/Switch only carries an accessible name
// when it is passed a `label`, and ds/Select's trigger is a <button> whose
// accessible name is the currently selected option, which is tenant data. Both
// of those are rung 3 by necessity, not by preference.
export class EgressFilterLocators extends CommonLocators {
  readonly egressFilterTab: Locator;
  readonly agentsTab: Locator;
  readonly settingsPaper: Locator;
  readonly secretFilterToggle: Locator;
  readonly modeSelect: Locator;
  readonly inheritingDefaultText: Locator;
  readonly agentInput: Locator;
  readonly agentAddBtn: Locator;
  readonly agentsEmptyNote: Locator;
  readonly newPatternBtn: Locator;
  readonly patternsTable: Locator;
  readonly patternsTableBody: Locator;
  readonly noPatternsNote: Locator;
  readonly piiToggle: Locator;
  readonly piiModeSelect: Locator;
  readonly piiNerSelect: Locator;
  readonly saveChangesBtn: Locator;
  readonly resetDefaultsBtn: Locator;
  readonly patternDialog: Locator;
  readonly newPatternDialog: Locator;
  readonly editPatternDialog: Locator;
  readonly patternNameInput: Locator;
  readonly patternRegexInput: Locator;
  readonly patternEnabledSwitch: Locator;
  readonly patternSaveBtn: Locator;
  readonly patternCancelBtn: Locator;
  readonly patternNameError: Locator;
  readonly patternRegexError: Locator;
  readonly deletePatternDialog: Locator;
  readonly confirmDeleteBtn: Locator;

  constructor(page: Page) {
    super(page);

    // The Nubi panel entry point and its Settings button are not redeclared —
    // tests/nubi/nubiLocators.ts owns them and openEgressFilterTab() drives that
    // class to reach the modal.
    this.egressFilterTab = page.getByRole("tab", { name: "Egress Filter" });
    this.agentsTab = page.getByRole("tab", { name: "Agents" });

    // The tab body carries no id, so the Settings dialog's own MUI paper is the
    // container every scoped fallback below hangs off. Matched by class + text
    // rather than getByRole("dialog") because MUI marks a background dialog
    // aria-hidden the moment the pattern dialog opens, which drops it out of
    // every role-based query while it is still perfectly present in the DOM.
    this.settingsPaper = page.locator(".MuiDialog-paper").filter({ hasText: "Secret filter enabled" }).first();

    // ds/Switch sets inputProps['aria-label'] only when it is given a `label`.
    // These two are rendered without one, so there is no accessible name to
    // reach and the id the tab passes straight through is the highest rung left.
    this.secretFilterToggle = page.locator("#egress-filter-enabled-toggle");
    this.piiToggle = page.locator("#egress-pii-enabled-toggle");

    // ds/Select renders <label htmlFor> against a <button>, which gives the
    // button no accessible name — its name is its text, i.e. the selected
    // option. Binding to that would bind the locator to the tenant's own config.
    this.modeSelect = page.locator("#egress-filter-mode-select");
    this.piiModeSelect = page.locator("#egress-pii-mode-select");
    this.piiNerSelect = page.locator("#egress-pii-ner-select");

    // Rendered only while the tenant has no override row, and mutually
    // exclusive with the reset button below.
    this.inheritingDefaultText = this.settingsPaper.getByText(/Currently inheriting the platform default/).first();

    this.agentInput = page.locator("#egress-agent-input").or(this.settingsPaper.getByPlaceholder("Agent name, e.g. websearch")).first();
    this.agentAddBtn = this.settingsPaper.getByRole("button", { name: "Add", exact: true }).or(this.settingsPaper.locator("#egress-agent-add")).first();
    this.agentsEmptyNote = page.getByTestId("egress-agents-empty");

    this.newPatternBtn = this.settingsPaper.getByRole("button", { name: "New pattern" }).or(this.settingsPaper.locator("#egress-pattern-new")).first();

    // CustomTable stamps id={id} on the <table> and `${id}-body` on its tbody.
    // The table is not rendered at all while the tenant holds zero patterns —
    // the dashed note takes its place — so both shapes need a locator.
    this.patternsTable = page.locator("#egress-patterns-table").or(this.settingsPaper.getByRole("table")).first();
    this.patternsTableBody = page.locator("#egress-patterns-table-body").or(this.patternsTable.locator("tbody")).first();
    this.noPatternsNote = this.settingsPaper.getByText("No custom patterns yet").first();

    // Matches "Save changes" and the "Saving…" label it takes while a write is
    // in flight. NEVER clicked by this suite: it writes tenant-wide policy.
    this.saveChangesBtn = this.settingsPaper.getByRole("button", { name: /^Sav(e changes|ing)/ }).or(this.settingsPaper.locator("#egress-filter-save")).first();

    // Present only when an override row exists. Asserted for presence, NEVER
    // clicked — it drops the tenant's mode, enabled flag and every custom
    // pattern in one irreversible call.
    this.resetDefaultsBtn = this.settingsPaper
      .getByRole("button", { name: "Reset to platform defaults" })
      .or(this.settingsPaper.locator("#egress-filter-reset"))
      .first();

    // ds/Modal portals to <body>, so the pattern dialog is a DOM sibling of the
    // Settings dialog rather than a descendant, and both stamp the same
    // #alert-dialog-title id — hence the filter on the title text.
    this.patternDialog = page.getByRole("dialog").filter({ hasText: /(New|Edit) custom pattern/ }).first();
    this.newPatternDialog = page.getByRole("dialog").filter({ hasText: "New custom pattern" }).first();
    this.editPatternDialog = page.getByRole("dialog").filter({ hasText: "Edit custom pattern" }).first();

    // ds/Input wires a real <label htmlFor>, so both fields have accessible names.
    this.patternNameInput = this.patternDialog.getByRole("textbox", { name: "Name", exact: true }).or(this.patternDialog.locator("#egress-pattern-name")).first();
    this.patternRegexInput = this.patternDialog
      .getByRole("textbox", { name: "Pattern (regular expression)", exact: true })
      .or(this.patternDialog.locator("#egress-pattern-regex"))
      .first();

    // This switch IS given a label, so unlike the two tab-level toggles it has
    // an accessible name. MUI renders it as a checkbox input, not role=switch.
    this.patternEnabledSwitch = this.patternDialog.getByRole("checkbox", { name: "Enabled", exact: true }).or(this.patternDialog.locator("#egress-pattern-enabled")).first();

    // The button reads "Create" in add mode and "Save" in edit mode.
    this.patternSaveBtn = page.getByTestId("egress-pattern-save").or(this.patternDialog.getByRole("button", { name: /^(Create|Save)$/ })).first();
    this.patternCancelBtn = this.patternDialog.getByRole("button", { name: "Cancel", exact: true });

    // ds/Input renders its validation message as <span id={`${inputId}-error`}
    // role="alert">, so the id is exact and the role is the scoped fallback.
    this.patternNameError = this.patternDialog.locator("#egress-pattern-name-error").or(this.patternDialog.getByRole("alert").filter({ hasText: /characters or less/ })).first();
    this.patternRegexError = this.patternDialog
      .locator("#egress-pattern-regex-error")
      .or(this.patternDialog.getByRole("alert").filter({ hasText: /regular expression|characters or less/ }))
      .first();

    this.deletePatternDialog = page.getByRole("dialog").filter({ hasText: "Delete custom pattern" }).first();
    this.confirmDeleteBtn = this.deletePatternDialog.getByRole("button", { name: "Delete", exact: true });
  }

  // One pattern row, matched inside the table body so a name that also appears
  // in an open dialog cannot satisfy it.
  patternRow(name: string): Locator {
    return this.patternsTableBody.getByRole("row").filter({ hasText: name }).or(this.patternsTableBody.locator("tr").filter({ hasText: name })).first();
  }

  // The per-row testids carry the server-assigned pattern id, which this suite
  // only learns after the write, so a row-scoped prefix match is the rung-1
  // form available here; the aria-label is the fallback in the same row.
  editPatternBtn(name: string): Locator {
    const row = this.patternRow(name);
    return row.locator('[data-testid^="egress-pattern-edit-"]').or(row.getByRole("button", { name: "Edit pattern" })).first();
  }

  deletePatternBtn(name: string): Locator {
    const row = this.patternRow(name);
    return row.locator('[data-testid^="egress-pattern-delete-"]').or(row.getByRole("button", { name: "Delete pattern" })).first();
  }

  // An interactive ds/Chip renders as a MUI ButtonBase, so the PII categories
  // are real buttons. Their selected state is NOT aria-pressed: the tab passes
  // it, but ds/Chip emits it only for a chip given `pressed` or `selected`
  // (Chip.tsx:677,838) and this one is given neither, so it is dropped. The
  // rendered signal is data-tone — warning when the category is ticked.
  piiCategoryChip(category: string): Locator {
    return this.page.getByTestId(`egress-pii-cat-${category.toLowerCase()}`).or(this.settingsPaper.getByRole("button", { name: category, exact: true })).first();
  }

  agentChip(agent: string): Locator {
    return this.page.getByTestId(`egress-agent-${agent.toLowerCase()}`);
  }

  // ds/Chip's dismiss slot renders its own button and derives its testid from
  // the chip's, so the x is addressable without an index.
  agentChipDismiss(agent: string): Locator {
    return this.page.getByTestId(`egress-agent-${agent.toLowerCase()}-dismiss`).or(this.agentChip(agent).getByRole("button")).first();
  }
}
