package core

import (
	"fmt"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
)

// parentToolCallIDKey is the metadata field linking a sub-step row to the tool
// call that produced it. Every aggregation over llm_conversation_tool_calls
// filters on it being NULL so child rows never inflate call counts, latency
// percentiles, or the reasoning attribution's "first tool call at/after this LLM
// call" mapping. Adding a new aggregation without that filter is the failure
// mode to watch for — it produces wrong numbers silently rather than erroring.
const parentToolCallIDKey = "parent_tool_call_id"

// PersistToolCallSteps writes each downstream operation a tool performed (an
// inventory-DB query, a command run in the cluster) as its own
// llm_conversation_tool_calls row, linked to the parent through
// metadata.parent_tool_call_id.
//
// The point is debuggability from the conversation UI: the parent row says a
// resource lookup took 4s, but not whether that was one indexed query or twenty
// sequential kubectl calls, nor what any of them returned. Child rows carry the
// exact command and its full output, so the answer is in the UI instead of
// requiring a SQL client and the server logs.
//
// Called from both persistence paths — the ReAct executor's callback handler and
// CallTool — because a tool records steps regardless of which one invoked it.
// Failures are logged and swallowed: telemetry must never change what the agent
// returns.
func PersistToolCallSteps(ctx *security.RequestContext, conversationID, accountID, userID, messageID, agentID, parentToolCallID string, meta *toolcore.NBToolResponseMetadata, toolType toolcore.NBToolType) {
	if meta == nil || len(meta.Steps) == 0 || parentToolCallID == "" {
		return
	}
	dao := GetConversationDao()
	for i, step := range meta.Steps {
		metaJSON, err := common.MarshalJson(map[string]any{
			parentToolCallIDKey:     parentToolCallID,
			"step_index":            i,
			"kind":                  step.Kind,
			"execution_duration_ms": step.Duration.Milliseconds(),
			"exit_status":           boolToExit(step.Err != ""),
		})
		if err != nil {
			ctx.GetLogger().Warn("toolcallsteps: failed to marshal step metadata", "error", err)
			continue
		}

		status := toolcore.NBToolResponseStatusSuccess
		response := step.Output
		if step.Err != "" {
			status = toolcore.NBToolResponseStatusError
			response = step.Err
		}

		// Deterministic per-step id keyed off the parent so a retried write
		// upserts the same row instead of duplicating the step list.
		stepToolID := fmt.Sprintf("%s#%d", parentToolCallID, i)
		if saveErr := dao.SaveConversationToolCall(conversationID, accountID, userID, messageID, agentID,
			stepToolID, stepToolName(step.Kind), stripNullBytes(step.Command), "", "", stripNullBytes(response),
			status, toolType, nil, nil, metaJSON, nil); saveErr != nil {
			ctx.GetLogger().Error("toolcallsteps: unable to save step row", "kind", step.Kind, "error", saveErr)
		}
	}
	if meta.StepsDropped > 0 {
		ctx.GetLogger().Info("toolcallsteps: step list capped",
			"recorded", len(meta.Steps), "dropped", meta.StepsDropped, "parent_tool_call_id", parentToolCallID)
	}
}

// stepToolName labels a child row by what it did, so the UI reads
// "Inventory Query" / "Cluster Command" rather than repeating the parent's name.
func stepToolName(kind string) string {
	if kind == "db" {
		return "inventory_query"
	}
	return "cluster_command"
}

func boolToExit(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

// NewAgentToolCallId returns the per-invocation id used as the tool_id column.
// SaveConversationToolCall upserts on (conversation_id, message_id, tool_id,
// tool_name, agent_id), so parallel calls to the same tool need distinct ids or
// they collapse into one row.
func NewAgentToolCallId() string {
	return uuid.NewString()
}
