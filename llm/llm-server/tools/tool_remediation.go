package tools

import (
	"fmt"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
	"time"
)

const ToolRemediationGenerate = "remediation_generate"
const ToolRemediationExecute = "remediation_execute"

func init() {
	core.RegisterNBToolFactory(ToolRemediationGenerate, func(accountId string) (core.NBTool, error) {
		return RemediationGenerateTool{}, nil
	})
	core.RegisterNBToolFactory(ToolRemediationExecute, func(accountId string) (core.NBTool, error) {
		return RemediationExecuteTool{}, nil
	})
}

// RemediationGenerateTool analyzes investigation context and generates a remediation plan
// it produces the RCA and proposed fixes without executing them
type RemediationGenerateTool struct{}

func (r RemediationGenerateTool) Name() string {
	return ToolRemediationGenerate
}

func (r RemediationGenerateTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (r RemediationGenerateTool) Description() string {
	return `Analyzes investigation findings and generates a detailed remediation plan with root cause analysis.

**IMPORTANT: Only call this tool when investigation reveals an ACTIONABLE issue requiring intervention.**

When to USE this tool:
* Investigation found fixable issues: OOMKilled, CrashLoopBackOff, ImagePullBackOff, pod failures
* Configuration errors: wrong env vars, incorrect service selectors, missing ConfigMap/Secret keys
* Resource constraints: CPU/memory limits too low, quota exhausted, insufficient replicas
* Deployment issues: wrong image tag, invalid manifest, failed rollout
* Networking problems: service endpoints missing, DNS resolution failures, wrong ports
* User explicitly requests a fix: "fix this", "resolve the issue", "apply the solution"

When to SKIP this tool (DO NOT call):
* Query is purely informational: "show me pods", "list deployments", "get logs", "what's the CPU usage"
* Investigation shows system is healthy: "all pods running fine", "no errors found", "metrics are normal"
* Issue is external/unfixable: "cloud provider outage", "external database down", "third-party API unavailable"
* Requires manual admin intervention: "missing RBAC permissions", "cluster upgrade needed", "certificate renewal required"
* User is exploring/learning: "explain this config", "how does this work", "tell me about the cluster"

Usage:
* Input: Investigation context including findings, tool observations, and diagnostics
* Output: Structured remediation plan with root cause analysis, proposed fixes, and commands

Output Format:
The tool returns a structured plan with:
- Root Cause Analysis: What went wrong and why
- Impact Assessment: How the issue affects the system
- Proposed Solution: Detailed fix strategy
- Commands: Exact kubectl/helm commands to execute
- Verification Steps: How to confirm the fix worked
- Rollback Plan: How to undo changes if needed

Examples:
1. OOMKilled pod analysis:
   Input: Investigation found pod OOMKilled with 512Mi limit
   Output: RCA identifying memory pressure, proposed limit increase to 1Gi

2. CrashLoopBackOff analysis:
   Input: Pod failing with config error
   Output: RCA identifying missing ConfigMap, proposed ConfigMap creation

3. High CPU usage:
   Input: Deployment using 95% CPU
   Output: RCA identifying resource contention, proposed HPA configuration
`
}

func (r RemediationGenerateTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"investigation_context": {
				Type:        core.ToolSchemaTypeString,
				Description: "Complete investigation context including user question, findings, and tools observations",
			},
		},
		Required: []string{"investigation_context"},
	}
}

func (r RemediationGenerateTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	logger := nbRequestContext.Ctx.GetLogger()
	logger.Info("remediation_generate: analyzing investigation context and generating remediation plan")

	// The investigation context is passed in the command
	investigationContext := strings.TrimSpace(input.Command)
	if investigationContext == "" {
		return core.NBToolResponse{
			Data:   "No investigation context provided",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("investigation context is required")
	}

	// Load the system prompt from the embedded prompt file rather than inlining it here.
	// This allows the prompt to be version-controlled, reviewed, and updated independently
	// of the tool's execution logic. See agents/prompts_repo/tool_remediation_generate.txt.
	systemPrompt := prompts_repo.GetPrompt(prompts_repo.PromptToolRemediationGenerate)
	if strings.TrimSpace(systemPrompt) == "" {
		logger.Error("remediation_generate: system prompt not found — embed may be broken")
		return core.NBToolResponse{
			Data:   "Remediation system prompt is not configured",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("remediation system prompt is missing")
	}

	userPrompt := investigationContext

	// Use LLM to generate the remediation plan
	// Note: "LLM" is the tool name constant defined in agents/core/llm_common.go
	// We use the string directly to avoid circular import dependency
	llmTool, ok := core.GetNBTool(nbRequestContext.AccountId, "LLM")
	if !ok {
		return core.NBToolResponse{
			Data:   "LLM tool not available",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("llm tool not found")
	}

	llmRequest := core.NBToolCallRequest{
		Command: userPrompt,
		Context: systemPrompt,
	}

	llmResponse, err := llmTool.Call(nbRequestContext, llmRequest)
	if err != nil {
		logger.Error("remediation_generate: failed to generate plan via LLM", "error", err)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Failed to generate remediation plan: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	logger.Info("remediation_generate: successfully generated remediation plan",
		"plan_length", len(llmResponse.Data))

	return core.NBToolResponse{
		Data:   llmResponse.Data,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
		AdditionalDetails: map[string]any{
			"action_type":        "plan_generation",
			"execution_status":   "not_executed",
			"requires_approval":  true,
			"phase":              "remediation_planning",
			"next_action":        "user_approval_required",
			"remediation_status": "plan_created_awaiting_approval",
		},
	}, nil
}

func (r RemediationGenerateTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{},
		ConfigSource: core.ToolConfigSourceAccountAgent,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}

type RemediationExecuteTool struct{}

func (r RemediationExecuteTool) Name() string {
	return ToolRemediationExecute
}

func (r RemediationExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (r RemediationExecuteTool) Description() string {
	return `Executes remediation commands to fix identified Kubernetes and infrastructure issues.

Usage:
* Input: Remediation command(s) to execute (kubectl, helm, shell commands, etc.)
* Output: Command execution results with stdout, stderr, and success status

Purpose: Safely execute remediation steps to resolve issues identified during debugging and troubleshooting.

IMPORTANT SAFETY REQUIREMENTS:
* User approval is required before executing commands (handled automatically by the system)
* Commands are executed in the customer's cluster via relay, not locally
* Built-in safety validation blocks destructive patterns

Supported Command Types:
* kubectl commands (e.g., "kubectl scale deployment my-app --replicas=3 -n default")
* helm commands (e.g., "helm upgrade my-release ./chart")
* shell commands (e.g., "echo 'test'")
* ArgoCD commands (e.g., "argocd app sync my-app")

Examples:
1. Scale deployment:
   kubectl scale deployment my-app --replicas=3 -n default

2. Restart deployment:
   kubectl rollout restart deployment my-app -n default

3. Update resource limits:
   kubectl patch deployment my-app -n default --type='json' -p='[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"512Mi"}]'
`
}

func (r RemediationExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "The remediation command to execute (kubectl, helm, shell, argocd). Command will require user approval before execution.",
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Kubernetes namespace for the command (optional, can be included in command with -n flag)",
			},
			"timeout_seconds": {
				Type:        core.ToolSchemaTypeNumber,
				Description: "Command execution timeout in seconds (default: 30)",
			},
		},
		Required: []string{"command"},
	}
}

type RemediationExecutionResult struct {
	Command    string `json:"command"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Success    bool   `json:"success"`
	Duration   string `json:"duration"`
	ExecutedAt string `json:"executed_at"`
	Error      string `json:"error,omitempty"`
}

func (r RemediationExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	logger := nbRequestContext.Ctx.GetLogger()
	logger.Info("remediation: executing remediation command", "command", input.Command)

	command := strings.TrimSpace(input.Command)
	if command == "" {
		return core.NBToolResponse{
			Data:   "Command cannot be empty",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("command cannot be empty")
	}

	// Validate command safety
	if err := r.validateCommandSafety(command); err != nil {
		logger.Warn("remediation: unsafe command rejected", "command", command, "error", err)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Command rejected for safety reasons: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// Approval is handled by the executor via InferToolRequestTypePrompt
	// No need to manually check for approval here

	// Append namespace if provided and not already in command
	if namespace, ok := input.Arguments["namespace"].(string); ok && namespace != "" {
		if !strings.Contains(command, "-n ") && !strings.Contains(command, "--namespace") {
			if strings.HasPrefix(command, "kubectl") {
				command = fmt.Sprintf("%s -n %s", command, namespace)
			}
		}
	}

	// Execute command via relay or workspace
	startTime := time.Now()
	result := RemediationExecutionResult{
		Command:    command,
		ExecutedAt: startTime.Format(time.RFC3339),
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		duration := time.Since(startTime)
		result.Duration = duration.String()

		if err != nil {
			logger.Error("remediation: command execution failed in workspace", "command", command, "error", err)
			result.Success = false
			result.ExitCode = 1
			result.Error = err.Error()
			result.Stderr = err.Error()
			if response != "" {
				result.Stdout = response
			}

			responseData, _ := r.formatExecutionResult(result)
			return core.NBToolResponse{
				Data:   responseData,
				Type:   core.NBToolResponseTypeJson,
				Status: core.NBToolResponseStatusError,
			}, nil
		}

		result.Success = true
		result.ExitCode = 0
		result.Stdout = response

		logger.Info("remediation: command executed successfully in workspace",
			"command", command,
			"duration", duration,
			"stdout_length", len(response))

		responseData, err := r.formatExecutionResult(result)
		if err != nil {
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Command executed but failed to format result: %v", err),
				Status: core.NBToolResponseStatusError,
			}, err
		}

		references := []core.NBToolResponseReference{}
		if strings.Contains(command, "kubectl") {
			references = append(references,
				core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"kubernetes", "applications"}, "View Cluster Workloads", nil, ""))
		}

		return core.NBToolResponse{
			Data:       responseData,
			Type:       core.NBToolResponseTypeJson,
			Status:     core.NBToolResponseStatusSuccess,
			References: references,
		}, nil
	}

	// Determine the relay job type based on command prefix
	var relayModule RelayJob
	switch {
	case strings.HasPrefix(command, "kubectl"):
		relayModule = RelayJobKubectl
	case strings.HasPrefix(command, "helm"):
		relayModule = RelayJobHelm
	case strings.HasPrefix(command, "argocd"):
		relayModule = RelayJobArgoCD
	default:
		// Default to shell for any other commands
		relayModule = RelayJobShell
	}

	response, err := ExecuteContainerJob(nbRequestContext, relayModule, command, nbRequestContext.AccountId, map[string]any{}, false)
	duration := time.Since(startTime)
	result.Duration = duration.String()

	// Extract stdout from response
	var stdout string
	if err == nil {
		if strOutput, ok := response.(string); ok {
			stdout = strOutput
		} else if mapOutput, ok := response.(map[string]any); ok {
			if stdoutVal, exists := mapOutput["stdout"]; exists {
				if stdoutStr, ok := stdoutVal.(string); ok {
					stdout = stdoutStr
				}
			}
		}
	}

	if err != nil {
		logger.Error("remediation: command execution failed", "command", command, "error", err)
		result.Success = false
		result.ExitCode = 1
		result.Error = err.Error()
		result.Stderr = err.Error()

		// Return error details in response
		responseData, _ := r.formatExecutionResult(result)
		return core.NBToolResponse{
			Data:   responseData,
			Type:   core.NBToolResponseTypeJson,
			Status: core.NBToolResponseStatusError,
		}, nil // Don't propagate error - we've captured it in result
	}

	result.Success = true
	result.ExitCode = 0
	result.Stdout = stdout

	logger.Info("remediation: command executed successfully",
		"command", command,
		"duration", duration,
		"stdout_length", len(stdout))

	// Format response
	responseData, err := r.formatExecutionResult(result)
	if err != nil {
		logger.Error("remediation: failed to format result", "error", err)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Command executed but failed to format result: %v", err),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// Add UI reference if applicable
	references := []core.NBToolResponseReference{}
	if strings.Contains(command, "kubectl") {
		references = append(references,
			core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"workloads"}, "View Cluster Workloads", nil, ""))
	}

	return core.NBToolResponse{
		Data:       responseData,
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: references,
	}, nil
}

// validateCommandSafety checks if the command is safe to execute
func (r RemediationExecuteTool) validateCommandSafety(command string) error {
	lowerCmd := strings.ToLower(strings.TrimSpace(command))

	// Block destructive commands without safeguards
	dangerousPatterns := []string{
		"rm -rf /",
		"dd if=/dev/zero",
		"mkfs.",
		":(){ :|:& };:", // fork bomb
		"> /dev/sda",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerCmd, pattern) {
			return fmt.Errorf("blocked destructive command pattern: %s", pattern)
		}
	}

	// Note: Dangerous operations (delete namespace, delete pv, etc.) are handled
	// via the InferToolRequestTypePrompt mechanism which requires user approval
	// for all create/update/delete operations

	return nil
}

// formatExecutionResult formats the execution result as JSON
func (r RemediationExecuteTool) formatExecutionResult(result RemediationExecutionResult) (string, error) {
	data, err := common.MarshalJson(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal execution result: %w", err)
	}
	return string(data), nil
}

// InferToolRequestTypePrompt returns a prompt to classify the remediation command type
// This enables automatic approval workflow via the executor
func (r RemediationExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a Kubernetes and infrastructure security expert. Your task is to classify a remediation command.

Based on the provided command, you must categorize its intent into exactly one of the following types:
* create
* update
* delete
* read

Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'update'.

Classification Rules:
- Commands that modify resource state (patch, scale, restart, edit, apply, set) are 'update'
- Commands that create new resources (create) are 'create'
- Commands that delete resources (delete) are 'delete'
- Commands that only retrieve information (get, describe, logs) are 'read'

Examples:

input: kubectl scale deployment my-app --replicas=3 -n default
answer: update

input: kubectl patch deployment nginx -n default --type='json' -p='[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"512Mi"}]'
answer: update

input: kubectl rollout restart deployment my-app -n production
answer: update

input: kubectl apply -f deployment.yaml -n default
answer: update

input: kubectl delete pod my-pod -n default
answer: delete

input: kubectl delete namespace old-namespace
answer: delete

input: kubectl create configmap my-config --from-file=config.yaml -n default
answer: create

input: kubectl get pods -n default
answer: read

input: helm upgrade my-release ./chart --namespace default
answer: update

input: helm install my-release ./chart --namespace default
answer: create

input: helm uninstall my-release --namespace default
answer: delete
`
	return prompt, nil
}
