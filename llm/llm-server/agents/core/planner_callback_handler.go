package core

import (
	"fmt"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"slices"
	"strings"
)

func newPlannerExecutorCallbackHandler(ctx *security.RequestContext, request NBAgentRequest, agent NBAgent) NBAgentToolCallback {
	return &plannerExecutorCallbackHandler{
		ctx:     ctx,
		request: request,
		agent:   agent,
	}
}

type plannerExecutorCallbackHandler struct {
	ctx     *security.RequestContext
	request NBAgentRequest
	agent   NBAgent
}

var toolCallFailedResponse = []string{"[]", "", "{}", "null", "none", toolcore.ErrUnableToFetchData.Error(), "no data found", `{"result":[]}`}

// stripNullBytes removes null bytes (0x00) from strings.
// Null bytes are valid UTF-8 but rejected by PostgreSQL text columns.
func stripNullBytes(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

func (h *plannerExecutorCallbackHandler) findTool(toolName string) (toolcore.NBTool, bool) {
	// check if it's a client tool first
	for _, ct := range h.request.ClientTools {
		if strings.EqualFold(ct.Name, toolName) {
			return toolcore.NewClientToolWrapper(ct), true
		}
	}

	nameToTool := getNameToTool(h.agent.GetSupportedTools(h.ctx))
	if tool, ok := nameToTool[strings.ToUpper(toolName)]; ok {
		return tool, true
	}

	// Handle common aliases and prioritize system tools over custom agents/tools
	resolvedToolName := toolName
	if strings.EqualFold(resolvedToolName, "shell") {
		resolvedToolName = toolcore.ToolExecuteShellCommand
	}

	// check for registered system/custom tools FIRST
	if tool, found := toolcore.GetNBTool(h.request.AccountId, resolvedToolName); found && tool != nil {
		return tool, true
	}

	// fallback to custom agent with same name
	if agent, found := GetCustomNbAgent(h.ctx, h.request.AccountId, resolvedToolName, AgentStatusEnabled); found {
		return NewToolFromAgent(agent), true
	}

	return nil, false
}

func (h *plannerExecutorCallbackHandler) BeforeToolCall(toolcall NBAgentPlannerToolAction) {
	tool, ok := h.findTool(toolcall.Tool)

	if !ok {
		h.ctx.GetLogger().Error("toolcallbackhandler: tool not found", "tool", toolcall.Tool)
		return
	}
	userId := h.request.UserId
	if userId == "" {
		userId = h.ctx.GetSecurityContext().GetUserId()
	}
	var parameters = toolcall.ToolInput
	log := toolcall.Log
	var sqlArgs = ""
	if toolcall.Tool != tools.ToolExecutePostgresQuery && containsSQLQuery(fmt.Sprintf("%v", toolcall.ToolInput)) {
		agentInput, err := GetConversationDao().GetConversationAgentInput(h.request.AgentId, h.request.AccountId)
		if err != nil {
			h.ctx.GetLogger().Error("toolcallbackhandler: unable to get agent input", "error", err.Error())
		}
		parameters = agentInput
		sqlArgs = fmt.Sprintf("%v", toolcall.ToolInput)
	}
	err := GetConversationDao().SaveConversationToolCall(h.request.ConversationId, h.request.AccountId, userId, h.request.MessageId, h.request.AgentId, toolcall.ToolID, toolcall.Tool, stripNullBytes(parameters), stripNullBytes(log), stripNullBytes(sqlArgs), "", toolcore.NBToolResponseStatusInProgress, tool.GetType(), nil, nil)
	if err != nil {
		h.ctx.GetLogger().Error("toolcallbackhandler: unable to save tool call", "error", err.Error())
	}
}

func (h *plannerExecutorCallbackHandler) AfterToolCallResponse(tcr NBAgentPlannerToolAction, response toolcore.NBToolResponse) {
	tool, ok := h.findTool(tcr.Tool)

	if !ok {
		h.ctx.GetLogger().Error("toolcallbackhandler: tool not found", "tool", tcr.Tool)
		return
	}
	userId := h.request.UserId
	if userId == "" {
		userId = h.ctx.GetSecurityContext().GetUserId()
	}
	status := toolcore.NBToolResponseStatusSuccess
	if slices.Contains(toolCallFailedResponse, response.Data) {
		status = toolcore.NBToolResponseStatusError
	}
	if response.Status != "" {
		status = response.Status
	}
	var refAgentId *string
	if response.AdditionalDetails != nil && response.AdditionalDetails[nbToolCallAdditionalDatailsAgentId] != nil && response.AdditionalDetails[nbToolCallAdditionalDatailsAgentId] != "" {
		refAgentId2 := response.AdditionalDetails[nbToolCallAdditionalDatailsAgentId].(string)
		if refAgentId2 != h.request.AgentId {
			refAgentId = &refAgentId2
		}
	}

	err := GetConversationDao().SaveConversationToolCall(h.request.ConversationId, h.request.AccountId, userId, h.request.MessageId, h.request.AgentId, tcr.ToolID, tcr.Tool, "", stripNullBytes(tcr.Log), "", stripNullBytes(response.Data), status, tool.GetType(), refAgentId, response.References)
	if err != nil {
		h.ctx.GetLogger().Error("toolcallbackhandler: unable to save tool call", "error", err.Error())
	}

	// Save skill references as agent-level KB references so they appear in the
	// "Additional Contexts" tab. The Url field carries the KB ID for the join.
	if strings.EqualFold(tcr.Tool, "load_skills") && status == toolcore.NBToolResponseStatusSuccess {
		var kbRefs []AgentReference
		for _, ref := range response.References {
			if ref.Type == "skill" && ref.Url != "" {
				kbRefs = append(kbRefs, AgentReference{
					Type:        AgentReferenceTypeKB,
					ReferenceID: ref.Url,
					Metadata: map[string]any{
						"name":        ref.Text,
						"description": ref.Description,
					},
				})
			}
		}
		if len(kbRefs) > 0 {
			if refErr := GetConversationDao().SaveAgentReferences(h.request.AccountId, h.request.ConversationId, h.request.MessageId, h.request.AgentId, kbRefs); refErr != nil {
				h.ctx.GetLogger().Warn("toolcallbackhandler: failed to save skill KB references", "error", refErr)
			}
		}
	}
}
