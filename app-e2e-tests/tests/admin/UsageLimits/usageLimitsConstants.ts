// Not for OSS

// Shared literals for the Admin -> AI & Tools -> Budgets & Limits -> Usage &
// Limits suite. Every string here is copied from the component that renders it,
// so a UI reword is one edit.

// Per-test budget. /user-management boots slowly on dev — the tab body only
// mounts after the session resolves, and LLMConsumptionTab then fetches budget
// status, the tenant and account config lists and the system defaults before it
// paints. Matches the budget tests/admin/TenantSettings already runs with.
export const SPEC_TIMEOUT_MS = 180000;

// The CRUD case pays that boot three times: once to create, once after the
// reload that proves the write persisted, and once after the reload that proves
// the delete did.
export const CRUD_TIMEOUT_MS = 540000;

// app/src/pages/user-management/index.jsx — the AI & Tools entry sets id:'AITools',
// and app/src/components/llm/admin/aiToolsConfig.js gives this sub-tab the
// 'budgets-limits' fragment. AnchorComponent routes '#<parent>/<child>'.
export const BUDGETS_LIMITS_PATH = "/user-management#ai-tools/budgets-limits";
export const AI_TOOLS_TAB_ID = "anchor-tab-AITools";
export const BUDGETS_LIMITS_TAB_NAME = "Budgets & Limits";

// BudgetsAndLimitsAdminTab.jsx renders these two as a single-selection
// ToggleGroup whose ariaLabel repeats the tab name.
export const TOGGLE_GROUP_NAME = "Budgets & Limits";
export const TOGGLE_USAGE_LIMITS = "Usage & Limits";
export const TOGGLE_MODEL_PRICING = "Model Pricing";

// LLMConsumptionTab.jsx header copy. The period itself is whatever month the
// run lands in, so only the stem is pinned.
export const PERIOD_HEADING_PREFIX = /^Usage for /;
export const ADD_BUDGET_BUTTON = "Add Budget";
export const ACTIVE_BUDGETS_HEADING = "Active Budgets";

// ActiveConfigsCompact's emptyMessage for the Active Budgets section. Rendered
// instead of the scope rows when neither the tenant nor the current account has
// a custom config, which is a legitimate state for a shared tenant.
export const NO_ACTIVE_CONFIGS_TEXT = "No custom configurations for tenant or this account. System defaults are being applied.";

// The collapsible section holding every account-scoped config. Admin mounts
// LLMConsumptionTab with no accountId, so it takes the tenant-wide wording.
export const OTHER_ACCOUNTS_HEADING = "Budgets for All Accounts";

// The section writes no aria-expanded, but it swaps its chevron, and MUI stamps
// every icon component with data-testid="<Name>Icon" — the one rung-1 handle
// anywhere on this surface.
export const ICON_CHEVRON_UP = "KeyboardArrowUpIcon";
export const ICON_CHEVRON_DOWN = "KeyboardArrowDownIcon";

// ActiveConfigChip's copy for a saved config with every limit switched off. Its
// counterpart, the dashed "system defaults" placeholder a module with no config
// at all renders, is what makes this string proof that a record exists.
export const NO_LIMITS_TEXT = "no limits enabled";

// getModuleLabels() in LLMConsumptionTab.jsx. Only the investigation label is
// pinned: the user_investigation one interpolates the tenant's assistant name
// from branding, so it is not a constant across environments. Every case here
// therefore works the investigation module.
export const MODULE_LABEL_EVENT_ANALYSIS = "Event Analysis";

// BudgetEditModal.jsx — ds/Modal titles, ds/Select labels and the Apply-to
// option copy. The option labels are hardcoded in the component (unlike the
// chip labels above), so they are safe to pin.
export const CREATE_DIALOG_TITLE = "Create Budget Configuration";
export const DELETE_DIALOG_TITLE = "Delete Budget Configuration";
export const SELECT_SCOPE = "Scope";
export const SELECT_ACCOUNT = "Account";
export const SELECT_APPLY_TO = "Apply to";
export const FIELD_TENANT = "Tenant";
export const SCOPE_TENANT = "Tenant";
export const SCOPE_ACCOUNT = "Account";
export const APPLY_BOTH_MODULES = "Both Modules";
export const APPLY_EVENT_ANALYSIS = "Event Analysis (Automated)";

// The four limit rows, by the label their Switch renders.
export const LIMIT_MONTHLY_COST = "Monthly Cost";
export const LIMIT_DAILY_COST = "Daily Cost";
export const LIMIT_MONTHLY_COUNT = "Monthly Count";
export const LIMIT_DAILY_COUNT = "Daily Count";

// Section headings between the two pairs of limit rows.
export const COST_LIMITS_HEADING = "Cost Limits (USD)";
export const COUNT_LIMITS_HEADING = "Count Limits (Conversations)";

// Dialog action buttons.
export const BUTTON_CREATE = "Create";
export const BUTTON_CANCEL = "Cancel";
export const BUTTON_DELETE = "Delete";

// The delete control on an ActiveConfigChip — ds/Button forwards aria-label onto
// its ButtonBase, so this is a real accessible name. It is also what marks a chip
// as carrying a config: the dashed placeholder for an unconfigured module renders
// neither this button nor its Edit sibling.
export const ARIA_DELETE_CONFIG = "Delete budget config";

// handleSave's guard for an account scope submitted with nothing picked. It
// runs before the duplicate-budget check and before validateLimits, which is
// what makes it the one rejection that does not depend on what the shared
// tenant already holds.
export const ERROR_NO_ENTITY = "Please select an entity";

// Toast copy from handleSave / handleDeleteConfirmed for the single-module path.
export const TOAST_SAVED = "Budget configuration saved";
export const TOAST_DELETED = "Budget config deleted — system defaults will apply";

// How many accounts the CRUD case will try before giving up on finding one with
// no Event Analysis budget. Bounded so a tenant where every account is already
// configured fails with a readable message instead of walking the whole list.
export const MAX_ACCOUNT_ATTEMPTS = 6;
