package core

import (
	"context"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"time"

	"github.com/tmc/langchaingo/llms"
)

type ConversationStatus string

const (
	ConversationStatusInProgress           ConversationStatus = "IN_PROGRESS"
	ConversationStatusCompleted            ConversationStatus = "COMPLETED"
	ConversationStatusFailed               ConversationStatus = "FAILED"
	ConversationStatusKilled               ConversationStatus = "KILLED"
	ConversationStatusPending              ConversationStatus = "PENDING"
	ConversationStatusWaiting              ConversationStatus = "WAITING"
	ConversationStatusWaitingForClientTool ConversationStatus = "WAITING_FOR_CLIENT_TOOL"
	ConversationStatusTerminated           ConversationStatus = "TERMINATED"
)

// IsTerminalConversationStatus returns true if the conversation has reached a
// final state and should not be restarted or cleaned up by recovery jobs.
func IsTerminalConversationStatus(status ConversationStatus) bool {
	return status == ConversationStatusCompleted ||
		status == ConversationStatusFailed ||
		status == ConversationStatusKilled ||
		status == ConversationStatusTerminated
}

type AgentExecutionStatus string

const (
	AgentExecutionStatusInProgress           AgentExecutionStatus = "in_progress"
	AgentExecutionStatusSuccess              AgentExecutionStatus = "success"
	AgentExecutionStatusFail                 AgentExecutionStatus = "fail"
	AgentExecutionStatusTerminated           AgentExecutionStatus = "terminated"
	AgentExecutionStatusSkipped              AgentExecutionStatus = "skipped"
	AgentExecutionStatusWaiting              AgentExecutionStatus = "waiting"
	AgentExecutionStatusWaitingForClientTool AgentExecutionStatus = "waiting_for_client_tool"
)

type ConversationSource string

const (
	ConversationSourceUserInvestigation    ConversationSource = "UserInvestigation"
	ConversationSourcePrometheusQuery      ConversationSource = "PrometheusQuery"
	ConversationSourceLokiQuery            ConversationSource = "LokiQuery"
	ConversationSourceESQuery              ConversationSource = "ESQuery"
	ConversationSourceInvestigation        ConversationSource = "Investigation"
	ConversationSourceInstantNotification  ConversationSource = "InstantNotification"
	ConversationSourceWorkflowBuilder      ConversationSource = "WorkflowBuilder"
	ConversationSourceUserInvestigationCLI ConversationSource = "UserInvestigationCli"
	ConversationSourceOptimize             ConversationSource = "Optimize"
)

// ImageAttachment represents an image attached to an agent request.
// Exactly one of Data or URL must be set.
type ImageAttachment struct {
	Data     string `json:"data,omitempty"`      // Base64-encoded image data
	URL      string `json:"url,omitempty"`       // URL pointing to the image
	MIMEType string `json:"mime_type,omitempty"` // MIME type — only image/png and image/jpeg are accepted
}

// DO not use for API calls
type NBAgentRequest struct {
	Query                 string                   `json:"query" mapstructure:"required" validate:"required"`
	AccountId             string                   `json:"account_id" mapstructure:"required" validate:"required"`
	ConversationId        string                   `json:"conversation_id"`
	AgentId               string                   `json:"agent_id"`
	ParentAgentId         string                   `json:"parent_agent_id"`
	MessageId             string                   `json:"message_id"`
	UserId                string                   `json:"user_id"`
	ConversationContext   string                   `json:"conversation_context"`
	QueryContext          string                   `json:"query_context"`
	QueryConfig           toolcore.NBQueryConfig   `json:"query_config"`
	EnableQueryRefinement bool                     `json:"enable_query_refinement"`
	AccountPrompt         string                   `json:"account_prompt"`
	SessionId             string                   `json:"session_id"`
	ConversationSource    ConversationSource       `json:"source"`
	EnableCritique        bool                     `json:"enable_critique"`
	ClientTools           []toolcore.NBToolCommand `json:"client_tools"`
	Capabilities          map[string]any           `json:"capabilities"`
	PreviousState         string                   `json:"previous_state"`
	Images                []ImageAttachment        `json:"images,omitempty"`
	// SkillsContext carries fully-rendered skill content (a `<skills>...</skills>` block)
	// for agents whose planner type is AgentPlannerTypeCustom AND whose Execute()
	// makes direct LLM calls (loganalysis, logs_default.generateFinalResponse,
	// resource_search, websearch). These agents bypass the executor's systemMessage
	// path so the lazy `load_skills` tool flow used by ReAct/ReWoo planners does not
	// reach them. The executor populates this field eagerly with the bodies of every
	// active KB mapped to (agent.GetName() ∪ InheritSkillsFromAgents), narrowed to
	// SelectedSkillIds when question-aware selection is enabled. The custom Execute()
	// reads this field and prepends the block to its LLM prompt.
	SkillsContext string `json:"skills_context,omitempty"`
	// InheritSkillsFromAgents lists ancestor agent names whose mapped KBs should be
	// surfaced to this agent in addition to its own. Custom-planner delegators
	// (metrics, traces, logs, logs_default) append their own name when invoking a
	// sub-agent so the sub-agent's lazy <skill-lists> + load_skills flow knows about
	// skills the user mapped to the parent — without this propagation those skills
	// would be silently invisible at the delegation boundary.
	InheritSkillsFromAgents []string `json:"inherit_skills_from_agents,omitempty"`
	// OriginalQuery captures the user's verbatim top-level question before any
	// rewriting by sub-agents (a sub-agent's command is mechanical — "fetch CPU for
	// pod foo" — and would destroy the relevance signal). It is set once at top-level
	// executor entry and propagated unchanged through delegation. Empty means this
	// is the top-level invocation; non-empty means we are running under a parent.
	OriginalQuery string `json:"original_query,omitempty"`
	// SelectedSkillIds is the question-aware short-list computed once at the
	// top-level invocation when LlmServerSkillSelectionTopK > 0. Both the eager
	// LoadActiveAgentSkillContents path and the lazy injectKBContext path filter to
	// these IDs (∪ the sub-agent's own mapped KBs). nil means "no filtering / show
	// every mapped skill" (selection disabled, or no mapped skills, or top-level
	// fan-out smaller than K).
	SelectedSkillIds []string `json:"selected_skill_ids,omitempty"`
	// IsResume marks the request as a resume of an already-active conversation
	// (client-tool-result, dead-worker recovery). When true, handleConversationRequest
	// skips the IN_PROGRESS guard at conversation.go:685 — the conversation is
	// expected to be IN_PROGRESS because markConversationActive flips it just
	// before this request enters the loop. Without this bypass, the resume worker
	// rejects its own work as "already in progress" (regression introduced by
	// #29973). New-turn requests must keep IsResume=false so legitimate races
	// (user submits while another turn is running) still surface as errors.
	IsResume bool `json:"is_resume,omitempty"`
	// KBPrestepContent holds knowledge base content retrieved by the pre-step
	// (retrieveRelevantKB) before planning. Populated only when
	// LlmServerKBPrestepEnabled is on. The planner renders it into the human
	// message — not the cacheable system prefix — so per-request KB content
	// never thrashes the LLM cache.
	KBPrestepContent string `json:"kb_prestep_content,omitempty"`
	// SkillListsMenu holds the `<skill-lists>` discovery block (names +
	// descriptions, no bodies) when LlmServerKBPrestepEnabled is on. Like
	// KBPrestepContent it is rendered into the human message instead of the
	// system prompt. When the flag is off this stays empty and the legacy
	// injectKBContext path prepends the block to the system prompt instead.
	SkillListsMenu string `json:"skill_lists_menu,omitempty"`
}

// DO not use for API calls
type NBAgentResponse struct {
	Response              []string                           `json:"response"`
	AgentName             string                             `json:"agent_name"`
	Query                 string                             `json:"query"`
	AgentStepResponse     []ToolInvocation                   `json:"agent_step_response"`
	AgentStepResponseData any                                `json:"agent_step_response_data"`
	ConversationId        string                             `json:"conversation_id"`
	SessionId             string                             `json:"session_id"`
	Status                ConversationStatus                 `json:"status"`
	AgentId               string                             `json:"agent_id"`
	MessageId             string                             `json:"message_id"`
	IsTerminal            bool                               `json:"is_terminal"`
	FollowupRequest       FollowupRequest                    `json:"followup"`
	QueryConfig           *toolcore.NBQueryConfig            `json:"query_config,omitempty"`
	References            []toolcore.NBToolResponseReference `json:"references,omitempty"`
}

type LlmToolResponse struct {
	MessageContent []llms.MessageContent `json:"message_content"`
	ChainName      string                `json:"chain_name"`
	ResponseData   any                   `json:"response_data"`
	Query          string                `json:"query"`
}

type NBAgentPlannerToolActionCondition struct {
	Prompt           string   `json:"prompt,omitempty"`
	Expression       string   `json:"expression,omitempty"`
	ExpectedResponse string   `json:"expected_response,omitempty"`
	AllowedResponses []string `json:"allowed_responses,omitempty"`
}

type NBAgentPlannerToolAction struct {
	Tool      string `json:"tool"`
	ToolInput string `json:"tool_input"`
	Log       string `json:"log"`
	ToolID    string `json:"tool_id"`
	// DisplayID is a human-readable sequential identifier assigned by the planner
	// (e.g. "E1", "E2", "E3") for use in citations and the response formatter.
	// For React_3 this is assigned as steps are generated; for ReWoo it remains
	// empty because the solver already produces correctly-numbered citations.
	DisplayID  string                            `json:"display_id,omitempty"`
	Dependency []string                          `json:"dependency"`
	Condition  NBAgentPlannerToolActionCondition `json:"condition"`
}

type NBAgentPlannerToolActionStep struct {
	Action      NBAgentPlannerToolAction           `json:"action"`
	Observation string                             `json:"observation"`
	Status      ToolStatus                         `json:"status"`
	IsTerminal  bool                               `json:"is_terminal"`
	References  []toolcore.NBToolResponseReference `json:"references"`
	Followup    *FollowupRequest                   `json:"followup,omitempty"`
	// CompressedObservation caches the LLM-generated summary for this step's observation.
	// Computed once when the step is first compressed, then reused across subsequent
	// scratchpad builds to avoid redundant LLM calls.
	CompressedObservation string `json:"compressed_observation,omitempty"`
}

type ToolStatus string

const (
	ToolStatusSuccess          ToolStatus = "SUCCESS"
	ToolStatusFailure          ToolStatus = "FAILURE"
	ToolStatusEmptyResult      ToolStatus = "EMPTY_RESULT"
	ToolStatusWaitingForClient ToolStatus = "WAITING_FOR_CLIENT"
	ToolStatusWaiting          ToolStatus = "WAITING"
)

type NBAgentPlannerFinishAction struct {
	Data              string             `json:"data"`
	Log               string             `json:"log"`
	Status            ConversationStatus `json:"status"`
	IsTerminal        bool               `json:"is_terminal"`
	Followup          FollowupRequest    `json:"followup"`
	AdditionalDetails map[string]any     `json:"additional_details,omitempty"`
	Invocations       []ToolInvocation   `json:"invocations,omitempty"`
}

type ToolInvocation struct {
	Call       llms.ToolCall
	Response   llms.ToolCallResponse
	Log        string
	References []toolcore.NBToolResponseReference
}

type NBAgentPlanner interface {
	Plan(ctx context.Context, intermediateSteps []NBAgentPlannerToolActionStep, input string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error)
	GetTools() []toolcore.NBTool
	Marshal() ([]byte, error)
	Unmarshal([]byte) error
}

type NBAgentNotebookProvider interface {
	GetNotebook() string
}

type MemoryFact struct {
	Content    string `json:"content"`
	IsPattern  bool   `json:"is_pattern"`
	IsWorkflow bool   `json:"is_workflow"`
	IsUpdate   bool   `json:"is_update"`
	Type       string `json:"type,omitempty"`
	OldContent string `json:"old_content,omitempty"`
}

type MemoryType string

func (m MemoryType) Validate() MemoryType {
	switch string(m) {
	case "user_preference":
		return MemoryTypeUserPreference
	case "pattern":
		return MemoryTypePattern
	case "workflow":
		return MemoryTypeWorkflow
	case "investigation_result":
		return MemoryTypeInvestigationResult
	case "architectural_fact":
		return MemoryTypeArchitecturalFact
	case "dependency_mapping":
		return MemoryTypeDependencyMapping
	case "troubleshooting_guide":
		return MemoryTypeTroubleshooting
	case "configuration_insight":
		return MemoryTypeConfigInsight
	default:
		return MemoryTypeInvestigationResult
	}
}

const (
	MemoryTypeInvestigationResult MemoryType = "investigation_result"
	MemoryTypeArchitecturalFact   MemoryType = "architectural_fact"
	MemoryTypeDependencyMapping   MemoryType = "dependency_mapping"
	MemoryTypeTroubleshooting     MemoryType = "troubleshooting_guide"
	MemoryTypeConfigInsight       MemoryType = "configuration_insight"
	MemoryTypeUserPreference      MemoryType = "user_preference"
	MemoryTypePattern             MemoryType = "pattern"
	MemoryTypeWorkflow            MemoryType = "workflow"
)

type AgentPlannerType string

const (
	AgentPlannerTypeTool           AgentPlannerType = "tools"
	AgentPlannerTypeReAct          AgentPlannerType = "react"
	AgentPlannerTypeReWoo          AgentPlannerType = "rewoo"
	AgentPlannerTypeCustom         AgentPlannerType = "custom"
	AgentPlannerTypeConversational AgentPlannerType = "conversation"
	AgentPlannerTypeClassification AgentPlannerType = "classification"
	AgentPlannerTypeReAct3         AgentPlannerType = "react_3"
)

type NBAgentPromptRagFormat string

const (
	NBAgentPromptRagFormatString NBAgentPromptRagFormat = "string"
	NBAgentPromptRagFormatJson   NBAgentPromptRagFormat = "json"
)

type NBAgentPromptRag struct {
	Module         string                 `json:"module"`
	Format         NBAgentPromptRagFormat `json:"format"`
	QuestionKey    string                 `json:"question_key,omitempty"`
	AnswerKey      string                 `json:"answer_key,omitempty"`
	ExplanationKey string                 `json:"explanation_key,omitempty"`
	Records        int                    `json:"records,omitempty"`
}

type NBAgentPromptExampleAnswerStep struct {
	Tool        string `json:"tool"`
	Input       string `json:"input"`
	Explanation string `json:"explanation,omitempty"`
}

type NBAgentPromptExample struct {
	Question    string                           `json:"question"`
	Answer      string                           `json:"answer,omitempty"`
	AnswerSteps []NBAgentPromptExampleAnswerStep `json:"answer_steps,omitempty"`
	Explanation string                           `json:"explanation,omitempty"`
}

type NBAgentPrompt struct {
	Role         string                 `json:"role,omitempty"`
	Instructions []string               `json:"instructions,omitempty"`
	Examples     []NBAgentPromptExample `json:"examples,omitempty"`
	Constraints  []string               `json:"constraints,omitempty"`
	ToolUsage    map[string][]string    `json:"tool_usage,omitempty"`
	OutputFormat string                 `json:"output_format,omitempty"`
	Schema       []string               `json:"schema,omitempty"`
	Rag          NBAgentPromptRag       `json:"rag,omitempty"`
	Variables    []string               `json:"variables,omitempty"`
}

type NBAgent interface {
	GetName() string
	GetNameAliases() []string
	GetDescription() string
	GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool
	GetSystemPrompt(ctx *security.RequestContext, query NBAgentRequest) NBAgentPrompt
	GetPlannerType() AgentPlannerType
}

// NBAgentCategoryProvider is an optional interface that lets an agent declare
// its model category (ModelTier). executeAgent stamps the category onto the
// request context so the agent's LLM calls resolve the category-specific
// model. An agent that does not implement it resolves through the normal flow.
type NBAgentCategoryProvider interface {
	GetModelCategory() ModelTier
}

// agentModelCategory returns the category an agent opted into, or an empty
// tier when it declared none (→ normal resolution flow).
func agentModelCategory(agent NBAgent) ModelTier {
	if p, ok := agent.(NBAgentCategoryProvider); ok {
		return p.GetModelCategory()
	}
	return ""
}

// NBAgentCacheScopeProvider is an optional interface that allows agents to define
// their caching stability scope (e.g. Account or Global level vs Conversation).
type NBAgentCacheScopeProvider interface {
	GetCacheScope() CacheScope
}

type NBAgentExecutorLlmResponseHandler interface {
	UpdateExecutorLlmResponse([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error)
}

type NBAgentExecutorToolResponseHandler interface {
	UpdateToolResponseForPlanner(NBAgentPlannerToolAction, string) string
}

type NBAgentResponseHandler interface {
	PostProcessResponse(ctx *security.RequestContext, request NBAgentRequest, response NBAgentResponse) NBAgentResponse
}

type NBCustomAgent interface {
	NBAgent
	Execute(ctx *security.RequestContext, query NBAgentRequest) (NBAgentResponse, error)
}

type NBAgentToolCallback interface {
	BeforeToolCall(toolcall NBAgentPlannerToolAction)
	AfterToolCallResponse(toolcall NBAgentPlannerToolAction, response toolcore.NBToolResponse)
}

// NBAgentIterationProvider allows an agent to declare its own maximum ReAct
// iteration count, overriding the global LLMServerAgentReActMaxIterations config.
// Implement this on short-lived tool-agents (e.g., query generators) to prevent
// runaway retry loops. The executor uses min(agent cap, global config).
type NBAgentIterationProvider interface {
	GetMaxIterations() int
}

// NBAgentTimeoutProvider allows an agent to declare a wall-clock timeout
// for its entire execution. The executor wraps the run context with this
// deadline before entering the main loop. Implement on agents with bounded
// latency SLAs (e.g., sub-agents invoked by orchestrators).
type NBAgentTimeoutProvider interface {
	GetTimeout() time.Duration
}

// NBAgentNotebookSectionProvider lets an agent opt out of the planner's
// notebook/working-memory section in the system prompt. Default (when not
// implemented) is true — notebook discipline is mandated for SRE-style
// investigations. Time-constrained imperative agents (e.g., tbench-style
// task runners) implement this returning false to suppress the section
// entirely instead of relying on contradictory guidance.
type NBAgentNotebookSectionProvider interface {
	GetNotebookEnabled() bool
}

// ResolveAgentNotebookEnabled returns whether the planner notebook section
// should be rendered for this agent. Defaults to true unless the agent
// implements NBAgentNotebookSectionProvider and returns false.
func ResolveAgentNotebookEnabled(agent NBAgent) bool {
	if p, ok := agent.(NBAgentNotebookSectionProvider); ok {
		return p.GetNotebookEnabled()
	}
	return true
}

// AgentModule identifies the functional bucket an agent belongs to. Used by
// the Memory Architecture to scope per-module memory (Patterns, Collective),
// filter tool visibility, and route signal collectors. Agents that span
// buckets can return multiple modules via NBAgentModuleProvider.
//
// Note: this enum mixes two axes — domain (Observability, K8sOps, CloudOps,
// FinOps) and capability (Automation). Memory routing treats them uniformly,
// so the mix is intentional; future split into AgentDomain + AgentCapability
// is a separate refactor that would also reshape the agent_module column.
type AgentModule string

const (
	AgentModuleObservability AgentModule = "observability" // logs/metrics/traces/alerts/RCA
	AgentModuleK8sOps        AgentModule = "k8s_ops"       // kubectl/helm/argocd/eks/gke/aks
	AgentModuleCloudOps      AgentModule = "cloud_ops"     // aws/gcp/azure infrastructure
	AgentModuleFinOps        AgentModule = "finops"        // cost/billing/budget
	AgentModuleAutomation    AgentModule = "automation"    // workflows/runbooks/playbooks
	AgentModuleGeneric       AgentModule = "generic"       // default fallback
)

// NBAgentModuleProvider is an optional interface agents implement to declare
// which functional module(s) they belong to. Agents that don't implement it
// are treated as AgentModuleGeneric. The executor calls ResolveAgentModule()
// which handles the fallback.
type NBAgentModuleProvider interface {
	// GetAgentModules returns the module tags for the agent. Return multiple
	// entries for multi-domain agents (e.g., AWS debug can be SRE + FinOps);
	// the first entry is treated as primary for ranking weight.
	GetAgentModules() []AgentModule
}

// ResolveAgentModule returns the primary module for an agent. Resolution order:
//  1. If the agent implements NBAgentModuleProvider, its first module wins.
//  2. Otherwise, the central agent-module registry (see agent_modules.go) is
//     consulted by agent name.
//  3. Otherwise, AgentModuleGeneric.
//
// This is the single read point used by memory bridge and signal collectors.
func ResolveAgentModule(agent NBAgent) AgentModule {
	if p, ok := agent.(NBAgentModuleProvider); ok {
		mods := p.GetAgentModules()
		if len(mods) > 0 {
			return mods[0]
		}
	}
	if mods := lookupAgentModules(agent.GetName()); len(mods) > 0 {
		return mods[0]
	}
	return AgentModuleGeneric
}

// ResolveAgentModules returns all modules an agent is tagged with. Same
// resolution order as ResolveAgentModule.
func ResolveAgentModules(agent NBAgent) []AgentModule {
	if p, ok := agent.(NBAgentModuleProvider); ok {
		mods := p.GetAgentModules()
		if len(mods) > 0 {
			return mods
		}
	}
	if mods := lookupAgentModules(agent.GetName()); len(mods) > 0 {
		return mods
	}
	return []AgentModule{AgentModuleGeneric}
}

// NBAgentDirectSummarization allows an agent to bypass the ReAct planner's
// next LLM call and proceed directly to summarizing a tool's output. Only works if summary tool is supported
type NBAgentDirectSummarization interface {
	// ShouldSummarizeNow checks if the output from a tool is suitable
	// for immediate summarization.
	// toolName: The name of the tool that was just executed.
	// observation: The string output (observation) from the tool.
	// Returns: True if the planner should immediately summarize the result.
	ShouldSummarizeNow(toolName string, observation string) bool
}

var agentCacheInvalidators []func(accountId string, agentName string)

func RegisterAgentCacheInvalidator(fn func(accountId string, agentName string)) {
	agentCacheInvalidators = append(agentCacheInvalidators, fn)
}

func InvalidateAllAgentCaches(accountId string, agentName string) {
	for _, fn := range agentCacheInvalidators {
		fn(accountId, agentName)
	}
}
