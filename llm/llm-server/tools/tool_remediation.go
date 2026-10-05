package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"strings"
	"time"
)

// Kubernetes namespace: RFC 1123 DNS label (lowercase alphanumeric + hyphens, 1-63 chars)
var k8sNamespaceRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

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
* Input: pass the full investigation context (findings, tool observations, and diagnostics) as the plain-text command input — there is no separate named field
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

	// Load the system prompt from prompts/default/v1/tools/remediation_generate.yaml.
	// Keeping it out of Go source lets it be versioned, A/B tested and rolled back per
	// account independently of this tool's execution logic. There is deliberately no
	// fallback to an older copy: the file is embedded and validated at build time, so a
	// failure here means a real defect and must surface as one.
	systemPrompt, err := prompts.GetPromptStrict(nbRequestContext.Ctx.GetContext(), prompts.PromptRemediationGenerate, nbRequestContext.AccountId)
	if err != nil {
		logger.Error("remediation_generate: system prompt could not be loaded", "error", err)
		return core.NBToolResponse{
			Data:   "Remediation system prompt is not configured",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("remediation_generate: loading system prompt: %w", err)
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
		if !k8sNamespaceRe.MatchString(namespace) {
			return core.NBToolResponse{
				Data:   "Invalid namespace: must be a valid Kubernetes namespace (lowercase alphanumeric and hyphens, 1-63 chars)",
				Status: core.NBToolResponseStatusError,
			}, fmt.Errorf("invalid namespace %q: does not match Kubernetes naming rules", namespace)
		}
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

	// The executor comes from the account's provider, the same way the remediation panel picks it, so
	// the two callers cannot disagree about where a command runs.
	//
	// A cloud CLI has to go through its own tool to get the account's credentials injected. Run it
	// anywhere else and it executes unauthenticated, prints its auth-required hint to stdout, exits 0,
	// and gets recorded as a successful remediation for an action that never happened (#28804).
	substrate := RemediationSubstrateFor(GetCloudProviderForAccount(nbRequestContext.AccountId), command)
	if substrate.Reject != "" {
		logger.Warn("remediation: command does not match the account's provider", "command", command, "reason", substrate.Reject)
		return core.NBToolResponse{
			Data:   "Command rejected: " + substrate.Reject,
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("remediation: %s", substrate.Reject)
	}
	if cloudCliTool := substrate.CloudCliTool; cloudCliTool != "" {
		stdout, execErr := ExecuteCloudCli(nbRequestContext.Ctx, cloudCliTool, nbRequestContext.AccountId, nbRequestContext.ConversationId, command)
		result.Duration = time.Since(startTime).String()
		result.Stdout = stdout
		if execErr != nil {
			logger.Error("remediation: cloud cli command failed", "command", command, "tool", cloudCliTool, "error", execErr)
			result.Success = false
			result.ExitCode = 1
			result.Error = execErr.Error()
			result.Stderr = execErr.Error()
			responseData, formatErr := r.formatExecutionResult(result)
			if formatErr != nil {
				logger.Error("remediation: failed to format execution result after command error", "format_error", formatErr, "command_error", execErr)
				return core.NBToolResponse{
					Data:   fmt.Sprintf("Command failed (%s) and formatting the result also failed: %s", execErr, formatErr),
					Status: core.NBToolResponseStatusError,
				}, nil
			}
			return core.NBToolResponse{
				Data:   responseData,
				Type:   core.NBToolResponseTypeJson,
				Status: core.NBToolResponseStatusError,
			}, nil
		}
		result.Success = true
		result.ExitCode = 0
		responseData, err := r.formatExecutionResult(result)
		if err != nil {
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Command executed but failed to format result: %v", err),
				Status: core.NBToolResponseStatusError,
			}, err
		}
		return core.NBToolResponse{
			Data:   responseData,
			Type:   core.NBToolResponseTypeJson,
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

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

		responseData, formatErr := r.formatExecutionResult(result)
		if formatErr != nil {
			logger.Error("remediation: failed to format execution result after command error", "format_error", formatErr, "command_error", err)
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Command failed (%s) and formatting the result also failed: %s", err, formatErr),
				Status: core.NBToolResponseStatusError,
			}, nil
		}
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

// ValidateCommandSafety blocks catastrophic command patterns (fork bomb, disk wipe, etc).
// Exported so direct execute handlers (e.g. the remediation RPC play-button path) enforce the
// same hard-block layer the agent tool applies. Write-level gating (create/update/delete) is a
// separate concern handled via RBAC at the call site.
//
// Note: Dangerous k8s operations (delete namespace, delete pv, etc.) are intentionally NOT blocked
// here — they are gated by the write-access RBAC check, not this destructive-pattern list.
func ValidateCommandSafety(command string) error {
	// Collapse runs of whitespace to single spaces so the substring patterns survive spacing
	// variation ("rm -rf  /", tabs, etc.).
	lowerCmd := strings.Join(strings.Fields(strings.ToLower(command)), " ")

	dangerousPatterns := []string{
		"rm -rf /",
		"rm -fr /",
		"rm -r -f /",
		"rm -f -r /",
		"--no-preserve-root",
		"dd if=/dev/zero",
		"dd if=/dev/random",
		"mkfs.",
		":(){ :|:& };:", // fork bomb
		"> /dev/sda",
		"of=/dev/sda",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerCmd, pattern) {
			return fmt.Errorf("blocked destructive command pattern: %s", pattern)
		}
	}

	if err := validateCloudCommandSafety(lowerCmd); err != nil {
		return err
	}

	return nil
}

// irreversibleCloudOperations are cloud operations that destroy state no rollback can restore.
//
// The k8s list above is deliberately permissive because its worst cases are recoverable: a deleted
// pod is rescheduled, a deleted deployment is redeployed from git. Cloud is not symmetric. A
// terminated instance takes its instance-store data and its identity with it, a deleted RDS
// instance without a final snapshot is unrecoverable, and neither has a rollback_command that
// means anything — the plan would offer one and it would be a lie.
//
// So these are blocked outright rather than left to the approval workflow. An operator who
// genuinely needs to terminate an instance can do it in the AWS console, where the consequence is
// unambiguous and the audit trail is theirs. That is a deliberately worse workflow for a
// deliberately rare action.
var irreversibleCloudOperations = []string{
	"terminate-instances",
	"delete-db-instance",
	"delete-db-cluster",
	"delete-cluster",
	"delete-bucket",
	"delete-vpc",
	"delete-nat-gateway",
	"delete-volume",
	"delete-snapshot",
	"deregister-image",
	"delete-key-pair",
	"delete-load-balancer",
	"delete-auto-scaling-group",
	"delete-stack",
	"delete-secret",
	"schedule-key-deletion",
	"delete-file-system",
	"delete-table",
	// `aws s3 rb` is the same destruction as s3api delete-bucket under a shorter name.
	"s3 rb",
	// Azure equivalents.
	"vm delete",
	"group delete",
	"sql server delete",
	"sql db delete",
	"storage account delete",
	"aks delete",
	"disk delete",
	"snapshot delete",
	"keyvault delete",
	// GCP equivalents.
	"instances delete",
	"clusters delete",
	"sql instances delete",
	"disks delete",
	"snapshots delete",
	"buckets delete",
	// Any gsutil rm is an object delete with no rollback. Scoped to the subcommand path, so the
	// recursive flag that makes it a bucket wipe is not needed to catch it - and object deletion is
	// not a remediation for an infrastructure alarm in the first place.
	"gsutil rm",
}

// leadingCommandNoise are tokens that can precede the real executable without changing what runs.
// Stripped before deciding whether this is a cloud CLI invocation.
var leadingCommandNoise = map[string]bool{
	"sudo": true, "env": true, "time": true, "command": true, "nohup": true, "exec": true,
}

// cloudCommandTokens returns the command's tokens with leading environment assignments and wrapper
// programs removed, and the executable reduced to its base name. Returns nil when what remains is
// not a cloud CLI invocation.
//
// This is deliberately more permissive about what counts as a cloud command than CloudCliToolFor,
// which matches fields[0] exactly. The asymmetry is the safe direction and is intentional: the
// router decides whether to inject credentials, and being strict there means an odd invocation
// simply fails; this guard decides whether to refuse, and being strict here would let
// "sudo aws ec2 terminate-instances" past. Today those two agreeing is what stops the prefixed form
// doing damage - it is not routed, so it runs without credentials and fails. That is a coincidence
// between two functions, not an invariant, and it stops holding the moment the execution
// environment gains ambient credentials (an instance profile on the workspace pod, for one). So the
// guard does not rely on it.
func cloudCommandTokens(lowerCmd string) []string {
	fields := strings.Fields(lowerCmd)
	for len(fields) > 0 {
		head := fields[0]
		// FOO=bar prefixes: an assignment cannot be the program being run.
		if i := strings.Index(head, "="); i > 0 && !strings.ContainsAny(head[:i], "/-.") {
			fields = fields[1:]
			continue
		}
		if leadingCommandNoise[head] {
			fields = fields[1:]
			continue
		}
		break
	}
	if len(fields) == 0 {
		return nil
	}
	// /usr/local/bin/aws and aws are the same program.
	exe := fields[0]
	if i := strings.LastIndex(exe, "/"); i >= 0 {
		exe = exe[i+1:]
	}
	switch exe {
	case "aws", "az", "gcloud", "gsutil":
	default:
		return nil
	}
	out := append([]string{exe}, fields[1:]...)
	return out
}

// validateCloudCommandSafety blocks irreversible cloud operations. Only applied to cloud CLI
// invocations so a kubectl or shell command containing a similar word is unaffected.
func validateCloudCommandSafety(lowerCmd string) error {
	tokens := cloudCommandTokens(lowerCmd)
	if tokens == nil {
		return nil
	}

	// Match against the subcommand path only - the tokens before the first flag. A provider CLI
	// puts service and operation there ("aws ec2 terminate-instances"), so this still catches every
	// real invocation while a resource that merely happens to be NAMED like one stops being a false
	// positive: `aws ec2 describe-instances --filters Name=tag:Name,Values=delete-volume` is a
	// read, and blocking it would train people to route around the guard.
	path := tokens
	for i, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			path = tokens[:i]
			break
		}
	}
	joined := strings.Join(path, " ")

	for _, op := range irreversibleCloudOperations {
		if strings.Contains(joined, op) {
			return fmt.Errorf(
				"blocked irreversible cloud operation %q: this destroys state that no rollback can "+
					"restore, so it is not available as an automated remediation - run it from the "+
					"cloud console if it is genuinely required", op)
		}
	}

	return nil
}

// validateCommandSafety checks if the command is safe to execute (agent-tool path).
func (r RemediationExecuteTool) validateCommandSafety(command string) error {
	return ValidateCommandSafety(command)
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
