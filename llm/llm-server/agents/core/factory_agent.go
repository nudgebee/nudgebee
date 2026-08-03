package core

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
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

// RegisterNBAgentFactoryWithAliases registers a system-agent factory under its
// primary name plus one or more legacy alias names, so an agent renamed in place
// keeps resolving under its former name. GetNBAgent does exact-name lookup, so a
// renamed agent (e.g. "aws_debug" → "aws_orchestrator") would otherwise stop
// resolving for stored conversation history and explicit @old_name invocations.
func RegisterNBAgentFactoryWithAliases(agent string, agentFactory func(accountId string) (NBAgent, error), aliases ...string) {
	RegisterNBAgentFactory(agent, agentFactory)
	for _, alias := range aliases {
		RegisterNBAgentFactory(alias, agentFactory)
	}
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

// maxAncestorWalkForAgentResolution caps how many parent_agent_id hops the
// resolver will follow before giving up. In practice depth is 1 (a delegate
// sub-agent → its top-level orchestrator); the cap is a runaway-loop backstop
// for a corrupted parent chain.
const maxAncestorWalkForAgentResolution = 8

// ResolveAgentByConversationAgentId translates a llm_conversation_agent row's
// UUID into an executable NBAgent. When the row's agent_name is a registered
// agent, that agent is returned directly. When it's NOT registered — the case
// that happens today for sub-agents constructed on-the-fly by tool wrappers
// (delegate_agent's dynamicReActAgent is the only such wrapper today) — the
// resolver walks up parent_agent_id until it finds a registered ancestor and
// returns that instead.
//
// Why walk up: on a followup-answer resume, the frontend POSTs the sub-agent's
// UUID because that's what the persisted followup message pointed at. Naïvely
// looking up NBAgent by the sub-agent's agent_name would return nil ("agent
// not found") and the whole request would 400 — leaving the user's approved
// write silently unexecuted and the message stuck WAITING forever. The
// registered ancestor is the real orchestrator (e.g. k8s_orchestrator_lean)
// that made the delegation; resuming it re-invokes its planner, which
// re-plans the delegation with the same intent. The user's Yes rides through
// via QueryConfig.ToolConfirmations (propagated from parent to sub-agent's
// request at factory_agent.go:361), so the sub-agent's re-plan sees the
// pre-approval and executes the tool for real.
//
// Root cause: prod log `api: agent not found agent_name=delegate_agent`
// at conv 7564e464 (2026-08-01). Every delegated approve-path hung; reject
// path (493eab2b) worked because reject is short-circuited before this
// resolver runs.
//
// Returns:
//   - agent: the resolved NBAgent (may be an ancestor if walk-up succeeded)
//   - resolvedAgentDto: the ConversationAgent row for `agent_uuid` (NOT the
//     ancestor). The caller needs this for downstream logging/audit and to
//     keep the request's original agent_id intact when calling
//     HandleConversationSessionRequest with WithAgentId — the executor's
//     resume state lookup keys off the ORIGINAL sub-agent's row.
//   - walkedLevels: how many parent hops were taken (0 when the direct lookup
//     succeeded); useful for the caller to log the fallback.
//   - err: non-nil only for DAO failures; when the walk fails to find a
//     registered ancestor the returned agent is nil with err == nil, so the
//     caller can distinguish "resolve failed cleanly" (400 to user) from
//     "DAO exploded" (500 to user).
func ResolveAgentByConversationAgentId(ctx *security.RequestContext, agentUUID uuid.UUID, accountId string) (agent NBAgent, resolvedAgentDto *ConversationAgent, walkedLevels int, err error) {
	rows, err := GetConversationDao().ListConversationAgents("", agentUUID.String())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("ResolveAgentByConversationAgentId: list conversation agents for %s: %w", agentUUID, err)
	}
	if len(rows) == 0 {
		return nil, nil, 0, nil
	}
	dto := rows[0]
	if candidate, ok := GetNBAgent(ctx, dto.AgentName, accountId, AgentStatusEnabled); ok {
		return candidate, &dto, 0, nil
	}

	currentID := dto.ParentAgentID
	for i := 0; i < maxAncestorWalkForAgentResolution && currentID != uuid.Nil; i++ {
		// DAO errors in the walk are propagated (not swallowed) so a transient
		// DB failure surfaces as 500 at the API layer instead of getting
		// laundered into a false "agent not found" 400. len(parents) == 0 is
		// a clean chain-break — leave the loop and let the caller decide.
		parents, pErr := GetConversationDao().ListConversationAgents("", currentID.String())
		if pErr != nil {
			return nil, &dto, 0, fmt.Errorf("ResolveAgentByConversationAgentId: list parent agents for %s (starting from %s): %w", currentID, agentUUID, pErr)
		}
		if len(parents) == 0 {
			break
		}
		parent := parents[0]
		if candidate, ok := GetNBAgent(ctx, parent.AgentName, accountId, AgentStatusEnabled); ok {
			return candidate, &dto, i + 1, nil
		}
		currentID = parent.ParentAgentID
	}
	return nil, &dto, 0, nil
}

const nbToolCallAdditionalDatailsAgentId = "agent_id"
const nbToolCallAdditionalDatailsMessageId = "message_id"
const nbToolCallAdditionalDatailsQuery = "query"
const nbToolCallAdditionalDatailsFollowupRequest = "followup_request"

// Exported aliases for bespoke sub-agent-as-tool wrappers (agent_delegate.go,
// agent_log.go) that build their own AdditionalDetails maps outside this package
// and must use the same keys the callback persistence layer expects. Value-identical
// to the unexported constants above; kept in sync so a single rename would catch both.
const (
	NBToolCallAdditionalDetailsAgentId         = nbToolCallAdditionalDatailsAgentId
	NBToolCallAdditionalDetailsMessageId       = nbToolCallAdditionalDatailsMessageId
	NBToolCallAdditionalDetailsQuery           = nbToolCallAdditionalDatailsQuery
	NBToolCallAdditionalDetailsFollowupRequest = nbToolCallAdditionalDatailsFollowupRequest
)

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
	if strings.TrimSpace(input.Command) == "" && len(input.Arguments) == 0 {
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   fmt.Sprintf("%s requires a non-empty query describing what to investigate. Provide a plain-text description of the problem or question.", m.name),
		}, nil
	}

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
		return toolcore.NBToolResponse{AdditionalDetails: additionalDetails, References: resp.References}, err
	}

	// Build the sub-agent evidence manifest ONCE, from the original chronological
	// step order — before the fallback path below may reverse resp.AgentStepResponse
	// in place. The manifest is budget-bounded, so this is cheap even for
	// high-volume sub-agents (logs/metrics/traces). Empty unless the feature is on.
	subAgentEvidence := BuildSubAgentEvidenceForTool(nbRequestContext.Ctx, m.name, resp.AgentStepResponse)

	if resp.Status == ConversationStatusWaiting {
		additionalDetails[nbToolCallAdditionalDatailsQuery] = resp.Query
		additionalDetails[nbToolCallAdditionalDatailsFollowupRequest] = resp.FollowupRequest
		return toolcore.NBToolResponse{
			Data:              resp.Response[0],
			Status:            toolcore.NBToolResponseStatusWaiting,
			Type:              toolcore.NBToolResponseTypeText,
			IsTerminal:        resp.IsTerminal,
			AdditionalDetails: additionalDetails,
			References:        resp.References,
			SubAgentEvidence:  subAgentEvidence,
		}, nil
	} else if resp.Status == ConversationStatusFailed {
		responseData := "Agent failed to provide a response."
		if len(resp.Response) > 0 {
			responseData = resp.Response[0]
		}
		return toolcore.NBToolResponse{
			Data:              responseData,
			Status:            toolcore.NBToolResponseStatusError,
			Type:              toolcore.NBToolResponseTypeText,
			IsTerminal:        resp.IsTerminal,
			AdditionalDetails: additionalDetails,
			References:        resp.References,
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
			if content, ok := lastNonTrivialStepContent(resp.AgentStepResponse); ok {
				return toolcore.NBToolResponse{
					Data:              content,
					Type:              toolcore.NBToolResponseTypeJson,
					Status:            toolcore.NBToolResponseStatusSuccess,
					IsTerminal:        resp.IsTerminal,
					AdditionalDetails: additionalDetails,
					References:        resp.References,
					SubAgentEvidence:  subAgentEvidence,
				}, nil
			}
		}
		return toolcore.NBToolResponse{
			Data:              resp.Response[0],
			Type:              toolcore.NBToolResponseTypeText,
			Status:            toolcore.NBToolResponseStatusSuccess,
			IsTerminal:        resp.IsTerminal,
			AdditionalDetails: additionalDetails,
			References:        resp.References,
			SubAgentEvidence:  subAgentEvidence,
		}, nil
	} else if content, ok := lastNonTrivialStepContent(resp.AgentStepResponse); ok {
		// No synthesized answer — return the last raw observation, but mark it
		// failed so callers don't mistake it for a result.
		return toolcore.NBToolResponse{
			Data:              content,
			Type:              toolcore.NBToolResponseTypeJson,
			AdditionalDetails: additionalDetails,
			Status:            toolcore.NBToolResponseStatusError,
			IsTerminal:        resp.IsTerminal,
			References:        resp.References,
			SubAgentEvidence:  subAgentEvidence,
		}, nil
	}

	return toolcore.NBToolResponse{
		AdditionalDetails: additionalDetails,
		Data:              "No response from agent",
		Type:              toolcore.NBToolResponseTypeText,
		Status:            toolcore.NBToolResponseStatusError,
		References:        resp.References,
	}, toolcore.ErrUnableToFetchData
}

// lastNonTrivialStepContent scans steps newest-first and returns the content of
// the most recent tool invocation whose response isn't empty or an empty JSON
// container ("[]"/"{}"/"null"). ok is false if no step qualifies.
func lastNonTrivialStepContent(steps []ToolInvocation) (content string, ok bool) {
	for i := len(steps) - 1; i >= 0; i-- {
		c := strings.TrimSpace(steps[i].Response.Content)
		if c != "" && c != "[]" && c != "{}" && c != "null" {
			return steps[i].Response.Content, true
		}
	}
	return "", false
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
		// ReAct planners rather than reinventing skill injection.
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
