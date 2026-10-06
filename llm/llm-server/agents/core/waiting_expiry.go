package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
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

// waitingExpiryBatchSize caps how many conversations each rule ends per run, so
// a large backlog is worked off over a few hourly runs.
const waitingExpiryBatchSize = 500

// RegisterWaitingConversationExpiry schedules an hourly leader-elected job
// that ends WAITING conversations idle longer than WaitingExpireDays. Without
// this the Waiting filter accumulates dead approval requests forever
// (measured: 99%+ of WAITING rows older than a week) and stops being usable as
// an inbox. Alert and automation conversations expire sooner, after
// WaitingExpireUnattendedHours.
//
// Each conversation is ended the way Stop ends it, with a note in place of the
// unanswered prompt saying why, so the old approval card can no longer be
// answered. Content is never deleted. Set both
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
		expireWaitingConversations(ctx, dbms.Db, &ConversationDao{dbManager: dbms}, expireDays, unattendedHours)
		return nil
	}, 1*time.Hour)
}

type selecter interface {
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
}

type waitingExpirer interface {
	ExpireWaitingConversation(ctx *security.RequestContext, accountId, conversationId, note string) (bool, error)
}

type waitingConversation struct {
	ID        string `db:"id"`
	AccountID string `db:"account_id"`
}

func unattendedExpiryNote(hours int) string {
	return fmt.Sprintf("Stopped after %d hours with no answer. This automatic run was waiting for someone to approve a step or answer a question, and nobody did, so that step was not run. The next run starts fresh.", hours)
}

func chatExpiryNote(days int) string {
	return fmt.Sprintf("Stopped after %d days with no answer. This conversation was waiting for you to approve a step or answer a question, so that step was not run. Ask again to continue.", days)
}

// expireWaitingConversations applies the unattended rule, then the days rule.
// A threshold of 0 or less skips its rule. While the unattended rule is on, the
// days rule leaves unattended sources to it, so they always get its note.
func expireWaitingConversations(ctx context.Context, db selecter, dao waitingExpirer, expireDays, unattendedHours int) {
	unattended := []string{}
	if unattendedHours > 0 {
		unattended = unattendedConversationSources
		expireWaitingBatch(ctx, db, dao, "unattended", unattendedExpiryNote(unattendedHours),
			`SELECT id, account_id FROM llm_conversations
			 WHERE status = $1 AND source = ANY($2) AND updated_at < NOW() - make_interval(hours => $3)
			 ORDER BY updated_at LIMIT $4`,
			string(ConversationStatusWaiting), pq.Array(unattended), unattendedHours, waitingExpiryBatchSize)
	}
	if expireDays > 0 {
		expireWaitingBatch(ctx, db, dao, "chat", chatExpiryNote(expireDays),
			`SELECT id, account_id FROM llm_conversations
			 WHERE status = $1 AND COALESCE(source, '') <> ALL($2) AND updated_at < NOW() - make_interval(days => $3)
			 ORDER BY updated_at LIMIT $4`,
			string(ConversationStatusWaiting), pq.Array(unattended), expireDays, waitingExpiryBatchSize)
	}
}

func expireWaitingBatch(ctx context.Context, db selecter, dao waitingExpirer, rule, note, query string, args ...any) {
	// Generous bound: the query only selects; each conversation is ended in its own transaction.
	selectCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var conversations []waitingConversation
	if err := db.SelectContext(selectCtx, &conversations, query, args...); err != nil {
		// Parent cancelled = server shutdown; a genuine timeout still logs.
		if ctx.Err() == nil {
			slog.Error("waiting-expiry: failed to list waiting conversations", "rule", rule, "error", err)
		}
		return
	}

	reqCtx := security.NewRequestContextForSuperAdmin()
	expired := 0
	for _, c := range conversations {
		if ctx.Err() != nil {
			return
		}
		ended, err := dao.ExpireWaitingConversation(reqCtx, c.AccountID, c.ID, note)
		if err != nil {
			slog.Error("waiting-expiry: failed to end waiting conversation", "rule", rule, "conversation_id", c.ID, "error", err)
			continue
		}
		if ended {
			expired++
		}
	}
	if expired > 0 {
		slog.Info("waiting-expiry: expired stale waiting conversations", "rule", rule, "count", expired)
	}
}
