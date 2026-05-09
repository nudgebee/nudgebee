package api

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// investigationCompletedEnvelope is published to the
// rabbit_mq_event_investigate_completed_exchange whenever a troubleshoot
// request that originated from a runbook-server workflow activity reaches
// a terminal state. The TaskToken correlates the message back to the
// suspended Temporal activity; runbook-server resumes it via
// CompleteActivity. Only published when EventAnalysisRequest.TaskToken is
// non-empty.
type investigationCompletedEnvelope struct {
	TaskToken     string `json:"task_token"`
	EventID       string `json:"event_id"`
	AccountID     string `json:"account_id"`
	Status        string `json:"status"`
	Summary       string `json:"summary,omitempty"`
	LogSummary    string `json:"log_summary,omitempty"`
	LogAnalysis   string `json:"log_analysis,omitempty"`
	Investigation string `json:"investigation,omitempty"`
	StatusReason  string `json:"status_reason,omitempty"`
	Error         string `json:"error,omitempty"`
}

// publishInvestigationCompleted emits a completion envelope to the
// configured exchange. Best-effort: failures are logged but never bubble
// up — they would only delay the suspended workflow until its activity
// timeout, which is the intended fallback for any signal-loss scenario.
func publishInvestigationCompleted(env investigationCompletedEnvelope) {
	if env.TaskToken == "" {
		return
	}
	exch := config.Config.RabbitMqEventInvestigateCompletedExchange
	rk := config.Config.RabbitMqEventInvestigateCompletedRoutingKey
	if exch == "" || rk == "" {
		slog.Warn("eventasync: completion exchange not configured, skipping publish",
			"event_id", env.EventID, "account_id", env.AccountID)
		return
	}
	if err := common.MqPublish(exch, rk, env); err != nil {
		slog.Error("eventasync: failed to publish investigation completion",
			"error", err, "event_id", env.EventID, "account_id", env.AccountID)
	}
}

func init() {
	// do not connect with RabbitMQ while doing testing
	if testing.Testing() {
		return
	}
	err := common.MqConsume(config.Config.RabbitMqTroubleshootExchange, config.Config.RabbitMqTroubleshootQueue, config.Config.RabbitMqTroubleshootQueue, processTroubleshootingEventFromMq)
	if err != nil {
		slog.Error("unable to register consumer", "error", err)
	}
	slog.Info("eventasync: consumer registered successfully to rabbitmq")
}

func processTroubleshootingEventFromMq(data []byte) error {
	// Track metadata for panic recovery cleanup
	var response EventAnalysisResponse
	var accountId string
	var ctx *security.RequestContext

	// State for the workflow-completion publish (deferred below). When the
	// inbound request carries a task_token, we MUST emit a completion
	// envelope on every terminal path so the runbook-server activity is
	// either resumed or fails fast — except for the IN_PROGRESS / WAITING /
	// PENDING / cache-hit-by-other-worker cases, which are handled by
	// whichever worker actually ran the analysis.
	var (
		taskToken    string
		publishState investigationCompletedEnvelope
		skipPublish  bool
	)

	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("recovered from panic: %v", r)
			slog.Error("eventasync: unable to process event, recovering", "error", panicErr, "data", string(data), "stack", string(debug.Stack()))

			// Mark all analysis types as FAILED to prevent stuck IN_PROGRESS state
			if ctx != nil && accountId != "" && response.EventFingerprint != "" {
				if dbManager, dbErr := common.GetDatabaseManager(common.Metastore); dbErr == nil {
					markAllAnalysisFailed(ctx, events.NewEventAnalysisRepository(dbManager), response.EventFingerprint, accountId, response.EventAggregationKey, panicErr.Error())
				}
			}

			publishState.Status = string(events.AnalysisStatusFailed)
			publishState.Error = panicErr.Error()
		}

		// 1. Publish for the inbound request's own token, when present
		//    and not in a "skip" state (IN_PROGRESS / WAITING / PENDING /
		//    unparseable request).
		if taskToken != "" && !skipPublish {
			publishState.TaskToken = taskToken
			publishInvestigationCompleted(publishState)
		}

		// 2. Fan-out to any pending tokens registered by workers that
		//    arrived during this analysis run with a non-empty token but
		//    saw IN_PROGRESS. We are the worker that finished the
		//    pipeline (or hit a terminal failure), so the envelope built
		//    above is the canonical result for everyone waiting on this
		//    event_id. Skipped when this worker itself is in a skip
		//    state — in that case some other worker is the real producer
		//    and will run this drain when it finishes.
		if !skipPublish && publishState.EventID != "" {
			pending, derr := common.DrainPendingTokens(context.Background(), publishState.EventID)
			if derr != nil {
				slog.Error("eventasync: drain pending tokens failed",
					"error", derr, "event_id", publishState.EventID)
			}
			for _, t := range pending {
				env := publishState
				env.TaskToken = t
				publishInvestigationCompleted(env)
			}
		}
	}()

	common.MetricsApiRequestsTotal("event_analyzer_mq")

	slog.Info("eventasync: processing mq request", "data", string(data))

	eventAnalysisRequest := EventAnalysisRequest{}
	err := common.UnmarshalJson(data, &eventAnalysisRequest)
	if err != nil {
		slog.Error("eventasync: unable to get serialize request", "error", err, "data", string(data))
		// No token available — request body itself is unreadable.
		skipPublish = true
		return nil
	}
	taskToken = eventAnalysisRequest.TaskToken
	publishState.EventID = eventAnalysisRequest.EventId
	publishState.AccountID = eventAnalysisRequest.AccountId

	if eventAnalysisRequest.AccountId == "" || eventAnalysisRequest.EventId == "" {
		slog.Error("eventasync: unable to process request as accountId/eventId is empty", "data", string(data))
		publishState.Status = string(events.AnalysisStatusFailed)
		publishState.Error = "account_id or event_id is empty"
		return nil
	}
	accountId = eventAnalysisRequest.AccountId

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("eventasync: unable to get db connections", "error", err)
		publishState.Status = string(events.AnalysisStatusFailed)
		publishState.Error = "db unavailable: " + err.Error()
		return nil
	}
	tenantId, err := security.GetTenantIdFromAccountId(eventAnalysisRequest.AccountId)
	if err != nil {
		slog.Error("eventasync: unable to get tenant id", "error", err)
		publishState.Status = string(events.AnalysisStatusFailed)
		publishState.Error = "tenant lookup failed: " + err.Error()
		return nil
	}

	ctx = security.NewRequestContextForTenantAdmin(tenantId)
	response, err = getOrCreateEventAnalysisStatus(ctx, eventAnalysisRequest, dbManager, true)
	if err != nil {
		ctx.GetLogger().Error("eventasync: unable to get or create event analysis status", "error", err)
		publishState.Status = string(events.AnalysisStatusFailed)
		publishState.Error = "analysis status init failed: " + err.Error()
		return nil
	}

	if strings.EqualFold(response.Status, string(core.ConversationStatusCompleted)) {
		ctx.GetLogger().Info("eventasync: event is already analyzed once", "relatedEventId", response.RelatedEventId)
		// Cache hit — publish the cached result so the workflow can resume.
		fillPublishStateFromResponse(&publishState, response, string(events.AnalysisStatusCompleted))
		return nil
	}

	if strings.EqualFold(response.Status, string(core.ConversationStatusInProgress)) {
		ctx.GetLogger().Info("eventasync: event is already being analyzed", "relatedEventId", response.RelatedEventId)
		// Multi-listener: another worker owns the in-progress analysis
		// and will publish the eventual completion. If we have a token,
		// register it in the pending list so that worker's drain at
		// completion time fans out to us as well.
		if taskToken != "" {
			if rerr := common.RegisterPendingToken(ctx.GetContext(), eventAnalysisRequest.EventId, taskToken); rerr != nil {
				ctx.GetLogger().Warn("eventasync: failed to register pending token, falling back to skip",
					"error", rerr, "event_id", eventAnalysisRequest.EventId)
				skipPublish = true
				return nil
			}
			// Race guard: the in-progress run could have reached a
			// terminal state (COMPLETED or FAILED) between our status
			// read and the token registration. Re-read; if it has, pop
			// our own token (so the eventual drain doesn't double-
			// publish) and fall through to the normal defer publish
			// path with the appropriate status.
			updated, uerr := getOrCreateEventAnalysisStatus(ctx, eventAnalysisRequest, dbManager, false)
			if uerr != nil {
				// Re-read failed — fall through to skipPublish=true and
				// rely on the eventual drain by whichever worker is
				// running the pipeline. Logged but not fatal.
				ctx.GetLogger().Warn("eventasync: race-guard re-read failed, falling through to skip",
					"error", uerr, "event_id", eventAnalysisRequest.EventId)
			} else if strings.EqualFold(updated.Status, string(core.ConversationStatusCompleted)) {
				if popped, _ := common.RemovePendingToken(ctx.GetContext(), eventAnalysisRequest.EventId, taskToken); popped {
					fillPublishStateFromResponse(&publishState, updated, string(events.AnalysisStatusCompleted))
					return nil
				}
				// Lost the race — drain already grabbed our token; that
				// other worker will publish for us. Skip our own publish.
			} else if strings.EqualFold(updated.Status, string(events.AnalysisStatusFailed)) {
				if popped, _ := common.RemovePendingToken(ctx.GetContext(), eventAnalysisRequest.EventId, taskToken); popped {
					publishState.Status = string(events.AnalysisStatusFailed)
					publishState.StatusReason = updated.StatusReason
					publishState.Error = updated.StatusReason
					return nil
				}
				// Same lost-race fallback as the COMPLETED branch.
			}
		}
		skipPublish = true
		return nil
	}

	if strings.EqualFold(response.Status, string(core.ConversationStatusWaiting)) {
		ctx.GetLogger().Info("eventasync: event is in waiting state, skipping", "relatedEventId", response.RelatedEventId)
		skipPublish = true
		return nil
	}

	if strings.EqualFold(response.Status, string(core.ConversationStatusPending)) {
		ctx.GetLogger().Info("eventasync: event is in pending state, skipping", "relatedEventId", response.RelatedEventId)
		skipPublish = true
		return nil
	}

	// Check budget limits for event analysis from MQ
	budgetExceeded, budgetErrorMsg := budget.CheckBudgetLimits(ctx.GetSecurityContext().GetTenantId(), eventAnalysisRequest.AccountId, budget.ModuleInvestigation, ctx.GetLogger())
	if budgetExceeded {
		ctx.GetLogger().Warn("eventasync: budget limit exceeded, skipping event analysis", "eventId", eventAnalysisRequest.EventId, "message", budgetErrorMsg)
		// Update status to failed with budget exceeded message for all analysis types
		markAllAnalysisFailed(ctx, events.NewEventAnalysisRepository(dbManager), response.EventFingerprint, eventAnalysisRequest.AccountId, response.EventAggregationKey, budgetErrorMsg)
		publishState.Status = string(events.AnalysisStatusFailed)
		publishState.StatusReason = budgetErrorMsg
		publishState.Error = budgetErrorMsg
		return nil
	}

	ctx.GetLogger().Info("eventasync: starting event analysis", "eventId", eventAnalysisRequest.EventId)
	common.MetricsEventAnalysisOperationsTotal("mq_investigation", "start", eventAnalysisRequest.AccountId)
	mqStart := time.Now()
	eventAnalysisRequest.Source = string(core.ConversationSourceInstantNotification)
	analysisResp, err := analyzeEventUsingAgentsAndUpdateDb(ctx, eventAnalysisRequest)
	if err != nil {
		ctx.GetLogger().Error("eventasync: unable to analyze event", "eventId", eventAnalysisRequest.EventId, "error", err)
		common.MetricsEventAnalysisOperationsTotal("mq_investigation", "fail", eventAnalysisRequest.AccountId)
		// Mark all analysis types as FAILED to prevent stuck IN_PROGRESS state
		markAllAnalysisFailed(ctx, events.NewEventAnalysisRepository(dbManager), response.EventFingerprint, eventAnalysisRequest.AccountId, response.EventAggregationKey, err.Error())
		publishState.Status = string(events.AnalysisStatusFailed)
		publishState.Error = err.Error()
	} else {
		common.MetricsEventAnalysisOperationsTotal("mq_investigation", "success", eventAnalysisRequest.AccountId)
		fillPublishStateFromResponse(&publishState, analysisResp, string(events.AnalysisStatusCompleted))
	}
	common.MetricsEventAnalysisLatencySeconds("mq_investigation", eventAnalysisRequest.AccountId, time.Since(mqStart).Seconds())

	return nil
}

// fillPublishStateFromResponse copies the canonical investigation outputs
// from an EventAnalysisResponse into the completion envelope. Summary
// uses DetailedResponse (Step 4 synthesis output, the canonical answer)
// when present and falls back to Summary (Step 1) for events that only
// have early-pipeline data — e.g. when debug analysis was disabled and
// detailed_response was set to the initial summary.
func fillPublishStateFromResponse(env *investigationCompletedEnvelope, resp EventAnalysisResponse, status string) {
	env.Status = status
	env.StatusReason = resp.StatusReason
	env.Summary = resp.DetailedResponse
	if env.Summary == "" {
		env.Summary = resp.Summary
	}
	env.LogSummary = resp.Summary
	env.LogAnalysis = resp.Analysis
	env.Investigation = resp.Investigation
}
