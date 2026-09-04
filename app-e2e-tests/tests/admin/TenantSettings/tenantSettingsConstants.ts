// Not for OSS

// Shared literals for the Admin -> Tenant Settings suite. Every string here is
// copied from the component that renders it, so a UI reword is one edit.

// Per-test budget. /user-management boots slowly on dev — the tab body only
// mounts after the session resolves, and this tab then fetches tenant
// attributes, the feature catalog and the tenant list before it paints.
export const SPEC_TIMEOUT_MS = 180000;

// app/src/pages/user-management/index.jsx — baseFilters entry for this tab.
export const TENANT_SETTINGS_PATH = "/user-management#tenant-settings";
export const TENANT_SETTINGS_TAB_NAME = "Tenant Settings";

// app/src/components/common/settings/TenantSettings.jsx — top-level Tabs.
export const TAB_GENERAL = "General";
export const TAB_LABEL_MAPPING = "Label Mapping";
export const TAB_FEATURES = "Features";

// The `ariaLabel` each Tabs instance sets, which is what names its tablist.
export const TABLIST_TENANT_SETTINGS = "Tenant settings";
export const TABLIST_LABEL_MAPPING = "Label mapping";
export const TABLIST_FEATURE_GROUPS = "Feature groups";

// Label Mapping sub-tabs.
export const SUBTAB_LOGS = "Logs";
export const SUBTAB_TRACES = "Traces";
export const SUBTAB_WEBHOOK = "Webhook alerts";

// General tab field labels — Input renders each as a <label for>, so these are
// the accessible names the locators match on.
export const FIELD_TENANT_NAME = "Tenant Name";
export const FIELD_ALLOWED_DOMAINS = "Allowed Domains";
export const FIELD_DEFAULT_AUTH_ROLE = "Default Auth Role";
export const CHECKBOX_SELF_ONBOARDING = "Allow self-onboarding via domain login";

// Logs sub-tab: TenantAccountCommonSettings renders `log-label-<field>` as both
// id and data-testid, from LOG_LABEL_FIELDS in labelMapperFields.jsx.
export const LOG_LABEL_TESTIDS = ["log-label-logPodLabel", "log-label-logNamespaceLabel", "log-label-logAppLabel", "log-label-logDefaultQuery"];
export const FIELD_CLUSTER_LABEL = "Cluster Label";

// Traces sub-tab: the five TRACE_LABEL_FIELDS, same `trace-label-<field>` shape.
export const TRACE_LABEL_TESTIDS = [
  "trace-label-service_name",
  "trace-label-workload_name",
  "trace-label-span_name",
  "trace-label-duration_ns",
  "trace-label-status_code",
];
// TRACE_LABEL_ADVANCED_FIELDS sits behind the disclosure toggle.
export const TRACE_ADVANCED_TESTID = "trace-label-toggle-advanced";
export const TRACE_ADVANCED_PANEL_ID = "trace-label-advanced";
export const TRACE_ADVANCED_FIRST_TESTID = "trace-label-trace_id";
export const TRACE_ADVANCED_SHOW_TEXT = "Show advanced trace fields";
export const TRACE_ADVANCED_HIDE_TEXT = "Hide advanced trace fields";

// Webhook alerts sub-tab — FilterDropdown labels.
export const WEBHOOK_DROPDOWN_LABELS = ["Subject Name Labels", "Namespace Labels", "Severity Labels"];

// Features tab.
export const FEATURES_TABLE_ID = "tenant-features-table";
export const FEATURE_TABLE_HEADERS = ["Feature", "When to turn it on", "Why / notes", "Toggle"];
export const FEATURES_ALL_TAB = "All";
export const SHOW_FLAG_IDS_LABEL = "Show flag ids";
export const HIDE_FLAG_IDS_LABEL = "Hide flag ids";

// Toast copy from handleSaveSettings — the two validation guards return before
// any network write, which is what makes the negative cases safe to run against
// a shared tenant.
export const SAVE_SUCCESS_TEXT = "Tenant Settings saved successfully";
export const ERROR_EMPTY_DOMAINS = "Allowed Domains field cannot be empty when domain login is enabled.";
export const ERROR_INVALID_ROLE = "Invalid role. Allowed roles are 'tenant_Admin' or 'tenant_admin_readonly'.";

// A role string no VALID_ROLES entry can match, so the rejection is about the
// guard rather than about what the tenant happens to have stored.
export const INVALID_AUTH_ROLE = "zz-not-a-role";

// missingPermissionMessage('tenants:Write') in app/src/lib/auth.tsx — the banner
// a viewer without the grant sees in place of every Save button.
export const READ_ONLY_BANNER_TEXT = 'You need the "tenants:Write" permission. Ask an admin to grant it.';
