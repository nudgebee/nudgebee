package gcloud

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// CreateGCPAlertPolicy creates a GCP alert policy using the provided configuration
func CreateGCPAlertPolicy(ctx providers.CloudProviderContext, account providers.Account, config providers.AlarmCreationConfig, projectID string) error {
	// Validate configuration before creating
	if err := ValidateGCPAlarmConfig(config); err != nil {
		return fmt.Errorf("invalid alarm configuration: %w", err)
	}

	// Get account-scoped session to use proper credentials
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := monitoring.NewAlertPolicyClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create alert policy client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Warn("failed to close alert policy client", "error", cerr)
		}
	}()

	// Build condition based on whether it's a simple metric or metric math
	var conditions []*monitoringpb.AlertPolicy_Condition

	if len(config.Metrics) > 0 {
		// Metric math alarm with MQL
		condition, err := buildMQLCondition(config)
		if err != nil {
			return fmt.Errorf("failed to build MQL condition: %w", err)
		}
		conditions = append(conditions, condition)
	} else {
		// Simple metric alarm
		condition, err := buildSimpleCondition(config)
		if err != nil {
			return fmt.Errorf("failed to build simple condition: %w", err)
		}
		conditions = append(conditions, condition)
	}

	// Convert comparison operator
	comparisonType := convertComparisonOperator(config.ComparisonOperator)

	// Build alert policy
	policy := &monitoringpb.AlertPolicy{
		DisplayName:          config.AlarmName,
		Conditions:           conditions,
		Combiner:             monitoringpb.AlertPolicy_OR,
		Enabled:              wrapperspb.Bool(true),
		NotificationChannels: []string{}, // User must configure notification channels separately
	}

	// Set comparison in the first condition
	if len(conditions) > 0 && conditions[0].GetConditionThreshold() != nil {
		conditions[0].GetConditionThreshold().Comparison = comparisonType
	}

	// Create the alert policy
	req := &monitoringpb.CreateAlertPolicyRequest{
		Name:        fmt.Sprintf("projects/%s", projectID),
		AlertPolicy: policy,
	}

	_, err = client.CreateAlertPolicy(ctx.GetContext(), req)
	if err != nil {
		return fmt.Errorf("failed to create alert policy: %w", err)
	}

	return nil
}

// CreateGCPAlertPolicyFromRecommendation creates a GCP alert policy from a recommendation's data
func CreateGCPAlertPolicyFromRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Extract alarm config from recommendation data
	alarmConfigData, ok := recommendation.Data["alarm_config"]
	if !ok {
		return fmt.Errorf("alarm_config not found in recommendation data")
	}

	// Convert to AlarmCreationConfig
	var alarmConfig providers.AlarmCreationConfig
	configBytes, err := json.Marshal(alarmConfigData)
	if err != nil {
		return fmt.Errorf("failed to marshal alarm config: %w", err)
	}

	if err := json.Unmarshal(configBytes, &alarmConfig); err != nil {
		return fmt.Errorf("failed to unmarshal alarm config: %w", err)
	}

	// Extract project ID from recommendation data or resource ID
	projectID := extractProjectID(recommendation.ResourceId)
	if projectID == "" {
		if pid, ok := recommendation.Data["project_id"].(string); ok {
			projectID = pid
		}
	}
	if projectID == "" {
		return fmt.Errorf("project ID not found in recommendation data or resource ID")
	}

	// Create the alert policy
	return CreateGCPAlertPolicy(ctx, account, alarmConfig, projectID)
}

// buildGCPAlarmConfig builds an AlarmCreationConfig from a resource and alarm template.
// This is used to populate the alarm_config field in recommendation data so the frontend
// can show the "Create Alarm" button. Dimensions scope the alert to a specific resource.
func buildGCPAlarmConfig(resource providers.Resource, template providers.AlarmTemplate, threshold float64, dimensions []providers.AlarmDimension) providers.AlarmCreationConfig {
	return providers.AlarmCreationConfig{
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
		Dimensions:         dimensions,
	}
}

// ValidateGCPAlarmConfig validates a GCP alarm configuration
func ValidateGCPAlarmConfig(config providers.AlarmCreationConfig) error {
	if config.AlarmName == "" {
		return fmt.Errorf("alarm name is required")
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

	// Validate based on alarm type
	if len(config.Metrics) > 0 {
		// Metric math alarm - validate metrics
		hasReturnData := false
		for _, metric := range config.Metrics {
			if metric.ReturnData {
				hasReturnData = true
				break
			}
		}
		if !hasReturnData {
			return fmt.Errorf("at least one metric must have return_data: true for metric math alarms")
		}
	} else {
		// Simple metric alarm - validate basic fields
		if config.MetricName == "" {
			return fmt.Errorf("metric name is required for simple metric alarms")
		}
	}

	return nil
}

// buildSimpleCondition builds a condition for a simple metric alarm
func buildSimpleCondition(config providers.AlarmCreationConfig) (*monitoringpb.AlertPolicy_Condition, error) {
	// Determine resource type from metric name
	resourceType, err := getResourceTypeFromMetric(config.MetricName)
	if err != nil {
		return nil, fmt.Errorf("failed to determine resource type: %w", err)
	}

	// Build filter for metric - MUST include resource.type for GCP
	filter := fmt.Sprintf(`resource.type="%s" AND metric.type="%s"`, resourceType, config.MetricName)
	if len(config.Dimensions) > 0 {
		for _, dim := range config.Dimensions {
			filter += fmt.Sprintf(` AND resource.labels.%s="%s"`, dim.Name, dim.Value)
		}
	}

	// Build aggregation
	aggregation := &monitoringpb.Aggregation{
		AlignmentPeriod:  durationpb.New(time.Duration(config.Period) * time.Second),
		PerSeriesAligner: convertStatisticToAligner(config.Statistic),
	}

	return &monitoringpb.AlertPolicy_Condition{
		DisplayName: config.AlarmName,
		Condition: &monitoringpb.AlertPolicy_Condition_ConditionThreshold{
			ConditionThreshold: &monitoringpb.AlertPolicy_Condition_MetricThreshold{
				Filter:         filter,
				Aggregations:   []*monitoringpb.Aggregation{aggregation},
				Duration:       durationpb.New(time.Duration(config.Period*config.EvaluationPeriods) * time.Second),
				ThresholdValue: config.Threshold,
			},
		},
	}, nil
}

// buildMQLCondition builds a condition using Monitoring Query Language for metric math
func buildMQLCondition(config providers.AlarmCreationConfig) (*monitoringpb.AlertPolicy_Condition, error) {
	// Build MQL query from metrics with proper threshold and comparison
	var mqlQuery string
	var hasExpression bool

	// First, try to find an expression metric
	for _, metric := range config.Metrics {
		if metric.Expression != "" && metric.ReturnData {
			// This is a math expression - use it directly
			mqlQuery = metric.Expression
			hasExpression = true
			break
		}
	}

	// If no expression found, build from metric definitions
	if !hasExpression {
		// Build query from individual metrics
		var metricQuery string
		for _, metric := range config.Metrics {
			if metric.MetricStat != nil && metric.MetricStat.Metric.MetricName != "" && metric.ReturnData {
				// Get resource type for this metric
				resourceType, err := getResourceTypeFromMetric(metric.MetricStat.Metric.MetricName)
				if err != nil {
					// Try to use metric name directly if resource type lookup fails
					resourceType = "gce_instance"
				}

				// Build fetch clause
				metricQuery = fmt.Sprintf("fetch %s | metric '%s'", resourceType, metric.MetricStat.Metric.MetricName)

				// Add aggregation if statistic is specified
				if config.Statistic != "" {
					alignerFunc := convertStatisticToMQLFunction(config.Statistic)
					metricQuery += fmt.Sprintf(" | group_by [], [val: %s(val())]", alignerFunc)
				}
				break
			}
		}

		// If still no query built and we have a metric name in config, use that
		if metricQuery == "" && config.MetricName != "" {
			resourceType, err := getResourceTypeFromMetric(config.MetricName)
			if err != nil {
				return nil, fmt.Errorf("failed to determine resource type for metric %s: %w", config.MetricName, err)
			}
			metricQuery = fmt.Sprintf("fetch %s | metric '%s'", resourceType, config.MetricName)
		}

		if metricQuery == "" {
			return nil, fmt.Errorf("no valid metric or expression found for MQL condition")
		}

		mqlQuery = metricQuery
	}

	// Build the comparison condition part of the query
	comparisonOp := convertComparisonOperatorToMQL(config.ComparisonOperator)

	// Append threshold condition to the query
	mqlQuery += fmt.Sprintf(" | condition val() %s %.2f", comparisonOp, config.Threshold)

	return &monitoringpb.AlertPolicy_Condition{
		DisplayName: config.AlarmName,
		Condition: &monitoringpb.AlertPolicy_Condition_ConditionMonitoringQueryLanguage{
			ConditionMonitoringQueryLanguage: &monitoringpb.AlertPolicy_Condition_MonitoringQueryLanguageCondition{
				Query:    mqlQuery,
				Duration: durationpb.New(time.Duration(config.Period*config.EvaluationPeriods) * time.Second),
			},
		},
	}, nil
}

// convertComparisonOperatorToMQL converts alarm config comparison operator to MQL operator
func convertComparisonOperatorToMQL(operator string) string {
	switch operator {
	case "GreaterThanThreshold":
		return ">"
	case "GreaterThanOrEqualToThreshold":
		return ">="
	case "LessThanThreshold":
		return "<"
	case "LessThanOrEqualToThreshold":
		return "<="
	default:
		return ">"
	}
}

// convertStatisticToMQLFunction converts alarm config statistic to MQL aggregation function
func convertStatisticToMQLFunction(statistic string) string {
	switch strings.ToLower(statistic) {
	case "average", "avg":
		return "mean"
	case "sum":
		return "sum"
	case "minimum", "min":
		return "min"
	case "maximum", "max":
		return "max"
	case "samplecount", "count":
		return "count"
	default:
		return "mean"
	}
}

// convertComparisonOperator converts alarm config comparison operator to GCP comparison type
func convertComparisonOperator(operator string) monitoringpb.ComparisonType {
	switch operator {
	case "GreaterThanThreshold":
		return monitoringpb.ComparisonType_COMPARISON_GT
	case "GreaterThanOrEqualToThreshold":
		return monitoringpb.ComparisonType_COMPARISON_GE
	case "LessThanThreshold":
		return monitoringpb.ComparisonType_COMPARISON_LT
	case "LessThanOrEqualToThreshold":
		return monitoringpb.ComparisonType_COMPARISON_LE
	default:
		return monitoringpb.ComparisonType_COMPARISON_GT
	}
}

// convertStatisticToAligner converts alarm config statistic to GCP aligner
func convertStatisticToAligner(statistic string) monitoringpb.Aggregation_Aligner {
	switch strings.ToLower(statistic) {
	case "average", "avg":
		return monitoringpb.Aggregation_ALIGN_MEAN
	case "sum":
		return monitoringpb.Aggregation_ALIGN_SUM
	case "minimum", "min":
		return monitoringpb.Aggregation_ALIGN_MIN
	case "maximum", "max":
		return monitoringpb.Aggregation_ALIGN_MAX
	case "samplecount", "count":
		return monitoringpb.Aggregation_ALIGN_COUNT
	default:
		return monitoringpb.Aggregation_ALIGN_MEAN
	}
}

// extractProjectID extracts project ID from a GCP resource ID
func extractProjectID(resourceID string) string {
	// Resource IDs typically contain projects/{project-id}
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if part == "projects" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// getResourceTypeFromMetric maps GCP metric names to their resource types
func getResourceTypeFromMetric(metricName string) (string, error) {
	// Map common GCP metrics to their resource types
	metricToResourceType := map[string]string{
		// Compute Engine
		"compute.googleapis.com/instance/cpu/utilization":        "gce_instance",
		"compute.googleapis.com/instance/disk/read_bytes_count":  "gce_instance",
		"compute.googleapis.com/instance/disk/write_bytes_count": "gce_instance",
		"compute.googleapis.com/instance/network/received_bytes": "gce_instance",
		"compute.googleapis.com/instance/network/sent_bytes":     "gce_instance",
		"compute.googleapis.com/instance/uptime":                 "gce_instance",

		// Cloud SQL
		"cloudsql.googleapis.com/database/cpu/utilization":    "cloudsql_database",
		"cloudsql.googleapis.com/database/memory/utilization": "cloudsql_database",
		"cloudsql.googleapis.com/database/disk/utilization":   "cloudsql_database",

		// Cloud Storage
		"storage.googleapis.com/storage/total_bytes": "gcs_bucket",
		"storage.googleapis.com/api/request_count":   "gcs_bucket",

		// GKE
		"container.googleapis.com/container/cpu/usage_time":     "k8s_container",
		"container.googleapis.com/container/memory/usage_bytes": "k8s_container",

		// Cloud Functions
		"cloudfunctions.googleapis.com/function/execution_count": "cloud_function",
		"cloudfunctions.googleapis.com/function/execution_times": "cloud_function",

		// Load Balancer
		"loadbalancing.googleapis.com/https/request_count":     "https_lb_rule",
		"loadbalancing.googleapis.com/https/backend_latencies": "https_lb_rule",

		// BigQuery
		"bigquery.googleapis.com/storage/stored_bytes":  "bigquery_project",
		"bigquery.googleapis.com/storage/table_count":   "bigquery_project",
		"bigquery.googleapis.com/job/num_in_flight":     "bigquery_project",
		"bigquery.googleapis.com/slots/total_available": "bigquery_project",
		"bigquery.googleapis.com/slots/total_allocated": "bigquery_project",
	}

	if resourceType, ok := metricToResourceType[metricName]; ok {
		return resourceType, nil
	}

	// Try to infer from metric namespace
	if strings.HasPrefix(metricName, "compute.googleapis.com/") {
		return "gce_instance", nil
	} else if strings.HasPrefix(metricName, "cloudsql.googleapis.com/") {
		return "cloudsql_database", nil
	} else if strings.HasPrefix(metricName, "storage.googleapis.com/") {
		return "gcs_bucket", nil
	} else if strings.HasPrefix(metricName, "container.googleapis.com/") {
		return "k8s_container", nil
	} else if strings.HasPrefix(metricName, "cloudfunctions.googleapis.com/") {
		return "cloud_function", nil
	} else if strings.HasPrefix(metricName, "loadbalancing.googleapis.com/") {
		return "https_lb_rule", nil
	} else if strings.HasPrefix(metricName, "bigquery.googleapis.com/") {
		return "bigquery_project", nil
	}

	// Return error for unknown metrics instead of defaulting
	return "", fmt.Errorf("unknown resource type for metric: %s", metricName)
}
