package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"nudgebee/runbook/services/security"
	"strings" // Added strings
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
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

	// Direct-apply path (GitOps/ticket not enabled). Unless explicitly disabled,
	// attempt a zero-downtime in-place pod resize when the cluster supports it
	// (K8s >= 1.35, where the feature is GA), falling back to the rollout below on ineligibility,
	// patch error, or an infeasible resize.
	inPlaceRequested := true
	if v, ok := params["in_place"].(bool); ok {
		inPlaceRequested = v
	}
	if inPlaceRequested {
		decision := evaluateInPlace(taskCtx, accountId, kind)
		if decision.eligible {
			ipResult, ipErr := t.applyInPlace(taskCtx, requestContext, accountId, kind, name, namespace, patchContainers, annoKey, annoVal, patch, description)
			if ipErr != nil {
				return nil, ipErr
			}
			if ipResult != nil {
				return ipResult, nil
			}
			// nil result → in-place not feasible; fall through to the rollout patch.
			taskCtx.GetLogger().Info("In-place resize not feasible, falling back to rollout", "kind", kind, "name", name, "namespace", namespace)
		} else {
			taskCtx.GetLogger().Info("In-place resize not eligible, using rollout", "reason", decision.reason, "kind", kind, "name", name)
		}
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

// applyInPlace resizes a workload's running pods via the pod `resize`
// subresource (KEP-1287), without recreating them. The controller template is
// intentionally left untouched (any template change triggers a rollout), so the
// change is not persisted — pods recreated later revert to template values.
//
// It returns a success result on completion, or (nil, nil) to signal the caller
// to fall back to the rollout patch (pods couldn't be listed, a patch errored,
// or a resize was infeasible). A non-nil error is a hard failure.
func (t *VerticalRightsizeTask) applyInPlace(taskCtx types.TaskContext, requestContext *security.RequestContext, accountId, kind, name, namespace string, patchContainers []map[string]any, annoKey, annoVal string, patch map[string]any, description string) (map[string]any, error) {
	pods, err := listWorkloadPods(requestContext, accountId, kind, name, namespace)
	if err != nil {
		taskCtx.GetLogger().Warn("In-place: failed to list pods, falling back to rollout", "error", err)
		return nil, nil
	}
	if len(pods) == 0 {
		taskCtx.GetLogger().Warn("In-place: no pods found, falling back to rollout", "kind", kind, "name", name)
		return nil, nil
	}

	// The resize subresource accepts only spec.containers[].resources.
	resizePatch := map[string]any{"spec": map[string]any{"containers": patchContainers}}
	resizePatchBytes, err := common.MarshalJson(resizePatch)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal resize patch: %w", err)
	}
	resizePatchStr := string(resizePatchBytes)

	annotationPatchStr := ""
	if annoKey != "" {
		annoBytes, aerr := common.MarshalJson(map[string]any{
			"metadata": map[string]any{"annotations": map[string]string{annoKey: annoVal}},
		})
		if aerr == nil {
			annotationPatchStr = string(annoBytes)
		}
	}

	// Trigger the resize (and best-effort annotation) on every pod that isn't
	// already at target up front, so the kubelets actuate in parallel; then poll
	// them together. Total time is ~the poll budget, not budget × replicas.
	resized := []string{}
	targeted := []string{}
	for _, pod := range pods {
		podName, _, _ := unstructured.NestedString(pod, "metadata", "name")
		if podName == "" {
			continue
		}

		// Idempotency: skip pods already at the target resources.
		if podResourcesMatch(pod, patchContainers) {
			taskCtx.GetLogger().Info("In-place: pod already at target, skipping", "pod", podName)
			resized = append(resized, podName)
			continue
		}

		cmd := fmt.Sprintf("kubectl patch pod %s -n %s --subresource=resize --patch '%s'", podName, namespace, resizePatchStr)
		resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", cmd, nil)
		if err != nil {
			taskCtx.GetLogger().Warn("In-place: resize patch failed, falling back to rollout", "pod", podName, "error", err)
			return nil, nil
		}
		if stderr := kubectlStderr(resp); stderr != "" {
			taskCtx.GetLogger().Warn("In-place: resize patch error, falling back to rollout", "pod", podName, "stderr", stderr)
			return nil, nil
		}

		// Stamp the traceability annotation on the pod (separate normal patch;
		// the resize subresource only accepts resources). Best-effort.
		if annotationPatchStr != "" {
			annoCmd := fmt.Sprintf("kubectl patch pod %s -n %s --patch '%s'", podName, namespace, annotationPatchStr)
			if _, aerr := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", annoCmd, nil); aerr != nil {
				taskCtx.GetLogger().Warn("In-place: failed to stamp pod annotation (non-fatal)", "pod", podName, "error", aerr)
			}
		}
		targeted = append(targeted, podName)
	}

	if len(targeted) > 0 && !t.waitForResizes(taskCtx, requestContext, accountId, namespace, targeted) {
		return nil, nil // infeasible / timeout → fall back to rollout
	}
	resized = append(resized, targeted...)

	return map[string]any{
		"status":       "success",
		"mode":         "in_place",
		"resized_pods": resized,
		"patch":        patch,
		"description":  description,
		"note":         "Applied in-place without restart; workload template was not modified, so pods will revert to template values if recreated.",
	}, nil
}

// waitForResizes polls the given pods (bounded) for in-place resize completion,
// inspecting the KEP-1287 status conditions. Returns true when all complete;
// false on an infeasible/errored resize or when the budget is exhausted,
// signalling the caller to fall back to a rollout. Polling all pods together
// (rather than one-at-a-time) bounds total time to ~the budget regardless of
// replica count.
func (t *VerticalRightsizeTask) waitForResizes(taskCtx types.TaskContext, requestContext *security.RequestContext, accountId, namespace string, pods []string) bool {
	const (
		interval    = 5 * time.Second
		maxAttempts = 12 // ~60s budget
	)
	ctx := taskCtx.GetContext()
	pending := make(map[string]struct{}, len(pods))
	for _, p := range pods {
		pending[p] = struct{}{}
	}
	// Reuse a single timer across iterations (Reset after the channel is drained
	// by the select) instead of time.After in the loop, which leaks a timer per
	// iteration until it fires.
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for i := 0; i < maxAttempts; i++ {
		for pod := range pending {
			resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", fmt.Sprintf("kubectl get pod %s -n %s -o json", pod, namespace), nil)
			if err != nil {
				continue
			}
			pm, perr := parseKubectlResponse(resp)
			if perr != nil {
				continue
			}
			switch state, msg := resizeStatus(pm); state {
			case resizeDone:
				delete(pending, pod)
			case resizeInfeasible:
				taskCtx.GetLogger().Warn("In-place: resize infeasible, falling back to rollout", "pod", pod, "message", msg)
				return false
			}
		}
		if len(pending) == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
		}
		timer.Reset(interval)
	}
	return false
}

// kubectlStderr extracts a non-empty stderr from a relay kubectl response, if any.
func kubectlStderr(resp any) string {
	respStr, ok := resp.(string)
	if !ok {
		return ""
	}
	kubectlResp := map[string]any{}
	if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil {
		return ""
	}
	if stderr, ok := kubectlResp["stderr"].(string); ok {
		return strings.TrimSpace(stderr)
	}
	return ""
}

// podResourcesMatch reports whether every target container's requests/limits
// already match the pod's live containerStatuses resources. Used to skip pods
// that are already at the desired size. Conservative: any parse/lookup miss
// returns false (don't skip).
func podResourcesMatch(pod map[string]any, patchContainers []map[string]any) bool {
	statuses, found, _ := unstructured.NestedSlice(pod, "status", "containerStatuses")
	if !found {
		return false
	}
	liveByName := map[string]map[string]any{}
	for _, s := range statuses {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		cname, _ := sm["name"].(string)
		liveByName[cname] = sm
	}
	for _, pc := range patchContainers {
		cname, _ := pc["name"].(string)
		live, ok := liveByName[cname]
		if !ok {
			return false
		}
		target, _ := pc["resources"].(map[string]any)
		for _, kind := range []string{"requests", "limits"} {
			tgt, _ := target[kind].(map[string]any)
			for res, valRaw := range tgt {
				if valRaw == nil {
					continue
				}
				liveVal, lfound, _ := unstructured.NestedString(live, "resources", kind, res)
				if !lfound {
					return false
				}
				tq, err1 := resource.ParseQuantity(fmt.Sprintf("%v", valRaw))
				lq, err2 := resource.ParseQuantity(liveVal)
				if err1 != nil || err2 != nil || tq.Cmp(lq) != 0 {
					return false
				}
			}
		}
	}
	return true
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
				Required:    true,
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
			"in_place": {
				Type:        types.PropertyTypeBoolean,
				Title:       "In-Place Resize",
				Description: "Attempt a zero-downtime in-place pod resize when the cluster supports it (K8s >= 1.35, where the feature is GA), falling back to a rolling restart otherwise. Defaults to true. Ignored when GitOps/ticket is enabled.",
				Default:     true,
				Order:       8,
			},
			"gitops_config": {
				Type:        types.PropertyTypeObject,
				Title:       "GitOps",
				Description: "Configuration for GitOps integration (e.g., Pull Request creation).",
				Order:       9,
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
	// Isolated "Run Task" executions have no real workflow run, so the id and
	// run id are empty. Return "" rather than a malformed /workflow/?... link;
	// callers (e.g. pv_rightsize, common_actions) already treat "" as "no link".
	if taskCtx.GetWorkflowID() == "" {
		return ""
	}
	return fmt.Sprintf("%s/workflow/%s?accountId=%s&executionId=%s", config.Config.BaseUrl, taskCtx.GetWorkflowID(), taskCtx.GetAccountID(), taskCtx.GetWorkflowRunID())
}
