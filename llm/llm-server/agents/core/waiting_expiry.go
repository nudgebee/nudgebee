package core

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

// unattendedConversationSources are conversations started by an alert or an
// automation rather than by a person in chat. Nobody watches their approval
// prompts, and while one sits WAITING every new firing of the same alert is
// skipped, so they expire after WaitingExpireUnattendedHours instead of
// WaitingExpireDays.
var unattendedConversationSources = []string{
	string(ConversationSourceInvestigation),
	string(ConversationSourceInstantNotification),
	string(ConversationSourceAutomation),
}

// RegisterWaitingConversationExpiry schedules an hourly leader-elected job
// that marks WAITING conversations idle longer than WaitingExpireDays as
// TERMINATED. Without this the Waiting filter accumulates dead approval
// requests forever (measured: 99%+ of WAITING rows older than a week) and
// stops being usable as an inbox. Alert and automation conversations expire
// sooner, after WaitingExpireUnattendedHours.
//
// Status update only — conversation content is never deleted. Set both
// LLM_SERVER_WAITING_EXPIRE_DAYS and LLM_SERVER_WAITING_EXPIRE_UNATTENDED_HOURS
// to 0 to disable.
func RegisterWaitingConversationExpiry(ctx context.Context) error {
	expireDays := config.Config.WaitingExpireDays
	unattendedHours := config.Config.WaitingExpireUnattendedHours
	if expireDays <= 0 && unattendedHours <= 0 {
		slog.Info("waiting-expiry: disabled (llm_server_waiting_expire_days=0, llm_server_waiting_expire_unattended_hours=0)")
		return nil
	}

	slog.Info("waiting-expiry: registering job", "expire_days", expireDays, "unattended_hours", unattendedHours, "interval_hours", 1)
	return common.NewLeaderIntervalJob("waiting_conversation_expiry", func() error {
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			slog.Error("waiting-expiry: failed to get database manager", "error", err)
			return nil
		}
		expireWaitingConversations(ctx, dbms.Db, expireDays, unattendedHours)
		return nil
	}, 1*time.Hour)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// expireWaitingConversations runs a single UPDATE. A threshold of 0 or less
// turns its clause off: the days clause is guarded by $3 > 0, and the
// unattended clause gets an empty source list.
func expireWaitingConversations(ctx context.Context, db execer, expireDays, unattendedHours int) {
	sources := unattendedConversationSources
	if unattendedHours <= 0 {
		sources = []string{}
	}
	// Generous bound: the first run sweeps the accumulated backlog in one UPDATE.
	execCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := db.ExecContext(execCtx,
		`UPDATE llm_conversations
		 SET status = $1, updated_at = NOW()
		 WHERE status = $2
		   AND (($3 > 0 AND updated_at < NOW() - make_interval(days => $3))
		     OR (source = ANY($4) AND updated_at < NOW() - make_interval(hours => $5)))`,
		string(ConversationStatusTerminated), string(ConversationStatusWaiting), expireDays,
		pq.Array(sources), unattendedHours,
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
		slog.Info("waiting-expiry: expired stale waiting conversations", "count", rows, "older_than_days", expireDays, "unattended_older_than_hours", unattendedHours)
	}
}
