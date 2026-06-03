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
		if message.MessageType == "followup" {
			return false
		}
		if message.WorkerName != nil && (*message.WorkerName == "localhost" || *message.WorkerName == "127.0.0.1" || *message.WorkerName == "0.0.0.0" || *message.WorkerName == "::" || strings.Contains(*message.WorkerName, ".local")) {
			return false
		}
		return true
	})

	slog.Info("sync: restarting conversation messages for dead workers", "count", len(messages))
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
	return nil
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

		k := key{a.EventFingerprint, a.AccountId, a.AnalysisType == events.AnalysisTypeRCA}
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
		if a.AnalysisType == events.AnalysisTypeRCA {
			parentSessionId = events.SessionIdPrefixEventRCA + a.EventFingerprint
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

		// If the analysis has been IN_PROGRESS for too long without a running conversation,
		// mark it as FAILED to stop the infinite retry loop.
		if time.Since(a.UpdatedAt) > maxRecoveryAge {
			slog.Warn("sync: marking stale event analysis as failed — exceeded max recovery age",
				"event_id", a.EventId, "session", parentSessionId, "conv_status", conv.Status,
				"updated_at", a.UpdatedAt, "age", time.Since(a.UpdatedAt).Round(time.Minute))
			failCtx := security.NewRequestContextForTenantAccountAdmin(tenantId, security.GetSystemUserId(), []string{a.AccountId})
			if updateErr := repo.UpdateEventAnalysisStatus(failCtx, a.EventFingerprint, a.AccountId, a.EventAggregationKey, string(events.AnalysisStatusFailed), "recovery abandoned: analysis stuck for over 24 hours", a.AnalysisType); updateErr != nil {
				slog.Error("sync: failed to mark stale analysis as failed", "error", updateErr, "event_id", a.EventId)
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

				if a.AnalysisType == events.AnalysisTypeRCA {
					req := EventRCAAnalysisRequest{
						EventId:   a.EventId,
						AccountId: a.AccountId,
						UserId:    security.GetSystemUserId(),
					}
					_, _ = analyzeEventRCAUsingAgentsAndUpdateDb(newCtx, req)
				} else {
					req := EventAnalysisRequest{
						EventId:   a.EventId,
						AccountId: a.AccountId,
						UserId:    security.GetSystemUserId(),
					}
					_, _ = analyzeEventUsingAgentsAndUpdateDb(newCtx, req)
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
