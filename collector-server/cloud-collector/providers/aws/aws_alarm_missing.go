package aws

import (
	"errors"
	"fmt"
	"maps"
	"nudgebee/collector/cloud/providers"
	"strings"
)

// alarmMissingRecommendations recommends each of the service's alarm templates
// (alarm_templates/<templateService>.yaml) that applies to the resource and has no
// matching alarm in its Meta["AlarmDetails"]. The dimension identifies the resource
// to CloudWatch: it scopes the check and the alarm that Create Alarm makes. data
// holds the service's own fields for the recommendation payload.
//
// A resource with no alarm listing gets an unevaluated placeholder per template, so
// a failed alarm listing keeps its recommendations as they were instead of either
// recommending every alarm or archiving them.
func alarmMissingRecommendations(ctx providers.CloudProviderContext, templateService string, resource providers.Resource, dimension providers.AlarmDimension, data map[string]any) []providers.Recommendation {
	templates, err := LoadAlarmTemplates(templateService)
	if err != nil {
		ctx.GetLogger().Warn("Failed to load alarm templates", "error", err, "service", templateService)
		return nil
	}

	var recommendations []providers.Recommendation
	for _, template := range templates {
		if !ShouldRecommendAlarm(resource, template) {
			continue
		}

		isMissing, err := IsAlarmMissing(resource, template, dimension.Value)
		if errors.Is(err, ErrAlarmDetailsUnknown) {
			recommendations = append(recommendations, providers.UnevaluatedRecommendation(template.Name, providers.RecommendationCategoryConfiguration, resource))
			continue
		}
		if err != nil {
			ctx.GetLogger().Warn("Error checking alarm", "error", err, "template", template.Name, "resourceId", resource.Id)
			continue
		}
		if !isMissing {
			continue
		}

		threshold, err := CalculateThreshold(resource, template)
		if err != nil {
			ctx.GetLogger().Warn("Error calculating threshold", "error", err, "template", template.Name, "resourceId", resource.Id)
			continue
		}

		alarmConfig := providers.AlarmCreationConfig{
			AlarmName:          fmt.Sprintf("%s-%s", template.Name, resource.Id),
			MetricName:         template.Configuration.MetricName,
			Namespace:          template.Configuration.Namespace,
			Statistic:          template.Configuration.Statistic,
			Period:             template.Configuration.Period,
			EvaluationPeriods:  template.Configuration.EvaluationPeriods,
			DatapointsToAlarm:  template.Configuration.DatapointsToAlarm,
			Threshold:          threshold,
			ComparisonOperator: template.Configuration.ComparisonOperator,
			TreatMissingData:   template.Configuration.TreatMissingData,
			Dimensions:         []providers.AlarmDimension{dimension},
		}

		recommendationData := map[string]any{
			"metric_name":  template.Configuration.MetricName,
			"threshold":    threshold,
			"alarm_config": alarmConfig,
			"alarm_type":   template.AlarmType,
			"reason":       template.Description,
		}
		maps.Copy(recommendationData, data)

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            template.Name,
			Severity:            providers.RecommendationSeverityFromString(template.Severity),
			Savings:             0,
			Data:                recommendationData,
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}
	return recommendations
}

// applyAlarmMissingRecommendation creates the CloudWatch alarm carried by one of the
// service's alarm-missing recommendations, whose rule names start with rulePrefix.
// Any other rule is errors.ErrUnsupported.
func applyAlarmMissingRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation, rulePrefix string) error {
	if !strings.HasPrefix(recommendation.RuleName, rulePrefix) || !strings.HasSuffix(recommendation.RuleName, "_alarm_missing") {
		return errors.ErrUnsupported
	}

	err := CreateCloudWatchAlarmFromRecommendation(ctx.GetContext(), account, recommendation)
	if err != nil {
		ctx.GetLogger().Error("Failed to create CloudWatch alarm", "error", err, "ruleName", recommendation.RuleName, "resourceId", recommendation.ResourceId)
		return fmt.Errorf("failed to create CloudWatch alarm: %w", err)
	}
	ctx.GetLogger().Info("Successfully created CloudWatch alarm", "ruleName", recommendation.RuleName, "resourceId", recommendation.ResourceId)
	return nil
}
