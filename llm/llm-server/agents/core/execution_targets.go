package core

import (
	"encoding/json"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// renderExecutionTargetsBlock lists the clusters and cloud accounts this
// request can run commands against. It goes in the human message: it depends
// on the user's access and the conversation's saved targets. It is a variable
// so tests can replace the database-backed lookup.
var renderExecutionTargetsBlock = func(ctx *security.RequestContext, request NBAgentRequest, agentTools []toolcore.NBTool) string {
	names := make([]string, 0, len(agentTools))
	for _, t := range agentTools {
		names = append(names, t.Name())
	}
	block := tools.RenderExecutionTargets(ctx, request.AccountId, request.QueryConfig.ToolConfigs, names)
	return strings.TrimSpace(block + "\n" + renderDelegatedTargetBlock(request.QueryConfig.DelegatedTarget))
}

func renderDelegatedTargetBlock(target *toolcore.ExecutionTarget) string {
	if target == nil {
		return ""
	}
	encoded, _ := json.Marshal(struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
	}{Provider: target.Provider, Name: target.Name})
	return "<delegated_target>\n" + string(encoded) + "\n" + target.ScopeNote() + " Matching remote tools apply it by default; an explicit per-call target takes precedence. It does not grant access; tools validate the selected account before use. Tools that cannot take this target are refused; if one was needed, say what was not checked on it.\n</delegated_target>"
}
