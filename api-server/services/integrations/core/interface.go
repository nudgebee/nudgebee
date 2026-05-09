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
	RequiredWhen     map[string]any        `json:"required_when,omitempty"`
	ShowWhen         map[string]any        `json:"show_when,omitempty"`
	Priority         int                   `json:"priority,omitempty"`
	AllowEdit        bool                  `json:"allow_edit,omitempty"`
	Hidden           bool                  `json:"hidden,omitempty"`
	Multiline        bool                  `json:"multiline,omitempty"`
	IsTestable       bool                  `json:"is_testable,omitempty"`
}

type IntegrationSchema struct {
	Type         IntegrationSchemaType                `json:"type"`
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
