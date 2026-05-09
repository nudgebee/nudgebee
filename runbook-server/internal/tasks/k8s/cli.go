package k8s

import (
	"errors"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"strings"
)

// K8sCliTask defines a task for executing kubectl commands.
type K8sCliTask struct{}

func (t *K8sCliTask) GetName() string {
	return "k8s.cli"
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

	command := strings.TrimSpace(params["command"].(string))
	if command != "kubectl" && !strings.HasPrefix(command, "kubectl ") {
		command = "kubectl " + command
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", command, nil)

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		kubectlResp := map[string]any{}
		if err = common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil {
			return nil, errors.New(respStr)
		}

		if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
			// If stderr indicates an error, return it as a task error.
			if strings.Contains(strings.ToLower(stderr), "error:") || strings.Contains(strings.ToLower(stderr), "fail") {
				return nil, errors.New(stderr)
			}
		}

		// Otherwise, return stdout and any non-error stderr as part of the data.
		output := map[string]any{
			"data": kubectlResp["stdout"],
		}
		if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
			output["stderr"] = stderr
		}

		return output, nil

	}

	return nil, errors.New("unable to process request")
}

func (t *K8sCliTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Title:       "Account",
				Description: "NB Account Id",
				Required:    false,
				Order:       1,
			},
			"command": {
				Type:        types.PropertyTypeString,
				Title:       "Command",
				Description: "Kubectl Command",
				Required:    true,
				Order:       2,
				// Intentionally no DependsOn: the kubectl command is free-text
				// and has no semantic dependency on which account it targets,
				// so changing the account (typing a new template, picking a
				// different option, switching modes) must NOT wipe what the
				// user has typed. depends_on is reserved for fields whose
				// option list / validity changes with the parent — like
				// namespace → kind → name on workload tasks.
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
				Description: "The output (stdout) of the K8s CLI command.",
				Required:    true,
			},
			"stderr": {
				Type:        types.PropertyTypeString,
				Description: "The error output (stderr) of the K8s CLI command, if any.",
			},
		},
	}
}
