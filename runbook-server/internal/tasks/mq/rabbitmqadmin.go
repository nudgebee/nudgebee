package mq

import (
	"errors"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	integrationsService "nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/relay"
)

// RabbitmqadminCliTask defines a task that executes rabbitmqadmin commands.
type RabbitmqadminCliTask struct{}

// GetName returns the unique name of the task.
func (t *RabbitmqadminCliTask) GetName() string {
	return "mq.rabbitmqadmin.cli"
}

// GetDescription returns a brief description of the task.
func (t *RabbitmqadminCliTask) GetDescription() string {
	return "Manage RabbitMQ queues, exchanges, and bindings."
}

// GetDisplayName returns a human-readable name for the task.
func (t *RabbitmqadminCliTask) GetDisplayName() string {
	return "rabbitmqadmin"
}

// Execute runs the core logic of the task.
func (t *RabbitmqadminCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing rabbitmqadmin Task", "params", params)
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

	requestContext := taskCtx.GetNewRequestContext()
	integrationId, err := integrationsService.ResolveIntegrationID(requestContext, params["integration_id"].(string), []string{"rabbitmq"})
	if err != nil {
		return nil, err
	}
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobRabbitmq, integrationId, params["command"].(string), nil)

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		RabbitmqResp := map[string]any{}
		if err = common.UnmarshalJson([]byte(respStr), &RabbitmqResp); err != nil {
			return nil, errors.New(respStr)
		}
		if RabbitmqResp["stderr"] != nil && RabbitmqResp["stderr"] != "" {
			return nil, errors.New(RabbitmqResp["stderr"].(string))
		}
		return map[string]any{"data": RabbitmqResp["stdout"]}, nil
	}

	return nil, errors.New("unable to process request")
}

// InputSchema returns the schema for the task's expected parameters.
func (t *RabbitmqadminCliTask) InputSchema() *types.Schema {
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
				Description: "RabbitMQ Integration Id.",
				Required:    true,
				SubType:     "rabbitmq",
				Order:       2,
			},
			"command": {
				Type:        types.PropertyTypeString,
				Description: "rabbitmqadmin command.",
				Required:    true,
				Order:       3,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *RabbitmqadminCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "rabbitmqadmin Cli Response.",
				Required:    true,
			},
		},
	}
}
