package dbms

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	integrationsService "nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/relay"
	"strings"
)

// DBMSQueryTask defines a task that queries databases.
type DBMSQueryTask struct{}

// GetName returns the unique name of the task.
func (t *DBMSQueryTask) GetName() string {
	return "dbms.query"
}

// GetDescription returns a brief description of the task.
func (t *DBMSQueryTask) GetDescription() string {
	return "Run a SQL query against a connected database."
}

// GetDisplayName returns a human-readable name for the task.
func (t *DBMSQueryTask) GetDisplayName() string {
	return "Database Query"
}

// Execute runs the core logic of the task.
func (t *DBMSQueryTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing DBMS Task", "params", params)
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

	if params["dbms_type"] == nil || params["dbms_type"] == "" {
		return nil, errors.New("dbms_type is required")
	}

	var replayjob relay.RelayJob
	switch strings.ToLower(params["dbms_type"].(string)) {
	case "mysql":
		replayjob = relay.RelayJobMysql
	case "postgresql":
		replayjob = relay.RelayJobPostgres
	case "clickhouse":
		replayjob = relay.RelayJobClickhouse
	case "mssql":
		replayjob = relay.RelayJobMssql
	case "oracle":
		replayjob = relay.RelayJobOracle
	default:
		return nil, errors.New("dbms_type not supported")
	}

	requestContext := taskCtx.GetNewRequestContext()
	integrationId, err := integrationsService.ResolveIntegrationID(requestContext, params["integration_id"].(string), []string{"mysql", "postgresql", "clickhouse", "mssql", "oracle"})
	if err != nil {
		return nil, err
	}
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, replayjob, integrationId, params["command"].(string), nil)

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		return map[string]any{"data": respStr}, nil
	}

	return nil, errors.New("unable to process request")
}

// InputSchema returns the schema for the task's expected parameters.
func (t *DBMSQueryTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"integration_id": {
				Type:        types.PropertyTypeIntegration,
				Description: "DBMS Integration Id.",
				Required:    true,
			},
			"dbms_type": {
				Type:        "string",
				Description: "Database Type.",
				Options:     []string{"mysql", "postgresql", "clickhouse", "mssql", "oracle"},
				Required:    true,
			},
			"command": {
				Type:        "string",
				Description: "DBMS command.",
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
func (t *DBMSQueryTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeArray,
				Description: "DBMS Cli Response.",
				Required:    true,
			},
		},
	}
}
