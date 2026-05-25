package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const HelmAgentName = "helm"

func init() {
	// This describes the 'helm' agent when it is used as a tool by another agent (e.g., k8s_debug).
	toolDescription := `Manages Kubernetes applications using Helm. Can list releases, install, upgrade, or uninstall charts based on natural language requests.`
	toolInput := "Provide question in natural language to interact with kubernetes cluster using helm"
	toolOutput := "The tool will return the output of the question"

	core.RegisterNBAgentFactoryAndTool(HelmAgentName, func(accountId string) (core.NBAgent, error) {
		return newHelmAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newHelmAgent(accountId string) HelmAgent {
	return HelmAgent{
		accountId: accountId,
	}
}

type HelmAgent struct {
	accountId string
}

func (l HelmAgent) GetName() string {
	return HelmAgentName
}

func (a HelmAgent) GetNameAliases() []string {
	return []string{"Helm"}
}

func (l HelmAgent) GetDescription() string {
	return `Interact with Kubernetes clusters using Helm commands. User can ask questions in natural language and get the output of the question.`
}

func (l HelmAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{tools.HelmExecuteTool{}}
	return tools
}

func (l HelmAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Kubernetes Interaction:** Always use `helm` to interact with the cluster. Avoid other shell commands unless essential for formatting or context and explicitly requested by the user.",
		"**Namespace Awareness:** Never assume a namespace. Always specify it using `-n <namespace>` or use `--all-namespaces` for cluster-wide queries. If the user doesn't provide a namespace, ask for clarification.",
		"**Large Data Optimization:** When dealing with logs or describe commands, pipe output to `grep`, `tail`, `head`, or other formatting tools for readability: `kubectl logs <pod_name> -n <namespace> | grep -i <keyword>` or `kubectl describe pod <pod_name> -n <namespace> | grep -i <keyword>`.",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	constraints := []string{
		"You can ONLY use helm to interact with the cluster",
		"Always specify namespace",
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteHelmCommand: {
			"Use this tool to execute helm command.",
			"Input: valid helm command",
			"Output: the data returned by the helm command.",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteHelmCommand] = []string{
			"Executes Helm commands (e.g., `helm list`, `helm history`, `helm status`).",
			"Input: A valid Helm command.",
			"Output: Data returned by the Helm CLI.",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary to process the helm output.",
		}
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "list all releases",
			Answer:      "helm list --all",
			Explanation: "Gets all helm releases",
		},
		{
			Question:    "list all releases in default namespace",
			Answer:      "helm list -n default",
			Explanation: "Helm releases in default namespace",
		},
	}

	return core.NBAgentPrompt{
		Role:         "a knowledgeable and concise Kubernetes expert, acting as an SRE",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

func (l HelmAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
