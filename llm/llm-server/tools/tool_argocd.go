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

const ToolExecuteArgoCDCommand = "argocd_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteArgoCDCommand, func(accountId string) (core.NBTool, error) {
		return ArgoCDExecuteTool{}, nil
	})
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
	if !strings.HasPrefix(command, "argocd") {
		command = "argocd " + command
	}

	if config.Config.LlmServerWorkspaceEnabled {
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

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobArgoCD, command, nbRequestContext.AccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("argocd: unable to execute shell script", "error", err.Error(), "command", command)
		responseData := ""
		if response != nil {
			if responseData1, ok := response.(string); ok {
				responseData = responseData1
			}
		}

		// Enhanced error handling with ArgoCD-specific error parsing
		enhancedError := parseArgoCDError(responseData, err.Error())

		return core.NBToolResponse{
			Data:   enhancedError,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("argocd command failed: %s", enhancedError)
	}

	data := response.(string)

	// Check for ArgoCD-specific errors in successful responses
	if containsArgoCDError(data) {
		enhancedError := parseArgoCDError(data, "")
		return core.NBToolResponse{
			Data:   enhancedError,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("argocd command completed with errors: %s", enhancedError)
	}

	resp := core.NBToolResponse{
		Data:       data,
		Type:       core.NBToolResponseTypeText,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"argocd", "applications"}, "Check ArgoCD Apps", nil, "")},
	}

	return resp, nil
}

// parseArgoCDError provides enhanced error messages for common ArgoCD issues
func parseArgoCDError(output, originalError string) string {
	errorMsg := output
	if errorMsg == "" {
		errorMsg = originalError
	}

	lowerOutput := strings.ToLower(errorMsg)

	// Authentication errors
	if strings.Contains(lowerOutput, "unauthenticated") || strings.Contains(lowerOutput, "invalid token") {
		return "ArgoCD authentication failed. Please check your auth token or username/password credentials."
	}

	// Authorization errors
	if strings.Contains(lowerOutput, "permission denied") || strings.Contains(lowerOutput, "forbidden") {
		return "ArgoCD authorization failed. Your account may not have sufficient permissions for this operation."
	}

	// Connection errors
	if strings.Contains(lowerOutput, "connection refused") || strings.Contains(lowerOutput, "dial tcp") {
		return "Cannot connect to ArgoCD server. Please check server URL and network connectivity."
	}

	// TLS/SSL errors
	if strings.Contains(lowerOutput, "certificate") || strings.Contains(lowerOutput, "tls") || strings.Contains(lowerOutput, "ssl") {
		return "TLS/SSL certificate error. Consider using --insecure flag or check certificate configuration."
	}

	// Application not found errors
	if strings.Contains(lowerOutput, "not found") && strings.Contains(lowerOutput, "application") {
		return "ArgoCD application not found. Please check the application name and ensure it exists."
	}

	// Sync errors
	if strings.Contains(lowerOutput, "sync failed") || strings.Contains(lowerOutput, "synchronization failed") {
		return "ArgoCD application sync failed. Check application health and Git repository connectivity."
	}

	// Resource errors
	if strings.Contains(lowerOutput, "resource not found") || strings.Contains(lowerOutput, "no matches for kind") {
		return "Kubernetes resource not found. The application may reference non-existent resources."
	}

	// Git repository errors
	if strings.Contains(lowerOutput, "repository not accessible") || strings.Contains(lowerOutput, "git") {
		return "Git repository error. Please check repository URL and access credentials."
	}

	// Server errors
	if strings.Contains(lowerOutput, "internal server error") || strings.Contains(lowerOutput, "500") {
		return "ArgoCD server internal error. Please check ArgoCD server logs and status."
	}

	// Timeout errors
	if strings.Contains(lowerOutput, "timeout") || strings.Contains(lowerOutput, "deadline exceeded") {
		return "ArgoCD operation timed out. The server may be overloaded or the operation may take longer than expected."
	}

	// Return original error if no specific pattern matched
	return errorMsg
}

// containsArgoCDError checks if the output contains ArgoCD-specific error indicators
func containsArgoCDError(output string) bool {
	lowerOutput := strings.ToLower(output)
	errorIndicators := []string{
		"error:",
		"failed:",
		"unauthenticated",
		"permission denied",
		"forbidden",
		"not found",
		"sync failed",
		"internal server error",
		"timeout",
		"connection refused",
	}

	for _, indicator := range errorIndicators {
		if strings.Contains(lowerOutput, indicator) {
			return true
		}
	}

	return false
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
