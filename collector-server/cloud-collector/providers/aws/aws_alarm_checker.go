package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// AlarmCheckResult contains the result of checking if an alarm exists
type AlarmCheckResult struct {
	AlarmExists bool
	AlarmName   string
	AlarmState  string
}

// ErrAlarmDetailsUnknown means the resource carries no alarm listing: the sync
// that stored it could not list the region's alarms, or does not list them for
// this service. Whether the alarm exists is then unknown, not missing.
var ErrAlarmDetailsUnknown = errors.New("alarm details unknown")

// IsAlarmMissing checks if an alarm matching the template exists for the resource
// Returns true if the alarm is missing (should be recommended), and
// ErrAlarmDetailsUnknown when the resource has no alarm listing to check
func IsAlarmMissing(resource providers.Resource, template providers.AlarmTemplate, dimensionValue string) (bool, error) {
	alarmDetails, ok := resource.Meta["AlarmDetails"]
	if !ok {
		return false, ErrAlarmDetailsUnknown
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

	// An alarm on any one of the template's covering metrics, scoped to this
	// resource, counts as the template's alarm
	for _, metric := range coveringMetrics(template) {
		// Get the expected dimension name for this namespace
		expectedDimensionName := getDimensionNameForNamespace(metric.Namespace)
		if expectedDimensionName == "" {
			// Unknown namespace - can't validate dimensions
			return false, fmt.Errorf("unknown namespace: %s", metric.Namespace)
		}
		if alarmWatchesMetric(alarm, metric, expectedDimensionName, dimensionValue) {
			return true, nil
		}
	}

	return false, nil
}

// coveringMetrics returns the metrics an alarm can watch to count as the
// template's alarm: the template's satisfied_by list, or else its own metric
func coveringMetrics(template providers.AlarmTemplate) []providers.MetricInfo {
	if len(template.Configuration.SatisfiedBy) > 0 {
		return template.Configuration.SatisfiedBy
	}
	return []providers.MetricInfo{{
		Namespace:  template.Configuration.Namespace,
		MetricName: template.Configuration.MetricName,
	}}
}

// alarmWatchesMetric checks if the alarm watches the metric for the resource
// (dimension name and value). A metric math alarm (e.g., ALB HTTP error rate)
// watches each data metric in its Metrics array; a simple alarm (e.g., EC2 CPU)
// watches its own metric.
func alarmWatchesMetric(alarm types.MetricAlarm, metric providers.MetricInfo, expectedDimensionName string, dimensionValue string) bool {
	if len(alarm.Metrics) > 0 {
		for _, query := range alarm.Metrics {
			// Expressions have no MetricStat - skip them
			if query.MetricStat == nil || query.MetricStat.Metric == nil {
				continue
			}
			watched := query.MetricStat.Metric
			if aws.ToString(watched.Namespace) == metric.Namespace &&
				aws.ToString(watched.MetricName) == metric.MetricName &&
				dimensionsMatch(watched.Dimensions, expectedDimensionName, dimensionValue) {
				return true
			}
		}
		return false
	}

	return aws.ToString(alarm.Namespace) == metric.Namespace &&
		aws.ToString(alarm.MetricName) == metric.MetricName &&
		dimensionsMatch(alarm.Dimensions, expectedDimensionName, dimensionValue)
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
