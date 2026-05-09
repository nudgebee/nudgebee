package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var k8sNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type HorizontalRightsizeTask struct{}

func (t *HorizontalRightsizeTask) GetName() string {
	return "k8s.horizontal_rightsize"
}

func (t *HorizontalRightsizeTask) GetDescription() string {
	return "Scale the number of replicas for a Kubernetes workload up or down."
}

func (t *HorizontalRightsizeTask) GetDisplayName() string {
	return "Horizontal Rightsize"
}

func (t *HorizontalRightsizeTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing HorizontalRightsizeTask", "params", params)

	if paramsStr, err := common.MarshalJson(params); err == nil {
		taskCtx.GetLogger().Info("params", "params", paramsStr)
	}

	// 1. Extract Parameters
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

	if !isValidHorizontalRightsizeKind(kind) {
		return nil, fmt.Errorf("horizontal rightsizing is not applicable for resource kind: %s", kind)
	}

	changeBy, changeByProvided := params["change_by"].(float64)
	changeTo, changeToProvided := params["change_to"].(float64)
	minReplicas, _ := params["min"].(float64)
	maxReplicas, _ := params["max"].(float64)

	direction, _ := params["direction"].(string)
	scaleUpVal, scaleUpProvided := params["scale_up"].(bool)

	if direction == "" {
		if scaleUpProvided {
			if scaleUpVal {
				direction = "up"
			} else {
				direction = "down"
			}
		} else {
			return nil, errors.New("direction or scale_up is required")
		}
	}
	direction = strings.ToLower(direction)
	if direction != "up" && direction != "down" {
		return nil, fmt.Errorf("invalid direction '%s': must be 'up' or 'down'", direction)
	}
	scaleUp := direction == "up"

	if !changeByProvided && !changeToProvided {
		return nil, errors.New("either 'change_by' or 'change_to' must be provided")
	}
	if changeByProvided && changeToProvided {
		return nil, errors.New("cannot provide both 'change_by' and 'change_to'")
	}

	// 2. Fetch Resource
	cmd := fmt.Sprintf("kubectl get %s %s -n %s -o json", kind, name, namespace)
	requestContext := taskCtx.GetNewRequestContext()
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch resource: %w", err)
	}

	respMap, err := parseKubectlResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubectl get response: %w", err)
	}

	obj := &unstructured.Unstructured{Object: respMap}

	specReplicas, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "replicas")
	if !found || err != nil {
		return nil, errors.New("unable to find current replicas in resource spec")
	}

	var currentReplicas int64
	switch v := specReplicas.(type) {
	case int64:
		currentReplicas = v
	case float64:
		currentReplicas = int64(v)
	case int:
		currentReplicas = int64(v)
	default:
		return nil, fmt.Errorf("unexpected type for replicas: %T", v)
	}

	var newReplicas int64
	changed := false

	if changeToProvided {
		newReplicas, changed, err = t.applyAbsoluteReplica(currentReplicas, int64(changeTo), scaleUp)
	} else { // changeByProvided
		newReplicas, changed, err = t.applyRateReplica(currentReplicas, int64(changeBy), scaleUp, int64(minReplicas), int64(maxReplicas))
	}

	if err != nil {
		return nil, err // MarkFailedWithReason equivalent
	}

	description := t.generateTicketDescription(kind, name, namespace, currentReplicas, newReplicas)

	if !changed {
		return map[string]any{"status": "skipped", "reason": "no changes calculated", "description": description}, nil
	}

	// 4. Construct Patch Object
	patch := map[string]any{
		"spec": map[string]any{
			"replicas": newReplicas,
		},
	}

	// Add Traceability Annotation
	resolverType := "AutoRunbook"
	resolverID := taskCtx.GetWorkflowID()
	moduleSuffix := "workflow"
	recommendationID := ""

	if id, ok := params["recommendation_id"].(string); ok {
		resolverType = "AutoOptimize"
		moduleSuffix = "optimizer"
		resolverID = id
		recommendationID = id
	}
	if id, ok := params["recommendation_optimizer_id"].(string); ok {
		resolverID = id
	}
	if id, ok := params["recommendation_task_id"].(string); ok {
		resolverID = id
	}

	annoKey, annoVal, err := GetTraceabilityAnnotation(taskCtx, resolverType, resolverID, moduleSuffix)
	if err != nil {
		taskCtx.GetLogger().Warn("Failed to generate traceability annotation", "error", err)
	} else {
		patch["metadata"] = map[string]any{
			"annotations": map[string]string{
				annoKey: annoVal,
			},
		}
	}

	patchBytes, _ := json.Marshal(patch)
	patchStr := strings.ReplaceAll(string(patchBytes), "'", "'\\''")

	// 5. Handle GitOps or Ticket
	prData := map[string]any{
		"replicas": newReplicas,
	}

	if taskCtx.IsDryRun() {
		return map[string]any{
				"status":       "dry_run",
				"old_replicas": currentReplicas,
				"new_replicas": newReplicas,
				"patch":        patch,
				"description":  description,
			},
			nil
	}

	result, handled, err := HandleGitOpsOrTicket(taskCtx, params, kind, namespace, name, description, "HorizontalRightsize", prData, nil, resolverType, resolverID, recommendationID)
	if err != nil {
		return nil, err
	}
	if handled {
		result["patch"] = patch
		return result, nil
	}

	// 5. Apply Patch
	cmdPatch := fmt.Sprintf("kubectl patch %s %s -n %s --patch '%s' --type=merge", kind, name, namespace, patchStr)

	resp, err = relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", cmdPatch, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to apply patch: %w", err)
	}

	// Check output of patch command
	if respStr, ok := resp.(string); ok {
		kubectlResp := map[string]any{}
		if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil { // Handle unmarshal error
			return nil, fmt.Errorf("failed to parse kubectl patch response: %w", err)
		}
		if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
			return nil, fmt.Errorf("kubectl patch error: %s", stderr)
		}
	}

	description = t.generateTicketDescription(kind, name, namespace, currentReplicas, newReplicas)

	return map[string]any{
		"status":       "success",
		"old_replicas": currentReplicas,
		"new_replicas": newReplicas,
		"patch":        patch,
		"description":  description,
	}, nil
}

func (t *HorizontalRightsizeTask) generateTicketDescription(kind, name, namespace string, currentReplicas, recommendedReplicas int64) string {
	var sb strings.Builder
	sb.WriteString("## Horizontal Rightsizing Recommendation\n\n")
	sb.WriteString("### Resource Details\n")
	fmt.Fprintf(&sb, "**Type:** %s\n", kind)
	fmt.Fprintf(&sb, "**Name:** %s\n", name)
	fmt.Fprintf(&sb, "**Namespace:** %s\n\n", namespace)
	sb.WriteString("### Recommended Changes\n\n")

	change := recommendedReplicas - currentReplicas
	var action string
	if change > 0 {
		action = "Scale up"
	} else {
		action = "Scale down"
	}

	fmt.Fprintf(&sb, "**Current Replicas:** %d\n", currentReplicas)
	fmt.Fprintf(&sb, "**Recommended Replicas:** %d\n\n", recommendedReplicas)
	fmt.Fprintf(&sb, "**Action:** %s\n", action)

	return sb.String()
}

func (t *HorizontalRightsizeTask) applyAbsoluteReplica(currentReplicas, changeTo int64, scaleUp bool) (int64, bool, error) {
	if scaleUp && changeTo < currentReplicas {
		return currentReplicas, false, fmt.Errorf("task skipped: replica is already greater than %d for resource for scale up", changeTo)
	}
	if !scaleUp && changeTo > currentReplicas {
		return currentReplicas, false, fmt.Errorf("task skipped: replica is already less than %d for resource for scale down", changeTo)
	}
	if currentReplicas == changeTo {
		return currentReplicas, false, errors.New("task skipped: current and new replica count are the same")
	}
	return changeTo, true, nil
}

func (t *HorizontalRightsizeTask) applyRateReplica(currentReplicas, changeBy int64, scaleUp bool, minReplicas, maxReplicas int64) (int64, bool, error) {
	var newReplicas int64
	if scaleUp {
		newReplicas = currentReplicas + changeBy
	} else {
		newReplicas = currentReplicas - changeBy
	}

	if maxReplicas > 0 && newReplicas > maxReplicas {
		return currentReplicas, false, fmt.Errorf("task skipped: replica cannot go above max replica of %d", maxReplicas)
	}
	if minReplicas > 0 && newReplicas < minReplicas {
		return currentReplicas, false, fmt.Errorf("task skipped: replica cannot go below min replica of %d", minReplicas)
	}

	if newReplicas < 0 { // Replicas cannot be negative
		newReplicas = 0
	}
	if currentReplicas == newReplicas {
		return currentReplicas, false, errors.New("task skipped: current and new replica count are the same after calculation")
	}

	return newReplicas, true, nil
}

func isValidHorizontalRightsizeKind(kind string) bool {
	switch strings.ToLower(kind) {
	case "deployment", "statefulset", "replicaset", "rollout": // Rollout is Argo Rollouts specific
		return true
	default:
		return false
	}
}

func parseKubectlResponse(resp any) (map[string]any, error) {
	respMap := make(map[string]any)
	if r, ok := resp.(map[string]any); ok {
		respMap = r
	} else if respStr, ok := resp.(string); ok {
		kubectlResp := map[string]any{}
		if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil {
			return nil, fmt.Errorf("failed to parse relay string response: %w", err)
		}
		if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
			return nil, fmt.Errorf("kubectl error: %s", stderr)
		}
		if stdout, ok := kubectlResp["stdout"].(string); ok {
			if err := common.UnmarshalJson([]byte(stdout), &respMap); err != nil {
				return nil, fmt.Errorf("failed to parse resource JSON from stdout: %w", err)
			}
		} else {
			return nil, errors.New("no stdout from kubectl response")
		}
	} else {
		return nil, errors.New("unexpected response format from relay")
	}
	return respMap, nil
}

func (t *HorizontalRightsizeTask) InputSchema() *types.Schema {
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
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Kind",
				Options:   []string{"Deployment", "StatefulSet", "ReplicaSet", "Rollout"},
				Order:     3,
				DependsOn: []string{"account_id"},
			},
			"name": {
				Type:      types.PropertyTypeString,
				Required:  true,
				Title:     "Name",
				Order:     4,
				DependsOn: []string{"account_id", "namespace", "kind"},
			},
			"direction": {
				Type:        types.PropertyTypeString,
				Title:       "Direction",
				Description: "Direction of scaling ('up' or 'down').",
				Required:    true,
				Options:     []string{"up", "down"},
				Order:       5,
			},
			"scaling_mode": {
				Type:        types.PropertyTypeString,
				Title:       "Scaling Mode",
				Description: "Choose how to scale: by a relative amount or to an absolute count.",
				Options:     []string{"change_by", "change_to"},
				Order:       6,
			},
			"change_by": {
				Type:        types.PropertyTypeNumber,
				Title:       "Change By",
				Description: "Amount to change replicas by (absolute number)",
				Order:       7,
				VisibleWhen: &types.VisibleWhen{Field: "scaling_mode", Value: []string{"change_by"}},
				DependsOn:   []string{"scaling_mode"},
			},
			"change_to": {
				Type:        types.PropertyTypeNumber,
				Title:       "Change To",
				Description: "Target absolute replica count",
				Order:       8,
				VisibleWhen: &types.VisibleWhen{Field: "scaling_mode", Value: []string{"change_to"}},
				DependsOn:   []string{"scaling_mode"},
			},
			"min": {
				Type:        types.PropertyTypeNumber,
				Title:       "Min",
				Description: "Minimum replica count",
				Order:       9,
			},
			"max": {
				Type:        types.PropertyTypeNumber,
				Title:       "Max",
				Description: "Maximum replica count",
				Order:       10,
			},
			"gitops_config": {
				Type:        types.PropertyTypeObject,
				Title:       "GitOps",
				Description: "Configuration for GitOps integration (e.g., Pull Request creation).",
				Order:       11,
				Schema: &types.Schema{
					Properties: map[string]types.Property{
						"enabled": {
							Type:        types.PropertyTypeBoolean,
							Title:       "Enabled",
							Description: "If true, creates a Pull Request with changes instead of applying them directly.",
							Default:     false,
							Order:       1,
						},
						"integration_id": {
							Type:         types.PropertyTypeTicket,
							Title:        "GitHub Config",
							SubType:      "github",
							Description:  "GitHub integration used to raise the Pull Request.",
							Order:        2,
							VisibleWhen:  &types.VisibleWhen{Field: "enabled", Value: []string{"true"}},
							RequiredWhen: &types.RequiredWhen{Field: "enabled", Value: []string{"true"}},
							DependsOn:    []string{"enabled"},
						},
					},
				},
			},
		},
	}
}

func (t *HorizontalRightsizeTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status":        {Type: types.PropertyTypeString},
			"old_replicas":  {Type: types.PropertyTypeNumber},
			"new_replicas":  {Type: types.PropertyTypeNumber},
			"patch":         {Type: types.PropertyTypeObject},
			"resolution_id": {Type: types.PropertyTypeString},
		},
	}
}
