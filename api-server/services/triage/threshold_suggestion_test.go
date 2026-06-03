package triage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetThresholdSuggestion_AWSCloudWatch(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// AWS CloudWatch alarm event (custom namespace: rds-deadlock-log-alert, threshold=0)
	eventID := testenv.RequireEnv(t, "TEST_EVENT_AWS_CLOUDWATCH")["TEST_EVENT_AWS_CLOUDWATCH"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	require.NotNil(t, ev.Source)
	assert.Equal(t, "AWS_CloudWatch_Alarm", *ev.Source)

	tenantID := ""
	if ev.Tenant != nil {
		tenantID = *ev.Tenant
	}
	require.NotEmpty(t, tenantID, "Event missing tenant")

	response := GetThresholdSuggestion(ctx, db, ev, tenantID)

	resultJSON, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("=== AWS CloudWatch Threshold Suggestion ===\n%s", string(resultJSON))

	assert.True(t, response.Available, "Should be available for AWS CloudWatch")
	assert.Equal(t, "AWS_CloudWatch_Alarm", response.Source)
	assert.Empty(t, response.Error)

	// Alert definition should be extracted
	require.NotNil(t, response.AlertDefinition, "AlertDefinition should not be nil")
	t.Logf("Metric: %s/%s", response.AlertDefinition.MetricNamespace, response.AlertDefinition.MetricName)
	t.Logf("Operator: %s, Threshold: %.2f", response.AlertDefinition.Operator, response.AlertDefinition.CurrentThreshold)
	assert.NotEmpty(t, response.AlertDefinition.MetricName)

	// Firing analysis should be populated
	if response.FiringAnalysis != nil {
		t.Logf("Firings: %d over %d days (%.1f/day)",
			response.FiringAnalysis.TotalOccurrences,
			response.FiringAnalysis.TimeRangeDays,
			response.FiringAnalysis.AvgFiringsPerDay)
		assert.Greater(t, response.FiringAnalysis.TotalOccurrences, 0)
	}

	// Metric history (may be nil if cloud-collector is not reachable)
	if response.MetricHistory != nil {
		t.Logf("Metric history: %d data points, step=%ds",
			len(response.MetricHistory.Values), response.MetricHistory.Step)
		assert.NotEmpty(t, response.MetricHistory.Values)
		assert.Equal(t, len(response.MetricHistory.Timestamps), len(response.MetricHistory.Values))
	} else {
		t.Log("MetricHistory is nil (cloud-collector may not be reachable)")
	}

	// Suggestion (may be nil if no data to base it on)
	if response.Suggestion != nil {
		t.Logf("Suggestion: %.2f (confidence: %s)", response.Suggestion.SuggestedThreshold, response.Suggestion.Confidence)
		t.Logf("Reason: %s", response.Suggestion.Reason)
		t.Logf("Percentiles: p50=%.2f p90=%.2f p95=%.2f p99=%.2f",
			response.Suggestion.MetricP50, response.Suggestion.MetricP90,
			response.Suggestion.MetricP95, response.Suggestion.MetricP99)
	}
}

func TestGetThresholdSuggestion_AWSCloudWatch_StandardNamespace(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// SQS alarm event (standard AWS/SQS namespace, threshold=60)
	eventID := testenv.RequireEnv(t, "TEST_EVENT_AWS_CLOUDWATCH_STANDARD")["TEST_EVENT_AWS_CLOUDWATCH_STANDARD"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	tenantID := ""
	if ev.Tenant != nil {
		tenantID = *ev.Tenant
	}
	require.NotEmpty(t, tenantID)

	response := GetThresholdSuggestion(ctx, db, ev, tenantID)

	resultJSON, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("=== AWS SQS Threshold Suggestion ===\n%s", string(resultJSON))

	assert.True(t, response.Available)
	assert.Equal(t, "AWS_CloudWatch_Alarm", response.Source)

	require.NotNil(t, response.AlertDefinition)
	t.Logf("Metric: %s/%s, Threshold: %.2f",
		response.AlertDefinition.MetricNamespace,
		response.AlertDefinition.MetricName,
		response.AlertDefinition.CurrentThreshold)
}

func TestGetThresholdSuggestion_AzureMonitor(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// Azure Monitor HighCPUAlert event
	eventID := testenv.RequireEnv(t, "TEST_EVENT_AZURE_MONITOR")["TEST_EVENT_AZURE_MONITOR"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	tenantID := ""
	if ev.Tenant != nil {
		tenantID = *ev.Tenant
	}
	require.NotEmpty(t, tenantID)

	response := GetThresholdSuggestion(ctx, db, ev, tenantID)

	resultJSON, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("=== Azure Monitor Threshold Suggestion ===\n%s", string(resultJSON))

	assert.True(t, response.Available)
	assert.Equal(t, "Azure_Monitor_Alert", response.Source)
	assert.Empty(t, response.Error)

	require.NotNil(t, response.AlertDefinition)
	t.Logf("Metric: %s/%s", response.AlertDefinition.MetricNamespace, response.AlertDefinition.MetricName)
	t.Logf("Operator: %s, Threshold: %.2f", response.AlertDefinition.Operator, response.AlertDefinition.CurrentThreshold)
	assert.NotEmpty(t, response.AlertDefinition.MetricName)

	if response.FiringAnalysis != nil {
		t.Logf("Firings: %d over %d days", response.FiringAnalysis.TotalOccurrences, response.FiringAnalysis.TimeRangeDays)
	}

	if response.Suggestion != nil {
		t.Logf("Suggestion: %.2f (confidence: %s)", response.Suggestion.SuggestedThreshold, response.Suggestion.Confidence)
		t.Logf("Reason: %s", response.Suggestion.Reason)
	}
}

func TestGetThresholdSuggestion_UnsupportedSource(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// Non-CloudWatch/Azure event
	eventID := testenv.RequireEnv(t, "TEST_EVENT_UNSUPPORTED_SOURCE")["TEST_EVENT_UNSUPPORTED_SOURCE"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	tenantID := ""
	if ev.Tenant != nil {
		tenantID = *ev.Tenant
	}

	response := GetThresholdSuggestion(ctx, db, ev, tenantID)

	t.Logf("Source: %s, Available: %v, Error: %s", response.Source, response.Available, response.Error)

	assert.False(t, response.Available)
	assert.Contains(t, response.Error, "not supported")
	assert.Nil(t, response.AlertDefinition)
	assert.Nil(t, response.Suggestion)
}

func TestExtractAWSAlertDefinition(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	eventID := testenv.RequireEnv(t, "TEST_EVENT_AWS_CLOUDWATCH")["TEST_EVENT_AWS_CLOUDWATCH"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	alertDef, err := extractAWSAlertDefinition(ctx, db, ev)
	require.NoError(t, err, "extractAWSAlertDefinition should not error")
	require.NotNil(t, alertDef)

	defJSON, _ := json.MarshalIndent(alertDef, "", "  ")
	t.Logf("AWS AlertDefinition:\n%s", string(defJSON))

	assert.NotEmpty(t, alertDef.MetricName)
	assert.NotEmpty(t, alertDef.MetricNamespace)
	assert.NotEmpty(t, alertDef.AlarmName)

	// ComparisonOperator should come from evidences
	if alertDef.Operator != "" {
		t.Logf("Operator extracted from evidences: %s", alertDef.Operator)
	} else {
		t.Log("Operator not found in evidences (may need to check evidence structure)")
	}
}

func TestExtractAzureAlertDefinition(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	eventID := testenv.RequireEnv(t, "TEST_EVENT_AZURE_MONITOR")["TEST_EVENT_AZURE_MONITOR"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	alertDef, err := extractAzureAlertDefinition(ctx, db, ev)
	require.NoError(t, err, "extractAzureAlertDefinition should not error")
	require.NotNil(t, alertDef)

	defJSON, _ := json.MarshalIndent(alertDef, "", "  ")
	t.Logf("Azure AlertDefinition:\n%s", string(defJSON))

	assert.NotEmpty(t, alertDef.MetricName)
	assert.NotEmpty(t, alertDef.Operator)
	assert.NotZero(t, alertDef.CurrentThreshold)
}

func TestGetThresholdSuggestion_AWSCloudWatch_CustomNamespace(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// Custom namespace alarm (rds/rds-deadlock-metric) with @aws.* dimensions
	eventID := testenv.RequireEnv(t, "TEST_EVENT_AWS_CLOUDWATCH_CUSTOM_NS")["TEST_EVENT_AWS_CLOUDWATCH_CUSTOM_NS"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	tenantID := ""
	if ev.Tenant != nil {
		tenantID = *ev.Tenant
	}
	require.NotEmpty(t, tenantID)

	response := GetThresholdSuggestion(ctx, db, ev, tenantID)

	resultJSON, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("=== AWS CloudWatch Custom Namespace Threshold Suggestion ===\n%s", string(resultJSON))

	assert.True(t, response.Available)
	assert.Equal(t, "AWS_CloudWatch_Alarm", response.Source)
	assert.Empty(t, response.Error)

	require.NotNil(t, response.AlertDefinition)
	assert.Equal(t, "rds-deadlock-metric", response.AlertDefinition.MetricName)
	assert.Equal(t, "rds", response.AlertDefinition.MetricNamespace)
	assert.Equal(t, "rds-deadlock-log-alert", response.AlertDefinition.AlarmName)
	t.Logf("Operator: %s, Threshold: %.2f", response.AlertDefinition.Operator, response.AlertDefinition.CurrentThreshold)

	if response.MetricHistory != nil {
		t.Logf("Metric history: %d data points", len(response.MetricHistory.Values))
	} else {
		t.Log("MetricHistory is nil (cloud-collector may not be reachable or metric has no data)")
	}

	if response.Suggestion != nil {
		t.Logf("Suggestion: %.2f (confidence: %s, reduction: %.0f%%)",
			response.Suggestion.SuggestedThreshold, response.Suggestion.Confidence, response.Suggestion.EstimatedReduction)
	}
}

func TestGetThresholdSuggestion_Prometheus(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// PodMemoryReachingLimit: simple threshold PromQL (> 0.8)
	eventID := testenv.RequireEnv(t, "TEST_EVENT_PROMETHEUS")["TEST_EVENT_PROMETHEUS"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title,  source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	require.NotNil(t, ev.Source)
	assert.Equal(t, "prometheus", *ev.Source)

	tenantID := ""
	if ev.Tenant != nil {
		tenantID = *ev.Tenant
	}
	require.NotEmpty(t, tenantID)

	response := GetThresholdSuggestion(ctx, db, ev, tenantID)

	resultJSON, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("=== Prometheus Threshold Suggestion ===\n%s", string(resultJSON))

	assert.True(t, response.Available)
	assert.Equal(t, "prometheus", response.Source)
	assert.Empty(t, response.Error)

	require.NotNil(t, response.AlertDefinition)
	assert.Equal(t, ">", response.AlertDefinition.Operator)
	assert.Equal(t, 0.8, response.AlertDefinition.CurrentThreshold)
	assert.Equal(t, "PodMemoryReachingLimit", response.AlertDefinition.AlarmName)
	t.Logf("Metric query: %s", response.AlertDefinition.MetricName)

	if response.MetricHistory != nil {
		t.Logf("Metric history: %d data points", len(response.MetricHistory.Values))
	} else {
		t.Log("MetricHistory is nil (relay/prometheus may not be reachable)")
	}

	if response.Suggestion != nil {
		t.Logf("Suggestion: %.4f (confidence: %s, reduction: %.0f%%)",
			response.Suggestion.SuggestedThreshold, response.Suggestion.Confidence, response.Suggestion.EstimatedReduction)
	}
}

func TestGetThresholdSuggestion_Prometheus_MultiCondition(t *testing.T) {
	ctx := context.Background()

	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// KubeDeploymentReplicasMismatch: multi-condition PromQL with 'and' — not parseable
	eventID := testenv.RequireEnv(t, "TEST_EVENT_PROMETHEUS_MULTI")["TEST_EVENT_PROMETHEUS_MULTI"]

	var ev models.Event
	err = db.GetContext(ctx, &ev,
		`SELECT id, title, source, fingerprint, cloud_account_id, tenant, labels, evidences
		 FROM events WHERE id = $1`, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	require.NotNil(t, ev.Source)
	assert.Equal(t, "prometheus", *ev.Source)

	tenantID := ""
	if ev.Tenant != nil {
		tenantID = *ev.Tenant
	}
	require.NotEmpty(t, tenantID)

	response := GetThresholdSuggestion(ctx, db, ev, tenantID)

	resultJSON, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("=== Prometheus Multi-Condition Threshold Suggestion ===\n%s", string(resultJSON))

	assert.False(t, response.Available, "Multi-condition PromQL should not be available")
	assert.Equal(t, "prometheus", response.Source)
	assert.NotEmpty(t, response.Error)
	t.Logf("Error (expected): %s", response.Error)
}

func TestComputeSuggestion(t *testing.T) {
	t.Run("greater_than_operator", func(t *testing.T) {
		alertDef := &AlertDefinition{
			MetricName:       "CPUUtilization",
			MetricNamespace:  "AWS/RDS",
			Operator:         "GreaterThanThreshold",
			CurrentThreshold: 70,
			Aggregation:      "Average",
		}

		metricHistory := &MetricHistory{
			Values: []float64{10, 15, 12, 20, 25, 18, 30, 22, 35, 28, 40, 50, 55, 60, 45, 32, 27, 19, 14, 11},
			Step:   300,
		}

		suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)
		require.NotNil(t, suggestion)

		t.Logf("Suggested threshold: %.2f (current: %.2f)", suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
		t.Logf("Percentiles: p50=%.2f p90=%.2f p95=%.2f p99=%.2f",
			suggestion.MetricP50, suggestion.MetricP90, suggestion.MetricP95, suggestion.MetricP99)
		t.Logf("Reason: %s", suggestion.Reason)

		// With these values, p95 is around 57.25, so suggestion should be ~62.98 (p95 * 1.10)
		// Current threshold 70 is above that, so it should say already tuned
		assert.Equal(t, alertDef.CurrentThreshold, suggestion.SuggestedThreshold)
		assert.Equal(t, "low", suggestion.Confidence) // Already tuned = low confidence
	})

	t.Run("threshold_too_low", func(t *testing.T) {
		alertDef := &AlertDefinition{
			MetricName:       "ErrorCount",
			Operator:         "GreaterThanThreshold",
			CurrentThreshold: 0,
		}

		// Values that frequently exceed 0
		metricHistory := &MetricHistory{
			Values: make([]float64, 500),
			Step:   300,
		}
		for i := range metricHistory.Values {
			metricHistory.Values[i] = float64(i % 10) // 0-9 repeating
		}

		suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)
		require.NotNil(t, suggestion)

		t.Logf("Suggested: %.2f (current: %.2f)", suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
		t.Logf("Reduction: %.0f%%", suggestion.EstimatedReduction)
		t.Logf("Reason: %s", suggestion.Reason)

		assert.Greater(t, suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
		assert.Greater(t, suggestion.EstimatedReduction, float64(0))
		// Multi-factor confidence: 500pts=20, CV~0.91=15, current=0 so ratio undefined=0, no quality=0 → 35=low
		assert.Contains(t, []string{"low", "medium"}, suggestion.Confidence)
	})

	t.Run("azure_high_cpu_threshold_1", func(t *testing.T) {
		// Simulates the real Azure HighCPUAlert: threshold=1 on Percentage CPU
		// This fires on essentially any CPU activity — clearly a false positive factory
		alertDef := &AlertDefinition{
			MetricName:       "Percentage CPU",
			MetricNamespace:  "Microsoft.Compute/virtualMachines",
			Operator:         "GreaterThan",
			CurrentThreshold: 1,
			Aggregation:      "Average",
			AlarmName:        "HighCPUAlert",
		}

		// Realistic VM CPU values (5-65% range, typical idle VM)
		metricHistory := &MetricHistory{
			Values: []float64{
				12, 15, 8, 22, 35, 28, 18, 10, 14, 25,
				30, 45, 38, 20, 16, 11, 9, 55, 42, 33,
				27, 19, 13, 7, 5, 48, 52, 60, 40, 23,
				17, 6, 10, 31, 44, 36, 21, 15, 8, 65,
			},
			Step: 300,
		}

		firingAnalysis := &FiringAnalysis{
			TotalOccurrences: 200,
			TimeRangeDays:    30,
			AvgFiringsPerDay: 6.67,
		}

		suggestion := computeSuggestion(alertDef, metricHistory, firingAnalysis, nil)
		require.NotNil(t, suggestion)

		t.Logf("Suggested: %.2f (current: %.2f)", suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
		t.Logf("Reduction: %.0f%%", suggestion.EstimatedReduction)
		t.Logf("Percentiles: p50=%.2f p90=%.2f p95=%.2f p99=%.2f",
			suggestion.MetricP50, suggestion.MetricP90, suggestion.MetricP95, suggestion.MetricP99)
		t.Logf("Reason: %s", suggestion.Reason)

		// Threshold of 1 is way below p95, should suggest raising significantly
		assert.Greater(t, suggestion.SuggestedThreshold, float64(50))
		assert.Greater(t, suggestion.EstimatedReduction, float64(90))
		assert.Contains(t, suggestion.Reason, "Raise threshold")
	})

	t.Run("sparse_metric_high_fire_rate", func(t *testing.T) {
		// Simulates HighErrorCriticalLogs: threshold > 1, metric is mostly 0.
		// Real scenario: relay returns ~2000 data points but only 4 are non-zero.
		// p95 of all values is well below threshold → normally "already tuned".
		// With firing analysis showing frequent fires, should use spike analysis.
		alertDef := &AlertDefinition{
			MetricName:       "increase(error_logs[5m])",
			Operator:         ">",
			CurrentThreshold: 1,
		}

		// Very sparse: 100 values, mostly 0, with 4 spikes above threshold
		values := make([]float64, 100)
		values[20] = 0.5
		values[40] = 0.8
		values[60] = 3
		values[80] = 5
		metricHistory := &MetricHistory{
			Values: values,
			Step:   300,
		}

		firingAnalysis := &FiringAnalysis{
			TotalOccurrences: 2000,
			TimeRangeDays:    30,
			AvgFiringsPerDay: 66.7,
		}

		// Without firing analysis → should say "correctly tuned" (p95=0.8 < threshold 1)
		suggestionNoFiring := computeSuggestion(alertDef, metricHistory, nil, nil)
		require.NotNil(t, suggestionNoFiring)
		assert.Equal(t, float64(0), suggestionNoFiring.EstimatedReduction)
		t.Logf("Without firing: suggested=%.2f reason=%s", suggestionNoFiring.SuggestedThreshold, suggestionNoFiring.Reason)

		// With firing analysis → should produce a useful suggestion based on spike values
		suggestion := computeSuggestion(alertDef, metricHistory, firingAnalysis, nil)
		require.NotNil(t, suggestion)
		assert.Greater(t, suggestion.SuggestedThreshold, alertDef.CurrentThreshold,
			"Should suggest threshold higher than current when alert fires frequently")
		assert.Greater(t, suggestion.EstimatedReduction, float64(0),
			"Should estimate some reduction in firings")
		t.Logf("With firing: suggested=%.2f reduction=%.0f%% reason=%s",
			suggestion.SuggestedThreshold, suggestion.EstimatedReduction, suggestion.Reason)
	})

	t.Run("less_than_operator", func(t *testing.T) {
		alertDef := &AlertDefinition{
			MetricName:       "AvailableMemory",
			Operator:         "LessThan",
			CurrentThreshold: 100,
		}

		metricHistory := &MetricHistory{
			Values: []float64{85, 88, 90, 92, 95, 87, 83, 91, 89, 94, 96, 93, 88, 86, 84, 90, 92, 87, 85, 91},
			Step:   300,
		}

		suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)
		require.NotNil(t, suggestion)

		t.Logf("Suggested: %.2f (current: %.2f)", suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
		t.Logf("Reason: %s", suggestion.Reason)

		assert.Less(t, suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
	})
}

func TestPercentile(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	assert.Equal(t, 5.5, percentile(sorted, 50))
	assert.InDelta(t, 9.1, percentile(sorted, 90), 0.01)
	assert.InDelta(t, 9.55, percentile(sorted, 95), 0.01)

	// Edge cases
	assert.Equal(t, float64(0), percentile([]float64{}, 50))
	assert.Equal(t, float64(42), percentile([]float64{42}, 50))
}

func TestExtractPromQLThreshold(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		wantQuery string
		wantOp    string
		wantVal   float64
		wantErr   bool
	}{
		{
			name:      "standard greater than",
			expr:      `rate(http_requests_total[5m]) > 100`,
			wantQuery: `rate(http_requests_total[5m])`,
			wantOp:    ">",
			wantVal:   100,
		},
		{
			name:      "greater than or equal",
			expr:      `rate(errors_total[5m]) >= 0.5`,
			wantQuery: `rate(errors_total[5m])`,
			wantOp:    ">=",
			wantVal:   0.5,
		},
		{
			name:      "less than",
			expr:      `up{job="myjob"} < 1`,
			wantQuery: `up{job="myjob"}`,
			wantOp:    "<",
			wantVal:   1,
		},
		{
			name:      "reversed comparison — number on left",
			expr:      `100 < rate(http_requests_total[5m])`,
			wantQuery: `rate(http_requests_total[5m])`,
			wantOp:    ">",
			wantVal:   100,
		},
		{
			name:      "parenthesized expression",
			expr:      `(sum(rate(errors[5m]))) >= 0.5`,
			wantQuery: `(sum(rate(errors[5m])))`,
			wantOp:    ">=",
			wantVal:   0.5,
		},
		{
			name:      "simple metric selector",
			expr:      `http_requests_total > 1000`,
			wantQuery: `http_requests_total`,
			wantOp:    ">",
			wantVal:   1000,
		},
		{
			name:      "complex expression with math",
			expr:      `(node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes) / node_memory_MemTotal_bytes * 100 > 90`,
			wantQuery: `(node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes) / node_memory_MemTotal_bytes * 100`,
			wantOp:    ">",
			wantVal:   90,
		},
		{
			name:    "no comparison — just a metric",
			expr:    `rate(requests_total[5m])`,
			wantErr: true,
		},
		{
			name:    "absent function — no comparison",
			expr:    `absent(up{job="myjob"})`,
			wantErr: true,
		},
		{
			name:      "compound AND — no guard, picks first leg (LHS)",
			expr:      `rate(errors[5m]) > 0.5 and rate(requests[5m]) > 10`,
			wantQuery: `rate(errors[5m])`,
			wantOp:    ">",
			wantVal:   0.5,
		},
		{
			name:      "compound OR — picks first leg",
			expr:      `increase(rabbitmq_channel_messages_unroutable_returned_total{namespace="rabbit"}[1m]) > 0 or increase(rabbitmq_channel_messages_unroutable_dropped_total{namespace="rabbit"}[1m]) > 0`,
			wantQuery: `increase(rabbitmq_channel_messages_unroutable_returned_total{namespace="rabbit"}[1m])`,
			wantOp:    ">",
			wantVal:   0,
		},
		{
			name:      "compound AND with guard — metric comparison on one side",
			expr:      `(error_rate / baseline) > 3 and error_count > 10`,
			wantQuery: `(error_rate / baseline)`,
			wantOp:    ">",
			wantVal:   3,
		},
		{
			name:      "compound AND — picks alerting threshold over guard (> 0)",
			expr:      `rate(errors[5m]) > 10 and rate(requests[5m]) > 0`,
			wantQuery: `rate(errors[5m])`,
			wantOp:    ">",
			wantVal:   10,
		},
		{
			name:      "compound AND — guard condition with zero literal",
			expr:      `kube_deployment_spec_replicas > kube_deployment_status_replicas_available and changes(kube_deployment_status_replicas_updated[10m]) == 0`,
			wantQuery: `changes(kube_deployment_status_replicas_updated[10m])`,
			wantOp:    "==",
			wantVal:   0,
		},
		{
			name:    "invalid PromQL",
			expr:    `this is not valid promql !!!`,
			wantErr: true,
		},
		{
			name:      "constant arithmetic threshold — (25 / 100)",
			expr:      `sum(increase(x[5m])) / sum(increase(y[5m])) > ( 25 / 100 )`,
			wantQuery: `sum(increase(x[5m])) / sum(increase(y[5m]))`,
			wantOp:    ">",
			wantVal:   0.25,
		},
		{
			name:      "constant multiplication threshold",
			expr:      `metric_total > 10 * 60`,
			wantQuery: `metric_total`,
			wantOp:    ">",
			wantVal:   600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractPromQLThreshold(tt.expr)
			if tt.wantErr {
				assert.Error(t, err, "expected error for expr: %s", tt.expr)
				return
			}
			require.NoError(t, err, "unexpected error for expr: %s", tt.expr)
			require.NotNil(t, result)
			assert.Equal(t, tt.wantQuery, result.QueryExpr)
			assert.Equal(t, tt.wantOp, result.Operator)
			assert.Equal(t, tt.wantVal, result.Value)
		})
	}
}

func TestFlipComparisonOperator(t *testing.T) {
	assert.Equal(t, "<", flipComparisonOperator(">"))
	assert.Equal(t, "<=", flipComparisonOperator(">="))
	assert.Equal(t, ">", flipComparisonOperator("<"))
	assert.Equal(t, ">=", flipComparisonOperator("<="))
	assert.Equal(t, "==", flipComparisonOperator("=="))
	assert.Equal(t, "!=", flipComparisonOperator("!="))
}

func TestParsePrometheusRangeResponse(t *testing.T) {
	now := time.Now()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)

	t.Run("valid response with string values", func(t *testing.T) {
		respMap := map[string]any{
			"threshold_query": map[string]any{
				"series_list_result": []any{
					map[string]any{
						"metric":     map[string]any{"__name__": "http_requests_total"},
						"timestamps": []any{float64(1000), float64(1060), float64(1120)},
						"values":     []any{"10.5", "20.3", "15.7"},
					},
				},
			},
		}

		result := parsePrometheusRangeResponse(respMap, "threshold_query", sevenDaysAgo, now)
		require.NotNil(t, result)
		assert.Equal(t, []float64{10.5, 20.3, 15.7}, result.Values)
		assert.Equal(t, []int64{1000, 1060, 1120}, result.Timestamps)
	})

	t.Run("valid response with float values", func(t *testing.T) {
		respMap := map[string]any{
			"threshold_query": map[string]any{
				"series_list_result": []any{
					map[string]any{
						"metric":     map[string]any{"__name__": "cpu_usage"},
						"timestamps": []any{float64(2000), float64(2060)},
						"values":     []any{float64(45.2), float64(78.9)},
					},
				},
			},
		}

		result := parsePrometheusRangeResponse(respMap, "threshold_query", sevenDaysAgo, now)
		require.NotNil(t, result)
		assert.Equal(t, []float64{45.2, 78.9}, result.Values)
		assert.Equal(t, []int64{2000, 2060}, result.Timestamps)
	})

	t.Run("missing query key", func(t *testing.T) {
		respMap := map[string]any{
			"other_key": map[string]any{},
		}

		result := parsePrometheusRangeResponse(respMap, "threshold_query", sevenDaysAgo, now)
		assert.Nil(t, result)
	})

	t.Run("empty series list", func(t *testing.T) {
		respMap := map[string]any{
			"threshold_query": map[string]any{
				"series_list_result": []any{},
			},
		}

		result := parsePrometheusRangeResponse(respMap, "threshold_query", sevenDaysAgo, now)
		assert.Nil(t, result)
	})

	t.Run("NaN values are skipped", func(t *testing.T) {
		respMap := map[string]any{
			"threshold_query": map[string]any{
				"series_list_result": []any{
					map[string]any{
						"metric":     map[string]any{},
						"timestamps": []any{float64(1000), float64(1060), float64(1120)},
						"values":     []any{"10.0", "NaN", "30.0"},
					},
				},
			},
		}

		result := parsePrometheusRangeResponse(respMap, "threshold_query", sevenDaysAgo, now)
		require.NotNil(t, result)
		assert.Equal(t, []float64{10.0, 30.0}, result.Values)
		assert.Equal(t, []int64{1000, 1120}, result.Timestamps)
	})
}

func TestParseISODurationToSeconds(t *testing.T) {
	assert.Equal(t, 300, parseISODurationToSeconds("PT5M"))
	assert.Equal(t, 3600, parseISODurationToSeconds("PT1H"))
	assert.Equal(t, 3900, parseISODurationToSeconds("PT1H5M"))
	assert.Equal(t, 30, parseISODurationToSeconds("PT30S"))
	assert.Equal(t, 0, parseISODurationToSeconds("P1D")) // Not a time duration
	assert.Equal(t, 0, parseISODurationToSeconds(""))
}

func TestComputeMedian(t *testing.T) {
	assert.Equal(t, 3.0, computeMedian([]float64{1, 2, 3, 4, 5}))
	assert.Equal(t, 2.5, computeMedian([]float64{1, 2, 3, 4}))
	assert.Equal(t, 42.0, computeMedian([]float64{42}))
	assert.Equal(t, 0.0, computeMedian([]float64{}))
}

func TestComputeMAD(t *testing.T) {
	// [1,2,3,4,5]: median=3, deviations=[2,1,0,1,2], sorted=[0,1,1,2,2], MAD=1
	assert.Equal(t, 1.0, computeMAD([]float64{1, 2, 3, 4, 5}))

	// All same values: MAD=0
	assert.Equal(t, 0.0, computeMAD([]float64{5, 5, 5, 5, 5}))

	// Single value: MAD=0
	assert.Equal(t, 0.0, computeMAD([]float64{42}))

	// Empty: MAD=0
	assert.Equal(t, 0.0, computeMAD([]float64{}))

	// [1,1,2,2,4,6,9]: median=2, deviations=[1,1,0,0,2,4,7], sorted=[0,0,1,1,2,4,7], MAD=1
	assert.Equal(t, 1.0, computeMAD([]float64{1, 1, 2, 2, 4, 6, 9}))
}

func TestComputeIQR(t *testing.T) {
	// [1,2,3,4,5,6,7,8,9,10]: P75=7.75, P25=3.25, IQR=4.5
	assert.InDelta(t, 4.5, computeIQR([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}), 0.01)

	// All same: IQR=0
	assert.Equal(t, 0.0, computeIQR([]float64{5, 5, 5, 5}))
}

func TestApplySanityCap(t *testing.T) {
	// Normal case: suggestion within bounds
	assert.Equal(t, 15.0, applySanityCap(15.0, 10.0, 20.0))

	// Suggestion exceeds cap (current*10=100, p99=20, cap=100)
	assert.Equal(t, 100.0, applySanityCap(500.0, 10.0, 20.0))

	// P99 > current*10: use P99 as cap
	assert.Equal(t, 200.0, applySanityCap(500.0, 10.0, 200.0))

	// Current=0: no cap applied
	assert.Equal(t, 500.0, applySanityCap(500.0, 0.0, 20.0))
}

func TestComputeSuggestion_MAD_sanity_cap(t *testing.T) {
	// Scenario: RabbitmqTooManyReadyMessages — current=10, values span 0-10000.
	// MAD-based suggestion would be huge, but sanity cap limits to max(100, P99).
	alertDef := &AlertDefinition{
		MetricName:       "rabbitmq_ready_messages",
		Operator:         ">",
		CurrentThreshold: 10,
	}

	values := make([]float64, 200)
	for i := range values {
		values[i] = float64(i * 50) // 0, 50, 100, ..., 9950
	}

	metricHistory := &MetricHistory{Values: values, Step: 300}
	suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)
	require.NotNil(t, suggestion)

	t.Logf("Suggested: %.2f (current: %.2f) method=%s", suggestion.SuggestedThreshold, alertDef.CurrentThreshold, suggestion.Method)

	// Sanity cap = max(10*10, P99) = max(100, ~9851) = ~9851
	// MAD-based would be even higher, but bounded
	assert.LessOrEqual(t, suggestion.SuggestedThreshold, float64(10000))
	assert.Greater(t, suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
}

func TestComputeSuggestion_constant_metric(t *testing.T) {
	// All same values: MAD=0, IQR=0, falls to P95 method
	alertDef := &AlertDefinition{
		MetricName:       "constant_metric",
		Operator:         ">",
		CurrentThreshold: 0,
	}

	values := make([]float64, 100)
	for i := range values {
		values[i] = 5.0
	}

	metricHistory := &MetricHistory{Values: values, Step: 300}
	suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)
	require.NotNil(t, suggestion)

	assert.Equal(t, "P95", suggestion.Method)
	assert.InDelta(t, 5.5, suggestion.SuggestedThreshold, 0.1) // 5.0 * 1.10 = 5.5
}

func TestClassifyAlertQuality(t *testing.T) {
	tests := []struct {
		name          string
		score         AlertQualityScore
		wantClass     string
		wantRecommend string
	}{
		{
			name: "false positive — fires often, auto-resolves, no engagement",
			score: AlertQualityScore{
				FlappingRate:    0.95,
				ResolutionRate:  0.95,
				EngagementRate:  0.02,
				FiringFrequency: 5.0,
				TransientRate:   0.3,
			},
			wantClass:     "false_positive",
			wantRecommend: "disable_alert",
		},
		{
			name: "false positive with transient spikes — recommend duration increase",
			score: AlertQualityScore{
				FlappingRate:    0.95,
				ResolutionRate:  0.95,
				EngagementRate:  0.02,
				FiringFrequency: 5.0,
				TransientRate:   0.85,
			},
			wantClass:     "false_positive",
			wantRecommend: "increase_duration",
		},
		{
			name: "broken — never resolves",
			score: AlertQualityScore{
				FlappingRate:    0.0,
				ResolutionRate:  0.05,
				EngagementRate:  0.3,
				FiringFrequency: 3.0,
				InstantRate:     0.0,
			},
			wantClass:     "broken",
			wantRecommend: "investigate",
		},
		{
			name: "not broken when instant events dominate",
			score: AlertQualityScore{
				FlappingRate:    0.0,
				ResolutionRate:  0.05,
				EngagementRate:  0.3,
				FiringFrequency: 3.0,
				InstantRate:     0.8,
			},
			// InstantRate > 0.5, so broken check doesn't fire.
			// Resolution 5% + engagement 30% doesn't match any noisy category → defaults to healthy.
			// This is correct: instant events provide insufficient signal for classification.
			wantClass:     "healthy",
			wantRecommend: "no_action",
		},
		{
			name: "noisy but real — high freq, resolves, engaged",
			score: AlertQualityScore{
				FlappingRate:    0.1,
				ResolutionRate:  0.8,
				EngagementRate:  0.5,
				FiringFrequency: 15.0,
				TransientRate:   0.3,
			},
			wantClass:     "noisy_but_real",
			wantRecommend: "tune_threshold",
		},
		{
			name: "noisy but real with transient — recommend duration",
			score: AlertQualityScore{
				FlappingRate:    0.2,
				ResolutionRate:  0.7,
				EngagementRate:  0.3,
				FiringFrequency: 3.0,
				TransientRate:   0.8,
			},
			wantClass:     "noisy_but_real",
			wantRecommend: "increase_duration",
		},
		{
			name: "noisy ignored — fires often, resolves, nobody acts",
			score: AlertQualityScore{
				FlappingRate:    0.2,
				ResolutionRate:  0.7,
				EngagementRate:  0.02,
				FiringFrequency: 1.5,
				TransientRate:   0.3,
			},
			wantClass:     "false_positive",
			wantRecommend: "tune_threshold",
		},
		{
			name: "healthy — low frequency, well-engaged",
			score: AlertQualityScore{
				FlappingRate:    0.05,
				ResolutionRate:  0.9,
				EngagementRate:  0.8,
				FiringFrequency: 0.3,
			},
			wantClass:     "healthy",
			wantRecommend: "no_action",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := tt.score
			classifyAlertQuality(&score)
			assert.Equal(t, tt.wantClass, score.Classification)
			assert.Equal(t, tt.wantRecommend, score.Recommendation)
		})
	}
}

func TestComputeConfidence_multifactor(t *testing.T) {
	t.Run("high confidence — stable data, reasonable suggestion, good quality", func(t *testing.T) {
		values := make([]float64, 600)
		for i := range values {
			values[i] = 50 + float64(i%5) // 50-54, low CV
		}
		quality := &AlertQualityScore{Classification: "noisy_but_real"}
		result := computeConfidence(values, 100, 80, quality)
		assert.Equal(t, "high", result) // 30+25+25+20=100
	})

	t.Run("low confidence — few points, extreme suggestion, no quality", func(t *testing.T) {
		values := []float64{1, 2, 3, 4, 5}
		result := computeConfidence(values, 10, 5000, nil)
		assert.Equal(t, "low", result) // 10+5+0+0=15
	})

	t.Run("medium confidence — decent data, moderate change", func(t *testing.T) {
		values := make([]float64, 200)
		for i := range values {
			values[i] = float64(i % 20) // 0-19
		}
		result := computeConfidence(values, 10, 15, nil)
		assert.Equal(t, "medium", result) // 20+5+25+0=50
	})
}

func TestComputeDurationRecommendation(t *testing.T) {
	tests := []struct {
		name          string
		suggestion    ThresholdSuggestion
		alertDef      AlertDefinition
		quality       *AlertQualityScore
		wantRecType   string
		wantDuration  int
		wantHasReason bool
	}{
		{
			name:         "no quality info — defaults to tune_threshold when reduction > 0",
			suggestion:   ThresholdSuggestion{EstimatedReduction: 30},
			alertDef:     AlertDefinition{},
			quality:      nil,
			wantRecType:  "tune_threshold",
			wantDuration: 0,
		},
		{
			name:         "no quality info — defaults to none when no reduction",
			suggestion:   ThresholdSuggestion{EstimatedReduction: 0},
			alertDef:     AlertDefinition{},
			quality:      nil,
			wantRecType:  "none",
			wantDuration: 0,
		},
		{
			name:       "transient + threshold suggestion — tune_both",
			suggestion: ThresholdSuggestion{EstimatedReduction: 50},
			alertDef:   AlertDefinition{Period: 300, EvaluationPeriods: 1}, // 5 min window
			quality: &AlertQualityScore{
				TransientRate:  0.85,
				DurationP90:    420, // 7 min P90
				Classification: "false_positive",
			},
			wantRecType:   "tune_both",
			wantDuration:  15, // ceil(7*1.5/5)*5 = ceil(2.1)*5 = 15
			wantHasReason: true,
		},
		{
			name:       "transient + no threshold reduction — increase_duration only",
			suggestion: ThresholdSuggestion{EstimatedReduction: 5},
			alertDef:   AlertDefinition{Period: 300, EvaluationPeriods: 1},
			quality: &AlertQualityScore{
				TransientRate:  0.9,
				DurationP90:    300, // 5 min P90
				Classification: "false_positive",
			},
			wantRecType:   "increase_duration",
			wantDuration:  10, // ceil(5*1.5/5)*5 = ceil(1.5)*5 = 10
			wantHasReason: true,
		},
		{
			name:       "transient but current window already sufficient — tune_threshold",
			suggestion: ThresholdSuggestion{EstimatedReduction: 40},
			alertDef:   AlertDefinition{Period: 300, EvaluationPeriods: 3}, // 15 min window
			quality: &AlertQualityScore{
				TransientRate:  0.85,
				DurationP90:    300, // P90=5min, suggested=10, but current=15 already covers it
				Classification: "noisy_but_real",
			},
			wantRecType:  "tune_threshold",
			wantDuration: 0,
		},
		{
			name:       "broken classification — disable",
			suggestion: ThresholdSuggestion{EstimatedReduction: 30},
			alertDef:   AlertDefinition{},
			quality: &AlertQualityScore{
				TransientRate:  0.0,
				Classification: "broken",
			},
			wantRecType:   "disable",
			wantDuration:  0,
			wantHasReason: true,
		},
		{
			name:       "not transient + has threshold suggestion — tune_threshold",
			suggestion: ThresholdSuggestion{EstimatedReduction: 50},
			alertDef:   AlertDefinition{},
			quality: &AlertQualityScore{
				TransientRate:  0.3,
				Classification: "noisy_but_real",
			},
			wantRecType:  "tune_threshold",
			wantDuration: 0,
		},
		{
			name:       "not transient + no reduction — none",
			suggestion: ThresholdSuggestion{EstimatedReduction: 0},
			alertDef:   AlertDefinition{},
			quality: &AlertQualityScore{
				TransientRate:  0.2,
				Classification: "healthy",
			},
			wantRecType:  "none",
			wantDuration: 0,
		},
		{
			name:       "duration capped at 30 minutes",
			suggestion: ThresholdSuggestion{EstimatedReduction: 5},
			alertDef:   AlertDefinition{Period: 300, EvaluationPeriods: 1},
			quality: &AlertQualityScore{
				TransientRate:  0.9,
				DurationP90:    1800, // 30 min P90
				Classification: "false_positive",
			},
			wantRecType:   "increase_duration",
			wantDuration:  30, // would be ceil(30*1.5/5)*5=45 but capped at 30
			wantHasReason: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.suggestion
			computeDurationRecommendation(&s, &tt.alertDef, tt.quality)
			assert.Equal(t, tt.wantRecType, s.RecommendationType)
			assert.Equal(t, tt.wantDuration, s.SuggestedDuration)
			if tt.wantHasReason {
				assert.NotEmpty(t, s.DurationReason)
			}
		})
	}
}

func TestComputeSuggestion_unknown_operator(t *testing.T) {
	t.Run("unknown operator should not suggest threshold lower than current", func(t *testing.T) {
		// SQS-like case: current=60, most values are 0 with occasional spikes
		values := make([]float64, 100)
		for i := range values {
			values[i] = 0 // mostly zero
		}
		values[95] = 100
		values[96] = 200
		values[97] = 50
		values[98] = 80
		values[99] = 150

		alertDef := &AlertDefinition{
			CurrentThreshold: 60,
			Operator:         "", // empty/unknown operator (AWS CloudWatch)
		}
		metricHistory := &MetricHistory{Values: values}
		suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)

		// Should NOT suggest lower than current (that would increase noise)
		assert.GreaterOrEqual(t, suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
	})

	t.Run("unknown operator suggests higher when metric warrants it", func(t *testing.T) {
		// Metric is consistently above threshold
		values := make([]float64, 100)
		for i := range values {
			values[i] = 80 + float64(i%20) // 80-99
		}

		alertDef := &AlertDefinition{
			CurrentThreshold: 50,
			Operator:         "",
		}
		metricHistory := &MetricHistory{Values: values}
		suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)

		assert.Greater(t, suggestion.SuggestedThreshold, alertDef.CurrentThreshold)
		assert.Greater(t, suggestion.EstimatedReduction, 0.0)
	})
}

func TestComputeSuggestion_large_ratio_warning(t *testing.T) {
	t.Run("GT operator warns on >5x change but does not cap", func(t *testing.T) {
		// current=1, metric values high → suggestion will be >> 5
		values := make([]float64, 100)
		for i := range values {
			values[i] = float64(i * 10) // 0 to 990
		}

		alertDef := &AlertDefinition{
			CurrentThreshold: 1,
			Operator:         ">",
		}
		metricHistory := &MetricHistory{Values: values}
		suggestion := computeSuggestion(alertDef, metricHistory, nil, nil)

		// Should NOT cap — the sanity cap (max(10, P99)) is the real guard
		assert.Greater(t, suggestion.SuggestedThreshold, 5.0)
		// Should add a review warning
		assert.Contains(t, suggestion.Reason, "review manually")
	})
}
