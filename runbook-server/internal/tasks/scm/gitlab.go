package scm

import (
	"errors"
	"fmt"
	"net/url"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/integrations"
	"strings"
)

// GitlabCliTask defines a task that executes GitLab CLI commands.
type GitlabCliTask struct{}

// GetName returns the unique name of the task.
func (t *GitlabCliTask) GetName() string {
	return "scm.gitlab.cli"
}

// GetDescription returns a brief description of the task.
func (t *GitlabCliTask) GetDescription() string {
	return "Run GitLab CLI commands (issues, MRs, pipelines, etc.)."
}

// GetDisplayName returns a human-readable name for the task.
func (t *GitlabCliTask) GetDisplayName() string {
	return "GitLab CLI"
}

// Execute runs the core logic of the task.
func (t *GitlabCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing GitLab Task", "params", params)

	command, ok := params["command"].(string)
	if !ok || command == "" {
		return nil, errors.New("command is required")
	}

	accountId := taskCtx.GetAccountID()
	if val, ok := params["account_id"].(string); ok && val != "" {
		accountId = val
	}

	integrationId, ok := params["integration_id"].(string)
	if !ok || integrationId == "" {
		return nil, errors.New("integration_id is required")
	}

	// Resolve Integration details
	requestContext := taskCtx.GetNewRequestContext()
	integrationId, err := integrations.ResolveIntegrationID(requestContext, integrationId, []string{"gitlab"})
	if err != nil {
		return nil, err
	}
	integrationConfig, err := integrations.GetIntegration(requestContext, accountId, "gitlab", integrationId)
	if err != nil {
		return nil, fmt.Errorf("failed to get integration details: %w", err)
	}

	var password, apiUrl string
	for _, v := range integrationConfig.Values {
		if v.Name == "url" {
			apiUrl = v.Value
		}
		if v.Name == "password" {
			password = v.Value
		}
	}

	if password == "" {
		return nil, errors.New("gitlab personal access token is required")
	}

	// Build env vars for glab CLI authentication
	env := map[string]string{
		"GITLAB_TOKEN": password,
	}

	// For self-hosted GitLab instances, set GITLAB_HOST to the hostname.
	// glab uses GITLAB_HOST (not GLAB_HOST) to target non-gitlab.com instances.
	if apiUrl != "" {
		parsed, err := url.Parse(apiUrl)
		if err == nil && parsed.Host != "" && !strings.EqualFold(parsed.Hostname(), "gitlab.com") {
			// parsed.Host includes port if present (e.g., "gitlab.mycompany.com:8080")
			env["GITLAB_HOST"] = parsed.Host
		}
	}

	command = strings.ReplaceAll(command, "\\n", "\n")

	// Initialize Kubernetes Executor
	k8sExecutor, err := executors.NewKubernetesExecutor()
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes executor: %w", err)
	}

	namespace := config.Config.NudgebeeNamespace

	executionConfig := executors.ExecutionConfig{
		AccountID:    accountId,
		Script:       command,
		Language:     "sh",
		Env:          env,
		K8sImage:     "registry.gitlab.com/gitlab-org/cli:v1.90.0",
		K8sNamespace: &namespace,
	}

	// Execute the command
	logs, err := k8sExecutor.Execute(taskCtx, executionConfig)
	if err != nil {
		taskCtx.GetLogger().Error("gitlab: execution failed", "error", err)
		// Return logs even if there's an error, so the user can see output
		if logs != "" {
			return map[string]any{"data": logs}, err
		}
		return nil, err
	}

	return map[string]any{"data": logs}, nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *GitlabCliTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"integration_id": {
				Type:        types.PropertyTypeIntegration,
				Description: "GitLab Integration Id.",
				Required:    true,
				SubType:     "gitlab",
			},
			"command": {
				Type:        "string",
				Description: "GitLab CLI command.",
				Required:    true,
			},
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *GitlabCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "GitLab CLI Response.",
				Required:    true,
			},
		},
	}
}
