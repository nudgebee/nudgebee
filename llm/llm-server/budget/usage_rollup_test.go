package budget

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
)

func withRollupFlag(t *testing.T, enabled bool) {
	t.Helper()
	saved := config.Config.BudgetUsageRollupEnabled
	config.Config.BudgetUsageRollupEnabled = enabled
	t.Cleanup(func() { config.Config.BudgetUsageRollupEnabled = saved })
}

const rollupStateQuery = `FROM llm_usage_rollup_state s`

// With every day of the period built and fresh, the check reads the rollup:
// the entity and module select rollup rows, priced at read time.
func TestGetEntityTokenUsage_ReadsRollupWhenComplete(t *testing.T) {
	withRollupFlag(t, true)
	cases := []struct {
		name, entityType, module, entity string
	}{
		{"tenant chat", EntityTypeTenant, ModuleUserInvestigation, "r.tenant_id = $1"},
		{"account investigation", EntityTypeAccount, ModuleInvestigation, "r.account_id = $1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dm, mock := newMockMetastoreDM(t)
			mock.ExpectQuery(regexp.QuoteMeta(rollupStateQuery)).
				WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(true))
			mock.ExpectQuery(regexp.QuoteMeta("FROM llm_usage_daily r") + `[\s\S]*` +
				regexp.QuoteMeta(tc.entity+" AND r.module = '"+tc.module+"'")).
				WithArgs("entity-1").
				WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(4.25))

			cost, err := getEntityTokenUsage(dm, tc.entityType, "entity-1", tc.module, "month")

			require.NoError(t, err)
			assert.Equal(t, 4.25, cost)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// An incomplete or stale period, an unreadable state table, or the flag off
// all read the usage rows directly, bucketed by when each call ran and
// attributed to the row's tenant/account/session (conversation as fallback).
func TestGetEntityTokenUsage_FallsBackToRawCallTime(t *testing.T) {
	raw := regexp.QuoteMeta("FROM llm_conversation_token_usage t") + `[\s\S]*` +
		regexp.QuoteMeta("WHERE t.created_at >= DATE_TRUNC('day', CURRENT_TIMESTAMP)") + `[\s\S]*` +
		regexp.QuoteMeta("WHERE u.tenant_id IS NOT NULL AND u.account_id = $1") + `[\s\S]*` +
		regexp.QuoteMeta("u.session_id LIKE '"+events.SessionIdPrefixEvent+"%'")

	run := func(t *testing.T, expectState func(sqlmock.Sqlmock)) {
		dm, mock := newMockMetastoreDM(t)
		expectState(mock)
		mock.ExpectQuery(raw).WithArgs("acct-1").
			WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(1.5))
		cost, err := getEntityTokenUsage(dm, EntityTypeAccount, "acct-1", ModuleInvestigation, "day")
		require.NoError(t, err)
		assert.Equal(t, 1.5, cost)
		assert.NoError(t, mock.ExpectationsWereMet())
	}

	t.Run("period not complete", func(t *testing.T) {
		withRollupFlag(t, true)
		run(t, func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(rollupStateQuery)).
				WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(false))
		})
	})
	t.Run("state unreadable", func(t *testing.T) {
		withRollupFlag(t, true)
		run(t, func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(rollupStateQuery)).WillReturnError(errors.New("no such table"))
		})
	})
	t.Run("flag off", func(t *testing.T) {
		withRollupFlag(t, false)
		run(t, func(sqlmock.Sqlmock) {})
	})
}

// The period-complete check counts built days from the start of the period
// to today and requires yesterday and today to be recent.
func TestRollupPeriodCompleteSQL(t *testing.T) {
	for _, period := range []string{"day", "month"} {
		dm, mock := newMockMetastoreDM(t)
		mock.ExpectQuery(regexp.QuoteMeta("(CURRENT_DATE - DATE_TRUNC('"+period+"', CURRENT_TIMESTAMP)::date + 1)") +
			`[\s\S]*` + regexp.QuoteMeta("s.status = 'ok'") + `[\s\S]*` +
			regexp.QuoteMeta("s.day < CURRENT_DATE - 1 OR s.computed_at > now() - INTERVAL '600 seconds'")).
			WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(true))
		complete, err := rollupPeriodComplete(context.Background(), dm, period)
		require.NoError(t, err)
		assert.True(t, complete)
		assert.NoError(t, mock.ExpectationsWereMet())
	}
}

// A day is rebuilt in one transaction under a per-day lock: clear, aggregate,
// record success.
func TestRebuildUsageDay(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL statement_timeout")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("pg_try_advisory_xact_lock")).WithArgs("2026-10-05").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM llm_usage_daily WHERE day = $1::date")).WithArgs("2026-10-05").
		WillReturnResult(sqlmock.NewResult(0, 12))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_usage_daily") + `[\s\S]*` +
		regexp.QuoteMeta("GREATEST(u.input_tokens - COALESCE(u.cached_input_tokens, 0), 0)") + `[\s\S]*` +
		regexp.QuoteMeta("WHERE u.tenant_id IS NOT NULL")).
		WithArgs("2026-10-05").WillReturnResult(sqlmock.NewResult(0, 9))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_usage_rollup_state")+`[\s\S]*'ok'`).
		WithArgs("2026-10-05", int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, RebuildUsageDay(context.Background(), dm, "2026-10-05"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A failed rebuild rolls back (the previous rows stay) and the day is marked
// failed, so budgets use the raw query for any period containing it.
func TestRebuildUsageDay_RecordsFailure(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL statement_timeout")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("pg_try_advisory_xact_lock")).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM llm_usage_daily")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_usage_daily")).WillReturnError(errors.New("canceling statement due to statement timeout"))
	mock.ExpectRollback()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_usage_rollup_state")+`[\s\S]*'error'`).
		WithArgs("2026-10-05", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))

	err := RebuildUsageDay(context.Background(), dm, "2026-10-05")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A tick rebuilds every listed day (yesterday, today, then bounded backfill),
// carries on past a failed day, and prunes old days.
func TestRunUsageRollupTick(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery(regexp.QuoteMeta("generate_series")).WithArgs(rollupBackfillPerTick).
		WillReturnRows(sqlmock.NewRows([]string{"d"}).AddRow("2026-10-06").AddRow("2026-10-05"))
	// First day fails at begin; it is recorded and the tick moves on.
	mock.ExpectBegin().WillReturnError(errors.New("connection reset"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_usage_rollup_state")).
		WithArgs("2026-10-06", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL statement_timeout")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("pg_try_advisory_xact_lock")).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM llm_usage_daily")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_usage_daily")).WithArgs("2026-10-05").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_usage_rollup_state")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM llm_usage_daily WHERE day < CURRENT_DATE - 100")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM llm_usage_rollup_state WHERE day < CURRENT_DATE - 100")).WillReturnResult(sqlmock.NewResult(0, 0))

	RunUsageRollupTick(context.Background(), dm)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A day another leader is rebuilding is skipped: no wait, and no failure
// recorded over the other leader's result.
func TestRebuildUsageDay_SkipsDayLockedElsewhere(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL statement_timeout")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("pg_try_advisory_xact_lock")).WithArgs("2026-10-05").
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	mock.ExpectRollback()

	require.NoError(t, RebuildUsageDay(context.Background(), dm, "2026-10-05"))
	assert.NoError(t, mock.ExpectationsWereMet())
}
