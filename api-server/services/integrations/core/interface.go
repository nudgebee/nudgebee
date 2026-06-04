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
)

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
	TestConnection(ctx *security.SecurityContext, values []IntegrationConfigValue, accountId string) error
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
	Type             IntegrationSchemaType `json:"type"`
	Description      string                `json:"description,omitempty"`
	Items            map[string]any        `json:"items,omitempty"`
	Enum             []any                 `json:"enum,omitempty"`
	Default          any                   `json:"default,omitempty"`
	Pattern          string                `json:"pattern,omitempty"`
	IsEncrypted      bool                  `json:"is_encrypted,omitempty"`
	AutoGenerateFunc string                `json:"auto_generate_func,omitempty"`
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
	IsTestable   bool           `json:"is_testable,omitempty"`
	// SingleSelect, on an array-typed property with auto_generate_func='listAccounts',
	// tells the frontend to render a single-select dropdown instead of the default
	// multi-select. Used for integrations that bind 1:1 to an account (e.g.
	// workflow_webhook, which is bound to one workflow per row).
	SingleSelect bool `json:"single_select,omitempty"`
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
