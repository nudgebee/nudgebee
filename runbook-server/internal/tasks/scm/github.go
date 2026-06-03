package scm

import (
	"errors"
	"fmt"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/integrations"
	"strconv"
	"strings"
)

// GithubCliTask defines a task that executes GitHub CLI commands.
type GithubCliTask struct{}

// GetName returns the unique name of the task.
func (t *GithubCliTask) GetName() string {
	return "scm.github.cli"
}

// GetDescription returns a brief description of the task.
func (t *GithubCliTask) GetDescription() string {
	return "Run GitHub CLI commands (issues, PRs, releases, etc.)."
}

// GetDisplayName returns a human-readable name for the task.
func (t *GithubCliTask) GetDisplayName() string {
	return "GitHub CLI"
}

// Execute runs the core logic of the task.
func (t *GithubCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Github Task", "params", params)
	if params["command"] == nil || params["command"] == "" {
		return nil, errors.New("command is required")
	}

	accountId := taskCtx.GetAccountID()
	if params["account_id"] != nil && params["account_id"] != "" {
		accountId = params["account_id"].(string)
	}

	if params["integration_id"] == nil || params["integration_id"] == "" {
		return nil, errors.New("integration_id is required")
	}

	integrationId := params["integration_id"].(string)
	command := params["command"].(string)

	// Resolve Integration details
	requestContext := taskCtx.GetNewRequestContext()
	integrationId, err := integrations.ResolveIntegrationID(requestContext, integrationId, []string{"github"})
	if err != nil {
		return nil, err
	}
	integrationConfig, err := integrations.GetIntegration(requestContext, accountId, "github", integrationId) // Call method
	if err != nil {
		return nil, fmt.Errorf("failed to get integration details: %w", err)
	}

	var authType, password, apiUrl string
	for _, v := range integrationConfig.Values {
		if v.Name == "auth_type" {
			authType = v.Value
		}
		if v.Name == "url" {
			apiUrl = v.Value
		}
		if v.Name == "password" {
			password = v.Value
		}
	}

	// For GitHub App authentication, get installation token
	githubToken := password
	if authType == "application" {
		installationID, err := strconv.ParseInt(password, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid installation_id in password field: %w", err)
		}
		if installationID == 0 {
			return nil, fmt.Errorf("installation_id is required for GitHub App authentication")
		}

		token, err := GetGithubAppInstallationToken(taskCtx.GetContext(), apiUrl, installationID)
		if err != nil {
			taskCtx.GetLogger().Error("github: unable to get installation token", "error", err.Error())
			return nil, fmt.Errorf("failed to get GitHub App installation token: %w", err)
		}
		githubToken = token
	}

	command = strings.ReplaceAll(command, "\\n", "\n")

	// Initialize Kubernetes Executor
	k8sExecutor, err := executors.NewKubernetesExecutor()
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes executor: %w", err)
	}

	namespace := config.Config.NudgebeeNamespace

	executionConfig := executors.ExecutionConfig{
		AccountID: accountId,
		Script:    command,
		Language:  "bash", // Use bash to execute the command
		Env: map[string]string{
			"GITHUB_TOKEN": githubToken,
		},
		K8sImage:     "ghcr.io/supportpal/github-gh-cli:latest",
		K8sNamespace: &namespace,
	}

	// Execute the command
	logs, err := k8sExecutor.Execute(taskCtx, executionConfig)
	if err != nil {
		taskCtx.GetLogger().Error("github: execution failed", "error", err)
		// Return logs even if there's an error, so the user can see output like "command not found"
		if logs != "" {
			return map[string]any{"data": logs}, err
		}
		return nil, err
	}

	return map[string]any{"data": logs}, nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *GithubCliTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
				Order:       1,
			},
			"integration_id": {
				Type:        types.PropertyTypeIntegration,
				Description: "Github Integration Id.",
				Required:    true,
				SubType:     "github",
				Order:       2,
			},
			"command": {
				Type:        types.PropertyTypeString,
				Description: "Github Cli command.",
				Required:    true,
				Order:       3,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *GithubCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "GitHub CLI Response.",
				Required:    true,
			},
		},
	}
}
