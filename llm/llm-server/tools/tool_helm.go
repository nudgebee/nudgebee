package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
)

const ToolExecuteHelmCommand = "helm_execute"

// InferHelmRequestType is shared by the tool and workspace proxy so target
// authorization uses the same command classification on both sides.
func InferHelmRequestType(command string) core.ToolRequestType { return inferHelmRequestType(command) }

func init() {
	core.RegisterNBToolFactory(ToolExecuteHelmCommand, func(accountId string) (core.NBTool, error) {
		return HelmExecuteTool{}, nil
	})
}

type HelmExecuteTool struct {
}

func (m HelmExecuteTool) ConfirmationKey(toolInput string) string {
	req := core.ParseNBToolCallRequestFromInput(toolInput)
	requested, err := RequestedHelmCluster(req.Command, req.Arguments)
	if err != nil {
		return m.Name() + "@" + toolInput
	}
	return targetConfirmationKey(m.Name(), requested)
}

func (m HelmExecuteTool) PrepareDelegatedTargetInput(target *core.ExecutionTarget, input string) (string, error) {
	if target == nil || target.Provider != "kubernetes" {
		return input, nil
	}
	req := core.ParseNBToolCallRequestFromInput(input)
	requested, err := RequestedHelmCluster(req.Command, req.Arguments)
	if err != nil || requested != "" {
		return input, err
	}
	return addDelegatedTargetArgument(input, "cluster", target.Name)
}

func (m HelmExecuteTool) RequestedTarget(toolInput string) (string, error) {
	req := core.ParseNBToolCallRequestFromInput(toolInput)
	return RequestedHelmCluster(req.Command, req.Arguments)
}

func RequestedHelmCluster(command string, args map[string]any) (string, error) {
	return requestedTarget(command, args, "cluster", "--kube-context", "cluster")
}

func (m HelmExecuteTool) Name() string {
	return ToolExecuteHelmCommand
}

func (m HelmExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

// ShellCommandPrefixes returns the shell command prefixes that map to this
// tool. Implements core.ShellWrappable so shell_execute's classifier can
// delegate confirmation-gate decisions to this tool when the LLM invokes
// the CLI via `shell_execute("helm ...")` instead of `helm_execute`.
func (m HelmExecuteTool) ShellCommandPrefixes() []string { return []string{"helm"} }

func (m HelmExecuteTool) Description() string {
	return `Executes 'helm' commands against a connected Kubernetes cluster. This tool allows you to gather information about the cluster's resources and configuration, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about the user's cluster to make decisions or provide accurate responses, use this tool. 
		* **Input:** Provide a valid, 'helm' command as input.
		* **Output:** The tool will return the output of the executed command.
		* **Cluster:** Pass the connected cluster name in 'cluster' to target another authorized cluster. Results from another cluster identify that cluster.

		**Examples:**

		* 'helm ls'

		**Important Notes:**

		* Ensure the 'helm' command is correctly formatted.
		* Whenever possible, provide namespace
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

// ToolPrompt implements core.NBToolPromptProvider — the how-to guidance a delegate
// sub-agent needs when this tool is named directly (post-3c, no wrapping helm agent
// to flatten). Mirrors what HelmAgent.GetSystemPrompt() carries today; kept as flat
// lines so it slots into the existing "Guidelines for the specialist tools above"
// section that delegate_agent already emits from flattenAgentGuidance.
func (m HelmExecuteTool) ToolPrompt() []string {
	return []string{
		"**Kubernetes Interaction:** Always use `helm` to interact with the cluster. Avoid other shell commands unless essential for formatting or context and explicitly requested by the user.",
		"**Cluster selection:** If the task names another connected cluster, pass its exact name in the `cluster` argument on each `helm_execute` call. `helm --kube-context <name>` also selects that cluster. Never infer a cluster from a release name.",
		"**Namespace Awareness:** Never assume a namespace. Always specify it using `-n <namespace>` or use `--all-namespaces` for cluster-wide queries. If the user doesn't provide a namespace, ask for clarification.",
		"**Large Data Optimization:** When output is voluminous, pipe through `grep`, `tail`, `head`, or other formatting tools for readability.",
		"You can ONLY use helm to interact with the cluster.",
		"Executes Helm commands (e.g., `helm list`, `helm history`, `helm status`). Input: a valid Helm command. Output: data returned by the Helm CLI.",
		"You can use standard shell features like pipes (|), redirects (>), and command substitutions ($( )) to process the helm output.",
		"Example — list all releases: `helm list --all`",
		"Example — list all releases in default namespace: `helm list -n default`",
	}
}

func (m HelmExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Helm command to execute",
			},
			"cluster": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional connected Kubernetes cluster name. Omit to use the current cluster.",
			},
		},
		Required: []string{"command"},
	}
}

func (m HelmExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("helm: executing executeShellCommand tool call", "query", input.Command)
	command := strings.TrimSpace(input.Command)
	requested, err := RequestedHelmCluster(command, input.Arguments)
	if err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}
	target, err := ResolveKubernetesTarget(nbRequestContext, nbRequestContext.ToolConfig, requested)
	if err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}
	if err := VerifyDelegatedTargetIdentity(nbRequestContext, "kubernetes", target, requested); err != nil {
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}
	if target.Name == "" && requested == "" {
		// Helm historically falls back to the conversation's workspace account
		// when it has no saved config. Keep that default for existing callers.
		target = core.ToolConfig{Values: []core.ToolConfigValue{{Name: "id", Value: nbRequestContext.AccountId}}}
	}
	crossCluster := !strings.EqualFold(target.Name, nbRequestContext.ToolConfig.Name)
	// Account-agent configs are placeholders ("Agent") without a cluster ID.
	// An untargeted Helm call must use the workspace account, not send that
	// placeholder to the proxy as though it were a concrete cluster name.
	var env map[string]string
	if crossCluster {
		_, commandWithoutContext := scanValueFlags(command, "--kube-context")
		if inferHelmRequestType(commandWithoutContext) != core.ToolRequestTypeRead {
			if err := CheckShellTargetWriteAccess(nbRequestContext.Ctx, target); err != nil {
				return core.NBToolResponse{}, err
			}
		}
		env, err = KubernetesTargetEnv(nbRequestContext, target)
		if err != nil {
			return core.NBToolResponse{}, err
		}
	}

	wm := workspace.NewWorkspaceManager()
	response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, env)
	if err != nil {
		// Pipeline-tail no-match reclassification (issue #32240).
		// Same as kubectl_execute: `helm list -A | grep <name>` with
		// no match exits 1 and surfaces as an opaque failure. Reuse
		// the helpers from tool_shell.go (PR #32007).
		if isNoMatchExit(err, command) {
			nbRequestContext.Ctx.GetLogger().Info("helm: reclassified pipeline-tail no-match as success", "command", command)
			if crossCluster {
				return successResponseNoMatchesOnCluster(nbRequestContext, response, target.Name)
			}
			return successResponseNoMatches(nbRequestContext, response)
		}
		nbRequestContext.Ctx.GetLogger().Error("helm: unable to execute shell script", "error", err.Error(), "command", command)
		if response == "" {
			response = err.Error()
		}
		if crossCluster {
			response = fmt.Sprintf("[cluster: %s] %s", target.Name, response)
		}
		return core.NBToolResponse{
			Data:   cliRecoveryEnvelope(response, "", "helm", "helm <command> --help"),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	outputformat := map[string]string{
		"stdout": response,
	}
	if crossCluster {
		outputformat["cluster"] = target.Name
	}
	outputformatBytes, err := common.MarshalJson(outputformat)
	if err != nil {
		// Marshal failure is an internal serialization bug, not a CLI error —
		// leave as raw passthrough (no CLI help suggestion would apply).
		nbRequestContext.Ctx.GetLogger().Error("helm: unable to marshal response", "error", err.Error())
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

func (m HelmExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a Helm security expert. Your task is to classify a 'helm' command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: helm list
	answer: read

	input: helm get values my-release
	answer: read

	input: helm history my-release
	answer: read

	input: helm search hub wordpress
	answer: read

	input: helm lint my-chart
	answer: read

	input: helm install my-release my-chart
	answer: create

	input: helm repo add bitnami https://charts.bitnami.com/bitnami
	answer: create

	input: helm upgrade my-release my-chart
	answer: update

	input: helm rollback my-release 1
	answer: update

	input: helm repo update
	answer: update

	input: helm uninstall my-release
	answer: delete

	input: helm repo remove bitnami
	answer: delete
	`
	return prompt, nil
}

func (m HelmExecuteTool) InferToolRequestType(ctx *security.RequestContext, toolName, input string) (core.ToolRequestType, error) {
	requestType := inferHelmRequestType(input)
	if requestType == "" {
		ctx.GetLogger().Warn("helm: action not recognized by heuristic, falling through to LLM classification", "input", input)
	}
	return requestType, nil
}

func (m HelmExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{},
		ConfigSource: core.ToolConfigSourceAccountAgent,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}
