package azure

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
)

// AzureCliTask defines a task for executing Azure CLI commands.
type AzureCliTask struct{}

func (t *AzureCliTask) GetName() string {
	return "azure.cli"
}

// GetDescription returns a brief description of the task.
func (t *AzureCliTask) GetDescription() string {
	return "Run Azure CLI commands against your Azure subscription."
}

// GetDisplayName returns a human-readable name for the task.
func (t *AzureCliTask) GetDisplayName() string {
	return "Azure CLI"
}

func (t *AzureCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing AzureCliTask", "params", params)
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

func (t *AzureCliTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
				Order:       1,
			},
			"command": {
				Type:        types.PropertyTypeString,
				Description: "Azure Cli Command",
				Required:    true,
				Order:       2,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *AzureCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The output of the Azure CLI command.",
				Required:    true,
			},
		},
	}
}
