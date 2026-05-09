package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
)

// sanitizeFloat replaces +Inf, -Inf, and NaN with 0 so JSON marshaling doesn't fail.
func sanitizeFloat(v float64) float64 {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return 0
	}
	return v
}

// noisyAlert represents an alert rule that fires frequently, grouped by alert identity
type noisyAlert struct {
	AlertRuleKey   string `db:"alert_rule_key"`
	CloudAccountID string `db:"cloud_account_id"`
	TenantID       string `db:"tenant"`
	Source         string `db:"source"`
	FireCount      int    `db:"fire_count"`
}

// AnalyzeNoisyAlerts scans for frequently-firing alerts and pre-computes threshold suggestions.
// Called by the "Threshold Suggestion Refresh" cron job.
func AnalyzeNoisyAlerts(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	db := dbms.Db

	// Find noisy alerts: >=5 firings in 30 days, grouped by alert rule identity per source.
	// Excludes events belonging to disabled cloud accounts.
	var noisy []noisyAlert
	err = db.Select(&noisy, `
		WITH noisy AS (
			SELECT
				CASE e.source
					WHEN 'prometheus' THEN e.labels->>'alertname'
					WHEN 'pagerduty_webhook' THEN COALESCE(e.labels->>'nb_alert_name', e.labels->>'alertname')
					WHEN 'AWS_CloudWatch_Alarm' THEN e.labels->>'aws_event_arn'
					WHEN 'azure_monitor_webhook' THEN COALESCE(e.labels->>'azure_alert_name', e.labels->>'alertname')
					WHEN 'Azure_Monitor_Alert' THEN COALESCE(e.labels->>'azure_alert_name', e.labels->>'alertname')
					WHEN 'GCP_Metric_Alert' THEN e.labels->>'gcp_policy_id'
				END as alert_rule_key,
				e.cloud_account_id, e.tenant, e.source,
				COUNT(*) as fire_count
			FROM events e
			JOIN cloud_accounts ca ON ca.id = e.cloud_account_id AND ca.status != 'disabled'
			WHERE e.source IN ('prometheus','pagerduty_webhook','AWS_CloudWatch_Alarm',
			                 'azure_monitor_webhook','Azure_Monitor_Alert','GCP_Metric_Alert')
			  AND e.starts_at > NOW() - INTERVAL '30 days'
			  AND e.cloud_account_id IS NOT NULL
			GROUP BY alert_rule_key, e.cloud_account_id, e.tenant, e.source
			HAVING COUNT(*) >= 5
		)
		SELECT alert_rule_key, cloud_account_id, tenant, source, fire_count
		FROM noisy
		WHERE alert_rule_key IS NOT NULL AND alert_rule_key != ''
		ORDER BY fire_count DESC
		LIMIT 500
	`)
	if err != nil {
		ctx.GetLogger().Error("threshold_suggestion_batch: failed to query noisy alerts", "error", err)
		return err
	}

	ctx.GetLogger().Info("threshold_suggestion_batch: found noisy alerts", "count", len(noisy))

	// Clean up stale rows older than 30 days.
	_, _ = db.Exec(`DELETE FROM event_threshold_suggestions WHERE computed_at < NOW() - INTERVAL '30 days'`)

	// Skip alert_rule_keys that don't need re-processing:
	// - Any status processed in the last 24 hours (recently computed)
	// - status='ok' rows less than 7 days old (still valid, no need to re-query metrics)
	var recentKeys []string
	if err = db.Select(&recentKeys, `
		SELECT alert_rule_key FROM event_threshold_suggestions
		WHERE alert_rule_key IS NOT NULL
		  AND (computed_at > NOW() - INTERVAL '24 hours'
		       OR (status = 'ok' AND computed_at > NOW() - INTERVAL '7 days'))`); err != nil {
		ctx.GetLogger().Warn("threshold_suggestion_batch: failed to query recent keys, processing all", "error", err)
	}
	recentSet := make(map[string]bool, len(recentKeys))
	for _, key := range recentKeys {
		recentSet[key] = true
	}

	pending := make([]noisyAlert, 0, len(noisy))
	for _, na := range noisy {
		if !recentSet[na.AlertRuleKey] {
			pending = append(pending, na)
		}
	}

	ctx.GetLogger().Info("threshold_suggestion_batch: after filtering recently processed",
		"total", len(noisy), "pending", len(pending), "already_processed", len(noisy)-len(pending))

	var processed, stored, skipped, errCount atomic.Int64

	// Group by cloud_account_id so we can limit per-account concurrency.
	byAccount := make(map[string][]noisyAlert)
	for _, na := range pending {
		byAccount[na.CloudAccountID] = append(byAccount[na.CloudAccountID], na)
	}

	// Process accounts concurrently (max 5), but entries within each account sequentially.
	const maxConcurrentAccounts = 5
	sem := make(chan struct{}, maxConcurrentAccounts)

	var wg sync.WaitGroup
	for _, entries := range byAccount {
		wg.Add(1)
		sem <- struct{}{}
		go func(entries []noisyAlert) {
			defer wg.Done()
			defer func() { <-sem }()
			for _, na := range entries {
				processNoisyAlert(ctx, db, na, &processed, &stored, &skipped, &errCount)
			}
		}(entries)
	}
	wg.Wait()

	ctx.GetLogger().Info("threshold_suggestion_batch: completed",
		"processed", processed.Load(), "stored", stored.Load(),
		"skipped", skipped.Load(), "errors", errCount.Load())

	return nil
}

func processNoisyAlert(ctx *security.RequestContext, db *sqlx.DB, na noisyAlert,
	processed, stored, skipped, errCount *atomic.Int64) {

	// Find a representative event by alert_rule_key.
	// Azure and PagerDuty use COALESCE fallback to match the batch query grouping.
	var whereCondition string
	switch na.Source {
	case "prometheus":
		whereCondition = "labels->>'alertname' = $1"
	case "pagerduty_webhook":
		whereCondition = "COALESCE(labels->>'nb_alert_name', labels->>'alertname') = $1"
	case "AWS_CloudWatch_Alarm":
		whereCondition = "labels->>'aws_event_arn' = $1"
	case "azure_monitor_webhook", "Azure_Monitor_Alert":
		whereCondition = "COALESCE(labels->>'azure_alert_name', labels->>'alertname') = $1"
	case "GCP_Metric_Alert":
		whereCondition = "labels->>'gcp_policy_id' = $1"
	default:
		errCount.Add(1)
		return
	}

	var ev models.Event
	err := db.Get(&ev,
		fmt.Sprintf(`SELECT * FROM events WHERE %s AND cloud_account_id = $2 AND source = $3
		 ORDER BY starts_at DESC LIMIT 1`, whereCondition),
		na.AlertRuleKey, na.CloudAccountID, na.Source)
	if err != nil {
		ctx.GetLogger().Warn("threshold_suggestion_batch: failed to get representative event",
			"alert_rule_key", na.AlertRuleKey, "source", na.Source, "error", err)
		upsertSkippedSuggestion(ctx.GetContext(), db, na, "error", "failed to get representative event", "")
		errCount.Add(1)
		return
	}

	processed.Add(1)

	response := GetThresholdSuggestion(ctx.GetContext(), db, ev, na.TenantID)

	if !response.Available || response.Suggestion == nil {
		skipped.Add(1)
		reason := response.Error
		if reason == "" {
			reason = "no suggestion available"
		}
		alertName := ""
		if response.AlertDefinition != nil {
			alertName = response.AlertDefinition.AlarmName
		}
		upsertSkippedSuggestion(ctx.GetContext(), db, na, "skipped", reason, alertName)
		return
	}

	// Not eligible alerts: store with reason so we don't re-process them
	recType := response.Suggestion.RecommendationType
	if recType == "not_eligible" {
		skipped.Add(1)
		reason := response.Suggestion.Reason
		if reason == "" {
			reason = "not eligible for threshold tuning"
		}
		alertName := ""
		if response.AlertDefinition != nil {
			alertName = response.AlertDefinition.AlarmName
		}
		upsertSkippedSuggestion(ctx.GetContext(), db, na, "not_eligible", reason, alertName)
		return
	}

	// Skip only if there's truly nothing actionable: no threshold change AND no duration/disable recommendation
	if response.Suggestion.EstimatedReduction == 0 &&
		recType != "increase_duration" && recType != "tune_both" && recType != "disable" {
		skipped.Add(1)
		alertName := ""
		if response.AlertDefinition != nil {
			alertName = response.AlertDefinition.AlarmName
		}
		upsertSkippedSuggestion(ctx.GetContext(), db, na, "skipped", "no reduction possible", alertName)
		return
	}

	// Use the representative event's fingerprint so the UI can query matching events.
	// Fall back to alert_rule_key if fingerprint is unavailable.
	fingerprint := na.AlertRuleKey
	if ev.Fingerprint != nil && *ev.Fingerprint != "" {
		fingerprint = *ev.Fingerprint
	}

	// Capture the events.aggregation_key value so the UI can filter the
	// Recent Events tab without duplicating the per-source label-mapping
	// logic. For sources like GCP/AWS, alert_rule_key is the policy/alarm
	// identifier and does NOT match events.aggregation_key.
	eventAggregationKey := ""
	if ev.AggregationKey != nil {
		eventAggregationKey = *ev.AggregationKey
	}

	if err := upsertSuggestion(ctx.GetContext(), db, na, fingerprint, eventAggregationKey, response); err != nil {
		ctx.GetLogger().Warn("threshold_suggestion_batch: failed to upsert suggestion",
			"alert_rule_key", na.AlertRuleKey, "error", err)
		errCount.Add(1)
		return
	}

	stored.Add(1)
}

// GetCachedSuggestion reads a pre-computed suggestion from the cache.
// Returns nil if not found or stale (>7 days old).
func GetCachedSuggestion(ctx context.Context, db *sqlx.DB, alertRuleKey, cloudAccountID string) (*ThresholdSuggestionResponse, error) {
	var row struct {
		Source             string    `db:"source"`
		AlertName          *string   `db:"alert_name"`
		MetricName         string    `db:"metric_name"`
		MetricNamespace    *string   `db:"metric_namespace"`
		CurrentThreshold   float64   `db:"current_threshold"`
		Operator           *string   `db:"operator"`
		Aggregation        *string   `db:"aggregation"`
		SuggestedThreshold float64   `db:"suggested_threshold"`
		Reason             string    `db:"reason"`
		Confidence         *string   `db:"confidence"`
		EstimatedReduction *float64  `db:"estimated_reduction"`
		FiringAnalysis     *string   `db:"firing_analysis"`
		MetricStats        *string   `db:"metric_stats"`
		AlertQuality       *string   `db:"alert_quality"`
		Method             *string   `db:"method"`
		ComputedAt         time.Time `db:"computed_at"`
	}

	err := db.GetContext(ctx, &row, `
		SELECT source, alert_name, metric_name, metric_namespace,
		       current_threshold, operator, aggregation,
		       suggested_threshold, reason, confidence, estimated_reduction,
		       firing_analysis, metric_stats, alert_quality, method, computed_at
		FROM event_threshold_suggestions
		WHERE alert_rule_key = $1 AND cloud_account_id = $2
		  AND status = 'ok'
		  AND computed_at > NOW() - INTERVAL '7 days'
	`, alertRuleKey, cloudAccountID)
	if err != nil {
		return nil, err
	}

	resp := &ThresholdSuggestionResponse{
		Available: true,
		Source:    row.Source,
		AlertDefinition: &AlertDefinition{
			MetricName:       row.MetricName,
			CurrentThreshold: row.CurrentThreshold,
		},
		Suggestion: &ThresholdSuggestion{
			SuggestedThreshold: row.SuggestedThreshold,
			Reason:             row.Reason,
		},
	}

	if row.MetricNamespace != nil {
		resp.AlertDefinition.MetricNamespace = *row.MetricNamespace
	}
	if row.Operator != nil {
		resp.AlertDefinition.Operator = *row.Operator
	}
	if row.Aggregation != nil {
		resp.AlertDefinition.Aggregation = *row.Aggregation
	}
	if row.AlertName != nil {
		resp.AlertDefinition.AlarmName = *row.AlertName
	}
	if row.Confidence != nil {
		resp.Suggestion.Confidence = *row.Confidence
	}
	if row.EstimatedReduction != nil {
		resp.Suggestion.EstimatedReduction = *row.EstimatedReduction
	}
	if row.Method != nil {
		resp.Suggestion.Method = *row.Method
	}

	// Parse JSONB firing analysis
	if row.FiringAnalysis != nil {
		var fa FiringAnalysis
		if err := json.Unmarshal([]byte(*row.FiringAnalysis), &fa); err == nil {
			resp.FiringAnalysis = &fa
		}
	}

	// Parse JSONB metric stats (includes MAD-based stats and duration recommendation)
	if row.MetricStats != nil {
		var stats map[string]interface{}
		if err := json.Unmarshal([]byte(*row.MetricStats), &stats); err == nil {
			toFloat := func(key string) float64 {
				if v, ok := stats[key].(float64); ok {
					return v
				}
				return 0
			}
			resp.Suggestion.MetricP50 = toFloat("p50")
			resp.Suggestion.MetricP90 = toFloat("p90")
			resp.Suggestion.MetricP95 = toFloat("p95")
			resp.Suggestion.MetricP99 = toFloat("p99")
			resp.Suggestion.MetricMedian = toFloat("median")
			resp.Suggestion.MetricMAD = toFloat("mad")
			if v, ok := stats["recommendation_type"].(string); ok {
				resp.Suggestion.RecommendationType = v
			}
			if v, ok := stats["suggested_duration"].(float64); ok {
				resp.Suggestion.SuggestedDuration = int(v)
			}
			if v, ok := stats["duration_reason"].(string); ok {
				resp.Suggestion.DurationReason = v
			}
			if v, ok := stats["risk_level"].(string); ok {
				resp.Suggestion.RiskLevel = v
			}
			if v, ok := stats["risk_warnings"].([]interface{}); ok {
				for _, w := range v {
					if ws, ok := w.(string); ok {
						resp.Suggestion.RiskWarnings = append(resp.Suggestion.RiskWarnings, ws)
					}
				}
			}
		}
	}

	// Parse JSONB alert quality
	if row.AlertQuality != nil {
		var aq AlertQualityScore
		if err := json.Unmarshal([]byte(*row.AlertQuality), &aq); err == nil {
			resp.AlertQuality = &aq
		}
	}

	return resp, nil
}

// GetCachedSkippedReason returns the reason string for a recently-skipped suggestion.
// This allows the investigate-page card to show a "not eligible" message instead of hiding entirely.
func GetCachedSkippedReason(ctx context.Context, db *sqlx.DB, alertRuleKey, cloudAccountID string) (string, string, error) {
	var row struct {
		Source string  `db:"source"`
		Reason string  `db:"reason"`
		Status string  `db:"status"`
		Name   *string `db:"alert_name"`
	}
	err := db.GetContext(ctx, &row, `
		SELECT source, reason, status, alert_name
		FROM event_threshold_suggestions
		WHERE alert_rule_key = $1 AND cloud_account_id = $2
		  AND status IN ('skipped', 'error')
		  AND computed_at > NOW() - INTERVAL '7 days'
	`, alertRuleKey, cloudAccountID)
	if err != nil {
		return "", "", err
	}
	return row.Source, row.Reason, nil
}

// UpsertSuggestionCache stores a live-computed suggestion into the cache for future use.
func UpsertSuggestionCache(ctx context.Context, db *sqlx.DB, ev models.Event, tenantID string, response ThresholdSuggestionResponse) {
	if ev.CloudAccountId == nil {
		return
	}

	source := ""
	if ev.Source != nil {
		source = *ev.Source
	}

	labels := GetLabelsMap(ev)
	alertRuleKey := ExtractAlertRuleKey(source, labels)
	if alertRuleKey == "" {
		return
	}

	na := noisyAlert{
		AlertRuleKey:   alertRuleKey,
		CloudAccountID: *ev.CloudAccountId,
		TenantID:       tenantID,
		Source:         response.Source,
	}

	// Use the event's actual fingerprint for accurate event querying in the UI
	fingerprint := alertRuleKey
	if ev.Fingerprint != nil && *ev.Fingerprint != "" {
		fingerprint = *ev.Fingerprint
	}

	eventAggregationKey := ""
	if ev.AggregationKey != nil {
		eventAggregationKey = *ev.AggregationKey
	}

	_ = upsertSuggestion(ctx, db, na, fingerprint, eventAggregationKey, response)
}

// upsertSuggestion writes a suggestion to the event_threshold_suggestions table.
// eventAggregationKey is the events.aggregation_key value of the representative
// event — stored so the UI can list matching events without re-deriving the
// per-source identity mapping.
func upsertSuggestion(ctx context.Context, db *sqlx.DB, na noisyAlert, fingerprint, eventAggregationKey string, response ThresholdSuggestionResponse) error {
	var alertName, metricName, metricNamespace, operator, aggregation string
	var currentThreshold float64

	if response.AlertDefinition != nil {
		alertName = response.AlertDefinition.AlarmName
		metricName = response.AlertDefinition.MetricName
		metricNamespace = response.AlertDefinition.MetricNamespace
		operator = response.AlertDefinition.Operator
		aggregation = response.AlertDefinition.Aggregation
		currentThreshold = response.AlertDefinition.CurrentThreshold
	}
	// Fall back to alert_rule_key if alert name wasn't extracted
	if alertName == "" {
		alertName = na.AlertRuleKey
	}

	// Serialize firing analysis to JSONB
	var firingAnalysisJSON []byte
	if response.FiringAnalysis != nil {
		analysisToMarshal := *response.FiringAnalysis
		if analysisToMarshal.MetricValuesAtFiring != nil {
			sanitized := make([]float64, len(analysisToMarshal.MetricValuesAtFiring))
			for i, v := range analysisToMarshal.MetricValuesAtFiring {
				sanitized[i] = sanitizeFloat(v)
			}
			analysisToMarshal.MetricValuesAtFiring = sanitized
		}
		var err error
		firingAnalysisJSON, err = json.Marshal(&analysisToMarshal)
		if err != nil {
			return fmt.Errorf("failed to marshal firing analysis: %w", err)
		}
	}

	// Serialize metric stats to JSONB (includes MAD-based stats, duration recommendation, and risk assessment)
	var metricStatsJSON []byte
	if response.Suggestion != nil {
		stats := map[string]interface{}{
			"p50":    sanitizeFloat(response.Suggestion.MetricP50),
			"p90":    sanitizeFloat(response.Suggestion.MetricP90),
			"p95":    sanitizeFloat(response.Suggestion.MetricP95),
			"p99":    sanitizeFloat(response.Suggestion.MetricP99),
			"median": sanitizeFloat(response.Suggestion.MetricMedian),
			"mad":    sanitizeFloat(response.Suggestion.MetricMAD),
		}
		if response.Suggestion.RecommendationType != "" {
			stats["recommendation_type"] = response.Suggestion.RecommendationType
		}
		if response.Suggestion.SuggestedDuration > 0 {
			stats["suggested_duration"] = response.Suggestion.SuggestedDuration
		}
		if response.Suggestion.DurationReason != "" {
			stats["duration_reason"] = response.Suggestion.DurationReason
		}
		if response.Suggestion.RiskLevel != "" {
			stats["risk_level"] = response.Suggestion.RiskLevel
		}
		if len(response.Suggestion.RiskWarnings) > 0 {
			stats["risk_warnings"] = response.Suggestion.RiskWarnings
		}
		var err error
		metricStatsJSON, err = json.Marshal(stats)
		if err != nil {
			return fmt.Errorf("failed to marshal metric stats: %w", err)
		}
	}

	// Serialize alert quality to JSONB
	var alertQualityJSON []byte
	if response.AlertQuality != nil {
		var err error
		alertQualityJSON, err = json.Marshal(response.AlertQuality)
		if err != nil {
			return fmt.Errorf("failed to marshal alert quality: %w", err)
		}
	}

	var method string
	if response.Suggestion != nil {
		method = response.Suggestion.Method
	}

	// Serialize query metadata to JSONB
	var queryMetadataJSON []byte
	if response.QueryMetadata != nil {
		var err error
		queryMetadataJSON, err = json.Marshal(response.QueryMetadata)
		if err != nil {
			return fmt.Errorf("failed to marshal query metadata: %w", err)
		}
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO event_threshold_suggestions
		  (alert_rule_key, fingerprint, cloud_account_id, tenant_id, source, status, alert_name, metric_name, metric_namespace,
		   current_threshold, operator, aggregation, suggested_threshold, reason, confidence,
		   estimated_reduction, firing_analysis, metric_stats, alert_quality, method, query_metadata,
		   event_aggregation_key, computed_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'ok', $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, NULLIF($21, ''), NOW(), NOW())
		ON CONFLICT (alert_rule_key, cloud_account_id)
		DO UPDATE SET
		  status = 'ok',
		  fingerprint = EXCLUDED.fingerprint,
		  alert_name = EXCLUDED.alert_name,
		  metric_name = EXCLUDED.metric_name,
		  metric_namespace = EXCLUDED.metric_namespace,
		  suggested_threshold = EXCLUDED.suggested_threshold,
		  reason = EXCLUDED.reason,
		  confidence = EXCLUDED.confidence,
		  estimated_reduction = EXCLUDED.estimated_reduction,
		  firing_analysis = EXCLUDED.firing_analysis,
		  metric_stats = EXCLUDED.metric_stats,
		  alert_quality = EXCLUDED.alert_quality,
		  method = EXCLUDED.method,
		  query_metadata = EXCLUDED.query_metadata,
		  current_threshold = EXCLUDED.current_threshold,
		  operator = EXCLUDED.operator,
		  event_aggregation_key = COALESCE(EXCLUDED.event_aggregation_key, event_threshold_suggestions.event_aggregation_key),
		  computed_at = NOW(),
		  updated_at = NOW()
	`,
		na.AlertRuleKey,
		fingerprint,
		na.CloudAccountID,
		na.TenantID,
		na.Source,
		alertName,
		metricName,
		metricNamespace,
		sanitizeFloat(currentThreshold),
		operator,
		aggregation,
		sanitizeFloat(response.Suggestion.SuggestedThreshold),
		response.Suggestion.Reason,
		response.Suggestion.Confidence,
		sanitizeFloat(response.Suggestion.EstimatedReduction),
		firingAnalysisJSON,
		metricStatsJSON,
		alertQualityJSON,
		method,
		queryMetadataJSON,
		eventAggregationKey,
	)
	return err
}

// upsertSkippedSuggestion records that an alert was processed but no suggestion was stored.
// alertName is optional — pass "" to fall back to alert_rule_key.
// IMPORTANT: Never overwrite a status='ok' row with 'skipped' — a previous successful computation
// should be preserved even if a subsequent metric query returns no data (transient failure).
// Only overwrite 'ok' rows if they're stale (older than 7 days).
func upsertSkippedSuggestion(ctx context.Context, db *sqlx.DB, na noisyAlert, status, reason, alertName string) {
	if alertName == "" {
		alertName = na.AlertRuleKey
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO event_threshold_suggestions
		  (alert_rule_key, fingerprint, cloud_account_id, tenant_id, source, status, reason, alert_name, computed_at, updated_at)
		VALUES ($1, $1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		ON CONFLICT (alert_rule_key, cloud_account_id)
		DO UPDATE SET
		  status = EXCLUDED.status,
		  reason = EXCLUDED.reason,
		  alert_name = COALESCE(NULLIF(EXCLUDED.alert_name, ''), event_threshold_suggestions.alert_name),
		  computed_at = NOW(),
		  updated_at = NOW()
		WHERE event_threshold_suggestions.status != 'ok'
		   OR event_threshold_suggestions.computed_at < NOW() - INTERVAL '7 days'
	`, na.AlertRuleKey, na.CloudAccountID, na.TenantID, na.Source, status, reason, alertName); err != nil {
		slog.WarnContext(ctx, "threshold_suggestion_batch: failed to upsert skipped suggestion", "error", err, "alert_rule_key", na.AlertRuleKey)
	}
}

// ThresholdSuggestionListItem represents a row in the listing view.
type ThresholdSuggestionListItem struct {
	ID                  string          `json:"id" db:"-"`
	AlertRuleKey        string          `json:"alert_rule_key" db:"alert_rule_key"`
	Fingerprint         string          `json:"fingerprint" db:"fingerprint"`
	CloudAccountID      string          `json:"cloud_account_id" db:"cloud_account_id"`
	Source              string          `json:"source" db:"source"`
	AlertName           *string         `json:"alert_name" db:"alert_name"`
	MetricName          *string         `json:"metric_name" db:"metric_name"`
	MetricNamespace     *string         `json:"metric_namespace" db:"metric_namespace"`
	CurrentThreshold    *float64        `json:"current_threshold" db:"current_threshold"`
	Operator            *string         `json:"operator" db:"operator"`
	SuggestedThreshold  *float64        `json:"suggested_threshold" db:"suggested_threshold"`
	Reason              *string         `json:"reason" db:"reason"`
	Confidence          *string         `json:"confidence" db:"confidence"`
	EstimatedReduction  *float64        `json:"estimated_reduction" db:"estimated_reduction"`
	Method              *string         `json:"method" db:"method"`
	RecommendationType  *string         `json:"recommendation_type" db:"-"`
	ComputedAt          *string         `json:"computed_at" db:"computed_at"`
	EventAggregationKey *string         `json:"event_aggregation_key" db:"event_aggregation_key"`
	FiringAnalysis      json.RawMessage `json:"firing_analysis,omitempty" db:"firing_analysis"`
	AlertQuality        json.RawMessage `json:"alert_quality,omitempty" db:"alert_quality"`
	MetricStats         json.RawMessage `json:"metric_stats,omitempty" db:"metric_stats"`
	QueryMetadata       json.RawMessage `json:"query_metadata,omitempty" db:"query_metadata"`
}

// ThresholdSuggestionListResponse is the response for the list action.
type ThresholdSuggestionListResponse struct {
	Suggestions []ThresholdSuggestionListItem `json:"suggestions"`
	Total       int                           `json:"total"`
}

// ListThresholdSuggestions queries all cached suggestions for a tenant with optional filters.
func ListThresholdSuggestions(ctx context.Context, db *sqlx.DB, tenantID string, cloudAccountID, source, confidence string, limit, offset int) (*ThresholdSuggestionListResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	where := "WHERE tenant_id = $1 AND status = 'ok'"
	args := []interface{}{tenantID}
	argIdx := 2

	if cloudAccountID != "" {
		where += fmt.Sprintf(" AND cloud_account_id = $%d", argIdx)
		args = append(args, cloudAccountID)
		argIdx++
	}
	if source != "" {
		where += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, source)
		argIdx++
	}
	if confidence != "" {
		where += fmt.Sprintf(" AND confidence = $%d", argIdx)
		args = append(args, confidence)
		argIdx++
	}

	// Count total
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM event_threshold_suggestions %s", where)
	if err := db.GetContext(ctx, &total, countQuery, args...); err != nil {
		return nil, fmt.Errorf("count threshold suggestions: %w", err)
	}

	// Fetch rows
	query := fmt.Sprintf(`
		SELECT alert_rule_key, fingerprint, cloud_account_id, source,
		       alert_name, metric_name, metric_namespace, current_threshold,
		       operator, suggested_threshold, reason, confidence,
		       estimated_reduction, method, computed_at, event_aggregation_key,
		       COALESCE(firing_analysis, 'null') AS firing_analysis,
		       COALESCE(alert_quality, 'null') AS alert_quality,
		       COALESCE(metric_stats, 'null') AS metric_stats,
		       COALESCE(query_metadata, 'null') AS query_metadata
		FROM event_threshold_suggestions
		%s
		ORDER BY
			-- Priority: high confidence first, then medium, then low
			CASE confidence
				WHEN 'high' THEN 1
				WHEN 'medium' THEN 2
				WHEN 'low' THEN 3
				ELSE 4
			END,
			-- Within same confidence: actionable types first
			CASE metric_stats->>'recommendation_type'
				WHEN 'tune_threshold' THEN 1
				WHEN 'tune_both' THEN 2
				WHEN 'increase_duration' THEN 3
				WHEN 'disable' THEN 4
				WHEN 'none' THEN 5
				WHEN 'review_alert' THEN 6
				WHEN 'not_eligible' THEN 7
				ELSE 8
			END,
			-- Within same type: prefer moderate reductions over extreme ones
			CASE
				WHEN estimated_reduction BETWEEN 20 AND 90 THEN 1
				WHEN estimated_reduction > 90 THEN 2
				WHEN estimated_reduction BETWEEN 1 AND 19 THEN 3
				ELSE 4
			END,
			computed_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	var items []ThresholdSuggestionListItem
	if err := db.SelectContext(ctx, &items, query, args...); err != nil {
		return nil, fmt.Errorf("list threshold suggestions: %w", err)
	}

	if items == nil {
		items = []ThresholdSuggestionListItem{}
	}

	// Populate derived fields that use db:"-" (not directly mapped by sqlx)
	for i := range items {
		items[i].ID = items[i].AlertRuleKey

		// Extract recommendation_type from metric_stats JSONB
		if len(items[i].MetricStats) > 0 {
			var stats map[string]interface{}
			if err := json.Unmarshal(items[i].MetricStats, &stats); err == nil {
				if rt, ok := stats["recommendation_type"].(string); ok {
					items[i].RecommendationType = &rt
				}
			}
		}
	}

	return &ThresholdSuggestionListResponse{
		Suggestions: items,
		Total:       total,
	}, nil
}
