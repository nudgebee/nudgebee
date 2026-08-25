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

// ToolImplTypeBuiltin is the default tool-implementation label emitted on
// metrics for tools that don't override it via NBToolImplTypeProvider.
const ToolImplTypeBuiltin = "tool"

// NBToolImplTypeProvider is an optional interface tools can implement to
// report their implementation class as a metric label (e.g. "mcp" for
// MCP integration tools). Tools that don't implement it are labeled
// ToolImplTypeBuiltin. Used by the planner when emitting tool-operation
// metrics so dashboards can group calls by tool family.
type NBToolImplTypeProvider interface {
	GetImplType() string
}

// NBToolPromptProvider is an optional interface tools can implement to
// carry their own "how to use me" guidance — the invocation shape,
// safety rules, common command patterns, and gotchas — that today lives
// in the wrapping agent's system prompt. When a tool is named directly
// in `delegate_agent(tools=[...])`, the delegate folds the returned
// lines into its brief the same way it folds a Flattenable agent's
// guidance today.
//
// Description() vs ToolPrompt():
//   - Description() answers "should I pick this tool?" — kept short, shown in every tool-list impression.
//   - ToolPrompt() answers "how do I use it correctly?" — larger, only paid when the tool is in the caller's toolset.
//
// Phase 3 migration path: helm/redis/rabbitmq (and eventually argocd)
// move their per-agent instructions/examples onto their `*_execute`
// tools via this interface, so the wrapping agent can be flag-off'd
// without losing pedagogy.
type NBToolPromptProvider interface {
	ToolPrompt() []string
}

// ImplTypeFor returns the metric label identifying a tool's
// implementation class. Falls back to ToolImplTypeBuiltin when the tool
// does not implement NBToolImplTypeProvider, or when nil.
func ImplTypeFor(tool NBTool) string {
	if tool == nil {
		return ToolImplTypeBuiltin
	}
	if p, ok := tool.(NBToolImplTypeProvider); ok {
		return p.GetImplType()
	}
	return ToolImplTypeBuiltin
}

// MissingFieldsResponder lets a tool override the generic
// "<tool>: missing required fields — <field> (<type>): <description>"
// line the planner emits when schema-Required validation fails, without
// changing the schema (so the LLM still sees the correct spec at call
// time).
//
// The handler receives the request the LLM actually sent so it can
// inspect misplaced content (e.g. narration text landing on Command
// when Arguments["reasoning"] was required) and return a response that
// names the specific misuse instead of the generic line.
//
// Return nil to fall back to the generic message.
//
// Implementers SHOULD add a compile-time interface check next to their
// registration so a typo in the receiver-method name fails the build
// instead of silently opting the tool out of the escape hatch:
//
//	var _ core.MissingFieldsResponder = (*myTool)(nil)
type MissingFieldsResponder interface {
	OnMissingRequiredFields(request NBToolCallRequest, missing []string) *NBToolResponse
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
	// SubAgentEvidence is a small, budget-bounded manifest of the concrete tool
	// calls a sub-agent actually ran (set only for agent-type tools when the
	// evidence feature is enabled). Kept OFF `Data` deliberately: it is rendered
	// as a separate block after the observation and is exempt from scratchpad
	// compression, so the distilled artifacts survive even when the raw
	// observation is summarized as it ages. Empty for normal tools.
	SubAgentEvidence string `json:"sub_agent_evidence,omitempty"`
	// IsCircuitOpen marks a response built by NewCircuitOpenResponse — the
	// tool's circuit breaker fast-failed the call before it ran. Lets callers
	// (the planner's investigation-abort counter, tool-call metrics) treat
	// this distinctly from a genuine tool failure.
	IsCircuitOpen bool `json:"is_circuit_open,omitempty"`
}

// NBToolResponseMetadata is the typed seam for tool-execution metadata. New
// fields land here without an interface change or DB migration (persisted as
// one JSONB column on llm_conversation_tool_calls).
type NBToolResponseMetadata struct {
	// ExitStatus mirrors POSIX intent: 1 failure, else 0 (empty-but-successful
	// counts as success). Derived from NBToolResponseStatus in the executor.
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
	// DBQueries/DBMs and RelayCalls/RelayMs split a tool's wall-clock time by
	// where it went. ExecutionDurationMs alone can't answer the question that
	// actually matters for resource lookups — "did this hit the inventory DB or
	// fall through to the live kubectl cascade?" — because both look identical
	// from outside. Populated by tools that call ToolCallStats; absent (omitempty)
	// for tools that don't, so existing rows and readers are unaffected.
	// DBMs/RelayMs are ELAPSED time (earliest start to latest end). DBBusyMs and
	// RelayBusyMs are summed per-call durations. They differ once a tool issues
	// its calls concurrently, and only the elapsed figure is latency — the summed
	// one rises when work is parallelised, which reads as a regression when it is
	// the opposite. Busy ÷ elapsed is the effective concurrency.
	DBQueries   int   `json:"db_queries,omitempty"`
	DBMs        int64 `json:"db_ms,omitempty"`
	DBBusyMs    int64 `json:"db_busy_ms,omitempty"`
	RelayCalls  int   `json:"relay_calls,omitempty"`
	RelayMs     int64 `json:"relay_ms,omitempty"`
	RelayBusyMs int64 `json:"relay_busy_ms,omitempty"`
	// Steps carries the individual DB/relay operations for persistence as child
	// tool-call rows. json:"-" on purpose — these are written as their own rows,
	// and duplicating full command output inside the parent's metadata column
	// would bloat every row that carries them. StepsDropped is how many the
	// recorder's cap discarded, so a partial list is never shown as complete.
	Steps        []ToolCallStep `json:"-"`
	StepsDropped int            `json:"steps_dropped,omitempty"`
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
	// RequiredOneOf: each group needs ≥1 listed key present (non-null) — a
	// shape-only anyOf subset for aliased fields (e.g. command/query).
	RequiredOneOf [][]string `json:"required_one_of,omitempty"`
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

// ToolConfirmationScope is an optional interface for write tools whose user
// confirmation must be per-action rather than per-tool. By default a
// write-confirmation is recorded under the tool name, so the first "yes"
// covers every later call to the same tool in the conversation. A tool
// implementing this interface returns a key that varies with the input
// (e.g. name + input hash), making each distinct invocation pause for its
// own approval.
type ToolConfirmationScope interface {
	ConfirmationKey(toolInput string) string
}

// ToolConfirmationPrompt is an optional interface for write tools that can
// express a pending action in operator terms. The confirmation card shows the
// returned question instead of the default "Tool(x) is trying to <type>
// cluster resources / Command - <raw input>" rendering — raw tool input is
// often just ids, which tells the approver nothing about the actual change.
// Return "" to fall back to the default rendering.
type ToolConfirmationPrompt interface {
	ConfirmationQuestion(toolInput string) string
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
