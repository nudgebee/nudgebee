package core

import "nudgebee/llm/security"

type NBToolType string

const (
	NBToolTypeAgent NBToolType = "agent"
	NBToolTypeTool  NBToolType = "tool"
)

const ToolExecuteShellCommand = "shell_execute"

type NBToolCommand struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema ToolSchema `json:"input"`
}

type NBTool interface {
	Name() string
	Description() string
	Call(ctx NbToolContext, input NBToolCallRequest) (NBToolResponse, error)
	GetType() NBToolType
	InputSchema() ToolSchema
}

type NBToolCallRequest struct {
	Command   string         `json:"command"`
	Arguments map[string]any `json:"args"`
	Context   string         `json:"context"`
}

type NBMultiCommandTool interface {
	GetSubCommands() ([]NBToolCommand, error)
}

type NBToolResposeType string

const (
	NBToolResponseTypeText  NBToolResposeType = "text"
	NBToolResponseTypeJson  NBToolResposeType = "json"
	NBToolResponseTypeTable NBToolResposeType = "table"
	NBToolResponseTypeImage NBToolResposeType = "image"
)

type NBToolResponseStatus string

const (
	NBToolResponseStatusSuccess          NBToolResponseStatus = "SUCCESS"
	NBToolResponseStatusError            NBToolResponseStatus = "ERROR"
	NBToolResponseStatusTerminated       NBToolResponseStatus = "TERMINATED"
	NBToolResponseStatusWaiting          NBToolResponseStatus = "WAITING"
	NBToolResponseStatusWaitingForClient NBToolResponseStatus = "WAITING_FOR_CLIENT"
	NBToolResponseStatusInProgress       NBToolResponseStatus = "IN_PROGRESS"
)

type NBToolResponseReference struct {
	Text        string `json:"text"`
	Url         string `json:"url"`
	Type        string `json:"type"` // "link", "file", "k8s_resource", "citation"
	Query       string `json:"query"`
	Description string `json:"description"`
}

type NBToolResponse struct {
	Data              string                    `json:"data"`
	Type              NBToolResposeType         `json:"type"`
	Status            NBToolResponseStatus      `json:"status"`
	IsTerminal        bool                      `json:"is_terminal"`
	AdditionalDetails map[string]any            `json:"additional_details,omitempty"`
	References        []NBToolResponseReference `json:"references"`
	// Metadata carries execution telemetry (exit status, duration, stderr,
	// truncation) that the planner formats into the prompt at render time and
	// the persistence layer stores as a JSONB column. Kept off `Data` so the
	// observation text the UI renders stays byte-for-byte what the tool
	// produced. Nil for tools that don't populate it.
	Metadata *NBToolResponseMetadata `json:"metadata,omitempty"`
}

// NBToolResponseMetadata is the typed seam for tool-execution metadata. New
// fields land here without an interface change or DB migration (persisted as
// one JSONB column on llm_conversation_tool_calls).
type NBToolResponseMetadata struct {
	// ExitStatus mirrors POSIX-style intent: 0 success, 1 failure, 2
	// empty-but-successful (e.g. grep with no match). Not a literal shell
	// exit code — it's derived from NBToolResponseStatus + empty-data check
	// in the executor.
	ExitStatus int `json:"exit_status"`
	// ExecutionDurationMs is wall-clock duration of the tool call in
	// milliseconds. Clamped to 0 on negative input.
	ExecutionDurationMs int64 `json:"execution_duration_ms"`
	// Stderr is the stderr stream when the tool surfaces one separately
	// (today: kubectl). Empty when stdout is the only stream.
	Stderr string `json:"stderr,omitempty"`
	// Truncated is true when Data was clipped by truncateToolResponse before
	// persistence. OriginalLen records the pre-truncation byte length so
	// callers can tell the planner how much was dropped.
	Truncated   bool `json:"truncated,omitempty"`
	OriginalLen int  `json:"original_len,omitempty"`
}

type ToolSchemaType string

const (
	ToolSchemaTypeString  ToolSchemaType = "string"
	ToolSchemaTypeInteger ToolSchemaType = "integer"
	ToolSchemaTypeNumber  ToolSchemaType = "number"
	ToolSchemaTypeBoolean ToolSchemaType = "boolean"
	ToolSchemaTypeObject  ToolSchemaType = "object"
	ToolSchemaTypeArray   ToolSchemaType = "array"
)

type ToolSchemaProperty struct {
	Type        ToolSchemaType `json:"type"`
	Description string         `json:"description,omitempty"`
	Items       map[string]any `json:"items,omitempty"`
	Enum        []any          `json:"enum,omitempty"`
	Default     any            `json:"default,omitempty"`
	Pattern     string         `json:"pattern,omitempty"`
	IsEncrypted bool           `json:"is_encrypted,omitempty"`
}

type ToolSchema struct {
	Type       ToolSchemaType                `json:"type"`
	Properties map[string]ToolSchemaProperty `json:"properties"`
	Required   []string                      `json:"required,omitempty"`
}

type ToolRequestType string

const (
	ToolRequestTypeCreate ToolRequestType = "create"
	ToolRequestTypeRead   ToolRequestType = "read"
	ToolRequestTypeUpdate ToolRequestType = "update"
	ToolRequestTypeDelete ToolRequestType = "delete"
)

type ToolRequestInference interface {
	InferToolRequestType(ctx *security.RequestContext, toolName, input string) (ToolRequestType, error)
}

type ToolRequestInferencePrompt interface {
	InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error)
}

type ToolConfigSource string

const (
	ToolConfigSourceLLMAgent        ToolConfigSource = "llm-agent"
	ToolConfigSourceAccountAgent    ToolConfigSource = "account-agent"
	ToolConfigSourceAccountAgentAll ToolConfigSource = "account-agent-all"
	ToolConfigSourceAccount         ToolConfigSource = "account"
	ToolConfigSourceIntegration     ToolConfigSource = "integration"
	ToolConfigSourceTicket          ToolConfigSource = "ticket"
	ToolConfigSourceTicketAll       ToolConfigSource = "ticket_all"
)

type ToolConfigSchema struct {
	Type         ToolSchemaType                `json:"type"`
	Properties   map[string]ToolSchemaProperty `json:"properties"`
	Required     []string                      `json:"required,omitempty"`
	ConfigType   string                        `json:"config_type,omitempty"`
	ConfigSource ToolConfigSource              `json:"config_source,omitempty"`
}

type NBToolConfig interface {
	ConfigSchema(ctx *security.RequestContext) ToolConfigSchema
}

type NBToolConfigIdentifier interface {
	IdentifyConfig(ctx NbToolContext, input NBToolCallRequest, availableConfigs []ToolConfig) (ToolConfig, error)
}

// NBToolConfigsFilter narrows the candidate config list before resolution
// strategies (findConfigInQuery, IdentifyConfig, LLM selection) run. Useful
// when the tool's ConfigSource returns a superset — e.g. ToolConfigSourceTicketAll
// returns every ticket integration regardless of platform, but the user query
// mentions only Jira, so GitHub/GitLab/ServiceNow/etc. should be filtered out.
//
// Implementations MUST return a non-empty subset when they successfully narrow,
// and SHOULD return the original configs unchanged when no filtering is possible.
// Returning an empty slice is treated as "no narrowing".
type NBToolConfigsFilter interface {
	FilterConfigs(ctx NbToolContext, configs []ToolConfig) []ToolConfig
}

type ToolConfigValue struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	IsEncrypted bool   `json:"is_encrypted"`
}

type ToolConfig struct {
	Id     string            `json:"id"`
	Values []ToolConfigValue `json:"values"`
	Tags   map[string]string `json:"tags"`
	Schema ToolConfigSchema  `json:"schema"`
	Name   string            `json:"name"`
}
