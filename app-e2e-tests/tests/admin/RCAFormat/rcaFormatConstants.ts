// Not for OSS

// Shared literals for the Admin -> AI & Tools -> RCA Format suite. Every string
// here is copied from the component that renders it, so a UI reword is one edit.

// Per-test budget. /user-management boots slowly on dev — the tab body only
// mounts after the session resolves, RCAFormatAdminTab then lazy-loads the
// CodeMirror bundle, and AccountSelect fetches the account list before the
// picker can be opened. Matches the budget tests/admin/UsageLimits runs with.
export const SPEC_TIMEOUT_MS = 180000;

// The persistence case pays that boot twice — once to write, once after the
// reload that proves the write landed — plus the restore save.
export const WRITE_TIMEOUT_MS = 540000;

// app/src/pages/user-management/index.jsx — the AI & Tools entry sets
// id:'AITools', and app/src/components/llm/admin/aiToolsConfig.js gives this
// sub-tab the 'rca-format' fragment. AnchorComponent routes '#<parent>/<child>'.
export const RCA_FORMAT_PATH = "/user-management#ai-tools/rca-format";
export const AI_TOOLS_TAB_ID = "anchor-tab-AITools";
export const RCA_FORMAT_TAB_NAME = "RCA Format";

// The sibling AI & Tools sub-tab the navigation case leaves to and returns from.
export const BUDGETS_LIMITS_TAB_NAME = "Budgets & Limits";

// Tabs.jsx stamps every sub-tab with a11yProps(opt.value, opt.id), taking the id
// verbatim when the option carries one — and AI_TOOLS_SUB_TABS does, surviving
// user-management's `{ ...sub, value: subIdx }` spread. So unlike ds/Tabs'
// positional `#tab-${index}` these do not shift when a feature flag drops the
// Functions or Gateway sub-tab, which makes them a real fallback rung.
export const RCA_FORMAT_TAB_ELEMENT_ID = "rca-format";
export const BUDGETS_LIMITS_TAB_ELEMENT_ID = "budgets-limits";

// RCAFormatAdminTab.jsx's EmptyState copy, rendered until an account is picked.
// Unlike Agents/Tools/Functions, this sub-tab has no tenant-wide view, so the
// picker is a required precondition rather than an optional narrowing.
export const EMPTY_STATE_TITLE = "Select an account";
export const EMPTY_STATE_DESCRIPTION = "RCA Format is configured per account — pick one above to view or edit its template.";

// RCAFormatTab.jsx header copy, rendered once an account is picked.
export const RCA_HEADER = "Root Cause Analysis (RCA) Format";
export const RCA_SUBHEADER = "Customize the Markdown template used by AI to generate RCA documents for your events.";
export const SAVE_BUTTON = "Save Changes";

// handleSave's toast copy. The failure branches are pinned too so a save that
// reported an error cannot pass for one that reported success.
export const TOAST_SAVED = "RCA Format updated successfully!";
export const TOAST_SAVE_FAILED = "Failed to update RCA Format.";
export const TOAST_LOAD_FAILED = "Failed to load RCA Format.";

// AccountSelect passes ds/Select no explicit id, so it falls back to its own
// 'account-select' default (app/src/components/ownership/AccountSelect.jsx).
export const ACCOUNT_SELECT_ID = "account-select";

// AdminAccountFilter renders the required variant with this placeholder, which
// is also what the trigger shows while nothing is picked.
export const ACCOUNT_PLACEHOLDER = "Select an account";

// ds/Select's OverlaySearch default placeholder (internal/Overlay.tsx). The
// character is U+2026, not three dots.
export const OVERLAY_SEARCH_PLACEHOLDER = "Search…";

// ds/Select's own empty copy for a search that matched nothing, as opposed to a
// picker that was offered no accounts at all.
export const NO_RESULTS_TEXT = "No results found";

// How many accounts the search case will sample when building its negative
// expectation. Bounded so a tenant with a long account list does not turn one
// assertion into a walk of the whole picker.
export const MAX_SAMPLED_ACCOUNTS = 8;
