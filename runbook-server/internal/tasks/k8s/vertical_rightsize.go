package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"strings" // Added strings

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type VerticalRightsizeTask struct{}

func (t *VerticalRightsizeTask) GetName() string {
	return "k8s.vertical_rightsize"
}

func (t *VerticalRightsizeTask) GetDescription() string {
	return "Optimize CPU and memory requests/limits for a Kubernetes workload."
}

func (t *VerticalRightsizeTask) GetDisplayName() string {
	return "Vertical Rightsize"
}

func (t *VerticalRightsizeTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing VerticalRightsizeTask", "params", params)

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
	if !k8sNameRegex.MatchString(strings.ToLower(kind)) {
		return nil, fmt.Errorf("invalid kind format: %s", kind)
	}

	// 2. Fetch Resource
	cmd := fmt.Sprintf("kubectl get %s %s -n %s -o json", kind, name, namespace)
	requestContext := taskCtx.GetNewRequestContext()
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch resource: %w", err)
	}

	respMap, ok := resp.(map[string]any)
	if !ok {
		// Try parsing if it's a string (generic relay output sometimes wraps it)
		if respStr, ok := resp.(string); ok {
			// In K8sCliTask it unmarshals. But ExecuteRelayJob returns map[string]any usually?
			// K8sCliTask: "if respStr, ok := resp.(string); ok {" -> unmarshals to map -> checks stderr.
			// Let's rely on common pattern.
			// If ExecuteRelayJob returns the parsed JSON from kubectl, it should be a map.
			// If it returns a string wrapped response, handle that.
			// Looking at K8sCliTask again...
			// It seems ExecuteRelayJob returns 'any', which could be string (JSON) or map.
			// K8sCliTask handles 'string'. Let's follow K8sCliTask pattern closely.
			kubectlResp := map[string]any{}
			if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil {
				return nil, fmt.Errorf("failed to parse relay response: %s", respStr)
			}
			if stderr, ok := kubectlResp["stderr"].(string); ok && stderr != "" {
				return nil, fmt.Errorf("kubectl error: %s", stderr)
			}
			if stdout, ok := kubectlResp["stdout"].(string); ok {
				// The stdout IS the JSON of the resource
				if err := common.UnmarshalJson([]byte(stdout), &respMap); err != nil {
					return nil, fmt.Errorf("failed to parse resource JSON: %w", err)
				}
			} else {
				return nil, errors.New("no stdout from kubectl")
			}
		} else {
			return nil, errors.New("unexpected response format from relay")
		}
	} else if data, ok := respMap["data"].(string); ok {
		// Sometimes relay returns {data: "..."}
		respMap = map[string]any{} // Reset
		if err := common.UnmarshalJson([]byte(data), &respMap); err != nil {
			return nil, fmt.Errorf("failed to parse data field: %w", err)
		}
	}

	obj := &unstructured.Unstructured{Object: respMap}

	// 3. Calculate New Values
	isPod := false
	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if !found || err != nil {
		// Try Pod structure (spec.containers)
		containers, found, err = unstructured.NestedSlice(obj.Object, "spec", "containers")
		if !found || err != nil {
			return nil, errors.New("unable to find containers in resource spec")
		}
		isPod = true
	}

	patchContainers := []map[string]any{}
	changed := false

	direction, _ := params["direction"].(string)
	scaleUpVal, scaleUpProvided := params["scale_up"].(bool)
	recommendationBlob, _ := params["recommendation"].(map[string]any)

	resolverType := "AutoRunbook"
	resolverID := taskCtx.GetWorkflowID()
	recommendationID := ""
	if recommendationBlob != nil {
		resolverType = "AutoOptimize"
		if resolverID1, ok := params["recommendation_id"].(string); ok {
			resolverID = resolverID1
			recommendationID = resolverID1
		}
		if resolverID2, ok := params["recommendation_optimizer_id"].(string); ok {
			resolverID = resolverID2
		}
		if resolverID3, ok := params["recommendation_task_id"].(string); ok {
			resolverID = resolverID3
		}
	}

	if direction == "" {
		if scaleUpProvided {
			if scaleUpVal {
				direction = "up"
			} else {
				direction = "down"
			}
		} else if recommendationBlob == nil {
			return nil, errors.New("direction or scale_up is required when recommendation is not provided")
		}
	}
	direction = strings.ToLower(direction)
	if direction != "" && direction != "up" && direction != "down" {
		return nil, fmt.Errorf("invalid direction '%s': must be 'up' or 'down'", direction)
	}

	cpuConfig, _ := params["cpu"].(map[string]any)
	memConfig, _ := params["memory"].(map[string]any)

	rules := &RightsizeRules{
		CPU:       cpuConfig,
		Memory:    memConfig,
		Direction: direction,
	}

	allErrors := []string{}
	richChanges := []containerChange{}

	for _, c := range containers {
		container, ok := c.(map[string]any)
		if !ok {
			continue
		}
		cName, _ := container["name"].(string)

		resMap, _, _ := unstructured.NestedMap(container, "resources")
		limits, _, _ := unstructured.NestedMap(resMap, "limits")
		requests, _, _ := unstructured.NestedMap(resMap, "requests")

		patchLimits := make(map[string]any)
		patchRequests := make(map[string]any)

		cpuRec, memRec := t.getContainerRecommendations(recommendationBlob, cName)

		thisChange := containerChange{name: cName}

		// CPU Logic
		if cpuConfig != nil {
			allocated := map[string]any{
				"request": requests["cpu"],
				"limit":   limits["cpu"],
			}
			newReq, newLim, errs := rules.ApplyCPURules(cName, cpuRec, allocated)
			allErrors = append(allErrors, errs...)

			if newReq != nil {
				patchRequests["cpu"] = *newReq
				old := ""
				if s, ok := requests["cpu"].(string); ok {
					old = s
				}
				thisChange.cpu = &resourceChange{old: old, new: *newReq}
			}
			if newLim != nil {
				patchLimits["cpu"] = *newLim
			} else if hasAlgo(cpuConfig) && newReq != nil {
				// If algo is used, Python logic often sets limit to nil (remove it)
				patchLimits["cpu"] = nil
			}
		}

		// Memory Logic
		if memConfig != nil {
			allocated := map[string]any{
				"request": requests["memory"],
				"limit":   limits["memory"],
			}
			newReq, newLim, errs := rules.ApplyMemoryRules(cName, memRec, allocated)
			allErrors = append(allErrors, errs...)

			if newReq != nil {
				patchRequests["memory"] = *newReq
				old := ""
				if s, ok := requests["memory"].(string); ok {
					old = s
				}
				thisChange.mem = &resourceChange{old: old, new: *newReq}
			}
			if newLim != nil {
				patchLimits["memory"] = *newLim
			}
		}

		// Construct Container Patch
		if len(patchLimits) > 0 || len(patchRequests) > 0 {
			cPatch := map[string]any{
				"name":      cName,
				"resources": map[string]any{},
			}
			if len(patchLimits) > 0 {
				cPatch["resources"].(map[string]any)["limits"] = patchLimits
			}
			if len(patchRequests) > 0 {
				cPatch["resources"].(map[string]any)["requests"] = patchRequests
			}
			patchContainers = append(patchContainers, cPatch)
			changed = true
			richChanges = append(richChanges, thisChange)
		}
	}

	if !changed {
		reason := "no changes calculated"
		if len(allErrors) > 0 {
			reason = strings.Join(allErrors, "\n\n")
		}
		return map[string]any{"status": "skipped", "reason": reason}, nil
	}

	description := t.generateTicketDescription(kind, name, namespace, richChanges)

	// Construct the full patch object
	var patch map[string]any
	if isPod {
		patch = map[string]any{
			"spec": map[string]any{
				"containers": patchContainers,
			},
		}
	} else {
		patch = map[string]any{
			"spec": map[string]any{
				"template": map[string]any{
					"spec": map[string]any{
						"containers": patchContainers,
					},
				},
			},
		}
	}

	// Add Traceability Annotation
	moduleSuffix := "workflow"
	if resolverType == "AutoOptimize" {
		moduleSuffix = "optimizer"
	}

	annoKey, annoVal, err := GetTraceabilityAnnotation(taskCtx, resolverType, resolverID, moduleSuffix)
	if err != nil {
		taskCtx.GetLogger().Warn("Failed to generate traceability annotation", "error", err)
	} else {
		if isPod {
			patch["metadata"] = map[string]any{
				"annotations": map[string]string{
					annoKey: annoVal,
				},
			}
		} else {
			// For controllers, we update the template metadata to trigger a rollout
			if spec, ok := patch["spec"].(map[string]any); ok {
				if tmpl, ok := spec["template"].(map[string]any); ok {
					tmpl["metadata"] = map[string]any{
						"annotations": map[string]string{
							annoKey: annoVal,
						},
					}
				}
			}
		}
	}

	patchBytes, err := common.MarshalJson(patch)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal patch: %w", err)
	}
	patchStr := string(patchBytes)

	// 5. Handle GitOps or Ticket
	prData := make(map[string]any)
	for _, cPatch := range patchContainers {
		containerName, _ := cPatch["name"].(string)
		resources, _ := cPatch["resources"].(map[string]any)
		limits, _ := resources["limits"].(map[string]any)
		requests, _ := resources["requests"].(map[string]any)

		containerChanges := map[string]any{
			"cpu":    map[string]string{},
			"memory": map[string]string{},
		}

		if val, ok := limits["cpu"]; ok {
			if val != nil {
				containerChanges["cpu"].(map[string]string)["limit"] = fmt.Sprintf("%v", val)
			}
		}
		if val, ok := requests["cpu"]; ok {
			containerChanges["cpu"].(map[string]string)["request"] = fmt.Sprintf("%v", val)
		}

		if val, ok := limits["memory"]; ok {
			if val != nil {
				containerChanges["memory"].(map[string]string)["limit"] = fmt.Sprintf("%v", val)
			}
		}
		if val, ok := requests["memory"]; ok {
			containerChanges["memory"].(map[string]string)["request"] = fmt.Sprintf("%v", val)
		}

		if len(containerChanges["cpu"].(map[string]string)) > 0 || len(containerChanges["memory"].(map[string]string)) > 0 {
			prData[containerName] = containerChanges
		}
	}

	if taskCtx.IsDryRun() {
		taskCtx.GetLogger().Info("Dry Run: Skipping side effects (GitOps, Tickets, Patch)")
		return map[string]any{
			"status":      "dry_run",
			"patch":       patch,
			"description": description,
		}, nil
	}

	result, handled, err := HandleGitOpsOrTicket(taskCtx, params, kind, namespace, name, description, "VerticalRightsize", prData, nil, resolverType, resolverID, recommendationID)
	if err != nil {
		return nil, err
	}
	if handled {
		result["patch"] = patch
		return result, nil
	}

	// using 'strategic merge patch' is default for kubectl patch, but for custom resources or standard ones it varies.
	// However, updating list elements in strategic merge patch requires "name" key which we have.
	cmdPatch := fmt.Sprintf("kubectl patch %s %s -n %s --patch '%s'", kind, name, namespace, patchStr)

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

	return map[string]any{
		"status":      "success",
		"patch":       patch,
		"description": description,
	}, nil
}

func (t *VerticalRightsizeTask) generateTicketDescription(kind, name, namespace string, changes []containerChange) string {
	var sb strings.Builder
	sb.WriteString("## Vertical Rightsizing Recommendation\n\n")
	sb.WriteString("### Resource Details\n")
	fmt.Fprintf(&sb, "**Type:** %s\n", kind)
	fmt.Fprintf(&sb, "**Name:** %s\n", name)
	fmt.Fprintf(&sb, "**Namespace:** %s\n\n", namespace)
	sb.WriteString("### Recommended Changes\n\n")

	for i, c := range changes {
		if i > 0 {
			sb.WriteString("---\n\n")
		}
		fmt.Fprintf(&sb, "Container Name: %s\n\n", c.name)
		if c.cpu != nil {
			fmt.Fprintf(&sb, "CPU Request: %s → %s\n", c.cpu.old, c.cpu.new)
		}
		if c.mem != nil {
			fmt.Fprintf(&sb, "Memory Request: %s → %s\n", c.mem.old, c.mem.new)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

type containerChange struct {
	name string
	cpu  *resourceChange
	mem  *resourceChange
}

type resourceChange struct {
	old string
	new string
}

func (t *VerticalRightsizeTask) getContainerRecommendations(blob map[string]any, containerName string) (cpuRec, memRec map[string]any) {
	if val, ok := blob[containerName]; ok {
		if list, ok := val.([]any); ok {
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					switch m["resource"] {
					case "cpu":
						cpuRec = m
					case "memory":
						memRec = m
					}
				}
			}
		}
	}

	if cpuRec == nil {
		if cpu, ok := blob["cpu"].(map[string]any); ok {
			cpuRec = cpu
		}
	}
	if memRec == nil {
		if mem, ok := blob["memory"].(map[string]any); ok {
			memRec = mem
		}
	}
	return
}

func hasAlgo(config map[string]any) bool {
	if config == nil {
		return false
	}
	algo, _ := config["algo"].(string)
	return algo != ""
}

func (t *VerticalRightsizeTask) InputSchema() *types.Schema {
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
				Required:    false,
				Options:     []string{"up", "down"},
				Order:       5,
			},
			"cpu": {
				Type:        types.PropertyTypeObject,
				Title:       "CPU",
				Description: "CPU configuration for vertical rightsizing.",
				Order:       6,
				Schema: &types.Schema{
					Properties: map[string]types.Property{
						"change_pct": {
							Type:        types.PropertyTypeNumber,
							Title:       "Change %",
							Description: "Percentage to change CPU (e.g., 10 for 10%).",
							Required:    true,
							Order:       1,
						},
						"min": {
							Type:        types.PropertyTypeString,
							Title:       "Min",
							Description: "Minimum CPU value (e.g., '10m', '0.1').",
							Order:       2,
						},
						"max": {
							Type:        types.PropertyTypeString,
							Title:       "Max",
							Description: "Maximum CPU value (e.g., '100m', '1').",
							Order:       3,
						},
						"remove_limit": {
							Type:        types.PropertyTypeBoolean,
							Title:       "Remove Limit",
							Description: "Set to true to remove the CPU limit.",
							Order:       4,
						},
					},
				},
			},
			"memory": {
				Type:        types.PropertyTypeObject,
				Title:       "Memory",
				Description: "Memory configuration for vertical rightsizing.",
				Order:       7,
				Schema: &types.Schema{
					Properties: map[string]types.Property{
						"change_pct": {
							Type:        types.PropertyTypeNumber,
							Title:       "Change %",
							Description: "Percentage to change Memory (e.g., 10 for 10%).",
							Required:    true,
							Order:       1,
						},
						"min": {
							Type:        types.PropertyTypeString,
							Title:       "Min",
							Description: "Minimum Memory value (e.g., '100Mi', '256Mi').",
							Order:       2,
						},
						"max": {
							Type:        types.PropertyTypeString,
							Title:       "Max",
							Description: "Maximum Memory value (e.g., '1Gi', '500Mi').",
							Order:       3,
						},
						"remove_limit": {
							Type:        types.PropertyTypeBoolean,
							Title:       "Remove Limit",
							Description: "Set to true to remove the Memory limit.",
							Order:       4,
						},
					},
				},
			},
			"gitops_config": {
				Type:        types.PropertyTypeObject,
				Title:       "GitOps",
				Description: "Configuration for GitOps integration (e.g., Pull Request creation).",
				Order:       8,
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

func (t *VerticalRightsizeTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"status":        {Type: types.PropertyTypeString},
			"patch":         {Type: types.PropertyTypeObject},
			"resolution_id": {Type: types.PropertyTypeString},
		},
	}
}

func getWorkflowBaseLink(taskCtx types.TaskContext) string {
	return fmt.Sprintf("%s/workflow/%s?accountId=%s&executionId=%s", config.Config.BaseUrl, taskCtx.GetWorkflowID(), taskCtx.GetAccountID(), taskCtx.GetWorkflowRunID())
}
