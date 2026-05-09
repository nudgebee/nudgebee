package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

	// Define requestContext once at the beginning of the function
	requestContext := taskCtx.GetNewRequestContext()

	var podToDelete string

	if strings.ToLower(kind) == "pod" {
		// When kind is Pod, target_pod_name (if provided) identifies the pod
		// to delete; otherwise fall back to name. This lets callers that also
		// send target_pod_name for workload forms keep it working when kind
		// is Pod (e.g. form auto-fill from a workload+pod selection).
		if targetPodName != "" {
			if targetPodName != name {
				taskCtx.GetLogger().Debug("target_pod_name provided with kind=Pod, using target_pod_name", "name", name, "target_pod_name", targetPodName)
			}
			podToDelete = targetPodName
		} else {
			podToDelete = name
		}
	} else {
		// It's a workload (Deployment, ReplicaSet, DaemonSet, Rollout)
		// First, get the workload to extract labels
		getWorkloadCmd := fmt.Sprintf("kubectl get %s %s -n %s -o json", kind, name, namespace)
		workloadResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", getWorkloadCmd, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch workload '%s/%s': %w", namespace, name, err)
		}
		workloadMap, err := parseKubectlResponse(workloadResp)
		if err != nil {
			return nil, fmt.Errorf("failed to parse workload response: %w", err)
		}
		workloadObj := &unstructured.Unstructured{Object: workloadMap}

		labels, found, err := unstructured.NestedStringMap(workloadObj.Object, "spec", "selector", "matchLabels")
		if !found || err != nil || len(labels) == 0 {
			return nil, fmt.Errorf("could not find selector labels for workload '%s/%s'", namespace, name)
		}

		labelSelector := make([]string, 0, len(labels))
		for k, v := range labels {
			labelSelector = append(labelSelector, fmt.Sprintf("%s=%s", k, v))
		}
		labelSelectorStr := strings.Join(labelSelector, ",")

		// Get pods for the workload
		getPodsCmd := fmt.Sprintf("kubectl get pods -l %s -n %s -o json", labelSelectorStr, namespace)
		podsResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", getPodsCmd, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch pods for workload '%s/%s': %w", namespace, name, err)
		}
		podsMap, err := parseKubectlResponse(podsResp)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pods response: %w", err)
		}
		podsObj := &unstructured.Unstructured{Object: podsMap}

		items, found, err := unstructured.NestedSlice(podsObj.Object, "items")
		if !found || err != nil || len(items) == 0 {
			return nil, fmt.Errorf("no pods found for workload '%s/%s'", namespace, name)
		}

		// Implement deterministic selection: Sort pods by creationTimestamp and then by name
		sort.Slice(items, func(i, j int) bool {
			podI, okI := items[i].(map[string]any)
			podJ, okJ := items[j].(map[string]any)
			if !okI || !okJ {
				return false // Cannot compare, maintain original order
			}

			// Sort by creationTimestamp (ascending)
			tsI, _, _ := unstructured.NestedString(podI, "metadata", "creationTimestamp")
			tsJ, _, _ := unstructured.NestedString(podJ, "metadata", "creationTimestamp")
			if tsI != tsJ {
				return tsI < tsJ
			}

			// Tie-breaker: sort by name (ascending)
			nameI, _, _ := unstructured.NestedString(podI, "metadata", "name")
			nameJ, _, _ := unstructured.NestedString(podJ, "metadata", "name")
			return nameI < nameJ
		})

		if targetPodName != "" {
			foundTarget := false
			for _, item := range items {
				pod, ok := item.(map[string]any)
				if !ok {
					continue
				}
				pName, _, _ := unstructured.NestedString(pod, "metadata", "name")
				if pName == targetPodName {
					podToDelete = targetPodName
					foundTarget = true
					break
				}
			}
			if !foundTarget {
				return nil, fmt.Errorf("specified target pod '%s' not found for workload '%s/%s'", targetPodName, namespace, name)
			}
		} else {
			// Default to deleting the first pod found after deterministic sorting
			firstPod, ok := items[0].(map[string]any)
			if !ok {
				return nil, errors.New("failed to parse first pod in list after sorting")
			}
			podToDelete, _, _ = unstructured.NestedString(firstPod, "metadata", "name")
			if podToDelete == "" {
				return nil, errors.New("could not determine pod name to delete after sorting")
			}
			taskCtx.GetLogger().Info("No specific target_pod_name provided, defaulting to deterministically selected pod", "pod", podToDelete)
		}
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
		kubectlResp := map[string]any{}
		if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil { // Handle unmarshal error
			return nil, fmt.Errorf("failed to parse kubectl delete response: %w", err)
		}
		if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
			// kubectl delete --force emits "Warning: Immediate deletion..." to
			// stderr on success, so we can't treat any stderr as failure.
			// Detect real errors by kubectl's known prefixes:
			//   "error:"            - client-side errors
			//   "error from server" - Kubernetes API errors (NotFound, Forbidden, ...)
			//   "fail"              - generic failure messages
			stderrLower := strings.ToLower(stderr)
			if strings.Contains(stderrLower, "error:") ||
				strings.Contains(stderrLower, "error from server") ||
				strings.Contains(stderrLower, "fail") {
				return nil, fmt.Errorf("kubectl delete error: %s", stderr)
			}
			// Log warnings
			taskCtx.GetLogger().Warn("kubectl delete stderr output", "stderr", stderr)
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
