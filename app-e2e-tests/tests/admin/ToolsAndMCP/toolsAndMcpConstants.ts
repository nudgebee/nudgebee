// Not for OSS

// Shared literals for the Admin -> AI & Tools -> Tools & MCP suite. Every string
// here is copied from the component that renders it, so a UI reword is one edit.

// Per-test budget. /user-management boots slowly on dev — the tab body only
// mounts after the session resolves, and ListTools then fetches the tool
// catalogue before it paints. Matches the budget tests/admin/UsageLimits and
// tests/admin/TenantSettings already run with.
export const SPEC_TIMEOUT_MS = 180000;

// The CRUD case pays that boot twice: once to create, once after the reload that
// proves the write persisted, plus the delete round-trip.
export const CRUD_TIMEOUT_MS = 540000;

// app/src/pages/user-management/index.jsx — the AI & Tools entry sets id:'AITools'
// and fragment:'ai-tools', and app/src/components/llm/admin/aiToolsConfig.js gives
// this sub-tab the 'tools-mcp' fragment. AnchorComponent routes '#<parent>/<child>'.
export const TOOLS_MCP_PATH = "/user-management#ai-tools/tools-mcp";
export const AI_TOOLS_TAB_ID = "anchor-tab-AITools";
export const TOOLS_MCP_TAB_NAME = "Tools & MCP";

// The sub-tab's own id, from AI_TOOLS_SUB_TABS. Tabs.jsx passes it through
// a11yProps(opt.value, opt.id), which prefers the option's id over the positional
// `tab-${index}` — so this survives a feature flag dropping a sibling sub-tab.
export const TOOLS_MCP_TAB_FRAGMENT = "tools-mcp";

// ToolsAndMCPAdminTab.jsx renders these two as a single-selection ToggleGroup
// whose ariaLabel repeats the tab name.
export const TOGGLE_GROUP_NAME = "Tools & MCP";
export const TOGGLE_TOOLS = "Tools";
export const TOGGLE_MCP = "MCP Servers";

// ListingLayout / CustomTable ids from ListTools.jsx and MCPConfigList.jsx.
// CustomTable derives its body id as `${id}-body` and its empty-state heading id
// as `${id}-no-data` (EmptyData.jsx), which is the one stable handle either
// listing gives us for "the fetch landed and returned nothing".
export const TOOLS_LISTING_ID = "all-tools";
export const TOOLS_TABLE_BODY_ID = "tools-body";
export const TOOLS_EMPTY_ID = "tools-no-data";
export const MCP_LISTING_ID = "mcp-config-list";
export const MCP_TABLE_BODY_ID = "mcp-config-body";
export const MCP_EMPTY_ID = "mcp-config-no-data";

// ListTools.jsx toolbar control ids. SearchInput renders <input id={id}> and
// FilterDropdown renders its trigger as <button id={`auto-complete-${id}`}>.
export const TOOL_SEARCH_ID = "tool-search";
export const TOOL_SEARCH_PLACEHOLDER = "Search Tool";
export const CREATED_BY_FILTER_ID = "auto-complete-tool-created-by-filter";
export const STATUS_FILTER_ID = "auto-complete-tool-status-filter";
export const CREATE_TOOL_BTN_ID = "create-tool";

// MCPConfigList.jsx toolbar control id.
export const MCP_STATUS_FILTER_ID = "auto-complete-mcp-config-status-filter";

// CREATED_BY_OPTIONS in ListTools.jsx — a fixed pair, not derived from data.
export const CREATED_BY_SYSTEM = "System Generated";
export const CREATED_BY_USER = "User Created";

// STATUS_OPTIONS in MCPConfigList.jsx. The Tools status filter is derived from
// whatever statuses the loaded tools carry, so its options are read at runtime
// rather than pinned here.
export const MCP_STATUS_ENABLED = "Enabled";
export const MCP_STATUS_DISABLED = "Disabled";

// OwnerTypeBadge.jsx renders `${brandTitle} System` for a system tool and the
// fixed userLabel for a custom one. Only the custom label is a constant — the
// system half interpolates the tenant's branding title, so it is matched by the
// word "System" instead of being pinned.
// Both are matched case-insensitively, and that is load-bearing rather than
// defensive: the badge carries `text-transform: uppercase`, and innerText()
// applies CSS transforms, so the cell really reads "USER CREATED".
export const BADGE_USER_CREATED = /user created/i;
export const BADGE_SYSTEM_WORD = /system/i;

// Column headers ListTools.jsx renders. The tenant-wide read (no account picked)
// swaps NB Tool Type + Actions for a single Account column, which is what makes
// these two sets the proof that the account filter actually changed the mount.
export const HEADER_NAME = "Name";
export const HEADER_DESCRIPTION = "Description";
export const HEADER_STATUS = "Status";
export const HEADER_ACCOUNT = "Account";
export const HEADER_NB_TOOL_TYPE = "NB Tool Type";
export const HEADER_ACTIONS = "Actions";

// MCPConfigList.jsx HEADERS.
export const MCP_HEADER_CONNECTION = "Connection";
export const MCP_HEADER_CREATED_BY = "Created By";

// The read-only Banner ToolsAndMCPAdminTab.jsx renders over the MCP sub-view.
// ds/Banner gives an 'info' tone role="status".
export const MCP_BANNER_TEXT = "This view is read-only.";
export const MCP_BANNER_ACTION = "Manage in Integrations";

// AdminAccountFilter -> AccountSelect passes id='account-select' and, without
// `required`, prepends a real "All accounts" entry rather than a placeholder.
export const ACCOUNT_SELECT_ID = "account-select";
export const ALL_ACCOUNTS_LABEL = "All accounts";

// CreateTool.jsx — ds/Modal titles, ds/Input labels and the action buttons.
export const CREATE_DIALOG_TITLE = "Add Tool";
export const FIELD_NAME = "Name";
export const FIELD_DESCRIPTION = "Description";
export const FIELD_CONTAINER_IMAGE = "Container Image";
export const BUTTON_SUBMIT = "Submit";
export const BUTTON_CANCEL = "Cancel";
export const BUTTON_DELETE = "Delete";

// Validation copy. The name message comes from getLlmIdentifierValidationMessage
// in app/src/utils/common.ts; the other two are CreateTool.jsx's own guards. All
// three run client-side inside validateForm, before any network call — which is
// what makes the negative case independent of what the shared tenant holds.
export const ERROR_NAME_REQUIRED = "Name is required.";
export const ERROR_DESCRIPTION_EMPTY = "Description cannot be empty.";
export const ERROR_CONTAINER_IMAGE_EMPTY = "Container image cannot be empty.";
export const ERROR_NAME_MUST_START_WITH_LETTER = "Name must start with a letter (a-z or A-Z).";

// Toast copy from CreateTool.jsx's handleSubmit.
export const TOAST_CREATED = "Tool created successfully";

// The per-row delete control ListTools.jsx renders for a custom tool. ds/Button
// forwards aria-label onto its ButtonBase, so this is a real accessible name.
export const ARIA_DELETE_TOOL = "Delete tool";

// The image the CRUD case registers. A tool row is a definition only — nothing
// executes it unless an agent invokes it by name — and this one is deleted in the
// same test, so a public base image keeps the record inert and recognisable.
export const CRUD_TOOL_IMAGE = "alpine:latest";

// The delete confirmation ds/Modal's title stem. The tool name is interpolated
// after it through snakeToTitleCase, whose UPPERCASE_ACRONYMS set rewrites parts
// like "mcp" to "MCP" — so the dialog is matched on the stem alone rather than on
// a display name this suite would have to reproduce.
export const DELETE_DIALOG_TITLE_STEM = "Delete Tool:";

// Name stem for entities this suite creates. getLlmIdentifierValidationMessage
// allows only [a-zA-Z]\w* — no hyphens — so the unique suffix is underscored.
export const CRUD_TOOL_NAME_STEM = "e2e_tools_mcp";

// How long the MCP listing gets to finish a server-side refetch and settle on a
// consistent row set. Longer than the client-side budget below because it covers
// a real round trip, and paid only on the failure path — the success path
// resolves on the first retry.
export const MCP_REFETCH_TIMEOUT_MS = 60000;

// How long the tools table gets to repaint after a client-side filter change.
// The filter is a useEffect over already-loaded rows, so this only has to cover
// a React commit, not a fetch.
export const FILTER_SETTLE_MS = 20000;

// Accessible name for the Create Tool button. It also carries an id, so this is
// the fallback rung, not the primary.
export const CREATE_TOOL_BTN_NAME = "Create Tool";
