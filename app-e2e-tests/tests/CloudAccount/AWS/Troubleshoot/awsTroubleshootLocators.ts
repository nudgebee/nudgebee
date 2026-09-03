// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { AWSLocators } from "../AWSLocators";

// Table ids the three sub-tabs hand to CustomTable / KubernetesTable. CustomTable renders
// its body as <tbody id={`${id}-body`}> (CustomTable.jsx:859) and its empty state as
// EmptyData id={id}, which becomes <h2 id={`${id}-no-data`}> (EmptyData.jsx:26).
export const EVENTS_TABLE = "cloudaccount-events";
export const RULES_TABLE = "triageRulesManager";
export const SUGGESTIONS_TABLE = "thresholdSuggestionsManager";

// ListingLayout.Toolbar forwards data-testid to the DOM through its ...rest spread
// (ListingLayout.tsx:113,136), so these are real test contracts rather than props.
export const EVENTS_TOOLBAR = `${EVENTS_TABLE}-filter-toolbar`;
export const RULES_TOOLBAR = "triage-rules-filter-toolbar";
export const SUGGESTIONS_TOOLBAR = "threshold-suggestions-filter-toolbar";

// Filter ids as passed to ds/FilterDropdown, which renders its trigger as
// <button id={`auto-complete-${toKebabCase(id)}`}> (FilterDropdown.jsx:1043-1050).
// None of these ids contains a space, so each kebabs to itself.
export const EVENTS_FILTER = {
  eventName: `${EVENTS_TABLE}-filter-event-name`,
  severity: `${EVENTS_TABLE}-filter-severity`,
  triagePriority: `${EVENTS_TABLE}-filter-triage-priority`,
  source: `${EVENTS_TABLE}-filter-source`,
  status: `${EVENTS_TABLE}-filter-status`,
  triageStatus: `${EVENTS_TABLE}-filter-triage-status`,
} as const;

// CloudAccountEvents composes its search id from the table id (CloudAccountEvents.tsx:733).
export const EVENTS_FILTER_SEARCH = `${EVENTS_TABLE}-filter-search-message`;

export const EVENTS_FILTER_LABEL: Record<keyof typeof EVENTS_FILTER, string> = {
  eventName: "Event Name",
  severity: "Severity",
  triagePriority: "Triage Priority",
  source: "Source",
  status: "Status",
  triageStatus: "Triage Status",
};

export const RULES_FILTER = {
  ruleType: "triage-rules-filter-rule-type",
  status: "triage-rules-filter-status",
} as const;

export const RULES_FILTER_LABEL: Record<keyof typeof RULES_FILTER, string> = {
  ruleType: "Rule Type",
  status: "Status",
};

export const SUGGESTIONS_FILTER = {
  source: "threshold-suggestions-filter-source",
  confidence: "threshold-suggestions-filter-confidence",
} as const;

export const SUGGESTIONS_FILTER_LABEL: Record<keyof typeof SUGGESTIONS_FILTER, string> = {
  source: "Source",
  confidence: "Confidence",
};

// The Account filter each manager declares behind isMultiAccountView is deliberately absent
// from the maps above: the cloud-account detail page always passes an accountId, so
// isMultiAccountView is false here (TriageRulesManager.tsx:54, ThresholdSuggestionsManager.tsx:103)
// and that dropdown never renders on this route.

// Column headers each listing declares, in order, for the single-account shape.
export const EVENTS_HEADERS = ["Severity", "Application", "Event", "Triage Score", "Alert Status", "Triage Status"] as const;
export const RULES_HEADERS = ["Name", "Type", "Action", "Match Criteria", "Priority", "Status", "Matches", "Created"] as const;
export const SUGGESTIONS_HEADERS = ["Alert", "Recommendation", "Threshold", "Confidence", "Noise Reduction"] as const;

// Zero-based column indices. The expand toggle CustomTable appends for an expandable table
// is the LAST cell (CustomTable.jsx:457-485), so these stay aligned with the headers arrays.
export const RULES_COLUMN = { name: 0, type: 1, action: 2, matchCriteria: 3, priority: 4, status: 5, matches: 6, created: 7 } as const;
export const SUGGESTIONS_COLUMN = { alert: 0, recommendation: 1, threshold: 2, confidence: 3, noiseReduction: 4 } as const;

// Both option lists are hardcoded in their components (TriageRulesManager.tsx:47,
// ThresholdSuggestionsManager.tsx:44) rather than fetched per account, so they are the two
// filters that offer the same values on every environment.
export const RULE_STATUS_OPTIONS = ["Enabled", "Disabled"] as const;
export const CONFIDENCE_OPTIONS = ["High", "Medium", "Low"] as const;

// The exact refusals TriageRuleModal.handleSubmit returns before it calls the API
// (TriageRuleModal.tsx:229,235).
export const NO_RULE_TYPE_ERROR = "Please select a rule type";
export const NO_CRITERIA_ERROR = "Please specify at least one match criterion";

function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export class AwsTroubleshootLocators extends AWSLocators {
  // Panel roots. Each is the ListingLayout id its sub-tab renders, so "which sub-tab is
  // mounted" is observable as a distinct node rather than as a restyle of a shared one.
  readonly eventsRoot: Locator;
  readonly rulesRoot: Locator;
  readonly suggestionsRoot: Locator;

  // Alert Tuning is the third Troubleshoot sub-tab ([CloudAccountDetails].jsx:158). AWSLocators
  // declares only Events and Triage Rules, so these two are added here rather than re-declared.
  readonly TroubleshootThresholdSuggestions: Locator;
  readonly TroubleshootThresholdSuggestionsUrl: RegExp;

  readonly eventsSearch: Locator;
  readonly rulesSearch: Locator;
  readonly createRuleBtn: Locator;
  readonly systemRulesToggle: Locator;

  constructor(page: Page) {
    super(page);

    // Deliberately id-only, with no .or() fallback.
    //
    // A ListingLayout root is a plain Box (ListingLayout.tsx:205) with no role, accessible
    // name or unique text of its own, so rung 3 is the highest one available for it. A wider
    // fallback would be actively unsafe rather than merely slower: these three roots are what
    // the navigation test uses to prove one sub-tab replaced another, so a structural match
    // that also resolved against a sibling panel would report a switch that never happened.
    // Tag-qualified because `cloudaccount-events` is NOT unique in the DOM: CloudAccountEvents
    // passes the same string to its ListingLayout (line 712) and to CustomTable (line 792),
    // which puts it on the <table> (CustomTable.jsx:1112). A bare #cloudaccount-events resolves
    // to both and fails Playwright's strict mode. The div is the panel root; the table is inside
    // it, so every descendant lookup below still works. See the PR's Follow-ups — the duplicate
    // id is an app-side defect, not something a test should have to work around.
    this.eventsRoot = page.locator(`div#${EVENTS_TABLE}`);
    this.rulesRoot = page.locator("#triage-rules-list-box");
    this.suggestionsRoot = page.locator("#threshold-suggestions-list-box");

    // Sub-tab entries in the Troubleshoot anchor flyout are rendered as #dropdown-{id}, the
    // same shape AWSLocators already relies on for Events and Triage Rules — so this matches a
    // contract eleven merged specs already depend on.
    //
    // Deliberately id-only: the flyout entry's only other handle is its "Alert Tuning" label,
    // and that exact string is also a global-search row (navSearchPages.ts:104), so a text
    // fallback could commit a click on the header search instead of the sub-tab. No fallback
    // beats a wrong one.
    this.TroubleshootThresholdSuggestions = page.locator("#dropdown-threshold-suggestions");
    this.TroubleshootThresholdSuggestionsUrl = /#events\/threshold-suggestions/;

    // ds/SearchInput forwards its id to the native input (SearchInput.jsx:86) and passes its
    // `label` through as the placeholder (SearchInput.jsx:89). The placeholder is the input's
    // only accessible name, so an id primary with a placeholder fallback is the safe pairing —
    // and the toolbar data-testid these two components render (TriageRulesManager.tsx:536,
    // CloudAccountEvents.tsx:714) is on the toolbar Box, not on the field, so it cannot lead here.
    this.eventsSearch = this.eventsRoot
      .locator(`#${EVENTS_FILTER_SEARCH}`)
      .or(this.eventsRoot.getByPlaceholder("Search by event"))
      .first();

    this.rulesSearch = this.rulesRoot
      .locator("#triage-rules-search")
      .or(this.rulesRoot.getByPlaceholder("Search by name"))
      .first();

    // Role leads: ds/Button renders a real <button> carrying its own visible text
    // (TriageRulesManager.tsx:540-542), so getByRole reaches it by accessible name and outranks
    // the id. Scoped to the rules panel because TriageRuleModal's submit button carries the
    // same "Create Rule" label while the modal is open (TriageRuleModal.tsx:449-451).
    this.createRuleBtn = this.rulesRoot
      .getByRole("button", { name: "Create Rule" })
      .or(this.rulesRoot.locator("#create-rule-btn"))
      .first();

    // ds/Switch puts the caller's id on the MUI Switch root and gives the inner checkbox an
    // aria-label from `label` (Switch.tsx:148-151), so role reaches the control itself while
    // the id form has to descend into it.
    this.systemRulesToggle = this.rulesRoot
      .getByRole("checkbox", { name: "System Rules" })
      .or(this.rulesRoot.locator("#triage-rules-filter-include-system input"))
      .first();
  }

  // ── Toolbars ────────────────────────────────────────────────────────────────

  toolbar(testId: string): Locator {
    return this.page.getByTestId(testId).first();
  }

  // ── Filter triggers ─────────────────────────────────────────────────────────

  private filterTrigger(root: Locator, id: string, label: string): Locator {
    return this.page
      .locator(`#auto-complete-${id}`)
      .or(root.getByRole("button", { name: new RegExp(`^${escapeForRegex(label)}`) }))
      .first();
  }

  eventsFilterTrigger(key: keyof typeof EVENTS_FILTER): Locator {
    return this.filterTrigger(this.eventsRoot, EVENTS_FILTER[key], EVENTS_FILTER_LABEL[key]);
  }

  rulesFilterTrigger(key: keyof typeof RULES_FILTER): Locator {
    return this.filterTrigger(this.rulesRoot, RULES_FILTER[key], RULES_FILTER_LABEL[key]);
  }

  suggestionsFilterTrigger(key: keyof typeof SUGGESTIONS_FILTER): Locator {
    return this.filterTrigger(this.suggestionsRoot, SUGGESTIONS_FILTER[key], SUGGESTIONS_FILTER_LABEL[key]);
  }

  // ── Rows ────────────────────────────────────────────────────────────────────

  // CloudAccountEvents passes showExpandable={false} (CloudAccountEvents.tsx:798), so
  // CustomTable emits exactly one <tr> per record and this count is the record count.
  get eventRows(): Locator {
    return this.page.locator(`#${EVENTS_TABLE}-body tr`);
  }

  // Triage Rules and Alert Tuning are both expandable, so CustomTable emits TWO <tr> per
  // record: the data row, then a collapse row holding a single full-width <td>
  // (CustomTable.jsx:487-499). Selecting on a second cell keeps only the data rows — counting
  // bare <tr> here would report double the records.
  get ruleRows(): Locator {
    return this.page.locator(`#${RULES_TABLE}-body tr:has(td:nth-child(2))`);
  }

  get suggestionRows(): Locator {
    return this.page.locator(`#${SUGGESTIONS_TABLE}-body tr:has(td:nth-child(2))`);
  }

  // One column's cell across every data row, in row order. Uses nth-child rather than
  // .locator("td").nth(n), which would index into the whole table's cells instead of each
  // row's. The COLUMN maps are zero-based; nth-child is 1-based.
  ruleColumnCells(column: keyof typeof RULES_COLUMN): Locator {
    return this.ruleRows.locator(`td:nth-child(${RULES_COLUMN[column] + 1})`);
  }

  suggestionColumnCells(column: keyof typeof SUGGESTIONS_COLUMN): Locator {
    return this.suggestionRows.locator(`td:nth-child(${SUGGESTIONS_COLUMN[column] + 1})`);
  }

  // The row whose Name cell is exactly `name`. Used to prove a created rule persisted into
  // the listing rather than only into a success toast.
  ruleRowNamed(name: string): Locator {
    return this.ruleRows.filter({
      has: this.page.locator(`td:nth-child(${RULES_COLUMN.name + 1})`, { hasText: new RegExp(`^\\s*${escapeForRegex(name)}\\s*$`) }),
    });
  }

  // ── Empty states ────────────────────────────────────────────────────────────

  // CustomTable renders shared/EmptyData rather than ds/EmptyState, so there is no
  // role='status' here — it is an <h2 id={`${id}-no-data`}> (EmptyData.jsx:26).
  //
  // This doubles as each listing's "settled" signal: renderEmptyState returns null while
  // `loading` is true (CustomTable.jsx:917), so it appears only once a fetch has finished and
  // genuinely returned nothing. That matters because a bare row count of 0 is also what a
  // refetch in flight looks like.
  get eventsEmptyState(): Locator {
    return this.page.locator(`#${EVENTS_TABLE}-no-data`).or(this.eventsRoot.getByRole("heading", { name: "No Data Available" })).first();
  }

  get suggestionsEmptyState(): Locator {
    return this.page
      .locator(`#${SUGGESTIONS_TABLE}-no-data`)
      .or(this.suggestionsRoot.getByRole("heading", { name: "No Data Available" }))
      .first();
  }

  // Triage Rules is the exception: it swaps the whole table out for its own EmptyData with a
  // dedicated id (TriageRulesManager.tsx:617-623) instead of letting CustomTable render one,
  // so this node is not `${RULES_TABLE}-no-data`.
  get rulesEmptyState(): Locator {
    return this.page.locator("#triage-rules-empty-no-data").or(this.rulesRoot.getByRole("heading", { name: "No Data Available" })).first();
  }

  // ── ds/FilterDropdown panel ─────────────────────────────────────────────────

  // Options are role='option' boxes inside the open panel (FilterDropdown.jsx:197-198).
  // Scoped by :visible rather than by the popover's MUI class: only one panel is ever open,
  // and every other dropdown's options are unmounted.
  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  visibleOptions(): Locator {
    return this.page.locator('[role="option"]:visible');
  }

  // The panel's search box, which FilterDropdown renders only above eight options, so callers
  // must treat its absence as an expected outcome rather than a failure.
  filterSearchInput(): Locator {
    return this.page.locator(".MuiPopover-root").getByPlaceholder("Search...").first();
  }

  // Opens `trigger` and commits `label`.
  //
  // Waits on the options rather than on the search box: the box exists only above eight
  // options, so waiting for it would burn a full timeout on every short list. Mirrors the
  // merged AWS Monitoring chooseFilterOption, which is the implementation proven against dev —
  // the shared selectDropdownOption in tests/utils/helpers.ts does not fit here, as it waits
  // for a role='listbox' that ds/FilterDropdown's Popover never renders.
  async chooseFilterOption(trigger: Locator, label: string): Promise<void> {
    await trigger.click();
    await this.visibleOptions().first().waitFor({ state: "visible", timeout: 20000 });

    // Options and the search box render in the same pass, so by this point its absence is a
    // settled answer and needs no timeout of its own.
    const search = this.filterSearchInput();
    if (await search.isVisible()) {
      await search.fill(label);
    }

    await this.filterOption(label).click();
    // The trigger renders its label plus the committed value, so this is the signal that the
    // selection landed rather than a fixed pause.
    await expect(trigger).toContainText(label);
  }

  // ── Dialogs ─────────────────────────────────────────────────────────────────

  // ds/Modal titles its dialog with a fixed id (Modal.tsx:357) and renders no testid, and both
  // dialogs in this module reuse that same id — so the title's TEXT is what tells the create
  // form apart from the delete confirmation. The dialog role is MUI Dialog's own contract.
  dialogTitled(title: RegExp): Locator {
    return this.page
      .getByRole("dialog")
      .filter({ has: this.page.locator("#alert-dialog-title").filter({ hasText: title }) })
      .first();
  }

  get createRuleDialog(): Locator {
    return this.dialogTitled(/^Create Triage Rule$/);
  }

  deleteRuleDialog(ruleName: string): Locator {
    return this.dialogTitled(new RegExp(`^Delete rule "${escapeForRegex(ruleName)}"$`));
  }

  // A labelled field inside an open dialog. ds/Input renders a real <label htmlFor> bound to
  // the control (Input.tsx:428-441), so the label is the field's accessible name and role
  // leads; getByLabel backs it up through the same association.
  dialogField(dialog: Locator, label: string): Locator {
    return dialog
      .getByRole("textbox", { name: label, exact: true })
      .or(dialog.getByLabel(label, { exact: true }))
      .first();
  }

  // ds/Modal's own footer and TriageRuleModal's custom actionButtons both use id='submit' /
  // id='cancel' (Modal.tsx:476-482, TriageRuleModal.tsx:446-451), so these ids are unique only
  // within a dialog — hence the scoping. Role leads on the accessible name each button carries.
  dialogSubmit(dialog: Locator, name: RegExp): Locator {
    return dialog.getByRole("button", { name }).or(dialog.locator("#submit")).first();
  }

  dialogCancel(dialog: Locator): Locator {
    return dialog.getByRole("button", { name: "Cancel" }).or(dialog.locator("#cancel")).first();
  }

  // ── Row action menu ─────────────────────────────────────────────────────────

  // ds/Button gives the three-dot trigger aria-label='More actions'
  // (TriageRulesManager.tsx:210), so role leads; its id is not unique across rows, which is
  // why any fallback would have to be scoped to the row rather than to the page.
  rowMenuTrigger(row: Locator): Locator {
    return row.getByRole("button", { name: "More actions" }).first();
  }

  // ds/DropdownMenu renders items as role='menuitem' carrying the caller's item id, but that
  // id embeds the rule's server-assigned uuid (`triage-action-${rule.id}-${m.id}`,
  // TriageRulesManager.tsx:203) — not a value a locator can name ahead of the run. :visible is
  // what narrows this to the one menu that is actually open, since closed menus stay mounted.
  menuItem(label: RegExp): Locator {
    return this.page.locator('[role="menuitem"]:visible').filter({ hasText: label }).first();
  }

  // ── Toasts ──────────────────────────────────────────────────────────────────

  // SnackbarComponent renders each toast as role='alert' for an error and role='status' for a
  // success (SnackbarComponent.tsx:268), inside a role='region' named Notifications (line 175).
  //
  // Matched with CSS attribute selectors rather than getByRole, and this is load-bearing: the
  // region is rendered inline in the app tree (no Portal), so when a ds/Modal is open MUI's
  // modal manager marks everything outside the dialog aria-hidden. getByRole consults the
  // accessibility tree and so cannot see the toast at all, even though it is painted above the
  // dialog at zIndex 1500 — which is exactly the case the validation test needs, since that
  // toast is the modal's own refusal. A CSS selector ignores the accessibility tree.
  toast(role: "alert" | "status", text: RegExp): Locator {
    return this.page.locator(`[aria-label="Notifications"] [role="${role}"]`).filter({ hasText: text }).first();
  }
}
