package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
)

// The daily usage rollup (V990) lets budget checks sum a few rows per day
// instead of every llm_conversation_token_usage row in the period. A
// leader-elected job rebuilds whole days from the usage rows; budgets read the
// rollup only when every day of the period is built and fresh
// (rollupPeriodComplete), and otherwise fall back to the raw query.
const (
	rollupJobName     = "llm_usage_daily_rollup"
	rollupJobInterval = time.Minute
	// rollupBackfillPerTick bounds how many missing or failed days one tick
	// builds on top of yesterday and today, so a fresh install backfills over a
	// few minutes instead of one long first run.
	rollupBackfillPerTick = 5
	// rollupFreshFor is how recently yesterday and today must have been rebuilt
	// for budgets to trust them. Older days only need to be built once.
	rollupFreshFor = 10 * time.Minute
	// rollupRetainDays keeps the current and previous month (what budgets read)
	// with room to spare; older days are pruned.
	rollupRetainDays = 100
	// rollupStatementTimeout bounds one day's rebuild.
	rollupStatementTimeout = "120s"
)

// usageRowAttributionSQL is the row source shared by the rollup and the raw
// fallback query: every token usage row with who its spend belongs to.
//
//   - tenant and session come from the row (written on insert, kept after the
//     conversation is deleted, V986) or, for rows written before that, from
//     the conversation.
//   - account is the conversation's account, the one budgets are enforced on
//     (chains.go, conversation_sync.go); a call's own account_id differs for
//     cross-account work and is only the last resort.
//   - rows with no tenant are calls that never had a conversation (system
//     cost) and are not counted, as before.
const usageRowAttributionSQL = `
	SELECT
		t.created_at,
		COALESCE(t.tenant_id, c.tenant_id) AS tenant_id,
		COALESCE(t.conversation_account_id, c.account_id, t.account_id) AS account_id,
		COALESCE(t.session_id, c.session_id) AS session_id,
		t.llm_provider, t.llm_model,
		t.input_tokens, t.cached_input_tokens, t.cache_creation_tokens,
		t.output_tokens, t.thinking_tokens
	FROM llm_conversation_token_usage t
	LEFT JOIN llm_conversations c ON c.id = t.conversation_id`

// rollupInsertSQL rebuilds one day ($1, 'YYYY-MM-DD'). The long-context tier is
// decided per row, exactly as the raw cost query does, and the non-cached input
// is clamped per row before summing so the read-time price matches the raw sum.
var rollupInsertSQL = fmt.Sprintf(`
	INSERT INTO llm_usage_daily (
		day, tenant_id, account_id, module, llm_provider, llm_model, long_ctx,
		non_cached_input_tokens, cached_input_tokens, cache_creation_tokens, output_tokens, calls
	)
	SELECT $1::date, u.tenant_id, u.account_id,
		CASE WHEN u.session_id LIKE '%[1]s%%' THEN '%[2]s' ELSE '%[3]s' END,
		u.llm_provider, u.llm_model,
		COALESCE(p.context_threshold_tokens IS NOT NULL
			AND (u.input_tokens + COALESCE(u.cache_creation_tokens, 0)) > p.context_threshold_tokens
			AND p.cost_per_million_input_tokens_long_ctx IS NOT NULL, false),
		SUM(GREATEST(u.input_tokens - COALESCE(u.cached_input_tokens, 0), 0)),
		SUM(COALESCE(u.cached_input_tokens, 0)),
		SUM(COALESCE(u.cache_creation_tokens, 0)),
		SUM(u.output_tokens + COALESCE(u.thinking_tokens, 0)),
		COUNT(*)
	FROM (%[4]s
		WHERE t.created_at >= $1::date AND t.created_at < $1::date + 1
	) u
	LEFT JOIN llm_model_pricing p
		ON p.model_name = u.llm_model AND p.provider_name = u.llm_provider AND p.tenant_id IS NULL
	WHERE u.tenant_id IS NOT NULL
	GROUP BY 1, 2, 3, 4, 5, 6, 7`,
	events.SessionIdPrefixEvent, ModuleInvestigation, ModuleUserInvestigation, usageRowAttributionSQL)

// rollupDaysToBuildSQL lists yesterday and today (always rebuilt: new calls
// land there) followed by up to $1 days of the current and previous month that
// are not built yet or failed, most recent first.
const rollupDaysToBuildSQL = `
	SELECT d::date::text FROM (
		SELECT CURRENT_DATE AS d
		UNION ALL SELECT CURRENT_DATE - 1
		UNION ALL (
			SELECT g::date
			FROM generate_series(
				(DATE_TRUNC('month', CURRENT_DATE) - INTERVAL '1 month')::date,
				CURRENT_DATE - 2, INTERVAL '1 day') g
			WHERE NOT EXISTS (
				SELECT 1 FROM llm_usage_rollup_state s WHERE s.day = g::date AND s.status = 'ok')
			ORDER BY g DESC
			LIMIT $1
		)
	) days
	ORDER BY d DESC`

// RegisterUsageRollupJob schedules the rollup on the elected leader. It is a
// no-op when llm_server_budget_usage_rollup_enabled is off.
func RegisterUsageRollupJob() error {
	if !config.Config.BudgetUsageRollupEnabled {
		slog.Info("budget: usage rollup disabled (llm_server_budget_usage_rollup_enabled=false)")
		return nil
	}
	return common.NewLeaderIntervalJob(rollupJobName, func() error {
		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			slog.Error("budget: usage rollup: database unavailable", "error", err)
			return nil
		}
		RunUsageRollupTick(context.Background(), dbManager)
		return nil
	}, rollupJobInterval)
}

// RunUsageRollupTick rebuilds the days that need it and prunes old ones. A day
// that fails is recorded as failed (budgets then fall back to the raw query for
// any period containing it) and retried on the next tick.
func RunUsageRollupTick(ctx context.Context, dbManager *common.DatabaseManager) {
	var days []string
	if err := dbManager.Db.SelectContext(ctx, &days, rollupDaysToBuildSQL, rollupBackfillPerTick); err != nil {
		slog.Error("budget: usage rollup: unable to list days to build", "error", err)
		return
	}
	for _, day := range days {
		if err := RebuildUsageDay(ctx, dbManager, day); err != nil {
			slog.Error("budget: usage rollup: day failed; budgets use the raw query for periods containing it",
				"day", day, "error", err)
		}
	}
	pruneUsageRollup(ctx, dbManager)
}

// errRollupDayBusy means another leader is rebuilding the day right now.
var errRollupDayBusy = errors.New("day is being rebuilt by another leader")

// RebuildUsageDay replaces one day of the rollup in a single transaction and
// records the result in llm_usage_rollup_state. A per-day advisory lock keeps
// two leaders overlapping during a deploy from interleaving their rebuilds; the
// one that finds the day locked skips it rather than waiting and recording a
// timeout over the other's result.
func RebuildUsageDay(ctx context.Context, dbManager *common.DatabaseManager, day string) error {
	rows, err := rebuildUsageDayTx(ctx, dbManager, day)
	if errors.Is(err, errRollupDayBusy) {
		slog.Debug("budget: usage rollup: day skipped, being rebuilt elsewhere", "day", day)
		return nil
	}
	if err != nil {
		msg := err.Error()
		if len(msg) > 1000 {
			msg = msg[:1000]
		}
		if _, stateErr := dbManager.Db.ExecContext(ctx, `
			INSERT INTO llm_usage_rollup_state (day, status, computed_at, row_count, error)
			VALUES ($1::date, 'error', now(), 0, $2)
			ON CONFLICT (day) DO UPDATE SET status = 'error', computed_at = now(), error = EXCLUDED.error`,
			day, msg); stateErr != nil {
			slog.Error("budget: usage rollup: unable to record failed day", "day", day, "error", stateErr)
		}
		return err
	}
	slog.Debug("budget: usage rollup: day rebuilt", "day", day, "rows", rows)
	return nil
}

func rebuildUsageDayTx(ctx context.Context, dbManager *common.DatabaseManager, day string) (int64, error) {
	tx, err := dbManager.Db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = '"+rollupStatementTimeout+"'"); err != nil {
		return 0, fmt.Errorf("statement timeout: %w", err)
	}
	var locked bool
	if err := tx.GetContext(ctx, &locked,
		`SELECT pg_try_advisory_xact_lock(hashtext('llm_usage_daily'), ($1::date - DATE '2000-01-01'))`, day); err != nil {
		return 0, fmt.Errorf("lock: %w", err)
	}
	if !locked {
		return 0, errRollupDayBusy
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM llm_usage_daily WHERE day = $1::date`, day); err != nil {
		return 0, fmt.Errorf("clear: %w", err)
	}
	res, err := tx.ExecContext(ctx, rollupInsertSQL, day)
	if err != nil {
		return 0, fmt.Errorf("aggregate: %w", err)
	}
	rows, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO llm_usage_rollup_state (day, status, computed_at, row_count, error)
		VALUES ($1::date, 'ok', now(), $2, NULL)
		ON CONFLICT (day) DO UPDATE SET status = 'ok', computed_at = now(), row_count = EXCLUDED.row_count, error = NULL`,
		day, rows); err != nil {
		return 0, fmt.Errorf("record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return rows, nil
}

func pruneUsageRollup(ctx context.Context, dbManager *common.DatabaseManager) {
	for _, table := range []string{"llm_usage_daily", "llm_usage_rollup_state"} {
		query := fmt.Sprintf(`DELETE FROM %s WHERE day < CURRENT_DATE - %d`, table, rollupRetainDays)
		if _, err := dbManager.Db.ExecContext(ctx, query); err != nil {
			slog.Warn("budget: usage rollup: prune failed", "table", table, "error", err)
		}
	}
}

// rollupPeriodCompleteSQL is true when every day of the current period (day or
// month, %s) is built, and yesterday and today were rebuilt within
// rollupFreshFor.
var rollupPeriodCompleteSQL = `
	SELECT COUNT(*) = (CURRENT_DATE - DATE_TRUNC('%[1]s', CURRENT_TIMESTAMP)::date + 1)
	FROM llm_usage_rollup_state s
	WHERE s.day >= DATE_TRUNC('%[1]s', CURRENT_TIMESTAMP)::date
	AND s.day <= CURRENT_DATE
	AND s.status = 'ok'
	AND (s.day < CURRENT_DATE - 1 OR s.computed_at > now() - INTERVAL '` + fmt.Sprintf("%d seconds", int(rollupFreshFor.Seconds())) + `')`

// rollupPeriodComplete reports whether budgets can read the period from the
// rollup. period must already be validated.
func rollupPeriodComplete(ctx context.Context, dbManager *common.DatabaseManager, period string) (bool, error) {
	var complete bool
	if err := dbManager.Db.GetContext(ctx, &complete, fmt.Sprintf(rollupPeriodCompleteSQL, period)); err != nil {
		return false, fmt.Errorf("rollup state: %w", err)
	}
	return complete, nil
}
