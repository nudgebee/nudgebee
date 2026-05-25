package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database/models"
)

type awsAdapter struct {
}

// ApplyRecommendation applies an AWS recommendation by calling the cloud-collector service
func (a *awsAdapter) ApplyRecommendation(ctx AccountAdapterContext, request ApplyRecommendationRequest, existingRecommendations []models.RecommendationResolution, recommendResolutionId string) (ApplyRecommendationResponse, error) {
	// Build the request payload for cloud-collector
	serviceName := ""
	if request.Resource.ServiceName != nil {
		serviceName = *request.Resource.ServiceName
	}

	// Merge recommendation data (which contains alarm_config) with user-provided data
	// The recommendation.Recommendation field contains the actual recommendation details from cloud-collector
	mergedData := make(map[string]interface{})

	// First, copy the recommendation data from database (contains alarm_config, thresholds, etc.)
	if !request.Recommendation.Recommendation.IsArray() {
		if recObj := request.Recommendation.Recommendation.Object(); recObj != nil {
			if recMap, ok := recObj.(map[string]interface{}); ok {
				for k, v := range recMap {
					mergedData[k] = v
				}
			}
		}
	}

	// Then overlay user-provided data (allows overrides like custom reason)
	// But skip custom_alarm_name and custom_threshold as they need special handling
	for k, v := range request.Data {
		if k != "custom_alarm_name" && k != "custom_threshold" {
			mergedData[k] = v
		}
	}

	// Handle custom_alarm_name and custom_threshold by deep-merging into alarm_config
	// This preserves all other alarm_config fields while allowing customization
	if alarmConfigRaw, ok := mergedData["alarm_config"]; ok {
		if alarmConfigMap, ok := alarmConfigRaw.(map[string]interface{}); ok {
			if customName, ok := request.Data["custom_alarm_name"]; ok {
				alarmConfigMap["alarm_name"] = customName
			}
			if customThreshold, ok := request.Data["custom_threshold"]; ok {
				alarmConfigMap["threshold"] = customThreshold
			}
			ctx.GetLogger().Info("Merged custom alarm parameters into alarm_config",
				"custom_alarm_name", request.Data["custom_alarm_name"],
				"custom_threshold", request.Data["custom_threshold"],
				"alarm_config_keys", len(alarmConfigMap))
		} else {
			ctx.GetLogger().Warn("alarm_config in recommendation data is not of expected type map[string]interface{}", "type", fmt.Sprintf("%T", alarmConfigRaw))
		}
	} else {
		// CRITICAL: alarm_config is missing from recommendation data
		ctx.GetLogger().Error("alarm_config not found in recommendation data - cannot create alarm",
			"recommendation_id", request.Recommendation.Id,
			"rule_name", request.Recommendation.RuleName,
			"resource_id", request.Recommendation.ResourceId,
			"available_keys", func() []string {
				keys := make([]string, 0, len(mergedData))
				for k := range mergedData {
					keys = append(keys, k)
				}
				return keys
			}())
		return ApplyRecommendationResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: "Alarm configuration missing from recommendation data. Please ensure the recommendation was properly stored with alarm_config.",
		}, fmt.Errorf("alarm_config not found in recommendation data for rule %s", request.Recommendation.RuleName)
	}

	// Use the external resource ID (full cloud resource path like Azure /subscriptions/... or GCP projects/...)
	// instead of the database UUID. The cloud-collector needs the full path to extract
	// subscription ID, resource group (Azure) or project ID (GCP).
	resourceId := request.Resource.ExternalResourceId
	if resourceId == "" && request.Recommendation.AccountObjectId != nil {
		resourceId = *request.Recommendation.AccountObjectId
	}

	payload := map[string]interface{}{
		"account_id":        request.Recommendation.CloudAccountId,
		"recommendation_id": request.Recommendation.Id,
		"data":              mergedData,
		"service_name":      serviceName,
		"resource_id":       resourceId,
		"rule_name":         request.Recommendation.RuleName,
		"resource_region":   request.Resource.Region,
	}

	ctx.GetLogger().Info("Sending alarm creation request to cloud-collector",
		"account_id", request.Recommendation.CloudAccountId,
		"rule_name", request.Recommendation.RuleName,
		"resource_id", resourceId,
		"service_name", serviceName,
		"has_alarm_config", mergedData["alarm_config"] != nil)

	// Call cloud-collector HTTP endpoint
	resp, err := common.HttpPost(
		config.Config.CloudCollectorServerUrl+"/v1/cloud/apply_recommendation",
		common.HttpWithJsonBody(payload),
		common.HttpWithHeaders(map[string]string{
			config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
			"X-Hasura-User-Tenant-Id":                     ctx.GetSecurityContext().GetTenantId(),
			"X-Hasura-User-Id":                            ctx.GetSecurityContext().GetUserId(),
		}),
	)

	if err != nil {
		ctx.GetLogger().Error("Failed to call cloud-collector apply_recommendation endpoint",
			"error", err,
			"account_id", request.Recommendation.CloudAccountId,
			"rule_name", request.Recommendation.RuleName)
		return ApplyRecommendationResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: fmt.Sprintf("Failed to apply recommendation: %v", err),
		}, err
	}

	// Parse the response
	var cloudCollectorResp struct {
		Data struct {
			Success         bool   `json:"success"`
			Message         string `json:"message"`
			ReferenceId     string `json:"reference_id"`
			ResourceCreated bool   `json:"resource_created"`
			Error           string `json:"error"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	defer func() { _ = resp.Body.Close() }()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("Failed to read cloud-collector response body", "error", err)
		return ApplyRecommendationResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: fmt.Sprintf("Failed to read response: %v", err),
		}, err
	}

	err = json.Unmarshal(bodyBytes, &cloudCollectorResp)
	if err != nil {
		ctx.GetLogger().Error("Failed to parse cloud-collector response", "error", err)
		return ApplyRecommendationResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: fmt.Sprintf("Failed to parse response: %v", err),
		}, err
	}

	// Check for errors in response
	if len(cloudCollectorResp.Errors) > 0 {
		errorMsg := cloudCollectorResp.Errors[0].Message
		ctx.GetLogger().Error("Cloud-collector returned error", "error", errorMsg)
		return ApplyRecommendationResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: errorMsg,
		}, fmt.Errorf("cloud-collector error: %s", errorMsg)
	}

	if !cloudCollectorResp.Data.Success {
		errorMsg := cloudCollectorResp.Data.Error
		if errorMsg == "" {
			errorMsg = "Unknown error from cloud-collector"
		}
		ctx.GetLogger().Error("Cloud-collector reported failure", "error", errorMsg)
		return ApplyRecommendationResponse{
			Status:        RecommendationResolutionStatusFailed,
			StatusMessage: errorMsg,
		}, fmt.Errorf("recommendation application failed: %s", errorMsg)
	}

	// Success! Extract the reference ID (alarm ARN/name)
	referenceId := cloudCollectorResp.Data.ReferenceId
	if referenceId == "" {
		// Fallback: construct a reference ID from the recommendation
		referenceId = fmt.Sprintf("%s-%s", request.Recommendation.RuleName, resourceId)
	}

	ctx.GetLogger().Info("Successfully applied cloud recommendation",
		"rule_name", request.Recommendation.RuleName,
		"resource_id", resourceId,
		"reference_id", referenceId)

	// For CloudWatch alarms, we can mark as Success immediately since the operation is synchronous
	// The alarm was either created successfully or failed - there's no async processing
	return ApplyRecommendationResponse{
		Data:                     request.Data,
		Status:                   RecommendationResolutionStatusSuccess,
		ResolutionType:           RecommendationResolutionTypeCloudResource,
		ResolutionTypeRefrenceId: referenceId,
		StatusMessage:            cloudCollectorResp.Data.Message,
	}, nil
}

// GetRecommendationResolutionStatus checks the status of an AWS recommendation resolution
// For AWS CloudWatch alarms, this is mostly a pass-through since alarms are created synchronously
func (a *awsAdapter) GetRecommendationResolutionStatus(ctx AccountAdapterContext, recommendation models.Recommendation, resolutionReferenceId string, applyRequestPayload models.Json, resolutionStatusMessage string) (GetRecommendationResolutionStatusResponse, error) {
	// For CloudWatch alarms and other AWS resources that are created synchronously,
	// if we got here, the resource was already created successfully during ApplyRecommendation
	// We could optionally verify the alarm still exists by calling DescribeAlarms via cloud-collector,
	// but for now we'll just return Success

	ctx.GetLogger().Debug("Checking AWS recommendation resolution status",
		"reference_id", resolutionReferenceId,
		"rule_name", recommendation.RuleName)

	// Since AWS operations are synchronous and we marked them as Success immediately,
	// this status check is just confirming the success
	return GetRecommendationResolutionStatusResponse{
		Status:        RecommendationResolutionStatusSuccess,
		StatusMessage: resolutionStatusMessage,
	}, nil
}
