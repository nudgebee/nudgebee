package executors

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
	"strings"
	"time"
)

type AwsSsmExecutor struct {
}

func NewAwsSsmExecutor() *AwsSsmExecutor {
	return &AwsSsmExecutor{}
}

func (e *AwsSsmExecutor) Execute(taskCtx types.TaskContext, execConfig ExecutionConfig) (string, error) {
	if execConfig.TargetID == "" {
		return "", fmt.Errorf("target_id is required for aws_ssm executor")
	}
	if execConfig.Region == "" {
		return "", fmt.Errorf("region is required for aws_ssm executor")
	}

	// Determine Document Name and Parameters based on OS type
	var documentName string
	var commands []string
	var err error

	if strings.ToLower(execConfig.OSType) == "windows" {
		documentName = "AWS-RunPowerShellScript"
		var scriptLine string
		scriptLine, err = BuildWindowsScriptWrapper(execConfig)
		commands = []string{scriptLine}
	} else {
		documentName = "AWS-RunShellScript"
		var scriptLine string
		scriptLine, err = BuildLinuxScriptWrapper(execConfig)
		commands = []string{scriptLine}
	}

	if err != nil {
		return "", err
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(execConfig.AccountID)

	// Build SSM parameters JSON
	parametersJSON, err := json.Marshal(map[string][]string{
		"commands": commands,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal SSM parameters: %w", err)
	}

	slog.Info("AWS SSM preparing send-command",
		"language", execConfig.Language,
		"documentName", documentName,
		"instanceId", execConfig.TargetID,
		"region", execConfig.Region,
	)

	// Send command via cloud.ExecuteCli
	sendCmd := fmt.Sprintf(
		"aws ssm send-command --document-name %s --instance-ids %s --parameters %s --region %s --output json",
		shellQuote(documentName),
		shellQuote(execConfig.TargetID),
		shellQuote(string(parametersJSON)),
		shellQuote(execConfig.Region),
	)

	sendResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: execConfig.AccountID,
		Command:   sendCmd,
	})
	if err != nil {
		return "", fmt.Errorf("failed to send SSM command: %w", err)
	}

	// Parse CommandId from response
	var sendResult struct {
		Command struct {
			CommandId string `json:"CommandId"`
		} `json:"Command"`
	}
	if err := json.Unmarshal([]byte(sendResp), &sendResult); err != nil {
		return "", fmt.Errorf("failed to parse SSM send-command response: %w", err)
	}
	commandId := sendResult.Command.CommandId
	if commandId == "" {
		return "", fmt.Errorf("SSM did not return a command ID")
	}

	slog.Info("AWS SSM Command sent", "commandId", commandId, "instanceId", execConfig.TargetID)

	// Poll for completion
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-taskCtx.GetContext().Done():
			// Attempt to cancel command
			cancelCmd := fmt.Sprintf(
				"aws ssm cancel-command --command-id %s --instance-ids %s --region %s",
				shellQuote(commandId),
				shellQuote(execConfig.TargetID),
				shellQuote(execConfig.Region),
			)
			_, _ = cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
				AccountID: execConfig.AccountID,
				Command:   cancelCmd,
			})
			return "", taskCtx.GetContext().Err()
		case <-ticker.C:
			pollCmd := fmt.Sprintf(
				"aws ssm get-command-invocation --command-id %s --instance-id %s --region %s --output json",
				shellQuote(commandId),
				shellQuote(execConfig.TargetID),
				shellQuote(execConfig.Region),
			)
			pollResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
				AccountID: execConfig.AccountID,
				Command:   pollCmd,
			})
			if err != nil {
				slog.Warn("Error getting command invocation", "error", err)
				continue
			}

			var invocation struct {
				Status                string `json:"Status"`
				StatusDetails         string `json:"StatusDetails"`
				StandardOutputContent string `json:"StandardOutputContent"`
				StandardErrorContent  string `json:"StandardErrorContent"`
			}
			if err := json.Unmarshal([]byte(pollResp), &invocation); err != nil {
				slog.Warn("Error parsing command invocation response", "error", err)
				continue
			}

			switch invocation.Status {
			case "Pending", "InProgress", "Delayed":
				continue
			case "Success", "Failed":
				stdout := strings.TrimSpace(invocation.StandardOutputContent)
				stderr := strings.TrimSpace(invocation.StandardErrorContent)
				output := stdout
				if stderr != "" {
					if output != "" {
						output += "\n" + stderr
					} else {
						output = stderr
					}
				}
				if invocation.Status == "Failed" {
					return output, fmt.Errorf("command failed: %s\n%s", invocation.StatusDetails, output)
				}
				return output, nil
			case "Cancelled":
				return "", fmt.Errorf("command cancelled")
			case "TimedOut":
				return "", fmt.Errorf("command timed out")
			default:
				return "", fmt.Errorf("unknown command status: %s", invocation.Status)
			}
		}
	}
}
