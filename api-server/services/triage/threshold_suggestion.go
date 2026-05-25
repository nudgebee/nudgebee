package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/cloud"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/prometheus/promql/parser"
)

// Supported sources for threshold suggestions
var thresholdSuggestionSources = map[string]bool{
	"AWS_CloudWatch_Alarm":  true,
	"azure_monitor_webhook": true,
	"Azure_Monitor_Alert":   true,
	"prometheus":            true,
	"GCP_Metric_Alert":      true,
	"pagerduty_webhook":     true,
}

// gcpMetricTypeRegex extracts metric.type from GCP monitoring filter strings
var gcpMetricTypeRegex = regexp.MustCompile(`metric\.type\s*=\s*"([^"]+)"`)

// GetThresholdSuggestion analyzes an alert event and suggests a threshold adjustment
func GetThresholdSuggestion(ctx context.Context, db *sqlx.DB, ev models.Event, tenantID string) ThresholdSuggestionResponse {
	source := ""
	if ev.Source != nil {
		source = *ev.Source
	}

	if !thresholdSuggestionSources[source] {
		return ThresholdSuggestionResponse{
			Available: false,
			Source:    source,
			Error:     "threshold suggestions not supported for source: " + source,
		}
	}

	// Extract alert definition based on source
	var alertDef *AlertDefinition
	var err error

	switch source {
	case "AWS_CloudWatch_Alarm":
		alertDef, err = extractAWSAlertDefinition(ctx, db, ev)
	case "azure_monitor_webhook", "Azure_Monitor_Alert":
		alertDef, err = extractAzureAlertDefinition(ctx, db, ev)
	case "prometheus":
		alertDef, err = extractPrometheusAlertDefinition(ctx, db, ev)
	case "GCP_Metric_Alert":
		alertDef, err = extractGCPAlertDefinition(ctx, db, ev)
	case "pagerduty_webhook":
		alertDef, err = extractPagerDutyAlertDefinition(ctx, db, ev)
	}

	if err != nil || alertDef == nil {
		errMsg := "failed to extract alert definition"
		if err != nil {
			errMsg = err.Error()
		}
		return ThresholdSuggestionResponse{
			Available: false,
			Source:    source,
			Error:     errMsg,
		}
	}

	// --- Eligibility gate ---
	// Check whether this alert type is eligible for threshold tuning at all,
	// before doing expensive metric queries and computation.
	if reason := checkThresholdEligibility(alertDef); reason != "" {
		return ThresholdSuggestionResponse{
			Available:       true,
			Source:          source,
			AlertDefinition: alertDef,
			Suggestion: &ThresholdSuggestion{
				RecommendationType: "not_eligible",
				Reason:             reason,
				Confidence:         "low",
			},
		}
	}

	// Get firing analysis
	accountID := ""
	if ev.CloudAccountId != nil {
		accountID = *ev.CloudAccountId
	}

	firingAnalysis := getFiringAnalysisForEvent(ctx, db, ev, source, accountID, tenantID)

	// Fetch metric history from source API
	metricHistory := fetchMetricHistory(ctx, ev, alertDef, tenantID)

	// Compute alert quality score
	labels := GetLabelsMap(ev)
	alertRuleKey := ExtractAlertRuleKey(source, labels)
	var alertQuality *AlertQualityScore
	if alertRuleKey != "" {
		alertQuality = computeAlertQuality(ctx, db, source, alertRuleKey, accountID, tenantID)
	}

	// Compute suggestion
	suggestion := computeSuggestion(alertDef, metricHistory, firingAnalysis, alertQuality)

	// If no suggestion could be computed, provide a specific error instead of the generic "no suggestion available"
	var computeError string
	if suggestion == nil {
		noMetricData := metricHistory == nil || len(metricHistory.Values) == 0
		noFiringData := firingAnalysis == nil || len(firingAnalysis.MetricValuesAtFiring) == 0
		if noMetricData && noFiringData {
			computeError = fmt.Sprintf("metric query returned no data for %q (service: %s, account: %s)", alertDef.MetricName, source, accountID)
		} else if noMetricData {
			computeError = fmt.Sprintf("metric query returned no data for %q but firing analysis available — insufficient data for suggestion", alertDef.MetricName)
		}
	}

	// Run guardrails: validate suggestion against metric semantics and operational risk
	if suggestion != nil {
		risk := ValidateSuggestion(suggestion, alertDef, alertQuality)
		if risk != nil {
			suggestion.RiskLevel = risk.Level
			suggestion.RiskWarnings = risk.Warnings
			suggestion.Confidence = AdjustConfidenceForRisk(suggestion.Confidence, risk)
			// Rebuild user-facing reason with guardrail context
			suggestion.Reason = BuildUserFacingReason(suggestion, alertDef, risk)
		}
	}

	queryMeta := buildQueryMetadata(source, labels, alertDef)

	return ThresholdSuggestionResponse{
		Available:       true,
		Source:          source,
		AlertDefinition: alertDef,
		FiringAnalysis:  firingAnalysis,
		MetricHistory:   metricHistory,
		Suggestion:      suggestion,
		AlertQuality:    alertQuality,
		QueryMetadata:   queryMeta,
		Error:           computeError,
	}
}

// extractAWSAlertDefinition extracts the alert definition from AWS CloudWatch alarm labels and evidences
func extractAWSAlertDefinition(ctx context.Context, db *sqlx.DB, ev models.Event) (*AlertDefinition, error) {
	labels := GetLabelsMap(ev)
	if labels == nil {
		return nil, fmt.Errorf("event has no labels")
	}

	metricName, _ := labels["aws_event_metric_name"].(string)
	if metricName == "" {
		return nil, fmt.Errorf("missing aws_event_metric_name label")
	}

	namespace, _ := labels["aws_event_metric_namespace"].(string)
	statistic, _ := labels["aws_event_metric_statistic"].(string)
	alarmName, _ := labels["aws_event_name"].(string)
	alarmARN, _ := labels["aws_event_arn"].(string)

	// Parse threshold from label
	thresholdStr, _ := labels["aws_event_threshold"].(string)
	threshold, err := strconv.ParseFloat(thresholdStr, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid or missing aws_event_threshold: %q", thresholdStr)
	}

	alertDef := &AlertDefinition{
		MetricName:       metricName,
		MetricNamespace:  namespace,
		CurrentThreshold: threshold,
		Aggregation:      statistic,
		AlarmName:        alarmName,
		AlarmARN:         alarmARN,
	}

	// Extract ComparisonOperator, Period, EvaluationPeriods from evidences
	extractAWSEvidenceFields(ctx, db, ev.Id, alertDef)

	return alertDef, nil
}

// extractAWSEvidenceFields parses the raw CloudWatch alarm JSON from evidences[0]
func extractAWSEvidenceFields(ctx context.Context, db *sqlx.DB, eventID string, alertDef *AlertDefinition) {
	if ev, ok := getEvidenceRawEvent(ctx, db, eventID); ok {
		if operator, ok := ev["ComparisonOperator"].(string); ok {
			alertDef.Operator = operator
		}
		if period, ok := ev["Period"].(float64); ok {
			alertDef.Period = int(period)
		}
		if evalPeriods, ok := ev["EvaluationPeriods"].(float64); ok {
			alertDef.EvaluationPeriods = int(evalPeriods)
		}
	}
}

// getEvidenceRawEvent fetches evidences for an event and returns the raw event data (evidences[0])
func getEvidenceRawEvent(ctx context.Context, db *sqlx.DB, eventID string) (map[string]interface{}, bool) {
	var evidencesJSON string
	err := db.GetContext(ctx, &evidencesJSON,
		"SELECT evidences FROM events WHERE id = $1", eventID)
	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to get evidences", "event_id", eventID, "error", err)
		return nil, false
	}

	var evidences []interface{}
	if err := json.Unmarshal([]byte(evidencesJSON), &evidences); err != nil {
		return nil, false
	}

	if len(evidences) == 0 {
		return nil, false
	}

	// evidences[0] is the Raw Event containing the full CloudWatch alarm definition
	firstEvidence, ok := evidences[0].(map[string]interface{})
	if !ok {
		return nil, false
	}

	// The raw event data is in the "data" field — may be an object or a JSON string
	switch data := firstEvidence["data"].(type) {
	case map[string]interface{}:
		return data, true
	case string:
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(data), &parsed); err == nil {
			return parsed, true
		}
	}

	// Fallback: the evidence itself might be the data
	return firstEvidence, true
}

// extractAzureAlertDefinition extracts the alert definition from Azure Monitor labels.
// Handles two label formats:
//   - azure_alert_criteria (majority, 1130+ events): allOf at top level
//   - azure_alert_context (37 events, webhook format): condition.allOf nested
//
// Falls back to event_rules.expr if labels don't contain criteria data.
func extractAzureAlertDefinition(ctx context.Context, db *sqlx.DB, ev models.Event) (*AlertDefinition, error) {
	labels := GetLabelsMap(ev)
	if labels == nil {
		return nil, fmt.Errorf("event has no labels")
	}

	metricCond, err := extractAzureMetricCondition(labels)
	if err != nil {
		// Fallback: try event_rules.expr for criteria data
		metricCond, err = extractAzureMetricConditionFromRules(ctx, db, ev, labels)
		if err != nil {
			return nil, err
		}
	}

	// Detect Log Analytics / Scheduled Query Rules alerts — these use a KQL query
	// instead of a metric name and are not supported for threshold tuning.
	// Also detect metricMeasureColumn which is another Log Analytics indicator.
	if query, hasQuery := metricCond["query"].(string); hasQuery && query != "" {
		return nil, fmt.Errorf("azure Log Analytics (Scheduled Query Rules) alerts are not supported for threshold tuning — they use KQL queries, not metric thresholds")
	}
	if _, hasMeasureCol := metricCond["metricMeasureColumn"].(string); hasMeasureCol {
		if _, hasMetric := metricCond["metricName"].(string); !hasMetric {
			return nil, fmt.Errorf("azure Log Analytics alert detected (has metricMeasureColumn but no metricName) — not supported for threshold tuning")
		}
	}

	metricName, ok := metricCond["metricName"].(string)
	if !ok || metricName == "" {
		return nil, fmt.Errorf("metricName not found in alert criteria")
	}

	threshold, ok := metricCond["threshold"].(float64)
	if !ok {
		return nil, fmt.Errorf("threshold not found or not a number in alert criteria")
	}

	operator, _ := metricCond["operator"].(string)
	timeAggregation, _ := metricCond["timeAggregation"].(string)
	metricNamespace, _ := metricCond["metricNamespace"].(string)

	alertDef := &AlertDefinition{
		MetricName:       metricName,
		MetricNamespace:  metricNamespace,
		Operator:         operator,
		CurrentThreshold: threshold,
		Aggregation:      timeAggregation,
	}

	// Parse window size (e.g., "PT5M" -> 300 seconds)
	if windowSize, ok := labels["azure_alert_window_size"].(string); ok {
		alertDef.Period = parseISODurationToSeconds(windowSize)
	}

	// Try both label names for alert name
	if name, _ := labels["azure_alert_name"].(string); name != "" {
		alertDef.AlarmName = name
	} else if name, _ := labels["alertname"].(string); name != "" {
		alertDef.AlarmName = name
	}

	return alertDef, nil
}

// extractAzureMetricCondition gets the first metric condition from either
// azure_alert_criteria or azure_alert_context labels
func extractAzureMetricCondition(labels map[string]interface{}) (map[string]interface{}, error) {
	// Try azure_alert_criteria first (majority of events)
	// Structure: {"allOf": [{"metricName":..., "operator":..., "threshold":...}]}
	if criteriaStr, ok := labels["azure_alert_criteria"].(string); ok && criteriaStr != "" {
		var criteria map[string]interface{}
		if err := json.Unmarshal([]byte(criteriaStr), &criteria); err == nil {
			if allOf, ok := criteria["allOf"].([]interface{}); ok && len(allOf) > 0 {
				if cond, ok := allOf[0].(map[string]interface{}); ok {
					return cond, nil
				}
			}
		}
	}

	// Fallback: azure_alert_context (webhook format)
	// Structure: {"condition": {"allOf": [{"metricName":..., ...}]}}
	if contextStr, ok := labels["azure_alert_context"].(string); ok && contextStr != "" {
		var alertCtx map[string]interface{}
		if err := json.Unmarshal([]byte(contextStr), &alertCtx); err == nil {
			if condMap, ok := alertCtx["condition"].(map[string]interface{}); ok {
				if allOf, ok := condMap["allOf"].([]interface{}); ok && len(allOf) > 0 {
					if cond, ok := allOf[0].(map[string]interface{}); ok {
						return cond, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("no azure metric criteria found (checked azure_alert_criteria and azure_alert_context)")
}

// extractAzureMetricConditionFromRules looks up the event_rules table for Azure alert criteria
// when labels don't contain criteria data directly.
func extractAzureMetricConditionFromRules(ctx context.Context, db *sqlx.DB, ev models.Event, labels map[string]interface{}) (map[string]interface{}, error) {
	alertName, _ := labels["azure_alert_name"].(string)
	if alertName == "" {
		alertName, _ = labels["alertname"].(string)
	}
	if alertName == "" {
		return nil, fmt.Errorf("no azure alert name found for event_rules lookup")
	}

	accountID := ""
	if ev.CloudAccountId != nil {
		accountID = *ev.CloudAccountId
	}
	if accountID == "" {
		return nil, fmt.Errorf("missing cloud_account_id for event_rules lookup")
	}

	var expr string
	err := db.QueryRowContext(ctx,
		"SELECT expr FROM event_rules WHERE alert = $1 AND account_id = $2 AND enabled = true AND expr IS NOT NULL AND expr != '' LIMIT 1",
		alertName, accountID).Scan(&expr)
	if err != nil {
		return nil, fmt.Errorf("no event_rule found for azure alert %q: %w", alertName, err)
	}

	// event_rules.expr for Azure contains criteria JSON similar to azure_alert_criteria
	var criteria map[string]interface{}
	if err := json.Unmarshal([]byte(expr), &criteria); err != nil {
		return nil, fmt.Errorf("failed to parse event_rules expr as JSON: %w", err)
	}

	if allOf, ok := criteria["allOf"].([]interface{}); ok && len(allOf) > 0 {
		if cond, ok := allOf[0].(map[string]interface{}); ok {
			return cond, nil
		}
	}

	return nil, fmt.Errorf("event_rules expr does not contain valid allOf criteria")
}

// fetchMetricHistory queries the source API for 7-day metric history
// buildQueryMetadata constructs the metadata needed to re-query metrics from the frontend.
func buildQueryMetadata(source string, labels map[string]interface{}, alertDef *AlertDefinition) *MetricQueryMetadata {
	if alertDef == nil {
		return nil
	}

	statistics := []string{alertDef.Aggregation}
	if alertDef.Aggregation == "" {
		statistics = []string{"Average"}
	}

	switch source {
	case "AWS_CloudWatch_Alarm":
		region, _ := labels["aws_region"].(string)
		serviceName, _ := labels["aws_service_name"].(string)
		var dimensions []map[string]string
		if dimStr, ok := labels["aws_event_alarm_dimensions"].(string); ok && dimStr != "" {
			var dimArr []map[string]interface{}
			if err := json.Unmarshal([]byte(dimStr), &dimArr); err == nil {
				for _, dim := range dimArr {
					name, _ := dim["Name"].(string)
					value, _ := dim["Value"].(string)
					if name != "" && value != "" && !strings.HasPrefix(name, "@") {
						dimensions = append(dimensions, map[string]string{name: value})
					}
				}
			}
		}
		return &MetricQueryMetadata{
			MetricProvider:  "aws_cloudwatch",
			ServiceName:     serviceName,
			Region:          region,
			MetricNames:     []string{alertDef.MetricName},
			MetricNamespace: alertDef.MetricNamespace,
			Dimensions:      dimensions,
			Statistics:      statistics,
		}

	case "azure_monitor_webhook", "Azure_Monitor_Alert":
		region, _ := labels["azure_region"].(string)
		serviceName, _ := labels["azure_service_name"].(string)
		resourceIDs := extractAzureResourceIDs(labels)
		return &MetricQueryMetadata{
			MetricProvider:  "azure_monitor",
			ServiceName:     serviceName,
			Region:          region,
			MetricNames:     []string{alertDef.MetricName},
			MetricNamespace: alertDef.MetricNamespace,
			ResourceIds:     resourceIDs,
			Statistics:      statistics,
		}

	case "GCP_Metric_Alert":
		region, _ := labels["gcp_region"].(string)
		if region == "" {
			region, _ = labels["region"].(string)
		}
		serviceName, _ := labels["gcp_service_name"].(string)
		if serviceName == "" {
			serviceName = "Cloud Monitoring"
		}
		return &MetricQueryMetadata{
			MetricProvider: "gcp_monitoring",
			ServiceName:    serviceName,
			Region:         region,
			MetricNames:    []string{alertDef.MetricName},
			Statistics:     statistics,
		}

	case "prometheus", "pagerduty_webhook":
		return &MetricQueryMetadata{
			MetricProvider: "prometheus",
			PromQL:         alertDef.MetricName,
		}
	}

	return nil
}

func fetchMetricHistory(ctx context.Context, ev models.Event, alertDef *AlertDefinition, tenantID string) *MetricHistory {
	labels := GetLabelsMap(ev)
	if labels == nil {
		return nil
	}

	source := ""
	if ev.Source != nil {
		source = *ev.Source
	}

	now := time.Now()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)

	cloudAccountID := ""
	if ev.CloudAccountId != nil {
		cloudAccountID = *ev.CloudAccountId
	}

	switch source {
	case "AWS_CloudWatch_Alarm":
		return fetchAWSMetricHistory(ctx, labels, alertDef, tenantID, cloudAccountID, sevenDaysAgo, now)
	case "azure_monitor_webhook", "Azure_Monitor_Alert":
		return fetchAzureMetricHistory(ctx, labels, alertDef, tenantID, cloudAccountID, sevenDaysAgo, now)
	case "prometheus":
		return fetchPrometheusMetricHistory(ctx, ev, alertDef, sevenDaysAgo, now)
	case "GCP_Metric_Alert":
		return fetchGCPMetricHistory(ctx, labels, alertDef, tenantID, cloudAccountID, sevenDaysAgo, now)
	case "pagerduty_webhook":
		// PagerDuty alerts originate from prometheus/alertmanager, reuse prometheus metric fetch
		return fetchPrometheusMetricHistory(ctx, ev, alertDef, sevenDaysAgo, now)
	}

	return nil
}

// fetchAWSMetricHistory fetches metric data from AWS CloudWatch
func fetchAWSMetricHistory(ctx context.Context, labels map[string]interface{}, alertDef *AlertDefinition, tenantID string, accountID string, startTime, endTime time.Time) *MetricHistory {
	region, _ := labels["aws_region"].(string)
	serviceName, _ := labels["aws_service_name"].(string)

	if region == "" {
		slog.WarnContext(ctx, "threshold_suggestion: missing aws_region")
		return nil
	}
	if accountID == "" {
		slog.WarnContext(ctx, "threshold_suggestion: missing cloud account ID for AWS alert")
		return nil
	}

	reqCtx := security.NewRequestContextForTenantAdmin(tenantID, slog.Default(), nil, nil)

	// Parse dimensions from labels
	var dimensions []map[string]string
	if dimStr, ok := labels["aws_event_alarm_dimensions"].(string); ok && dimStr != "" {
		var dimArr []map[string]interface{}
		if err := json.Unmarshal([]byte(dimStr), &dimArr); err == nil {
			for _, dim := range dimArr {
				name, _ := dim["Name"].(string)
				value, _ := dim["Value"].(string)
				// Skip EventBridge metadata dimensions (e.g. @aws.account, @aws.region)
				if name == "" || value == "" || strings.HasPrefix(name, "@") {
					continue
				}
				dimensions = append(dimensions, map[string]string{name: value})
			}
		}
	}

	// For custom namespaces, use CLI approach
	if alertDef.MetricNamespace != "" && !strings.HasPrefix(alertDef.MetricNamespace, "AWS/") {
		return fetchAWSCustomNamespaceMetrics(ctx, reqCtx, accountID, alertDef, region, dimensions, startTime, endTime)
	}

	// Standard AWS namespace - use QueryMetrics
	statistics := []string{alertDef.Aggregation}
	if alertDef.Aggregation == "" {
		statistics = []string{"Average"}
	}

	step := calcStep(startTime, endTime)
	metricsResp, err := cloud.QueryMetrics(reqCtx, cloud.QueryMetricsRequest{
		AccountId: accountID,
		Query: cloud.MetricsQuery{
			StartDate:       &startTime,
			EndDate:         &endTime,
			ServiceName:     serviceName,
			Region:          region,
			MetricNames:     []string{alertDef.MetricName},
			MetricNamespace: alertDef.MetricNamespace,
			Dimensions:      dimensions,
			Statistics:      statistics,
			Step:            step,
		},
	})

	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to query AWS metrics", "error", err)
		return nil
	}

	return metricsResponseToHistory(metricsResp, startTime, endTime, int(step.Seconds()))
}

// safeCliArg validates that a string only contains safe characters for CLI arguments.
// Allows alphanumeric, underscores, hyphens, dots, slashes, colons, and spaces.
var safeCliArgPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-./: ]+$`)

func sanitizeCliArg(s string) string {
	if safeCliArgPattern.MatchString(s) {
		return s
	}
	// Strip unsafe characters
	return safeCliArgPattern.FindString(s)
}

// fetchAWSCustomNamespaceMetrics fetches metrics for custom (non-AWS/) namespaces via CLI
func fetchAWSCustomNamespaceMetrics(ctx context.Context, reqCtx *security.RequestContext, accountID string, alertDef *AlertDefinition, region string, dimensions []map[string]string, startTime, endTime time.Time) *MetricHistory {
	statistic := alertDef.Aggregation
	if statistic == "" {
		statistic = "Sum"
	}

	// Sanitize inputs that come from external webhook labels
	namespace := sanitizeCliArg(alertDef.MetricNamespace)
	metricName := sanitizeCliArg(alertDef.MetricName)
	statistic = sanitizeCliArg(statistic)
	region = sanitizeCliArg(region)

	// Build dimensions parameter
	dimensionsParam := ""
	if len(dimensions) > 0 {
		var dimParts []string
		for _, dim := range dimensions {
			for name, value := range dim {
				safeName := sanitizeCliArg(name)
				safeValue := sanitizeCliArg(value)
				if safeName == "" || safeValue == "" {
					continue
				}
				dimParts = append(dimParts, fmt.Sprintf("Name=%s,Value=%s", safeName, safeValue))
			}
		}
		if len(dimParts) > 0 {
			dimensionsParam = fmt.Sprintf(" --dimensions %s", strings.Join(dimParts, " "))
		}
	}

	step := calcStep(startTime, endTime)
	periodSeconds := int(step.Seconds())

	command := fmt.Sprintf(
		`aws cloudwatch get-metric-statistics --namespace "%s" --metric-name "%s" --statistics %s --start-time "%s" --end-time "%s" --period %d --region %s%s --output json`,
		namespace,
		metricName,
		statistic,
		startTime.Format(time.RFC3339),
		endTime.Format(time.RFC3339),
		periodSeconds,
		region,
		dimensionsParam,
	)

	cliResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   command,
	})
	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to execute AWS CLI for custom metrics", "error", err)
		return nil
	}

	dataStr, ok := cliResp["data"].(string)
	if !ok {
		return nil
	}

	var awsResp map[string]interface{}
	if err := json.Unmarshal([]byte(dataStr), &awsResp); err != nil {
		return nil
	}

	datapoints, ok := awsResp["Datapoints"].([]interface{})
	if !ok {
		return nil
	}

	type dp struct {
		ts    time.Time
		value float64
	}
	var points []dp

	for _, raw := range datapoints {
		dpMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		tsStr, _ := dpMap["Timestamp"].(string)
		value, _ := dpMap[statistic].(float64)
		if ts, err := time.Parse(time.RFC3339, tsStr); err == nil {
			points = append(points, dp{ts: ts, value: value})
		}
	}

	sort.Slice(points, func(i, j int) bool {
		return points[i].ts.Before(points[j].ts)
	})

	timestamps := make([]int64, len(points))
	values := make([]float64, len(points))
	for i, p := range points {
		timestamps[i] = p.ts.Unix()
		values[i] = p.value
	}

	return &MetricHistory{
		Timestamps: timestamps,
		Values:     values,
		StartTime:  startTime.Format(time.RFC3339),
		EndTime:    endTime.Format(time.RFC3339),
		Step:       periodSeconds,
	}
}

// fetchAzureMetricHistory fetches metric data from Azure Monitor.
// Handles two label formats: azure_alert_target_resources / azure_alert_criteria (majority)
// and azure_essentials / azure_alert_context (webhook format).
func fetchAzureMetricHistory(ctx context.Context, labels map[string]interface{}, alertDef *AlertDefinition, tenantID string, accountID string, startTime, endTime time.Time) *MetricHistory {
	// Region: try azure_region first, then region
	region := ""
	for _, key := range []string{"azure_region", "region"} {
		if v, ok := labels[key].(string); ok && v != "" {
			region = v
			break
		}
	}

	// Service name: try azure_service_name first, then service_name
	serviceName := ""
	for _, key := range []string{"azure_service_name", "service_name"} {
		if v, ok := labels[key].(string); ok && v != "" {
			serviceName = v
			break
		}
	}

	// Extract target resource IDs from either label format
	resourceIDs := extractAzureResourceIDs(labels)
	if len(resourceIDs) == 0 {
		slog.WarnContext(ctx, "threshold_suggestion: no Azure target resource IDs found")
		return nil
	}

	if accountID == "" {
		slog.WarnContext(ctx, "threshold_suggestion: missing cloud account ID for Azure alert")
		return nil
	}

	reqCtx := security.NewRequestContextForTenantAdmin(tenantID, slog.Default(), nil, nil)

	// Parse dimensions from metric condition
	var dimensions []map[string]string
	if metricCond, err := extractAzureMetricCondition(labels); err == nil {
		if dimsRaw, ok := metricCond["dimensions"].([]interface{}); ok {
			dimMap := make(map[string]string)
			for _, dimItemRaw := range dimsRaw {
				if dimItem, ok := dimItemRaw.(map[string]interface{}); ok {
					name, _ := dimItem["name"].(string)
					value, _ := dimItem["value"].(string)
					if name != "" {
						dimMap[name] = value
					}
				}
			}
			if len(dimMap) > 0 {
				dimensions = append(dimensions, dimMap)
			}
		}
	}

	statistics := []string{alertDef.Aggregation}
	if alertDef.Aggregation == "" {
		statistics = []string{"Average"}
	}

	step := calcStep(startTime, endTime)
	if alertDef.Period > 0 {
		periodStep := time.Duration(alertDef.Period) * time.Second
		if periodStep > step {
			step = periodStep
		}
	}

	metricsResp, err := cloud.QueryMetrics(reqCtx, cloud.QueryMetricsRequest{
		AccountId: accountID,
		Query: cloud.MetricsQuery{
			StartDate:       &startTime,
			EndDate:         &endTime,
			ServiceName:     serviceName,
			Region:          region,
			MetricNames:     []string{alertDef.MetricName},
			MetricNamespace: alertDef.MetricNamespace,
			ResourceIds:     resourceIDs,
			Dimensions:      dimensions,
			Statistics:      statistics,
			Step:            step,
		},
	})

	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to query Azure metrics", "error", err)
		return nil
	}

	return metricsResponseToHistory(metricsResp, startTime, endTime, int(step.Seconds()))
}

// --- GCP support ---

// extractGCPAlertDefinition extracts alert definition from a GCP Metric Alert event
// by parsing the full alert policy JSON stored in event_rules.expr.
func extractGCPAlertDefinition(ctx context.Context, db *sqlx.DB, ev models.Event) (*AlertDefinition, error) {
	labels := GetLabelsMap(ev)
	if labels == nil {
		return nil, fmt.Errorf("event has no labels")
	}

	policyName, _ := labels["gcp_policy_name"].(string)
	if policyName == "" {
		return nil, fmt.Errorf("missing gcp_policy_name label")
	}

	accountID := ""
	if ev.CloudAccountId != nil {
		accountID = *ev.CloudAccountId
	}
	if accountID == "" {
		return nil, fmt.Errorf("missing cloud_account_id")
	}

	// Look up the full alert policy from event_rules
	var expr string
	err := db.QueryRowContext(ctx,
		"SELECT expr FROM event_rules WHERE alert = $1 AND account_id = $2 AND enabled = true AND expr IS NOT NULL AND expr != '' LIMIT 1",
		policyName, accountID).Scan(&expr)
	if err != nil {
		return nil, fmt.Errorf("no event_rule found for GCP policy %q: %w", policyName, err)
	}

	// Parse the policy JSON (protojson serialized AlertPolicy)
	var policy map[string]interface{}
	if err := json.Unmarshal([]byte(expr), &policy); err != nil {
		return nil, fmt.Errorf("failed to parse GCP policy JSON: %w", err)
	}

	// Extract condition threshold from conditions[0].conditionThreshold
	conditions, _ := policy["conditions"].([]interface{})
	if len(conditions) == 0 {
		return nil, fmt.Errorf("no conditions in GCP policy")
	}
	cond, _ := conditions[0].(map[string]interface{})
	if cond == nil {
		return nil, fmt.Errorf("invalid condition format in GCP policy")
	}

	condThreshold, _ := cond["conditionThreshold"].(map[string]interface{})
	if condThreshold == nil {
		return nil, fmt.Errorf("no conditionThreshold in GCP policy condition")
	}

	alertDef := &AlertDefinition{
		AlarmName: policyName,
	}

	// Extract threshold value
	if tv, ok := condThreshold["thresholdValue"].(float64); ok {
		alertDef.CurrentThreshold = tv
	}

	// Extract comparison operator
	if comp, ok := condThreshold["comparison"].(string); ok {
		alertDef.Operator = mapGCPComparison(comp)
	}

	// Extract metric type from filter
	if filter, ok := condThreshold["filter"].(string); ok {
		alertDef.MetricName = extractMetricTypeFromFilter(filter)
	}

	// Fallback metric name from labels
	if alertDef.MetricName == "" {
		if mt, ok := labels["gcp_metric_type"].(string); ok {
			alertDef.MetricName = mt
		}
	}

	// Extract aggregation
	if aggs, ok := condThreshold["aggregations"].([]interface{}); ok && len(aggs) > 0 {
		if agg, ok := aggs[0].(map[string]interface{}); ok {
			if aligner, ok := agg["perSeriesAligner"].(string); ok {
				alertDef.Aggregation = aligner
			}
		}
	}

	// Extract duration as period
	if durStr, ok := condThreshold["duration"].(string); ok {
		alertDef.Period = parseGCPDuration(durStr)
	}

	return alertDef, nil
}

// mapGCPComparison maps GCP comparison enum to operator string
func mapGCPComparison(comp string) string {
	switch comp {
	case "COMPARISON_GT":
		return ">"
	case "COMPARISON_GE":
		return ">="
	case "COMPARISON_LT":
		return "<"
	case "COMPARISON_LE":
		return "<="
	case "COMPARISON_EQ":
		return "=="
	case "COMPARISON_NE":
		return "!="
	default:
		return comp
	}
}

// extractMetricTypeFromFilter extracts metric.type value from a GCP monitoring filter string.
// Filter format: metric.type="compute.googleapis.com/instance/cpu/utilization" AND resource.type="gce_instance"
func extractMetricTypeFromFilter(filter string) string {
	matches := gcpMetricTypeRegex.FindStringSubmatch(filter)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// parseGCPDuration parses a GCP duration string like "60s", "300s" to seconds
func parseGCPDuration(durStr string) int {
	durStr = strings.TrimSpace(durStr)
	if strings.HasSuffix(durStr, "s") {
		if secs, err := strconv.Atoi(strings.TrimSuffix(durStr, "s")); err == nil {
			return secs
		}
	}
	return 0
}

// mapGCPAlignerToStatistic converts a GCP per-series aligner enum to the
// statistic name expected by the cloud-collector's QueryMetrics API.
func mapGCPAlignerToStatistic(aligner string) string {
	switch aligner {
	case "ALIGN_MEAN":
		return "Average"
	case "ALIGN_SUM":
		return "Sum"
	case "ALIGN_MAX":
		return "Maximum"
	case "ALIGN_MIN":
		return "Minimum"
	case "ALIGN_COUNT":
		return "Count"
	case "ALIGN_DELTA":
		return "Delta"
	default:
		return "Average"
	}
}

// fetchGCPMetricHistory fetches metric data from GCP Cloud Monitoring
func fetchGCPMetricHistory(ctx context.Context, labels map[string]interface{}, alertDef *AlertDefinition, tenantID string, accountID string, startTime, endTime time.Time) *MetricHistory {
	serviceName, _ := labels["gcp_service_name"].(string)
	// Don't pass region — event labels often have truncated values (e.g. "us-central" instead of
	// "us-central1") that cause empty results. The account ID already scopes to the right project.

	if accountID == "" {
		slog.WarnContext(ctx, "threshold_suggestion: missing cloud account ID for GCP alert")
		return nil
	}

	reqCtx := security.NewRequestContextForTenantAdmin(tenantID, slog.Default(), nil, nil)

	metricName := alertDef.MetricName
	if metricName == "" {
		return nil
	}

	// GCP alert policies use full metric type paths like "cloudsql.googleapis.com/database/cpu/utilization".
	// If gcp_service_name is empty or doesn't match a known service in the cloud-collector,
	// fall back to "Cloud Monitoring" which can query any metric by its full type path.
	if serviceName == "" {
		serviceName = "Cloud Monitoring"
		slog.InfoContext(ctx, "threshold_suggestion: gcp_service_name empty, using Cloud Monitoring service for metric query",
			"metric", metricName)
	}

	// Map GCP aligner enum (e.g. ALIGN_SUM) to cloud-collector statistic name (e.g. Sum)
	statistics := []string{mapGCPAlignerToStatistic(alertDef.Aggregation)}

	step := calcStep(startTime, endTime)

	metricsResp, err := cloud.QueryMetrics(reqCtx, cloud.QueryMetricsRequest{
		AccountId: accountID,
		Query: cloud.MetricsQuery{
			StartDate:   &startTime,
			EndDate:     &endTime,
			ServiceName: serviceName,
			MetricNames: []string{metricName},
			Statistics:  statistics,
			Step:        step,
		},
	})

	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to query GCP metrics",
			"error", err, "service", serviceName, "metric", metricName, "account", accountID)
		return nil
	}

	history := metricsResponseToHistory(metricsResp, startTime, endTime, int(step.Seconds()))
	if history == nil || len(history.Values) == 0 {
		slog.WarnContext(ctx, "threshold_suggestion: GCP metric query returned no data",
			"service", serviceName, "metric", metricName, "account", accountID)
	}
	return history
}

// --- PagerDuty support ---

// extractPagerDutyAlertDefinition extracts alert definition from a PagerDuty webhook event.
// PagerDuty alerts that originate from prometheus/alertmanager have nb_alert_name matching
// the prometheus alertname, so we reuse the prometheus extraction logic.
func extractPagerDutyAlertDefinition(ctx context.Context, db *sqlx.DB, ev models.Event) (*AlertDefinition, error) {
	labels := GetLabelsMap(ev)
	if labels == nil {
		return nil, fmt.Errorf("event has no labels")
	}

	alertName, _ := labels["nb_alert_name"].(string)
	if alertName == "" {
		// Fallback to standard alertname
		alertName, _ = labels["alertname"].(string)
	}
	if alertName == "" {
		return nil, fmt.Errorf("missing nb_alert_name/alertname label")
	}

	accountID := ""
	if ev.CloudAccountId != nil {
		accountID = *ev.CloudAccountId
	}
	if accountID == "" {
		return nil, fmt.Errorf("missing cloud_account_id")
	}

	// Look up PromQL expression from event_rules (same as prometheus)
	var expr string
	err := db.QueryRowContext(ctx,
		"SELECT expr FROM event_rules WHERE alert = $1 AND account_id = $2 AND enabled = true AND expr IS NOT NULL AND expr != '' LIMIT 1",
		alertName, accountID).Scan(&expr)
	if err != nil {
		return nil, fmt.Errorf("no event_rule found for pagerduty alert %q: %w", alertName, err)
	}

	threshold, err := extractPromQLThreshold(expr)
	if err != nil {
		return nil, fmt.Errorf("cannot extract threshold from PromQL %q: %w", expr, err)
	}

	return &AlertDefinition{
		MetricName:       threshold.QueryExpr,
		Operator:         threshold.Operator,
		CurrentThreshold: threshold.Value,
		AlarmName:        alertName,
	}, nil
}

// --- Prometheus support ---

// promQLThreshold holds the decomposed parts of a PromQL expression with a comparison
type promQLThreshold struct {
	QueryExpr      string  // PromQL expression without the comparison (directly queryable)
	Operator       string  // ">", ">=", "<", "<="
	Value          float64 // threshold number
	ExpressionType string  // "simple", "compound_and", "compound_or"
}

// extractPromQLThreshold parses a PromQL expression and extracts the comparison operator and threshold value.
// Supports patterns like "expr > N", "N < expr", "(expr) >= N".
// Returns an error for expressions without a clear threshold comparison (absent(), multi-condition, etc.).
func extractPromQLThreshold(exprStr string) (*promQLThreshold, error) {
	exprLower := strings.ToLower(exprStr)

	// Pre-check: absent() / absent_over_time() expressions detect missing metrics, not threshold violations.
	// There's no static threshold to tune.
	if strings.Contains(exprLower, "absent(") || strings.Contains(exprLower, "absent_over_time(") {
		return nil, fmt.Errorf("expression uses absent() — detects missing metrics, not threshold violations")
	}

	expr, err := parser.ParseExpr(exprStr)
	if err != nil {
		return nil, fmt.Errorf("invalid PromQL: %w", err)
	}

	// Unwrap top-level parentheses
	for {
		if paren, ok := expr.(*parser.ParenExpr); ok {
			expr = paren.Expr
		} else {
			break
		}
	}

	binExpr, ok := expr.(*parser.BinaryExpr)
	if !ok {
		return nil, fmt.Errorf("expression is not a binary comparison (may be a function call like absent() or vector())")
	}

	// Handle set operators (AND/OR/UNLESS) at top level by decomposing
	if binExpr.Op.IsSetOperator() {
		return extractFromCompoundExpr(binExpr)
	}

	if !binExpr.Op.IsComparisonOperator() {
		return nil, fmt.Errorf("top-level operator %q is not a comparison", binExpr.Op.String())
	}

	// Determine which side is the number literal
	switch {
	case isPromQLNumberLiteral(binExpr.RHS):
		// Standard: expr > N
		return &promQLThreshold{
			QueryExpr: binExpr.LHS.String(),
			Operator:  binExpr.Op.String(),
			Value:     getPromQLNumberValue(binExpr.RHS),
		}, nil
	case isPromQLNumberLiteral(binExpr.LHS):
		// Reversed: N < expr → flip to expr > N
		return &promQLThreshold{
			QueryExpr: binExpr.RHS.String(),
			Operator:  flipComparisonOperator(binExpr.Op.String()),
			Value:     getPromQLNumberValue(binExpr.LHS),
		}, nil
	default:
		return nil, fmt.Errorf("dynamic threshold — both sides of %q are expressions (e.g. expr > expr*N), not a static threshold comparison", binExpr.Op.String())
	}
}

// extractFromCompoundExpr handles AND/OR/UNLESS at the top level of a PromQL expression.
// For AND: extracts the leg with a tunable threshold (the other is a guard condition).
// For OR: extracts the first leg with a tunable threshold.
func extractFromCompoundExpr(binExpr *parser.BinaryExpr) (*promQLThreshold, error) {
	lhsResult, lhsErr := extractPromQLThreshold(binExpr.LHS.String())
	rhsResult, rhsErr := extractPromQLThreshold(binExpr.RHS.String())

	var exprType string
	switch binExpr.Op {
	case parser.LAND: // AND
		exprType = "compound_and"
	case parser.LOR: // OR
		exprType = "compound_or"
	case parser.LUNLESS: // UNLESS
		exprType = "compound_unless"
	default:
		return nil, fmt.Errorf("unsupported set operator: %s", binExpr.Op.String())
	}

	switch {
	case lhsErr == nil && rhsErr != nil:
		lhsResult.ExpressionType = exprType
		return lhsResult, nil
	case rhsErr == nil && lhsErr != nil:
		rhsResult.ExpressionType = exprType
		return rhsResult, nil
	case lhsErr == nil && rhsErr == nil:
		// Both sides have thresholds — for AND, one leg is typically a guard
		// condition (e.g., "requests > 0") and the other is the real alerting threshold.
		// Heuristic: a guard condition uses a trivial value (0) with GT/GTE.
		// Pick the non-guard leg; if neither is a guard, pick LHS.
		if binExpr.Op == parser.LAND {
			lhsIsGuard := isGuardCondition(lhsResult)
			rhsIsGuard := isGuardCondition(rhsResult)
			if lhsIsGuard && !rhsIsGuard {
				rhsResult.ExpressionType = exprType
				return rhsResult, nil
			}
			if rhsIsGuard && !lhsIsGuard {
				lhsResult.ExpressionType = exprType
				return lhsResult, nil
			}
			// Both or neither are guards: pick LHS (first leg)
		}
		lhsResult.ExpressionType = exprType
		return lhsResult, nil
	default:
		return nil, fmt.Errorf("compound %s: no extractable threshold in either leg: lhs: %v, rhs: %v",
			binExpr.Op.String(), lhsErr, rhsErr)
	}
}

// isGuardCondition returns true if a threshold leg looks like a guard condition
// (e.g., "> 0", ">= 0") rather than a real alerting threshold.
func isGuardCondition(t *promQLThreshold) bool {
	if t.Value == 0 && (t.Operator == ">" || t.Operator == ">=") {
		return true
	}
	return false
}

func isPromQLNumberLiteral(expr parser.Expr) bool {
	_, ok := evalPromQLConstant(expr)
	return ok
}

func getPromQLNumberValue(expr parser.Expr) float64 {
	val, _ := evalPromQLConstant(expr)
	return val
}

// evalPromQLConstant evaluates a PromQL expression that resolves to a constant number.
// Handles NumberLiteral, ParenExpr wrapping, and arithmetic between two constants (e.g. 25 / 100).
func evalPromQLConstant(expr parser.Expr) (float64, bool) {
	for {
		if p, ok := expr.(*parser.ParenExpr); ok {
			expr = p.Expr
		} else {
			break
		}
	}
	if n, ok := expr.(*parser.NumberLiteral); ok {
		return n.Val, true
	}
	if bin, ok := expr.(*parser.BinaryExpr); ok {
		lVal, lOK := evalPromQLConstant(bin.LHS)
		rVal, rOK := evalPromQLConstant(bin.RHS)
		if lOK && rOK {
			switch bin.Op.String() {
			case "+":
				return lVal + rVal, true
			case "-":
				return lVal - rVal, true
			case "*":
				return lVal * rVal, true
			case "/":
				if rVal != 0 {
					return lVal / rVal, true
				}
			}
		}
	}
	return 0, false
}

func flipComparisonOperator(op string) string {
	switch op {
	case ">":
		return "<"
	case ">=":
		return "<="
	case "<":
		return ">"
	case "<=":
		return ">="
	default:
		return op
	}
}

// extractPrometheusAlertDefinition extracts the alert definition from a Prometheus event
// by looking up the PromQL expression in the event_rules table and parsing its threshold.
func extractPrometheusAlertDefinition(ctx context.Context, db *sqlx.DB, ev models.Event) (*AlertDefinition, error) {
	labels := GetLabelsMap(ev)
	if labels == nil {
		return nil, fmt.Errorf("event has no labels")
	}

	alertName, _ := labels["alertname"].(string)
	if alertName == "" {
		return nil, fmt.Errorf("missing alertname label")
	}

	accountID := ""
	if ev.CloudAccountId != nil {
		accountID = *ev.CloudAccountId
	}
	if accountID == "" {
		return nil, fmt.Errorf("missing cloud_account_id")
	}

	// Look up PromQL expression from event_rules
	var expr string
	err := db.QueryRowContext(ctx,
		"SELECT expr FROM event_rules WHERE alert = $1 AND account_id = $2 AND enabled = true AND expr IS NOT NULL AND expr != '' LIMIT 1",
		alertName, accountID).Scan(&expr)
	if err != nil {
		return nil, fmt.Errorf("no event_rule found for alert %q: %w", alertName, err)
	}

	threshold, err := extractPromQLThreshold(expr)
	if err != nil {
		return nil, fmt.Errorf("cannot extract threshold from PromQL %q: %w", expr, err)
	}

	return &AlertDefinition{
		MetricName:       threshold.QueryExpr,
		Operator:         threshold.Operator,
		CurrentThreshold: threshold.Value,
		AlarmName:        alertName,
	}, nil
}

// fetchPrometheusMetricHistory fetches 7-day metric data from Prometheus via relay
func fetchPrometheusMetricHistory(ctx context.Context, ev models.Event, alertDef *AlertDefinition, startTime, endTime time.Time) *MetricHistory {
	accountID := ""
	if ev.CloudAccountId != nil {
		accountID = *ev.CloudAccountId
	}
	if accountID == "" {
		slog.WarnContext(ctx, "threshold_suggestion: missing cloud_account_id for prometheus event")
		return nil
	}

	queryExpr := alertDef.MetricName
	if queryExpr == "" {
		return nil
	}

	queriesMap := map[string]string{"threshold_query": queryExpr}

	respMap, err := relay.ExecutePrometheus(accountID, startTime, endTime, queriesMap, false)
	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to query Prometheus", "error", err, "query", queryExpr)
		return nil
	}

	return parsePrometheusRangeResponse(respMap, "threshold_query", startTime, endTime)
}

// parsePrometheusRangeResponse parses the relay ExecutePrometheus response into MetricHistory.
// Response format: map[queryKey] -> {"series_list_result": [{"timestamps": [...], "values": [...]}]}
// When multiple series are returned (e.g., one per pod/instance), aggregates by taking the
// max value at each timestamp. This captures worst-case metric behavior for threshold tuning.
func parsePrometheusRangeResponse(respMap map[string]any, queryKey string, startTime, endTime time.Time) *MetricHistory {
	queryResult, ok := respMap[queryKey]
	if !ok {
		return nil
	}

	qMap, ok := queryResult.(map[string]any)
	if !ok {
		return nil
	}

	seriesList, ok := qMap["series_list_result"].([]any)
	if !ok || len(seriesList) == 0 {
		return nil
	}

	// Aggregate across all series: take max value per timestamp
	tsValueMap := make(map[int64]float64)
	for _, seriesRaw := range seriesList {
		seriesMap, ok := seriesRaw.(map[string]any)
		if !ok {
			continue
		}

		tsRaw, tsOK := seriesMap["timestamps"].([]any)
		valRaw, valOK := seriesMap["values"].([]any)
		if !tsOK || !valOK || len(tsRaw) == 0 {
			continue
		}

		timestamps, values := parseSeriesData(tsRaw, valRaw)
		for i, ts := range timestamps {
			if existing, ok := tsValueMap[ts]; !ok || values[i] > existing {
				tsValueMap[ts] = values[i]
			}
		}
	}

	if len(tsValueMap) == 0 {
		return nil
	}

	// Sort timestamps and build output arrays
	sortedTs := make([]int64, 0, len(tsValueMap))
	for ts := range tsValueMap {
		sortedTs = append(sortedTs, ts)
	}
	sort.Slice(sortedTs, func(i, j int) bool { return sortedTs[i] < sortedTs[j] })

	values := make([]float64, len(sortedTs))
	for i, ts := range sortedTs {
		values[i] = tsValueMap[ts]
	}

	step := 60
	if len(sortedTs) > 1 {
		step = int(sortedTs[1] - sortedTs[0])
		if step < 1 {
			step = 60
		}
	}

	return &MetricHistory{
		Timestamps: sortedTs,
		Values:     values,
		StartTime:  startTime.Format(time.RFC3339),
		EndTime:    endTime.Format(time.RFC3339),
		Step:       step,
	}
}

// parseSeriesData extracts timestamps and values from raw Prometheus series data
func parseSeriesData(tsRaw, valRaw []any) ([]int64, []float64) {
	timestamps := make([]int64, 0, len(tsRaw))
	values := make([]float64, 0, len(valRaw))

	for i := 0; i < len(tsRaw) && i < len(valRaw); i++ {
		var ts int64
		switch t := tsRaw[i].(type) {
		case float64:
			ts = int64(t)
		case int64:
			ts = t
		default:
			continue
		}

		var val float64
		switch v := valRaw[i].(type) {
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || math.IsNaN(f) {
				continue
			}
			val = f
		case float64:
			if math.IsNaN(v) {
				continue
			}
			val = v
		default:
			continue
		}

		timestamps = append(timestamps, ts)
		values = append(values, val)
	}

	return timestamps, values
}

// extractAzureResourceIDs gets target resource IDs from either azure_alert_target_resources
// or azure_essentials.alertTargetIDs
func extractAzureResourceIDs(labels map[string]interface{}) []string {
	// Try azure_alert_target_resources (majority format)
	if targetStr, ok := labels["azure_alert_target_resources"].(string); ok && targetStr != "" {
		var ids []string
		if err := json.Unmarshal([]byte(targetStr), &ids); err == nil && len(ids) > 0 {
			return ids
		}
	}

	// Fallback: azure_essentials.alertTargetIDs (webhook format)
	if essStr, ok := labels["azure_essentials"].(string); ok && essStr != "" {
		var essentials map[string]interface{}
		if err := json.Unmarshal([]byte(essStr), &essentials); err == nil {
			if targetIDsRaw, ok := essentials["alertTargetIDs"].([]interface{}); ok {
				var ids []string
				for _, idRaw := range targetIDsRaw {
					if idStr, ok := idRaw.(string); ok {
						ids = append(ids, idStr)
					}
				}
				return ids
			}
		}
	}

	return nil
}

// getFiringAnalysisForEvent computes statistics about how often the alert fires.
// For Prometheus events, groups by fingerprint (stable hash of labels).
// For AWS/Azure events, groups by alarm identity (ARN or alert name) since
// their fingerprints include timestamps and are unique per event.
func getFiringAnalysisForEvent(ctx context.Context, db *sqlx.DB, ev models.Event, source, accountID, tenantID string) *FiringAnalysis {
	if accountID == "" {
		return nil
	}

	var query string
	var args []interface{}
	labels := GetLabelsMap(ev)

	switch source {
	case "AWS_CloudWatch_Alarm":
		alarmARN := ""
		if labels != nil {
			alarmARN, _ = labels["aws_event_arn"].(string)
		}
		if alarmARN == "" {
			return nil
		}
		query = `SELECT COUNT(*), COALESCE(MIN(starts_at), NOW()), COALESCE(MAX(starts_at), NOW())
			FROM events
			WHERE labels->>'aws_event_arn' = $1 AND cloud_account_id = $2 AND tenant = $3
			  AND starts_at > NOW() - INTERVAL '30 days'`
		args = []interface{}{alarmARN, accountID, tenantID}

	case "azure_monitor_webhook", "Azure_Monitor_Alert":
		alertName := ""
		if labels != nil {
			if name, _ := labels["azure_alert_name"].(string); name != "" {
				alertName = name
			} else if name, _ := labels["alertname"].(string); name != "" {
				alertName = name
			}
		}
		if alertName == "" {
			return nil
		}
		query = `SELECT COUNT(*), COALESCE(MIN(starts_at), NOW()), COALESCE(MAX(starts_at), NOW())
			FROM events
			WHERE COALESCE(labels->>'azure_alert_name', labels->>'alertname') = $1
			  AND cloud_account_id = $2 AND tenant = $3
			  AND starts_at > NOW() - INTERVAL '30 days'`
		args = []interface{}{alertName, accountID, tenantID}

	case "prometheus":
		alertName := ""
		if labels != nil {
			alertName, _ = labels["alertname"].(string)
		}
		if alertName == "" {
			return nil
		}
		query = `SELECT COUNT(*), COALESCE(MIN(starts_at), NOW()), COALESCE(MAX(starts_at), NOW())
			FROM events
			WHERE labels->>'alertname' = $1 AND cloud_account_id = $2 AND tenant = $3
			  AND source = 'prometheus'
			  AND starts_at > NOW() - INTERVAL '30 days'`
		args = []interface{}{alertName, accountID, tenantID}

	case "GCP_Metric_Alert":
		policyID := ""
		if labels != nil {
			policyID, _ = labels["gcp_policy_id"].(string)
		}
		if policyID == "" {
			return nil
		}
		query = `SELECT COUNT(*), COALESCE(MIN(starts_at), NOW()), COALESCE(MAX(starts_at), NOW())
			FROM events
			WHERE labels->>'gcp_policy_id' = $1 AND cloud_account_id = $2 AND tenant = $3
			  AND starts_at > NOW() - INTERVAL '30 days'`
		args = []interface{}{policyID, accountID, tenantID}

	case "pagerduty_webhook":
		alertName := ""
		if labels != nil {
			alertName, _ = labels["nb_alert_name"].(string)
			if alertName == "" {
				alertName, _ = labels["alertname"].(string)
			}
		}
		if alertName == "" {
			return nil
		}
		query = `SELECT COUNT(*), COALESCE(MIN(starts_at), NOW()), COALESCE(MAX(starts_at), NOW())
			FROM events
			WHERE labels->>'nb_alert_name' = $1 AND cloud_account_id = $2 AND tenant = $3
			  AND source = 'pagerduty_webhook'
			  AND starts_at > NOW() - INTERVAL '30 days'`
		args = []interface{}{alertName, accountID, tenantID}

	default:
		fingerprint := ""
		if ev.Fingerprint != nil {
			fingerprint = *ev.Fingerprint
		}
		if fingerprint == "" {
			return nil
		}
		query = `SELECT COUNT(*), COALESCE(MIN(starts_at), NOW()), COALESCE(MAX(starts_at), NOW())
			FROM events
			WHERE fingerprint = $1 AND cloud_account_id = $2 AND tenant = $3
			  AND starts_at > NOW() - INTERVAL '30 days'`
		args = []interface{}{fingerprint, accountID, tenantID}
	}

	var totalCount int
	var minStartsAt, maxStartsAt time.Time

	err := db.QueryRowContext(ctx, query, args...).Scan(&totalCount, &minStartsAt, &maxStartsAt)
	if err != nil {
		slog.WarnContext(ctx, "threshold_suggestion: failed to get firing analysis", "error", err)
		return nil
	}

	timeRangeDays := int(maxStartsAt.Sub(minStartsAt).Hours()/24) + 1
	if timeRangeDays < 1 {
		timeRangeDays = 1
	}

	avgPerDay := float64(totalCount) / float64(timeRangeDays)

	// Collect metric values at firing time — use fingerprint for Prometheus,
	// but for AWS/Azure this won't find much (unique fingerprints).
	fingerprint := ""
	if ev.Fingerprint != nil {
		fingerprint = *ev.Fingerprint
	}
	metricValues := collectStoredMetricValues(ctx, db, fingerprint, accountID, tenantID)

	return &FiringAnalysis{
		TotalOccurrences:     totalCount,
		TimeRangeDays:        timeRangeDays,
		AvgFiringsPerDay:     math.Round(avgPerDay*100) / 100,
		MetricValuesAtFiring: metricValues,
	}
}

// collectStoredMetricValues extracts metric values from stored events (from labels/evidences)
func collectStoredMetricValues(ctx context.Context, db *sqlx.DB, fingerprint, accountID, tenantID string) []float64 {
	rows, err := db.QueryContext(ctx,
		`SELECT labels FROM events
		 WHERE fingerprint = $1 AND cloud_account_id = $2 AND tenant = $3
		   AND starts_at > NOW() - INTERVAL '30 days'
		 ORDER BY starts_at DESC LIMIT 50`,
		fingerprint, accountID, tenantID)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var values []float64
	for rows.Next() {
		var labelsJSON string
		if err := rows.Scan(&labelsJSON); err != nil {
			continue
		}

		var labelsMap map[string]interface{}
		if err := json.Unmarshal([]byte(labelsJSON), &labelsMap); err != nil {
			continue
		}

		// Try Azure metricValue from azure_alert_context
		if alertCtxStr, ok := labelsMap["azure_alert_context"].(string); ok {
			var alertCtx map[string]interface{}
			if err := json.Unmarshal([]byte(alertCtxStr), &alertCtx); err == nil {
				if cond, ok := alertCtx["condition"].(map[string]interface{}); ok {
					if allOf, ok := cond["allOf"].([]interface{}); ok && len(allOf) > 0 {
						if mc, ok := allOf[0].(map[string]interface{}); ok {
							if mv, ok := mc["metricValue"].(float64); ok {
								values = append(values, mv)
							}
						}
					}
				}
			}
		}

		// Try nb_metric_value label (future enhancement)
		if mvStr, ok := labelsMap["nb_metric_value"].(string); ok {
			if mv, err := strconv.ParseFloat(mvStr, 64); err == nil {
				values = append(values, mv)
			}
		}
	}

	return values
}

// computeSuggestion generates a threshold recommendation based on metric data
func computeSuggestion(alertDef *AlertDefinition, metricHistory *MetricHistory, firingAnalysis *FiringAnalysis, alertQuality *AlertQualityScore) *ThresholdSuggestion {
	if metricHistory == nil || len(metricHistory.Values) == 0 {
		if firingAnalysis != nil && len(firingAnalysis.MetricValuesAtFiring) > 0 {
			return computeSuggestionFromFiringValues(alertDef, firingAnalysis)
		}
		return nil
	}

	sorted := make([]float64, len(metricHistory.Values))
	copy(sorted, metricHistory.Values)
	sort.Float64s(sorted)

	p50 := percentile(sorted, 50)
	p90 := percentile(sorted, 90)
	p95 := percentile(sorted, 95)
	p99 := percentile(sorted, 99)
	med := computeMedian(sorted)
	madVal := computeMAD(sorted)

	suggestion := &ThresholdSuggestion{
		MetricP50:    roundTo(p50, 2),
		MetricP90:    roundTo(p90, 2),
		MetricP95:    roundTo(p95, 2),
		MetricP99:    roundTo(p99, 2),
		MetricMedian: roundTo(med, 2),
		MetricMAD:    roundTo(madVal, 2),
	}

	operator := alertDef.Operator
	currentThreshold := alertDef.CurrentThreshold

	if isGreaterThanOperator(operator) {
		suggestedThreshold, method := computeUpperThreshold(sorted, med, madVal, p95)

		if suggestedThreshold <= currentThreshold {
			// Threshold above our suggestion — check for sparse metric pattern
			if firingAnalysis != nil && firingAnalysis.AvgFiringsPerDay >= 1 {
				aboveThreshold := filterAboveThreshold(sorted, currentThreshold)
				if len(aboveThreshold) > 0 {
					sort.Float64s(aboveThreshold)
					spikeMed := computeMedian(aboveThreshold)
					spikeMAD := computeMAD(aboveThreshold)
					var spikeSuggestion float64
					if spikeMAD > 0 {
						spikeSuggestion = spikeMed + 3.0*spikeMAD
					} else {
						spikeSuggestion = spikeMed * 1.20
					}
					spikeSuggestion = roundTo(spikeSuggestion, 2)
					if spikeSuggestion <= currentThreshold {
						// Previous fallback used global P99*1.10 which gets pulled up by extreme outliers.
						// Instead use the P95 of just the spike values — this captures most spike behavior
						// without being dominated by rare extreme values.
						spikeP95 := percentile(aboveThreshold, 95)
						spikeSuggestion = roundTo(spikeP95*1.10, 2)
					}
					if spikeSuggestion > currentThreshold {
						// Apply sanity cap
						spikeSuggestion = applySanityCap(spikeSuggestion, currentThreshold, p99)
						suggestion.SuggestedThreshold = spikeSuggestion
						suggestion.Method = "spike"
						suggestion.RecommendationType = "tune_threshold"
						reduction := estimateReduction(sorted, currentThreshold, spikeSuggestion, true)
						suggestion.EstimatedReduction = reduction
						suggestion.Reason = fmt.Sprintf(
							"Alert fires %.1f times/day. Raise threshold from %.2f to %.2f (spike analysis: %d values exceed threshold, spike median=%.2f, MAD=%.2f). Estimated %.0f%% reduction.",
							firingAnalysis.AvgFiringsPerDay, currentThreshold, spikeSuggestion, len(aboveThreshold), spikeMed, spikeMAD, reduction)
						suggestion.Confidence = computeConfidence(aboveThreshold, currentThreshold, spikeSuggestion, alertQuality)
						return suggestion
					}
				}
			}
			suggestion.SuggestedThreshold = currentThreshold
			suggestion.Method = method
			suggestion.Reason = fmt.Sprintf("Current threshold (%.2f) is already above the suggested value (%.2f). The alert appears correctly tuned.", currentThreshold, suggestedThreshold)
			suggestion.Confidence = "low"
			suggestion.EstimatedReduction = 0
			return suggestion
		}

		// Apply sanity cap: never suggest more than max(current*10, P99)
		suggestedThreshold = applySanityCap(suggestedThreshold, currentThreshold, p99)
		suggestion.SuggestedThreshold = roundTo(suggestedThreshold, 2)
		suggestion.Method = method
		reduction := estimateReduction(sorted, currentThreshold, suggestedThreshold, true)
		suggestion.EstimatedReduction = reduction

		suggestion.Reason = fmt.Sprintf(
			"Raise threshold from %.2f to %.2f (%s: median=%.2f, MAD=%.2f). Metric values: p50=%.2f, p90=%.2f, p95=%.2f. Estimated %.0f%% reduction.",
			currentThreshold, suggestion.SuggestedThreshold, method, med, madVal, p50, p90, p95, reduction)

	} else if isLessThanOperator(operator) {
		suggestedThreshold, method := computeLowerThreshold(sorted, med, madVal)

		if suggestedThreshold >= currentThreshold {
			suggestion.SuggestedThreshold = currentThreshold
			suggestion.Method = method
			suggestion.Reason = fmt.Sprintf("Current threshold (%.2f) is already below the suggested value (%.2f). The alert appears correctly tuned.", currentThreshold, suggestedThreshold)
			suggestion.Confidence = "low"
			suggestion.EstimatedReduction = 0
			return suggestion
		}

		// Sanity floor: never suggest less than min(current*0.1, P1)
		p1 := percentile(sorted, 1)
		sanityFloor := math.Min(currentThreshold*0.1, p1)
		if suggestedThreshold < sanityFloor {
			suggestedThreshold = sanityFloor
		}

		suggestion.SuggestedThreshold = roundTo(suggestedThreshold, 2)
		suggestion.Method = method
		reduction := estimateReduction(sorted, currentThreshold, suggestedThreshold, false)
		suggestion.EstimatedReduction = reduction

		suggestion.Reason = fmt.Sprintf(
			"Lower threshold from %.2f to %.2f (%s: median=%.2f, MAD=%.2f). Estimated %.0f%% reduction.",
			currentThreshold, suggestion.SuggestedThreshold, method, med, madVal, reduction)
	} else {
		// Unknown operator (e.g., AWS CloudWatch empty operator).
		// Try GT direction: if metric data suggests threshold should be higher, raise it.
		// If suggested <= current, that would increase noise — don't suggest it.
		suggestedThreshold, method := computeUpperThreshold(sorted, med, madVal, p95)

		if suggestedThreshold > currentThreshold {
			suggestedThreshold = applySanityCap(suggestedThreshold, currentThreshold, p99)
			suggestion.SuggestedThreshold = roundTo(suggestedThreshold, 2)
			suggestion.Method = method
			reduction := estimateReduction(sorted, currentThreshold, suggestedThreshold, true)
			suggestion.EstimatedReduction = reduction
			suggestion.Reason = fmt.Sprintf(
				"Raise threshold from %.2f to %.2f (%s: median=%.2f, MAD=%.2f). Estimated %.0f%% reduction.",
				currentThreshold, suggestion.SuggestedThreshold, method, med, madVal, reduction)
		} else {
			suggestion.SuggestedThreshold = currentThreshold
			suggestion.Method = method
			suggestion.Reason = fmt.Sprintf(
				"Current threshold (%.2f) appears correctly tuned. Metric stats: median=%.2f, MAD=%.2f, p95=%.2f.",
				currentThreshold, med, madVal, p95)
			suggestion.Confidence = "low"
			suggestion.EstimatedReduction = 0
		}
	}

	// Guard: warn on large ratio changes — these may be correct (e.g., CPU threshold=1→57)
	// but warrant manual review. The existing sanity cap (max(current*10, P99)) already
	// prevents truly absurd values; this adds a softer "review manually" signal.
	if currentThreshold > 0 && suggestion.SuggestedThreshold > currentThreshold*5 {
		suggestion.Reason += " [Large change (>5x) — review manually.]"
	}

	suggestion.Confidence = computeConfidence(sorted, currentThreshold, suggestion.SuggestedThreshold, alertQuality)
	computeDurationRecommendation(suggestion, alertDef, alertQuality)
	return suggestion
}

// computeUpperThreshold computes a suggested upper threshold using MAD > IQR > P95 fallback chain
func computeUpperThreshold(sorted []float64, med, madVal, p95 float64) (float64, string) {
	const k = 3.0
	if madVal > 0 {
		return roundTo(med+k*madVal, 2), "MAD"
	}
	iqrVal := computeIQR(sorted)
	if iqrVal > 0 {
		p75 := percentile(sorted, 75)
		return roundTo(p75+1.5*iqrVal, 2), "IQR"
	}
	return roundTo(p95*1.10, 2), "P95"
}

// computeLowerThreshold computes a suggested lower threshold using MAD > IQR > P5 fallback chain
func computeLowerThreshold(sorted []float64, med, madVal float64) (float64, string) {
	const k = 3.0
	if madVal > 0 {
		return roundTo(med-k*madVal, 2), "MAD"
	}
	iqrVal := computeIQR(sorted)
	if iqrVal > 0 {
		p25 := percentile(sorted, 25)
		return roundTo(p25-1.5*iqrVal, 2), "IQR"
	}
	p5 := percentile(sorted, 5)
	return roundTo(p5*0.90, 2), "P5"
}

// applySanityCap caps a suggested threshold to avoid absurd values (e.g., 393x increase).
// Returns the capped value. Cap = max(current*10, P99).
func applySanityCap(suggested, currentThreshold, p99 float64) float64 {
	if currentThreshold == 0 {
		return suggested
	}
	cap := math.Max(currentThreshold*10, p99)
	if suggested > cap {
		return roundTo(cap, 2)
	}
	return suggested
}

// filterAboveThreshold returns values that exceed the given threshold
func filterAboveThreshold(sorted []float64, threshold float64) []float64 {
	var above []float64
	for _, v := range sorted {
		if v > threshold {
			above = append(above, v)
		}
	}
	return above
}

// computeConfidence scores suggestion confidence using multiple factors:
// data coverage, data stability (CV), suggestion reasonableness, and alert quality.
func computeConfidence(values []float64, currentThreshold, suggestedThreshold float64, alertQuality *AlertQualityScore) string {
	score := 0.0

	// Data coverage (0-30 points)
	switch {
	case len(values) > 500:
		score += 30
	case len(values) > 100:
		score += 20
	default:
		score += 10
	}

	// Data stability via coefficient of variation (0-25 points)
	if len(values) > 1 {
		mean := 0.0
		for _, v := range values {
			mean += v
		}
		mean /= float64(len(values))
		if mean > 0 {
			variance := 0.0
			for _, v := range values {
				d := v - mean
				variance += d * d
			}
			cv := math.Sqrt(variance/float64(len(values))) / mean
			switch {
			case cv < 0.5:
				score += 25
			case cv < 1.0:
				score += 15
			default:
				score += 5
			}
		}
	}

	// Suggestion reasonableness — how far from current (0-25 points)
	if currentThreshold != 0 {
		ratio := suggestedThreshold / currentThreshold
		switch {
		case ratio > 0.5 && ratio < 2.0:
			score += 25
		case ratio > 0.1 && ratio < 10.0:
			score += 15
		}
	}

	// Alert quality alignment (0-20 points)
	if alertQuality != nil {
		switch alertQuality.Classification {
		case "noisy_but_real":
			score += 20
		case "healthy":
			score += 10
		}
	}

	switch {
	case score >= 70:
		return "high"
	case score >= 40:
		return "medium"
	default:
		return "low"
	}
}

// computeSuggestionFromFiringValues generates suggestion from only the firing values (no full history)
func computeSuggestionFromFiringValues(alertDef *AlertDefinition, firingAnalysis *FiringAnalysis) *ThresholdSuggestion {
	sorted := make([]float64, len(firingAnalysis.MetricValuesAtFiring))
	copy(sorted, firingAnalysis.MetricValuesAtFiring)
	sort.Float64s(sorted)

	p50 := percentile(sorted, 50)
	p90 := percentile(sorted, 90)
	p95 := percentile(sorted, 95)
	p99 := percentile(sorted, 99)
	med := computeMedian(sorted)
	madVal := computeMAD(sorted)

	suggestion := &ThresholdSuggestion{
		MetricP50:    roundTo(p50, 2),
		MetricP90:    roundTo(p90, 2),
		MetricP95:    roundTo(p95, 2),
		MetricP99:    roundTo(p99, 2),
		MetricMedian: roundTo(med, 2),
		MetricMAD:    roundTo(madVal, 2),
		Confidence:   "low", // Low confidence since we only have firing values, not full metric history
	}

	if isGreaterThanOperator(alertDef.Operator) {
		suggested, method := computeUpperThreshold(sorted, med, madVal, p95)
		suggested = applySanityCap(suggested, alertDef.CurrentThreshold, p99)
		suggestion.SuggestedThreshold = roundTo(suggested, 2)
		suggestion.Method = method
	} else {
		suggested, method := computeLowerThreshold(sorted, med, madVal)
		suggestion.SuggestedThreshold = roundTo(suggested, 2)
		suggestion.Method = method
	}

	suggestion.Reason = fmt.Sprintf(
		"Based on %d stored firing values (no full metric history). Method: %s, median=%.2f, MAD=%.2f. Consider fetching full metric history for higher confidence.",
		len(sorted), suggestion.Method, med, madVal)
	suggestion.RecommendationType = "tune_threshold"

	return suggestion
}

// computeDurationRecommendation sets the recommendation type and optional duration suggestion
// based on alert quality transient patterns.
func computeDurationRecommendation(suggestion *ThresholdSuggestion, alertDef *AlertDefinition, alertQuality *AlertQualityScore) {
	if alertQuality == nil {
		if suggestion.EstimatedReduction > 0 {
			suggestion.RecommendationType = "tune_threshold"
		} else {
			suggestion.RecommendationType = "none"
		}
		return
	}

	isTransient := alertQuality.TransientRate > 0.7
	hasThresholdSuggestion := suggestion.EstimatedReduction > 10

	// Compute suggested duration based on P90 of event duration
	suggestedDurationMin := 0
	if isTransient && alertQuality.DurationP90 > 0 {
		durationMinutes := alertQuality.DurationP90 / 60.0
		suggestedDurationMin = int(math.Ceil(durationMinutes*1.5/5.0)) * 5
		if suggestedDurationMin < 10 {
			suggestedDurationMin = 10
		}
		if suggestedDurationMin > 30 {
			suggestedDurationMin = 30
		}

		// If current evaluation window already covers transient spikes, skip duration suggestion
		currentWindowMin := (alertDef.Period * alertDef.EvaluationPeriods) / 60
		if currentWindowMin >= suggestedDurationMin {
			isTransient = false
		}
	}

	switch {
	case alertQuality.Classification == "broken":
		suggestion.RecommendationType = "disable"
		suggestion.DurationReason = "Alert never resolves — investigate root cause or disable."

	case isTransient && hasThresholdSuggestion:
		suggestion.RecommendationType = "tune_both"
		suggestion.SuggestedDuration = suggestedDurationMin
		suggestion.DurationReason = fmt.Sprintf(
			"%.0f%% of firings resolve within 10 minutes (P90 duration: %.0fs). Increase evaluation window to %d minutes to filter transient spikes.",
			alertQuality.TransientRate*100, alertQuality.DurationP90, suggestedDurationMin)

	case isTransient && !hasThresholdSuggestion:
		suggestion.RecommendationType = "increase_duration"
		suggestion.SuggestedDuration = suggestedDurationMin
		suggestion.DurationReason = fmt.Sprintf(
			"Threshold appears correct, but %.0f%% of firings resolve within 10 minutes. Increase evaluation window to %d minutes.",
			alertQuality.TransientRate*100, suggestedDurationMin)

	case hasThresholdSuggestion:
		suggestion.RecommendationType = "tune_threshold"

	default:
		suggestion.RecommendationType = "none"
	}
}

// alertRuleWhereClause returns the WHERE condition and first arg for querying events
// by alert rule identity for a given source. Returns empty string if key is missing.
func alertRuleWhereClause(source, alertRuleKey string) (whereCondition string, sourceFilter string) {
	switch source {
	case "AWS_CloudWatch_Alarm":
		return "labels->>'aws_event_arn' = $1", ""
	case "azure_monitor_webhook", "Azure_Monitor_Alert":
		return "COALESCE(labels->>'azure_alert_name', labels->>'alertname') = $1", ""
	case "prometheus":
		return "labels->>'alertname' = $1", "AND source = 'prometheus'"
	case "GCP_Metric_Alert":
		return "labels->>'gcp_policy_id' = $1", ""
	case "pagerduty_webhook":
		return "labels->>'nb_alert_name' = $1", "AND source = 'pagerduty_webhook'"
	default:
		return "", ""
	}
}

// computeAlertQuality classifies alert health based on firing patterns from the events table.
func computeAlertQuality(ctx context.Context, db *sqlx.DB, source, alertRuleKey, cloudAccountID, tenantID string) *AlertQualityScore {
	if alertRuleKey == "" || cloudAccountID == "" {
		return nil
	}

	whereCondition, sourceFilter := alertRuleWhereClause(source, alertRuleKey)
	if whereCondition == "" {
		return nil
	}

	var result struct {
		Total          int        `db:"total"`
		FlappingCount  int        `db:"flapping_count"`
		AvgDuration    *float64   `db:"avg_duration"`
		ResolvedCount  int        `db:"resolved_count"`
		EngagedCount   int        `db:"engaged_count"`
		TransientCount int        `db:"transient_count"`
		InstantCount   int        `db:"instant_count"`
		DurationP90    *float64   `db:"duration_p90"`
		FirstFire      *time.Time `db:"first_fire"`
		LastFire       *time.Time `db:"last_fire"`
	}

	query := fmt.Sprintf(`
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE ends_at IS NOT NULL AND ends_at > starts_at
				AND EXTRACT(EPOCH FROM (ends_at - starts_at)) < 300) as flapping_count,
			AVG(EXTRACT(EPOCH FROM (ends_at - starts_at)))
				FILTER (WHERE ends_at IS NOT NULL AND ends_at > starts_at) as avg_duration,
			COUNT(*) FILTER (WHERE ends_at IS NOT NULL AND ends_at > starts_at) as resolved_count,
			COUNT(*) FILTER (WHERE nb_status_changed_by IS NOT NULL) as engaged_count,
			COUNT(*) FILTER (WHERE ends_at IS NOT NULL AND ends_at > starts_at
				AND EXTRACT(EPOCH FROM (ends_at - starts_at)) < 600) as transient_count,
			COUNT(*) FILTER (WHERE ends_at IS NOT NULL AND ends_at = starts_at) as instant_count,
			PERCENTILE_CONT(0.9) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (ends_at - starts_at))
			) FILTER (WHERE ends_at IS NOT NULL AND ends_at > starts_at) as duration_p90,
			MIN(starts_at) as first_fire,
			MAX(starts_at) as last_fire
		FROM events
		WHERE %s AND cloud_account_id = $2 AND tenant = $3
		  %s
		  AND starts_at > NOW() - INTERVAL '30 days'
	`, whereCondition, sourceFilter)

	err := db.GetContext(ctx, &result, query, alertRuleKey, cloudAccountID, tenantID)
	if err != nil || result.Total == 0 {
		return nil
	}

	total := float64(result.Total)
	score := &AlertQualityScore{
		FlappingRate:   roundTo(float64(result.FlappingCount)/total, 4),
		ResolutionRate: roundTo(float64(result.ResolvedCount)/total, 4),
		EngagementRate: roundTo(float64(result.EngagedCount)/total, 4),
		InstantRate:    roundTo(float64(result.InstantCount)/total, 4),
	}

	// Transient rate = events resolving < 10 min / total resolved (excludes instant events)
	if result.ResolvedCount > 0 {
		score.TransientRate = roundTo(float64(result.TransientCount)/float64(result.ResolvedCount), 4)
	}

	if result.AvgDuration != nil {
		score.MeanTimeToClose = roundTo(*result.AvgDuration, 0)
	}
	if result.DurationP90 != nil {
		score.DurationP90 = roundTo(*result.DurationP90, 0)
	}

	// Compute firing frequency (events per day)
	if result.FirstFire != nil && result.LastFire != nil {
		days := result.LastFire.Sub(*result.FirstFire).Hours() / 24
		if days < 1 {
			days = 1
		}
		score.FiringFrequency = roundTo(total/days, 2)
	}

	classifyAlertQuality(score)
	return score
}

// classifyAlertQuality assigns classification and recommendation based on quality metrics.
// Priority order matters: false_positive > broken > noisy_but_real > noisy_ignored > healthy.
func classifyAlertQuality(score *AlertQualityScore) {
	switch {
	// 1. False positive: fires often, auto-resolves, nobody acts on it
	case score.FiringFrequency > 1 && score.ResolutionRate > 0.8 && score.EngagementRate < 0.1:
		score.Classification = "false_positive"
		if score.TransientRate > 0.7 {
			score.Recommendation = "increase_duration"
		} else {
			score.Recommendation = "disable_alert"
		}

	// 2. Broken: never resolves (but only if instant events aren't dominating)
	case score.ResolutionRate < 0.1 && score.InstantRate < 0.5:
		score.Classification = "broken"
		score.Recommendation = "investigate"

	// 3. Noisy but real: fires often, resolves, and humans act on it
	case score.FiringFrequency > 1 && score.ResolutionRate > 0.5 && score.EngagementRate > 0.1:
		score.Classification = "noisy_but_real"
		if score.TransientRate > 0.7 {
			score.Recommendation = "increase_duration"
		} else {
			score.Recommendation = "tune_threshold"
		}

	// 4. Noisy ignored: fires moderately often, resolves, but nobody acts
	case score.FiringFrequency > 0.5 && score.ResolutionRate > 0.5 && score.EngagementRate < 0.1:
		score.Classification = "false_positive"
		if score.TransientRate > 0.7 {
			score.Recommendation = "increase_duration"
		} else {
			score.Recommendation = "tune_threshold"
		}

	// 5. Healthy: low frequency or well-engaged
	default:
		score.Classification = "healthy"
		score.Recommendation = "no_action"
	}
}

// --- Helpers ---

// calcStep computes a step/period that keeps datapoints under the CloudWatch 1440 limit.
// Returns at least 5 minutes, rounded up to the nearest minute.
func calcStep(start, end time.Time) time.Duration {
	totalSeconds := end.Sub(start).Seconds()
	minPeriod := totalSeconds / 1440
	stepSeconds := math.Ceil(minPeriod/60) * 60 // round up to nearest minute
	if stepSeconds < 300 {
		stepSeconds = 300
	}
	return time.Duration(stepSeconds) * time.Second
}

// GetLabelsMap extracts the labels map from a models.Event
func GetLabelsMap(ev models.Event) map[string]interface{} {
	if ev.Labels == nil {
		return nil
	}
	labelsMap, ok := ev.Labels.Object().(map[string]interface{})
	if !ok {
		return nil
	}
	return labelsMap
}

// metricsResponseToHistory converts QueryMetricsResponse to MetricHistory
func metricsResponseToHistory(resp cloud.QueryMetricsResponse, startTime, endTime time.Time, stepSeconds int) *MetricHistory {
	if len(resp.Items) == 0 {
		return nil
	}

	// Use the first metric item
	item := resp.Items[0]
	timestamps := make([]int64, len(item.Timestamps))
	for i, ts := range item.Timestamps {
		timestamps[i] = ts.Unix()
	}

	return &MetricHistory{
		Timestamps: timestamps,
		Values:     item.Values,
		StartTime:  startTime.Format(time.RFC3339),
		EndTime:    endTime.Format(time.RFC3339),
		Step:       stepSeconds,
	}
}

// computeMedian returns the median of a sorted slice
func computeMedian(sorted []float64) float64 {
	return percentile(sorted, 50)
}

// computeMAD returns the Median Absolute Deviation: median(|xi - median(X)|)
// MAD is robust to outliers — a single spike won't distort it like standard deviation would.
func computeMAD(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	med := computeMedian(sorted)
	deviations := make([]float64, len(sorted))
	for i, v := range sorted {
		deviations[i] = math.Abs(v - med)
	}
	sort.Float64s(deviations)
	return computeMedian(deviations)
}

// computeIQR returns the Interquartile Range: P75 - P25
func computeIQR(sorted []float64) float64 {
	return percentile(sorted, 75) - percentile(sorted, 25)
}

// percentile computes the p-th percentile of a sorted slice
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p / 100 * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := rank - float64(lower)
	return sorted[lower] + frac*(sorted[upper]-sorted[lower])
}

// estimateReduction estimates what % of firings would be eliminated by the new threshold
func estimateReduction(sorted []float64, currentThreshold, suggestedThreshold float64, isGreaterThan bool) float64 {
	if len(sorted) == 0 {
		return 0
	}

	currentFireCount := 0
	newFireCount := 0

	for _, v := range sorted {
		if isGreaterThan {
			if v > currentThreshold {
				currentFireCount++
			}
			if v > suggestedThreshold {
				newFireCount++
			}
		} else {
			if v < currentThreshold {
				currentFireCount++
			}
			if v < suggestedThreshold {
				newFireCount++
			}
		}
	}

	if currentFireCount == 0 {
		return 0
	}

	return math.Round(float64(currentFireCount-newFireCount) / float64(currentFireCount) * 100)
}

func isGreaterThanOperator(op string) bool {
	op = strings.ToLower(op)
	return strings.Contains(op, "greater") || op == ">" || op == ">="
}

func isLessThanOperator(op string) bool {
	op = strings.ToLower(op)
	return strings.Contains(op, "less") || op == "<" || op == "<="
}

func roundTo(val float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(val*pow) / pow
}

// parseISODurationToSeconds parses ISO 8601 duration like "PT5M" to seconds
func parseISODurationToSeconds(duration string) int {
	duration = strings.ToUpper(strings.TrimSpace(duration))
	if !strings.HasPrefix(duration, "PT") {
		return 0
	}
	duration = strings.TrimPrefix(duration, "PT")

	totalSeconds := 0
	current := ""
	for _, c := range duration {
		if c >= '0' && c <= '9' {
			current += string(c)
		} else {
			val, err := strconv.Atoi(current)
			if err != nil {
				current = ""
				continue
			}
			switch c {
			case 'H':
				totalSeconds += val * 3600
			case 'M':
				totalSeconds += val * 60
			case 'S':
				totalSeconds += val
			}
			current = ""
		}
	}
	return totalSeconds
}

// ExtractAlertRuleKey returns the stable alert identity key for a given source and labels.
// This key is used to group events by alert rule (not by fingerprint).
func ExtractAlertRuleKey(source string, labels map[string]interface{}) string {
	switch source {
	case "prometheus":
		key, _ := labels["alertname"].(string)
		return key
	case "pagerduty_webhook":
		key, _ := labels["nb_alert_name"].(string)
		if key == "" {
			key, _ = labels["alertname"].(string)
		}
		return key
	case "AWS_CloudWatch_Alarm":
		key, _ := labels["aws_event_arn"].(string)
		return key
	case "azure_monitor_webhook", "Azure_Monitor_Alert":
		if key, _ := labels["azure_alert_name"].(string); key != "" {
			return key
		}
		key, _ := labels["alertname"].(string)
		return key
	case "GCP_Metric_Alert":
		key, _ := labels["gcp_policy_id"].(string)
		return key
	default:
		return ""
	}
}
