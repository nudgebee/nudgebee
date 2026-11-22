package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"strings"
)

type PodDeleteTask struct{}

func (t *PodDeleteTask) GetName() string {
	return "k8s.pod_delete"
}

func (t *PodDeleteTask) GetDescription() string {
	return "Delete a Kubernetes pod (e.g., to force a restart)."
}

func (t *PodDeleteTask) GetDisplayName() string {
	return "Pod Delete"
}

func (t *PodDeleteTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing PodDeleteTask", "params", params)

	accountId := taskCtx.GetAccountID()
	if id, ok := params["account_id"].(string); ok && id != "" {
		accountId = id
	}

	namespace, _ := params["namespace"].(string)
	name, _ := params["name"].(string) // Name of the workload or pod
	kind, _ := params["kind"].(string)
	if kind == "" {
		kind = "Pod"
	}
	targetPodName, _ := params["target_pod_name"].(string) // Optional: specific pod name to delete

	if namespace == "" || name == "" {
		return nil, errors.New("namespace and name are required")
	}

	if !k8sNameRegex.MatchString(namespace) {
		return nil, fmt.Errorf("invalid namespace format: %s", namespace)
	}
	if !k8sNameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid name format: %s", name)
	}
	if !k8sNameRegex.MatchString(strings.ToLower(kind)) {
		return nil, fmt.Errorf("invalid kind format: %s", kind)
	}
	if targetPodName != "" && !k8sNameRegex.MatchString(targetPodName) {
		return nil, fmt.Errorf("invalid target_pod_name format: %s", targetPodName)
	}

	if strings.ToLower(kind) == "statefulset" {
		return nil, errors.New("deleting pods from StatefulSets directly is not recommended; consider scaling down or rolling update instead")
	}

	requestContext := taskCtx.GetNewRequestContext()

	podToDelete, err := ResolveTargetPod(taskCtx, accountId, namespace, kind, name, targetPodName)
	if err != nil {
		return nil, err
	}

	if podToDelete == "" {
		return nil, errors.New("no pod determined for deletion")
	}

	// Execute deletion
	force, _ := params["force"].(bool)
	deleteCmd := fmt.Sprintf("kubectl delete pod %s -n %s", podToDelete, namespace)
	if force {
		deleteCmd += " --grace-period=0 --force"
	}

	deleteResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", deleteCmd, nil)
	taskCtx.GetLogger().Debug("Relay response for delete pod", "resp", deleteResp, "err", err)
	if err != nil {
		return nil, fmt.Errorf("failed to delete pod '%s/%s': %w", namespace, podToDelete, err)
	}

	// Parse deletion response for errors
	if respStr, ok := deleteResp.(string); ok {
		if _, err := interpretKubectlResponse(respStr); err != nil {
			return nil, fmt.Errorf("kubectl delete error: %w", err)
		}
	}

	return map[string]any{"status": "success", "deleted_pod": podToDelete, "namespace": namespace}, nil
}

func (t *PodDeleteTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:  types.PropertyTypeAccount,
				Title: "Account",
				Order: 1,
			},
			"namespace": {
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Namespace",
				Order:     2,
				DependsOn: []string{"account_id"},
			},
			"kind": {
				Type:        types.PropertyTypeString,
				Required:    false,
				Default:     "Pod",
				Description: "Kind of the resource (Deployment, DaemonSet, Pod, etc.). Defaults to 'Pod'.",
				Title:       "Kind",
				Order:       3,
				Options:     []string{"Pod", "Deployment", "DaemonSet", "StatefulSet", "ReplicaSet", "Job", "CronJob"},
				DependsOn:   []string{"account_id", "namespace"},
			},
			"name": {
				Type:        types.PropertyTypeString,
				Required:    true,
				Description: "Name of the workload (Deployment, DaemonSet, etc.) or the specific Pod.",
				Title:       "Name",
				Order:       4,
				DependsOn:   []string{"account_id", "namespace", "kind"},
			},
			"target_pod_name": {
				Type:        types.PropertyTypeString,
				Required:    false,
				Description: "Optional: Specific pod name to delete if 'kind' is a workload. If not provided, the first found pod will be deleted.",
				Title:       "Target Pod Name",
				Order:       5,
				DependsOn:   []string{"account_id", "namespace", "kind", "name"},
			},
			"force": {
				Type:        types.PropertyTypeBoolean,
				Description: "If true, immediately removes the pod (grace-period=0 --force). Useful for stuck pods.",
				Title:       "Force Delete",
				Order:       6,
			},
		},
	}
}

func (t *PodDeleteTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status":      {Type: types.PropertyTypeString},
			"deleted_pod": {Type: types.PropertyTypeString},
			"namespace":   {Type: types.PropertyTypeString},
		},
	}
}
