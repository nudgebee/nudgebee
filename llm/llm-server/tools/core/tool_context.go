package core

import (
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

	// Query filtering labels (e.g. nb_cloud_account_id for AWS timeseries)
	Labels map[string]any `json:"labels,omitempty"`

	// LLM provider overrides (per-request)
	LlmProvider  string `json:"llm_provider,omitempty"`
	LlmModelName string `json:"llm_model_name,omitempty"`

	// Original user query — preserved across sub-agent boundaries so that
	// config selection strategies can access natural-language hints (e.g. "dev-aws")
	// that the planner may strip when rewriting the step query.
	OriginalUserQuery string `json:"original_user_query,omitempty"`

	// Tool infrastructure (managed by executor/planner)
	ToolConfigs        map[string]string `json:"tool_configs,omitempty"`
	ClientTools        []NBToolCommand   `json:"client_tools,omitempty"`
	Capabilities       map[string]any    `json:"capabilities,omitempty"`
	ToolConfirmations  map[string]string `json:"tool_confirmations,omitempty"`
	ToolConfigMetadata map[string]any    `json:"tool_config_metadata,omitempty"`
}

// IsEmpty returns true when the config has no meaningful values set.
func (q NBQueryConfig) IsEmpty() bool {
	return q.EventId == "" && q.RecommendationId == "" && q.Namespace == "" &&
		q.Workload == "" && q.GitRepo == "" && q.AccountId == "" &&
		q.WorkflowId == "" && q.ExecutionId == "" && len(q.WorkflowDefinition) == 0 && len(q.Labels) == 0 &&
		q.LlmProvider == "" && q.LlmModelName == "" && len(q.ToolConfigs) == 0 &&
		len(q.ClientTools) == 0 && len(q.Capabilities) == 0 && len(q.ToolConfirmations) == 0 &&
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
	if src.Labels != nil {
		q.Labels = lo.Assign(src.Labels, q.Labels)
	}
	if q.LlmProvider == "" {
		q.LlmProvider = src.LlmProvider
	}
	if q.LlmModelName == "" {
		q.LlmModelName = src.LlmModelName
	}
	if src.ToolConfigs != nil {
		q.ToolConfigs = lo.Assign(src.ToolConfigs, q.ToolConfigs)
	}
	if q.ClientTools == nil {
		q.ClientTools = src.ClientTools
	}
	if src.Capabilities != nil {
		q.Capabilities = lo.Assign(src.Capabilities, q.Capabilities)
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
	// agents so sub-agents (e.g. agent_code_2) can pass it to external services
	// like the workspace pod for conversation linking.
	SessionId string
	// InheritSkillsFromAgents is the chain of ancestor agent names whose mapped KBs
	// should be merged into the sub-agent's <skill-lists>. Custom-planner delegators
	// append their own name to this slice when constructing the NbToolContext for a
	// sub-agent so the lazy load_skills mechanism in ReAct/ReWoo planners can find
	// the parent's skills. Sub-agents reached via ExecuteAgentToolCall rebuild any
	// eager SkillsContext fresh from this list, so SkillsContext itself is not
	// carried on this struct.
	InheritSkillsFromAgents []string
	// OriginalQuery and SelectedSkillIds carry the top-level question-aware skill
	// selection across delegation. See NBAgentRequest field comments for semantics.
	OriginalQuery    string
	SelectedSkillIds []string
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
