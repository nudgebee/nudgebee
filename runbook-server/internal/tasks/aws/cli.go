package aws

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
)

// AWSCliTask defines a task for executing AWS CLI commands.
type AWSCliTask struct{}

func (t *AWSCliTask) GetName() string {
	return "aws.cli"
}

// GetDescription returns a brief description of the task.
func (t *AWSCliTask) GetDescription() string {
	return "Run AWS CLI commands against your AWS account."
}

// GetDisplayName returns a human-readable name for the task.
func (t *AWSCliTask) GetDisplayName() string {
	return "AWS CLI"
}

func (t *AWSCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing AWSCliTask", "params", params)
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

func (t *AWSCliTask) InputSchema() *types.Schema {
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
				Description: "AWS Cli Command",
				Required:    true,
				Order:       2,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *AWSCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The output of the AWS CLI command.",
				Required:    true,
			},
		},
	}
}
