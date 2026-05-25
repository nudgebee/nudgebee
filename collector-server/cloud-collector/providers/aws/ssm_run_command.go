package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// SSMCommandTemplate represents a predefined SSM command template
type SSMCommandTemplate struct {
	ID               string
	Name             string
	Description      string
	DocumentName     string
	Parameters       map[string][]string
	Comment          string
	AllowedOverrides []string // Keys that the caller may override
}

// GetSSMCommandTemplates returns predefined command templates
func GetSSMCommandTemplates() map[string]SSMCommandTemplate {
	return map[string]SSMCommandTemplate{
		"update_ssm_agent": {
			ID:           "update_ssm_agent",
			Name:         "Update SSM Agent",
			Description:  "Update the SSM agent to the latest version",
			DocumentName: "AWS-UpdateSSMAgent",
			Parameters: map[string][]string{
				"allowDowngrade": {"false"},
			},
			Comment:          "Automated SSM agent update via Nudgebee",
			AllowedOverrides: []string{"allowDowngrade"}, // Only allow override of this parameter
		},
		"check_disk_space": {
			ID:           "check_disk_space",
			Name:         "Check Disk Space",
			Description:  "Check available disk space on instance",
			DocumentName: "AWS-RunShellScript",
			Parameters: map[string][]string{
				"commands": {"df -h"},
			},
			Comment:          "Disk space check via Nudgebee",
			AllowedOverrides: []string{}, // No overrides allowed - fixed command
		},
		"check_memory": {
			ID:           "check_memory",
			Name:         "Check Memory Usage",
			Description:  "Check memory usage on instance",
			DocumentName: "AWS-RunShellScript",
			Parameters: map[string][]string{
				"commands": {"free -m"},
			},
			Comment:          "Memory check via Nudgebee",
			AllowedOverrides: []string{}, // No overrides allowed - fixed command
		},
		"install_cloudwatch_agent": {
			ID:           "install_cloudwatch_agent",
			Name:         "Install CloudWatch Agent",
			Description:  "Install and configure CloudWatch agent",
			DocumentName: "AWS-ConfigureAWSPackage",
			Parameters: map[string][]string{
				"action":  {"Install"},
				"name":    {"AmazonCloudWatchAgent"},
				"version": {"latest"},
			},
			Comment:          "CloudWatch agent installation via Nudgebee",
			AllowedOverrides: []string{"version"}, // Allow version override only
		},
	}
}

// RunSSMCommand executes a predefined SSM command on target instances
func RunSSMCommand(
	ctx context.Context,
	cfg aws.Config,
	templateID string,
	instanceIDs []string,
	customParams map[string]interface{},
) (string, error) {
	client := ssm.NewFromConfig(cfg)

	templates := GetSSMCommandTemplates()
	template, ok := templates[templateID]
	if !ok {
		return "", fmt.Errorf("unknown template ID: %s", templateID)
	}

	// Build parameters, starting with template defaults
	parameters := make(map[string][]string)
	for k, v := range template.Parameters {
		parameters[k] = v
	}

	// Validate and apply custom parameters (if any provided)
	if len(customParams) > 0 {
		for key, val := range customParams {
			// Security check: only allow whitelisted parameter overrides
			allowed := false
			for _, allowedKey := range template.AllowedOverrides {
				if key == allowedKey {
					allowed = true
					break
				}
			}

			if !allowed {
				return "", fmt.Errorf("parameter '%s' is not allowed for template '%s' (allowed: %v)", key, templateID, template.AllowedOverrides)
			}

			// Convert value to string slice
			switch v := val.(type) {
			case string:
				parameters[key] = []string{v}
			case []string:
				parameters[key] = v
			case []interface{}:
				strSlice := make([]string, len(v))
				for i, item := range v {
					strSlice[i] = fmt.Sprintf("%v", item)
				}
				parameters[key] = strSlice
			default:
				return "", fmt.Errorf("invalid type for parameter '%s'", key)
			}
		}
	}

	// Send command
	input := &ssm.SendCommandInput{
		DocumentName: aws.String(template.DocumentName),
		InstanceIds:  instanceIDs,
		Parameters:   parameters,
		Comment:      aws.String(template.Comment),
	}

	result, err := client.SendCommand(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to send SSM command: %w", err)
	}

	if result.Command == nil || result.Command.CommandId == nil {
		return "", fmt.Errorf("command ID not returned from SSM")
	}

	return *result.Command.CommandId, nil
}

// CheckSSMAgentStatus checks if SSM agent is online for the given instances
func CheckSSMAgentStatus(ctx context.Context, cfg aws.Config, instanceIDs []string) (map[string]string, error) {
	client := ssm.NewFromConfig(cfg)

	statuses := make(map[string]string)

	// Query instance information
	input := &ssm.DescribeInstanceInformationInput{
		Filters: []types.InstanceInformationStringFilter{
			{
				Key:    aws.String("InstanceIds"),
				Values: instanceIDs,
			},
		},
	}

	result, err := client.DescribeInstanceInformation(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe instance information: %w", err)
	}

	for _, info := range result.InstanceInformationList {
		if info.InstanceId != nil {
			statuses[*info.InstanceId] = string(info.PingStatus)
		}
	}

	// Mark instances not found as "UNKNOWN"
	for _, id := range instanceIDs {
		if _, exists := statuses[id]; !exists {
			statuses[id] = "UNKNOWN"
		}
	}

	return statuses, nil
}

// PollSSMCommandStatus polls for command execution results
func PollSSMCommandStatus(
	ctx context.Context,
	cfg aws.Config,
	commandID string,
	instanceID string,
	timeout time.Duration,
) (string, string, error) {
	client := ssm.NewFromConfig(cfg)

	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		// Check context cancellation first
		select {
		case <-ctx.Done():
			return "Cancelled", "", ctx.Err()
		default:
		}

		input := &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(instanceID),
		}

		result, err := client.GetCommandInvocation(ctx, input)
		if err != nil {
			// Distinguish between transient and permanent errors
			errStr := err.Error()

			// Permanent errors - return immediately
			if strings.Contains(errStr, "InvalidCommandId") ||
				strings.Contains(errStr, "InvalidInstanceId") ||
				strings.Contains(errStr, "AccessDenied") {
				return "Failed", "", fmt.Errorf("permanent error: %w", err)
			}

			// InvocationDoesNotExist can be transient during eventual consistency
			// after SendCommand, so retry
			// Unknown errors are also retried (could be transient network issues)

			// Wait before retrying
			select {
			case <-ctx.Done():
				return "Cancelled", "", ctx.Err()
			case <-time.After(pollInterval):
			}
			continue
		}

		status := string(result.Status)

		// Check if command is finished
		if status == "Success" || status == "Failed" || status == "Cancelled" || status == "TimedOut" {
			output := ""
			if result.StandardOutputContent != nil {
				output = *result.StandardOutputContent
			}
			if result.StandardErrorContent != nil && *result.StandardErrorContent != "" {
				output += "\nSTDERR:\n" + *result.StandardErrorContent
			}
			return status, output, nil
		}

		// Still in progress (Pending, InProgress, Cancelling), wait and retry
		select {
		case <-ctx.Done():
			return "Cancelled", "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return "Timeout", "", fmt.Errorf("command polling timed out after %v", timeout)
}

// PollSSMCommandStatusMulti polls for command execution results across multiple instances
func PollSSMCommandStatusMulti(
	ctx context.Context,
	cfg aws.Config,
	commandID string,
	instanceIDs []string,
	timeout time.Duration,
) (map[string]CommandResult, error) {
	results := make(map[string]CommandResult)

	// Poll instances concurrently to avoid blocking for N * timeout seconds
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, instanceID := range instanceIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			status, output, err := PollSSMCommandStatus(ctx, cfg, commandID, id, timeout)
			mu.Lock()
			results[id] = CommandResult{
				Status: status,
				Output: output,
				Error:  err,
			}
			mu.Unlock()
		}(instanceID)
	}

	wg.Wait()
	return results, nil
}

// CommandResult holds the result of a command execution on a single instance
type CommandResult struct {
	Status string
	Output string
	Error  error
}
