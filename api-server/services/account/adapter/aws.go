package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	for k, v := range storedRecommendationData(request.Recommendation) {
		mergedData[k] = v
	}

	// Then overlay user-provided data (allows overrides like custom reason)
	// But skip the custom_* alarm overrides as they need special handling
	for k, v := range request.Data {
		if k != "custom_alarm_name" && k != "custom_threshold" && k != "custom_notification_targets" {
			mergedData[k] = v
		}
	}

	// Handle custom_alarm_name and custom_threshold by deep-merging into alarm_config
	// This preserves all other alarm_config fields while allowing customization
	responseData := request.Data
	if nonAlarmApplyRules[request.Recommendation.RuleName] {
		// A change already running for this recommendation is not started again: the
		// collector would refuse it, and that refusal would fail the attempt being polled.
		for _, existing := range existingRecommendations {
			if existing.Status == models.RecommendationResolutionStatusInProgress {
				return ApplyRecommendationResponse{
					Data:                     storedRecommendationData(request.Recommendation),
					Status:                   RecommendationResolutionStatusInProgress,
					ResolutionType:           RecommendationResolutionTypeCloudResource,
					ResolutionTypeRefrenceId: existing.TypeReferenceId,
					StatusMessage:            "This change is already running.",
				}, nil
			}
		}
		// Apply exactly what was recommended: the stored payload names the resource the
		// change targets, so caller-supplied data is not overlaid onto it.
		mergedData = storedRecommendationData(request.Recommendation)
		responseData = mergedData
	} else if alarmConfigRaw, ok := mergedData["alarm_config"]; ok {
		if alarmConfigMap, ok := alarmConfigRaw.(map[string]interface{}); ok {
			if customName, ok := request.Data["custom_alarm_name"]; ok {
				alarmConfigMap["alarm_name"] = customName
			}
			if customThreshold, ok := request.Data["custom_threshold"]; ok {
				alarmConfigMap["threshold"] = customThreshold
			}
			if customTargets, ok := request.Data["custom_notification_targets"]; ok {
				alarmConfigMap["notification_targets"] = customTargets
			}
			ctx.GetLogger().Info("Merged custom alarm parameters into alarm_config",
				"custom_alarm_name", request.Data["custom_alarm_name"],
				"custom_threshold", request.Data["custom_threshold"],
				"alarm_config_keys", len(alarmConfigMap))
		} else {
			ctx.GetLogger().Warn("alarm_config in recommendation data is not of expected type map[string]interface{}", "type", fmt.Sprintf("%T", alarmConfigRaw))
		}
	} else {
		ctx.GetLogger().Error("recommendation has no alarm_config and its rule cannot be applied",
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
			StatusMessage: "This recommendation can't be applied from NudgeBee yet.",
		}, fmt.Errorf("rule %s cannot be applied: no alarm_config and not an applicable rule", request.Recommendation.RuleName)
	}

	if serviceName == "" {
		serviceName, _ = mergedData["service_name"].(string)
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

	ctx.GetLogger().Info("Sending apply request to cloud-collector",
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
			"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
			"x-user-id":   ctx.GetSecurityContext().GetUserId(),
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
			InProgress      bool   `json:"in_progress"`
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

	// Alarms are created synchronously, so they settle now. An apply the collector only
	// initiated stays InProgress until GetRecommendationResolutionStatus sees it finish.
	status := RecommendationResolutionStatusSuccess
	if cloudCollectorResp.Data.InProgress {
		status = RecommendationResolutionStatusInProgress
	}
	return ApplyRecommendationResponse{
		Data:                     responseData,
		Status:                   status,
		ResolutionType:           RecommendationResolutionTypeCloudResource,
		ResolutionTypeRefrenceId: referenceId,
		StatusMessage:            cloudCollectorResp.Data.Message,
	}, nil
}

// GetRecommendationResolutionStatus checks the status of a cloud recommendation resolution.
// Alarms are created synchronously, so their resolutions are already settled. A rule in
// nonAlarmApplyRules may have been initiated asynchronously, so the collector is asked
// where the change has got to.
func (a *awsAdapter) GetRecommendationResolutionStatus(ctx AccountAdapterContext, recommendation models.Recommendation, resolutionReferenceId string, applyRequestPayload models.Json, resolutionStatusMessage string) (GetRecommendationResolutionStatusResponse, error) {
	ctx.GetLogger().Debug("Checking cloud recommendation resolution status",
		"reference_id", resolutionReferenceId,
		"rule_name", recommendation.RuleName)

	if !nonAlarmApplyRules[recommendation.RuleName] {
		return GetRecommendationResolutionStatusResponse{
			Status:        RecommendationResolutionStatusSuccess,
			StatusMessage: resolutionStatusMessage,
		}, nil
	}

	data := storedRecommendationData(recommendation)
	serviceName, _ := data["service_name"].(string)
	resourceId := ""
	if recommendation.AccountObjectId != nil {
		resourceId = *recommendation.AccountObjectId
	}
	payload := map[string]interface{}{
		"account_id":        recommendation.CloudAccountId,
		"recommendation_id": recommendation.Id,
		"data":              data,
		"service_name":      serviceName,
		"resource_id":       resourceId,
		"rule_name":         recommendation.RuleName,
	}

	resp, err := common.HttpPost(
		config.Config.CloudCollectorServerUrl+"/v1/cloud/apply_recommendation_status",
		common.HttpWithJsonBody(payload),
		common.HttpWithHeaders(map[string]string{
			config.Config.CloudCollectorServerTokenHeader: config.Config.CloudCollectorServerToken,
			"x-tenant-id": ctx.GetSecurityContext().GetTenantId(),
			"x-user-id":   ctx.GetSecurityContext().GetUserId(),
		}),
	)
	if err != nil {
		return unverifiedCloudStatus(err.Error()), nil
	}
	defer func() { _ = resp.Body.Close() }()

	var statusResp struct {
		Data struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return unverifiedCloudStatus(err.Error()), nil
	}
	parseErr := json.Unmarshal(bodyBytes, &statusResp)
	if parseErr == nil && len(statusResp.Errors) > 0 && statusResp.Errors[0].Message != "" {
		return unverifiedCloudStatus(statusResp.Errors[0].Message), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return unverifiedCloudStatus(fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))), nil
	}
	if parseErr != nil {
		return unverifiedCloudStatus(parseErr.Error()), nil
	}

	switch statusResp.Data.Status {
	case "Succeeded":
		return GetRecommendationResolutionStatusResponse{Status: RecommendationResolutionStatusSuccess, StatusMessage: statusResp.Data.Message}, nil
	case "Failed":
		return GetRecommendationResolutionStatusResponse{Status: RecommendationResolutionStatusFailed, StatusMessage: statusResp.Data.Message}, nil
	case "InProgress":
		return GetRecommendationResolutionStatusResponse{Status: RecommendationResolutionStatusInProgress, StatusMessage: statusResp.Data.Message}, nil
	default:
		return unverifiedCloudStatus(fmt.Sprintf("unexpected status %q from cloud-collector", statusResp.Data.Status)), nil
	}
}

// nonAlarmApplyRules are the recommendations without alarm_config that may be applied
// from NudgeBee. Every other non-alarm rule is refused before reaching the collector.
var nonAlarmApplyRules = map[string]bool{
	"aws_ec2_ebs_generation_upgrade": true,
}

// storedRecommendationData is the recommendation payload as stored, without any
// caller-supplied overrides.
func storedRecommendationData(recommendation models.Recommendation) map[string]interface{} {
	data := make(map[string]interface{})
	if recommendation.Recommendation.IsArray() {
		return data
	}
	if recMap, ok := recommendation.Recommendation.Object().(map[string]interface{}); ok {
		for k, v := range recMap {
			data[k] = v
		}
	}
	return data
}

// unverifiedCloudStatus is the status check's answer when the collector could not be
// asked or did not answer: still in progress, with the reason shown.
func unverifiedCloudStatus(reason string) GetRecommendationResolutionStatusResponse {
	return GetRecommendationResolutionStatusResponse{
		Status:        RecommendationResolutionStatusInProgress,
		StatusMessage: truncateForStatus("Could not check the change in the cloud account: " + reason),
		Unverified:    true,
	}
}
