package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

const KubectlAgentName = "kubectl"

func init() {
	// This describes the 'kubectl' agent when it is used as a tool by another agent.
	toolDescription := `Self-directed Kubernetes investigation agent. Describe the problem you want solved (e.g. "find why pod X in namespace prod is crash-looping", "list all deployments with unhealthy replicas across namespaces", "scale deployment Y to 3 replicas") and the agent will plan and run the necessary kubectl commands itself, including resource discovery, namespace inference, multi-step lookups, and error recovery. DO NOT use this for log retrieval if the "logs" agent is available. DO NOT use this for initial resource discovery if "resource_search" is available.`
	toolInput := "A natural-language description of the Kubernetes problem to investigate or the action to perform. You may also pass an exact kubectl command if you already know it, but prefer describing the goal so the agent can adapt and recover from errors."
	toolOutput := "The agent's findings or the result of the executed action(s), including relevant kubectl output."

	core.RegisterNBAgentFactoryAndTool(KubectlAgentName, func(accountId string) (core.NBAgent, error) {
		return newKubectlAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newKubectlAgent(accountId string) KubectlAgent {
	return KubectlAgent{
		accountId: accountId,
	}
}

type KubectlAgent struct {
	accountId string
}

func (l KubectlAgent) GetName() string {
	return KubectlAgentName
}

func (l KubectlAgent) GetNameAliases() []string {
	return []string{"Kubectl", "K8s", "Kubernetes"}
}

func (l KubectlAgent) GetDescription() string {
	return `Self-directed Kubernetes investigation agent. Describe the problem you want solved (e.g. "find why pod X in namespace prod is crash-looping", "list all deployments with unhealthy replicas across namespaces", "scale deployment Y to 3 replicas") and the agent will plan and run the necessary kubectl commands itself, including resource discovery, namespace inference, multi-step lookups, and error recovery. DO NOT use this for log retrieval if the "logs" agent is available. DO NOT use this for initial resource discovery if "resource_search" is available.`
}

func (l KubectlAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{tools.KubectlExecuteTool{}}

	// Add resource search tool
	if resourceSearchTool, ok := toolcore.GetNBTool(l.accountId, ResourceSearchAgentName); ok {
		toolsList = append(toolsList, resourceSearchTool)
	}

	return toolsList
}

func (l KubectlAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Kubernetes Interaction:** Always use `kubectl` to interact with the cluster. Avoid other shell commands unless essential for formatting or context and explicitly requested by the user.",
		"**Namespace Awareness:** Never assume a namespace. Prefer explicit `-n <namespace>`. Use the `<namespace>` placeholder only when RETURNING a template command; if the user wants to EXECUTE and the namespace is missing, resolve it (resource_search or one concise clarification) before running.",
		"**When namespace is missing:**",
		"  - For RETURN-COMMAND requests (what/which/how-to command): use TEMPLATE MODE and emit a single command with `-n <namespace>` placeholder.",
		"  - For EXECUTE requests (user asks to perform/scale/apply/cordon/uncordon/delete, etc.): do **not** execute with placeholders. First resolve the namespace via `resource_search` or ask **one** concise clarification; then execute.",
		"**Avoid Fetching Entire Cluster Details:** Never fetch entire cluster details at once (e.g., don't use `kubectl get all --all-namespaces -o json|yaml` without filters). Break requests into specific resource types, namespaces, and filters.",
		"**Use Resource-Specific Queries:** Prefer specific resource types (pods, services, deployments, etc.) over `all`.",
		"**Provide Final Answer Directly:** After successfully gathering data with other tools, provide your final answer directly to the user in Markdown format. For list/get queries, include the full data. For investigation queries, be concise and focus on key findings.",
		"**Always try resource_search when:**",
		"   - User mentions app/service names without exact k8s resource details",
		"   - User says 'deployment' (could be Deployment, StatefulSet, or DaemonSet)",
		"   - User says 'app' or 'workload' (search returns all workload types + pods)",
		"   - kubectl commands fail with 'not found' or 'no resources found'",
		"   - Resource names seem ambiguous or could have variations",
		"**Event Retrieval:** Prefer sorted recent events with filters (e.g., `kubectl get events --sort-by=.metadata.creationTimestamp -n <namespace> | tail -n 20`).",
		"**Large Data Optimization:** For logs/describe, use grep/tail/head for readability (`kubectl logs <pod-name> -n <namespace> | grep <keyword>`).",
		"**Quoting Arguments:** Always wrap complex arguments, especially those with special characters (e.g., `-o custom-columns=...`, `-o jsonpath=...`, `-l`, `--field-selector`, `[`, `(`, `?`, `@`, `*`), in double or single quotes to ensure correct execution in the shell. Example: `kubectl get pods -A -o 'custom-columns=NAME:.metadata.name,NAMESPACE:.metadata.namespace'`",
		"**Large Data Protection:** AVOID using `-o json` or `-o yaml` with `kubectl get <resource> -A` or `--all-namespaces` as it generates massive output. Prefer default output, `-o wide`, or custom columns for broad checks. For direct list/get queries (e.g. 'get pods', 'list nodes'), return the actual output formatted as a Markdown table. For investigation queries, summarize key findings rather than dumping raw output.",
		"**Field Selectors:** Use selectors for efficient filtering (e.g., `kubectl get pods --field-selector=status.phase=Running -n <namespace>`).",
		"**Context and Describe:** Use `kubectl describe` for detailed info and `kubectl get <resource> -o yaml` for configuration.",
		"**Prefer safe reads first:** If the intent is unclear, prefer `get`, `describe`, or `logs` over mutating commands.",
		"**Short-hands/aliases:** Normalize short forms (po→pods, svc→services, deploy→deployments, ing→ingresses, sts→statefulsets, ds→daemonsets, cm→configmaps, sa→serviceaccounts, pvc→persistentvolumeclaims).",

		"**EXECUTION MODE (DEFAULT):** If the user asks to perform an action or get information (e.g., 'get pods', 'describe service', 'check logs'), YOU MUST EXECUTE the command using the `kubectl_execute` tool. Then provide the final answer based on the output.",
		"**COMMAND-ONLY MODE:** ONLY if the user explicitly asks for 'the command', 'query', 'syntax', or 'how do I...', then your ONLY output should be the `kubectl` command string. Do not execute it.",
		"**TEMPLATE MODE (Default for Missing Info):** If required details (like name or namespace) are missing for an execution request, try to resolve them with `resource_search` or one clarification question. If the user only asked for the command (Command-Only Mode), then return the command with placeholders.",

		"**DISAMBIGUATION:**",
		"- If the query mentions 'health', 'healthcheck', or 'component health', output `kubectl get componentstatuses`.",
		"- Use `kubectl cluster-info` only when the query explicitly asks for control-plane endpoints/addresses or 'cluster info'.",
		"- Rollout progress of an app → `kubectl rollout status deployment <deployment-name> -n <namespace>`.",
		"- Revision history of an app → `kubectl rollout history deployment <deployment-name> -n <namespace>`.",
		"- Node maintenance: stop scheduling → `kubectl cordon <node-name>`; resume scheduling → `kubectl uncordon <node-name>`.",
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, core.GetWorkspaceInstructions()...)
	}

	constraints := []string{
		"**CRITICAL WORKFLOW:** Your first step MUST be to call the `kubectl_execute` tool. Only if that fails or if resource names are ambiguous should you use `resource_search`.",
		"Always specify namespace (or `<namespace>` placeholder in TEMPLATE MODE).",
		"Always use --no-headers with kubectl get commands when counting items (e.g., with '| wc -l') or when the output is intended for programmatic parsing, to avoid including header lines",
		"NEVER fetch entire cluster details at once without filters - this leads to timeouts and truncated responses",
		"For mutating actions (delete, drain, cordon, uncordon, scale, rollout restart, apply): if authorization allows, EXECUTE the command; do not execute commands with placeholders—resolve identifiers first (resource_search or one concise clarification).",
		"For counting or listing names, prefer `-o name` or `--no-headers` to reduce noise.",
		"For logs: if logs appear empty, consider `--previous` or specifying `-c <container>` if the pod has multiple containers.",
		"Always break complex queries into multiple targeted steps with specific resource types and filters",
		"NEVER use `kubectl get all --all-namespaces -o json` or `kubectl get all --all-namespaces -o yaml` without filters, as it can lead to timeouts or truncated responses",
		"NEVER use writing kubectl output to a file as we do not have access to the file system",
		"NEVER user '-o json' or '-o yaml' options with kubectl commands without filters, as it can lead to timeouts or truncated responses",
		"Use specific resource types (pods, services, deployments) instead of 'all' when possible",
		"**IMPORTANT: Use resource_search tool proactively** - Don't wait for kubectl to fail first",
		"**When in doubt about resource names:** Always use resource_search tool to find exact matches",
		"**Never guess resource names:** Use resource_search tool to get accurate suggestions",
		"Do not just return 'command' or generic informations, unless or until user explicitly asks for it",
		"In templates, use canonical resource kinds (pods, services, deployments, statefulsets, daemonsets, ingresses); avoid shorthands like po/svc/deploy.",
	}

	if config.Config.LlmServerShellToolEnabled {
		newConstraints := []string{}
		for _, c := range constraints {
			if !strings.Contains(c, "we do not have access to the file system") {
				newConstraints = append(newConstraints, c)
			}
		}
		constraints = newConstraints
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteKubectlCommand: {
			"Use this tool FIRST to execute a kubectl command to answer the user's question.",
			"Input: valid kubectl command",
			"Output: the data returned by the kubectl command.",
		},
		ResourceSearchAgentName: {
			"Use this tool for fuzzy resource matching and generating search strategies when resources are not found.",
			"Input: search query in natural language",
			"Output: resource suggestions and search strategies",
			"Examples: Can you search pods maching `pod1`",
			"Examples: Get me pods for app `nginx` in namespace `ingress`",
		},
	}

	if config.Config.LlmServerShellToolEnabled {
		toolUsage[tools.ToolExecuteKubectlCommand] = []string{
			"Use this tool FIRST to execute a kubectl command to answer the user's question.",
			"Input: valid kubectl command",
			"Output: the data returned by the kubectl command.",
			"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) if necessary.",
		}
	}
	examples := []core.NBAgentPromptExample{
		{
			Question: "shows control-plane endpoints/addresses?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteKubectlCommand,
					Input: "kubectl cluster-info",
				},
			},
			Explanation: "Cluster-info is for endpoints/addresses, not health.",
		},
		{
			Question: "Show me the revision history for my 'api-gateway' deployment",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteKubectlCommand,
					Input: "kubectl rollout history deployment api-gateway -n <namespace>",
				},
			},
		},
		{
			Question:    "Which command lists all nodes?",
			Answer:      "kubectl get nodes",
			Explanation: "Command-only output.",
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

func (l KubectlAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l KubectlAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

func (l KubectlAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolExecuteKubectlCommand) {
		resultsMap := map[string]any{}
		err := common.UnmarshalJson([]byte(toolResponse), &resultsMap)
		if err != nil {
			return toolResponse
		}

		stdout := ""
		stderr := ""
		if v, isOk := resultsMap["stdout"].(string); isOk {
			stdout = v
		}
		if v, isOk := resultsMap["stderr"].(string); isOk {
			stderr = v
		}

		// Handle kubectl logs specifically
		if strings.Contains(toolRequest.ToolInput, "kubectl logs") {
			logs := tools.GetErrorLinesFromLogStringOrDefault(stdout+stderr, true)
			return strings.Join(logs, "\n")
		}

	}
	return toolResponse
}
