import { Page, Locator } from "@playwright/test";
import { TroubleshootLocators } from "../TroubleshootLocators";
import {
  ACCOUNT_FILTER_ID,
  ACCOUNT_FILTER_LABEL,
  ALERT_NAME_LABEL,
  ALERT_NAME_PLACEHOLDER,
  APPLY_TO_EXISTING_LABEL,
  CANCEL_LABEL,
  CLUSTER_TRIAGE_RULES_TAB_ID,
  CREATE_BUTTON_ID,
  CREATE_MODAL_TITLE,
  CREATE_SUBMIT_LABEL,
  DELETE_CONFIRM_LABEL,
  DISABLE_CONFIRM_LABEL,
  EDIT_MODAL_TITLE,
  EMPTY_STATE_ID,
  EXPAND_ROW_LABEL,
  EXPIRATION_HEADING,
  EXPIRES_AT_FIELD_ID,
  LISTING_ID,
  MATCHES_COLUMN_LABEL,
  ROW_MENU_LABEL,
  RULE_NAME_LABEL,
  RULE_NAME_PLACEHOLDER,
  RULE_TYPE_FILTER_ID,
  RULE_TYPE_FILTER_LABEL,
  SCORE_ADJUSTMENT_LABEL,
  SEARCH_ID,
  SEARCH_PLACEHOLDER,
  STATUS_FILTER_ID,
  STATUS_FILTER_LABEL,
  SYSTEM_CHIP,
  SYSTEM_SWITCH_ID,
  SYSTEM_SWITCH_LABEL,
  TABLE_ID,
  TOAST_REGION_LABEL,
  TOOLBAR_TESTID,
  TRIAGE_INBOX_TAB_NAME,
  TRIAGE_RULES_TAB_ID,
  TRIAGE_RULES_TAB_NAME,
} from "./triageRulesConstants";

// Rung choice, measured rather than assumed. Counted before a locator was written:
//   grep -c data-testid app/src/components/triage/TriageRulesManager.tsx -> 1
//   grep -c data-testid app/src/components/triage/TriageRuleModal.tsx    -> 0
// The single testid is the toolbar's, so rung 1 exists for exactly one element and is taken
// there. Everywhere else the highest available rung is used: role+name wherever the component
// gives the element a real accessible name (ds/Input renders a <label htmlFor>, ds/Switch sets
// aria-label from its label, ds/Button and ds/FilterDropdown render real buttons), with the id
// the app publishes as the same-element `.or()` fallback. Locators that are deliberately
// id-only say so on the line above them.

// CustomTable is driven with showExpandable, so each rule contributes a data <tr> plus a
// collapse <tr> holding a single full-width <td>. The second-cell guard keeps the collapse
// rows out of every count and every column read.
const DATA_ROW = "tr:has(td:nth-child(2))";

export class TriageRulesLocators extends TroubleshootLocators {
  // Sub-tab handles. Both pages mount the same manager, but each names its tab differently.
  readonly triageRulesSubTab: Locator;
  readonly clusterTriageRulesSubTab: Locator;
  readonly triageInboxSubTab: Locator;

  // Toolbar.
  readonly listing: Locator;
  readonly toolbar: Locator;
  readonly search: Locator;
  readonly accountFilter: Locator;
  readonly ruleTypeFilter: Locator;
  readonly statusFilter: Locator;
  readonly systemRulesSwitch: Locator;
  readonly createButton: Locator;

  // Listing body.
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly rows: Locator;
  readonly emptyState: Locator;

  // Create / edit modal.
  readonly ruleModal: Locator;
  readonly ruleModalTitle: Locator;
  readonly editRuleModalTitle: Locator;
  readonly ruleNameField: Locator;
  readonly alertNameField: Locator;
  readonly scoreAdjustmentField: Locator;
  readonly expirationToggle: Locator;
  readonly expiresAtField: Locator;
  readonly applyToExistingToggle: Locator;
  readonly applyToExistingPreview: Locator;
  readonly modalSubmit: Locator;
  readonly modalCancel: Locator;

  // Delete confirmation and toasts.
  readonly deleteConfirmButton: Locator;
  readonly disableConfirmButton: Locator;
  readonly toastRegion: Locator;

  // A row's own expandable drilldown. Only one row is ever expanded within a single test here,
  // so an unscoped page-level match is unambiguous in practice.
  readonly matchedEventsPanel: Locator;

  constructor(page: Page) {
    super(page);

    // MUI renders <Tab> with role="tab" and the tabOption's `text` as its accessible name, so
    // role outranks the id the app also puts there via a11yProps.
    this.triageRulesSubTab = page
      .getByRole("tab", { name: TRIAGE_RULES_TAB_NAME, exact: true })
      .or(page.locator(`[id="${TRIAGE_RULES_TAB_ID}"]`))
      .first();
    // The cluster page's strip renders the same tab under a different id.
    this.clusterTriageRulesSubTab = page
      .getByRole("tab", { name: TRIAGE_RULES_TAB_NAME, exact: true })
      .or(page.locator(`[id="${CLUSTER_TRIAGE_RULES_TAB_ID}"]`))
      .first();
    // The sub-tab the navigation case bounces through. Id-only fallback is deliberately absent:
    // the two pages id this same tab differently ('tab-fingerprint' vs 'fingerprint'), so a
    // single id would be wrong on one of them while the accessible name is right on both.
    this.triageInboxSubTab = page.getByRole("tab", { name: TRIAGE_INBOX_TAB_NAME, exact: true }).first();

    // ds/ListingLayout puts the caller's id straight on a ds/Card, which forwards it to a plain
    // Box — no role, no accessible name, no testid. Deliberately id-only: this is the scope the
    // toolbar controls fall back inside, so a wider match would defeat the scoping itself.
    this.listing = page.locator(`#${LISTING_ID}`);

    // The one rung-1 handle on this surface. ListingLayout.Toolbar spreads `...rest`, so the
    // caller's data-testid reaches the DOM (ds/ListingLayout.tsx:113-137). No fallback on
    // purpose: a testid is a test contract, and this locator is the scope half the toolbar
    // falls back inside, so a wider match here would quietly unscope all of them.
    this.toolbar = page.getByTestId(TOOLBAR_TESTID);

    // ds/SearchInput forwards its id to the <input> and passes `label` through as the
    // placeholder, so it renders no <label> element and has no accessible name to reach by
    // role — the id (rung 3) outranks the placeholder (rung 4). The fallback is scoped to the
    // toolbar so it cannot resolve to another search box elsewhere on the page.
    this.search = page.locator(`#${SEARCH_ID}`).or(this.toolbar.getByPlaceholder(SEARCH_PLACEHOLDER)).first();

    this.accountFilter = this.filterTrigger(ACCOUNT_FILTER_ID, ACCOUNT_FILTER_LABEL);
    this.ruleTypeFilter = this.filterTrigger(RULE_TYPE_FILTER_ID, RULE_TYPE_FILTER_LABEL);
    this.statusFilter = this.filterTrigger(STATUS_FILTER_ID, STATUS_FILTER_LABEL);

    // ds/Switch sets inputProps['aria-label'] from its `label` and forwards `id` to the same
    // <input type="checkbox">, so both rungs land on one element.
    this.systemRulesSwitch = page
      .getByRole("checkbox", { name: SYSTEM_SWITCH_LABEL })
      .or(page.locator(`#${SYSTEM_SWITCH_ID}`))
      .first();

    // Rendered only on the single-account view, and only for a writer. Scoped to the toolbar
    // because the modal it opens carries a second "Create Rule" button — its submit — and an
    // unscoped role match would resolve to whichever came first in document order.
    this.createButton = this.toolbar
      .getByRole("button", { name: CREATE_SUBMIT_LABEL, exact: true })
      .or(page.locator(`#${CREATE_BUTTON_ID}`))
      .first();

    // Id-only by design: CustomTable renders no testid and a <table>/<tbody> carries no
    // accessible name, so the ids it publishes are the only handle there is.
    this.table = page.locator(`#${TABLE_ID}`);
    this.tableBody = page.locator(`#${TABLE_ID}-body`);
    this.rows = page.locator(`#${TABLE_ID}-body ${DATA_ROW}`);

    // The module's own EmptyData, which replaces the table entirely rather than rendering
    // inside it. Id-only: EmptyData gives its <h2> no role and no testid.
    this.emptyState = page.locator(`#${EMPTY_STATE_ID}`);

    // ds/Modal renders a MUI Dialog. Only one is open at a time, so role alone is unambiguous
    // and is what every field below scopes to.
    this.ruleModal = page.getByRole("dialog").first();
    // ds/Modal ids its title <h2> `alert-dialog-title`; the role+name rung is unavailable
    // because the heading is not exposed as one, so this is scoped exact text. The same
    // modal renders one of two titles depending on isCreate — a second locator for the
    // Edit title rather than one that resolves to either, so a test names the state it
    // actually expects instead of accepting whichever one shows up.
    this.ruleModalTitle = this.ruleModal.getByText(CREATE_MODAL_TITLE, { exact: true }).first();
    this.editRuleModalTitle = this.ruleModal.getByText(EDIT_MODAL_TITLE, { exact: true }).first();

    // ds/Input renders a real <label htmlFor={inputId}>, so each field has a genuine accessible
    // name — rung 2. The placeholder fallback is scoped to the dialog for the same reason.
    this.ruleNameField = this.ruleModal
      .getByRole("textbox", { name: RULE_NAME_LABEL, exact: true })
      .or(this.ruleModal.getByPlaceholder(RULE_NAME_PLACEHOLDER))
      .first();
    this.alertNameField = this.ruleModal
      .getByRole("textbox", { name: ALERT_NAME_LABEL, exact: true })
      .or(this.ruleModal.getByPlaceholder(ALERT_NAME_PLACEHOLDER))
      .first();
    // A number input, so its ARIA role is "spinbutton" — not "textbox" like the fields above.
    // Verified live rather than assumed, since guessing the wrong role here is a silent
    // zero-match locator.
    this.scoreAdjustmentField = this.ruleModal.getByRole("spinbutton", { name: SCORE_ADJUSTMENT_LABEL, exact: true }).first();

    // The Expiration block's own expand toggle carries no text, aria-label or testid
    // (BlockWithHeading.tsx:188) — the heading text is a direct child of the same container the
    // button sits in, so the button is reached from that text's parent rather than by role+name.
    // Verified live: this resolves to exactly one button before the section expands.
    this.expirationToggle = this.ruleModal
      .getByText(EXPIRATION_HEADING, { exact: true })
      .locator("xpath=..")
      .getByRole("button")
      .first();
    // The field's own <label> is not htmlFor-associated (CustomDateTimePicker.jsx), so its
    // accessible name is its format mask, not a human label — deliberately id-only.
    this.expiresAtField = this.ruleModal.locator(`#${EXPIRES_AT_FIELD_ID}`);

    // The switch carries no label/aria-label at all (TriageRuleModal.tsx:544) — verified live
    // that its aria-label/aria-labelledby are both null. Its description text sits two DOM levels
    // above the switch itself (Typography inside a Box inside the flex Box the switch also sits
    // in), not a wrapping FormControlLabel, so it is reached from that ancestor rather than the
    // text's immediate parent.
    this.applyToExistingToggle = this.ruleModal
      .getByText(APPLY_TO_EXISTING_LABEL, { exact: true })
      .locator("xpath=../..")
      .getByRole("checkbox")
      .first();
    // Only rendered while applyToExisting is on, so it exists exactly when a test could read it.
    this.applyToExistingPreview = this.ruleModal.getByText(/will be affected/).first();

    // Both action buttons carry ids, but the accessible name is the higher rung and is
    // unambiguous once scoped to the dialog.
    this.modalSubmit = this.ruleModal
      .getByRole("button", { name: CREATE_SUBMIT_LABEL, exact: true })
      .or(this.ruleModal.locator("#submit"))
      .first();
    this.disableConfirmButton = this.ruleModal
      .getByRole("button", { name: DISABLE_CONFIRM_LABEL, exact: true })
      .or(this.ruleModal.locator("#submit"))
      .first();
    this.modalCancel = this.ruleModal
      .getByRole("button", { name: CANCEL_LABEL, exact: true })
      .or(this.ruleModal.locator("#cancel"))
      .first();

    // The delete confirmation is ds/Modal's own footer, whose primary button takes the
    // caller's confirmText as its label.
    this.deleteConfirmButton = this.ruleModal
      .getByRole("button", { name: DELETE_CONFIRM_LABEL, exact: true })
      .or(this.ruleModal.locator("#submit"))
      .first();

    // Every toast the app raises mounts inside this one labelled region, so asserting on the
    // region's text covers success and error alike.
    //
    // Matched by attribute rather than by role, which looks like a needless drop to rung 5 and
    // is not. The region lives at the app root, OUTSIDE the modal; MUI's ModalManager sets
    // aria-hidden="true" on everything outside an open modal, and getByRole reads the
    // accessibility tree — so a role query finds NOTHING for exactly as long as a modal is open.
    // That is precisely when the validation case needs to read a toast, since the whole point is
    // that the modal stays open. CI run 33439802494 proved it: the create case, which asserts
    // its toast AFTER handleClose(), passed on the role locator, while the validation case timed
    // out 30s on "element(s) not found". A CSS attribute match ignores the a11y tree and sees
    // the region in both states. Kept as one locator so both cases read toasts the same way.
    this.toastRegion = page.locator(`[role="region"][aria-label="${TOAST_REGION_LABEL}"]`).first();

    // CustomTable's expandable drilldown row is its own <tabpanel>, mounted as a sibling row
    // immediately after the one that owns it. Only one row is ever expanded within a single test
    // here, so matching it unscoped at the page level is unambiguous in practice.
    this.matchedEventsPanel = page.getByRole("tabpanel").first();
  }

  // Both rungs land on ds/FilterDropdown's own trigger button. The accessible name starts with
  // the label and continues with the current selection, so it is anchored rather than exact.
  // Scoped to the toolbar: `.or()` resolves in document order, and a page-wide id fallback
  // could reach a same-named filter on another pane that is still mounted.
  private filterTrigger(id: string, label: string): Locator {
    return this.toolbar
      .getByRole("button", { name: new RegExp(`^${label}`) })
      .or(this.toolbar.locator(`#auto-complete-${id}`))
      .first();
  }

  // One column across every rendered row, as a single locator, so a whole-table claim ("every
  // Type cell now reads Suppression") can be made with a retrying count assertion instead of a
  // read-then-compare that races the refetch.
  cellsInColumn(column: number): Locator {
    return this.page.locator(`#${TABLE_ID}-body ${DATA_ROW} td:nth-child(${column})`);
  }

  // The row whose Name cell holds this exact text. Used to prove a created rule persisted and
  // that a deleted one is gone.
  rowByName(name: string): Locator {
    return this.rows.filter({ has: this.page.getByText(name, { exact: true }) }).first();
  }

  // A single row's own Matches cell. Reads the position off the table's own header row rather
  // than trusting a fixed index — the multi-account view prepends an Account Name column ahead
  // of every other one, so "Matches" sits at a different position there than on the cluster
  // page. Async because it has to read the DOM first; every caller awaits it.
  async matchesCellFor(row: Locator): Promise<Locator> {
    const headers = this.page.locator(`#${TABLE_ID} thead th`);
    await headers.first().waitFor({ state: "visible" });
    const headerTexts = await headers.allInnerTexts();
    const index = headerTexts.findIndex((text) => text.trim() === MATCHES_COLUMN_LABEL);
    if (index === -1) throw new Error(`No "${MATCHES_COLUMN_LABEL}" column header found`);
    return row.locator(`td:nth-child(${index + 1})`);
  }

  // A row's own expand/collapse toggle (CustomTable's showExpandable column).
  expandRowButton(row: Locator): Locator {
    return row.getByRole("button", { name: EXPAND_ROW_LABEL, exact: true }).first();
  }

  // The "System" tag chips currently rendered. Exact text, because a rule's own name or its
  // match-criteria summary can legitimately contain the word as a substring ("NS: kube-system")
  // and only the chip is ever exactly "System".
  systemChips(): Locator {
    return this.page.locator(`#${TABLE_ID}-body ${DATA_ROW}`).getByText(SYSTEM_CHIP, { exact: true });
  }

  // A row's action-menu trigger. ds/Button composition='icon-only' renders no text, so the
  // aria-label the manager sets is the only name it has.
  rowMenuTrigger(row: Locator): Locator {
    return row.getByRole("button", { name: ROW_MENU_LABEL }).first();
  }

  // An entry of an open row menu, by its exact label. ds/DropdownMenu forwards each item's id,
  // but that id embeds the server-assigned rule id, which a test that just created the rule
  // through the UI never learns — so the open menu's own item text is the handle, anchored so
  // it cannot match a longer label. `:visible` keeps it off the closed menus every other row
  // leaves mounted. Shared by every row-menu action (Edit, Enable/Disable, Delete, the system
  // rule override) rather than one method per label.
  rowMenuItem(label: string): Locator {
    return this.page
      .locator('[role="menuitem"]:visible')
      .filter({ hasText: new RegExp(`^${label}$`) })
      .first();
  }

  // One of the create/edit modal's own Rule Type tabs (Suppression/Scoring/Classification).
  // @shared/navigation/Tabs renders MUI <Tab> with role="tab" and the tabOption's text as its
  // accessible name — same mechanism already proven for the page-level Triage Rules/Triage
  // Inbox sub-tabs, verified live for this modal via a snapshot of the open tablist.
  ruleTypeTab(label: string): Locator {
    return this.ruleModal.getByRole("tab", { name: label, exact: true }).first();
  }

  // One of the create/edit modal's Action radios (Suppress/Drop under Suppression, False
  // Positive/Duplicate under Classification). FormControlLabel wraps both the option's title and
  // its description Typography inside the same <label>, so the radio's accessible name is the
  // title followed by the description (e.g. "Duplicate Auto-classify as duplicate") — verified
  // live — hence the anchored-start match rather than exact.
  actionRadio(label: string): Locator {
    return this.ruleModal.getByRole("radio", { name: new RegExp(`^${label}`) }).first();
  }

  // ds/FilterDropdown portals its options into a MUI Popover, so they are not inside the
  // trigger's container and have to be reached from the page. Only one popover is open at a
  // time. Anchored and escaped so one option label cannot match another that extends it.
  filterOption(label: string): Locator {
    const exact = new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
    return this.page.locator('[role="option"]').filter({ hasText: exact }).first();
  }
}
