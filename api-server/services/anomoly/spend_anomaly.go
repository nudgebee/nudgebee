package anomoly

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strconv"
	"time"
)

// SpendAnomalyTargetDate overrides the detection date. Nil = yesterday. Used for testing.
var SpendAnomalyTargetDate *time.Time

// spendAnomalyConfig holds resolved thresholds (global defaults + tenant overrides).
type spendAnomalyConfig struct {
	BaselineDays               int
	ZScoreThreshold            float64
	MinAbsChange               float64
	MinPctChange               float64
	MinBaselineSpend           float64
	CooldownDays               int
	ResolutionStddevMultiplier float64
	ResolutionConsecutiveDays  int
	TargetDate                 *time.Time // nil = yesterday (CURRENT_DATE - 1 day). Set for testing.
}

func getSpendAnomalyConfig(ctx *security.RequestContext, tenantId string) spendAnomalyConfig {
	cfg := spendAnomalyConfig{
		BaselineDays:               30,
		ZScoreThreshold:            3.0,
		MinAbsChange:               50.0,
		MinPctChange:               20.0,
		MinBaselineSpend:           10.0,
		CooldownDays:               3,
		ResolutionStddevMultiplier: 1.0,
		ResolutionConsecutiveDays:  2,
	}

	// Override from global config if set (non-zero)
	if config.Config.NBSpendAnomalyBaselineDays > 0 {
		cfg.BaselineDays = config.Config.NBSpendAnomalyBaselineDays
	}
	if config.Config.NBSpendAnomalyZScoreThreshold > 0 {
		cfg.ZScoreThreshold = config.Config.NBSpendAnomalyZScoreThreshold
	}
	if config.Config.NBSpendAnomalyMinAbsChange > 0 {
		cfg.MinAbsChange = config.Config.NBSpendAnomalyMinAbsChange
	}
	if config.Config.NBSpendAnomalyMinPctChange > 0 {
		cfg.MinPctChange = config.Config.NBSpendAnomalyMinPctChange
	}
	if config.Config.NBSpendAnomalyMinBaselineSpend > 0 {
		cfg.MinBaselineSpend = config.Config.NBSpendAnomalyMinBaselineSpend
	}
	if config.Config.NBSpendAnomalyCooldownDays > 0 {
		cfg.CooldownDays = config.Config.NBSpendAnomalyCooldownDays
	}
	if config.Config.NBSpendAnomalyResolutionStddevMultiplier > 0 {
		cfg.ResolutionStddevMultiplier = config.Config.NBSpendAnomalyResolutionStddevMultiplier
	}
	if config.Config.NBSpendAnomalyResolutionConsecutiveDays > 0 {
		cfg.ResolutionConsecutiveDays = config.Config.NBSpendAnomalyResolutionConsecutiveDays
	}

	// Override from tenant_attrs if set — single batch query instead of 8 individual queries.
	attrNames := []string{
		"spend_anomaly_baseline_days",
		"spend_anomaly_zscore_threshold",
		"spend_anomaly_min_abs_change",
		"spend_anomaly_min_pct_change",
		"spend_anomaly_min_baseline_spend",
		"spend_anomaly_cooldown_days",
		"spend_anomaly_resolution_stddev_multiplier",
		"spend_anomaly_resolution_consecutive_days",
	}
	attrMap, err := tenant.GetTenantAttributesByNames(ctx, tenantId, attrNames)
	if err != nil {
		slog.Warn("spend-anomaly: failed to fetch tenant overrides, using defaults", "error", err)
	}

	// findValue returns the attribute value for this tenant, or "" if not found.
	findValue := func(name string) string {
		if a, ok := attrMap[name]; ok {
			return a.Value
		}
		return ""
	}
	overrideFloat := func(name string, target *float64) {
		if v, parseErr := strconv.ParseFloat(findValue(name), 64); parseErr == nil && v > 0 {
			*target = v
		}
	}
	overrideInt := func(name string, target *int) {
		if v, parseErr := strconv.Atoi(findValue(name)); parseErr == nil && v > 0 {
			*target = v
		}
	}

	overrideInt("spend_anomaly_baseline_days", &cfg.BaselineDays)
	overrideFloat("spend_anomaly_zscore_threshold", &cfg.ZScoreThreshold)
	overrideFloat("spend_anomaly_min_abs_change", &cfg.MinAbsChange)
	overrideFloat("spend_anomaly_min_pct_change", &cfg.MinPctChange)
	overrideFloat("spend_anomaly_min_baseline_spend", &cfg.MinBaselineSpend)
	overrideInt("spend_anomaly_cooldown_days", &cfg.CooldownDays)
	overrideFloat("spend_anomaly_resolution_stddev_multiplier", &cfg.ResolutionStddevMultiplier)
	overrideInt("spend_anomaly_resolution_consecutive_days", &cfg.ResolutionConsecutiveDays)

	cfg.TargetDate = SpendAnomalyTargetDate

	return cfg
}

// targetDateSQL returns the SQL expression for the target date.
func (c spendAnomalyConfig) targetDateSQL() string {
	if c.TargetDate != nil {
		return fmt.Sprintf("'%s'::date", c.TargetDate.Format("2006-01-02"))
	}
	return "(CURRENT_DATE - INTERVAL '1 day')::date"
}

// currentDateSQL returns the SQL expression for "today" relative to the target.
func (c spendAnomalyConfig) currentDateSQL() string {
	if c.TargetDate != nil {
		nextDay := c.TargetDate.AddDate(0, 0, 1)
		return fmt.Sprintf("'%s'::date", nextDay.Format("2006-01-02"))
	}
	return "CURRENT_DATE::date"
}

// targetDateValue returns the actual target date as a time.Time.
func (c spendAnomalyConfig) targetDateValue() time.Time {
	if c.TargetDate != nil {
		return *c.TargetDate
	}
	return time.Now().UTC().AddDate(0, 0, -1)
}

type cloudAccountInfo struct {
	AccountID     string `db:"cloud_account_id"`
	TenantID      string `db:"tenant"`
	AccountName   string `db:"account_name"`
	CloudProvider string `db:"cloud_provider"`
}

// ExecuteSpendAnomaly detects cloud spend anomalies for all eligible accounts.
func ExecuteSpendAnomaly(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	rows, err := dbms.Db.Queryx(`
		SELECT ca.id AS cloud_account_id, ca.tenant, ca.account_name, ca.cloud_provider
		FROM cloud_accounts ca
		WHERE ca.cloud_provider IN ('AWS', 'Azure', 'GCP')
		  AND ca.status != 'DELETED'
		GROUP BY ca.tenant, ca.id, ca.account_name, ca.cloud_provider
	`)
	if err != nil {
		slog.Error("spend-anomaly: failed to query cloud accounts", "error", err)
		return err
	}
	defer func() { _ = rows.Close() }()

	var accounts []cloudAccountInfo
	for rows.Next() {
		var acc cloudAccountInfo
		if err := rows.StructScan(&acc); err != nil {
			slog.Error("spend-anomaly: failed to scan account", "error", err)
			continue
		}
		accounts = append(accounts, acc)
	}

	slog.Info("spend-anomaly: found cloud accounts", "count", len(accounts))

	for _, acc := range accounts {
		if !tenant.IsFeatureEnabled(ctx, acc.TenantID, tenant.FEATURE_ANOMALY_DETECTION) {
			continue
		}
		if err := executeSpendAnomalyForAccount(ctx, acc); err != nil {
			slog.Error("spend-anomaly: failed for account", "error", err, "account", acc.AccountName)
		}
	}

	return nil
}

// ExecuteSpendAnomalyForAccount runs spend anomaly detection for a single account.
func ExecuteSpendAnomalyForAccount(ctx *security.RequestContext, accountId string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	var acc cloudAccountInfo
	err = dbms.Db.Get(&acc, `
		SELECT ca.id AS cloud_account_id, ca.tenant, ca.account_name, ca.cloud_provider
		FROM cloud_accounts ca
		WHERE ca.id = $1
		  AND ca.cloud_provider IN ('AWS', 'Azure', 'GCP')
		  AND ca.status != 'DELETED'
	`, accountId)
	if err != nil {
		return fmt.Errorf("account %s not found or not eligible: %w", accountId, err)
	}

	return executeSpendAnomalyForAccount(ctx, acc)
}

func executeSpendAnomalyForAccount(ctx *security.RequestContext, acc cloudAccountInfo) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	cfg := getSpendAnomalyConfig(ctx, acc.TenantID)

	if !hasTargetDateData(dbms, acc.AccountID, cfg) {
		slog.Debug("spend-anomaly: no target date data, skipping", "account", acc.AccountName)
		return nil
	}

	// Phase 1: Update open anomalies
	processOpenAnomalies(ctx, dbms, acc.AccountID, acc.TenantID, acc.AccountName, acc.CloudProvider, cfg)

	// Phase 2: Resolve recovered anomalies
	resolveRecoveredAnomalies(ctx, dbms, acc.AccountID, acc.TenantID, acc.AccountName, acc.CloudProvider, cfg)

	// Phase 3: Detect new anomalies (with clean baseline)
	detectNewAccountLevelAnomalies(ctx, dbms, acc.AccountID, acc.TenantID, acc.AccountName, acc.CloudProvider, cfg)
	detectNewServiceLevelAnomalies(ctx, dbms, acc.AccountID, acc.TenantID, acc.AccountName, acc.CloudProvider, cfg)

	return nil
}

func hasTargetDateData(dbms *database.DatabaseManager, accountId string, cfg spendAnomalyConfig) bool {
	var count int
	query := fmt.Sprintf(`
		SELECT COUNT(*) FROM spends
		WHERE cloud_account = $1
		  AND date::date = %s
		  AND exclude_aggregate = false
	`, cfg.targetDateSQL())
	err := dbms.Db.Get(&count, query, accountId)
	return err == nil && count > 0
}

// =============================================================================
// Phase 1: Update Open Anomalies
// =============================================================================

func getOpenSpendAnomalies(dbms *database.DatabaseManager, accountId string) ([]OpenSpendAnomaly, error) {
	var anomalies []OpenSpendAnomaly
	err := dbms.Db.Select(&anomalies, `
		SELECT id, tenant, account_id, name, namespace, anomaly_type, anomaly_status,
		       reference_value, current_value
		FROM anomaly
		WHERE account_id = $1
		  AND anomaly_status = $2
		  AND anomaly_type IN ($3, $4)
	`, accountId, string(SpendAnomalyStatusOpen), string(MetricAnomolyTypeCloudSpendAccount), string(MetricAnomolyTypeCloudSpendService))
	if err != nil {
		return nil, err
	}
	for i := range anomalies {
		anomalies[i].ParseReferenceValue()
	}
	return anomalies, nil
}

func processOpenAnomalies(
	ctx *security.RequestContext,
	dbms *database.DatabaseManager,
	accountId, tenantId, accountName, cloudProvider string,
	cfg spendAnomalyConfig,
) {
	openAnomalies, err := getOpenSpendAnomalies(dbms, accountId)
	if err != nil {
		slog.Error("spend-anomaly: failed to get open anomalies", "error", err, "account", accountName)
		return
	}

	if len(openAnomalies) == 0 {
		return
	}

	slog.Info("spend-anomaly: processing open anomalies", "account", accountName, "count", len(openAnomalies))

	for _, oa := range openAnomalies {
		yesterdaySpend := getYesterdaySpend(dbms, accountId, oa, cfg)
		threshold := oa.FrozenMean + cfg.ResolutionStddevMultiplier*oa.FrozenStddev

		if yesterdaySpend > threshold {
			// Still elevated — update tracking
			dailyExcess := yesterdaySpend - oa.FrozenMean
			oa.AnomalyDays++
			oa.TotalImpact += dailyExcess
			if dailyExcess > oa.MaxDailyImpact {
				oa.MaxDailyImpact = dailyExcess
			}
			oa.ConsecutiveNormalDays = 0

			slog.Info("spend-anomaly: open anomaly still elevated",
				"account", accountName, "name", oa.Name, "type", oa.AnomalyType,
				"yesterday_spend", yesterdaySpend, "threshold", threshold,
				"anomaly_days", oa.AnomalyDays, "total_impact", oa.TotalImpact)
		} else {
			// Normalized — increment consecutive normal days
			oa.ConsecutiveNormalDays++
			slog.Info("spend-anomaly: open anomaly normalized for day",
				"account", accountName, "name", oa.Name, "type", oa.AnomalyType,
				"yesterday_spend", yesterdaySpend, "threshold", threshold,
				"consecutive_normal_days", oa.ConsecutiveNormalDays)
		}

		// Update reference_value with new tracking data
		if err := updateOpenAnomalyTracking(dbms, oa, yesterdaySpend); err != nil {
			slog.Error("spend-anomaly: failed to update open anomaly", "error", err, "id", oa.ID)
			continue
		}

		// Update event evidence with cumulative data
		updateOpenAnomalyEvent(dbms, oa, accountName, cloudProvider, accountId, cfg)
	}
}

func getYesterdaySpend(dbms *database.DatabaseManager, accountId string, oa OpenSpendAnomaly, cfg spendAnomalyConfig) float64 {
	var spend float64
	var query string

	if oa.AnomalyType == MetricAnomolyTypeCloudSpendAccount {
		query = fmt.Sprintf(`
			SELECT COALESCE(SUM(s.amount), 0)
			FROM spends s
			WHERE s.cloud_account = $1
			  AND s.exclude_aggregate = false
			  AND s.date::date = %s
		`, cfg.targetDateSQL())
		_ = dbms.Db.Get(&spend, query, accountId)
	} else {
		query = fmt.Sprintf(`
			SELECT COALESCE(SUM(s.amount), 0)
			FROM spends s
			INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
			WHERE s.cloud_account = $1
			  AND cr.service_name = $2
			  AND s.exclude_aggregate = false
			  AND s.date::date = %s
		`, cfg.targetDateSQL())
		_ = dbms.Db.Get(&spend, query, accountId, oa.Name)
	}
	return spend
}

func updateOpenAnomalyTracking(dbms *database.DatabaseManager, oa OpenSpendAnomaly, currentSpend float64) error {
	// Build updated reference_value JSON
	var ref map[string]any
	if err := json.Unmarshal(oa.ReferenceValueRaw, &ref); err != nil {
		ref = map[string]any{}
	}

	ref["total_impact"] = oa.TotalImpact
	ref["max_daily_impact"] = oa.MaxDailyImpact
	ref["anomaly_days"] = oa.AnomalyDays
	ref["consecutive_normal_days"] = oa.ConsecutiveNormalDays

	refJSON, err := json.Marshal(ref)
	if err != nil {
		return err
	}

	_, err = dbms.Db.Exec(`
		UPDATE anomaly
		SET reference_value = $1, current_value = $2
		WHERE id = $3
	`, string(refJSON), currentSpend, oa.ID)
	return err
}

func updateOpenAnomalyEvent(dbms *database.DatabaseManager, oa OpenSpendAnomaly, accountName, cloudProvider, accountId string, cfg spendAnomalyConfig) {
	evidences := buildSpendEvidences(dbms, &Anomaly{
		Id:           fmt.Sprintf("%d", oa.ID),
		AccountId:    oa.AccountID,
		Tenant:       oa.Tenant,
		Name:         oa.Name,
		AnomalyType:  oa.AnomalyType,
		CurrentValue: oa.CurrentValue,
		OldValue: map[string]any{
			"mean":                    oa.FrozenMean,
			"stddev":                  oa.FrozenStddev,
			"frozen_mean":             oa.FrozenMean,
			"frozen_stddev":           oa.FrozenStddev,
			"start_date":              oa.StartDate,
			"total_impact":            oa.TotalImpact,
			"max_daily_impact":        oa.MaxDailyImpact,
			"anomaly_days":            oa.AnomalyDays,
			"consecutive_normal_days": oa.ConsecutiveNormalDays,
		},
	}, accountName, cloudProvider, accountId, cfg)

	var title, description string
	if oa.AnomalyType == MetricAnomolyTypeCloudSpendAccount {
		title = fmt.Sprintf("Cloud Spend Anomaly: %s account spend elevated for %d days", accountName, oa.AnomalyDays)
		description = fmt.Sprintf("Account %s spend anomaly ongoing since %s. Total excess: $%.2f over %d days (frozen baseline: $%.2f/day)",
			accountName, oa.StartDate, oa.TotalImpact, oa.AnomalyDays, oa.FrozenMean)
	} else {
		title = fmt.Sprintf("Cloud Spend Anomaly: %s in %s elevated for %d days", oa.Name, accountName, oa.AnomalyDays)
		description = fmt.Sprintf("Service %s in %s spend anomaly ongoing since %s. Total excess: $%.2f over %d days (frozen baseline: $%.2f/day)",
			oa.Name, accountName, oa.StartDate, oa.TotalImpact, oa.AnomalyDays, oa.FrozenMean)
	}

	namespace := ""
	if oa.Namespace != nil {
		namespace = *oa.Namespace
	}

	evalTime := time.Now().UTC()
	eventObj := event.Event{
		AccountId:        oa.AccountID,
		Tenant:           oa.Tenant,
		Source:           "anomaly",
		Title:            title,
		Failure:          "true",
		FindingType:      "Anomaly",
		Category:         "CostAnomaly",
		Priority:         "HIGH",
		SubjectName:      oa.Name,
		SubjectNamespace: namespace,
		Evidences:        evidences,
		FindingId:        fmt.Sprintf("%d", oa.ID),
		AggregationKey:   "CostAnomaly",
		Description:      description,
		SubjectType:      "cloud_account",
		Status:           "FIRING",
		StartsAt:         &evalTime,
		Fingerprint:      fmt.Sprintf("spend-anomaly-%s-%s-%s", oa.AccountID, oa.AnomalyType, oa.Name),
		Cluster:          accountName,
	}

	if _, err := event.InsertEvent(eventObj, ""); err != nil {
		slog.Error("spend-anomaly: failed to update event for open anomaly", "error", err, "name", oa.Name)
	}
}

// =============================================================================
// Phase 2: Resolve Recovered Anomalies
// =============================================================================

func resolveRecoveredAnomalies(
	ctx *security.RequestContext,
	dbms *database.DatabaseManager,
	accountId, tenantId, accountName, cloudProvider string,
	cfg spendAnomalyConfig,
) {
	// Find OPEN anomalies where consecutive_normal_days >= threshold
	openAnomalies, err := getOpenSpendAnomalies(dbms, accountId)
	if err != nil {
		slog.Error("spend-anomaly: failed to get open anomalies for resolution", "error", err)
		return
	}

	for _, oa := range openAnomalies {
		if oa.ConsecutiveNormalDays < cfg.ResolutionConsecutiveDays {
			continue
		}

		slog.Info("spend-anomaly: resolving anomaly",
			"account", accountName, "name", oa.Name, "type", oa.AnomalyType,
			"anomaly_days", oa.AnomalyDays, "total_impact", oa.TotalImpact)

		// Calculate end date (target date minus consecutive normal days)
		endDate := cfg.targetDateValue().AddDate(0, 0, -oa.ConsecutiveNormalDays+1)

		// Update reference_value with end_date and mark RESOLVED
		var ref map[string]any
		if err := json.Unmarshal(oa.ReferenceValueRaw, &ref); err != nil {
			ref = map[string]any{}
		}
		ref["end_date"] = endDate.Format("2006-01-02")
		refJSON, err := json.Marshal(ref)
		if err != nil {
			slog.Error("spend-anomaly: failed to marshal reference_value for resolution", "error", err)
			continue
		}

		_, err = dbms.Db.Exec(`
			UPDATE anomaly SET anomaly_status = $1, reference_value = $2
			WHERE id = $3
		`, string(SpendAnomalyStatusResolved), string(refJSON), oa.ID)
		if err != nil {
			slog.Error("spend-anomaly: failed to resolve anomaly", "error", err, "id", oa.ID)
			continue
		}

		// Update event to RESOLVED
		resolveAnomalyEvent(dbms, oa, accountName, cloudProvider, accountId, endDate, cfg)
	}
}

func resolveAnomalyEvent(dbms *database.DatabaseManager, oa OpenSpendAnomaly, accountName, cloudProvider, accountId string, endDate time.Time, cfg spendAnomalyConfig) {
	namespace := ""
	if oa.Namespace != nil {
		namespace = *oa.Namespace
	}

	var title, description string
	if oa.AnomalyType == MetricAnomolyTypeCloudSpendAccount {
		title = fmt.Sprintf("Cloud Spend Anomaly Resolved: %s (lasted %d days, $%.2f excess)", accountName, oa.AnomalyDays, oa.TotalImpact)
		description = fmt.Sprintf("Account %s spend anomaly resolved. Duration: %s to %s (%d days). Total excess: $%.2f",
			accountName, oa.StartDate, endDate.Format("2006-01-02"), oa.AnomalyDays, oa.TotalImpact)
	} else {
		title = fmt.Sprintf("Cloud Spend Anomaly Resolved: %s in %s (lasted %d days, $%.2f excess)", oa.Name, accountName, oa.AnomalyDays, oa.TotalImpact)
		description = fmt.Sprintf("Service %s in %s spend anomaly resolved. Duration: %s to %s (%d days). Total excess: $%.2f",
			oa.Name, accountName, oa.StartDate, endDate.Format("2006-01-02"), oa.AnomalyDays, oa.TotalImpact)
	}

	evidences := buildSpendEvidences(dbms, &Anomaly{
		Id:           fmt.Sprintf("%d", oa.ID),
		AccountId:    oa.AccountID,
		Tenant:       oa.Tenant,
		Name:         oa.Name,
		AnomalyType:  oa.AnomalyType,
		CurrentValue: oa.CurrentValue,
		OldValue: map[string]any{
			"mean":             oa.FrozenMean,
			"stddev":           oa.FrozenStddev,
			"frozen_mean":      oa.FrozenMean,
			"frozen_stddev":    oa.FrozenStddev,
			"start_date":       oa.StartDate,
			"end_date":         endDate.Format("2006-01-02"),
			"total_impact":     oa.TotalImpact,
			"max_daily_impact": oa.MaxDailyImpact,
			"anomaly_days":     oa.AnomalyDays,
		},
	}, accountName, cloudProvider, accountId, cfg)

	endsAt := endDate
	evalTime := time.Now().UTC()
	eventObj := event.Event{
		AccountId:        oa.AccountID,
		Tenant:           oa.Tenant,
		Source:           "anomaly",
		Title:            title,
		Failure:          "true",
		FindingType:      "Anomaly",
		Category:         "CostAnomaly",
		Priority:         "HIGH",
		SubjectName:      oa.Name,
		SubjectNamespace: namespace,
		Evidences:        evidences,
		FindingId:        fmt.Sprintf("%d", oa.ID),
		AggregationKey:   "CostAnomaly",
		Description:      description,
		SubjectType:      "cloud_account",
		Status:           "RESOLVED",
		StartsAt:         &evalTime,
		EndsAt:           &endsAt,
		Fingerprint:      fmt.Sprintf("spend-anomaly-%s-%s-%s", oa.AccountID, oa.AnomalyType, oa.Name),
		Cluster:          accountName,
	}

	if _, err := event.InsertEvent(eventObj, ""); err != nil {
		slog.Error("spend-anomaly: failed to update event for resolved anomaly", "error", err, "name", oa.Name)
	}
}

// =============================================================================
// Phase 3: Detect New Anomalies (with clean baseline)
// =============================================================================

func hasOpenAnomaly(dbms *database.DatabaseManager, accountId string, anomalyType AnomalyType, name string) bool {
	var count int
	err := dbms.Db.Get(&count, `
		SELECT COUNT(*) FROM anomaly
		WHERE account_id = $1
		  AND anomaly_type = $2
		  AND name = $3
		  AND anomaly_status = $4
	`, accountId, anomalyType, name, string(SpendAnomalyStatusOpen))
	return err == nil && count > 0
}

func detectNewAccountLevelAnomalies(
	ctx *security.RequestContext,
	dbms *database.DatabaseManager,
	accountId, tenantId, accountName, cloudProvider string,
	cfg spendAnomalyConfig,
) {
	// Skip if already have an OPEN account-level anomaly
	if hasOpenAnomaly(dbms, accountId, MetricAnomolyTypeCloudSpendAccount, accountName) {
		slog.Debug("spend-anomaly: open account-level anomaly exists, skipping detection", "account", accountName)
		return
	}

	targetDate := cfg.targetDateSQL()
	currentDate := cfg.currentDateSQL()

	// Clean baseline: exclude OPEN anomaly date ranges
	query := fmt.Sprintf(`
		WITH open_anomaly_dates AS (
			SELECT generate_series(
				(reference_value->>'start_date')::date,
				%s,
				'1 day'::interval
			)::date AS excluded_date
			FROM anomaly
			WHERE account_id = $1 AND anomaly_status = 'OPEN'
			  AND anomaly_type IN ('CloudSpendAccount', 'CloudSpendService')
			  AND reference_value->>'start_date' IS NOT NULL
		),
		daily_spend AS (
			SELECT s.cloud_account, s.date::date AS spend_date, SUM(s.amount) AS daily_amount
			FROM spends s
			WHERE s.tenant = $2 AND s.cloud_account = $1
			  AND s.exclude_aggregate = false
			  AND s.date >= (%s - INTERVAL '%d days')
			  AND s.date < %s
			  AND s.date::date NOT IN (SELECT excluded_date FROM open_anomaly_dates)
			GROUP BY s.cloud_account, s.date::date
		),
		baseline AS (
			SELECT cloud_account,
				AVG(daily_amount) AS mean_spend,
				STDDEV_SAMP(daily_amount) AS stddev_spend,
				COUNT(*) AS baseline_days
			FROM daily_spend
			WHERE spend_date < %s
			GROUP BY cloud_account
		),
		yesterday AS (
			SELECT s.cloud_account, SUM(s.amount) AS current_spend
			FROM spends s
			WHERE s.tenant = $2 AND s.cloud_account = $1
			  AND s.exclude_aggregate = false
			  AND s.date::date = %s
			GROUP BY s.cloud_account
		)
		SELECT y.cloud_account,
			y.current_spend,
			b.mean_spend,
			b.stddev_spend,
			b.baseline_days,
			CASE WHEN b.stddev_spend > 0 THEN (y.current_spend - b.mean_spend) / b.stddev_spend ELSE 0 END AS z_score,
			CASE WHEN b.mean_spend > 0 THEN ((y.current_spend - b.mean_spend) / b.mean_spend) * 100 ELSE 0 END AS pct_change,
			(y.current_spend - b.mean_spend) AS abs_change
		FROM yesterday y
		INNER JOIN baseline b ON y.cloud_account = b.cloud_account
		WHERE b.baseline_days >= 14
		  AND b.mean_spend >= $3
		  AND b.stddev_spend > 0
	`, targetDate, currentDate, cfg.BaselineDays+1, currentDate, targetDate, targetDate)

	var results []SpendAnomalyResult
	err := dbms.Db.Select(&results, query, accountId, tenantId, cfg.MinBaselineSpend)
	if err != nil {
		slog.Error("spend-anomaly: account-level detection query failed", "error", err, "account", accountName)
		return
	}

	for _, r := range results {
		if !isSpendAnomaly(r, cfg) {
			continue
		}

		slog.Info("spend-anomaly: new account-level anomaly detected",
			"account", accountName, "current", r.CurrentSpend, "mean", r.MeanSpend,
			"z_score", r.ZScore, "pct_change", r.PctChange)

		insertNewSpendAnomaly(dbms, r, accountId, tenantId, accountName, cloudProvider, MetricAnomolyTypeCloudSpendAccount, accountName, "", cfg)
	}
}

func detectNewServiceLevelAnomalies(
	ctx *security.RequestContext,
	dbms *database.DatabaseManager,
	accountId, tenantId, accountName, cloudProvider string,
	cfg spendAnomalyConfig,
) {
	targetDate := cfg.targetDateSQL()
	currentDate := cfg.currentDateSQL()

	// Clean baseline: exclude OPEN anomaly date ranges
	query := fmt.Sprintf(`
		WITH open_anomaly_dates AS (
			SELECT generate_series(
				(reference_value->>'start_date')::date,
				%s,
				'1 day'::interval
			)::date AS excluded_date
			FROM anomaly
			WHERE account_id = $1 AND anomaly_status = 'OPEN'
			  AND anomaly_type IN ('CloudSpendAccount', 'CloudSpendService')
			  AND reference_value->>'start_date' IS NOT NULL
		),
		daily_service_spend AS (
			SELECT s.cloud_account, cr.service_name, s.date::date AS spend_date, SUM(s.amount) AS daily_amount
			FROM spends s
			INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
			WHERE s.tenant = $2 AND s.cloud_account = $1
			  AND s.exclude_aggregate = false
			  AND cr.service_name IS NOT NULL AND cr.service_name != ''
			  AND s.date >= (%s - INTERVAL '%d days')
			  AND s.date < %s
			  AND s.date::date NOT IN (SELECT excluded_date FROM open_anomaly_dates)
			GROUP BY s.cloud_account, cr.service_name, s.date::date
		),
		baseline AS (
			SELECT cloud_account, service_name,
				AVG(daily_amount) AS mean_spend,
				STDDEV_SAMP(daily_amount) AS stddev_spend,
				COUNT(*) AS baseline_days
			FROM daily_service_spend
			WHERE spend_date < %s
			GROUP BY cloud_account, service_name
		),
		yesterday AS (
			SELECT s.cloud_account, cr.service_name, SUM(s.amount) AS current_spend
			FROM spends s
			INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
			WHERE s.tenant = $2 AND s.cloud_account = $1
			  AND s.exclude_aggregate = false
			  AND cr.service_name IS NOT NULL AND cr.service_name != ''
			  AND s.date::date = %s
			GROUP BY s.cloud_account, cr.service_name
		)
		SELECT y.cloud_account,
			y.service_name,
			y.current_spend,
			b.mean_spend,
			b.stddev_spend,
			b.baseline_days,
			CASE WHEN b.stddev_spend > 0 THEN (y.current_spend - b.mean_spend) / b.stddev_spend ELSE 0 END AS z_score,
			CASE WHEN b.mean_spend > 0 THEN ((y.current_spend - b.mean_spend) / b.mean_spend) * 100 ELSE 0 END AS pct_change,
			(y.current_spend - b.mean_spend) AS abs_change
		FROM yesterday y
		INNER JOIN baseline b ON y.cloud_account = b.cloud_account AND y.service_name = b.service_name
		WHERE b.baseline_days >= 14
		  AND b.mean_spend >= $3
		  AND b.stddev_spend > 0
	`, targetDate, currentDate, cfg.BaselineDays+1, currentDate, targetDate, targetDate)

	var results []SpendAnomalyResult
	err := dbms.Db.Select(&results, query, accountId, tenantId, cfg.MinBaselineSpend)
	if err != nil {
		slog.Error("spend-anomaly: service-level detection query failed", "error", err, "account", accountName)
		return
	}

	for _, r := range results {
		if !isSpendAnomaly(r, cfg) {
			continue
		}

		if hasOpenAnomaly(dbms, accountId, MetricAnomolyTypeCloudSpendService, r.ServiceName) {
			slog.Debug("spend-anomaly: open service-level anomaly exists, skipping",
				"account", accountName, "service", r.ServiceName)
			continue
		}

		slog.Info("spend-anomaly: new service-level anomaly detected",
			"account", accountName, "service", r.ServiceName,
			"current", r.CurrentSpend, "mean", r.MeanSpend,
			"z_score", r.ZScore, "pct_change", r.PctChange)

		insertNewSpendAnomaly(dbms, r, accountId, tenantId, accountName, cloudProvider, MetricAnomolyTypeCloudSpendService, r.ServiceName, accountName, cfg)
	}
}

func isSpendAnomaly(r SpendAnomalyResult, cfg spendAnomalyConfig) bool {
	if r.AbsChange <= 0 {
		return false
	}
	return r.ZScore >= cfg.ZScoreThreshold &&
		r.AbsChange >= cfg.MinAbsChange &&
		r.PctChange >= cfg.MinPctChange
}

// =============================================================================
// Insert new anomaly (Phase 3)
// =============================================================================

func insertNewSpendAnomaly(
	dbms *database.DatabaseManager,
	r SpendAnomalyResult,
	accountId, tenantId, accountName, cloudProvider string,
	anomalyType AnomalyType,
	name, namespace string,
	cfg spendAnomalyConfig,
) {
	evalTime := time.Now().UTC()
	targetDate := cfg.targetDateValue()
	absChange := r.CurrentSpend - r.MeanSpend

	// Build reference_value with frozen baseline + tracking fields
	refValue := map[string]any{
		"mean":                    r.MeanSpend,
		"stddev":                  r.StddevSpend,
		"z_score":                 r.ZScore,
		"pct_change":              r.PctChange,
		"baseline_days":           r.BaselineDays,
		"frozen_mean":             r.MeanSpend,
		"frozen_stddev":           r.StddevSpend,
		"anomaly_status":          string(SpendAnomalyStatusOpen),
		"start_date":              targetDate.Format("2006-01-02"),
		"total_impact":            absChange,
		"max_daily_impact":        absChange,
		"anomaly_days":            1,
		"consecutive_normal_days": 0,
	}
	if r.ServiceName != "" {
		refValue["service_name"] = r.ServiceName
	}

	anomalyId := common.GenerateUUID()

	anomaly := Anomaly{
		Id:           anomalyId,
		AccountId:    accountId,
		Tenant:       tenantId,
		Name:         name,
		Namespace:    namespace,
		OldValue:     refValue,
		CurrentValue: r.CurrentSpend,
		AnomalyType:  anomalyType,
		IsAnomaly:    true,
		EvaluatedAt:  &evalTime,
	}

	// Generate event first
	if err := generateSpendAnomalyEvent(dbms, &anomaly, accountName, cloudProvider, accountId, cfg); err != nil {
		slog.Error("spend-anomaly: failed to generate event", "error", err, "account", accountName, "name", name)
	}

	// Insert anomaly record with status
	oldValueStr, _ := common.MarshalJson(anomaly.OldValue)

	var namespacePtr *string
	if namespace != "" {
		namespacePtr = &namespace
	}

	query := `INSERT INTO anomaly (id, account_id, tenant, name, namespace, reference_value, current_value, anomaly_type, is_anomaly, evaluated_at, pod_name, training_end_time, anomaly_status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, '', NULL, $11)`

	_, err := dbms.Db.Exec(query,
		anomaly.Id, anomaly.AccountId, anomaly.Tenant, anomaly.Name, namespacePtr,
		string(oldValueStr), anomaly.CurrentValue, anomaly.AnomalyType, anomaly.IsAnomaly,
		anomaly.EvaluatedAt.Format(dateTimeFormat),
		string(SpendAnomalyStatusOpen),
	)
	if err != nil {
		slog.Error("spend-anomaly: failed to insert anomaly", "error", err, "account", accountName, "name", name)
	}
}

func generateSpendAnomalyEvent(dbms *database.DatabaseManager, anomaly *Anomaly, cloudAccountName, cloudProvider, accountId string, cfg spendAnomalyConfig) error {
	evidences := buildSpendEvidences(dbms, anomaly, cloudAccountName, cloudProvider, accountId, cfg)

	var title, description string
	if anomaly.AnomalyType == MetricAnomolyTypeCloudSpendAccount {
		title = fmt.Sprintf("Cloud Spend Anomaly: %s account spend increased by %.1f%%", cloudAccountName, anomaly.OldValue["pct_change"])
		description = fmt.Sprintf("Daily spend of $%.2f for account %s exceeds baseline average of $%.2f (z-score: %.2f)",
			anomaly.CurrentValue, cloudAccountName, anomaly.OldValue["mean"], anomaly.OldValue["z_score"])
	} else {
		title = fmt.Sprintf("Cloud Spend Anomaly: %s in %s increased by %.1f%%", anomaly.Name, cloudAccountName, anomaly.OldValue["pct_change"])
		description = fmt.Sprintf("Daily spend of $%.2f for %s in account %s exceeds baseline average of $%.2f (z-score: %.2f)",
			anomaly.CurrentValue, anomaly.Name, cloudAccountName, anomaly.OldValue["mean"], anomaly.OldValue["z_score"])
	}

	eventObj := event.Event{
		AccountId:        anomaly.AccountId,
		Tenant:           anomaly.Tenant,
		Source:           "anomaly",
		Title:            title,
		Failure:          "true",
		FindingType:      "Anomaly",
		Category:         "CostAnomaly",
		Priority:         "HIGH",
		SubjectName:      anomaly.Name,
		SubjectNamespace: anomaly.Namespace,
		Evidences:        evidences,
		FindingId:        anomaly.Id,
		AggregationKey:   "CostAnomaly",
		Description:      description,
		SubjectType:      "cloud_account",
		Status:           "FIRING",
		StartsAt:         anomaly.EvaluatedAt,
		Fingerprint:      fmt.Sprintf("spend-anomaly-%s-%s-%s", anomaly.AccountId, anomaly.AnomalyType, anomaly.Name),
		Cluster:          cloudAccountName,
	}

	_, err := event.InsertEvent(eventObj, "")
	if err != nil {
		slog.Error("spend-anomaly: error inserting event", "error", err,
			"account", cloudAccountName, "name", anomaly.Name)
	}
	return err
}

// =============================================================================
// Evidence builders (shared across all phases)
// =============================================================================

func buildSpendEvidences(dbms *database.DatabaseManager, anomaly *Anomaly, cloudAccountName, cloudProvider, accountId string, cfg spendAnomalyConfig) []any {
	var evidences []any

	// Evidence 1: Anomaly summary
	mean, _ := anomaly.OldValue["mean"].(float64)
	stddev, _ := anomaly.OldValue["stddev"].(float64)
	zScore, _ := anomaly.OldValue["z_score"].(float64)
	pctChange, _ := anomaly.OldValue["pct_change"].(float64)
	absChange := anomaly.CurrentValue - mean

	summaryRows := []any{
		[]any{"Current Daily Spend", fmt.Sprintf("$%.2f", anomaly.CurrentValue)},
		[]any{"Expected Daily Spend", fmt.Sprintf("$%.2f", mean)},
		[]any{"Standard Deviation", fmt.Sprintf("$%.2f", stddev)},
		[]any{"Change", fmt.Sprintf("+$%.2f (+%.1f%%)", absChange, pctChange)},
		[]any{"Z-Score", fmt.Sprintf("%.2f", zScore)},
		[]any{"Cloud Provider", cloudProvider},
	}

	// Add cumulative fields for multi-day anomalies
	if anomalyDays, ok := anomaly.OldValue["anomaly_days"]; ok {
		days := 0
		switch v := anomalyDays.(type) {
		case float64:
			days = int(v)
		case int:
			days = v
		}
		if days > 1 {
			startDate, _ := anomaly.OldValue["start_date"].(string)
			totalImpact, _ := anomaly.OldValue["total_impact"].(float64)
			maxDailyImpact, _ := anomaly.OldValue["max_daily_impact"].(float64)

			summaryRows = append(summaryRows,
				[]any{"Duration", fmt.Sprintf("%d days (since %s)", days, startDate)},
				[]any{"Total Excess Impact", fmt.Sprintf("$%.2f", totalImpact)},
				[]any{"Max Daily Impact", fmt.Sprintf("$%.2f", maxDailyImpact)},
			)

			if endDate, ok := anomaly.OldValue["end_date"].(string); ok && endDate != "" {
				summaryRows = append(summaryRows, []any{"Resolved On", endDate})
			}
		}
	}

	insightMsg := fmt.Sprintf("Daily spend of $%.2f exceeds baseline average of $%.2f by %.1f%%",
		anomaly.CurrentValue, mean, pctChange)

	evidences = append(evidences, map[string]any{
		"type": "table",
		"data": map[string]any{
			"table_name": "Spend Anomaly Summary",
			"headers":    []string{"Metric", "Value"},
			"rows":       summaryRows,
		},
		"additional_info": map[string]any{
			"title": "Spend Anomaly Details",
		},
		"insight": []map[string]any{
			{"message": insightMsg, "severity": "HIGH"},
		},
	})

	// Evidence 2: Root cause breakdown
	rootCauseEvidence := buildRootCauseEvidence(dbms, anomaly, accountId, cfg)
	if rootCauseEvidence != nil {
		evidences = append(evidences, rootCauseEvidence)
	}

	// Evidence 3: Spend trend (last 30 days)
	trendEvidence := buildSpendTrendEvidence(dbms, anomaly, accountId, cfg)
	if trendEvidence != nil {
		evidences = append(evidences, trendEvidence)
	}

	return evidences
}

func buildRootCauseEvidence(dbms *database.DatabaseManager, anomaly *Anomaly, accountId string, cfg spendAnomalyConfig) map[string]any {
	var query string
	var tableName, title string
	targetDate := cfg.targetDateSQL()

	if anomaly.AnomalyType == MetricAnomolyTypeCloudSpendAccount {
		tableName = "Top Contributors to Spend Change"
		title = "Service Breakdown"
		query = fmt.Sprintf(`
			WITH yesterday_by_service AS (
				SELECT cr.service_name, SUM(s.amount) AS yesterday_amount
				FROM spends s
				INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
				WHERE s.cloud_account = $1
				  AND s.date::date = %s
				  AND s.exclude_aggregate = false
				  AND cr.service_name IS NOT NULL AND cr.service_name != ''
				GROUP BY cr.service_name
			),
			baseline_by_service AS (
				SELECT service_name, AVG(daily_total) AS avg_daily_amount
				FROM (
					SELECT cr.service_name, s.date::date, SUM(s.amount) AS daily_total
					FROM spends s
					INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
					WHERE s.cloud_account = $1
					  AND s.date::date >= (%s - INTERVAL '%d days')
					  AND s.date::date < %s
					  AND s.exclude_aggregate = false
					  AND cr.service_name IS NOT NULL AND cr.service_name != ''
					GROUP BY cr.service_name, s.date::date
				) sub GROUP BY service_name
			)
			SELECT y.service_name AS name,
				ROUND(y.yesterday_amount::numeric, 2) AS yesterday,
				ROUND(COALESCE(b.avg_daily_amount, 0)::numeric, 2) AS avg,
				ROUND((y.yesterday_amount - COALESCE(b.avg_daily_amount, 0))::numeric, 2) AS change,
				CASE WHEN b.avg_daily_amount IS NULL THEN 'NEW' ELSE '' END AS is_new
			FROM yesterday_by_service y
			LEFT JOIN baseline_by_service b ON y.service_name = b.service_name
			ORDER BY (y.yesterday_amount - COALESCE(b.avg_daily_amount, 0)) DESC
			LIMIT 10
		`, targetDate, targetDate, cfg.BaselineDays, targetDate)
	} else {
		tableName = "Top Resources Contributing to Change"
		title = "Resource Breakdown"
		query = fmt.Sprintf(`
			WITH yesterday_by_resource AS (
				SELECT cr.name AS resource_name, SUM(s.amount) AS yesterday_amount
				FROM spends s
				INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
				WHERE s.cloud_account = $1
				  AND cr.service_name = $2
				  AND s.date::date = %s
				  AND s.exclude_aggregate = false
				GROUP BY cr.name
			),
			baseline_by_resource AS (
				SELECT cr.name AS resource_name, AVG(s.amount) AS avg_daily_amount
				FROM spends s
				INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
				WHERE s.cloud_account = $1
				  AND cr.service_name = $2
				  AND s.date::date >= (%s - INTERVAL '%d days')
				  AND s.date::date < %s
				  AND s.exclude_aggregate = false
				GROUP BY cr.name
			)
			SELECT y.resource_name AS name,
				ROUND(y.yesterday_amount::numeric, 2) AS yesterday,
				ROUND(COALESCE(b.avg_daily_amount, 0)::numeric, 2) AS avg,
				ROUND((y.yesterday_amount - COALESCE(b.avg_daily_amount, 0))::numeric, 2) AS change,
				CASE WHEN b.avg_daily_amount IS NULL OR b.avg_daily_amount = 0 THEN 'NEW' ELSE '' END AS is_new
			FROM yesterday_by_resource y
			LEFT JOIN baseline_by_resource b ON y.resource_name = b.resource_name
			ORDER BY (y.yesterday_amount - COALESCE(b.avg_daily_amount, 0)) DESC
			LIMIT 10
		`, targetDate, targetDate, cfg.BaselineDays, targetDate)
	}

	type breakdownRow struct {
		Name      string  `db:"name"`
		Yesterday float64 `db:"yesterday"`
		Avg       float64 `db:"avg"`
		Change    float64 `db:"change"`
		IsNew     string  `db:"is_new"`
	}

	var rows []breakdownRow
	var err error
	if anomaly.AnomalyType == MetricAnomolyTypeCloudSpendAccount {
		err = dbms.Db.Select(&rows, query, accountId)
	} else {
		err = dbms.Db.Select(&rows, query, accountId, anomaly.Name)
	}
	if err != nil {
		slog.Error("spend-anomaly: root cause query failed", "error", err)
		return nil
	}

	if len(rows) == 0 {
		return nil
	}

	tableRows := make([]any, 0, len(rows))
	for _, r := range rows {
		changeStr := fmt.Sprintf("+$%.2f", r.Change)
		if r.Change < 0 {
			changeStr = fmt.Sprintf("-$%.2f", -r.Change)
		}
		tableRows = append(tableRows, []any{r.Name, fmt.Sprintf("$%.2f", r.Yesterday), fmt.Sprintf("$%.2f", r.Avg), changeStr, r.IsNew})
	}

	return map[string]any{
		"type": "table",
		"data": map[string]any{
			"table_name": tableName,
			"headers":    []string{"Name", "Yesterday", "30d Avg", "Change", "New?"},
			"rows":       tableRows,
		},
		"additional_info": map[string]any{
			"title": title,
		},
	}
}

func buildSpendTrendEvidence(dbms *database.DatabaseManager, anomaly *Anomaly, accountId string, cfg spendAnomalyConfig) map[string]any {
	var query string
	var args []any
	currentDate := cfg.currentDateSQL()

	if anomaly.AnomalyType == MetricAnomolyTypeCloudSpendAccount {
		query = fmt.Sprintf(`
			SELECT s.date::date AS spend_date, ROUND(SUM(s.amount)::numeric, 2) AS daily_amount
			FROM spends s
			WHERE s.cloud_account = $1
			  AND s.exclude_aggregate = false
			  AND s.date >= (%s - INTERVAL '%d days')
			  AND s.date < %s
			GROUP BY s.date::date
			ORDER BY s.date::date
		`, currentDate, cfg.BaselineDays, currentDate)
		args = []any{accountId}
	} else {
		query = fmt.Sprintf(`
			SELECT s.date::date AS spend_date, ROUND(SUM(s.amount)::numeric, 2) AS daily_amount
			FROM spends s
			INNER JOIN cloud_resourses cr ON s.cloud_resource_id = cr.id
			WHERE s.cloud_account = $1
			  AND cr.service_name = $2
			  AND s.exclude_aggregate = false
			  AND s.date >= (%s - INTERVAL '%d days')
			  AND s.date < %s
			GROUP BY s.date::date
			ORDER BY s.date::date
		`, currentDate, cfg.BaselineDays, currentDate)
		args = []any{accountId, anomaly.Name}
	}

	type trendRow struct {
		SpendDate   time.Time `db:"spend_date"`
		DailyAmount float64   `db:"daily_amount"`
	}

	var rows []trendRow
	err := dbms.Db.Select(&rows, query, args...)
	if err != nil {
		slog.Error("spend-anomaly: trend query failed", "error", err)
		return nil
	}

	if len(rows) == 0 {
		return nil
	}

	tableRows := make([]any, 0, len(rows))
	for _, r := range rows {
		tableRows = append(tableRows, []any{r.SpendDate.Format("2006-01-02"), fmt.Sprintf("$%.2f", r.DailyAmount)})
	}

	return map[string]any{
		"type": "table",
		"data": map[string]any{
			"table_name": "Daily Spend Trend",
			"headers":    []string{"Date", "Amount"},
			"rows":       tableRows,
		},
		"additional_info": map[string]any{
			"title": "Spend History",
		},
	}
}
