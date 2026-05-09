package core

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"slices"
	"strings"

	"github.com/google/uuid"
)

var nbSystemAgents = map[string]func(accountId string) (NBAgent, error){}

func RegisterNBAgentFactory(agent string, agentFactory func(accountId string) (NBAgent, error)) {
	slog.Info("registering agent", "agent", agent)
	if _, ok := nbSystemAgents[strings.ToLower(agent)]; ok {
		slog.Warn("agent already registered", "agent", agent)
	}
	nbSystemAgents[strings.ToLower(agent)] = agentFactory
}

func NewToolFromAgent(agent NBAgent) toolcore.NBTool {
	return &nbAgentTool{name: agent.GetName(), description: agent.GetDescription(), input: "Refer description for inputs", output: "Refer description for inputs", agent: agent, toolType: toolcore.NBToolTypeAgent}
}

func RegisterNBAgentFactoryAndTool(agent string, agentFactory func(accountId string) (NBAgent, error), toolDescription string, toolInput string, toolOutput string) {
	slog.Info("registering agent", "agent", agent)
	if _, ok := nbSystemAgents[strings.ToLower(agent)]; ok {
		slog.Warn("agent already registered", "agent", agent)
	}
	nbSystemAgents[strings.ToLower(agent)] = agentFactory
	useResponseAlways := true
	toolcore.RegisterNBToolFactory(agent, func(accountId string) (toolcore.NBTool, error) {
		return &nbAgentTool{name: agent, description: toolDescription, input: toolInput, output: toolOutput, useResponseAlways: useResponseAlways, toolType: toolcore.NBToolTypeAgent, accountId: accountId}, nil
	})
}

func RegisterNBAgentFactoryAsTool(agent string, agentFactory func(accountId string) (NBAgent, error), toolDescription string, toolInput string, toolOutput string) {
	slog.Info("registering agent as tool", "agent", agent)
	if _, ok := nbSystemAgents[strings.ToLower(agent)]; ok {
		slog.Warn("agent already registered", "agent", agent)
	}
	nbSystemAgents[strings.ToLower(agent)] = agentFactory
	toolcore.RegisterNBToolFactory(agent, func(accountId string) (toolcore.NBTool, error) {
		return &nbAgentTool{name: agent, description: toolDescription, input: toolInput, output: toolOutput, useResponseAlways: true, agent: nil, toolType: toolcore.NBToolTypeTool, accountId: accountId}, nil
	})
}

func RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(agent string, agentFactory func(accountId string) (NBAgent, error), toolDescription string, toolInput string, toolOutput string) {
	slog.Info("registering agent", "agent", agent)
	if _, ok := nbSystemAgents[strings.ToLower(agent)]; ok {
		slog.Warn("agent already registered", "agent", agent)
	}
	nbSystemAgents[strings.ToLower(agent)] = agentFactory
	toolcore.RegisterNBToolFactory(agent, func(accountId string) (toolcore.NBTool, error) {
		return &nbAgentTool{name: agent, description: toolDescription, input: toolInput, output: toolOutput, useResponseAlways: true, toolType: toolcore.NBToolTypeAgent, accountId: accountId}, nil
	})
}

func getSystemAgent(agentName string, accountId string) (NBAgent, error) {
	agentFactory := nbSystemAgents[strings.ToLower(agentName)]
	if agentFactory == nil {
		return nil, fmt.Errorf("agent %s not found", agentName)
	}
	return agentFactory(accountId)
}

func GetNBAgent(ctx *security.RequestContext, agentName string, accountId string, status AgentStatus) (NBAgent, bool) {
	systemAgent, err := getSystemAgent(agentName, accountId)
	if err != nil {
		return GetCustomNbAgent(ctx, accountId, agentName, status)
	}

	customAgent, found := GetCustomNbAgent(ctx, accountId, agentName, AgentStatusEnabled)
	if found {
		return customAgent, true
	}
	return systemAgent, true
}

const nbToolCallAdditionalDatailsAgentId = "agent_id"
const nbToolCallAdditionalDatailsMessageId = "message_id"
const nbToolCallAdditionalDatailsQuery = "query"
const nbToolCallAdditionalDatailsFollowupRequest = "followup_request"

type AgentTool interface {
	toolcore.NBTool
	GetAgent(ctx *security.RequestContext) NBAgent
}

type nbAgentTool struct {
	name              string
	description       string
	input             string
	output            string
	useResponseAlways bool
	agent             NBAgent
	toolType          toolcore.NBToolType
	accountId         string
}

func (m *nbAgentTool) GetAgent(ctx *security.RequestContext) NBAgent {
	if m.agent == nil {
		if agent, ok := GetNBAgent(ctx, m.name, m.accountId, AgentStatusEnabled); ok {
			m.agent = agent
		}
	}
	return m.agent
}

func (m *nbAgentTool) Name() string {
	return m.name
}

func (m *nbAgentTool) GetType() toolcore.NBToolType {
	return m.toolType
}

func (m *nbAgentTool) Description() string {
	return fmt.Sprintf(`%s

	Usage:

	* Input: %s
	* Output: %s
	`, m.description, m.input, m.output)
}

func (m *nbAgentTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: m.input,
			},
		},
		Required: []string{"command"},
	}
}

func (m *nbAgentTool) ConfigSchema(ctx *security.RequestContext) toolcore.ToolConfigSchema {
	toolNames := map[string]any{}
	agent := m.GetAgent(ctx)
	if agent == nil {
		ctx.GetLogger().Error("agent not found", "agent", m.name)
		return toolcore.ToolConfigSchema{}
	}
	for _, t := range agent.GetSupportedTools(ctx) {
		toolNames[t.Name()] = true
	}
	return toolcore.ToolConfigSchema{
		Type:         toolcore.ToolSchemaTypeObject,
		Required:     []string{},
		ConfigSource: toolcore.ToolConfigSourceLLMAgent,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"tools": {
				Type:        toolcore.ToolSchemaTypeArray,
				Description: "List of tools to enable for the agent",
				Items:       toolNames,
			},
		},
	}
}

func (m *nbAgentTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	agent := m.GetAgent(nbRequestContext.Ctx)
	if agent == nil {
		return toolcore.NBToolResponse{}, errAgentNotFound
	}

	resp, err := ExecuteAgentToolCall(nbRequestContext, agent, input)
	additionalDetails := map[string]any{
		nbToolCallAdditionalDatailsAgentId:   resp.AgentId,
		nbToolCallAdditionalDatailsMessageId: resp.MessageId,
	}

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error(m.name+": unable to process events request", "error", err, "input", input)
		return toolcore.NBToolResponse{AdditionalDetails: additionalDetails}, err
	}

	if resp.Status == ConversationStatusWaiting {

		additionalDetails[nbToolCallAdditionalDatailsQuery] = resp.Query

		additionalDetails[nbToolCallAdditionalDatailsFollowupRequest] = resp.FollowupRequest

		return toolcore.NBToolResponse{

			Data: resp.Response[0],

			Status: toolcore.NBToolResponseStatusWaiting,

			Type: toolcore.NBToolResponseTypeText,

			IsTerminal: resp.IsTerminal,

			AdditionalDetails: additionalDetails,
		}, nil

	} else if resp.Status == ConversationStatusFailed {

		responseData := "Agent failed to provide a response."

		if len(resp.Response) > 0 {

			responseData = resp.Response[0]

		}

		return toolcore.NBToolResponse{

			Data: responseData,

			Status: toolcore.NBToolResponseStatusError,

			Type: toolcore.NBToolResponseTypeText,

			IsTerminal: resp.IsTerminal,

			AdditionalDetails: additionalDetails,
		}, toolcore.ErrUnableToFetchData

	} else if len(resp.Response) > 0 {

		useAgentResponse := m.useResponseAlways

		if !useAgentResponse {

			if _, ok := agent.(NBAgentReActPlannerSummaryToolProvider); ok {

				useAgentResponse = true

			}

		}

		if resp.IsTerminal {

			useAgentResponse = true

		}

		if !useAgentResponse {

			slices.Reverse(resp.AgentStepResponse)

			for _, invocation := range resp.AgentStepResponse {

				if invocation.Response.Content != "" && invocation.Response.Content != "[]" && invocation.Response.Content != "{}" {

					return toolcore.NBToolResponse{

						Data: invocation.Response.Content,

						Type: toolcore.NBToolResponseTypeJson,

						Status: toolcore.NBToolResponseStatusSuccess,

						IsTerminal: resp.IsTerminal,

						AdditionalDetails: additionalDetails,
					}, nil

				}

			}

		}

		return toolcore.NBToolResponse{

			Data: resp.Response[0],

			Type: toolcore.NBToolResponseTypeText,

			Status: toolcore.NBToolResponseStatusSuccess,

			IsTerminal: resp.IsTerminal,

			AdditionalDetails: additionalDetails,
		}, nil

	} else if resp.AgentStepResponse != nil {

		//sometimes LLMs are not able to analyze the responses

		//so returning data as is for next step

		slices.Reverse(resp.AgentStepResponse)

		for _, invocation := range resp.AgentStepResponse {

			if invocation.Response.Content != "" && invocation.Response.Content != "[]" && invocation.Response.Content != "{}" {

				return toolcore.NBToolResponse{

					Data: invocation.Response.Content,

					Type: toolcore.NBToolResponseTypeJson,

					AdditionalDetails: additionalDetails,

					Status: toolcore.NBToolResponseStatusSuccess,

					IsTerminal: resp.IsTerminal,
				}, nil

			}

		}

	}

	return toolcore.NBToolResponse{
		AdditionalDetails: additionalDetails,
		Data:              "No response from agent",
		Type:              toolcore.NBToolResponseTypeText,
		Status:            toolcore.NBToolResponseStatusError,
	}, toolcore.ErrUnableToFetchData

}

func ExecuteAgentToolCall(nbRequestContext toolcore.NbToolContext, agent NBAgent, query toolcore.NBToolCallRequest) (NBAgentResponse, error) {
	existingAgentId := ""
	if nbRequestContext.ToolCallId != "" {
		existingAgentId = GetConversationDao().GetConversationToolCallChildAgentId(nbRequestContext.ConversationId, nbRequestContext.ParentAgentId, nbRequestContext.ToolCallId)
		// Validate the child agent ID actually exists in llm_conversation_agent.
		// A stale/nil UUID would cause FK violations in token usage tracking.
		if existingAgentId != "" {
			if parsedUUID, err := uuid.Parse(existingAgentId); err != nil || parsedUUID == uuid.Nil {
				existingAgentId = ""
			} else {
				// Verify the record exists
				parentId, _ := GetConversationDao().GetConversationAgentParentAgentIdAndPreviousState(existingAgentId)
				if parentId == "" {
					nbRequestContext.Ctx.GetLogger().Warn("ExecuteAgentToolCall: child agent record not found, will create new agent record", "existingAgentId", existingAgentId)
					existingAgentId = ""
				}
			}
		}
	}

	if query.Context != "" {
		nbRequestContext.QueryContext = nbRequestContext.QueryContext + "\n" + query.Context
	}

	agentRequest := NBAgentRequest{
		Query:          query.Command,
		AccountId:      nbRequestContext.AccountId,
		UserId:         nbRequestContext.UserId,
		ConversationId: nbRequestContext.ConversationId,
		ParentAgentId:  nbRequestContext.ParentAgentId,
		MessageId:      nbRequestContext.MessageId,
		QueryContext:   nbRequestContext.QueryContext,
		QueryConfig:    nbRequestContext.QueryConfig,
		AgentId:        existingAgentId,
		AccountPrompt:  nbRequestContext.AccountPrompt,
		// Propagate the inherited-skills chain. Custom-planner delegators like
		// metrics, traces, logs, and logs_default append their own name to this
		// list so the sub-agent's executor can union it with its own KB lookups
		// — feeding the existing lazy <skill-lists> + load_skills flow inside
		// ReAct/ReWoo planners rather than reinventing skill injection.
		InheritSkillsFromAgents: nbRequestContext.InheritSkillsFromAgents,
		// Propagate the top-level question and the question-aware skill selection
		// computed once at top-level entry. Sub-agents must trust the parent's
		// selection — re-running it against a mechanical sub-agent command (e.g.
		// "fetch CPU for pod foo") would destroy relevance.
		OriginalQuery:    nbRequestContext.OriginalQuery,
		SelectedSkillIds: nbRequestContext.SelectedSkillIds,
		SessionId:        nbRequestContext.SessionId,
	}
	resp, err := executeAgent(nbRequestContext.Ctx, agent, agentRequest)
	return resp, err
}
