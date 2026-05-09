package account

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
)

// ApplyRecommendationResponse contains the result of applying a recommendation
type ApplyRecommendationResponse struct {
	Success         bool   `json:"success"`
	Message         string `json:"message"`
	ReferenceId     string `json:"reference_id,omitempty"` // ARN or identifier of created resource
	ResourceCreated bool   `json:"resource_created"`       // True if new resource was created
	Error           string `json:"error,omitempty"`        // Error message if failed
}

// ApplyRecommendation applies a recommendation by calling the appropriate cloud provider's ApplyRecommendation method
func ApplyRecommendation(ctx *security.RequestContext, accountId string, recommendation providers.Recommendation) (ApplyRecommendationResponse, error) {
	// Fetch account details from database
	account, providerName, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("Failed to get account", "error", err, "accountId", accountId)
		return ApplyRecommendationResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to get account: %v", err),
		}, err
	}

	// Get the cloud provider implementation
	cloudProvider, ok := providers.GetProvider(providerName)
	if !ok {
		ctx.GetLogger().Error("Failed to get cloud provider", "provider", providerName)
		return ApplyRecommendationResponse{
			Success: false,
			Error:   fmt.Sprintf("Cloud provider %s not found", providerName),
		}, fmt.Errorf("provider not found: %s", providerName)
	}

	// Create cloud provider context
	cloudCtx := providers.NewCloudProviderContextWithLogger(ctx.GetContext(), ctx.GetLogger())

	// Call the cloud provider's ApplyRecommendation method
	err = cloudProvider.ApplyRecommendation(cloudCtx, account, recommendation)
	if err != nil {
		ctx.GetLogger().Error("Failed to apply recommendation",
			"error", err,
			"ruleName", recommendation.RuleName,
			"resourceId", recommendation.ResourceId,
			"service", recommendation.ResourceServiceName)
		return ApplyRecommendationResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to apply recommendation: %v", err),
		}, err
	}

	// Extract reference ID from recommendation data (e.g., alarm ARN)
	referenceId := ""
	if alarmConfig, ok := recommendation.Data["alarm_config"].(map[string]interface{}); ok {
		if alarmName, ok := alarmConfig["alarm_name"].(string); ok {
			referenceId = alarmName
		}
	}

	// If we couldn't get it from alarm_config, try to construct it from the recommendation
	if referenceId == "" {
		referenceId = fmt.Sprintf("%s-%s", recommendation.RuleName, recommendation.ResourceId)
	}

	ctx.GetLogger().Info("Successfully applied recommendation",
		"ruleName", recommendation.RuleName,
		"resourceId", recommendation.ResourceId,
		"service", recommendation.ResourceServiceName,
		"referenceId", referenceId)

	// Trigger targeted resource re-sync so the new alarm shows in AlarmDetails immediately
	if recommendation.ResourceServiceName != "" && recommendation.ResourceRegion != "" {
		go func() {
			bgCtx := security.NewRequestContext(
				context.Background(),
				ctx.GetSecurityContext(),
				ctx.GetLogger(),
				ctx.GetTracer(),
				ctx.GetMeter(),
			)
			_, err := StoreResources(bgCtx, accountId, recommendation.ResourceServiceName, recommendation.ResourceRegion)
			if err != nil {
				bgCtx.GetLogger().Warn("failed to sync resources after recommendation apply",
					"error", err,
					"service", recommendation.ResourceServiceName,
					"region", recommendation.ResourceRegion)
			}
		}()
	}

	return ApplyRecommendationResponse{
		Success:         true,
		Message:         "Recommendation applied successfully",
		ReferenceId:     referenceId,
		ResourceCreated: true,
	}, nil
}
