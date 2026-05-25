package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
)

const ToolExecuteHelmCommand = "helm_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteHelmCommand, func(accountId string) (core.NBTool, error) {
		return HelmExecuteTool{}, nil
	})
}

type HelmExecuteTool struct {
}

func (m HelmExecuteTool) Name() string {
	return ToolExecuteHelmCommand
}

func (m HelmExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m HelmExecuteTool) Description() string {
	return `Executes 'helm' commands against the user's Kubernetes cluster. This tool allows you to gather information about the cluster's resources and configuration, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about the user's cluster to make decisions or provide accurate responses, use this tool. 
		* **Input:** Provide a valid, 'helm' command as input.
		* **Output:** The tool will return the output of the executed command.

		**Examples:**

		* 'helm ls'

		**Important Notes:**

		* Ensure the 'helm' command is correctly formatted.
		* Whenever possible, provide namespace
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m HelmExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Helm command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m HelmExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("helm: executing executeShellCommand tool call", "query", input.Command)
	command := strings.TrimSpace(input.Command)
	if !strings.HasPrefix(command, "helm") {
		command = "helm " + command
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("helm: unable to execute shell script", "error", err.Error(), "command", command)
			if response == "" {
				response = err.Error()
			}
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}

		// Wrap in JSON to be consistent with non-workspace mode
		outputformat := map[string]string{
			"stdout": response,
		}
		outputformatBytes, err := common.MarshalJson(outputformat)
		if err != nil {
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

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobHelm, command, nbRequestContext.AccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("helm: unable to execute shell script", "error", err.Error())
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
	return core.NBToolResponse{
		Data:   data,
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

func (m HelmExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{},
		ConfigSource: core.ToolConfigSourceAccountAgent,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}
