package scripting

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
	integrationsService "nudgebee/runbook/services/integrations"
	"strings"
)

// RunScriptTask implements the Task interface for executing shell commands.
type RunScriptTask struct{}

func (t *RunScriptTask) GetName() string {
	return "scripting.run_script"
}

// GetDescription returns a brief description of the task.
func (t *RunScriptTask) GetDescription() string {
	return "Run a custom shell script (Bash, Python, etc.)."
}

// GetDisplayName returns a human-readable name for the task.
func (t *RunScriptTask) GetDisplayName() string {
	return "Run Script"
}

func (t *RunScriptTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	// Create a safe copy of params for logging to avoid exposing secrets
	logParams := make(map[string]any)

	// Allowed keys for full logging
	allowedKeys := map[string]bool{
		"language":    true,
		"cwd":         true,
		"account_id":  true,
		"image":       true,
		"resources":   true,
		"parser_type": true,
	}

	for k, v := range params {
		if allowedKeys[k] {
			logParams[k] = v
		} else if k == "script" {
			if str, ok := v.(string); ok {
				logParams[k] = fmt.Sprintf("<script content: %d bytes>", len(str))
			} else {
				logParams[k] = "[REDACTED]"
			}
		} else if k == "args" {
			if args, ok := v.([]any); ok {
				logParams[k] = fmt.Sprintf("<args: %d items>", len(args))
			} else if args, ok := v.([]string); ok {
				logParams[k] = fmt.Sprintf("<args: %d items>", len(args))
			} else {
				logParams[k] = "[REDACTED]"
			}
		} else {
			// Default redact for unknown or sensitive keys (like env)
			logParams[k] = "[REDACTED]"
		}
	}
	taskCtx.GetLogger().Debug("Executing RunScriptTask", "params", logParams)

	script, ok := params["script"].(string)
	if !ok {
		return nil, fmt.Errorf("missing required parameter: 'script'")
	}

	language, _ := params["language"].(string)
	if language == "" {
		if detected := detectScriptLanguage(script); detected != "" {
			language = detected
			taskCtx.GetLogger().Warn("Language not specified; auto-detected from script content. Set 'language' explicitly to avoid this warning.",
				"detected_language", detected)
		} else {
			language = "bash"
		}
	}

	accountId := taskCtx.GetAccountID()
	if params["account_id"] != nil {
		accountId = params["account_id"].(string)
	}

	args := []string{}
	if argsAny, ok := params["args"].([]any); ok {
		for _, a := range argsAny {
			if as, ok := a.(string); ok {
				args = append(args, as)
			}
		}
	} else if argsStr, ok := params["args"].([]string); ok {
		args = argsStr
	}

	osType, _ := params["os_type"].(string)
	if osType == "" {
		if language == "powershell" {
			osType = "windows"
		} else {
			osType = "linux"
		}
	}

	// Prepare Configuration
	config := executors.ExecutionConfig{
		AccountID: accountId,
		Script:    script,
		Language:  language,
		OSType:    osType,
		Env:       make(map[string]string),
		Args:      args,
	}

	// Executor Selection
	if executorType, ok := params["executor_type"].(string); ok {
		// Validation for specific executors
		switch executorType {
		case "aws_ssm", "gcp_compute_ssh":
			if params["target_id"] == nil || params["target_id"] == "" {
				return nil, fmt.Errorf("target_id is required for %s executor", executorType)
			}
			if params["region"] == nil || params["region"] == "" {
				return nil, fmt.Errorf("region is required for %s executor", executorType)
			}
		case "azure_run_command":
			if params["target_id"] == nil || params["target_id"] == "" {
				return nil, fmt.Errorf("target_id is required for %s executor", executorType)
			}
		case "ssh":
			if params["integration_id"] == nil || params["integration_id"] == "" {
				return nil, fmt.Errorf("integration_id is required for ssh executor")
			}
		}
		config.ExecutorType = executorType
	}

	// Remote Execution Params
	if targetID, ok := params["target_id"].(string); ok {
		config.TargetID = targetID
	}
	if region, ok := params["region"].(string); ok {
		config.Region = region
	}
	if integrationID, ok := params["integration_id"].(string); ok && integrationID != "" {
		// Only the ssh executor consumes IntegrationID; other executors set
		// target_id/region instead. Skip the DB hit unless this is ssh.
		if config.ExecutorType == "ssh" {
			resolved, err := integrationsService.ResolveIntegrationID(taskCtx.GetNewRequestContext(), integrationID, []string{"ssh"})
			if err != nil {
				return nil, err
			}
			integrationID = resolved
		}
		config.IntegrationID = integrationID
	}

	if cwd, ok := params["cwd"].(string); ok {
		config.Cwd = cwd
	}

	if envMapStr, ok := params["env"].(map[string]string); ok {
		config.Env = envMapStr
	} else if envMap, ok := params["env"].(map[string]any); ok {
		if config.Env == nil {
			config.Env = make(map[string]string)
		}
		for k, v := range envMap {
			if vs, ok := v.(string); ok {
				config.Env[k] = vs
			} else {
				config.Env[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	// For PowerShell, we ensure NO_COLOR and TERM=dumb are set by default to avoid ANSI codes
	if language == "powershell" {
		if config.Env == nil {
			config.Env = make(map[string]string)
		}
		if _, ok := config.Env["NO_COLOR"]; !ok {
			config.Env["NO_COLOR"] = "1"
		}
		if _, ok := config.Env["TERM"]; !ok {
			config.Env["TERM"] = "dumb"
		}
	}

	// Optional: Image Override
	if image, ok := params["image"].(string); ok {
		config.K8sImage = image
	}

	// Optional: Resource Limits
	if resMap, ok := params["resources"].(map[string]any); ok {
		resources := &executors.ResourceConfig{}
		if v, ok := resMap["cpu_request"].(string); ok {
			resources.CPURequest = v
		}
		if v, ok := resMap["cpu_limit"].(string); ok {
			resources.CPULimit = v
		}
		if v, ok := resMap["memory_request"].(string); ok {
			resources.MemoryRequest = v
		}
		if v, ok := resMap["memory_limit"].(string); ok {
			resources.MemoryLimit = v
		}
		config.K8sResources = resources
	}

	// Determine Executor based on account provider
	if accountId != "" {
		reqCtx := taskCtx.GetNewRequestContext()
		provider, err := cloud.GetAccountProvider(reqCtx, accountId)
		if err != nil {
			return nil, fmt.Errorf("failed to get account provider for account %s: %w", accountId, err)
		}
		config.AccountProvider = provider
	}

	// Instantiate Executor
	executor, err := executors.NewExecutor(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	output, err := executor.Execute(taskCtx, config)
	if err != nil {
		return nil, fmt.Errorf("script execution failed: %w\nOutput:\n%s", err, output)
	}

	if strings.TrimSpace(output) == "" {
		taskCtx.GetLogger().Warn("Script produced empty stdout. Ensure your script uses print()/echo to produce output; return statements are not captured.",
			"language", config.Language,
			"parser_type", params["parser_type"],
		)
	}

	var data any = output
	if params["parser_type"] == "json" {
		var parsed any
		if err := json.Unmarshal([]byte(output), &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse script output as JSON: %w (output: %s)", err, output)
		} else {
			data = parsed
		}
	}

	return map[string]any{
		"data": data,
	}, nil
}

func (t *RunScriptTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB AccountId",
				Required:    false,
				Title:       "Account",
				Order:       1,
			},
			"executor_type": {
				Type:        types.PropertyTypeString,
				Description: "The execution mode. Defaults to system config. Select the appropriate cloud executor (AWS SSM, Azure Run Command, GCP Compute SSH, etc.) depending on your infrastructure.",
				Required:    false,
				Title:       "Executor Type",
				Options:     []string{"kubernetes", "agent", "aws_ssm", "azure_run_command", "gcp_compute_ssh", "ssh"},
				Order:       2,
			},
			"os_type": {
				Type:        types.PropertyTypeString,
				Description: "The operating system of the target. Defaults to 'linux'. Affects the execution mechanism on cloud providers.",
				Required:    false,
				Default:     "linux",
				Options:     []string{"linux", "windows"},
				Title:       "Target OS",
				Order:       3,
			},
			"language": {
				Type:        types.PropertyTypeString,
				Description: "The script language. Supported values are 'bash', 'javascript', 'python', 'powershell'. Defaults to 'bash'.",
				Required:    false,
				Default:     "bash",
				Options:     []string{"bash", "javascript", "python", "powershell"},
				Title:       "Language",
				Order:       4,
			},
			"script": {
				Type:        types.PropertyTypeString,
				Description: "The shell script to execute.",
				Required:    true,
				Title:       "Script",
				Order:       5,
				SubType:     "code",
			},
			"args": {
				Type:        types.PropertyTypeArray,
				Description: "List of Args to pass script",
				Required:    false,
				Title:       "Arguments",
				Order:       6,
			},
			"env": {
				Type:        types.PropertyTypeObject,
				Description: "A map of environment variables to set for the script.",
				Required:    false,
				Title:       "Environment Variables",
				Order:       7,
			},
			"target_id": {
				Type:        types.PropertyTypeString,
				Description: "Target VM/Instance ID (e.g., EC2 Instance ID, Azure VM name, GCP Instance name or project/instance).",
				Required:    false,
				Title:       "Target ID",
				Order:       8,
				DependsOn:   []string{"executor_type"},
				VisibleWhen: &types.VisibleWhen{Field: "executor_type", Value: []string{"aws_ssm", "azure_run_command", "gcp_compute_ssh"}},
			},
			"region": {
				Type:        types.PropertyTypeString,
				Description: "Cloud Provider Region.",
				Required:    false,
				Title:       "Region",
				Order:       9,
				DependsOn:   []string{"executor_type"},
				VisibleWhen: &types.VisibleWhen{Field: "executor_type", Value: []string{"aws_ssm", "azure_run_command", "gcp_compute_ssh"}},
			},
			"integration_id": {
				Type:        types.PropertyTypeIntegration,
				Description: "Integration ID for SSH connection.",
				Required:    false,
				Title:       "Integration",
				Order:       10,
				DependsOn:   []string{"executor_type"},
				VisibleWhen: &types.VisibleWhen{Field: "executor_type", Value: []string{"ssh"}},
			},
			"cwd": {
				Type:        types.PropertyTypeString,
				Description: "The working directory to run the script in.",
				Required:    false,
				Title:       "Working Directory",
				Order:       11,
			},
			"image": {
				Type:        types.PropertyTypeString,
				Description: "Override the container image for execution. Defaults: bash/python use nudgebee-debug, javascript uses node:22-alpine, powershell uses mcr.microsoft.com/powershell:lts-alpine-3.17.",
				Required:    false,
				Title:       "Docker Image",
				Order:       12,
				DependsOn:   []string{"executor_type"},
				VisibleWhen: &types.VisibleWhen{Field: "executor_type", Value: []string{"kubernetes", "agent"}},
			},
			"resources": {
				Type:        types.PropertyTypeObject,
				Description: "Resource requests and limits. Keys: cpu_request, cpu_limit, memory_request, memory_limit.",
				Required:    false,
				Title:       "Resource Limits",
				Order:       13,
				DependsOn:   []string{"executor_type"},
				VisibleWhen: &types.VisibleWhen{Field: "executor_type", Value: []string{"kubernetes"}},
			},
			"parser_type": {
				Type:        types.PropertyTypeString,
				Description: "The format to parse the output as. Supported values: 'json'.",
				Required:    false,
				Title:       "Output Parser",
				Order:       14,
			},
		},
	}
}

func (t *RunScriptTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeAny,
				Description: "The output of the script. If parser_type is 'json', this will be a structured object.",
				Required:    true,
			},
		},
	}
}

func (t *RunScriptTask) RuntimeNotes() []string {
	return []string{
		"Always set 'language' explicitly (bash/python/javascript/powershell). If omitted, auto-detection is attempted but may default to bash.",
		"Scripts capture stdout only. Using 'return' without 'print' produces empty output.",
		"For Python scripts, always use print(json.dumps(...)) to produce output and set parser_type='json' to get structured data.",
		"Pass task output to scripts via the 'env' parameter: { \"INPUT\": \"{{ Tasks['x'].output.data | to_json }}\" }, then read with json.loads(os.environ['INPUT']). Do NOT embed {{ }} inside the script string — it breaks when data contains quotes.",
	}
}

// detectScriptLanguage attempts to identify the script language from its content.
// Returns the detected language or empty string if detection is inconclusive.
func detectScriptLanguage(script string) string {
	// Check shebang line first — most reliable signal
	trimmed := strings.TrimSpace(script)
	if strings.HasPrefix(trimmed, "#!") {
		line := strings.SplitN(trimmed, "\n", 2)[0]
		if strings.Contains(line, "python") {
			return "python"
		}
		if strings.Contains(line, "node") {
			return "javascript"
		}
		if strings.Contains(line, "pwsh") || strings.Contains(line, "powershell") {
			return "powershell"
		}
		if strings.Contains(line, "bash") || strings.Contains(line, "sh") {
			return "bash"
		}
	}

	// Fall back to keyword heuristics
	pythonIndicators := []string{"import ", "from ", "def ", "print(", "json.loads", "class ", "if __name__"}
	if countMatches(script, pythonIndicators) >= 2 {
		return "python"
	}

	jsIndicators := []string{"const ", "let ", "console.log", "require(", "module.exports", "async ", "await "}
	if countMatches(script, jsIndicators) >= 2 {
		return "javascript"
	}

	psIndicators := []string{"$PSVersionTable", "Get-", "Set-", "Write-Host", "Write-Output", "param("}
	if countMatches(script, psIndicators) >= 2 {
		return "powershell"
	}

	return ""
}

func countMatches(script string, indicators []string) int {
	count := 0
	for _, indicator := range indicators {
		if strings.Contains(script, indicator) {
			count++
		}
	}
	return count
}
