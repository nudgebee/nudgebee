package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"nudgebee/runbook/services/security"
)

type NodeGracefulShutdownTask struct{}

func (t *NodeGracefulShutdownTask) GetName() string {
	return "k8s.node_graceful_shutdown"
}

func (t *NodeGracefulShutdownTask) GetDescription() string {
	return "Safely drain and remove a Kubernetes node from the cluster."
}

func (t *NodeGracefulShutdownTask) GetDisplayName() string {
	return "Node Graceful Shutdown"
}

func (t *NodeGracefulShutdownTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing NodeGracefulShutdownTask", "params", params)

	accountId := taskCtx.GetAccountID()
	if id, ok := params["account_id"].(string); ok && id != "" {
		accountId = id
	}

	name, _ := params["name"].(string)
	deleteNode, _ := params["delete_node"].(bool)
	ignorePDBs, _ := params["ignore_pdbs"].(bool)

	// disableEviction is from legacy, if true it might mean skipping drain or using specific flags?
	// For now, we will interpret "graceful shutdown" as cordon + drain.
	// If needed, we can add more granular control over drain flags.

	if name == "" {
		return nil, errors.New("node name is required")
	}

	if !k8sNameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid node name format: %s", name)
	}

	requestContext := taskCtx.GetNewRequestContext()

	// 1. Cordon the node
	cordonCmd := fmt.Sprintf("kubectl cordon %s", name)
	if _, err := t.executeKubectl(requestContext, accountId, cordonCmd); err != nil {
		return nil, fmt.Errorf("failed to cordon node '%s': %w", name, err)
	}
	taskCtx.GetLogger().Info("Node cordoned", "node", name)

	// 2. Drain the node
	// Using standard flags to ensure drain succeeds in most automation cases:
	// --ignore-daemonsets: Required because daemonsets cannot be drained.
	// --delete-emptydir-data: Required if pods have local data.
	// --force: Required if there are standalone pods not managed by a controller.
	// --timeout: Prevent hanging indefinitely (5 minutes default).
	drainCmd := fmt.Sprintf("kubectl drain %s --ignore-daemonsets --delete-emptydir-data --force --grace-period=60 --timeout=300s", name)

	if ignorePDBs {
		// Using --disable-eviction=true forces 'delete' instead of 'evict', bypassing PDB checks.
		drainCmd += " --disable-eviction=true"
		taskCtx.GetLogger().Info("ignoring PDBs by using delete instead of eviction", "node", name)
	}

	if _, err := t.executeKubectl(requestContext, accountId, drainCmd); err != nil {
		return nil, fmt.Errorf("failed to drain node '%s': %w", name, err)
	}
	taskCtx.GetLogger().Info("Node drained", "node", name)

	// 3. Delete the node (optional)
	if deleteNode {
		deleteCmd := fmt.Sprintf("kubectl delete node %s", name)
		if _, err := t.executeKubectl(requestContext, accountId, deleteCmd); err != nil {
			return nil, fmt.Errorf("failed to delete node '%s': %w", name, err)
		}
		taskCtx.GetLogger().Info("Node deleted", "node", name)
	}

	return map[string]any{
		"status":   "success",
		"node":     name,
		"cordoned": true,
		"drained":  true,
		"deleted":  deleteNode,
	}, nil
}

func (t *NodeGracefulShutdownTask) executeKubectl(ctx *security.RequestContext, accountId, command string) (map[string]any, error) {
	resp, err := relay.ExecuteRelayJob(ctx, accountId, relay.RelayJobKubectl, "", command, nil)
	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		return interpretKubectlResponse(respStr)
	}
	return nil, errors.New("unexpected response from relay")
}

func (t *NodeGracefulShutdownTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:  types.PropertyTypeAccount,
				Title: "Account",
				Order: 1,
			},
			"name": {
				Type:        types.PropertyTypeString,
				Required:    true,
				Title:       "Name",
				Description: "Name of the node to shutdown.",
				Order:       2,
				DependsOn:   []string{"account_id"},
			},
			"delete_node": {
				Type:        types.PropertyTypeBoolean,
				Default:     false,
				Title:       "Delete Node",
				Description: "Whether to delete the node object from Kubernetes after draining.",
				Order:       3,
			},
			"ignore_pdbs": {
				Type:        types.PropertyTypeBoolean,
				Default:     false,
				Title:       "Ignore PDBs",
				Description: "If true, bypasses PodDisruptionBudgets by using deletion instead of eviction.",
				Order:       4,
			},
		},
	}
}

func (t *NodeGracefulShutdownTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status":   {Type: types.PropertyTypeString},
			"node":     {Type: types.PropertyTypeString},
			"cordoned": {Type: types.PropertyTypeBoolean},
			"drained":  {Type: types.PropertyTypeBoolean},
			"deleted":  {Type: types.PropertyTypeBoolean},
		},
	}
}
