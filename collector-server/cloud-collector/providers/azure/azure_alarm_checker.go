package azure

import (
	"nudgebee/collector/cloud/providers"
	"strings"
)

// AzureAlertInfo represents the essential information about an existing Azure metric alert
// This is stored in resource.Meta["AlertDetails"] after enrichment
type AzureAlertInfo struct {
	AlertName       string `json:"alert_name"`
	MetricNamespace string `json:"metric_namespace"`
	MetricName      string `json:"metric_name"`
	Severity        int    `json:"severity"`
	Enabled         bool   `json:"enabled"`
	ResourceID      string `json:"resource_id"`
}

// IsAzureAlertMissing checks if an Azure metric alert matching the template exists for the resource
// Returns true if the alert is missing (should be recommended)
func IsAzureAlertMissing(resource providers.Resource, template providers.AlarmTemplate) bool {
	alertDetails, ok := resource.Meta["AlertDetails"]
	if !ok {
		// No alerts configured at all - alert is missing
		return true
	}

	alertArray, ok := alertDetails.([]interface{})
	if !ok {
		// Try typed slice
		if typedAlerts, ok := alertDetails.([]AzureAlertInfo); ok {
			return !anyAzureAlertMatchesTemplate(typedAlerts, template)
		}
		// Invalid alert details format - treat as missing
		return true
	}

	if len(alertArray) == 0 {
		// No alerts configured - alert is missing
		return true
	}

	// Check each existing alert to see if it matches our template
	for _, alertInterface := range alertArray {
		if doesAzureAlertMatchTemplate(alertInterface, template) {
			return false // Found matching alert - not missing
		}
	}

	// No matching alert found - alert is missing
	return true
}

// doesAzureAlertMatchTemplate checks if an individual Azure alert matches the template criteria
func doesAzureAlertMatchTemplate(alertInterface interface{}, template providers.AlarmTemplate) bool {
	// Try typed assertion first
	if alert, ok := alertInterface.(AzureAlertInfo); ok {
		return matchesAzureAlertCriteria(alert, template)
	}

	// Fall back to map[string]interface{} for deserialized data
	alertMap, ok := alertInterface.(map[string]interface{})
	if !ok {
		return false
	}

	// Convert map to AzureAlertInfo and use centralized matching logic
	metricNamespace, _ := alertMap["metric_namespace"].(string)
	metricName, _ := alertMap["metric_name"].(string)
	enabled, _ := alertMap["enabled"].(bool)

	alertInfo := AzureAlertInfo{
		MetricNamespace: metricNamespace,
		MetricName:      metricName,
		Enabled:         enabled,
	}
	return matchesAzureAlertCriteria(alertInfo, template)
}

// anyAzureAlertMatchesTemplate checks if any alert in a typed slice matches the template
func anyAzureAlertMatchesTemplate(alerts []AzureAlertInfo, template providers.AlarmTemplate) bool {
	for _, alert := range alerts {
		if matchesAzureAlertCriteria(alert, template) {
			return true
		}
	}
	return false
}

// matchesAzureAlertCriteria checks if an AzureAlertInfo matches the template's metric criteria
func matchesAzureAlertCriteria(alert AzureAlertInfo, template providers.AlarmTemplate) bool {
	// Disabled alerts don't count as coverage
	if !alert.Enabled {
		return false
	}

	// Match by metric namespace and metric name (case-insensitive)
	return strings.EqualFold(alert.MetricNamespace, template.Configuration.Namespace) &&
		strings.EqualFold(alert.MetricName, template.Configuration.MetricName)
}

// ShouldRecommendAzureAlarm determines if we should create a recommendation for a missing alarm
// Uses the generic rule evaluator for CONDITIONAL metrics
func ShouldRecommendAzureAlarm(resource providers.Resource, template providers.AlarmTemplate) bool {
	evaluator := providers.NewRuleEvaluator(nil)
	return evaluator.ShouldRecommendAlarm(resource, template)
}
