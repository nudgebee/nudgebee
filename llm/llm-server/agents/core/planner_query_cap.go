package core

import (
	"fmt"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/google/uuid"
)

// maxOrchestratorQueryBytes bounds the per-iteration query text the ReAct3
// planner renders into its human message (<question>{{.input}}</question>) for
// shell-capable orchestrating agents. It mirrors maxInvestigationPromptBytes —
// the event-analyzer path's cap in api/event_analyzer.go — using the same 64KB
// ceiling, but applied generically at the single point every source's query
// reaches the planner. This bounds Automation- and UserInvestigation-sourced
// turns the same way capInvestigationPrompt already bounds Investigation-sourced
// (event-RCA) ones.
const maxOrchestratorQueryBytes = 64000

// capOrchestratorQuery bounds request.Query before it is handed to the planner
// as the per-iteration human message. Oversized queries — machine-generated
// automation context (CI logs + cluster snapshots), user-pasted log dumps, or
// un-stripped event metadata — are re-sent UNCACHED on every planner iteration,
// so a single 200KB query can dominate a turn's token cost. When over budget the
// FULL query is offloaded to the conversation workspace as a grep-able file and
// the inline query condensed to head+tail with a bounded-grep pointer; the agent
// retrieves any detail on demand without paying the full size each iteration.
// This is the generic, source-agnostic sibling of capInvestigationPrompt, which
// caps the same content earlier but only on the event-analyzer path.
//
// The cap is applied ONLY for orchestrating agents that carry shell_execute: the
// pointer tells the agent to grep the workspace file, which is only actionable
// for a shell-capable agent operating in that same workspace. Non-shell agents
// (chat, classification, notebook) are returned unchanged rather than handed a
// pointer they cannot act on. Callers invoke this on the run branch (immediately
// before chains.Run), where the conversation and its workspace already exist, so
// the write succeeds; if the workspace is nonetheless unavailable the query is
// plainly middle-truncated with an explicit marker and NO file pointer, so a
// dangling reference is never produced. request.Query itself is never mutated —
// only the string handed to the planner is bounded, leaving classification,
// query refinement, and persistence to see the verbatim query.
func capOrchestratorQuery(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest) string {
	query := request.Query
	if len(query) <= maxOrchestratorQueryBytes {
		return query
	}
	// Gate: only shell-capable orchestrating agents can act on the grep pointer.
	// Resolve tools the same way the planner does (SupportedToolsForRequest), so
	// request-aware agents that adjust their toolset per request are judged on the
	// set they actually run with. Evaluated lazily — only for the rare oversized query.
	if agent.GetPlannerType() != AgentPlannerTypeOrchestrating || !agentHasShellTool(ctx, agent, request) {
		return query
	}

	head := maxOrchestratorQueryBytes * 3 / 4
	tail := maxOrchestratorQueryBytes / 4
	condensed := TruncateMiddle(query, head, tail)

	file := fmt.Sprintf("investigation_query_%s.txt", queryOverflowKey(request))
	saved := saveQueryOverflowToWorkspace(ctx, request.AccountId, request.SessionId, file, query)
	if saved == "" {
		// Workspace unavailable — degrade to plain truncation, never a dangling pointer.
		ctx.GetLogger().Warn("planner: oversized query truncated; workspace offload unavailable",
			"agent", agent.GetName(), "original_bytes", len(query), "cap", maxOrchestratorQueryBytes)
		return condensed + fmt.Sprintf("\n\n[Query condensed from %d bytes; full text unavailable this turn.]", len(query))
	}
	ctx.GetLogger().Warn("planner: oversized query offloaded to workspace",
		"agent", agent.GetName(), "original_bytes", len(query), "cap", maxOrchestratorQueryBytes, "workspace_file", saved)
	return condensed + fmt.Sprintf("\n\n[The query above was condensed from %d bytes; the full query is in workspace file %s. If you need a detail truncated above, grep it, e.g. grep -in \"<keyword>\" %s | head -40.]", len(query), saved, saved)
}

// queryOverflowKey picks a stable, per-turn-unique filename suffix so two
// overflowing turns in the same conversation do not clobber each other's file.
func queryOverflowKey(request NBAgentRequest) string {
	if request.MessageId != "" {
		return request.MessageId
	}
	if request.ConversationId != "" {
		return request.ConversationId
	}
	return request.SessionId
}

// agentHasShellTool reports whether the agent's tool set for this request
// includes shell_execute, the tool the offload pointer instructs the agent to
// grep with. Uses SupportedToolsForRequest (the same resolution the planner
// uses) so request-aware agents are judged on the toolset they actually run with.
func agentHasShellTool(ctx *security.RequestContext, agent NBAgent, request NBAgentRequest) bool {
	for _, t := range SupportedToolsForRequest(ctx, agent, request) {
		if t.Name() == toolcore.ToolExecuteShellCommand {
			return true
		}
	}
	return false
}

// saveQueryOverflowToWorkspace persists the full query to the conversation's
// workspace and returns the filename to point the agent at ("" = disabled or on
// failure). Mirrors saveOverflowToWorkspace in api/event_analyzer.go: it is
// best-effort and degrades gracefully rather than panicking when the DAO or
// workspace is unavailable. An empty sessionId disables offload.
func saveQueryOverflowToWorkspace(ctx *security.RequestContext, accountId, sessionId, filename, content string) string {
	if sessionId == "" {
		return ""
	}
	dao := GetConversationDao()
	if dao == nil {
		ctx.GetLogger().Warn("planner: conversation DAO unavailable for query overflow", "session_id", sessionId)
		return ""
	}
	conv, err := dao.GetConversationBySession(accountId, sessionId)
	if err != nil || conv.ID == uuid.Nil {
		ctx.GetLogger().Warn("planner: cannot resolve conversation for query overflow", "error", err, "session_id", sessionId)
		return ""
	}
	if err := workspace.NewWorkspaceManager().SaveFile(ctx, accountId, conv.ID.String(), filename, content); err != nil {
		ctx.GetLogger().Warn("planner: failed to save query overflow to workspace", "error", err, "file", filename)
		return ""
	}
	return filename
}
