package core

import (
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
	return tools.RenderExecutionTargets(ctx, request.AccountId, request.QueryConfig.ToolConfigs, names)
}
