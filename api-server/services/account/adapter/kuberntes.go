package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

const (
	MEMORY_BASE_MAGNITUDE = 1024
	KiB                   = MEMORY_BASE_MAGNITUDE
	MiB                   = KiB * 1024
	GiB                   = MiB * 1024
	TiB                   = GiB * 1024
)

type kuberntesAdapter struct {
}

// memoryQuantitySuffixes maps Kubernetes memory quantity suffixes to their byte
// multiplier. Binary (Ki/Mi/...) always end in "i", so they never collide with
// the SI single-letter forms; the milli suffix "m" is intentionally absent so a
// CPU quantity like "91m" is not mistaken for a memory value.
var memoryQuantitySuffixes = []struct {
	suffix string
	mult   float64
}{
	{"Ei", float64(TiB) * 1024 * 1024}, {"Pi", float64(TiB) * 1024}, {"Ti", TiB}, {"Gi", GiB}, {"Mi", MiB}, {"Ki", KiB},
	{"E", 1e18}, {"P", 1e15}, {"T", 1e12}, {"G", 1e9}, {"M", 1e6}, {"k", 1e3},
}

// parseMemoryQuantityToBytes parses a Kubernetes memory quantity string
// (e.g. "338077Ki", "500Mi", "1.5Gi", "346190848") into a byte count. It
// returns ok=false for CPU-style quantities (e.g. "91m") and any input that is
// not a recognizable memory quantity, so callers can leave those untouched.
func parseMemoryQuantityToBytes(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, u := range memoryQuantitySuffixes {
		if strings.HasSuffix(s, u.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSpace(s[:len(s)-len(u.suffix)]), 64)
			if err != nil {
				return 0, false
			}
			return n * u.mult, true
		}
	}
	// No suffix: a plain byte count.
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// applyMemoryUnit renders a memory value as a compact Kubernetes quantity string
// (e.g. "331Mi", "1.5Gi"). It accepts a raw byte count (float64/int, as carried
// by the recommendation pipeline and the UI "Create PR" path) or an already
// unit-suffixed quantity string (e.g. "338077Ki", as produced by the
// AutoOptimize path). Unrecognized inputs are returned unchanged so non-memory
// quantities (e.g. CPU "91m") pass through.
func applyMemoryUnit(unit any) any {
	if unit == nil {
		return nil
	}

	var bytes float64
	switch v := unit.(type) {
	case float64:
		bytes = v
	case int:
		bytes = float64(v)
	case int64:
		bytes = float64(v)
	case string:
		b, ok := parseMemoryQuantityToBytes(v)
		if !ok {
			return unit
		}
		bytes = b
	default:
		return unit
	}

	unitInt := int(bytes)
	switch {
	case unitInt >= TiB:
		// Round to 1 decimal place for values equal to or greater than Ti
		return fmt.Sprintf("%.1fTi", float64(unitInt)/TiB)
	case unitInt >= GiB:
		// Round to 1 decimal place for values equal to or greater than Gi
		return fmt.Sprintf("%.1fGi", float64(unitInt)/GiB)
	case unitInt >= MiB:
		return fmt.Sprintf("%dMi", int(math.Ceil(float64(unitInt)/MiB)))
	default:
		return fmt.Sprintf("%dKi", unitInt/KiB)
	}
}

// Resource sanity bounds. They catch unit-scale mistakes (a MiB value applied as
// bytes, or millicores applied as whole cores) before they reach the cluster and
// brick the workload — e.g. the "160.79 MiB sent unitless -> 160790m (~160
// bytes)" apply that OOM-killed a pod. They are deliberately wide: nothing
// legitimate in this product runs below 1Mi of memory or above 128 CPU cores per
// container, so a violation is a bug, not a real resource decision.
const (
	minMemoryBytes = float64(MiB)     // 1Mi floor
	maxMemoryBytes = float64(TiB) * 2 // 2Ti ceiling
	maxCPUCores    = 128.0
)

// cpuQuantityToCores parses a Kubernetes CPU quantity ("500m", "0.5", "2") into a
// core count. ok=false for empty or unparseable input.
func cpuQuantityToCores(v any) (float64, bool) {
	s := strings.TrimSpace(asString(v))
	if s == "" {
		return 0, false
	}
	if strings.HasSuffix(s, "m") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "m"), 64)
		if err != nil {
			return 0, false
		}
		return n / 1000.0, true
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// validateContainerResources rejects physically implausible cpu/memory
// quantities in the agent_task payload. Empty (unset) fields are skipped; a
// non-empty field that is unparseable or outside its bounds is an error. This is
// the last line of defense against a unit bug in any upstream caller — it runs on
// the exact normalized values the agent will patch, so it holds even if a future
// path bypasses the UI/applyMemoryUnit normalization.
func validateContainerResources(containers []any) error {
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		name := asString(cm["container_name"])
		for _, field := range []string{"memory_request", "memory_limit"} {
			raw := asString(cm[field])
			if raw == "" {
				continue
			}
			bytes, ok := parseMemoryQuantityToBytes(raw)
			if !ok {
				return fmt.Errorf("container %q: %s %q is not a valid memory quantity", name, field, raw)
			}
			if bytes < minMemoryBytes {
				return fmt.Errorf("container %q: %s %q is below the 1Mi floor — likely a unit error", name, field, raw)
			}
			if bytes > maxMemoryBytes {
				return fmt.Errorf("container %q: %s %q exceeds the 2Ti ceiling — likely a unit error", name, field, raw)
			}
		}
		for _, field := range []string{"cpu_request", "cpu_limit"} {
			raw := asString(cm[field])
			if raw == "" {
				continue
			}
			cores, ok := cpuQuantityToCores(raw)
			if !ok {
				return fmt.Errorf("container %q: %s %q is not a valid cpu quantity", name, field, raw)
			}
			if cores <= 0 {
				return fmt.Errorf("container %q: %s %q must be greater than 0", name, field, raw)
			}
			if cores > maxCPUCores {
				return fmt.Errorf("container %q: %s %q exceeds %.0f cores — likely a unit error", name, field, raw, maxCPUCores)
			}
		}
	}
	return nil
}

func generateAgentTask(ctx AccountAdapterContext, recommendation models.Recommendation, action string, actionParams map[string]any, source string) (string, error) {
	return generateAgentTaskWithStatus(ctx, recommendation, action, actionParams, source, "TODO")
}

// generateAgentTaskWithStatus inserts an agent_task with an explicit status.
// Status "TODO" lets the in-cluster legacy agent pick it up; "PROCESSING" (in
// the agent's IGNORE_STATUS) makes the agent skip it so api-server can process
// it itself — used by the in-place rightsizing path, which later finalizes the
// task to COMPLETED or resets it to TODO for the legacy rollout fallback.
func generateAgentTaskWithStatus(ctx AccountAdapterContext, recommendation models.Recommendation, action string, actionParams map[string]any, source string, status string) (string, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}

	jsonActionParams, err := common.MarshalJson(actionParams)
	if err != nil {
		return "", err
	}

	taskId := uuid.New().String()

	recommendationSource := "recommendation"
	if source != "" {
		recommendationSource = source
	}

	_, err = manager.Db.Exec("INSERT INTO agent_task (cloud_account_id, tenant, status, action, payload, source, source_id, resoruce_id, id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)", recommendation.CloudAccountId, recommendation.TenantId, status, action, string(jsonActionParams), recommendationSource, recommendation.Id, recommendation.ResourceId, taskId)
	if err != nil {
		ctx.GetLogger().Error("failed to insert agent task", "error", err)
		return "", err
	}
	return taskId, nil
}

func (k *kuberntesAdapter) ApplyRecommendation(ctx AccountAdapterContext, request ApplyRecommendationRequest, existingRecommendations []models.RecommendationResolution, recommendResolutionId string) (ApplyRecommendationResponse, error) {
	recommendationSource := "recommendation"
	if request.ProviderConfig["recommendation_source"] != nil {
		recommendationSource = request.ProviderConfig["recommendation_source"].(string)
	}
	switch {
	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "abandoned_resource":
		resourceMeta := request.Resource.Meta.Object().(map[string]any)
		ctx.GetLogger().Info(fmt.Sprintf("applying abandoned_resource recommendation: %s, %s, %s",
			resourceMeta["namespace"], resourceMeta["controller"], resourceMeta["controllerKind"]))

		taskId, err := generateAgentTask(ctx, request.Recommendation, "rightsizing_pvc", map[string]any{
			"action_name": "replica_rightsizing",
			"action_params": map[string]any{
				"name":          resourceMeta["controller"],
				"namespace":     resourceMeta["namespace"],
				"kind":          resourceMeta["controllerKind"],
				"replica_count": 0,
			},
		}, recommendationSource)
		if err != nil {
			return ApplyRecommendationResponse{}, fmt.Errorf("failed to generate agent task: %w", err)
		}
		return ApplyRecommendationResponse{
			Data:                     request.Data,
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypeDeploymentChange,
			ResolutionTypeRefrenceId: taskId,
		}, nil
	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "pod_right_sizing":
		if request.Resource.Id == "" {
			return ApplyRecommendationResponse{}, fmt.Errorf("rule is not supported for empty respurce id")
		}
		containers := []any{}
		resourceMeta := request.Resource.Meta.Object().(map[string]any)
		resourceMeta["controller"] = request.Resource.Name
		containerAnnotations := []map[string]string{}
		for key, value := range request.Data {
			resourceData, ok := value.(map[string]any)
			if !ok {
				continue
			}
			// Skip non-container keys (e.g. cloud_resourse, card_id, id, accountId, container_name)
			if resourceData["cpu"] == nil && resourceData["memory"] == nil {
				continue
			}
			// Resolve the cpu/memory sub-maps once with comma-ok. Reads from a nil
			// map are safe in Go, so the per-field accesses below can't panic even
			// when a sub-map is absent or the wrong shape (previously each access
			// was an unchecked resourceData["cpu"].(map[string]any) assertion).
			cpuData, _ := resourceData["cpu"].(map[string]any)
			memData, _ := resourceData["memory"].(map[string]any)
			ctx.GetLogger().Info(fmt.Sprintf("applying recommendation: %s, %s, %s, %s, %v, %v, %v, %v",
				resourceMeta["namespace"], resourceMeta["controller"], resourceMeta["controllerKind"],
				key,
				cpuData["request"],
				cpuData["limit"],
				memData["request"],
				memData["limit"]))
			recObj, isRecMap := request.Recommendation.Recommendation.Object().(map[string]any)
			if !isRecMap {
				continue
			}
			recommendationList, _ := recObj[key].([]any)
			var cpuAllocated map[string]any
			var memAllocated map[string]any
			for i := range recommendationList {
				item, ok := recommendationList[i].(map[string]any)
				if !ok {
					continue
				}
				resource, ok := item["resource"].(string)
				if !ok {
					continue
				}
				switch resource {
				case "cpu":
					if allocated, ok := item["allocated"].(map[string]any); ok {
						cpuAllocated = allocated
					}
				case "memory":
					if allocated, ok := item["allocated"].(map[string]any); ok {
						memAllocated = allocated
					}
				}
			}
			// Normalize the requested memory to a compact Kubernetes quantity
			// (e.g. "161Mi") before it reaches the agent. The UI sends memory as a
			// unit-suffixed string ("160.79Mi") and the server-side pipeline sends a
			// raw byte count; applyMemoryUnit renders both to a valid quantity. Sent
			// verbatim, a bare number is read by Kubernetes as *bytes* — 160.79 lands
			// as "160790m" (~160 bytes) and OOM-kills the pod. CPU is a valid quantity
			// as bare cores, so it passes through untouched.
			memRequest := applyMemoryUnit(memData["request"])
			memLimit := applyMemoryUnit(memData["limit"])
			allocatedMemRequest := applyMemoryUnit(memAllocated["request"])
			allocatedMemLimit := applyMemoryUnit(memAllocated["limit"])
			allocatedCpuRequest := applyMemoryUnit(cpuAllocated["request"])
			allocatedCpuLimit := applyMemoryUnit(cpuAllocated["limit"])
			annotation := map[string]any{
				"cpu_request":         cpuData["request"],
				"cpu_limit":           cpuData["limit"],
				"prev_cpu_request":    allocatedCpuRequest,
				"prev_cpu_limit":      allocatedCpuLimit,
				"memory_request":      memRequest,
				"memory_limit":        memLimit,
				"prev_memory_request": allocatedMemRequest,
				"prev_memory_limit":   allocatedMemLimit,
			}
			contianerAnnotation := getAnnotationDict(key, annotation)
			containerAnnotations = append(containerAnnotations, contianerAnnotation)
			containers = append(containers, map[string]any{
				"cpu_limit":      cpuData["limit"],
				"cpu_request":    cpuData["request"],
				"memory_limit":   memLimit,
				"container_name": key,
				"memory_request": memRequest,
			})
		}
		// Reject a physically implausible apply (e.g. a value mangled by a unit
		// bug) before it reaches the agent, instead of silently bricking the
		// workload. Runs on the normalized quantities the agent will actually patch,
		// so it guards both the in-place and legacy rollout paths below.
		if err := validateContainerResources(containers); err != nil {
			return ApplyRecommendationResponse{}, fmt.Errorf("recommendation apply rejected: %w", err)
		}
		containerChanges := map[string]any{
			"container_changes": containerAnnotations,
		}
		var finalAnnotation map[string]any
		finalAnnotation = getDefaultAnnotations(request.Recommendation.Id, ctx.GetSecurityContext().GetUserId())
		finalAnnotation = lo.Assign(finalAnnotation, containerChanges)
		annotationJson, err := json.Marshal(finalAnnotation)
		if err != nil {
			return ApplyRecommendationResponse{}, fmt.Errorf("failed to marshal annotation: %w", err)
		}

		agentTaskPayload := map[string]any{
			"action_name": "rightsizing_resource",
			"action_params": map[string]any{
				"kind":        resourceMeta["controllerKind"],
				"name":        resourceMeta["controller"],
				"namespace":   resourceMeta["namespace"],
				"containers":  containers,
				"annotations": map[string]string{"recommendation_apply.vertical-scaler": string(annotationJson)},
			},
		}

		// Zero-downtime path: when in_place is requested and the cluster supports
		// it (>= 1.35), create the agent_task as PROCESSING (the legacy agent
		// skips it) and resize the running pods in place from api-server. On
		// success the task is finalized COMPLETED; if in-place is infeasible the
		// task is reset to TODO so the legacy rollout path applies it normally.
		inPlace, _ := request.ProviderConfig["in_place"].(bool)
		if inPlace && clusterSupportsInPlaceResize(ctx, request.Recommendation.CloudAccountId) {
			taskId, err := generateAgentTaskWithStatus(ctx, request.Recommendation, "rightsizing_resource", agentTaskPayload, recommendationSource, "PROCESSING")
			if err != nil {
				return ApplyRecommendationResponse{}, fmt.Errorf("failed to generate agent task: %w", err)
			}
			accountId := request.Recommendation.CloudAccountId
			kind, _ := resourceMeta["controllerKind"].(string)
			name, _ := resourceMeta["controller"].(string)
			ns, _ := resourceMeta["namespace"].(string)
			logger := ctx.GetLogger()
			go runInPlaceRightsizing(logger, accountId, taskId, kind, name, ns, containers)
			return ApplyRecommendationResponse{
				Data:                     request.Data,
				Status:                   RecommendationResolutionStatusInProgress,
				ResolutionType:           RecommendationResolutionTypeDeploymentChange,
				ResolutionTypeRefrenceId: taskId,
				StatusMessage:            "Applying in-place (no restart)",
			}, nil
		}

		taskId, err := generateAgentTask(ctx, request.Recommendation, "rightsizing_resource", agentTaskPayload, recommendationSource)
		if err != nil {
			return ApplyRecommendationResponse{}, fmt.Errorf("failed to generate agent task: %w", err)
		}
		return ApplyRecommendationResponse{
			Data:                     request.Data,
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypeDeploymentChange,
			ResolutionTypeRefrenceId: taskId,
		}, nil

	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "unused_pvc":
		existingRecommendation := request.Recommendation.Recommendation.Object().(map[string]any)
		ctx.GetLogger().Info("Applying recommendation: %s", existingRecommendation["metadata"].(map[string]any)["name"])

		taskId, err := generateAgentTask(ctx, request.Recommendation, "volume_delete", map[string]any{
			"action_name": "volume_delete",
			"action_params": map[string]any{
				"name": existingRecommendation["metadata"].(map[string]any)["name"],
			},
		}, recommendationSource)
		if err != nil {
			return ApplyRecommendationResponse{}, fmt.Errorf("failed to generate agent task: %w", err)
		}
		return ApplyRecommendationResponse{
			Data:                     request.Data,
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypeDeploymentChange,
			ResolutionTypeRefrenceId: taskId,
		}, nil

	case request.Recommendation.Category == "RightSizing" && request.Recommendation.RuleName == "pv_rightsize":
		existingRecommendation := request.Recommendation.Recommendation.Object().(map[string]any)
		recommendedSizeAny := request.Data["recommendedSize"]
		recommendedSize := 0.0
		if recommendedSizeAny != nil {
			switch recommendedSize1 := recommendedSizeAny.(type) {
			case float64:
				recommendedSize = recommendedSize1
			case int:
				recommendedSize = float64(recommendedSize1)
			case string:
				recommendedSize2, err := strconv.ParseFloat(recommendedSize1, 64)
				if err != nil {
					return ApplyRecommendationResponse{}, fmt.Errorf("failed to parse recommendedSize: %w", err)
				}
				recommendedSize = recommendedSize2
			}
		}

		if recommendedSize == 0 {
			return ApplyRecommendationResponse{}, errors.New("recommendedSize is 0")
		}

		currentSize := 0.0
		if existingRecommendation["spec"].(map[string]any)["capacity"].(map[string]any)["storage"] != nil {
			currentSizeStr := existingRecommendation["spec"].(map[string]any)["capacity"].(map[string]any)["storage"].(string)
			currentSizeStr = strings.ToLower(currentSizeStr)

			if strings.HasSuffix(currentSizeStr, "gi") {
				currentSizeStr = strings.TrimSuffix(currentSizeStr, "gi")
			} else if strings.HasSuffix(currentSizeStr, "gb") {
				currentSizeStr = strings.TrimSuffix(currentSizeStr, "gb")
			} else {
				return ApplyRecommendationResponse{}, fmt.Errorf("unsupported unit: %s", currentSizeStr)
			}
			currentSize1, err := strconv.ParseFloat(currentSizeStr, 64)
			if err != nil {
				return ApplyRecommendationResponse{}, fmt.Errorf("failed to parse currentSize: %w", err)
			}
			currentSize = currentSize1
		}

		if currentSize == recommendedSize {
			return ApplyRecommendationResponse{
				Data:                     request.Data,
				Status:                   RecommendationResolutionStatusSuccess,
				ResolutionType:           RecommendationResolutionTypeDeploymentChange,
				ResolutionTypeRefrenceId: "",
			}, nil
		}

		ctx.GetLogger().Info(fmt.Sprintf("Applying recommendation: %s, %s, %f, existing size: %f",
			existingRecommendation["spec"].(map[string]any)["claimRef"].(map[string]any)["name"],
			existingRecommendation["spec"].(map[string]any)["claimRef"].(map[string]any)["namespace"],
			recommendedSize, currentSize))

		taskId, err := generateAgentTask(ctx, request.Recommendation, "rightsize_pvc", map[string]any{
			"action_name": "rightsize_pvc",
			"action_params": map[string]any{
				"name":      existingRecommendation["spec"].(map[string]any)["claimRef"].(map[string]any)["name"],
				"namespace": existingRecommendation["spec"].(map[string]any)["claimRef"].(map[string]any)["namespace"],
				"size":      fmt.Sprintf("%fGi", recommendedSize),
			},
		}, recommendationSource)

		if err != nil {
			return ApplyRecommendationResponse{}, fmt.Errorf("failed to generate agent task: %w", err)
		}

		return ApplyRecommendationResponse{
			Data:                     request.Data,
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypeDeploymentChange,
			ResolutionTypeRefrenceId: taskId,
		}, nil

	case request.Recommendation.Category == "EventResolution":
		data, ok := request.Recommendation.Recommendation.Object().(map[string]any)
		if !ok {
			return ApplyRecommendationResponse{}, fmt.Errorf("invalid recommendation data format: not a map[string]any")
		}
		taskId, err := generateAgentTask(ctx, request.Recommendation, data["action_name"].(string), map[string]any{
			"action_name":   data["action_name"].(string),
			"action_params": data["action_params"].(map[string]any),
		}, "event_resolution")
		if err != nil {
			return ApplyRecommendationResponse{}, fmt.Errorf("failed to generate agent task: %w", err)
		}
		return ApplyRecommendationResponse{
			Data:                     request.Data,
			Status:                   RecommendationResolutionStatusInProgress,
			ResolutionType:           RecommendationResolutionTypeDeploymentChange,
			ResolutionTypeRefrenceId: taskId,
		}, nil
	default:
		return ApplyRecommendationResponse{}, fmt.Errorf("unsupported recommendation category: %s, rule: %s", request.Recommendation.Category, request.Recommendation.RuleName)
	}
}

func (k *kuberntesAdapter) GetRecommendationResolutionStatus(ctx AccountAdapterContext, recommendation models.Recommendation, resolutionReferenceId string, applyRequestPayload models.Json, resolutionStatusMessage string) (GetRecommendationResolutionStatusResponse, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return GetRecommendationResolutionStatusResponse{}, err
	}
	// get status and message for agent_task
	row := manager.Db.QueryRow("SELECT status, response::text FROM agent_task WHERE id = $1", resolutionReferenceId)
	if row.Err() != nil {
		return GetRecommendationResolutionStatusResponse{}, errors.New("task not found")
	}
	var status string
	var messagePtr *string
	err = row.Scan(&status, &messagePtr)
	if err != nil {
		ctx.GetLogger().Error("failed to get agent task status", "error", err)
		return GetRecommendationResolutionStatusResponse{}, err
	}
	message := resolutionStatusMessage
	if messagePtr != nil {
		message = *messagePtr
	}
	resolutionStatus := RecommendationResolutionStatusInProgress
	switch status {
	case "COMPLETED":
		resolutionStatus = RecommendationResolutionStatusSuccess
		message = "Recommendation applied successfully"
	case "FAILED":
		resolutionStatus = RecommendationResolutionStatusFailed
	case "TODO":
		resolutionStatus = RecommendationResolutionStatusInProgress
	}

	return GetRecommendationResolutionStatusResponse{
		Status:        resolutionStatus,
		StatusMessage: message,
	}, nil
}
