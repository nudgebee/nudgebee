package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

const AgentVMOrchestratorName = "vm_orchestrator"

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentVMOrchestratorName, func(accountID string) (core.NBAgent, error) {
		return VMOrchestratorAgent{accountID: accountID}, nil
	}, "vm_debug")
}

type VMOrchestratorAgent struct{ accountID string }

func (a VMOrchestratorAgent) GetName() string { return AgentVMOrchestratorName }
func (a VMOrchestratorAgent) GetNameAliases() []string {
	return []string{"VM Troubleshooter", "VM Fleet"}
}
func (a VMOrchestratorAgent) GetDescription() string {
	return "Investigates an account-scoped fleet of virtual machines through verified inventory and target-bound SSH execution."
}
func (a VMOrchestratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}
func (a VMOrchestratorAgent) GetModelCategory() llmruntime.ModelTier {
	return llmruntime.ModelTierReasoning
}
func (a VMOrchestratorAgent) GetCacheScope() llmruntime.CacheScope {
	return llmruntime.CacheScopeAccount
}

func (a VMOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	allowed := map[string]bool{
		tools.ToolVMList:                 true,
		tools.ToolVMExecute:              true,
		toolcore.ToolExecuteShellCommand: true,
		FinOpsAgentName:                  true,
		DelegateAgentToolName:            true,
		SearchToolsToolName:              true,
		tools.SearchSkillsToolName:       true,
		NudgebeeAgentName:                true,
		AgentCodeAnalyzer:                true,
	}
	var out []toolcore.NBTool
	for _, tool := range toolcore.GetEnabledNBTools(ctx, a.accountID) {
		if allowed[tool.Name()] {
			out = append(out, tool)
		}
	}
	return out
}

func (a VMOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	content, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptVMLean, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("vm orchestrator: prompt load failed", "error", err)
		return core.NBAgentPrompt{}
	}
	return core.ParsePromptToNBAgentPrompt(content + linkedServicesBlock(ctx, query.AccountId))
}

// linkedServicesBlock tells the agent which repository and branch each service of the
// account's VMs runs, so it can pass them to code_analyzer. Empty when nothing is linked.
func linkedServicesBlock(ctx *security.RequestContext, accountId string) string {
	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("vm orchestrator: unable to read linked services", "error", err)
		return ""
	}
	links, err := services_server.ListVMServiceCodeLinks(dbm, accountId)
	if err != nil {
		ctx.GetLogger().Warn("vm orchestrator: unable to read linked services", "error", err)
		return ""
	}
	return formatLinkedServices(links)
}

func formatLinkedServices(links []services_server.VMServiceLink) string {
	if len(links) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n  Services with a linked repository (VM, service, repository, branch):\n")
	for _, l := range links {
		branch := l.Branch
		if branch == "" {
			branch = "default branch"
		}
		fmt.Fprintf(&b, "  - %s, %s, %s, %s\n", l.VM, l.Service, l.Repo, branch)
	}
	return b.String()
}
