package core

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"nudgebee/llm/common"
)

// Per-tenant custom send hour for the AI Cost Daily Report, backed by
// configuration_store (see V598 / V894). Stored as a plain "0"-"23" string —
// always UTC, no timezone concept anywhere in this codebase.

const (
	aiCostReportScheduleConfigType = "ai_cost_report_schedule"
	aiCostReportScheduleKey        = "send_hour_utc"
	defaultAiCostReportSendHourUTC = 6 // the historical fixed cron time, before this was configurable
)

// resolveSendHour falls back to the default on any bad value (missing,
// malformed, out of range) rather than erroring, so a tenant never silently
// drops out of the dispatch loop over a corrupt stored value.
func resolveSendHour(value sql.NullString) int {
	if !value.Valid {
		return defaultAiCostReportSendHourUTC
	}
	hour, err := strconv.Atoi(value.String)
	if err != nil || hour < 0 || hour > 23 {
		return defaultAiCostReportSendHourUTC
	}
	return hour
}

// tryClaimDispatch returns true only for the caller that wins the
// (tenant, report_date) insert — a second attempt (Temporal catch-up, a
// retry) finds the row already present and returns false, not an error.
func tryClaimDispatch(dbManager *common.DatabaseManager, tenantID string, reportDate time.Time) (bool, error) {
	var claimedID string
	err := dbManager.Db.QueryRowx(
		`INSERT INTO ai_cost_report_dispatch_log (tenant_id, report_date)
		 VALUES ($1, $2)
		 ON CONFLICT (tenant_id, report_date) DO NOTHING
		 RETURNING id`,
		tenantID, reportDate.UTC().Format("2006-01-02"),
	).Scan(&claimedID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("tryClaimDispatch: %w", err)
	}
	return true, nil
}

// releaseDispatchClaim undoes tryClaimDispatch after a failed compute or
// publish, so a later retry (Temporal catch-up, a same-hour reattempt) can
// claim the slot again instead of finding it permanently taken.
func releaseDispatchClaim(dbManager *common.DatabaseManager, tenantID string, reportDate time.Time) error {
	_, err := dbManager.Db.Exec(
		`DELETE FROM ai_cost_report_dispatch_log WHERE tenant_id = $1 AND report_date = $2`,
		tenantID, reportDate.UTC().Format("2006-01-02"),
	)
	if err != nil {
		return fmt.Errorf("releaseDispatchClaim: %w", err)
	}
	return nil
}

// GetAiCostReportSchedule returns a tenant's configured send hour (UTC), or
// defaultAiCostReportSendHourUTC if they've never set one.
func GetAiCostReportSchedule(dbManager *common.DatabaseManager, tenantID string) (int, error) {
	if dbManager == nil {
		return 0, fmt.Errorf("GetAiCostReportSchedule: database manager unavailable")
	}
	var value sql.NullString
	err := dbManager.Db.QueryRowx(
		`SELECT value FROM configuration_store
		 WHERE tenant_id = $1 AND config_type = $2 AND key = $3 AND is_active AND account_id IS NULL`,
		tenantID, aiCostReportScheduleConfigType, aiCostReportScheduleKey,
	).Scan(&value)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("GetAiCostReportSchedule: %w", err)
	}
	return resolveSendHour(value), nil
}

// UpsertAiCostReportSchedule sets a tenant's send hour (UTC, 0-23). The V894
// partial unique index is what makes this a plain ON CONFLICT upsert — the
// conflict target's WHERE clause must match that index's predicate exactly.
func UpsertAiCostReportSchedule(dbManager *common.DatabaseManager, tenantID, userID string, sendHourUTC int) error {
	if dbManager == nil {
		return fmt.Errorf("UpsertAiCostReportSchedule: database manager unavailable")
	}
	if tenantID == "" {
		return fmt.Errorf("UpsertAiCostReportSchedule: tenant is required")
	}
	if sendHourUTC < 0 || sendHourUTC > 23 {
		return fmt.Errorf("UpsertAiCostReportSchedule: send hour must be between 0 and 23 (got %d)", sendHourUTC)
	}
	_, err := dbManager.Db.Exec(
		`INSERT INTO configuration_store (config_type, key, value, tenant_id, created_by)
		 VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid)
		 ON CONFLICT (tenant_id, config_type, key) WHERE is_active AND account_id IS NULL
		 DO UPDATE SET value = EXCLUDED.value`,
		aiCostReportScheduleConfigType, aiCostReportScheduleKey, strconv.Itoa(sendHourUTC), tenantID, userID,
	)
	if err != nil {
		return fmt.Errorf("UpsertAiCostReportSchedule: %w", err)
	}
	return nil
}
