package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
)

const ToolExecuteArgoCDCommand = "argocd_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteArgoCDCommand, func(accountId string) (core.NBTool, error) {
		return ArgoCDExecuteTool{}, nil
	})
	// Phase 3f (#32503): the retired ArgoCDAgent used the short handle "argocd".
	// Preserve resolvability for stored delegate_agent(tools=["argocd"]) calls.
	core.RegisterNBToolAlias("argocd", ToolExecuteArgoCDCommand)
}

// ToolPrompt implements core.NBToolPromptProvider. Delegate-context-only —
// fires when a sub-agent reaches this tool via delegate_agent(tools=[...]),
// where no argocd-specific orchestrator prompt is loaded (there isn't one).
// Slim: safety-critical + argocd command patterns + cross-tool hints so the
// caller knows to reach for kubectl_execute / github when the argocd trail
// runs out. The retired agent's full 5-step protocol + 8 remediation
// examples are NOT ported — zero agent traffic on dev in 30d means the
// methodology wasn't earning its weight. Add it back with evidence if
// argocd volume ever justifies.
func (m ArgoCDExecuteTool) ToolPrompt() []string {
	return []string{
		"**Evidence-based:** Run `argocd` command → parse output → make statement. NEVER invent app names, sync states, or revision hashes — empty results mean 'not found'.",
		"**Investigation is read-only by default.** Mutating verbs (`app sync`, `app rollback`, `app pause`, `app resume`, `app patch`, `app delete`, `repo add/remove`, `proj create/delete`) require user approval — do not run unsolicited.",
		"**No credentials in commands:** ArgoCD server URL, username, password, and auth token are injected via workspace env. Do NOT hardcode them; do NOT run `argocd login`.",
		"**Argocd is GitOps-shaped:** the source of truth is the Git repo, not the cluster. A durable fix belongs at the manifest layer (edit-in-Git + sync), NOT at the ArgoCD/K8s runtime. Direct `kubectl edit`/`patch` on argocd-managed resources reverts on next reconcile — flag that to the user.",
		"**Sync-status one-liner:** `Failed` → check git repo access + manifest syntax (`app get --show-operation`, look at ArgoCD controller logs). `Synced but Degraded` → check K8s runtime (use `kubectl_execute` for pods/events/logs on the app's namespace). `OutOfSync` → check config drift (`app diff`, then investigate what modified the live resource — `kubectl get <resource> -o yaml` vs Git). `Progressing` → poll `app get`, don't conclude yet.",
		"**Command families:** listing (`app list`, `proj list`, `cluster list`, `repo list`); inspection (`app get [--show-operation]`, `app diff`, `app history`, `app manifests`); mutation (`app sync`, `app rollback --revision <rev>`, `app pause`, `app resume`, `app patch`); recovery (`app wait --health --timeout <s>` for post-mutation verification).",
		"**Cross-tool hints (not preloaded — discover if needed):** For K8s state behind a Degraded app, use `kubectl_execute` (already in your toolset if you're an orchestrator). For git commit history behind a SyncFailed / OutOfSync, discover `github` via `search_tools` and delegate. Don't try to derive git state from argocd output alone — the argocd CLI shows sync results, not commit history.",
	}
}

type ArgoCDExecuteTool struct {
}

func (m ArgoCDExecuteTool) Name() string {
	return ToolExecuteArgoCDCommand
}

func (m ArgoCDExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ArgoCDExecuteTool) Description() string {
	return `Executes 'argocd' commands against the user's ArgoCD installation. This tool allows you to gather information about applications, sync status, and troubleshoot GitOps deployments.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about ArgoCD applications, sync status, or deployment issues, use this tool. 
		* **Input:** Provide a valid 'argocd' command as input.
		* **Output:** The tool will return the output of the executed command.

		**Examples:**

		* 'argocd app list'
		* 'argocd app get <app-name>'
		* 'argocd app sync <app-name>'
		* 'argocd app diff <app-name>'
		* 'argocd app history <app-name>'
		* 'argocd app logs <app-name>'
		* 'argocd app wait <app-name>'
		* 'argocd proj list'
		* 'argocd cluster list'
		* 'argocd repo list'

		**Important Notes:**

		* Ensure the 'argocd' command is correctly formatted.
		* Use the output of this tool to inform your responses and suggestions to the user.
		* This tool is specialized for ArgoCD GitOps operations and application lifecycle management.
		`
}

func (m ArgoCDExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "ArgoCD command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m ArgoCDExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("argocd: executing executeShellCommand tool call", "query", input.Command)

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	command := strings.TrimSpace(input.Command)

	wm := workspace.NewWorkspaceManager()
	env := map[string]string{
		workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
	}

	// Note: The workspace pod will have these keys available via k8s-secret if configured correctly in relay action.
	// However, for direct workspace execution, we expect the workspace to have its own way to resolve secrets
	// or we pass them if we have them. Since we don't have the values here (they are in K8s secrets),
	// we rely on the fact that 'argocd' in the workspace pod will be a shim that calls back to llm-server.
	// BUT if we want full shell access (pipes), the shim will handle the argocd command, and the shell will handle pipes.

	response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, env)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("argocd: unable to execute shell script", "error", err.Error(), "command", command)
		if response == "" {
			response = err.Error()
		}
		return core.NBToolResponse{
			Data:   cliRecoveryEnvelope(response, "", "argocd", "argocd <command> --help"),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	outputformat := map[string]string{
		"stdout": response,
	}
	outputformatBytes, err := common.MarshalJson(outputformat)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("argocd: unable to marshal response", "error", err.Error())
		return core.NBToolResponse{
			Data:   response,
			Status: core.NBToolResponseStatusError,
		}, err
	}
	response = string(outputformatBytes)

	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m ArgoCDExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"k8s_secret", "server"},
		ConfigType:   "argocd",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "ArgoCD Secret in k8s. Required Keys: ARGOCD_SERVER and ARGOCD_AUTH_TOKEN",
			},
			"server": {
				Type:        core.ToolSchemaTypeString,
				Description: "ArgoCD Server URL (e.g., https://argocd.example.com)",
			},
			"server_key_in_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name for server URL in the secret (defaults to ARGOCD_SERVER)",
				Default:     "ARGOCD_SERVER",
			},
			"auth_token_key_in_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name for auth token in the secret (defaults to ARGOCD_AUTH_TOKEN)",
				Default:     "ARGOCD_AUTH_TOKEN",
			},
			"timeout": {
				Type:        core.ToolSchemaTypeString,
				Description: "Command timeout in seconds (defaults to 30)",
				Default:     "30",
			},
			"insecure": {
				Type:        core.ToolSchemaTypeString,
				Description: "Skip TLS certificate verification (true/false, defaults to false)",
				Default:     "true",
			},
			"config_file_path": {
				Type:        core.ToolSchemaTypeString,
				Description: "Path to ArgoCD CLI config file (optional)",
				Default:     "",
			},
			"grpc_web": {
				Type:        core.ToolSchemaTypeString,
				Description: "Use gRPC-Web protocol (true/false, defaults to false)",
				Default:     "false",
			},
		},
	}
}

func (m ArgoCDExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are an ArgoCD security expert. Your task is to classify an 'argocd' command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: argocd app list
	answer: read

	input: argocd app get my-app
	answer: read

	input: argocd app history my-app
	answer: read

	input: argocd repo list
	answer: read

	input: argocd app create new-app --repo https://github.com/user/repo.git --path guestbook --dest-server https://kubernetes.default.svc --dest-namespace default
	answer: create

	input: argocd proj create new-project
	answer: create

	input: argocd repo add https://github.com/user/repo.git
	answer: create

	input: argocd app sync my-app
	answer: update

	input: argocd app set my-app --sync-policy automated
	answer: update

	input: argocd app unset my-app --sync-policy
	answer: update

	input: argocd app patch my-app --patch '{"metadata":{"annotations":{"new-key":"new-value"}}}' --type merge
	answer: update

	input: argocd app delete my-app
	answer: delete

	input: argocd cluster rm my-cluster
	answer: delete

	input: argocd app terminate-op my-app
	answer: delete
	`
	return prompt, nil
}
