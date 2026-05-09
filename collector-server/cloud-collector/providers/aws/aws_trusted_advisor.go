package aws

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/support"
)

const ServiceNameTrustedAdvisor = "TrustedAdvisor"

type awsTrustedAdvisor struct {
	DefaultAwsServiceImpl
}

func (a *awsTrustedAdvisor) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (a *awsTrustedAdvisor) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, nil
}

func (a *awsTrustedAdvisor) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for trusted advisor", "error", err)
		return nil, err
	}

	// Trusted Advisor API is only available in us-east-1
	cfg, err = awsconfig.LoadDefaultConfig(ctx.GetContext(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(cfg.Credentials),
	)
	if err != nil {
		ctx.GetLogger().Error("failed to create us-east-1 config for trusted advisor", "error", err)
		return nil, err
	}

	client := support.NewFromConfig(cfg)

	// List all checks
	checksOutput, err := client.DescribeTrustedAdvisorChecks(ctx.GetContext(), &support.DescribeTrustedAdvisorChecksInput{
		Language: aws.String("en"),
	})
	if err != nil {
		// Gracefully handle AccessDenied — requires Business/Enterprise support plan
		if isAccessDeniedError(err) {
			ctx.GetLogger().Info("trusted advisor not available (requires Business/Enterprise support plan)")
			return recommendations, nil
		}
		ctx.GetLogger().Warn("failed to list trusted advisor checks", "error", err)
		return recommendations, nil
	}

	// Filter to cost optimization and performance checks (most relevant for recommendations)
	relevantCategories := map[string]bool{
		"cost_optimizing": true,
		"performance":     true,
		"fault_tolerance": true,
		"security":        true,
		"service_limits":  true,
	}

	for _, check := range checksOutput.Checks {
		if !relevantCategories[*check.Category] {
			continue
		}

		resultOutput, err := client.DescribeTrustedAdvisorCheckResult(ctx.GetContext(), &support.DescribeTrustedAdvisorCheckResultInput{
			CheckId: check.Id,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get trusted advisor check result", "checkId", *check.Id, "error", err)
			continue
		}

		if resultOutput.Result == nil {
			continue
		}

		result := resultOutput.Result

		// Skip checks with no flagged resources
		if len(result.FlaggedResources) == 0 {
			continue
		}

		// Skip checks that are OK (green)
		if result.Status != nil && *result.Status == "ok" {
			continue
		}

		category := mapTACategory(*check.Category)
		severity := mapTAStatusToSeverity(result.Status)

		for _, flaggedResource := range result.FlaggedResources {
			if flaggedResource.Status != nil && *flaggedResource.Status == "ok" {
				continue
			}

			data := map[string]any{
				"source":      "aws",
				"check_id":    *check.Id,
				"check_name":  *check.Name,
				"ta_category": *check.Category,
				"ta_status":   stringValue(result.Status),
			}

			// Map metadata fields using check.Metadata as column headers
			if check.Metadata != nil && flaggedResource.Metadata != nil {
				for i, header := range check.Metadata {
					if i < len(flaggedResource.Metadata) {
						data[sanitizeMetadataKey(*header)] = flaggedResource.Metadata[i]
					}
				}
			}

			resourceId := ""
			if flaggedResource.ResourceId != nil {
				resourceId = *flaggedResource.ResourceId
			}

			region := "global"
			if regionVal, ok := data["region"]; ok {
				if regionStr, ok := regionVal.(string); ok && regionStr != "" && regionStr != "-" {
					region = regionStr
				}
			}

			savings := extractTASavings(data)

			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        category,
				RuleName:            fmt.Sprintf("aws_native_ta_%s", *check.Id),
				Severity:            severity,
				Savings:             savings,
				Action:              providers.RecommendationActionModify,
				Data:                data,
				ResourceServiceName: ServiceNameTrustedAdvisor,
				ResourceId:          resourceId,
				ResourceType:        "trusted-advisor-check",
				ResourceRegion:      region,
			})
		}
	}

	ctx.GetLogger().Info("fetched trusted advisor recommendations", "count", len(recommendations))
	return recommendations, nil
}

func mapTACategory(category string) providers.RecommendationCategory {
	switch category {
	case "cost_optimizing":
		return providers.RecommendationCategoryRightSizing
	case "security":
		return providers.RecommendationCategorySecurity
	case "fault_tolerance":
		return providers.RecommendationCategoryConfiguration
	case "performance":
		return providers.RecommendationCategoryConfiguration
	case "service_limits":
		return providers.RecommendationCategoryConfiguration
	default:
		return providers.RecommendationCategoryConfiguration
	}
}

func mapTAStatusToSeverity(status *string) providers.RecommendationSeverity {
	if status == nil {
		return providers.RecommendationSeverityMedium
	}
	switch *status {
	case "error":
		return providers.RecommendationSeverityHigh
	case "warning":
		return providers.RecommendationSeverityMedium
	default:
		return providers.RecommendationSeverityLow
	}
}

func sanitizeMetadataKey(key string) string {
	// Convert header names like "Estimated Monthly Savings" to snake_case
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, " ", "_")
	key = strings.ReplaceAll(key, "(", "")
	key = strings.ReplaceAll(key, ")", "")
	key = strings.ReplaceAll(key, "/", "_")
	return key
}

func extractTASavings(data map[string]any) float64 {
	// Try common savings field names from Trusted Advisor metadata
	savingsKeys := []string{
		"estimated_monthly_savings",
		"estimated_monthly_savings_$",
		"monthly_savings",
	}
	for _, key := range savingsKeys {
		if v, ok := data[key]; ok {
			if s, ok := v.(string); ok {
				if val, err := parseFloat64(s); err == nil {
					return val
				}
			}
		}
	}
	return 0
}

func isAccessDeniedError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "AccessDeniedException") ||
		strings.Contains(errStr, "SubscriptionRequiredException") ||
		strings.Contains(errStr, "not subscribed")
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (a *awsTrustedAdvisor) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return nil
}

func (a *awsTrustedAdvisor) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, nil
}

func (a *awsTrustedAdvisor) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (a *awsTrustedAdvisor) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, nil
}
