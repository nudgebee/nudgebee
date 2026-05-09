package cloud

import (
	"errors"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
)

// K8sCliTask defines a task for executing kubectl commands.
type K8sCliTask struct{}

func (t *K8sCliTask) GetName() string {
	return "cloud.k8s.cli"
}

// GetDescription returns a brief description of the task.
func (t *K8sCliTask) GetDescription() string {
	return "Run kubectl commands against your Kubernetes cluster."
}

// GetDisplayName returns a human-readable name for the task.
func (t *K8sCliTask) GetDisplayName() string {
	return "Kubectl"
}

func (t *K8sCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing K8sCliTask", "params", params)
	if params["command"] == nil || params["command"] == "" {
		return nil, errors.New("command is requried")
	}

	accountId := taskCtx.GetAccountID()
	if params["account_id"] != nil && params["account_id"] != "" {
		accountId = params["account_id"].(string)
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", params["command"].(string), nil)

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		kubectlResp := map[string]any{}
		if err = common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil {
			return nil, errors.New(respStr)
		}
		if kubectlResp["stderr"] != nil && kubectlResp["stderr"] != "" {
			return nil, errors.New(kubectlResp["stderr"].(string))
		}

		return map[string]any{"data": kubectlResp["stdout"]}, nil

	}

	return nil, errors.New("unable to process request")
}

func (t *K8sCliTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"command": {
				Type:        types.PropertyTypeString,
				Description: "Kubectl Command",
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
func (t *K8sCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The output of the K8s CLI command.",
				Required:    true,
			},
		},
	}
}
