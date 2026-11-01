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
	// KnowledgeHints are short identifiers of the subject (e.g. an event's
	// title, category, finding type) that knowledge discovery adds to its search
	// query. The investigation prompt itself is too long to search with.
	KnowledgeHints []string `json:"knowledge_hints,omitempty"`

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

	// K8sOrchestratorMode selects a K8s orchestrator variant per-request.
	// Empty defers to the service-level default.
	K8sOrchestratorMode string `json:"k8s_orchestrator_mode,omitempty"`

	// Log provider override: outranks LLM_SERVER_LOG_PROVIDER_OVERRIDE and account
	// routing ("k8s" = kubectl path). Case-sensitive — api-server spells ES "ES".
	// Sticky within a conversation via MergeFrom's fill-if-zero, like LlmProvider.
	LogProviderOverride string `json:"log_provider_override,omitempty"`

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

	// SkipConversationHistory runs the turn without the conversation's earlier
	// messages or distilled conversation memory. Automated event re-analysis
	// reuses one conversation per fingerprint; with history loaded, the agent
	// answered a new occurrence by rewording the previous occurrence's RCA
	// without calling a tool. Stored in message_config so sub-agents and
	// approval resumes of the same turn inherit it; never inherited by the
	// conversation's next turn (see handleConversationRequest).
	SkipConversationHistory bool `json:"skip_conversation_history,omitempty"`

	// Memory overrides the server-wide memory settings for this one request.
	// Grouped rather than three flat flags because they are one concern, and a
	// reader should not have to know that a "rerank" flag relates to a "memory"
	// one.
	Memory *MemoryOverrides `json:"memory,omitempty"`

	// Tool infrastructure (managed by executor/planner)
	ToolConfigs map[string]string `json:"tool_configs,omitempty"`
	// DelegatedTarget is scoped to one child agent invocation. It is saved on
	// the agent row for resume, never in the conversation's message_config.
	DelegatedTarget    *ExecutionTarget  `json:"delegated_target,omitempty"`
	ClientTools        []NBToolCommand   `json:"client_tools,omitempty"`
	Capabilities       AgentCapabilities `json:"capabilities,omitzero"`
	ToolConfirmations  map[string]string `json:"tool_confirmations,omitempty"`
	ToolConfigMetadata map[string]any    `json:"tool_config_metadata,omitempty"`
	DiscoveredTools    []string          `json:"discovered_tools,omitempty"`
}

// IsEmpty returns true when the config has no meaningful values set.
func (q NBQueryConfig) IsEmpty() bool {
	return q.EventId == "" && q.RecommendationId == "" && len(q.KnowledgeHints) == 0 && q.Namespace == "" &&
		q.Workload == "" && q.GitRepo == "" && q.AccountId == "" &&
		q.WorkflowId == "" && q.ExecutionId == "" && len(q.WorkflowDefinition) == 0 && len(q.Labels) == 0 &&
		q.CurrentCluster == "" && q.CurrentClusterId == "" && q.K8sOrchestratorMode == "" &&
		q.LlmProvider == "" && q.LlmModelName == "" && len(q.LlmTierModels) == 0 && q.LlmConfigSource == "" && !q.LlmConfigReset && len(q.ToolConfigs) == 0 && q.DelegatedTarget == nil &&
		len(q.ClientTools) == 0 && q.Capabilities.IsEmpty() && len(q.ToolConfirmations) == 0 &&
		len(q.ToolConfigMetadata) == 0 && q.LogProviderOverride == "" && !q.SkipConversationHistory &&
		len(q.DiscoveredTools) == 0 && q.Memory.IsEmpty()
}

// MemoryOverrides narrows what memory may do for one request. The three
// switches are independent — reading, writing and reranking are separate
// decisions, and a caller commonly wants one off without the others (a
// benchmark reads real memory but must not add to it; a cost experiment keeps
// memory but drops the reranker).
//
// Every field is a pointer because the server defaults are ON. A plain bool
// cannot tell "the caller asked for off" apart from "the caller said nothing",
// and the second must leave the deployment's setting alone — the same reason
// an empty K8sOrchestratorMode defers to the boot-time default.
//
// Off-only: these narrow, never widen. A request cannot switch on what the
// deployment, the tenant's enrolment or the user's consent has switched off.
// Enabling a licensed capability is not a caller's decision.
type MemoryOverrides struct {
	// Read injects stored memory into the prompt and serves the memory tool.
	Read *bool `json:"read,omitempty"`
	// Write records what this turn learned. Off leaves the store untouched, so
	// a diagnostic or benchmark turn cannot contaminate what it is measuring.
	Write *bool `json:"write,omitempty"`
	// Rerank scores retrieved candidates with an LLM before injection. Off
	// falls back to cosine order, and also skips the overfetch that exists only
	// to feed the reranker.
	Rerank *bool `json:"rerank,omitempty"`
}

// Clone returns an independent copy, including the switches themselves. Merging
// must never alias: src is usually the conversation's stored config, inherited
// by sub-agents that the planner runs concurrently, so a shared pointer would
// let one action's merge mutate another's view — and the executor already lost a
// process to shared request state being written from parallel actions.
func (m *MemoryOverrides) Clone() *MemoryOverrides {
	if m == nil {
		return nil
	}
	return &MemoryOverrides{Read: cloneBool(m.Read), Write: cloneBool(m.Write), Rerank: cloneBool(m.Rerank)}
}

// cloneBool copies the pointed-to value so two configs never share a switch.
func cloneBool(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// ReadSwitch and RerankSwitch expose one switch for the context carrier,
// nil-safe on an absent block so callers need no guard of their own.
func (m *MemoryOverrides) ReadSwitch() *bool {
	if m == nil {
		return nil
	}
	return m.Read
}

func (m *MemoryOverrides) RerankSwitch() *bool {
	if m == nil {
		return nil
	}
	return m.Rerank
}

// IsEmpty reports whether the caller set no override at all.
func (m *MemoryOverrides) IsEmpty() bool {
	return m == nil || (m.Read == nil && m.Write == nil && m.Rerank == nil)
}

// allowed resolves one switch: unset defers, set narrows.
func allowed(p *bool) bool { return p == nil || *p }

// MemoryReadAllowed reports whether THIS REQUEST allows memory to be read.
//
// Request scope only. Whether the deployment, the tenant's enrolment and the
// user's consent allow it is a separate question, answered by each gate's own
// config check before it asks this one. An earlier version took the deployment
// default as an argument, but every caller passed a literal true — so it
// enforced nothing while reading as though it did.
func (q NBQueryConfig) MemoryReadAllowed() bool {
	return q.Memory == nil || allowed(q.Memory.Read)
}

// MemoryWriteAllowed reports whether this request allows memory to be recorded.
// Reading off implies writing off: a turn that ran without memory should not
// shape later turns with what it produced.
func (q NBQueryConfig) MemoryWriteAllowed() bool {
	return q.MemoryReadAllowed() && (q.Memory == nil || allowed(q.Memory.Write))
}

// MemoryRerankAllowed reports whether this request allows retrieved memory to
// be LLM-reranked. Reading off implies no rerank: there is nothing to rank.
func (q NBQueryConfig) MemoryRerankAllowed() bool {
	return q.MemoryReadAllowed() && (q.Memory == nil || allowed(q.Memory.Rerank))
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
	if q.KnowledgeHints == nil {
		q.KnowledgeHints = src.KnowledgeHints
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
	if q.LogProviderOverride == "" {
		q.LogProviderOverride = src.LogProviderOverride
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
	// The log provider override selects which log backend the turn reads from, so
	// it is independent of the LLM model config above and of a reset turn.
	if q.LogProviderOverride == "" {
		q.LogProviderOverride = src.LogProviderOverride
	}
	if q.Memory == nil {
		q.Memory = src.Memory.Clone()
	} else if src.Memory != nil {
		// Per-switch fill-if-unset, matching every other field: a turn that set
		// only one switch must still inherit the others. Copied by value, so the
		// two configs never share a switch.
		if q.Memory.Read == nil {
			q.Memory.Read = cloneBool(src.Memory.Read)
		}
		if q.Memory.Write == nil {
			q.Memory.Write = cloneBool(src.Memory.Write)
		}
		if q.Memory.Rerank == nil {
			q.Memory.Rerank = cloneBool(src.Memory.Rerank)
		}
	}
	if !q.SkipConversationHistory {
		q.SkipConversationHistory = src.SkipConversationHistory
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
	if len(src.DiscoveredTools) > 0 {
		q.DiscoveredTools = lo.Uniq(append(q.DiscoveredTools, src.DiscoveredTools...))
	}
}

type NbToolContext struct {
	// AccountId is the account the conversation belongs to (the origin): it
	// owns the workspace, conversation records and billing.
	AccountId string
	// TargetAccountId is the account whose data this call reads. It equals
	// AccountId unless the call is about another connected cluster or
	// account. Read it through DataAccountID().
	TargetAccountId string
	// ExecutedTarget is the resolved target when TargetAccountId is another
	// account; it is recorded on the tool call (MetadataExecutedTarget).
	ExecutedTarget *ExecutionTarget
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
	AccountContext string
	AccountPrompt  string
	// Source is the conversation source this tool call belongs to, as a plain
	// string so tools/core stays free of the agents/core import. Empty means
	// unknown. Tools use it to tell a live user's request, where asking or
	// inferring from the user's own words is reasonable, from an unattended
	// run where nobody is there to answer and a guess is silently wrong.
	Source string

	// SessionId is the top-level conversation session ID. Propagated from parent
	// agents so sub-agents (e.g. agent_code_2) can pass it to external services
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
	// OriginalQueryFromUser mirrors NBAgentRequest.OriginalQueryFromUser.
	OriginalQueryFromUser bool
	// FollowupAnswer is the user's answer to a question this tool's sub-agent
	// asked. It is set only when a paused tool resumes after that question, so a
	// tool that runs a sub-agent can hand the answer to it instead of treating it
	// as the tool's own input.
	FollowupAnswer string
	// KBPrestepContent, KBPrestepExecuted, and KBReferences carry the pre-step retrieval results
	// across delegation so sub-agents can reuse them without triggering redundant RAG executions.
	KBPrestepContent  string
	KBPrestepExecuted bool
	KBReferences      any
	// Knowledge policy is resolved once per top-level invocation and propagated
	// through in-process delegation. Strings avoid a tools/core -> agents/core
	// dependency; only framework code reads these fields.
	KnowledgePolicy         string
	KnowledgePolicyResolved bool
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

// userConversationSources are the sources with a person waiting on the answer.
// Mirrors agents/core's isUserConversationSource, duplicated as strings because
// tools/core cannot import agents/core.
var userConversationSources = map[string]struct{}{
	"UserInvestigation":    {},
	"UserInvestigationCli": {},
}

// HasLiveUser reports whether someone is waiting on this tool call. False for
// event analysis, automations and other unattended runs.
func (c NbToolContext) HasLiveUser() bool {
	_, ok := userConversationSources[c.Source]
	return ok
}

func NewNbToolContext(ctx *security.RequestContext, tool NBTool, accountId string, userId string, conversationId string, messageId string, agenId string, query string, history []llms.MessageContent, queryContext string, queryConfig NBQueryConfig, toolCallId string, source ...string) NbToolContext {
	// Variadic so the ~37 existing callers stay untouched; only the paths that
	// know the conversation source pass it.
	conversationSource := ""
	if len(source) > 0 {
		conversationSource = source[0]
	}

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

	targetAccountID := accountId
	if target := queryConfig.DelegatedTarget; target != nil && target.AccountID != "" {
		targetAccountID = target.AccountID
	}

	toolContext := NbToolContext{
		AccountId:       accountId,
		TargetAccountId: targetAccountID,
		UserId:          userId,
		Query:           query,
		History:         history,
		ConversationId:  conversationId,
		ParentAgentId:   agenId,
		MessageId:       messageId,
		QueryConfig:     queryConfig,
		QueryContext:    queryContext,
		Ctx:             ctx,
		ToolConfig:      toolConfig,
		ToolCallId:      toolCallId,
		Source:          conversationSource,
	}
	return toolContext
}

// DataAccountID is the account this call reads data from: the target when
// the call is about another account, otherwise the conversation's own.
// Contexts built without NewNbToolContext leave TargetAccountId empty.
func (c NbToolContext) DataAccountID() string {
	if c.TargetAccountId != "" {
		return c.TargetAccountId
	}
	return c.AccountId
}

// ForTarget returns the context a follows-target tool reads data with: its
// AccountId is the call's data account. Keep the original context for
// anything that belongs to the conversation (workspace files, references to
// the conversation). The executor has already resolved and access-checked
// TargetAccountId.
func (c NbToolContext) ForTarget() NbToolContext {
	c.AccountId = c.DataAccountID()
	c.TargetAccountId = c.AccountId
	c.QueryConfig.DelegatedTarget = nil
	return c
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
