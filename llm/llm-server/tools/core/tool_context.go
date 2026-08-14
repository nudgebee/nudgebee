package core

import (
	"context"
	"errors"
	"nudgebee/llm/security"

	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"
)

const (
	ToolMessageConfigToolConfigsKey        = "tool_configs"
	ToolMessageConfigClientToolsKey        = "client_tools"
	ToolMessageConfigCapabilitiesKey       = "capabilities"
	ToolMessageConfigToolConfirmationKey   = "tool_confirmations"
	ToolMessageConfigToolConfigMetadataKey = "tool_config_metadata"
)

var ErrUnableToFetchData = errors.New("error: unable to fetch data")
var ErrUserNotAuthorized = errors.New("error: user is not authoirized to perform this action")

// Lives in tools/core (not agents/core) to avoid a circular import.
// TierModelPick is one task's choice: which credential serves it and which
// model to ask for. ConfigSource makes the pair complete — without it a
// per-tier pick would inherit whatever credential the conversation was pinned
// to, which is a different choice than the user made.
type TierModelPick struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// ConfigSource is the credential this task resolves through. Empty for
	// picks made before per-tier credentials existed; those fall back to the
	// conversation-wide pin.
	ConfigSource string `json:"llm_config_source,omitempty"`
}

// NBQueryConfig holds structured configuration for agent requests and tool contexts.
// It replaces the previous map[string]any with compile-time type safety.
// JSON tags match the original map keys to preserve backward compatibility with DB-stored configs.
type NBQueryConfig struct {
	// Observability/event context
	EventId          string `json:"event_id,omitempty"`
	RecommendationId string `json:"recommendation_id,omitempty"`

	// Kubernetes resource context
	Namespace string `json:"namespace,omitempty"`
	Workload  string `json:"workload,omitempty"`

	// Code analysis context
	GitRepo   string `json:"git_repo,omitempty"`
	AccountId string `json:"account_id,omitempty"`

	// Workflow context
	WorkflowId         string         `json:"workflow_id,omitempty"`
	ExecutionId        string         `json:"execution_id,omitempty"`
	WorkflowDefinition map[string]any `json:"workflow_definition,omitempty"`

	// Current cluster/cloud-account the user is viewing when starting an automation-builder chat.
	// Lets the builder default to it instead of asking which account/cluster to target (#30162).
	CurrentCluster   string `json:"current_cluster,omitempty"`    // display name
	CurrentClusterId string `json:"current_cluster_id,omitempty"` // cloud-account UUID

	// Query filtering labels (e.g. nb_cloud_account_id for AWS timeseries)
	Labels map[string]any `json:"labels,omitempty"`

	// K8sOrchestratorMode selects a K8s orchestrator variant per-request:
	// "native" routes to k8s_orchestrator_native (kubectl-first, lean tool set).
	// Empty defers to config.Config.K8sOrchestratorMode (boot-time default).
	// Case-insensitive at read time. Sticky within a conversation via
	// MergeFrom's fill-if-zero, like LlmProvider.
	K8sOrchestratorMode string `json:"k8s_orchestrator_mode,omitempty"`

	// LLM provider overrides (per-request)
	LlmProvider  string `json:"llm_provider,omitempty"`
	LlmModelName string `json:"llm_model_name,omitempty"`

	// Mutually exclusive with LlmProvider+LlmModelName above.
	LlmTierModels map[string]TierModelPick `json:"llm_tier_models,omitempty"`

	// LlmConfigSource pins the LLM call to a specific configured slot so
	// endpoint/api-key/api-type/api-version/region are deterministic and don't
	// drift across sub-agent tier context. Format: {layer}:{scope}[:{name}]
	// where layer = env | db, scope = global | tier | agent. For db-scoped
	// values, the integration UUID follows: db:<uuid>[:tier:<t>|:agent:<a>].
	// Higher precedence than LlmProvider+LlmModelName alone — when set, all
	// downstream credential lookups short-circuit to the pinned slot.
	// Sticky within a conversation via MergeFrom's fill-if-zero, like LlmProvider.
	LlmConfigSource string `json:"llm_config_source,omitempty"`

	// LlmConfigReset drops every stored model choice on the conversation —
	// blanket, per-tier and pin — so the turn and everything after it fall back
	// to the resolver's normal layered walk. Set by the picker's "Clear all",
	// which needs a signal distinct from "this turn made no choice": the latter
	// inherits the stored config, which is the whole point of stickiness.
	//
	// One-shot: it applies to the turn that carries it and is never inherited
	// by later turns (see MergeFrom).
	LlmConfigReset bool `json:"llm_config_reset,omitempty"`

	// Original user query — preserved across sub-agent boundaries so that
	// config selection strategies can access natural-language hints (e.g. "dev-aws")
	// that the planner may strip when rewriting the step query.
	OriginalUserQuery string `json:"original_user_query,omitempty"`

	// Tool infrastructure (managed by executor/planner)
	ToolConfigs        map[string]string `json:"tool_configs,omitempty"`
	ClientTools        []NBToolCommand   `json:"client_tools,omitempty"`
	Capabilities       AgentCapabilities `json:"capabilities,omitzero"`
	ToolConfirmations  map[string]string `json:"tool_confirmations,omitempty"`
	ToolConfigMetadata map[string]any    `json:"tool_config_metadata,omitempty"`
}

// IsEmpty returns true when the config has no meaningful values set.
func (q NBQueryConfig) IsEmpty() bool {
	return q.EventId == "" && q.RecommendationId == "" && q.Namespace == "" &&
		q.Workload == "" && q.GitRepo == "" && q.AccountId == "" &&
		q.WorkflowId == "" && q.ExecutionId == "" && len(q.WorkflowDefinition) == 0 && len(q.Labels) == 0 &&
		q.CurrentCluster == "" && q.CurrentClusterId == "" && q.K8sOrchestratorMode == "" &&
		q.LlmProvider == "" && q.LlmModelName == "" && len(q.LlmTierModels) == 0 && q.LlmConfigSource == "" && !q.LlmConfigReset && len(q.ToolConfigs) == 0 &&
		len(q.ClientTools) == 0 && q.Capabilities.IsEmpty() && len(q.ToolConfirmations) == 0 &&
		len(q.ToolConfigMetadata) == 0
}

// MergeFrom copies fields from src into q only when q's field is the zero value.
// This preserves existing values in q while filling in missing ones from src.
func (q *NBQueryConfig) MergeFrom(src NBQueryConfig) {
	if q.EventId == "" {
		q.EventId = src.EventId
	}
	if q.RecommendationId == "" {
		q.RecommendationId = src.RecommendationId
	}
	if q.Namespace == "" {
		q.Namespace = src.Namespace
	}
	if q.Workload == "" {
		q.Workload = src.Workload
	}
	if q.GitRepo == "" {
		q.GitRepo = src.GitRepo
	}
	if q.AccountId == "" {
		q.AccountId = src.AccountId
	}
	if q.WorkflowId == "" {
		q.WorkflowId = src.WorkflowId
	}
	if q.ExecutionId == "" {
		q.ExecutionId = src.ExecutionId
	}
	if q.WorkflowDefinition == nil {
		q.WorkflowDefinition = src.WorkflowDefinition
	}
	if q.CurrentCluster == "" {
		q.CurrentCluster = src.CurrentCluster
	}
	if q.CurrentClusterId == "" {
		q.CurrentClusterId = src.CurrentClusterId
	}
	if src.Labels != nil {
		q.Labels = lo.Assign(src.Labels, q.Labels)
	}
	if q.K8sOrchestratorMode == "" {
		q.K8sOrchestratorMode = src.K8sOrchestratorMode
	}
	// Blanket (provider+model) and per-tier picks are mutually exclusive: the
	// conversation row enforces it by nulling one when the other is written
	// (UpdateConversationModelBlanket / UpdateConversationTierOverrides). Fill-
	// if-zero must respect that, or a turn switching modes inherits the mode it
	// just replaced and the two fight — a request carrying tier picks would
	// resurrect the previous turn's blanket model, which then wins for calls the
	// tier picks were meant to govern.
	//
	// So each side only inherits when this request hasn't chosen the other.
	//
	// A reset turn inherits none of it — stickiness is exactly what it is
	// undoing. The flag itself never inherits either, so the turn after a reset
	// is an ordinary turn against a now-empty config.
	if !q.LlmConfigReset {
		if len(q.LlmTierModels) == 0 {
			if q.LlmProvider == "" {
				q.LlmProvider = src.LlmProvider
			}
			if q.LlmModelName == "" {
				q.LlmModelName = src.LlmModelName
			}
		}
		if len(q.LlmTierModels) == 0 && src.LlmTierModels != nil && q.LlmProvider == "" && q.LlmModelName == "" {
			q.LlmTierModels = src.LlmTierModels
		}
		// The pinned config source is orthogonal to both — it selects the
		// credential, not the model — so it inherits regardless of which mode
		// the turn uses.
		if q.LlmConfigSource == "" {
			q.LlmConfigSource = src.LlmConfigSource
		}
	}
	if src.ToolConfigs != nil {
		q.ToolConfigs = lo.Assign(src.ToolConfigs, q.ToolConfigs)
	}
	if q.ClientTools == nil {
		q.ClientTools = src.ClientTools
	}
	if !src.Capabilities.IsEmpty() {
		q.Capabilities = q.Capabilities.Merge(src.Capabilities)
	}
	if src.ToolConfirmations != nil {
		q.ToolConfirmations = lo.Assign(src.ToolConfirmations, q.ToolConfirmations)
	}
	if src.ToolConfigMetadata != nil {
		q.ToolConfigMetadata = lo.Assign(src.ToolConfigMetadata, q.ToolConfigMetadata)
	}
}

type NbToolContext struct {
	AccountId      string
	UserId         string
	Query          string
	QueryContext   string
	QueryConfig    NBQueryConfig
	ConversationId string
	ParentAgentId  string
	MessageId      string
	History        []llms.MessageContent
	Ctx            *security.RequestContext
	ToolConfig     ToolConfig
	ToolCallId     string
	AccountPrompt  string
	// SessionId is the top-level conversation session ID. Propagated from parent
	// agents so sub-agents (e.g. code_analyzer) can pass it to external services
	// like the workspace pod for conversation linking.
	SessionId string
	// InheritSkillsFromAgents is the chain of ancestor agent names whose mapped KBs
	// should be merged into the sub-agent's <skill-lists>. Custom-planner delegators
	// append their own name to this slice when constructing the NbToolContext for a
	// sub-agent so the lazy load_skills mechanism in ReAct planners can find
	// the parent's skills. Sub-agents reached via ExecuteAgentToolCall rebuild any
	// eager SkillsContext fresh from this list, so SkillsContext itself is not
	// carried on this struct.
	InheritSkillsFromAgents []string
	// OriginalQuery and SelectedSkillIds carry the top-level question-aware skill
	// selection across delegation. See NBAgentRequest field comments for semantics.
	OriginalQuery    string
	SelectedSkillIds []string
	// Stats accumulates the DB/relay split for the in-flight tool call. A tool
	// that records sets this on its local copy of the context at the top of
	// Call(); because NbToolContext is passed by value the pointer — not the
	// counters — is what propagates, so every helper and fan-out goroutine
	// downstream increments the same accumulator. Nil for tools that don't
	// record, and RecordDB/RecordRelay are nil-safe.
	Stats *ToolCallStats
}

// GoContext returns the underlying context.Context for outbound trace
// propagation, falling back to context.Background() when no RequestContext is
// attached (e.g. discovery paths or tests). This keeps the otelhttp transport's
// traceparent injection working without risking a nil-pointer panic.
func (tc NbToolContext) GoContext() context.Context {
	if tc.Ctx == nil || tc.Ctx.GetContext() == nil {
		return context.Background()
	}
	return tc.Ctx.GetContext()
}

func NewNbToolContext(ctx *security.RequestContext, tool NBTool, accountId string, userId string, conversationId string, messageId string, agenId string, query string, history []llms.MessageContent, queryContext string, queryConfig NBQueryConfig, toolCallId string) NbToolContext {

	toolConfigs := map[string]string{}
	if queryConfig.ToolConfigs != nil {
		for k, v := range queryConfig.ToolConfigs {
			toolConfigs[k] = v
		}
	}

	var toolConfig ToolConfig
	if tool != nil {
		configs, err := ListToolConfigs(ctx, accountId, tool)
		if err != nil {
			ctx.GetLogger().Error("tools: unable to build context", "erorr", err)
		}
		if len(configs) == 1 && toolConfigs[tool.Name()] == "" {
			toolConfig = configs[0]
		} else if len(configs) > 0 && toolConfigs[tool.Name()] != "" {
			configName := toolConfigs[tool.Name()]
			for _, config := range configs {
				if config.Name == configName {
					toolConfig = config
					break
				}
			}
		}
	}

	toolContext := NbToolContext{
		AccountId:      accountId,
		UserId:         userId,
		Query:          query,
		History:        history,
		ConversationId: conversationId,
		ParentAgentId:  agenId,
		MessageId:      messageId,
		QueryConfig:    queryConfig,
		QueryContext:   queryContext,
		Ctx:            ctx,
		ToolConfig:     toolConfig,
		ToolCallId:     toolCallId,
	}
	return toolContext
}

// updateMessageHistory updates the message history with the assistant's
// response and requested tool calls.
func UpdateMessageHistory(messageHistory []llms.MessageContent, resp *llms.ContentResponse) []llms.MessageContent {
	respchoice := resp.Choices[0]

	assistantResponse := llms.TextParts(llms.ChatMessageTypeAI, respchoice.Content)
	for _, tc := range respchoice.ToolCalls {
		assistantResponse.Parts = append(assistantResponse.Parts, tc)
	}
	return append(messageHistory, assistantResponse)
}
