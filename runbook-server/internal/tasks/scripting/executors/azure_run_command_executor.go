package executors

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
	"strings"
)

type AzureRunCommandExecutor struct{}

func NewAzureRunCommandExecutor() *AzureRunCommandExecutor {
	return &AzureRunCommandExecutor{}
}

func (e *AzureRunCommandExecutor) Execute(taskCtx types.TaskContext, execConfig ExecutionConfig) (string, error) {
	if execConfig.TargetID == "" {
		return "", fmt.Errorf("target_id is required for azure_run_command executor (VM resource ID)")
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(execConfig.AccountID)

	var commandID string
	var scriptLine string
	var err error

	if strings.ToLower(execConfig.OSType) == "windows" {
		commandID = "RunPowerShellScript"
		scriptLine, err = BuildWindowsScriptWrapper(execConfig)
	} else {
		commandID = "RunShellScript"
		scriptLine, err = BuildLinuxScriptWrapper(execConfig)
	}

	if err != nil {
		return "", err
	}

	slog.Info("Azure run-command preparing invoke",
		"language", execConfig.Language,
		"commandId", commandID,
		"targetId", execConfig.TargetID,
	)

	cmd := fmt.Sprintf(
		"az vm run-command invoke --ids %s --command-id %s --scripts %s --output json",
		shellQuote(execConfig.TargetID),
		shellQuote(commandID),
		shellQuote(scriptLine),
	)

	type result struct {
		output string
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
			AccountID: execConfig.AccountID,
			Command:   cmd,
		})
		resultCh <- result{resp, err}
	}()

	select {
	case <-taskCtx.GetContext().Done():
		return "", taskCtx.GetContext().Err()
	case r := <-resultCh:
		if r.err != nil {
			return "", fmt.Errorf("azure run-command failed: %w", r.err)
		}
		output, err := parseAzureRunCommandOutput(r.output)
		if err != nil {
			slog.Error("Azure run-command script failed", "targetId", execConfig.TargetID, "error", err)
		} else {
			slog.Info("Azure run-command completed", "targetId", execConfig.TargetID)
		}
		return output, err
	}
}

// parseAzureRunCommandOutput extracts stdout/stderr from the az vm run-command JSON response.
//
// Response shape:
//
//	{
//	  "value": [
//	    {"code": "ComponentStatus/StdOut/succeeded", "message": "<stdout>"},
//	    {"code": "ComponentStatus/StdErr/succeeded", "message": "<stderr>"},
//	    {"code": "ProvisioningState/succeeded",      "message": ""}   // or "failed"
//	  ]
//	}
func parseAzureRunCommandOutput(resp string) (string, error) {
	var result struct {
		Value []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"value"`
	}
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return "", fmt.Errorf("failed to parse Azure run-command JSON response: %w. Raw response: %s", err, resp)
	}

	var stdout, stderr string
	var failed bool
	for _, v := range result.Value {
		codeLower := strings.ToLower(v.Code)
		switch {
		case strings.Contains(codeLower, "stdout"):
			stdout += v.Message
		case strings.Contains(codeLower, "stderr"):
			stderr += v.Message
		case strings.Contains(codeLower, "provisioningstate") && strings.Contains(codeLower, "failed"):
			failed = true
		}
	}

	output := strings.TrimSpace(stdout)
	if stderr != "" {
		trimmedStderr := strings.TrimSpace(stderr)
		if trimmedStderr != "" {
			if output != "" {
				output += "\n" + trimmedStderr
			} else {
				output = trimmedStderr
			}
		}
	}

	if failed {
		return output, fmt.Errorf("command failed: %s", output)
	}
	return output, nil
}
