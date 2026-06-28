package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
)

const ToolExecuteTektonCommand = "tekton_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteTektonCommand, func(accountId string) (core.NBTool, error) {
		return TektonExecuteTool{}, nil
	})
}

type TektonExecuteTool struct {
}

func (m TektonExecuteTool) Name() string {
	return ToolExecuteTektonCommand
}

func (m TektonExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m TektonExecuteTool) Description() string {
	return `Executes 'tkn' (Tekton CLI) commands to investigate CI pipeline status, view build/test logs, and troubleshoot pipeline failures in Tekton Pipelines.

		**Usage:**

		* **Prioritize this tool:** Whenever you need information about Tekton CI pipelines, build status, test results, or pipeline failures, use this tool.
		* **Input:** Provide a valid 'tkn' command as input.
		* **Output:** The tool will return the output of the executed command.

		**Examples:**

		* 'tkn pipelinerun list' — list recent pipeline executions
		* 'tkn pipelinerun describe <name>' — detailed status of a pipeline run
		* 'tkn pipelinerun logs <name> -a' — ALL logs across all tasks and steps
		* 'tkn pipelinerun logs <name> --task <task-name>' — logs for a specific task
		* 'tkn taskrun list' — list task executions
		* 'tkn taskrun logs <name>' — logs for a specific task run
		* 'tkn taskrun describe <name>' — detailed status of a task run
		* 'tkn pipeline list' — list available pipelines
		* 'tkn task list' — list available tasks

		**Important Notes:**

		* A PipelineRun creates multiple TaskRun pods, each with multiple step containers. Use 'tkn pipelinerun logs <name> -a' to get complete logs across the entire hierarchy.
		* Use the '-a' flag for all logs, or '--task <task-name>' to narrow to a specific task.
		* This tool is specialized for Tekton CI pipeline operations and lifecycle management.
		`
}

func (m TektonExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Tekton CLI (tkn) command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m TektonExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("tekton: executing tkn command", "query", input.Command)

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	command := strings.TrimSpace(input.Command)
	if !strings.HasPrefix(command, "tkn") {
		command = "tkn " + command
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		env := map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		}

		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, env)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("tekton: unable to execute command", "error", err.Error(), "command", command)
			if response == "" {
				response = err.Error()
			}
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}

		outputformat := map[string]string{
			"stdout": response,
		}
		outputformatBytes, err := common.MarshalJson(outputformat)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("tekton: unable to marshal response", "error", err.Error())
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

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobTekton, command, nbRequestContext.AccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("tekton: unable to execute command", "error", err.Error(), "command", command)
		responseData := ""
		if response != nil {
			if responseData1, ok := response.(string); ok {
				responseData = responseData1
			}
		}

		enhancedError := parseTektonError(responseData, err.Error())

		return core.NBToolResponse{
			Data:   enhancedError,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("tekton command failed: %s", enhancedError)
	}

	data, ok := response.(string)
	if !ok {
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data:   "Unexpected response format from container job",
		}, fmt.Errorf("unexpected response type: %T", response)
	}

	resp := core.NBToolResponse{
		Data:       data,
		Type:       core.NBToolResponseTypeText,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"tekton", "pipelines"}, "Check Tekton Pipelines", nil, "")},
	}

	return resp, nil
}

func parseTektonError(output, originalError string) string {
	errorMsg := output
	if errorMsg == "" {
		errorMsg = originalError
	}

	lowerOutput := strings.ToLower(errorMsg)

	if strings.HasPrefix(lowerOutput, "erro[") {
		return errorMsg
	}

	if strings.Contains(lowerOutput, "pipelines.tekton.dev") && strings.Contains(lowerOutput, "not found") {
		return "Tekton Pipelines is not installed in this cluster. Please install Tekton Pipelines first."
	}

	if strings.Contains(lowerOutput, "command not found") || strings.Contains(lowerOutput, "executable file not found") {
		return "tkn CLI is not available. Please ensure the Tekton CLI is installed in the workspace."
	}

	if strings.Contains(lowerOutput, "doesn't have a resource type") || strings.Contains(lowerOutput, "not installed") {
		return "Tekton Pipelines is not installed in this cluster. Please install Tekton Pipelines first."
	}

	if strings.Contains(lowerOutput, "unauthenticated") || strings.Contains(lowerOutput, "unauthorized") {
		return "Tekton authentication failed. Please check cluster RBAC permissions."
	}

	if strings.Contains(lowerOutput, "permission denied") || strings.Contains(lowerOutput, "forbidden") {
		return "Tekton authorization failed. The service account may not have sufficient permissions to access Tekton resources."
	}

	if strings.Contains(lowerOutput, "no such host") {
		return "Cannot resolve the Kubernetes API server hostname. Please check cluster connectivity."
	}

	if strings.Contains(lowerOutput, "connection refused") || strings.Contains(lowerOutput, "dial tcp") {
		return "Cannot connect to the Kubernetes API server. Please check network connectivity."
	}

	if strings.Contains(lowerOutput, "not found") && (strings.Contains(lowerOutput, "pipelinerun") || strings.Contains(lowerOutput, "taskrun")) {
		return "Tekton resource not found. Please check the resource name and namespace."
	}

	if strings.Contains(lowerOutput, "timeout") || strings.Contains(lowerOutput, "deadline exceeded") {
		return "Tekton operation timed out. The cluster API may be overloaded or the operation may take longer than expected."
	}

	return errorMsg
}

func (m TektonExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{},
		ConfigType:   "tekton",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Default Tekton namespace",
				Default:     "",
			},
			"timeout": {
				Type:        core.ToolSchemaTypeString,
				Description: "Command timeout in seconds (defaults to 30)",
				Default:     "30",
			},
		},
	}
}

func (m TektonExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a Tekton CI/CD security expert. Your task is to classify a 'tkn' command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: tkn pipelinerun list
	answer: read

	input: tkn pipelinerun describe my-run
	answer: read

	input: tkn pipelinerun logs my-run -a
	answer: read

	input: tkn taskrun list
	answer: read

	input: tkn taskrun logs my-taskrun
	answer: read

	input: tkn pipeline list
	answer: read

	input: tkn task list
	answer: read

	input: tkn pipeline start my-pipeline
	answer: create

	input: tkn pipelinerun cancel my-run
	answer: update

	input: tkn pipelinerun delete my-run
	answer: delete

	input: tkn taskrun delete my-taskrun
	answer: delete
	`
	return prompt, nil
}
