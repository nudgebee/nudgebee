package queue

import (
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event/lifecycle"
)

// InvestigationCompletedEnvelope mirrors the shape llm-server's
// publishInvestigationCompleted / publishCompletionUnconditional emits
// (llm/llm-server/api/event_analyzer_mq.go) and runbook-server's mirror
// (runbook-server/internal/events/investigation_completion.go). Field names
// are coupled by JSON tag — if either side changes, all copies must move
// together.
type InvestigationCompletedEnvelope struct {
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

const (
	investigationStatusCompleted = "COMPLETED"
	investigationStatusFailed    = "FAILED"

	// Dedup marker namespace. The same completion may be delivered more than
	// once (llm-server fans out per task-token AND emits one unconditional
	// envelope), so we run the deferred processors at most once per event.
	investigationCompletedDedupNamespace = "investigation_completed_dedup"
	investigationCompletedDedupTTL       = 1 * time.Hour
)

var investigationCompletedDedupOnce sync.Once

// Indirection seams so processInvestigationCompleted is unit-testable without
// a live DB / MQ. Overridden in tests.
var (
	loadEventMapFn  = loadEventMap
	emitLifecycleFn = lifecycle.Emit
)

func init() {
	// Do not dial RabbitMQ during tests.
	if testing.Testing() {
		return
	}
	err := common.MqConsume(
		config.Config.RabbitMqEventInvestigateCompletedExchange,
		config.Config.RabbitMqEventInvestigateCompletedRoutingKey,
		config.Config.RabbitMqEventInvestigateCompletedQueue,
		config.Config.RabbitMqEventInvestigateCompletedConcurrency,
		processInvestigationCompleted,
	)
	if err != nil {
		slog.Error("investigation_completed_queue: failed to start consumer", "error", err)
	}
}

// processInvestigationCompleted reacts to an LLM investigation reaching a
// terminal state by emitting the investigation.completed / investigation.failed
// lifecycle phase. That fans out to the phase's in-process hooks (e.g.
// pagerduty_comment on completed) and publishes to the runbook exchange for
// workflows subscribed via params.on. The analysis result is passed as the
// lifecycle `extra` so hooks/workflows can use the summary / investigation.
// The primary notification is NOT here — it already fired at event.created; its
// AI-summary thread-reply is delivered separately (notifications-server, Phase 2).
//
// Returns nil on every path so the message is always ack'd — a dropped
// callback degrades to "the lifecycle phase didn't fire for this event",
// never an MQ redelivery storm.
func processInvestigationCompleted(data []byte) error {
	var env InvestigationCompletedEnvelope
	if err := common.UnmarshalJson(data, &env); err != nil {
		slog.Error("investigation_completed_queue: failed to unmarshal envelope", "error", err, "data", string(data))
		return nil // Never requeue malformed messages
	}

	if env.EventID == "" || env.AccountID == "" {
		slog.Warn("investigation_completed_queue: envelope missing event_id/account_id, dropping",
			"event_id", env.EventID, "account_id", env.AccountID)
		return nil
	}

	// Token-bearing envelopes belong to runbook-server's completion consumer
	// (it resumes the suspended workflow activity via the task_token). The
	// completion exchange is shared with one routing key, so every envelope is
	// copied to both queues; api-server must drop token-bearing ones the same
	// way runbook-server drops empty-token ones. api-server only owns the
	// no-token auto-summary path it deferred at creation — without this guard
	// it would run the deferred processors (workflow / pagerduty_comment) for
	// events that already ran them inline at creation (a runbook investigation
	// never goes through api-server's deferral).
	if env.TaskToken != "" {
		return nil
	}

	// Only act on terminal states. llm-server only publishes terminal
	// envelopes, but guard defensively against future shapes.
	if env.Status != investigationStatusCompleted && env.Status != investigationStatusFailed {
		slog.Info("investigation_completed_queue: non-terminal status, skipping",
			"event_id", env.EventID, "status", env.Status)
		return nil
	}

	logger := slog.Default().With("event_id", env.EventID, "account_id", env.AccountID)

	// Idempotency: run the deferred processors at most once per event. The
	// marker is set AFTER a successful run (below), not before — a transient
	// load/enrich failure must not leave a marker that suppresses a later
	// legitimate delivery for the same event. Two truly-concurrent deliveries
	// could still both pass this check and double-run; acceptable, matching
	// the rca_writeback dedup approach (and api-server normally receives only
	// one envelope per event after the task_token drop above).
	investigationCompletedDedupOnce.Do(func() {
		common.CacheCreateNamespace(investigationCompletedDedupNamespace,
			common.CacheNamespaceWithExpiration(investigationCompletedDedupTTL))
	})
	dedupKey := env.AccountID + ":" + env.EventID
	if _, found := common.CacheGet(investigationCompletedDedupNamespace, dedupKey); found {
		logger.Info("investigation_completed_queue: already processed, skipping duplicate", "status", env.Status)
		return nil
	}

	ctx, eventMap, err := loadEventMapFn(env.EventID, logger)
	if err != nil {
		// Distinguish permanent from transient failures. A missing event
		// (sql.ErrNoRows) is permanent — ACK so we don't requeue forever. Any
		// other error (DB / network blip) is transient — return it so RabbitMQ
		// redelivers and the lifecycle phase still fires once the event loads.
		// Safe to requeue: the dedup marker is set only AFTER a successful emit
		// below, so a retry here can never suppress the eventual successful run.
		if errors.Is(err, sql.ErrNoRows) {
			return nil // Already logged; event does not exist
		}
		return err // Transient — requeue for retry
	}

	// Pass the analysis result as lifecycle `extra` so hooks / workflows can use
	// it. On FAILED these are empty; the phase still fires (investigation.failed)
	// so the event isn't permanently dropped and on=investigation_failed
	// workflows can react.
	phase := lifecycle.PhaseInvestigationCompleted
	if env.Status == investigationStatusFailed {
		phase = lifecycle.PhaseInvestigationFailed
	}
	extra := map[string]any{
		"analysis_status":        env.Status,
		"analysis_summary":       env.Summary,
		"analysis_investigation": env.Investigation,
		"analysis_log_summary":   env.LogSummary,
		"analysis_log_analysis":  env.LogAnalysis,
	}
	if env.StatusReason != "" {
		extra["analysis_status_reason"] = env.StatusReason
	}

	logger.Info("investigation_completed_queue: emitting lifecycle phase", "phase", string(phase), "status", env.Status)
	emitLifecycleFn(ctx, phase, eventMap, extra)

	// Mark processed only after the deferred processors have run, so a
	// transient failure above doesn't permanently suppress this event.
	if err := common.CacheSet(investigationCompletedDedupNamespace, dedupKey, []byte("1"),
		common.CacheSetWithExpiration(investigationCompletedDedupTTL)); err != nil {
		logger.Warn("investigation_completed_queue: failed to set dedup marker (may double-run on redelivery)", "error", err)
	}

	return nil
}
