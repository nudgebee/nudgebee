package integrations

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	integrationsService "nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/relay"
)

// SSHTask implements the Task interface for making HTTP requests.
type SSHTask struct{}

func (t *SSHTask) GetName() string {
	return "integrations.ssh"
}

// GetDescription returns a brief description of the task.
func (t *SSHTask) GetDescription() string {
	return "Run commands on a remote server via SSH."
}

// GetDisplayName returns a human-readable name for the task.
func (t *SSHTask) GetDisplayName() string {
	return "SSH Request"
}

// Execute runs the core logic of the task.
func (t *SSHTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing SSH Task", "params", params)
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
	integrationId, err := integrationsService.ResolveIntegrationID(requestContext, params["integration_id"].(string), []string{"ssh"})
	if err != nil {
		return nil, err
	}
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobSSH, integrationId, params["command"].(string), sshTargetConfigs(params))

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		return relay.InterpretCommandResponse("ssh", respStr)
	}

	return nil, errors.New("unable to process request")
}

// sshTargetConfigs extracts the optional per-task target host/port. They are
// honoured on the vm_agent (forager ssh-proxy) path, where they override the
// integration's saved host; a dynamic-mode proxy requires one of the two.
func sshTargetConfigs(params map[string]any) map[string]any {
	configs := map[string]any{}
	if host, ok := params["host"].(string); ok && host != "" {
		configs["host"] = host
	}
	if port, ok := params["port"]; ok && port != nil && port != "" {
		configs["port"] = port
	}
	return configs
}

func (t *SSHTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"integration_id": {
				Type:        types.PropertyTypeIntegration,
				Description: "SSH Integration Id.",
				Required:    true,
				SubType:     "ssh",
			},
			"command": {
				Type:        "string",
				Description: "SSH command.",
				Required:    true,
			},
			"host": {
				Type:        "string",
				Description: "Target host (hostname or IPv4). Overrides the integration's default host; required for agent SSH integrations without a fixed host.",
				Required:    false,
			},
			"port": {
				Type:        "integer",
				Description: "Target SSH port. Defaults to the integration's port (22 for k8s-mode integrations).",
				Required:    false,
			},
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
			},
		},
	}
}

func (t *SSHTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "SSH Cli Response.",
				Required:    true,
			},
		},
	}
}
