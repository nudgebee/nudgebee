package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type WorkloadRestartTask struct{}

func (t *WorkloadRestartTask) GetName() string {
	return "k8s.workload_restart"
}

func (t *WorkloadRestartTask) GetDescription() string {
	return "Rolling restart a Kubernetes workload to apply changes or recover."
}

func (t *WorkloadRestartTask) GetDisplayName() string {
	return "Workload Restart"
}

func (t *WorkloadRestartTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing WorkloadRestartTask", "params", params)

	accountId := taskCtx.GetAccountID()
	if id, ok := params["account_id"].(string); ok && id != "" {
		accountId = id
	}

	namespace, _ := params["namespace"].(string)
	name, _ := params["name"].(string)
	kind, _ := params["kind"].(string)

	if namespace == "" || name == "" || kind == "" {
		return nil, errors.New("namespace, name, and kind are required")
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

	requestContext := taskCtx.GetNewRequestContext()
	targetName := name
	targetKind := kind

	// Logic to find owner if kind is Pod
	if strings.ToLower(kind) == "pod" {
		taskCtx.GetLogger().Info("Input is a Pod, resolving owner workload", "pod", name)

		getPodCmd := fmt.Sprintf("kubectl get pod %s -n %s -o json", name, namespace)
		podResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", getPodCmd, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch pod '%s/%s': %w", namespace, name, err)
		}
		podMap, err := parseKubectlResponse(podResp)
		if err != nil {
			return nil, fmt.Errorf("failed to parse pod response: %w", err)
		}
		podObj := &unstructured.Unstructured{Object: podMap}

		ownerRefs := podObj.GetOwnerReferences()
		if len(ownerRefs) == 0 {
			return nil, fmt.Errorf("pod '%s/%s' has no owner references, cannot restart workload", namespace, name)
		}

		// Take the first controller owner
		var ownerKind, ownerName string
		for _, ref := range ownerRefs {
			if ref.Controller != nil && *ref.Controller {
				ownerKind = ref.Kind
				ownerName = ref.Name
				break
			}
		}
		if ownerKind == "" {
			// Fallback to first owner if no controller is marked
			ownerKind = ownerRefs[0].Kind
			ownerName = ownerRefs[0].Name
		}

		// Sanitize derived owner values
		if !k8sNameRegex.MatchString(ownerName) {
			return nil, fmt.Errorf("invalid owner name format from API: %s", ownerName)
		}
		if !k8sNameRegex.MatchString(strings.ToLower(ownerKind)) {
			return nil, fmt.Errorf("invalid owner kind format from API: %s", ownerKind)
		}

		// Handle ReplicaSet ownership (common for Deployments)
		if strings.ToLower(ownerKind) == "replicaset" {
			taskCtx.GetLogger().Info("Pod is owned by ReplicaSet, resolving Deployment", "replicaset", ownerName)
			getRsCmd := fmt.Sprintf("kubectl get replicaset %s -n %s -o json", ownerName, namespace)
			rsResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", getRsCmd, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch replicaset '%s/%s': %w", namespace, ownerName, err)
			}
			rsMap, err := parseKubectlResponse(rsResp)
			if err != nil {
				return nil, fmt.Errorf("failed to parse replicaset response: %w", err)
			}
			rsObj := &unstructured.Unstructured{Object: rsMap}

			rsOwnerRefs := rsObj.GetOwnerReferences()
			if len(rsOwnerRefs) > 0 {
				// Assume the first owner of RS is the Deployment
				ownerKind = rsOwnerRefs[0].Kind
				ownerName = rsOwnerRefs[0].Name
			} else {
				// Standalone ReplicaSet?
				taskCtx.GetLogger().Warn("ReplicaSet has no owner, restarting ReplicaSet directly", "replicaset", ownerName)
			}
		}

		targetKind = ownerKind
		targetName = ownerName
		taskCtx.GetLogger().Info("Resolved owner workload", "kind", targetKind, "name", targetName)
	}

	// Validate supported kinds for rollout restart
	validKinds := map[string]bool{
		"deployment":  true,
		"statefulset": true,
		"daemonset":   true,
		"rollout":     true, // Argo Rollouts
	}
	if !validKinds[strings.ToLower(targetKind)] {
		return nil, fmt.Errorf("workload type '%s' is not supported for restart (must be Deployment, StatefulSet, DaemonSet, or Rollout)", targetKind)
	}

	// Execute rollout restart
	// Argo Rollouts uses 'kubectl argo rollouts restart' or just 'kubectl rollout restart' depending on setup.
	// Standard 'kubectl rollout restart' works for Deployments, DS, STS.
	// Inputs are validated against regex to prevent injection.
	restartCmd := fmt.Sprintf("kubectl rollout restart %s %s -n %s", targetKind, targetName, namespace)

	restartResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", restartCmd, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to restart workload '%s/%s/%s': %w", namespace, targetKind, targetName, err)
	}

	// Check output
	if respStr, ok := restartResp.(string); ok {
		kubectlResp := map[string]any{}
		if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil { // Handle unmarshal error
			return nil, fmt.Errorf("failed to parse kubectl restart response: %w", err)
		}
		if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
			return nil, fmt.Errorf("kubectl restart error: %s", stderr)
		}
	}

	return map[string]any{
		"status":         "success",
		"message":        fmt.Sprintf("Restarted %s/%s", targetKind, targetName),
		"restarted_kind": targetKind,
		"restarted_name": targetName,
	}, nil
}

func (t *WorkloadRestartTask) InputSchema() *types.Schema {
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
				Required:    true,
				Description: "Kind of the resource (Deployment, Pod, etc.).",
				Title:       "Kind",
				Order:       3,
				Options:     []string{"Pod", "Deployment", "DaemonSet", "StatefulSet", "ReplicaSet"},
				DependsOn:   []string{"account_id", "namespace"},
			},
			"name": {
				Type:        types.PropertyTypeString,
				Required:    true,
				Description: "Name of the workload or pod.",
				Title:       "Name",
				Order:       4,
				DependsOn:   []string{"account_id", "namespace", "kind"},
			},
		},
	}
}

func (t *WorkloadRestartTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status":         {Type: types.PropertyTypeString},
			"message":        {Type: types.PropertyTypeString},
			"restarted_kind": {Type: types.PropertyTypeString},
			"restarted_name": {Type: types.PropertyTypeString},
		},
	}
}
