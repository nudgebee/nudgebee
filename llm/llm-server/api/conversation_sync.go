package api

import (
	"context"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func handleSyncConversationStatusApi(r *gin.Engine) {
	groupV2 := r.Group("/v1/conversation")

	groupV2.POST("/sync", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("conversation_sync")
		dao := core.GetConversationDao()
		if dao == nil {
			slog.Error("sync: conversation dao is not initialized")
			c.JSON(500, buildApiResponse(nil, []error{
				common.Error{
					Message: "conversation dao is not initialized",
				},
			}))
			return
		}
		err := dao.MarkInProgressConversationAsKilled()
		if err != nil {
			slog.Error("sync: error syncing conversation status", "error", err)
			c.JSON(500, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}
	})
}

var workerPool *common.WorkerPool

func init() {
	if testing.Testing() {
		return
	}

	if !config.Config.SyncDeadWorkerMessages {
		slog.Info("sync: skipping sync_dead_worker_messages job")
		return
	}

	workerPool = common.NewWorkerPool("sync_dead_worker_messages", config.Config.SyncDeadWorkerCount, config.Config.SyncDeadQueueSize)

	slog.Info("sync: initializing conversation sync jobs")

	err := common.NewLeaderIntervalJob("sync_dead_worker_messages", syncDeadWorkerMessages, time.Duration(config.Config.ServerHeartBeatTimeoutSecond)*time.Second)
	if err != nil {
		slog.Error("sync: unable to create sync_dead_worker_messages job", "error", err)
	}

	err = common.NewLeaderIntervalJob("sync_stuck_event_analyses", syncStuckEventAnalyses, time.Duration(config.Config.ServerHeartBeatTimeoutSecond)*time.Second)
	if err != nil {
		slog.Error("sync: unable to create sync_stuck_event_analyses job", "error", err)
	}

	// Registered everywhere, pods included. The dead-worker reaper above can only recover
	// a message whose owner has vanished from nb_workers, so it is blind to any process
	// that comes back under the SAME worker name — which is exactly what an OOMKilled
	// container does: the pod (and its name) survives, only the container restarts, and
	// the heartbeat resumes as if nothing happened. Its in-flight messages then stay
	// IN_PROGRESS forever, with the restarted process often being the very leader running
	// the reaper that skips them. Observed on dev: llm-server-7f86ddd467-d6scf, OOMKilled
	// twice in one afternoon, still holding an orphan 35 minutes later.
	//
	// Safe in-cluster because bootCutoff() bounds the sweep to work that predates this
	// process: a restarted container recovers what it abandoned and cannot touch a request
	// its siblings are still serving.
	if err := common.NewPooledJob("sync_own_orphan_messages", syncOwnOrphanMessages); err != nil {
		slog.Error("sync: unable to create sync_own_orphan_messages job", "error", err)
	}
}

// checkBudgetAndRestartMessage checks budget limits and conversation state before restarting a message.
// Returns true if message should be restarted, false if the conversation is terminal, budget exceeded, or error occurred.
func checkBudgetAndRestartMessage(dao core.IConversationDao, message core.ConversationMessage) bool {
	logger := slog.With("message", message.ID.String(), "conversation", message.ConversationID.String(), "account", message.AccountID.String())

	// Get conversation to check status and budget
	conversation, err := dao.GetConversation(message.ConversationID.String())
	if err != nil {
		logger.Error("sync: unable to get conversation for budget check", "error", err)
		// Mark as failed - can't proceed without conversation details
		_ = dao.UpdateConversationMessage(message.ID.String(), "Failed to restart: unable to get conversation details", core.ConversationStatusFailed)
		return false
	}

	// Skip messages whose conversation already reached a terminal state.
	// The message may be stuck as IN_PROGRESS due to a race condition (e.g. followup
	// completion updated the conversation but not the generation message). Restarting
	// would destroy the completed execution data via CleanupConversationMessage.
	if core.IsTerminalConversationStatus(conversation.Status) {
		logger.Info("sync: skipping message for terminal conversation, fixing orphaned message status",
			"conversation_status", conversation.Status)
		_ = dao.UpdateConversationMessage(message.ID.String(), message.Response, conversation.Status)
		return false
	}

	// Determine module based on session_id
	module := budget.ModuleUserInvestigation
	if strings.HasPrefix(conversation.SessionID, events.SessionIdPrefixEvent) {
		module = budget.ModuleInvestigation
	}

	// Check budget limits before restarting
	budgetExceeded, budgetErrorMsg := budget.CheckBudgetLimits(conversation.TenantID.String(), message.AccountID.String(), module, logger)
	if budgetExceeded {
		logger.Warn("sync: budget limit exceeded, marking conversation as failed instead of restarting", "error", budgetErrorMsg)
		// Mark message and conversation as failed due to budget limit - prevent infinite retry
		_ = dao.UpdateConversationMessage(message.ID.String(), budgetErrorMsg, core.ConversationStatusFailed)
		_ = dao.UpdateConversationStatus(conversation.ID.String(), core.ConversationStatusFailed)
		return false
	}

	logger.Info("sync: budget check passed, restarting conversation message")
	return true
}

func syncDeadWorkerMessages() error {
	dao := core.GetConversationDao()
	if dao == nil {
		return common.Error{Message: "conversation dao is not initialized"}
	}
	messages, err := dao.ListConversationMessages(core.ConversationStatusInProgress, "", "", true)
	if err != nil {
		return err
	}

	messages = lo.Filter(messages, func(message core.ConversationMessage, index int) bool {
		return isDeadWorkerMessageRecoverable(message)
	})

	slog.Info("sync: restarting conversation messages for dead workers", "count", len(messages))
	restartMessages(dao, messages)
	return nil
}

// syncOwnOrphanMessages restarts the in-progress messages this worker owns. It runs
// once at boot on processes that can never win the leader election, whose abandoned
// conversations nothing else will pick up.
//
// Running at boot is what makes it safe: a message still marked in-progress under our
// own name before we have started any work was necessarily left behind by a previous
// run of this same process. There is no live execution to race with, so no staleness
// heuristic is needed — unlike the dead-worker path, which cannot use our own name
// because we re-register it on startup and so never look dead to ourselves.
func syncOwnOrphanMessages() error {
	dao := core.GetConversationDao()
	if dao == nil {
		return common.Error{Message: "conversation dao is not initialized"}
	}
	messages, err := dao.ListConversationMessages(core.ConversationStatusInProgress, config.Config.ServerName, "", false)
	if err != nil {
		return err
	}

	cutoff := bootCutoff()
	messages = lo.Filter(messages, func(message core.ConversationMessage, index int) bool {
		return isOwnOrphanRecoverable(message, cutoff)
	})

	if len(messages) == 0 {
		return nil
	}

	slog.Info("sync: restarting own conversation messages orphaned by a previous run",
		"count", len(messages), "worker_name", config.Config.ServerName)
	restartMessages(dao, messages)
	return nil
}

// isDeadWorkerMessageRecoverable reports whether the cluster may restart a message
// left behind by a worker that is no longer registered.
func isDeadWorkerMessageRecoverable(message core.ConversationMessage) bool {
	if message.MessageType == "followup" {
		return false
	}
	// A conversation owned by a machine outside the cluster is that machine's to
	// recover (syncOwnOrphanMessages). Restarting it here would run someone's local
	// session on cluster infrastructure, against whatever credentials and code that
	// pod happens to have.
	if message.WorkerName != nil && config.IsLocalWorkerName(*message.WorkerName) {
		return false
	}
	return true
}

// processStartedAt is captured at package init, before the HTTP listener can accept
// anything. Only its *elapsed* value is ever used, never its wall-clock value — see
// bootCutoff.
var processStartedAt = time.Now()

// bootCutoff returns the moment this process started, expressed on the database's
// clock. updated_at is written by the database, so comparing it against a laptop's
// time.Now() would make this bound only as trustworthy as the skew between the two
// machines — and it guards a window a few seconds wide, so seconds of skew are enough
// to matter. Instead the elapsed time comes from Go's monotonic clock and now() from
// the database: each side reads only its own clock.
func bootCutoff() time.Time {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err == nil {
		var cutoff time.Time
		if err = dbManager.Db.Get(&cutoff, "SELECT now() - make_interval(secs => $1)",
			time.Since(processStartedAt).Seconds()); err == nil {
			return cutoff
		}
	}
	// Fall back to the local clock rather than skipping recovery entirely: a slightly
	// skewed bound still rules out almost everything, whereas no bound reopens the
	// boot race this exists to close.
	slog.Warn("sync: could not read boot cutoff from the database clock, using local time", "error", err)
	return processStartedAt
}

// isOwnOrphanRecoverable reports whether a message this worker owns is worth
// restarting at boot.
func isOwnOrphanRecoverable(message core.ConversationMessage, bootCutoff time.Time) bool {
	if message.MessageType == "followup" {
		return false
	}
	if message.UpdatedAt == nil {
		return false
	}
	// Only work that predates this process can be an orphan of a previous run. The
	// listener starts serving a few seconds before this one-time job fires, so without
	// this bound the sweep can race a conversation the current process just accepted
	// and restart it underneath itself.
	if !message.UpdatedAt.Before(bootCutoff) {
		return false
	}
	// Same horizon the dead-worker query applies, from the one shared constant.
	return time.Since(*message.UpdatedAt) < config.OrphanRecoveryHorizon
}

// restartMessages re-dispatches messages whose owning worker is gone, after checking
// budget and conversation state for each.
func restartMessages(dao core.IConversationDao, messages []core.ConversationMessage) {
	for _, message := range messages {
		// Check budget before restarting
		if !checkBudgetAndRestartMessage(dao, message) {
			continue
		}

		logger := slog.With("message", message.ID.String(), "conversation", message.ConversationID.String())

		// update worker to current worker.. so that next check doesnt include this
		err := dao.UpdateConversationMessage(message.ID.String(), message.Response, core.ConversationStatusInProgress)
		if err != nil {
			logger.Error("sync: unable to update conversation message", "error", err)
			continue
		}

		// these messages are generated using followups
		submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
		// No defer cancel here as it's in a loop; the worker will call it
		currentErr := workerPool.Submit(submissionCtx, func() {
			defer cancel()
			_, err = core.HandleConversationMessageRequest(message.AccountID.String(), message.ConversationID.String(), message.ID.String())
			if err != nil {
				logger.Error("sync: unable to restart conversation messages", "error", err)
			}
		})
		if currentErr != nil {
			cancel()
			common.MetricsApiRequestsFailedTotal("conversation_sync", "timedout")
			logger.Error("sync: failed to submit message restart task", "error", currentErr)
		}
		// Throttle to avoid OOM spike
		time.Sleep(100 * time.Millisecond)
	}
}

// syncStuckEventAnalyses reconciles event analysis records that are stuck in IN_PROGRESS status.
func syncStuckEventAnalyses() error {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}
	repo := events.NewEventAnalysisRepository(dbManager)

	// Use a system request context for the job
	ctx := security.NewRequestContextForSuperAdmin()

	// Pending workflow tokens remain eligible after the last stage becomes
	// terminal, including when the immediate confirmation read failed.
	syncPendingEventAnalysisTerminals(ctx, dbManager, publishAnalysisCompletedTerminal)

	analyses, err := repo.ListInProgressAnalysis(ctx)
	if err != nil {
		return err
	}

	if len(analyses) == 0 {
		return nil
	}

	slog.Info("sync: found in-progress event analyses to reconcile", "count", len(analyses))

	// Group by (EventFingerprint, AccountId, isRCA) — not per-analysis-type.
	//
	// A single event investigation produces FOUR event_log_analysis rows (summary,
	// investigation, log_analysis, detailed_response) that are all handled by one
	// invocation of analyzeEventUsingAgentsAndUpdateDb. If we deduplicated by the
	// analysis_type column we would submit that same worker up to four times
	// per sync tick — each submission creates a fresh sub-agent dispatch (the
	// observed "duplicate k8s_debug messages" bug). RCA lives on a separate
	// session_id prefix and a separate worker function so it remains a
	// distinct dedup bucket.
	type key struct {
		eventFingerprint string
		accountId        string
		isRCA            bool
	}
	processed := make(map[key]bool)

	// How long a stuck IN_PROGRESS analysis is retried before being abandoned as
	// FAILED. Distinct from llm_server_event_analysis_freshness_hours, which
	// bounds how long a COMPLETED analysis may be reused for a new event sharing
	// its fingerprint — the two are natural to set to the same value but are
	// unrelated, and tuning that knob does not change stuck-run recovery.
	const maxRecoveryAge = 24 * time.Hour

	// Don't restart an analysis that was updated very recently — the in-flight
	// worker may briefly flip the parent conversation out of InProgress between
	// sub-agent dispatches. Without a floor, the sync tick races into that
	// window and submits a redundant worker. Tie the floor to the heartbeat
	// timeout so it scales with operator config; fall back to 60s when unset.
	minRecoveryAge := time.Duration(config.Config.ServerHeartBeatTimeoutSecond) * time.Second * 2
	if minRecoveryAge < 60*time.Second {
		minRecoveryAge = 60 * time.Second
	}

	// Cap the number of analyses recovered per cycle to prevent memory stampede on restart.
	// Remaining stuck analyses will be picked up in subsequent sync cycles.
	batchSize := config.Config.EventAnalysisRecoveryBatchSize
	if batchSize <= 0 {
		batchSize = 5
	}
	submitted := 0

	for _, a := range analyses {
		if submitted >= batchSize {
			slog.Info("sync: reached recovery batch limit, deferring remaining analyses to next cycle",
				"submitted", submitted, "total", len(analyses), "batch_size", batchSize)
			break
		}

		k := key{a.EventFingerprint, a.AccountId, isRCAAnalysisType(a.AnalysisType)}
		if a.AnalysisType == events.AnalysisTypeRCAAttempt {
			k.eventFingerprint = a.ID
		}
		if processed[k] {
			continue
		}
		processed[k] = true

		// Check budget before triggering recovery
		tenantId, err := security.GetTenantIdFromAccountId(a.AccountId)
		if err != nil {
			slog.Error("sync: unable to get tenant id for recovery", "error", err, "account_id", a.AccountId)
			continue
		}

		// Check conversation status
		parentSessionId := events.SessionIdPrefixEvent + a.EventFingerprint
		if isRCAAnalysisType(a.AnalysisType) {
			parentSessionId = rcaRecoverySession(a)
		}

		conv, err := core.GetConversationDao().GetConversationBySession(a.AccountId, parentSessionId)
		if err != nil {
			slog.Error("sync: failed to get conversation for event analysis", "error", err, "session", parentSessionId)
			continue
		}

		// If conversation is still actively running or waiting for a client tool response,
		// skip — the sync job must not restart it as the relay may still deliver the result.
		if conv.Status == core.ConversationStatusInProgress ||
			conv.Status == core.ConversationStatusWaiting ||
			conv.Status == core.ConversationStatusWaitingForClientTool {
			continue
		}

		// Don't restart an analysis whose row was touched recently. The parent
		// conversation can briefly flip out of InProgress between sub-agent
		// dispatches inside analyzeEventUsingAgentsAndUpdateDb — the conv-status
		// check above can race into that window and think the worker is dead.
		// The updated_at floor is a second guard that relies on
		// UpsertEventAnalysis(InProgress) bumping updated_at on every status
		// write (see events/event_analyzer_repository.go).
		if time.Since(a.UpdatedAt) < minRecoveryAge {
			continue
		}

		// If the analysis has been IN_PROGRESS for too long without a running or completed conversation,
		// mark it as FAILED to stop the infinite retry loop.
		// If the conversation reached COMPLETED, do not abandon it solely because of event-row age —
		// reconcile the completed findings into the event report (#37865).
		if conv.Status != core.ConversationStatusCompleted && time.Since(a.UpdatedAt) > maxRecoveryAge {
			slog.Warn("sync: marking stale event analysis as failed — exceeded max recovery age",
				"event_id", a.EventId, "session", parentSessionId, "conv_status", conv.Status,
				"updated_at", a.UpdatedAt, "age", time.Since(a.UpdatedAt).Round(time.Minute))
			failCtx := security.NewRequestContextForTenantAccountAdmin(tenantId, security.GetSystemUserId(), []string{a.AccountId})
			failResp, terminal, failErr := failAbandonedEventAnalysis(failCtx, a, dbManager)
			if failErr != nil {
				slog.Error("sync: failed to mark stale analysis as failed", "error", failErr, "event_id", a.EventId)
			} else if terminal {
				publishAnalysisCompletedTerminal(context.WithoutCancel(failCtx.GetContext()), a.AccountId, a.EventId, failResp, failResp.Status)
			}
			continue
		}

		budgetExceeded, budgetErrorMsg := budget.CheckBudgetLimits(tenantId, a.AccountId, budget.ModuleInvestigation, slog.Default())
		if budgetExceeded {
			slog.Warn("sync: budget limit exceeded for event analysis recovery, skipping", "event_id", a.EventId, "account_id", a.AccountId, "error", budgetErrorMsg)
			continue
		}

		slog.Info("sync: triggering recovery for stuck event analysis", "event_id", a.EventId, "session", parentSessionId, "conv_status", conv.Status)

		func() {
			submissionCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

			err := getEventAnalysisWorkerPool().Submit(submissionCtx, func() {
				defer cancel()
				newCtx := security.NewRequestContextForTenantAccountAdmin(tenantId, security.GetSystemUserId(), []string{a.AccountId})

				if isRCAAnalysisType(a.AnalysisType) {
					req := EventRCAAnalysisRequest{
						EventId:   a.EventId,
						AccountId: a.AccountId,
						UserId:    security.GetSystemUserId(),
					}
					if a.AnalysisType == events.AnalysisTypeRCAAttempt {
						req.AttemptID = a.ID
					} else {
						req.LegacyAnalysisID = a.ID
					}
					_, _ = analyzeEventRCAUsingAgentsAndUpdateDb(newCtx, req)
				} else {
					req := EventAnalysisRequest{
						EventId:   a.EventId,
						AccountId: a.AccountId,
						UserId:    security.GetSystemUserId(),
					}
					recoveredResp, recErr := analyzeEventUsingAgentsAndUpdateDb(newCtx, req)
					if recErr != nil {
						slog.Error("sync: event analysis recovery failed", "error", recErr, "event_id", a.EventId)
					}
					// A worker response cannot prove persistence or sibling-stage completion.
					// Use the same database confirmation as the normal MQ completion path.
					if confirmed, terminal := getConfirmedTerminalAnalysis(newCtx, req, dbManager); terminal {
						publishAnalysisCompletedTerminal(context.WithoutCancel(newCtx.GetContext()), a.AccountId, a.EventId, confirmed, confirmed.Status)
					} else {
						slog.Info("sync: recovery not confirmed terminal, preserving pending tokens",
							"event_id", a.EventId, "status", recoveredResp.Status)
					}
				}
			})
			if err != nil {
				cancel()
				slog.Error("sync: failed to submit event analysis recovery task", "error", err)
			}
		}()
		// Throttle to avoid OOM spike
		time.Sleep(100 * time.Millisecond)
		submitted++
	}

	slog.Info("sync: recovery cycle completed", "submitted", submitted, "total_stuck", len(analyses))
	return nil
}

// failAbandonedEventAnalysis persists abandonment before checking the normal pipeline.
// RCA uses a separate conversation and must never drain normal investigation tokens.
func failAbandonedEventAnalysis(ctx *security.RequestContext, analysis events.InProgressAnalysis, dbManager *common.DatabaseManager) (EventAnalysisResponse, bool, error) {
	repo := events.NewEventAnalysisRepository(dbManager)
	const reason = "recovery abandoned: analysis stuck for over 24 hours"
	switch analysis.AnalysisType {
	case events.AnalysisTypeRCAAttempt:
		return EventAnalysisResponse{}, false, repo.UpdateRCAAttemptStatus(ctx, analysis.ID, analysis.EventId, analysis.AccountId, string(events.AnalysisStatusFailed), reason)
	case events.AnalysisTypeRCA:
		return EventAnalysisResponse{}, false, repo.UpdateLegacyRCAStatus(ctx, analysis.ID, analysis.EventId, analysis.AccountId, string(events.AnalysisStatusFailed), reason)
	}
	if err := repo.UpdateEventAnalysisStatusById(ctx, analysis.ID, string(events.AnalysisStatusFailed), reason); err != nil {
		return EventAnalysisResponse{}, false, err
	}
	response, terminal := getConfirmedTerminalAnalysis(ctx, EventAnalysisRequest{EventId: analysis.EventId, AccountId: analysis.AccountId}, dbManager)
	return response, terminal, nil
}

var pendingAnalysisScan struct {
	sync.Mutex
	cursor uint64
}

// syncPendingEventAnalysisTerminals does not restart agents or consume tokens
// until the normal investigation pipeline is confirmed terminal in Postgres.
func syncPendingEventAnalysisTerminals(ctx *security.RequestContext, db *common.DatabaseManager, publish func(context.Context, string, string, EventAnalysisResponse, string)) {
	scanCtx, cancel := context.WithTimeout(ctx.GetContext(), 5*time.Second)
	defer cancel()
	// MATCH filters after Redis scans the shared database. Traverse multiple
	// pages per tick so unrelated cache keys do not postpone retries past the
	// token TTL, while bounding Redis work and wall time per recovery tick.
	pendingAnalysisScan.Lock()
	ids := make([]string, 0)
	seen := make(map[string]bool)
	for page := 0; page < 100; page++ {
		batch, next, err := common.ScanPendingTokenEvents(scanCtx, pendingAnalysisScan.cursor)
		if err != nil {
			ctx.GetLogger().Warn("sync: unable to enumerate pending investigation tokens", "error", err)
			break
		}
		pendingAnalysisScan.cursor = next
		for _, id := range batch {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if next == 0 || scanCtx.Err() != nil {
			break
		}
	}
	pendingAnalysisScan.Unlock()
	for _, id := range ids {
		reconcilePendingEventAnalysisTerminal(ctx, db, id, publish)
	}
}

func reconcilePendingEventAnalysisTerminal(ctx *security.RequestContext, db *common.DatabaseManager, eventID string, publish func(context.Context, string, string, EventAnalysisResponse, string)) {
	// Tokens already identify a globally unique event. Resolve its account from
	// the authoritative event row rather than changing the shared registry shape.
	var accountID string
	if err := db.Db.GetContext(ctx.GetContext(), &accountID, "SELECT cloud_account_id FROM events WHERE id = $1", eventID); err != nil {
		ctx.GetLogger().Warn("sync: unable to resolve pending investigation account", "event_id", eventID, "error", err)
		return
	}
	if response, terminal := getConfirmedTerminalAnalysis(ctx, EventAnalysisRequest{EventId: eventID, AccountId: accountID}, db); terminal {
		publish(context.WithoutCancel(ctx.GetContext()), accountID, eventID, response, response.Status)
	}
}
