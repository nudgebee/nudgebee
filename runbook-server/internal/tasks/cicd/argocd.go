package cicd

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	integrationsService "nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/relay"
	"regexp"
)

// ArgoCDCliTask defines a task that executes ArgoCD commands.
type ArgoCDCliTask struct{}

// GetName returns the unique name of the task.
func (t *ArgoCDCliTask) GetName() string {
	return "cicd.argocd.cli"
}

// GetDescription returns a brief description of the task.
func (t *ArgoCDCliTask) GetDescription() string {
	return "Run ArgoCD commands to manage application deployments. A non-zero exit fails the step, except argocd app diff, which exits 1 when the app has drifted (output.exit_code = 1)."
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
		return relay.InterpretCommandResponse("argocd", respStr, argoCDOKExitCodes(params["command"].(string))...)
	}

	return nil, errors.New("unable to process request")
}

var argoAppDiff = regexp.MustCompile(`(^|\s)app\s+diff(\s|$)`)

// argoCDOKExitCodes lists exit codes that are a result for this command:
// `argocd app diff` exits 1 when the app has drifted from git.
func argoCDOKExitCodes(command string) []int {
	if argoAppDiff.MatchString(command) {
		return []int{1}
	}
	return nil
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
			"exit_code": {
				Type:        "integer",
				Description: "Exit code of the argocd command, when the agent reports it.",
				Required:    false,
			},
		},
	}
}
