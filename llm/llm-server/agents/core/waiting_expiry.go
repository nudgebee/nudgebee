package core

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

// RegisterWaitingConversationExpiry schedules an hourly leader-elected job
// that marks WAITING conversations idle longer than WaitingExpireDays as
// TERMINATED. Without this the Waiting filter accumulates dead approval
// requests forever (measured: 99%+ of WAITING rows older than a week) and
// stops being usable as an inbox.
//
// Status update only — conversation content is never deleted. Set
// LLM_SERVER_WAITING_EXPIRE_DAYS = 0 to disable.
func RegisterWaitingConversationExpiry(ctx context.Context) error {
	expireDays := config.Config.WaitingExpireDays
	if expireDays <= 0 {
		slog.Info("waiting-expiry: disabled (llm_server_waiting_expire_days=0)")
		return nil
	}

	slog.Info("waiting-expiry: registering job", "expire_days", expireDays, "interval_hours", 1)
	return common.NewLeaderIntervalJob("waiting_conversation_expiry", func() error {
		expireWaitingConversations(ctx, expireDays)
		return nil
	}, 1*time.Hour)
}

func expireWaitingConversations(ctx context.Context, expireDays int) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("waiting-expiry: failed to get database manager", "error", err)
		return
	}
	// Generous bound: the first run sweeps the accumulated backlog in one UPDATE.
	execCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := dbms.Db.ExecContext(execCtx,
		`UPDATE llm_conversations
		 SET status = $1, updated_at = NOW()
		 WHERE status = $2 AND updated_at < NOW() - make_interval(days => $3)`,
		string(ConversationStatusTerminated), string(ConversationStatusWaiting), expireDays,
	)
	if err != nil {
		// Parent cancelled = server shutdown; a genuine timeout still logs.
		if ctx.Err() != nil {
			return
		}
		slog.Error("waiting-expiry: failed to expire conversations", "error", err)
		return
	}
	if rows, err := result.RowsAffected(); err == nil && rows > 0 {
		slog.Info("waiting-expiry: expired stale waiting conversations", "count", rows, "older_than_days", expireDays)
	}
}
