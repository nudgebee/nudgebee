package core

import (
	"fmt"
	"maps"
	"nudgebee/llm/common"
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

// internalAdditionalDetailsKeys names the AdditionalDetails entries that are
// part of the in-process plumbing — sub-agent routing, ReWOO orchestration,
// follow-up handoff, client-tool waiting/resume — and have no business landing
// in the persisted metadata column. Anything NOT in this set is treated as
// tool-surfaced telemetry the downstream consumers (ai_aggregate_tool_usage,
// conversation viewer) can reason about (e.g. delegate_agent's iterations_used
// / tools_used / budget_exhausted from PR #32334, which were being silently
// dropped before the merge below was wired in).
//
// The client-tool-waiting keys (`tool_name`, `tool_input`, `tool_id`,
// `followupId`) are filtered specifically because `tool_input` carries
// LLM-generated function-call arguments and sometimes user-typed content —
// not safe to land in the analytics surface as a side-effect of the persistence
// fix. Sourced inline from executor_planner.go:1477-1479, 2010-2012, 2093, 2474
// (no shared constants today; promote alongside the next refactor that touches
// the waiting/resume path).
var internalAdditionalDetailsKeys = map[string]struct{}{
	nbToolCallAdditionalDatailsAgentId:              {},
	nbToolCallAdditionalDatailsMessageId:            {},
	nbToolCallAdditionalDatailsQuery:                {},
	nbToolCallAdditionalDatailsFollowupRequest:      {},
	toolcore.ToolMessageConfigClientToolsKey:        {},
	toolcore.ToolMessageConfigToolConfigsKey:        {},
	toolcore.ToolMessageConfigCapabilitiesKey:       {},
	toolcore.ToolMessageConfigToolConfirmationKey:   {},
	toolcore.ToolMessageConfigToolConfigMetadataKey: {},
	"tool_name":  {},
	"tool_input": {},
	"tool_id":    {},
	"followupId": {},
}

// stripNullBytes removes null bytes (0x00) from strings.
// Null bytes are valid UTF-8 but rejected by PostgreSQL text columns.
func stripNullBytes(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

// mergeToolResponseMetadata flattens NBToolResponseMetadata + the non-internal
// subset of AdditionalDetails into a single JSONB blob for the
// llm_conversation_tool_calls.metadata column. Metadata wins on key collision
// (when Metadata sets the field — `omitempty` zero values are dropped before
// the merge, so an AD-supplied key with the same name survives in that case)
// because it's the typed contract the existing readers (tool_usage.go's
// `tc.metadata->>'execution_duration_ms'` / `tc.metadata->>'stderr'`) lean on;
// AdditionalDetails only fills tool-specific gaps without that contract.
//
// If the merged map can't be marshalled (e.g. an AdditionalDetails value
// contains an unmarshalable type — a channel, function, etc.), the helper
// falls back to marshalling Metadata alone. This preserves the pre-merge
// guarantee that a tool which correctly populates Metadata never loses its
// telemetry to a misbehaving AdditionalDetails value.
//
// Returns nil when there's nothing to persist so SaveConversationToolCall stores
// SQL NULL rather than an empty `{}`, matching prior behavior.
func mergeToolResponseMetadata(metadata *toolcore.NBToolResponseMetadata, additionalDetails map[string]any) ([]byte, error) {
	merged := map[string]any{}
	for k, v := range additionalDetails {
		if _, internal := internalAdditionalDetailsKeys[k]; internal {
			continue
		}
		merged[k] = v
	}
	var metaMap map[string]any
	if metadata != nil {
		b, mErr := common.MarshalJson(metadata)
		if mErr != nil {
			return nil, fmt.Errorf("mergeToolResponseMetadata: marshal Metadata: %w", mErr)
		}
		if uErr := common.UnmarshalJson(b, &metaMap); uErr != nil {
			return nil, fmt.Errorf("mergeToolResponseMetadata: unmarshal Metadata to map: %w", uErr)
		}
		maps.Copy(merged, metaMap)
	}
	if len(merged) == 0 {
		return nil, nil
	}
	if out, mErr := common.MarshalJson(merged); mErr == nil {
		return out, nil
	} else if metaMap != nil {
		// Fall back to Metadata-only marshal so a misbehaving AD value
		// doesn't take down the typed telemetry contract too.
		if out, mErr2 := common.MarshalJson(metaMap); mErr2 == nil {
			return out, nil
		}
		return nil, fmt.Errorf("mergeToolResponseMetadata: marshal merged map + Metadata fallback both failed: %w", mErr)
	} else {
		return nil, fmt.Errorf("mergeToolResponseMetadata: marshal merged map: %w", mErr)
	}
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
	err := GetConversationDao().SaveConversationToolCall(h.request.ConversationId, h.request.AccountId, userId, h.request.MessageId, h.request.AgentId, toolcall.ToolID, toolcall.Tool, stripNullBytes(parameters), stripNullBytes(log), stripNullBytes(sqlArgs), "", toolcore.NBToolResponseStatusInProgress, tool.GetType(), nil, nil, nil)
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

	// Build the V751 metadata JSONB blob: Metadata (typed telemetry) merged
	// with the non-internal subset of AdditionalDetails (per-tool extras like
	// delegate_agent's iterations_used / tools_used). Failure is non-fatal —
	// log and persist NULL so the row still lands.
	metadataJSON, mErr := mergeToolResponseMetadata(response.Metadata, response.AdditionalDetails)
	if mErr != nil {
		h.ctx.GetLogger().Warn("toolcallbackhandler: failed to marshal tool metadata", "error", mErr)
	}
	err := GetConversationDao().SaveConversationToolCall(h.request.ConversationId, h.request.AccountId, userId, h.request.MessageId, h.request.AgentId, tcr.ToolID, tcr.Tool, "", stripNullBytes(tcr.Log), "", stripNullBytes(response.Data), status, tool.GetType(), refAgentId, response.References, metadataJSON)
	if err != nil {
		h.ctx.GetLogger().Error("toolcallbackhandler: unable to save tool call", "error", err.Error())
	}

	// Save skill references as agent-level KB references so they appear in the
	// "Additional Contexts" tab. The Url field carries the KB ID for the join.
	// Both load_skills (by name) and search_skills (by query) produce skill
	// references, so a mid-conversation search is recorded the same way.
	if (strings.EqualFold(tcr.Tool, "load_skills") || strings.EqualFold(tcr.Tool, "search_skills")) && status == toolcore.NBToolResponseStatusSuccess {
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
