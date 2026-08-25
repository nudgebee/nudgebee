package core

import (
	"nudgebee/llm/common"
	toolcore "nudgebee/llm/tools/core"
	"time"

	"github.com/google/uuid"
)

// CallTool invokes an NBTool on behalf of an AgentPlannerTypeCustom agent and
// records the invocation the way the planner loop records its own: an
// in-progress llm_conversation_tool_calls row before the call, a terminal row
// with exit status and wall-clock duration after it, plus the same Prometheus
// counters.
//
// Custom agents drive their own tool calls instead of going through the ReAct
// executor, so they never reach plannerExecutorCallbackHandler's
// Before/AfterToolCallResponse hooks — the only place those rows and metrics
// are written. Tool persistence was therefore a side-effect of *which planner
// ran*, not of a tool having run, and every direct caller silently lost its
// telemetry: resource_search's rows stopped on 2026-02-06 (commit c79b98c63c)
// when it became a custom agent, and fetch_logs / metrics / traces / visualizer
// / websearch / unified_search never had any.
//
// This is the sanctioned direct-call path. Calling NBTool.Call from an agent is
// rejected by TestNoDirectToolCallsInAgents — that guard is the part that keeps
// the gap closed, since nothing else makes a bypass visible.
//
// Telemetry is best-effort: persistence failures are logged and swallowed so a
// DB hiccup never changes what the agent returns. The tool's response and error
// pass through untouched.
func CallTool(tc toolcore.NbToolContext, tool toolcore.NBTool, req toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	ctx := tc.Ctx
	dao := GetConversationDao()
	toolName := tool.Name()
	implType := toolcore.ImplTypeFor(tool)

	// The upsert keys on (conversation_id, message_id, tool_id, tool_name,
	// agent_id), so calls that share a tool_id collapse into one row. Callers
	// that need the tool itself to see the id (client-tool follow-ups) set it on
	// the context; everyone else gets a fresh one per invocation, which is what
	// parallel fan-outs like resource_search require.
	toolCallId := tc.ToolCallId
	if toolCallId == "" {
		toolCallId = uuid.NewString()
	}

	if err := dao.SaveConversationToolCall(tc.ConversationId, tc.AccountId, toolCallUserId(tc), tc.MessageId,
		tc.ParentAgentId, toolCallId, toolName, stripNullBytes(toolCallParameters(tc, req)), "", "", "",
		toolcore.NBToolResponseStatusInProgress, tool.GetType(), nil, nil, nil, nil); err != nil {
		ctx.GetLogger().Error("customagenttool: unable to save in-progress tool call", "tool", toolName, "error", err)
	}

	start := time.Now()
	resp, callErr := tool.Call(tc, req)
	elapsed := time.Since(start)

	status := toolcore.NBToolResponseStatusSuccess
	metricStatus := "success"
	if callErr != nil {
		status = toolcore.NBToolResponseStatusError
		metricStatus = "fail"
	} else if resp.Status != "" {
		status = resp.Status
	}
	common.MetricsToolOperationsTotal(implType, toolName, metricStatus, tc.AccountId)
	common.MetricsToolLatencySeconds(implType, toolName, tc.AccountId, elapsed.Seconds())

	// Reuse the tool's own Metadata when it set one (kubectl fills Stderr) so
	// only the executor-owned fields are overwritten.
	if resp.Metadata == nil {
		resp.Metadata = &toolcore.NBToolResponseMetadata{}
	}
	metadata := resp.Metadata
	metadata.ExitStatus = 0
	if status == toolcore.NBToolResponseStatusError {
		metadata.ExitStatus = 1
	}
	metadata.ExecutionDurationMs = elapsed.Milliseconds()

	metadataJSON, mErr := mergeToolResponseMetadata(metadata, resp.AdditionalDetails)
	if mErr != nil {
		ctx.GetLogger().Warn("customagenttool: failed to marshal tool metadata", "tool", toolName, "error", mErr)
	}
	response := resp.Data
	if callErr != nil {
		response = callErr.Error()
	}
	if err := dao.SaveConversationToolCall(tc.ConversationId, tc.AccountId, toolCallUserId(tc), tc.MessageId,
		tc.ParentAgentId, toolCallId, toolName, "", "", "", stripNullBytes(response),
		status, tool.GetType(), nil, resp.References, metadataJSON, nil); err != nil {
		ctx.GetLogger().Error("customagenttool: unable to save tool call result", "tool", toolName, "error", err)
	}

	PersistToolCallSteps(ctx, tc.ConversationId, tc.AccountId, toolCallUserId(tc), tc.MessageId,
		tc.ParentAgentId, toolCallId, metadata, tool.GetType())

	return resp, callErr
}

// toolCallUserId falls back to the request context's user when the tool context
// carries none — the tool_calls row's user_id is nullable but the planner path
// always resolves one, and the two paths should agree.
func toolCallUserId(tc toolcore.NbToolContext) string {
	if tc.UserId != "" {
		return tc.UserId
	}
	if tc.Ctx != nil && tc.Ctx.GetSecurityContext() != nil {
		return tc.Ctx.GetSecurityContext().GetUserId()
	}
	return ""
}

// toolCallParameters renders what the tool was actually asked to do, mirroring
// the planner's `toolcall.ToolInput`. Command is the common shape; Arguments-only
// callers (mermaid validation, the logs bundle tool) would otherwise persist an
// empty parameters column. NbToolContext.Query is the last resort — call sites
// pass the same input there.
func toolCallParameters(tc toolcore.NbToolContext, req toolcore.NBToolCallRequest) string {
	if req.Command != "" {
		return req.Command
	}
	if len(req.Arguments) > 0 {
		if b, err := common.MarshalJson(req.Arguments); err == nil {
			return string(b)
		}
	}
	return tc.Query
}
