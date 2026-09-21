package core

import (
	"database/sql"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveSendHour pins the fallback contract: a tenant that never set a
// custom hour, or whose stored value is malformed/out of range, must always
// resolve to defaultAiCostReportSendHourUTC rather than dropping out of the
// dispatch loop or panicking.
func TestResolveSendHour(t *testing.T) {
	cases := []struct {
		name  string
		value sql.NullString
		want  int
	}{
		{"unset (no configuration_store row)", sql.NullString{}, defaultAiCostReportSendHourUTC},
		{"midnight", sql.NullString{String: "0", Valid: true}, 0},
		{"last hour of the day", sql.NullString{String: "23", Valid: true}, 23},
		{"mid-day", sql.NullString{String: "14", Valid: true}, 14},
		{"out of range negative", sql.NullString{String: "-1", Valid: true}, defaultAiCostReportSendHourUTC},
		{"out of range high", sql.NullString{String: "24", Valid: true}, defaultAiCostReportSendHourUTC},
		{"non-numeric", sql.NullString{String: "not-an-hour", Valid: true}, defaultAiCostReportSendHourUTC},
		{"empty string", sql.NullString{String: "", Valid: true}, defaultAiCostReportSendHourUTC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveSendHour(tc.value))
		})
	}
}

// TestTryClaimDispatch_SecondCallFindsExisting pins the idempotency contract
// RunAiCostReportDispatch depends on: a repeat claim for the same
// (tenant, report_date) — the ON CONFLICT DO NOTHING path — returns
// claimed=false, not an error, so a catch-up/retry fire skips silently
// instead of double-publishing.
func TestTryClaimDispatch_SecondCallFindsExisting(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dbManager := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	reportDate := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("INSERT INTO ai_cost_report_dispatch_log").
		WithArgs("tenant-1", "2026-08-10").
		WillReturnRows(sqlmock.NewRows([]string{"id"})) // ON CONFLICT DO NOTHING -> zero rows

	claimed, err := tryClaimDispatch(dbManager, "tenant-1", reportDate)
	require.NoError(t, err)
	assert.False(t, claimed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestReleaseDispatchClaim_AllowsReclaim pins the fix for the bug where a
// compute/publish failure after a successful claim permanently blocked every
// later retry: releasing the claim must let a subsequent tryClaimDispatch
// for the same (tenant, report_date) succeed again.
func TestReleaseDispatchClaim_AllowsReclaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	dbManager := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	reportDate := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("INSERT INTO ai_cost_report_dispatch_log").
		WithArgs("tenant-1", "2026-08-10").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("claim-1"))
	claimed, err := tryClaimDispatch(dbManager, "tenant-1", reportDate)
	require.NoError(t, err)
	require.True(t, claimed, "first claim must succeed")

	mock.ExpectExec("DELETE FROM ai_cost_report_dispatch_log").
		WithArgs("tenant-1", "2026-08-10").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, releaseDispatchClaim(dbManager, "tenant-1", reportDate))

	mock.ExpectQuery("INSERT INTO ai_cost_report_dispatch_log").
		WithArgs("tenant-1", "2026-08-10").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("claim-2"))
	reclaimed, err := tryClaimDispatch(dbManager, "tenant-1", reportDate)
	require.NoError(t, err)
	assert.True(t, reclaimed, "claim released after a failed compute/publish must be reclaimable")

	require.NoError(t, mock.ExpectationsWereMet())
}
