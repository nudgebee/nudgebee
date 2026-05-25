package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// AlarmCheckResult contains the result of checking if an alarm exists
type AlarmCheckResult struct {
	AlarmExists bool
	AlarmName   string
	AlarmState  string
}

// IsAlarmMissing checks if an alarm matching the template exists for the resource
// Returns true if the alarm is missing (should be recommended)
func IsAlarmMissing(resource providers.Resource, template providers.AlarmTemplate, dimensionValue string) (bool, error) {
	alarmDetails, ok := resource.Meta["AlarmDetails"]
	if !ok {
		// No alarms configured at all - alarm is missing
		return true, nil
	}

	alarmArray, ok := alarmDetails.([]interface{})
	if !ok {
		// Invalid alarm details format - treat as missing
		return true, nil
	}

	if len(alarmArray) == 0 {
		// No alarms configured - alarm is missing
		return true, nil
	}

	// Check each existing alarm to see if it matches our template
	for _, alarmInterface := range alarmArray {
		exists, err := doesAlarmMatchTemplate(alarmInterface, template, dimensionValue)
		if err != nil {
			// Log error but continue checking other alarms
			continue
		}
		if exists {
			// Found matching alarm - not missing
			return false, nil
		}
	}

	// No matching alarm found - alarm is missing
	return true, nil
}

// doesAlarmMatchTemplate checks if an individual alarm matches the template criteria
// Handles both simple metric alarms and metric math alarms
func doesAlarmMatchTemplate(alarmInterface interface{}, template providers.AlarmTemplate, dimensionValue string) (bool, error) {
	var alarm types.MetricAlarm

	// Try type assertion first (most efficient)
	if a, ok := alarmInterface.(types.MetricAlarm); ok {
		alarm = a
	} else {
		// Fall back to JSON marshal/unmarshal for map[string]interface{} or other types
		alarmBytes, err := json.Marshal(alarmInterface)
		if err != nil {
			return false, fmt.Errorf("failed to marshal alarm: %w", err)
		}

		if err := json.Unmarshal(alarmBytes, &alarm); err != nil {
			return false, fmt.Errorf("failed to unmarshal alarm: %w", err)
		}
	}

	// Get the expected dimension name for this namespace
	expectedDimensionName := getDimensionNameForNamespace(template.Configuration.Namespace)
	if expectedDimensionName == "" {
		// Unknown namespace - can't validate dimensions
		return false, fmt.Errorf("unknown namespace: %s", template.Configuration.Namespace)
	}

	// Check if this is a metric math alarm (has Metrics array) or simple metric alarm
	if len(alarm.Metrics) > 0 {
		// Metric Math Alarm (e.g., ALB HTTP error rate)
		// Check if any of the metrics in the array match our template
		return matchMetricMathAlarm(alarm, template, expectedDimensionName, dimensionValue)
	}

	// Simple Metric Alarm (e.g., EC2 CPU, RDS memory)
	// Check namespace match
	if alarm.Namespace == nil || *alarm.Namespace != template.Configuration.Namespace {
		return false, nil
	}

	// Check metric name match
	if alarm.MetricName == nil || *alarm.MetricName != template.Configuration.MetricName {
		return false, nil
	}

	// Check dimensions match (both name and value)
	if !dimensionsMatch(alarm.Dimensions, expectedDimensionName, dimensionValue) {
		return false, nil
	}

	// All criteria match - alarm exists
	return true, nil
}

// matchMetricMathAlarm checks if a metric math alarm matches the template
// Metric math alarms have a Metrics array with multiple MetricDataQuery entries
func matchMetricMathAlarm(alarm types.MetricAlarm, template providers.AlarmTemplate, expectedDimensionName string, dimensionValue string) (bool, error) {
	// For metric math alarms, we need to check if any of the data metrics (not expressions)
	// match our namespace, metric name, and dimensions
	for _, metric := range alarm.Metrics {
		if metric.MetricStat == nil {
			// This is an expression, not a data metric - skip it
			continue
		}

		metricStat := metric.MetricStat
		if metricStat.Metric == nil {
			continue
		}

		// Check if this metric matches our template
		if metricStat.Metric.Namespace != nil &&
			*metricStat.Metric.Namespace == template.Configuration.Namespace &&
			metricStat.Metric.MetricName != nil &&
			*metricStat.Metric.MetricName == template.Configuration.MetricName {

			// Check if dimensions match
			if dimensionsMatch(metricStat.Metric.Dimensions, expectedDimensionName, dimensionValue) {
				// Found a matching metric in the metric math alarm
				return true, nil
			}
		}
	}

	// No matching metrics found in the metric math alarm
	return false, nil
}

// getDimensionNameForNamespace returns the expected CloudWatch dimension name for a given namespace
// This mapping is based on AWS CloudWatch dimension conventions for each service
func getDimensionNameForNamespace(namespace string) string {
	switch namespace {
	case "AWS/EC2":
		return "InstanceId"
	case "AWS/RDS":
		return "DBInstanceIdentifier"
	case "AWS/ElastiCache":
		return "CacheClusterId"
	case "AWS/ELB":
		return "LoadBalancerName"
	case "AWS/ApplicationELB", "AWS/NetworkELB":
		return "LoadBalancer"
	case "AWS/Lambda":
		return "FunctionName"
	case "AWS/DynamoDB":
		return "TableName"
	case "AWS/S3":
		return "BucketName"
	case "AWS/SQS":
		return "QueueName"
	case "AWS/SNS":
		return "TopicName"
	default:
		return ""
	}
}

// dimensionsMatch checks if alarm dimensions contain the expected resource identifier
// It checks BOTH the dimension name AND value to ensure correct matching
// For EC2: checks InstanceId dimension
// For RDS: checks DBInstanceIdentifier dimension
// For ElastiCache: checks CacheClusterId dimension
// For Load Balancers: checks LoadBalancerName or LoadBalancer dimension
func dimensionsMatch(dimensions []types.Dimension, expectedName string, expectedValue string) bool {
	if len(dimensions) == 0 || expectedName == "" {
		return false
	}

	for _, dim := range dimensions {
		if dim.Value == nil || dim.Name == nil {
			continue
		}

		// Check if BOTH dimension name and value match
		// This prevents false matches where different resources might have same value
		if *dim.Name == expectedName && *dim.Value == expectedValue {
			return true
		}
	}

	return false
}

// GetAlarmCheckResults returns detailed information about all alarms for a resource
func GetAlarmCheckResults(resource providers.Resource, templates []providers.AlarmTemplate, dimensionValue string) []AlarmCheckResult {
	results := make([]AlarmCheckResult, 0, len(templates))

	for _, template := range templates {
		result := AlarmCheckResult{
			AlarmExists: false,
			AlarmName:   template.Name,
			AlarmState:  "MISSING",
		}

		isMissing, err := IsAlarmMissing(resource, template, dimensionValue)
		if err == nil && !isMissing {
			result.AlarmExists = true
			result.AlarmState = getAlarmState(resource, template, dimensionValue)
		}

		results = append(results, result)
	}

	return results
}

// getAlarmState retrieves the current state of a matching alarm (OK, ALARM, INSUFFICIENT_DATA)
func getAlarmState(resource providers.Resource, template providers.AlarmTemplate, dimensionValue string) string {
	alarmDetails, ok := resource.Meta["AlarmDetails"]
	if !ok {
		return "UNKNOWN"
	}

	alarmArray, ok := alarmDetails.([]interface{})
	if !ok {
		return "UNKNOWN"
	}

	for _, alarmInterface := range alarmArray {
		// Check if this alarm matches our template
		matches, err := doesAlarmMatchTemplate(alarmInterface, template, dimensionValue)
		if err != nil {
			continue
		}

		if !matches {
			continue
		}

		// Found matching alarm - extract its state
		var alarm types.MetricAlarm

		// Try type assertion first (most efficient)
		if a, ok := alarmInterface.(types.MetricAlarm); ok {
			alarm = a
		} else {
			// Fall back to JSON marshal/unmarshal
			alarmBytes, err := json.Marshal(alarmInterface)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(alarmBytes, &alarm); err != nil {
				continue
			}
		}

		if alarm.StateValue != "" {
			return string(alarm.StateValue)
		}
		return "UNKNOWN"
	}

	return "UNKNOWN"
}

// ShouldRecommendAlarm determines if we should create a recommendation for a missing alarm
// Uses generic rule evaluator for CONDITIONAL metrics
func ShouldRecommendAlarm(resource providers.Resource, template providers.AlarmTemplate) bool {
	// Use the generic rule evaluator
	evaluator := providers.NewRuleEvaluator(nil)
	return evaluator.ShouldRecommendAlarm(resource, template)
}
