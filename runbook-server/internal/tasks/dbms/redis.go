package dbms

import (
	"errors"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	integrationsService "nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/relay"
)

// RedisCliTask defines a task that executes Redis commands.
type RedisCliTask struct{}

// GetName returns the unique name of the task.
func (t *RedisCliTask) GetName() string {
	return "dbms.redis.cli"
}

// GetDescription returns a brief description of the task.
func (t *RedisCliTask) GetDescription() string {
	return "Run commands against a Redis instance."
}

// GetDisplayName returns a human-readable name for the task.
func (t *RedisCliTask) GetDisplayName() string {
	return "Redis CLI"
}

// Execute runs the core logic of the task.
func (t *RedisCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Redis Task", "params", params)
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
	integrationId, err := integrationsService.ResolveIntegrationID(requestContext, params["integration_id"].(string), []string{"redis"})
	if err != nil {
		return nil, err
	}
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobRedis, integrationId, params["command"].(string), nil)

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		redisResp := map[string]any{}
		if err = common.UnmarshalJson([]byte(respStr), &redisResp); err != nil {
			return nil, errors.New(respStr)
		}
		if redisResp["stderr"] != nil && redisResp["stderr"] != "" {
			return nil, errors.New(redisResp["stderr"].(string))
		}
		return map[string]any{"data": redisResp["stdout"]}, nil
	}

	return nil, errors.New("unable to process request")
}

// InputSchema returns the schema for the task's expected parameters.
func (t *RedisCliTask) InputSchema() *types.Schema {
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
				Description: "Redis Integration Id.",
				Required:    true,
				SubType:     "redis",
				Order:       2,
			},
			"command": {
				Type:        types.PropertyTypeString,
				Description: "Redis command.",
				Required:    true,
				Order:       3,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *RedisCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "Redis Cli Response.",
				Required:    true,
			},
		},
	}
}
