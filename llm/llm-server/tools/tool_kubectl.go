package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"

	"github.com/pkg/errors"
)

const ToolExecuteKubectlCommand = "kubectl_execute"

// kubectlStderrNoisePrefixes are kubectl stderr lines that the workspace pod's
// /execute handler merges into stdout (because cmd.Stdout and cmd.Stderr point at
// the same buffer). They are informational notices, not actual command output, and
// must be stripped before the result is shown to the agent — otherwise the LLM
// interprets them as the only output and concludes the command failed.
var kubectlStderrNoisePrefixes = []string{
	`Defaulted container "`, // multi-container pod, no -c flag
	"Warning: ",             // deprecation / version warnings
	"W0",                    // klog warning lines (e.g. W0406 ...)
	"I0",                    // klog info lines (e.g. I0406 ...)
	"E0",                    // klog error lines (e.g. E0406 ...)
	"Flag --",               // deprecated flag notices
	"Unable to use a TTY",   // exec without TTY notice
}

// stripKubectlStderrNoise removes leading kubectl stderr notice lines from a
// merged stdout/stderr blob. It only removes a *prefix* of consecutive noise lines
// so that real output below them is preserved untouched. If the entire response
// turns out to be noise (i.e. the actual stdout was empty), the result is the
// empty string — which the LLM should interpret as "command produced no output",
// the correct semantics for e.g. `grep ERROR` finding no matches.
func stripKubectlStderrNoise(response string) string {
	if response == "" {
		return response
	}
	lines := strings.Split(response, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		matched := false
		for _, prefix := range kubectlStderrNoisePrefixes {
			if strings.HasPrefix(line, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			break
		}
		i++
	}
	if i == 0 {
		return response
	}
	return strings.Join(lines[i:], "\n")
}

func init() {
	core.RegisterNBToolFactory(ToolExecuteKubectlCommand, func(accountId string) (core.NBTool, error) {
		return KubectlExecuteTool{}, nil
	})
}

type KubectlExecuteTool struct {
}

func (m KubectlExecuteTool) Name() string {
	return ToolExecuteKubectlCommand
}

func (m KubectlExecuteTool) GetNameAliases() []string {
	return []string{"kubectl"}
}

func (m KubectlExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m KubectlExecuteTool) Description() string {
	return `Executes 'kubectl' commands against the user's Kubernetes cluster. This tool allows you to gather information about the cluster's resources and configuration, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about the user's cluster to make decisions or provide accurate responses, use this tool.
		* **Input:** Provide a valid, 'kubectl' command as input. Shell piping (|) is supported — you can pipe kubectl output through grep, head, tail, awk, etc.
		* **Output:** The tool will return the output of the executed command.

		**Examples:**

		* 'kubectl get pods'
		* 'kubectl describe node <node-name>'
		* 'kubectl get events --sort-by=.metadata.creationTimestamp'
		* 'kubectl get pods -A -o "custom-columns=NAME:.metadata.name,NAMESPACE:.metadata.namespace"'

		**Log Commands — IMPORTANT:**

		When fetching logs, ALWAYS use --tail or --since to limit output. Unfiltered logs can return hundreds of thousands of lines and overwhelm the response.
		Combine --tail with grep/head/tail pipes to get focused, relevant output.

		* 'kubectl logs <pod> -n <namespace> --tail 200' — limit to recent lines
		* 'kubectl logs <pod> -n <namespace> --tail 500 | grep -i -E "(error|exception|fatal|panic|fail|warn)"' — filter for errors
		* 'kubectl logs <pod> -n <namespace> --since=1h | grep -i error' — recent logs with error filter
		* 'kubectl logs <pod> -n <namespace> --tail 1000 | grep -i -E "(error|exception)" | head -50' — cap filtered output
		* 'kubectl logs <pod> -n <namespace> --tail 500 | grep -i -B2 -A2 error' — errors with surrounding context
		* 'kubectl logs <pod> -n <namespace> --tail 500 | awk "/error|exception/,/^$/"' — extract error blocks
		* 'kubectl logs <pod> -n <namespace> -p --tail 200' — previous container logs (crash loops)
		* 'kubectl logs <pod> -n <namespace> --tail 500 | tail -100' — last 100 lines of recent 500

		**Important Notes:**

		* Ensure the 'kubectl' command is correctly formatted.
		* Whenever possible, provide namespace
		* **Quoting Arguments:** Always wrap complex arguments, especially those with special characters (e.g., '-o custom-columns=...', '-o jsonpath=...', '-l', '--field-selector', '[', '(', '?', '@', '*'), in double or single quotes to ensure correct execution in the shell.
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m KubectlExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Kubectl command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m KubectlExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	nbRequestContext.Ctx.GetLogger().Info("k8s: executing executeShellCommand tool call", "query", input.Command)
	command := strings.TrimSpace(input.Command)
	if !strings.HasPrefix(command, "kubectl") {
		command = "kubectl " + command
	}

	command1 := strings.ToLower(command)
	if strings.Contains(command1, " secret") || strings.Contains(command1, " secrets") {
		return core.NBToolResponse{}, errors.New("kubectl: access to secrets is blocked")
	}

	// Safety net: auto-inject --tail for kubectl logs if not already limited
	if strings.Contains(command1, " logs ") || strings.HasPrefix(command1, "kubectl logs") {
		if !strings.Contains(command, "--tail") && !strings.Contains(command, "--since") && !strings.Contains(command, " -f") {
			// Insert --tail before any pipe to avoid breaking piped commands
			if pipeIdx := strings.Index(command, "|"); pipeIdx > 0 {
				command = strings.TrimRight(command[:pipeIdx], " ") + " --tail 500 | " + strings.TrimLeft(command[pipeIdx+1:], " ")
			} else {
				command += " --tail 500"
			}
		}
	}

	// we want to ignor eany context related information as system always uses default context
	if strings.Contains(command, "--context") {
		parts := strings.Fields(command)
		for i, p := range parts {
			if p == "--context" {
				// Ensure there is an argument after --context before slicing
				if i+1 < len(parts) {
					parts = append(parts[:i], parts[i+2:]...)
				} else {
					// --context is the last argument, just remove it
					parts = parts[:i]
				}
				command = strings.Join(parts, " ")
				break
			}
		}
	}

	// Extract accountId from tool config (selected cluster account)
	configAccountId := ""
	for _, v := range nbRequestContext.ToolConfig.Values {
		if v.Name == "id" {
			configAccountId = v.Value
			break
		}
	}

	// Use config-selected account if available, otherwise fall back to request account
	effectiveAccountId := nbRequestContext.AccountId
	if configAccountId != "" {
		effectiveAccountId = configAccountId
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, effectiveAccountId, nbRequestContext.ConversationId, command, map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("k8s: unable to execute shell script", "error", err.Error(), "command", command)
			if response == "" {
				response = err.Error()
			}
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}

		// Strip kubectl stderr noise that the workspace pod merges into stdout.
		// The workspace /execute handler points cmd.Stdout and cmd.Stderr at the same
		// buffer, so kubectl notices like `Defaulted container "x" out of: ...` and
		// `Warning: ...` (deprecation/version notices) end up labeled as stdout. The
		// LLM then interprets this as "tool produced no real output" and gives up,
		// which is wrong — especially for piped commands like
		// `kubectl logs ... | grep ERROR | tail -10` where an empty grep result is the
		// correct answer ("no errors found").
		response = stripKubectlStderrNoise(response)

		// Wrap in JSON to be consistent with non-workspace mode and allow agents to parse it
		outputformat := map[string]string{
			"stdout": response,
		}
		outputformatBytes, err := common.MarshalJson(outputformat)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("kubectl: unable to marshal response", "error", err.Error())
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}
		response = string(outputformatBytes)

		return core.NBToolResponse{
			Data:       response,
			Type:       core.NBToolResponseTypeText,
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"kubernetes", "applications"}, "Check Apps & Pods", nil, "")},
		}, nil
	}

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobKubectl, command, effectiveAccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("k8s: unable to execute shell script", "error", err.Error(), "command", command)
		responseData := ""
		if response != nil {
			if responseData1, ok := response.(string); ok {
				responseData = responseData1
			}
		}
		return core.NBToolResponse{
			Data:   responseData,
			Status: core.NBToolResponseStatusError,
		}, err
	}

	data := response.(string)
	resp := core.NBToolResponse{
		Data:       data,
		Type:       core.NBToolResponseTypeText,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"kubernetes", "applications"}, "Check Apps & Pods", nil, "")},
	}

	return resp, nil
}

func (m KubectlExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	// 1. Try to match via context labels (e.g. nb_cloud_account_id from UI context)
	if ctx.QueryConfig.Labels != nil {
		var cloudAccountID string
		if val, ok := ctx.QueryConfig.Labels["nb_cloud_account_id"].(string); ok {
			cloudAccountID = val
		}

		if cloudAccountID != "" {
			for _, cfg := range availableConfigs {
				for _, v := range cfg.Values {
					if v.Name == "account_number" && v.Value == cloudAccountID {
						return cfg, nil
					}
					if v.Name == "id" && v.Value == cloudAccountID {
						return cfg, nil
					}
				}
			}
		}
	}

	// 2. If project/cluster ID is mentioned in the command, try to find matching config
	command := strings.ToLower(input.Command)
	var matches []core.ToolConfig

	for _, cfg := range availableConfigs {
		matched := false
		// Cluster ID is often used in names
		if cfg.Name != "" && len(cfg.Name) >= 3 && strings.Contains(command, strings.ToLower(cfg.Name)) {
			matched = true
		}

		if !matched {
			// Check values for id, cluster_id, etc.
			for _, v := range cfg.Values {
				lowName := strings.ToLower(v.Name)
				if (lowName == "id" || lowName == "cluster_id" || lowName == "cluster_name" ||
					lowName == "account_id" || lowName == "account_number" || lowName == "name") && len(v.Value) >= 3 {
					if strings.Contains(command, strings.ToLower(v.Value)) {
						matched = true
						break
					}
				}
			}
		}

		if matched {
			matches = append(matches, cfg)
		}
	}

	// Ambiguous: multiple configs matched — let the next strategy decide
	if len(matches) != 1 {
		return core.ToolConfig{}, nil
	}
	return matches[0], nil
}

func (m KubectlExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a Kubernetes security expert. Your task is to classify an input string as a 'kubectl' command type.

	The input might be a full 'kubectl' command or a JSONPath expression used for filtering output.

	Based on the input, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	command - kubectl run pod1 --image=ubuntu --restart=Never -- sleep 3600
	answer - write
	reason - command is creating pod

	command - kubectl get pods --all-namespaces
	answer - read
	reason - command is reading pod details

	command - kubectl apply -f pod.yaml
	answer - write
	reason - command is updating resources in cluster

	command - kubectl describe pod pod1
	answer - read
	reason - command is reading pod details

	command - kubectl scale deployment my-deployment --replicas=3
	answer - write
	reason - command is updating resources in cluster

	command - kubectl edit deployment my-deployment
	answer - write
	reason - command is updating resources in cluster

	command - kubectl get nodes
	answer - read
	reason - command is reading node details

	command - kubectl top pods
	answer - read
	reason - command is reading pod resource usage details

	command - kubectl config view
	answer - read
	reason - command is reading kubeconfig details

	command - kubectl cordon node1
	answer - write
	reason - command is updating node details

	`
	return prompt, nil
}

func (m KubectlExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"account_name", "account_number"},
		ConfigType:   "k8s",
		ConfigSource: core.ToolConfigSourceAccount,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}
