package cicd

import (
	"errors"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	integrationsService "nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/relay"
)

// ArgoCDCliTask defines a task that executes ArgoCD commands.
type ArgoCDCliTask struct{}

// GetName returns the unique name of the task.
func (t *ArgoCDCliTask) GetName() string {
	return "cicd.argocd.cli"
}

// GetDescription returns a brief description of the task.
func (t *ArgoCDCliTask) GetDescription() string {
	return "Run ArgoCD commands to manage application deployments."
}

// GetDisplayName returns a human-readable name for the task.
func (t *ArgoCDCliTask) GetDisplayName() string {
	return "ArgoCD CLI"
}

// Execute runs the core logic of the task.
func (t *ArgoCDCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing ArgoCD Task", "params", params)
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
	integrationId, err := integrationsService.ResolveIntegrationID(requestContext, params["integration_id"].(string), []string{"argocd"})
	if err != nil {
		return nil, err
	}
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobArgoCD, integrationId, params["command"].(string), nil)

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		argoResp := map[string]any{}
		if err = common.UnmarshalJson([]byte(respStr), &argoResp); err != nil {
			return nil, errors.New(respStr)
		}
		if argoResp["stderr"] != nil && argoResp["stderr"] != "" {
			return nil, errors.New(argoResp["stderr"].(string))
		}
		return map[string]any{"data": argoResp["stdout"]}, nil
	}

	return nil, errors.New("unable to process request")
}

// InputSchema returns the schema for the task's expected parameters.
func (t *ArgoCDCliTask) InputSchema() *types.Schema {
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
				Description: "ARGOCD Integration Id.",
				Required:    true,
				SubType:     "argocd",
				Order:       2,
			},
			"command": {
				Type:        types.PropertyTypeString,
				Description: "ARGOCD command.",
				Required:    true,
				Order:       3,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *ArgoCDCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "ARGO Cli Response.",
				Required:    true,
			},
		},
	}
}
