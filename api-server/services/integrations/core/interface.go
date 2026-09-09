package core

import "nudgebee/services/security"

type IntegrationCategory string

const (
	IntegrationCategoryMessagingQueue        IntegrationCategory = "messaging_queue"
	IntegrationCategoryDatabase              IntegrationCategory = "database"
	IntegrationCategoryLog                   IntegrationCategory = "log"
	IntegrationCategoryTrace                 IntegrationCategory = "trace"
	IntegrationCategoryMetrics               IntegrationCategory = "metrics"
	IntegrationCategoryIncidentWebhook       IntegrationCategory = "incident_webhook"
	IntegrationCategoryDocs                  IntegrationCategory = "docs"
	IntegrationCategoryObservabilityPlatform IntegrationCategory = "observability_platform"
	IntegrationCategoryCICD                  IntegrationCategory = "ci_cd"
	IntegrationLLM                           IntegrationCategory = "llm"
	IntegrationCategoryTicketing             IntegrationCategory = "ticketing"
	IntegrationCategoryProxy                 IntegrationCategory = "proxy"
	IntegrationCategoryMessaging             IntegrationCategory = "messaging"
)

// IsTenantScoped reports whether integrations of this category bind to a
// tenant directly rather than to a (tenant, cloud_account) pair. Tenant-scoped
// integrations accept empty accountIds in CreateIntegrationConfig.
func (c IntegrationCategory) IsTenantScoped() bool {
	switch c {
	case IntegrationCategoryTicketing, IntegrationCategoryMessaging:
		return true
	}
	return false
}

type Integration interface {
	Name() string
	Category() IntegrationCategory
	ValidateConfig(ctx *security.SecurityContext, values []IntegrationConfigValue, accountId string) []error
	ConfigSchema() IntegrationSchema
}

// TestableIntegration is an optional capability an Integration may implement to
// provide a real live-connectivity probe distinct from structural ValidateConfig.
// TestIntegrationConnectionByConfig will type-assert to this interface after
// ValidateConfig passes and invoke TestConnection if present. Implementations
// MUST NOT mutate the configuration and SHOULD be fast (a few seconds at most).
type TestableIntegration interface {
	TestConnection(ctx *security.RequestContext, values []IntegrationConfigValue, accountId string) error
}

// TenantScopedIntegration is an optional capability an Integration may implement to
// declare that it binds to the tenant directly and needs NO cloud-account mapping (no
// account_id). The generic create + connection-test paths skip the account_id requirement
// for these — e.g. llm_gateway resolves credentials per-tenant and has no account concept.
// This is the per-integration counterpart to IntegrationCategory.IsTenantScoped (which
// scopes whole categories like ticketing/messaging); an integration is treated as
// tenant-scoped if EITHER says so. Default (interface not implemented) = account-scoped,
// so existing integrations are unaffected.
type TenantScopedIntegration interface {
	TenantScoped() bool
}

// ConfigNormalizer is an optional capability an Integration may implement to
// rewrite user input into the canonical form every consumer of the stored
// config reads (e.g. a pasted page URL into the page ID). CreateIntegrationConfig
// invokes it on decrypted values before ValidateConfig, and persists the
// rewritten values. Implementations must be idempotent: canonical input must
// pass through unchanged, because the hook runs once per account.
type ConfigNormalizer interface {
	NormalizeConfig(ctx *security.SecurityContext, values []IntegrationConfigValue) error
}

type IntegrationSchemaType string

const (
	ToolSchemaTypeString  IntegrationSchemaType = "string"
	ToolSchemaTypeInteger IntegrationSchemaType = "integer"
	ToolSchemaTypeNumber  IntegrationSchemaType = "number"
	ToolSchemaTypeBoolean IntegrationSchemaType = "boolean"
	ToolSchemaTypeObject  IntegrationSchemaType = "object"
	ToolSchemaTypeArray   IntegrationSchemaType = "array"
)

type IntegrationSchemaProperty struct {
	Type IntegrationSchemaType `json:"type"`
	// DisplayName overrides the field label, which otherwise title-cases the
	// property key. Use it when the storage key is not the clearest name for
	// the user (e.g. page_trees -> "Limit to pages"); the key itself is a
	// stored contract and must not be renamed to improve wording.
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	// SearchPlaceholder overrides the placeholder inside a picker's search box.
	// Only meaningful on fields rendered as a dropdown.
	SearchPlaceholder string         `json:"search_placeholder,omitempty"`
	Items             map[string]any `json:"items,omitempty"`
	Enum              []any          `json:"enum,omitempty"`
	Default           any            `json:"default,omitempty"`
	Pattern           string         `json:"pattern,omitempty"`
	IsEncrypted       bool           `json:"is_encrypted,omitempty"`
	AutoGenerateFunc  string         `json:"auto_generate_func,omitempty"`
	// DependsOn lists other field names whose values are inputs to
	// AutoGenerateFunc. The frontend watches these fields and refetches the
	// autogen options when any of them changes. Used for cascading dropdowns
	// (e.g. column-name suggestions that depend on host/database/table).
	DependsOn    []string       `json:"depends_on,omitempty"`
	RequiredWhen map[string]any `json:"required_when,omitempty"`
	ShowWhen     map[string]any `json:"show_when,omitempty"`
	Priority     int            `json:"priority,omitempty"`
	AllowEdit    bool           `json:"allow_edit,omitempty"`
	Hidden       bool           `json:"hidden,omitempty"`
	Multiline    bool           `json:"multiline,omitempty"`
	// Widget names a custom frontend renderer for this field, overriding the default
	// input for its Type (which is unchanged, so the stored value + validation still
	// apply). E.g. "model_alias_list" renders a string field of comma-joined
	// `alias=served` entries as a two-column name → served-model editor.
	Widget     string `json:"widget,omitempty"`
	IsTestable bool   `json:"is_testable,omitempty"`
	// SingleSelect, on an array-typed property with auto_generate_func='listAccounts',
	// tells the frontend to render a single-select dropdown instead of the default
	// multi-select. Used for integrations that bind 1:1 to an account (e.g.
	// workflow_webhook, which is bound to one workflow per row).
	SingleSelect bool `json:"single_select,omitempty"`
	// Advanced moves the field into the form's collapsed "Advanced Settings"
	// section, for options most users never touch. The field is still part of
	// the schema and is validated and stored like any other.
	Advanced bool `json:"advanced,omitempty"`
}

type IntegrationSchema struct {
	Type IntegrationSchemaType `json:"type"`
	// Category mirrors the owning integration's Category(). Surfaced to the
	// frontend so the account_id dropdown can pick the right account list:
	// webhook integrations (incident_webhook) route alerts to any account and
	// show all of them, while relay/agent integrations target a K8s cluster and
	// must exclude cloud accounts. Populated in IntegrationConfigs, not by each
	// ConfigSchema().
	Category IntegrationCategory `json:"category,omitempty"`
	// Description is an optional schema-level notice rendered as a banner
	// above the form. Useful for integration-specific prerequisites the user
	// must satisfy (e.g. "the source table must be time-partitioned").
	Description  string                               `json:"description,omitempty"`
	Properties   map[string]IntegrationSchemaProperty `json:"properties"`
	Required     []string                             `json:"required,omitempty"`
	Testable     bool                                 `json:"testable,omitempty"`
	TestableWhen map[string]any                       `json:"testable_when,omitempty"`
}

type IntegrationConfigValue struct {
	Name        string `json:"name" db:"name"`
	Value       string `json:"value" db:"value"`
	IsEncrypted bool   `json:"is_encrypted,omitempty" db:"is_encrypted"`
}
