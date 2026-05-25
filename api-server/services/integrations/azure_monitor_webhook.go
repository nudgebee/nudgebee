package integrations

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"path"
	"strconv"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(AzureMonitorWebhook{})
}

type AzureMonitorWebhook struct {
}

// AzureMonitorAlert represents the root structure of Azure Monitor Common Alert Schema
type AzureMonitorAlert struct {
	SchemaID string                `json:"schemaId"`
	Data     AzureMonitorAlertData `json:"data"`
}

// AzureMonitorAlertData contains the main alert data
type AzureMonitorAlertData struct {
	Essentials       AzureMonitorEssentials `json:"essentials"`
	AlertContext     AzureMonitorContext    `json:"alertContext"`
	CustomProperties map[string]any         `json:"customProperties"`
}

// AzureMonitorEssentials contains essential alert information (standardized across all alert types)
type AzureMonitorEssentials struct {
	AlertID             string   `json:"alertId"`
	AlertRule           string   `json:"alertRule"`
	Severity            string   `json:"severity"`          // Sev0, Sev1, Sev2, Sev3, Sev4
	SignalType          string   `json:"signalType"`        // Metric, Log, Activity Log
	MonitorCondition    string   `json:"monitorCondition"`  // Fired, Resolved
	MonitoringService   string   `json:"monitoringService"` // Platform, Log Analytics, Application Insights, etc.
	AlertTargetIDs      []string `json:"alertTargetIDs"`
	ConfigurationItems  []string `json:"configurationItems"`
	OriginAlertID       string   `json:"originAlertId"`
	FiredDateTime       string   `json:"firedDateTime"`    // ISO 8601 datetime string
	ResolvedDateTime    string   `json:"resolvedDateTime"` // ISO 8601 datetime string (optional)
	Description         string   `json:"description"`
	EssentialsVersion   string   `json:"essentialsVersion"`
	AlertContextVersion string   `json:"alertContextVersion"`

	// Optional fields that may appear in some alert types
	TargetResourceType  string `json:"targetResourceType,omitempty"`
	TargetResourceGroup string `json:"targetResourceGroup,omitempty"`
	AlertRuleID         string `json:"alertRuleID,omitempty"`
	InvestigationLink   string `json:"investigationLink,omitempty"`
}

// AzureMonitorContext contains alert context information (varies by alert type)
type AzureMonitorContext struct {
	Properties    map[string]any        `json:"properties"`
	ConditionType string                `json:"conditionType"` // SingleResourceMultipleMetricCriteria, DynamicThresholdCriteria, LogQueryCriteria, etc.
	Condition     AzureMonitorCondition `json:"condition"`
}

// AzureMonitorCondition contains condition details (structure varies by conditionType)
type AzureMonitorCondition struct {
	// Common fields
	WindowSize      string `json:"windowSize"`      // PT5M, PT1H, etc. (ISO 8601 duration)
	WindowStartTime string `json:"windowStartTime"` // ISO 8601 datetime
	WindowEndTime   string `json:"windowEndTime"`   // ISO 8601 datetime

	// Metric alert fields
	AllOf                         []AzureMonitorMetricCondition `json:"allOf,omitempty"`
	StaticThresholdFailingPeriods *AzureMonitorFailingPeriods   `json:"staticThresholdFailingPeriods,omitempty"`

	// Log alert fields
	SearchQuery                   string `json:"searchQuery,omitempty"`
	SearchIntervalStartTimeUtc    string `json:"searchIntervalStartTimeUtc,omitempty"`
	SearchIntervalEndtimeUtc      string `json:"searchIntervalEndtimeUtc,omitempty"`
	ResultCount                   int    `json:"resultCount,omitempty"`
	LinkToSearchResults           string `json:"linkToSearchResults,omitempty"`
	LinkToFilteredSearchResultsUI string `json:"linkToFilteredSearchResultsUI,omitempty"`
	LinkToSearchResultsAPI        string `json:"linkToSearchResultsAPI,omitempty"`

	// Activity Log alert fields
	Authorization  map[string]any `json:"authorization,omitempty"`
	Claims         map[string]any `json:"claims,omitempty"`
	Caller         string         `json:"caller,omitempty"`
	CorrelationId  string         `json:"correlationId,omitempty"`
	EventSource    string         `json:"eventSource,omitempty"`
	EventTimestamp string         `json:"eventTimestamp,omitempty"`
	EventDataId    string         `json:"eventDataId,omitempty"`
	Level          string         `json:"level,omitempty"`
	OperationName  string         `json:"operationName,omitempty"`
	OperationId    string         `json:"operationId,omitempty"`
	Status         string         `json:"status,omitempty"`
	SubStatus      string         `json:"subStatus,omitempty"`
}

// AzureMonitorMetricCondition represents a single metric condition (for metric alerts)
type AzureMonitorMetricCondition struct {
	MetricName      string      `json:"metricName"`
	MetricNamespace string      `json:"metricNamespace"`
	Operator        string      `json:"operator"`        // GreaterThan, LessThan, GreaterThanOrEqual, LessThanOrEqual
	Threshold       interface{} `json:"threshold"`       // Can be string or number
	TimeAggregation string      `json:"timeAggregation"` // Average, Minimum, Maximum, Total, Count
	Dimensions      []any       `json:"dimensions"`
	MetricValue     float64     `json:"metricValue"`
	WebTestName     *string     `json:"webTestName"`
}

// AzureMonitorFailingPeriods contains static threshold failing periods configuration
type AzureMonitorFailingPeriods struct {
	NumberOfEvaluationPeriods int `json:"numberOfEvaluationPeriods"`
	MinFailingPeriodsToAlert  int `json:"minFailingPeriodsToAlert"`
}

const IntegrationAzureMonitorWebhook = "azure_monitor_webhook"

func (m AzureMonitorWebhook) Name() string {
	return IntegrationAzureMonitorWebhook
}

func (m AzureMonitorWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m AzureMonitorWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Azure Monitor Webhook",
				Default:          "",
				AutoGenerateFunc: "",
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			"token": {
				Type:             core.ToolSchemaTypeString,
				Default:          "",
				AutoGenerateFunc: "",
			},
		},
	}
}

func (m AzureMonitorWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m AzureMonitorWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

func (m AzureMonitorWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	// Parse JSON payload into typed struct
	var azureAlert AzureMonitorAlert
	err := common.UnmarshalJson([]byte(webhookPayloadString), &azureAlert)
	if err != nil {
		return []core.EventIncomingWebhook{}, fmt.Errorf("failed to parse Azure Monitor payload: %w", err)
	}

	// Validate schema ID
	if azureAlert.SchemaID != "azureMonitorCommonAlertSchema" {
		return []core.EventIncomingWebhook{}, fmt.Errorf("invalid Azure Monitor payload: unsupported schema %s", azureAlert.SchemaID)
	}

	// Validate and extract critical required fields
	essentials := azureAlert.Data.Essentials
	if strings.TrimSpace(essentials.AlertID) == "" {
		return []core.EventIncomingWebhook{}, fmt.Errorf("invalid Azure Monitor payload: alertId is missing or empty")
	}

	// Apply basic validation and defaults
	alertRule := essentials.AlertRule
	if strings.TrimSpace(alertRule) == "" {
		alertRule = "Unknown Alert Rule"
	}

	severity := essentials.Severity
	if strings.TrimSpace(severity) == "" {
		severity = "Sev4" // Default to lowest severity
	}

	// Extract fields with clean struct access
	alertId := essentials.AlertID
	signalType := essentials.SignalType
	monitorCondition := essentials.MonitorCondition
	targetResourceGroup := essentials.TargetResourceGroup
	description := essentials.Description
	firedDateTime := essentials.FiredDateTime
	investigationLink := essentials.InvestigationLink
	targetResourceType := essentials.TargetResourceType
	alertRuleID := essentials.AlertRuleID
	monitoringService := essentials.MonitoringService
	originAlertId := essentials.OriginAlertID
	alertTargetIDs := essentials.AlertTargetIDs
	configurationItems := essentials.ConfigurationItems

	// Parse fired datetime
	var eventTime time.Time
	if firedDateTime != "" {
		if parsedTime, err := time.Parse(time.RFC3339, firedDateTime); err == nil {
			eventTime = parsedTime
		} else {
			sc.GetLogger().Error("Failed to parse firedDateTime from Azure Monitor payload, using current time as fallback",
				"firedDateTime", firedDateTime, "error", err, "alertId", alertId)
			eventTime = time.Now()
		}
	} else {
		eventTime = time.Now()
	}

	// Build event title and message
	title := fmt.Sprintf("Azure Monitor Alert: %s", alertRule)
	if alertRule == "" {
		title = "Azure Monitor Alert"
	}

	message := description
	if message == "" {
		message = fmt.Sprintf("Alert triggered for %s with severity %s", targetResourceGroup, severity)
	}

	// Extract alert context using typed structs
	alertContext := azureAlert.Data.AlertContext
	var contextInfo string
	var conditionType string
	var windowSize string
	var windowStartTime string
	var windowEndTime string
	var metricNamespace string
	var timeAggregation string

	// Extract condition type and timing information from alert context
	conditionType = alertContext.ConditionType
	condition := alertContext.Condition
	windowSize = condition.WindowSize
	windowStartTime = condition.WindowStartTime
	windowEndTime = condition.WindowEndTime

	// Process metric conditions if available
	if len(condition.AllOf) > 0 {
		var contextInfoParts []string
		var metricNamespaces []string
		var timeAggregations []string

		// Process all conditions in the allOf array
		for i, metricCondition := range condition.AllOf {
			if metricCondition.MetricName != "" {
				conditionInfo := fmt.Sprintf("**Condition %d:** %s %s %v (current: %.2f)",
					i+1, metricCondition.MetricName, metricCondition.Operator, metricCondition.Threshold, metricCondition.MetricValue)
				if metricCondition.TimeAggregation != "" {
					conditionInfo += fmt.Sprintf(" | **Aggregation:** %s", metricCondition.TimeAggregation)
				}
				contextInfoParts = append(contextInfoParts, conditionInfo)
			}

			// Collect unique metric namespaces and time aggregations for labels
			if metricCondition.MetricNamespace != "" && !contains(metricNamespaces, metricCondition.MetricNamespace) {
				metricNamespaces = append(metricNamespaces, metricCondition.MetricNamespace)
			}
			if metricCondition.TimeAggregation != "" && !contains(timeAggregations, metricCondition.TimeAggregation) {
				timeAggregations = append(timeAggregations, metricCondition.TimeAggregation)
			}
		}

		// Combine all condition information
		if len(contextInfoParts) > 0 {
			contextInfo = strings.Join(contextInfoParts, "\n\n")
			if windowSize != "" {
				contextInfo += fmt.Sprintf("\n\n**Evaluation Window:** %s", windowSize)
			}
		}

		// Set aggregated values for labels (comma-separated if multiple)
		if len(metricNamespaces) > 0 {
			metricNamespace = strings.Join(metricNamespaces, ",")
		}
		if len(timeAggregations) > 0 {
			timeAggregation = strings.Join(timeAggregations, ",")
		}
	}

	// Format message as markdown with structured information
	var markdownMessage strings.Builder

	// Main description
	if message != "" {
		fmt.Fprintf(&markdownMessage, "## Alert Description\n%s\n\n", message)
	}

	// Alert details section
	markdownMessage.WriteString("## Alert Details\n")
	fmt.Fprintf(&markdownMessage, "- **Alert Rule:** %s\n", alertRule)
	fmt.Fprintf(&markdownMessage, "- **Severity:** %s\n", severity)
	fmt.Fprintf(&markdownMessage, "- **Signal Type:** %s\n", signalType)
	fmt.Fprintf(&markdownMessage, "- **Monitor Condition:** %s\n", monitorCondition)
	fmt.Fprintf(&markdownMessage, "- **Target Resource Group:** %s\n", targetResourceGroup)

	if targetResourceType != "" {
		fmt.Fprintf(&markdownMessage, "- **Target Resource Type:** %s\n", targetResourceType)
	}
	if monitoringService != "" {
		fmt.Fprintf(&markdownMessage, "- **Monitoring Service:** %s\n", monitoringService)
	}
	if conditionType != "" {
		fmt.Fprintf(&markdownMessage, "- **Condition Type:** %s\n", conditionType)
	}

	// Add configuration items if available
	if len(configurationItems) > 0 {
		fmt.Fprintf(&markdownMessage, "- **Configuration Items:** %s\n", strings.Join(configurationItems, ", "))
	}

	if investigationLink != "" {
		fmt.Fprintf(&markdownMessage, "- **Investigation Link:** [View in Azure Portal](%s)\n", investigationLink)
	}

	// Add context info if available
	if contextInfo != "" {
		fmt.Fprintf(&markdownMessage, "\n## Metric Information\n%s\n", contextInfo)
	}

	// Add timing information if available
	if windowStartTime != "" && windowEndTime != "" {
		fmt.Fprintf(&markdownMessage, "\n## Evaluation Window\n- **Start:** %s\n- **End:** %s\n", windowStartTime, windowEndTime)
	}

	message = markdownMessage.String()

	essentialsJSON, err := json.Marshal(essentials)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal azure essentials: %w", err)
	}
	alertContextJSON, err := json.Marshal(alertContext)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal azure alert context: %w", err)
	}

	// Build labels
	labels := map[string]string{
		"integration":           "azure_monitor",
		"alert_id":              alertId,
		"alert_rule":            alertRule,
		"severity":              severity,
		"signal_type":           signalType,
		"monitor_condition":     monitorCondition,
		"target_resource_group": targetResourceGroup,
		"alertname":             alertRule, // Standard alertname label
		"azure_essentials":      string(essentialsJSON),
		"azure_alert_context":   string(alertContextJSON),
	}

	// Add additional labels if available
	if investigationLink != "" {
		labels["investigation_link"] = investigationLink
	}
	if targetResourceType != "" {
		labels["target_resource_type"] = targetResourceType
	}
	if alertRuleID != "" {
		labels["alert_rule_id"] = alertRuleID
	}
	if monitoringService != "" {
		labels["monitoring_service"] = monitoringService
	}
	if originAlertId != "" {
		labels["origin_alert_id"] = originAlertId
	}
	if conditionType != "" {
		labels["condition_type"] = conditionType
	}
	if windowSize != "" {
		labels["window_size"] = windowSize
	}
	if metricNamespace != "" {
		labels["metric_namespace"] = metricNamespace
	}
	if timeAggregation != "" {
		labels["time_aggregation"] = timeAggregation
	}

	// Add alert target IDs as comma-separated string
	if len(alertTargetIDs) > 0 {
		labels["alert_target_ids"] = strings.Join(alertTargetIDs, ",")
	}

	// Add configuration items as comma-separated string
	if len(configurationItems) > 0 {
		labels["configuration_items"] = strings.Join(configurationItems, ",")
	}

	// Compatibility labels for the enrichment chain (cloud_azure_alert_rule,
	// cloud_azure_kql_query_results, cloud_azure_activity_log, etc.).
	// These actions expect polling-style label names set by the cloud-collector.
	//
	// Extract subscription ID from alertId (always present) rather than alertTargetIDs
	// which can be null for certain alert types (e.g. CostAlerts/budget alerts).
	if parts := strings.Split(alertId, "/"); len(parts) > 2 && strings.EqualFold(parts[1], "subscriptions") {
		labels["azure_subscription_id"] = parts[2]
	}

	if len(alertTargetIDs) > 0 {
		labels["azure_alert_target_resource"] = alertTargetIDs[0]
		// Derive service name and resource group from the resource ID path.
		// The essential fields targetResourceType/targetResourceGroup are not
		// reliably included in Azure webhook payloads (omitted in most alert types),
		// so we extract from the resource ID the same way the polling path does.
		labels["azure_service_name"] = extractAzureServiceName(alertTargetIDs[0])
		labels["azure_resource_group"] = extractAzureResourceGroup(alertTargetIDs[0])
	} else if subID, ok := labels["azure_subscription_id"]; ok {
		// Budget/CostAlerts send alertTargetIDs: null. Construct a subscription-level
		// resource ID so downstream actions (cloud_azure_alert_rule, etc.) can still
		// match via azure_alert_target_resource.
		labels["azure_alert_target_resource"] = "/subscriptions/" + subID
	}
	if alertRuleID != "" {
		labels["azure_alert_rule"] = alertRuleID
	}
	labels["azure_alert_name"] = alertRule
	// Use struct fields as overrides if Azure does include them (newer API versions)
	if targetResourceType != "" {
		labels["azure_service_name"] = strings.ToLower(targetResourceType)
	}
	if targetResourceGroup != "" {
		labels["azure_region"] = targetResourceGroup
	}

	// Map Azure severity to event priority
	var eventPriority event.EventPriortiy
	switch strings.ToLower(severity) {
	case "sev0":
		eventPriority = event.EventPriortiyHigh
	case "sev1":
		eventPriority = event.EventPriortiyHigh
	case "sev2":
		eventPriority = event.EventPriortiyMedium
	case "sev3", "sev4":
		eventPriority = event.EventPriortiyLow
	default:
		eventPriority = event.EventPriortiyLow
	}

	// Determine event status based on monitor condition
	eventStatus := "triggered"
	if strings.ToLower(monitorCondition) == "resolved" {
		eventStatus = "resolved"
	}

	// Map event status to investigation status
	var investigationStatus event.EventStatus
	if eventStatus == "resolved" {
		investigationStatus = event.EventStatusResolved
	} else {
		investigationStatus = event.EventStatusFiring
	}

	// Create evidence for the raw Azure Monitor event
	var evidences []event.EventEvidence

	// Add raw payload evidence
	rawPayloadEvidence := event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "Azure Monitor Event",
			"data": azureAlert, // Store the entire parsed payload
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("Azure Monitor Alert ID: %s", alertId),
				Severity: "info",
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "azure_monitor_event",
			"actual_action_name":     "azure_monitor_event",
			"action_title":           "Azure Monitor Event",
			"conditional_expression": "",
		},
	}
	evidences = append(evidences, rawPayloadEvidence)

	// Create investigation structure
	investigation := core.EventIncomingWebhookInvestigation{
		RuleName: alertRule,
		Labels:   labels,
		Annotations: map[string]string{
			"description": message,
			"summary":     title,
		},
		RuleType:    "azure_monitor_alert",
		RuleId:      alertRule,
		Fingerprint: alertId,
		Status:      investigationStatus,
		Severity:    eventPriority,
		SourceUrl:   investigationLink,
		Evidences:   evidences,
	}

	var subjectName string
	if len(essentials.AlertTargetIDs) > 0 {
		subjectName = path.Base(essentials.AlertTargetIDs[0])
	}
	// Resolve the correct cloud account when the integration is linked to multiple accounts.
	// Extract subscription ID from alertId (always present, format: /subscriptions/{sub-id}/providers/...)
	// or fall back to alertTargetIDs.
	var resolvedAccountId string
	resourceIDs := []string{alertId}
	if len(alertTargetIDs) > 0 {
		resourceIDs = append(resourceIDs, alertTargetIDs[0])
	}
	for _, resourceID := range resourceIDs {
		if subID := extractSubscriptionFromResourceID(resourceID); subID != "" {
			resolvedAccountId = resolveAzureAccountBySubscription(sc, subID)
			if resolvedAccountId != "" {
				break
			}
		}
	}

	webhook := core.EventIncomingWebhook{
		WebhookId:             fmt.Sprintf("azure-monitor-%s", alertId),
		EventType:             "azure_monitor_alert",
		EventId:               alertId,
		EventUrl:              investigationLink,
		EventStatus:           eventStatus,
		EventPriority:         string(eventPriority),
		EventCreatedAt:        eventTime,
		EventEndsAt:           time.Time{}, // Azure Monitor doesn't provide end time for active alerts
		EventTitle:            title,
		EventDescription:      message,
		EventTags:             []string{"azure", "monitor", severity, signalType},
		Investigation:         investigation,
		EventSubjectNamespace: essentials.TargetResourceType,
		EventSubjectName:      subjectName,
		AccountId:             resolvedAccountId,
	}

	// Create event rule asynchronously based on Azure Monitor alert configuration
	go func() {
		// Add panic recovery to prevent application crashes
		defer func() {
			if r := recover(); r != nil {
				sc.GetLogger().Error("Panic recovered in Azure Monitor event rule creation goroutine",
					"panic", r, "alertId", alertId, "alertRule", alertRule)
			}
		}()

		alertName := alertRule
		alertQuery := ""
		alertMessage := message
		alertDuration := 0.0
		alertCategory := "alert"

		// Extract query/condition information from alert context using typed structs
		if conditionType != "" {
			alertCategory = strings.ToLower(conditionType)
		}

		// Build query string from metric conditions
		if len(azureAlert.Data.AlertContext.Condition.AllOf) > 0 {
			var queryParts []string
			for _, metricCondition := range azureAlert.Data.AlertContext.Condition.AllOf {
				if metricCondition.MetricName != "" && metricCondition.Operator != "" {
					queryPart := fmt.Sprintf("%s %s %v", metricCondition.MetricName, metricCondition.Operator, metricCondition.Threshold)
					queryParts = append(queryParts, queryPart)
				}
			}
			if len(queryParts) > 0 {
				alertQuery = strings.Join(queryParts, " AND ")
			}
		}

		// Extract window size as duration
		if windowSize != "" {
			// Convert Azure duration format (PT5M) to seconds
			if duration, err := parseAzureDuration(windowSize); err == nil {
				alertDuration = duration
			} else {
				sc.GetLogger().Error("Failed to parse windowSize from Azure Monitor payload, using default duration 0.0",
					"windowSize", windowSize, "error", err, "alertId", alertId)
			}
		}

		// Set default values if empty
		if alertName == "" {
			alertName = "Azure Monitor Alert"
		}
		if alertQuery == "" {
			alertQuery = fmt.Sprintf("azure_monitor_alert{alert_id=\"%s\"}", alertId)
		}

		var alertSeverity = "warning"
		if eventPriority == "HIGH" {
			alertSeverity = "critical"
		}
		eventReq := eventrule.EventConfig{
			Annotations: struct {
				Description string `json:"description"`
				Summary     string `json:"summary"`
				Runbook     string `json:"runbook"`
			}{
				Description: alertMessage,
				Summary:     title,
			},
			Expr: alertQuery,
			Labels: struct {
				Severity string `json:"severity"`
			}{Severity: alertSeverity},
			Alert:         alertName,
			Duration:      strconv.FormatFloat(alertDuration, 'f', -1, 64),
			AccountID:     accountId,
			Source:        "azure_monitor_webhook",
			Category:      alertCategory,
			Severity:      alertSeverity,
			Enabled:       true,
			TriggerParams: []map[string]interface{}{},
			ActionParams:  []map[string]interface{}{},
		}
		_, err := eventrule.CreateEventRule(sc, eventReq)
		if err != nil {
			sc.GetLogger().Error("CreateEventRule failed for Azure Monitor webhook", "error", err, "alertId", alertId)
		}
	}()

	data := []core.EventIncomingWebhook{webhook}
	return data, nil
}

func (m AzureMonitorWebhook) TestConnection(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId string) (bool, error) {
	return true, nil
}

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// extractAzureServiceName extracts the full resource type from an Azure resource ID.
// e.g. "/subscriptions/.../providers/Microsoft.Compute/virtualMachines/myvm" → "microsoft.compute/virtualmachines"
func extractAzureServiceName(resourceID string) string {
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")
	for i, part := range parts {
		if strings.EqualFold(part, "providers") && i+2 < len(parts) {
			remaining := parts[i+1:]
			typeParts := []string{remaining[0]}
			for j := 1; j < len(remaining); j += 2 {
				typeParts = append(typeParts, remaining[j])
			}
			return strings.ToLower(strings.Join(typeParts, "/"))
		}
	}
	return ""
}

// extractAzureResourceGroup extracts the resource group name from an Azure resource ID.
// e.g. "/subscriptions/.../resourceGroups/myRG/providers/..." → "myRG"
func extractAzureResourceGroup(resourceID string) string {
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")
	for i, part := range parts {
		if strings.EqualFold(part, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// parseAzureDuration converts Azure duration format (e.g., "PT5M") to seconds
func parseAzureDuration(duration string) (float64, error) {
	// Azure uses ISO 8601 duration format: PT5M = 5 minutes
	if !strings.HasPrefix(duration, "PT") {
		return 0, fmt.Errorf("invalid Azure duration format: %s", duration)
	}

	duration = strings.TrimPrefix(duration, "PT")

	// Simple parsing for common cases
	if strings.HasSuffix(duration, "S") {
		// Seconds
		secsStr := strings.TrimSuffix(duration, "S")
		if secs, err := strconv.ParseFloat(secsStr, 64); err == nil {
			return secs, nil
		}
	} else if strings.HasSuffix(duration, "M") {
		// Minutes
		minsStr := strings.TrimSuffix(duration, "M")
		if mins, err := strconv.ParseFloat(minsStr, 64); err == nil {
			return mins * 60, nil
		}
	} else if strings.HasSuffix(duration, "H") {
		// Hours
		hoursStr := strings.TrimSuffix(duration, "H")
		if hours, err := strconv.ParseFloat(hoursStr, 64); err == nil {
			return hours * 3600, nil
		}
	}

	return 0, fmt.Errorf("unsupported Azure duration format: %s", duration)
}

// extractSubscriptionFromResourceID extracts the subscription ID from an Azure ARM resource ID.
// Format: /subscriptions/{subscription-id}/providers/... or /subscriptions/{subscription-id}/resourceGroups/...
// Returns empty string if the format doesn't match.
func extractSubscriptionFromResourceID(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "subscriptions") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// resolveAzureAccountBySubscription finds the cloud_accounts row matching the given
// Azure subscription ID within the current tenant. Returns the account UUID or empty string if not found.
func resolveAzureAccountBySubscription(sc *security.RequestContext, subscriptionID string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("azure_monitor_webhook: failed to get database manager", "error", err)
		return ""
	}

	tenantID := sc.GetSecurityContext().GetTenantId()
	var accountID string
	err = dbms.Db.Get(&accountID,
		`SELECT id::text FROM cloud_accounts WHERE assume_role = $1 AND tenant = $2::uuid AND cloud_provider = 'Azure' ORDER BY created_at DESC LIMIT 1`,
		subscriptionID, tenantID)
	if err != nil {
		sc.GetLogger().Warn("azure_monitor_webhook: could not resolve account by subscription, will use default",
			"subscriptionID", subscriptionID, "tenantID", tenantID, "error", err)
		return ""
	}
	return accountID
}
