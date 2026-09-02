package aws

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// Phase 3d (#32503): agent registration removed. The aws agent was a thin
// wrapper around AwsCliTool; its dynamic prompt loader (`prompts.PromptAws`)
// had exactly one consumer (this file) so retirement removed the loader entry
// and the aws.yaml module too. The short handle `"aws"` continues to resolve
// to `aws_execute` via tool alias (see tool_cloud_aws.go init). Kept the type
// with the same NBAgent surface for one release so external callers (tests,
// stored-history bridges) still compile; delete after bake.
// `AwsAgentName` const is in constants.go.

// AwsAgent is deprecated (Phase 3d #32503). Runtime registration is gone;
// guidance lives on AwsCliTool now. Kept for compat.
type AwsAgent struct{}

func (a AwsAgent) GetName() string          { return AwsAgentName }
func (a AwsAgent) GetNameAliases() []string { return []string{"AWS"} }

func (a AwsAgent) GetDescription() string {
	return `Deprecated (Phase 3d #32503) — use ` + tools.ToolExecuteAwsCliCommand + ` directly.`
}

func (a AwsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{tools.AwsCliTool{}}
}

func (a AwsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}

func (a AwsAgent) GetPlannerType() core.AgentPlannerType { return core.AgentPlannerTypeReAct }
