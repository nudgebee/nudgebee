package azure

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// CreateAzureMetricAlert creates an Azure metric alert using the provided configuration
func CreateAzureMetricAlert(ctx providers.CloudProviderContext, account providers.Account, config providers.AlarmCreationConfig, resourceID string, resourceRegion string, severity string) error {
	// Validate configuration before creating
	if err := ValidateAzureAlarmConfig(config); err != nil {
		ctx.GetLogger().Error("invalid Azure alarm configuration",
			"alarm_name", config.AlarmName,
			"error", err)
		return fmt.Errorf("invalid alarm configuration: %w", err)
	}

	ctx.GetLogger().Info("creating Azure metric alert",
		"alarm_name", config.AlarmName,
		"resource_id", resourceID,
		"metric_name", config.MetricName,
		"threshold", config.Threshold)

	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Extract subscription ID from resource ID
	subscriptionID, _ := extractSubscriptionID(resourceID)
	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}

	client, err := armmonitor.NewMetricAlertsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return fmt.Errorf("failed to create metric alerts client: %w", err)
	}

	// Extract resource group from resource ID
	rg, err := extractResourceGroup(resourceID)
	if err != nil {
		return fmt.Errorf("failed to extract resource group from resource ID: %w", err)
	}

	// Convert comparison operator to Azure format
	comparisonOperator := convertComparisonOperator(config.ComparisonOperator)

	// Convert statistic to Azure aggregation type
	aggregationType := convertStatisticToAggregation(config.Statistic)

	// Build metric criteria
	odataType := armmonitor.OdatatypeMicrosoftAzureMonitorSingleResourceMultipleMetricCriteria
	criteria := &armmonitor.MetricAlertSingleResourceMultipleMetricCriteria{
		ODataType: &odataType,
		AllOf: []*armmonitor.MetricCriteria{
			{
				Name:            to.Ptr("metric1"),
				MetricName:      to.Ptr(config.MetricName),
				MetricNamespace: to.Ptr(config.Namespace),
				Operator:        &comparisonOperator,
				Threshold:       to.Ptr(config.Threshold),
				TimeAggregation: &aggregationType,
				CriterionType:   to.Ptr(armmonitor.CriterionTypeStaticThresholdCriterion),
			},
		},
	}

	// Convert period to ISO 8601 duration
	windowSize := secondsToISO8601Duration(config.Period * config.EvaluationPeriods)
	evaluationFrequency := secondsToISO8601Duration(config.Period)

	// Convert severity string to Azure severity int (0=Critical, 1=Error, 2=Warning, 3=Informational, 4=Verbose)
	azureSeverity := convertSeverityToAzure(severity)

	// Build the metric alert resource
	alertResource := armmonitor.MetricAlertResource{
		Location: to.Ptr("global"), // Metric alerts are global resources
		Properties: &armmonitor.MetricAlertProperties{
			Description:         to.Ptr(fmt.Sprintf("Auto-created alert for %s", config.MetricName)),
			Severity:            to.Ptr(azureSeverity),
			Enabled:             to.Ptr(true),
			Scopes:              []*string{to.Ptr(resourceID)},
			EvaluationFrequency: to.Ptr(evaluationFrequency),
			WindowSize:          to.Ptr(windowSize),
			Criteria:            criteria,
			AutoMitigate:        to.Ptr(true),
			Actions:             []*armmonitor.MetricAlertAction{}, // User must configure action groups separately
		},
	}

	// Create or update the alert
	alertName := config.AlarmName
	_, err = client.CreateOrUpdate(ctx.GetContext(), rg, alertName, alertResource, nil)
	if err != nil {
		return fmt.Errorf("failed to create metric alert: %w", err)
	}

	ctx.GetLogger().Info("successfully created Azure metric alert",
		"alertName", alertName,
		"resourceID", resourceID,
		"metricName", config.MetricName,
		"threshold", config.Threshold,
	)

	return nil
}

// CreateAzureMetricAlertFromRecommendation creates an Azure metric alert from a recommendation's data
func CreateAzureMetricAlertFromRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	ctx.GetLogger().Info("creating Azure metric alert from recommendation",
		"resource_id", recommendation.ResourceId,
		"resource_region", recommendation.ResourceRegion)

	// Validate that the resource ID belongs to the account's subscription (security check)
	if err := validateResourceBelongsToAccount(ctx, account, recommendation.ResourceId); err != nil {
		ctx.GetLogger().Error("resource validation failed",
			"resource_id", recommendation.ResourceId,
			"error", err)
		return fmt.Errorf("resource validation failed: %w", err)
	}

	// Extract alarm config from recommendation data
	alarmConfigData, ok := recommendation.Data["alarm_config"]
	if !ok {
		ctx.GetLogger().Error("alarm_config not found in recommendation data",
			"resource_id", recommendation.ResourceId,
			"available_keys", getDataKeys(recommendation.Data))
		return fmt.Errorf("alarm_config not found in recommendation data")
	}

	ctx.GetLogger().Info("parsed alarm configuration successfully from recommendation")

	// Convert to AlarmCreationConfig
	var alarmConfig providers.AlarmCreationConfig
	configBytes, err := json.Marshal(alarmConfigData)
	if err != nil {
		return fmt.Errorf("failed to marshal alarm config: %w", err)
	}

	if err := json.Unmarshal(configBytes, &alarmConfig); err != nil {
		return fmt.Errorf("failed to unmarshal alarm config: %w", err)
	}

	// Extract severity from recommendation data (default to "Medium" if not specified)
	severity := "Medium"
	if severityData, ok := recommendation.Data["severity"]; ok {
		if s, ok := severityData.(string); ok {
			severity = s
		}
	}

	// Create the alert
	return CreateAzureMetricAlert(ctx, account, alarmConfig, recommendation.ResourceId, recommendation.ResourceRegion, severity)
}

// validateResourceBelongsToAccount validates that the resource ID belongs to the account's subscription
func validateResourceBelongsToAccount(ctx providers.CloudProviderContext, account providers.Account, resourceID string) error {
	if resourceID == "" {
		return fmt.Errorf("resource ID is empty")
	}

	// Extract subscription ID from resource ID
	resourceSubID, err := extractSubscriptionID(resourceID)
	if err != nil || resourceSubID == "" {
		return fmt.Errorf("cannot extract subscription ID from resource ID: %s", resourceID)
	}

	// Get account's session to retrieve subscription IDs
	session, err := getAzureSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get azure session: %w", err)
	}

	// Check if resource subscription matches any of account's subscriptions
	accountSubIDs := strings.Split(session.SubscriptionID, ",")
	for _, subID := range accountSubIDs {
		if strings.EqualFold(strings.TrimSpace(subID), resourceSubID) {
			return nil // Resource belongs to account
		}
	}

	return fmt.Errorf("resource subscription %s does not match account subscriptions", resourceSubID)
}

// ValidateAzureAlarmConfig validates an Azure alarm configuration before creating it
func ValidateAzureAlarmConfig(config providers.AlarmCreationConfig) error {
	if config.AlarmName == "" {
		return fmt.Errorf("alarm name is required")
	}
	if config.MetricName == "" {
		return fmt.Errorf("metric name is required")
	}
	if config.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if config.Period <= 0 {
		return fmt.Errorf("period must be greater than 0")
	}
	if config.EvaluationPeriods <= 0 {
		return fmt.Errorf("evaluation periods must be greater than 0")
	}
	if config.ComparisonOperator == "" {
		return fmt.Errorf("comparison operator is required")
	}
	return nil
}

// convertComparisonOperator converts alarm template comparison operator to Azure Operator
func convertComparisonOperator(operator string) armmonitor.Operator {
	switch operator {
	case "GreaterThanThreshold":
		return armmonitor.OperatorGreaterThan
	case "GreaterThanOrEqualToThreshold":
		return armmonitor.OperatorGreaterThanOrEqual
	case "LessThanThreshold":
		return armmonitor.OperatorLessThan
	case "LessThanOrEqualToThreshold":
		return armmonitor.OperatorLessThanOrEqual
	default:
		return armmonitor.OperatorGreaterThan
	}
}

// convertStatisticToAggregation converts alarm template statistic to Azure AggregationTypeEnum
func convertStatisticToAggregation(statistic string) armmonitor.AggregationTypeEnum {
	switch statistic {
	case "Average":
		return armmonitor.AggregationTypeEnumAverage
	case "Sum":
		return armmonitor.AggregationTypeEnumTotal
	case "Maximum":
		return armmonitor.AggregationTypeEnumMaximum
	case "Minimum":
		return armmonitor.AggregationTypeEnumMinimum
	case "Count":
		return armmonitor.AggregationTypeEnumCount
	default:
		return armmonitor.AggregationTypeEnumAverage
	}
}

// convertSeverityToAzure converts template severity string to Azure severity int
// Azure severity levels: 0=Critical, 1=Error, 2=Warning, 3=Informational, 4=Verbose
func convertSeverityToAzure(severity string) int32 {
	switch strings.ToLower(severity) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	case "info", "informational":
		return 4
	default:
		return 3 // Default to Informational
	}
}

// secondsToISO8601Duration converts seconds to ISO 8601 duration format
// Azure Monitor uses ISO 8601 durations (e.g., PT5M, PT1H)
func secondsToISO8601Duration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("PT%dS", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("PT%dM", minutes)
	}
	hours := minutes / 60
	remainingMinutes := minutes % 60
	if remainingMinutes == 0 {
		return fmt.Sprintf("PT%dH", hours)
	}
	return fmt.Sprintf("PT%dH%dM", hours, remainingMinutes)
}

// buildAzureAlarmConfig creates an AlarmCreationConfig for an Azure metric alert recommendation
func buildAzureAlarmConfig(resource providers.Resource, template providers.AlarmTemplate, threshold float64) providers.AlarmCreationConfig {
	return providers.AlarmCreationConfig{
		AlarmName:          sanitizeAlarmName(fmt.Sprintf("%s-%s", template.Name, resource.Name)),
		MetricName:         template.Configuration.MetricName,
		Namespace:          template.Configuration.Namespace,
		Statistic:          template.Configuration.Statistic,
		Period:             template.Configuration.Period,
		EvaluationPeriods:  template.Configuration.EvaluationPeriods,
		DatapointsToAlarm:  template.Configuration.DatapointsToAlarm,
		Threshold:          threshold,
		ComparisonOperator: template.Configuration.ComparisonOperator,
		TreatMissingData:   template.Configuration.TreatMissingData,
	}
}

// sanitizeAlarmName removes characters that are not allowed in Azure metric alert names
func sanitizeAlarmName(name string) string {
	// Azure metric alert names can contain letters, digits, hyphens, underscores, and periods
	var result strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			result.WriteRune(c)
		}
	}
	s := result.String()
	// Limit length
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

// getDataKeys returns keys from recommendation data map for logging
func getDataKeys(data map[string]interface{}) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return keys
}
