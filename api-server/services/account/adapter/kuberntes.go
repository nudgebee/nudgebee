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

func applyMemoryUnit(unit any) any {
	if unit == nil {
		return nil
	}

	unitInt := int(unit.(float64))
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

func generateAgentTask(ctx AccountAdapterContext, recommendation models.Recommendation, action string, actionParams map[string]any, source string) (string, error) {
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

	_, err = manager.Db.Exec("INSERT INTO agent_task (cloud_account_id, tenant, status, action, payload, source, source_id, resoruce_id, id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)", recommendation.CloudAccountId, recommendation.TenantId, "TODO", action, string(jsonActionParams), recommendationSource, recommendation.Id, recommendation.ResourceId, taskId)
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
			ctx.GetLogger().Info(fmt.Sprintf("applying recommendation: %s, %s, %s, %s, %v, %v, %v, %v",
				resourceMeta["namespace"], resourceMeta["controller"], resourceMeta["controllerKind"],
				key,
				resourceData["cpu"].(map[string]any)["request"],
				resourceData["cpu"].(map[string]any)["limit"],
				resourceData["memory"].(map[string]any)["request"],
				resourceData["memory"].(map[string]any)["limit"]))
			recommendationList := request.Recommendation.Recommendation.Object().(map[string]any)[key].([]any)
			var cpuAllocated map[string]any
			var memAllocated map[string]any
			for i := range recommendationList {
				value := recommendationList[i].(map[string]any)
				if value["resource"].(string) == "cpu" && value["allocated"] != nil {
					cpuAllocated = value["allocated"].(map[string]any)
				} else if value["resource"].(string) == "memory" && value["allocated"] != nil {
					memAllocated = value["allocated"].(map[string]any)
				}
			}
			allocatedMemRequest := applyMemoryUnit(memAllocated["request"])
			allocatedMemLimit := applyMemoryUnit(memAllocated["limit"])
			allocatedCpuRequest := applyMemoryUnit(cpuAllocated["request"])
			allocatedCpuLimit := applyMemoryUnit(cpuAllocated["limit"])
			annotation := map[string]any{
				"cpu_request":         resourceData["cpu"].(map[string]any)["request"],
				"cpu_limit":           resourceData["cpu"].(map[string]any)["limit"],
				"prev_cpu_request":    allocatedCpuRequest,
				"prev_cpu_limit":      allocatedCpuLimit,
				"memory_request":      resourceData["memory"].(map[string]any)["request"],
				"memory_limit":        resourceData["memory"].(map[string]any)["limit"],
				"prev_memory_request": allocatedMemRequest,
				"prev_memory_limit":   allocatedMemLimit,
			}
			contianerAnnotation := getAnnotationDict(key, annotation)
			containerAnnotations = append(containerAnnotations, contianerAnnotation)
			containers = append(containers, map[string]any{
				"cpu_limit":      resourceData["cpu"].(map[string]any)["limit"],
				"cpu_request":    resourceData["cpu"].(map[string]any)["request"],
				"memory_limit":   resourceData["memory"].(map[string]any)["limit"],
				"container_name": key,
				"memory_request": resourceData["memory"].(map[string]any)["request"],
			})
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

		taskId, err := generateAgentTask(ctx, request.Recommendation, "rightsizing_resource", map[string]any{
			"action_name": "rightsizing_resource",
			"action_params": map[string]any{
				"kind":        resourceMeta["controllerKind"],
				"name":        resourceMeta["controller"],
				"namespace":   resourceMeta["namespace"],
				"containers":  containers,
				"annotations": map[string]string{"recommendation_apply.vertical-scaler": string(annotationJson)},
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
