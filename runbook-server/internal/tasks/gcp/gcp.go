package gcp

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
)

// GCPCliTask defines a task for executing GCP CLI commands.
type GCPCliTask struct{}

func (t *GCPCliTask) GetName() string {
	return "gcp.cli"
}

// GetDescription returns a brief description of the task.
func (t *GCPCliTask) GetDescription() string {
	return "Run gcloud commands against your GCP project."
}

// GetDisplayName returns a human-readable name for the task.
func (t *GCPCliTask) GetDisplayName() string {
	return "GCP CLI"
}

func (t *GCPCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing GCPCliTask", "params", params)
	if params["command"] == nil || params["command"] == "" {
		return nil, errors.New("command is requried")
	}

	accountId := taskCtx.GetAccountID()
	if params["account_id"] != nil && params["account_id"] != "" {
		accountId = params["account_id"].(string)
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(accountId)
	resp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountId,
		Command:   params["command"].(string),
	})

	if err != nil {
		return nil, err
	}

	return map[string]any{"data": resp}, nil
}

func (t *GCPCliTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"command": {
				Type:        types.PropertyTypeString,
				Description: "GCP Cli Command",
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
func (t *GCPCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The output of the GCP CLI command.",
			},
		},
	}
}
