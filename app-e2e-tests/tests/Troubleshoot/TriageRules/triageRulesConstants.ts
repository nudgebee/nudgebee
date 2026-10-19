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
// Not used as a fixed index — the multi-account view's prepended Account Name column shifts it
// to 8 there, so matchesCellFor() reads this label off the table's own header row instead of
// trusting a hardcoded position. Confirmed live: the header cell prints exactly this text.
export const MATCHES_COLUMN_LABEL = "Matches";

// RULE_TYPE_OPTIONS / STATUS_OPTIONS (TriageRulesManager.tsx:41-47). The filter label and the
// text the Type cell prints are the same string, which is what lets the filter case assert
// against the rendered rows.
export const SUPPRESSION_TYPE = "Suppression";
export const SCORING_TYPE = "Scoring";
export const CLASSIFICATION_TYPE = "Classification";
export const ENABLED_STATUS = "Enabled";
// Also what a system rule's override shows — "Disabled (Override)" — so a Disabled-filter
// assertion should check the cell CONTAINS this, not that it equals it exactly.
export const DISABLED_STATUS = "Disabled";

// The chip a system rule carries in its Name cell (TriageRulesManager.tsx:316).
export const SYSTEM_CHIP = "System";

// Modal contract — TriageRuleModal.tsx. Titles at :438, field labels at :455-497, and the
// two action buttons at :443-448.
export const CREATE_MODAL_TITLE = "Create Triage Rule";
export const EDIT_MODAL_TITLE = "Edit Triage Rule";
export const RULE_NAME_LABEL = "Rule Name";
export const RULE_NAME_PLACEHOLDER = "e.g., Suppress maintenance alerts";
export const ALERT_NAME_LABEL = "Alert Name (regex)";
export const ALERT_NAME_PLACEHOLDER = "e.g., KubePodCrashLooping";
// The Scoring tab's only Action field. Verified live: renders as role="spinbutton" (it's a
// number input, not role="textbox" like the other fields), accessible name is this label.
export const SCORE_ADJUSTMENT_LABEL = "Score Adjustment";
// The Classification tab's second Action radio. Verified live: TriageRuleModal.tsx wraps each
// option's title AND its description Typography inside the same FormControlLabel, so the radio's
// accessible name is "Duplicate Auto-classify as duplicate" — this label is only the start of it.
export const DUPLICATE_ACTION_LABEL = "Duplicate";
// The raw action value TriageRuleModal.tsx stores for that radio (CLASSIFICATION_ACTIONS), which
// TriageRulesManager.tsx's getActionDisplay prints verbatim as `Auto-classify: ${rule.action}`.
export const DUPLICATE_ACTION_VALUE = "auto_classify_duplicate";
// The Expiration block (:569) is collapsed by default in both Create and Edit — its own toggle
// is a bare MUI IconButton with no text, aria-label or testid (BlockWithHeading.tsx:188), so it
// has to be reached by anchoring on this exact heading text rather than by role+name. Verified
// live (browser_run_code_unsafe) that anchor.locator("xpath=..").getByRole("button") resolves to
// exactly one button before the section expands.
export const EXPIRATION_HEADING = "Expiration (Optional)";
// CustomDateTimePicker.jsx renders the label as a plain, unassociated <label> (no htmlFor), so
// the input's accessible name is its own format mask ("mm/dd/yyyy hh:mm (a|p)m"), not this text
// — not usable as a role+name locator. The id it sets directly (TriageRuleModal.tsx:574) is used
// alone instead.
export const EXPIRES_AT_FIELD_ID = "triage-expires-at";
// The listing has no column for expiration (TriageRulesManager.tsx never reads effective_until),
// so a create-and-reopen-Edit round trip is the only way to observe this field at all. Far enough
// in the future that it can never fall inside this rule's own brief lifetime.
export const EXPIRES_AT_VALUE = "12/31/2099 11:59 PM";
// The Apply to Existing Events switch (:544) is bare — no label/aria-label prop — and its
// description text sits in a sibling Typography two levels up, not a wrapping FormControlLabel.
// Verified live: the switch's aria-label/aria-labelledby are both null, so it is reached by
// anchoring on this exact text and taking its ancestor's checkbox descendant.
export const APPLY_TO_EXISTING_LABEL = "Apply this rule to existing events";
// renderPreviewContent's template (:392) collapses to this exact string when
// preview.matching_events_count is 0. Verified live with an inert Alert Name pattern.
export const ZERO_EVENTS_PREVIEW = "0 events will be affected";
export const CREATE_SUBMIT_LABEL = "Create Rule";
export const CANCEL_LABEL = "Cancel";

// handleSubmit rejects a rule with no match criteria before it issues any request
// (TriageRuleModal.tsx:231). This exact string appears once in app/src, so a page-wide
// text match cannot resolve to another surface.
export const NO_CRITERIA_ERROR = "Please specify at least one match criterion";
// The success toasts handleSubmit raises (TriageRuleModal.tsx:280).
export const CREATE_SUCCESS = "Rule created successfully";
// The bulk-apply suffix (:285) only appends when events_to_update is truthy — 0 is falsy, so a
// rule whose match criteria hit nothing real gets no suffix at all. Verified live.
export const APPLIED_TO_SUFFIX = "Applied to";
export const UPDATE_SUCCESS = "Rule updated successfully";

// Row action menu items (TriageRulesManager.tsx getMenuItems, :166-179). The Disable/Enable
// label flips on the rule's own `enabled` field, so the same menu item reads "Disable" the
// first time and "Enable" once the rule is off.
export const EDIT_MENU_ITEM = "Edit";
export const DISABLE_MENU_ITEM = "Disable";
export const ENABLE_MENU_ITEM = "Enable";
// The delete confirmation is a ds/Modal with confirmText='Delete' (TriageRulesManager.tsx:515).
export const DELETE_MENU_ITEM = "Delete";
export const DELETE_CONFIRM_LABEL = "Delete";
export const DISABLE_CONFIRM_LABEL = "Disable";
// A system rule's row menu has exactly one item, whose own label flips on `is_overridden`
// (TriageRulesManager.tsx:152) — a different pair of strings from the regular Disable/Enable
// above, since toggling a system rule writes an account-level override row, not the rule itself.
export const SYSTEM_RULE_DISABLE_MENU_ITEM = "Disable for this Account";
export const SYSTEM_RULE_ENABLE_MENU_ITEM = "Enable System Rule";
// Seeded on every tenant by migration V637_event_triage_system_rule.up.sql — global
// (account_id/tenant_id both NULL) and can_override: true, so it is a safe, deterministic target
// for the override-toggle case without this suite creating its own system rule.
export const SEEDED_DUPLICATE_SYSTEM_RULE_NAME = "System: Auto-mark Duplicates";
// Seeded on every tenant by migration V762_seed_service_tier_scoring_rules.up.sql — global,
// can_override: true. Matched against real, continuously-generated fixture traffic (the
// benchmark-scenarios namespace), so unlike the other seeded Service Tier rules it reliably
// carries a non-zero Matches count — the property this test exists to lean on.
export const SEEDED_CLICKHOUSE_SCORING_RULE_NAME = "Service Tier 1: clickhouse";
// The row's own expand/collapse control (CustomTable's showExpandable). Only ever clicked from
// its collapsed state here, so the flipped "Collapse row" label is never the target.
export const EXPAND_ROW_LABEL = "Expand row";
// A recurring PagerDuty test-alert integration check (source: pagerduty_webhook), not a seeded
// or code-guaranteed fixture like the Service Tier rules above — found live while verifying
// #38740's fix (46 open events under this exact title on the dev tenant at the time). Because its
// continued presence isn't guaranteed the way a migration-seeded row is, the test that uses it
// SKIPS on an empty precondition rather than hard-failing.
export const RETRO_MATCH_FIXTURE_TITLE = "e2ealerttest";
// The row action menu's trigger carries this aria-label (TriageRulesManager.tsx:210).
export const ROW_MENU_LABEL = "More actions";

// One fixed name per test, not a timestamped/random one — mirrors CreateGroups.spec.ts. Safe here
// because every test below deletes the rule it creates in a finally block (unlike Groups, which
// has no delete and needs the fixed-pool/find-or-create pattern for that reason). Each name is
// unique across this file so no two tests' searches can ever resolve to each other's row.
export const CRUD_ROUND_TRIP_RULE_NAME = "E2E Triage Rule - CRUD Round Trip";
export const CROSS_SCREEN_RULE_NAME = "E2E Triage Rule - Cross Screen";
export const STATUS_FILTER_RULE_NAME = "E2E Triage Rule - Status Filter";
export const SCORING_RULE_NAME = "E2E Triage Rule - Scoring";
export const CLASSIFICATION_RULE_NAME = "E2E Triage Rule - Classification";
export const EXPIRATION_RULE_NAME = "E2E Triage Rule - Expiration";
export const APPLY_EXISTING_NEGATIVE_RULE_NAME = "E2E Triage Rule - Apply Existing Negative";
export const VALIDATION_RULE_NAME = "E2E Triage Rule - Validation";
export const CANCEL_RULE_NAME = "E2E Triage Rule - Cancel";
export const RETRO_MATCH_RULE_NAME = "E2E Triage Rule - Retro Match";
// Deliberately nonsensical, so it can never collide with a real rule or any fixed name above.
export const NO_SUCH_RULE_NAME = "E2E Triage Rule - Definitely Does Not Exist";

// SnackbarComponent renders every toast inside one labelled region
// (app/src/components/common/SnackbarComponent.tsx:175-176).
export const TOAST_REGION_LABEL = "Notifications";

// Long because a case may log in, resolve the account, deep-link, wait out the rules fetch,
// and then drive a create-and-delete round trip against the shared dev tenant.
export const SPEC_TIMEOUT_MS = 240000;
