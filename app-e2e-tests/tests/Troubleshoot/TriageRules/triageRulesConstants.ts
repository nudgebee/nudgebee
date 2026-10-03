// Not for OSS

// Triage Rules — app/src/components/triage/TriageRulesManager.tsx, mounted twice:
//   /troubleshoot#all-events/triage-rules          (no accountId -> multi-account view)
//   /kubernetes/details/<accountId>#events/triage-rules  (accountId -> single-account view)
// The two views differ by more than scope, and the difference decides where a case can run:
// the Create Rule button renders only when `accountId && hasWriteAccess(accountId)`
// (TriageRulesManager.tsx:539), and the Account filter renders only when it is absent
// (TriageRulesManager.tsx:557). So every write case runs on the cluster view, and the
// multi-account view is asserted for exactly the contract it owns.

export const TROUBLESHOOT_PATH = "/troubleshoot";
export const CLUSTER_DETAILS_PATH = "/kubernetes/details";

// Hash fragments. The troubleshoot page is `<parent>/<sub>`; the cluster page uses `events`
// as the parent fragment for the same Troubleshoot section.
export const TRIAGE_RULES_FRAGMENT = "all-events/triage-rules";
export const CLUSTER_TRIAGE_RULES_FRAGMENT = "events/triage-rules";
// The neighbouring sub-tab the navigation case bounces through, on the cluster page.
// Its entry is { id: 'fingerprint', text: 'Triage Inbox', fragment: 'inbox' }
// ([KubernetesDetails].jsx:300) — the id and the fragment differ here, and it is the FRAGMENT
// that reaches the URL. ClusterDetails/Troubleshoot/TroubleshootTabLocator.ts uses 'fingerprint'
// for the same tab because it clicks it by DOM id, which is the other half of the same entry.
export const CLUSTER_TRIAGE_INBOX_FRAGMENT = "events/inbox";

// navigation/Tabs.jsx puts each tabOption's own `id` on the MUI <Tab> via a11yProps, and the
// visible `text` becomes the tab's accessible name (app/src/pages/troubleshoot/index.jsx:65-71,
// app/src/pages/kubernetes/details/[KubernetesDetails].jsx:307).
export const TRIAGE_RULES_TAB_ID = "tab-triage-rules";
export const TRIAGE_RULES_TAB_NAME = "Triage Rules";
export const CLUSTER_TRIAGE_RULES_TAB_ID = "triage-rules";
export const TRIAGE_INBOX_TAB_NAME = "Triage Inbox";

// Ids the module itself renders. Every one of these was read off the component rather than
// guessed; see the rung notes in triageRulesLocators.ts for why each is used the way it is.
export const LISTING_ID = "triage-rules-list-box";
export const TOOLBAR_TESTID = "triage-rules-filter-toolbar";
export const SEARCH_ID = "triage-rules-search";
export const SEARCH_PLACEHOLDER = "Search by name";
export const CREATE_BUTTON_ID = "create-rule-btn";
export const SYSTEM_SWITCH_ID = "triage-rules-filter-include-system";
export const SYSTEM_SWITCH_LABEL = "System Rules";

// ds/FilterDropdown renders its trigger as <button id={`auto-complete-${toKebabCase(id)}`}>.
// toKebabCase only lowercases and joins on whitespace/underscore (app/src/utils/common.ts:861),
// so an already-kebab id passes through unchanged.
export const ACCOUNT_FILTER_ID = "triage-rules-filter-account";
export const RULE_TYPE_FILTER_ID = "triage-rules-filter-rule-type";
export const STATUS_FILTER_ID = "triage-rules-filter-status";
export const ACCOUNT_FILTER_LABEL = "Account";
export const RULE_TYPE_FILTER_LABEL = "Rule Type";
export const STATUS_FILTER_LABEL = "Status";

// The module renders its OWN EmptyData instead of the table whenever the filtered set is
// empty (TriageRulesManager.tsx:617), so CustomTable's `${tableId}-no-data` branch is
// unreachable here. EmptyData ids only its heading, as `${id}-no-data` (EmptyData.jsx:26).
export const EMPTY_STATE_ID = "triage-rules-empty-no-data";
export const EMPTY_STATE_HEADING = "No Data Available";

// CustomTable puts the caller's id on the <table> and `${id}-body` on the loaded <tbody>,
// swapping in an id-less skeleton body while loading (CustomTable.jsx:859).
export const TABLE_ID = "triageRulesManager";

// Column contract from the `headers` array (TriageRulesManager.tsx:629-640), for the
// single-account view — the multi-account view prepends an "Account Name" column, so these
// 1-based indices are only valid on the cluster page.
export const NAME_COLUMN = 1;
export const TYPE_COLUMN = 2;
export const STATUS_COLUMN = 6;

// RULE_TYPE_OPTIONS / STATUS_OPTIONS (TriageRulesManager.tsx:41-47). The filter label and the
// text the Type cell prints are the same string, which is what lets the filter case assert
// against the rendered rows.
export const SUPPRESSION_TYPE = "Suppression";
export const ENABLED_STATUS = "Enabled";

// The chip a system rule carries in its Name cell (TriageRulesManager.tsx:316).
export const SYSTEM_CHIP = "System";

// Modal contract — TriageRuleModal.tsx. Titles at :438, field labels at :455-497, and the
// two action buttons at :443-448.
export const CREATE_MODAL_TITLE = "Create Triage Rule";
export const RULE_NAME_LABEL = "Rule Name";
export const RULE_NAME_PLACEHOLDER = "e.g., Suppress maintenance alerts";
export const ALERT_NAME_LABEL = "Alert Name (regex)";
export const ALERT_NAME_PLACEHOLDER = "e.g., KubePodCrashLooping";
export const CREATE_SUBMIT_LABEL = "Create Rule";
export const CANCEL_LABEL = "Cancel";

// handleSubmit rejects a rule with no match criteria before it issues any request
// (TriageRuleModal.tsx:231). This exact string appears once in app/src, so a page-wide
// text match cannot resolve to another surface.
export const NO_CRITERIA_ERROR = "Please specify at least one match criterion";
// The success toast a create raises (TriageRuleModal.tsx:280).
export const CREATE_SUCCESS = "Rule created successfully";

// The delete confirmation is a ds/Modal with confirmText='Delete' (TriageRulesManager.tsx:515).
export const DELETE_MENU_ITEM = "Delete";
export const DELETE_CONFIRM_LABEL = "Delete";
// The row action menu's trigger carries this aria-label (TriageRulesManager.tsx:210).
export const ROW_MENU_LABEL = "More actions";

// SnackbarComponent renders every toast inside one labelled region
// (app/src/components/common/SnackbarComponent.tsx:175-176).
export const TOAST_REGION_LABEL = "Notifications";

// Long because a case may log in, resolve the account, deep-link, wait out the rules fetch,
// and then drive a create-and-delete round trip against the shared dev tenant.
export const SPEC_TIMEOUT_MS = 240000;
