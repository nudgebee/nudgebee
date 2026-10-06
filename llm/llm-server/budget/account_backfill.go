package budget

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/llm/common"
)

// The AI Cost Analyser and cost report filter llm_conversation_token_usage on
// conversation_account_id (V990, indexed by V992). Rows written before V990,
// or by a pod still running older code during a rollout, have it empty; this
// leader job fills them from their conversation in small batches, newest
// first, so recent windows are right first and no long transaction holds the
// table. V993 indexes exactly the rows it still has to fill.
const (
	accountBackfillJobName     = "llm_usage_account_backfill"
	accountBackfillJobInterval = time.Minute
	accountBackfillBatchSize   = 5000
	// accountBackfillBatchesPerTick bounds one tick to 50k rows (a full dev
	// backfill of ~1.7M rows takes about 35 minutes).
	accountBackfillBatchesPerTick = 10
	// accountBackfillTickTimeout bounds one tick, under the interval, so a
	// slow batch cannot hold the leader job; the next tick carries on.
	accountBackfillTickTimeout = 50 * time.Second
)

// accountBackfillSQL fills one batch. The account is the conversation's, as
// on insert; a row whose conversation is gone falls back to the call's own
// account, like the rollup's attribution (usageRowAttributionSQL). The
// candidate predicate matches V993's index predicate so the planner can use
// it. Rows another transaction holds (a conversation being deleted stamps the
// same rows) are skipped and picked up on a later tick.
const accountBackfillSQL = `
	UPDATE llm_conversation_token_usage t
	SET conversation_account_id = s.account_id
	FROM (
		SELECT p.id, COALESCE(c.account_id, p.account_id) AS account_id
		FROM llm_conversation_token_usage p
		LEFT JOIN llm_conversations c ON c.id = p.conversation_id
		WHERE p.conversation_account_id IS NULL
			AND (p.conversation_id IS NOT NULL OR p.tenant_id IS NOT NULL)
			AND COALESCE(c.account_id, p.account_id) IS NOT NULL
		ORDER BY p.created_at DESC
		LIMIT $1
		FOR UPDATE OF p SKIP LOCKED
	) s
	WHERE t.id = s.id AND t.conversation_account_id IS NULL`

// RegisterUsageAccountBackfillJob schedules the backfill on the elected
// leader. Once every row is filled each tick is one empty index read.
func RegisterUsageAccountBackfillJob() error {
	return common.NewLeaderIntervalJob(accountBackfillJobName, func() error {
		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			slog.Error("budget: usage account backfill: database unavailable", "error", err)
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), accountBackfillTickTimeout)
		defer cancel()
		RunUsageAccountBackfillTick(ctx, dbManager)
		return nil
	}, accountBackfillJobInterval)
}

// RunUsageAccountBackfillTick fills up to accountBackfillBatchesPerTick
// batches, stopping early when a batch comes back short (nothing left that
// can be filled right now).
func RunUsageAccountBackfillTick(ctx context.Context, dbManager *common.DatabaseManager) {
	var filled int64
	for i := 0; i < accountBackfillBatchesPerTick; i++ {
		res, err := dbManager.Db.ExecContext(ctx, accountBackfillSQL, accountBackfillBatchSize)
		if err != nil {
			if ctx.Err() != nil {
				// The tick ran out of time (expected while a large backlog drains)
				// or the server is stopping; the next tick carries on.
				slog.Warn("budget: usage account backfill: tick stopped early", "error", err, "filled", filled)
				return
			}
			slog.Error("budget: usage account backfill: batch failed", "error", err, "filled", filled)
			return
		}
		n, _ := res.RowsAffected()
		filled += n
		if n < accountBackfillBatchSize {
			break
		}
	}
	if filled > 0 {
		slog.Info("budget: usage account backfill: filled rows", "rows", filled)
	}
}
