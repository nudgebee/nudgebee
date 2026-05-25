package memory

import (
	"context"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/memory/stores/eventlog"
	"sync"
	"time"
)

var (
	projPoolOnce sync.Once
	projPool     *common.WorkerPool
)

// projectionPool returns the shared projection worker pool, creating it on first use.
// Pool size is controlled by config.MemoryProjectionWorkers (default 4).
func projectionPool() *common.WorkerPool {
	projPoolOnce.Do(func() {
		n := config.Config.MemoryProjectionWorkers
		if n <= 0 {
			n = 4
		}
		projPool = common.NewWorkerPool("memory_projection", n, 1024)
	})
	return projPool
}

// Observe records what happened in a turn. The event is written to the log
// synchronously (one INSERT, typically sub-ms) for audit-trail durability.
// Projection onto typed stores (e.g. pattern extraction) runs async on the
// projection worker pool and is fire-and-forget — failures are logged but
// don't fail the caller.
func (m *impl) Observe(ctx context.Context, req ObserveRequest) error {
	if !config.Config.MemoryModuleEnabled {
		return nil
	}
	if !isTenantAllowed(req.TenantID) {
		return nil
	}

	evt := buildEventFromRequest(req)

	if err := eventlog.Append(evt); err != nil {
		slog.Warn("memory.Observe: event log append failed",
			"error", err, "tenant", req.TenantID, "user", req.UserID, "event_type", req.EventType)
		return err
	}

	// Dispatch projection asynchronously. Projection errors are swallowed; the
	// event is durable in the log and can be replayed.
	submitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = projectionPool().Submit(submitCtx, func() {
		project(context.Background(), evt)
	})
	return nil
}

// buildEventFromRequest maps the public ObserveRequest to the event-log row.
func buildEventFromRequest(req ObserveRequest) eventlog.Event {
	evt := eventlog.Event{
		TenantID:  req.TenantID,
		EventType: req.EventType,
		Payload:   eventlog.MarshalPayload(req.Payload),
		ActorKind: req.ActorKind,
		CreatedAt: time.Now(),
	}
	if req.UserID != "" {
		uid := req.UserID
		evt.UserID = &uid
	}
	if req.AgentModule != "" {
		mod := req.AgentModule
		evt.AgentModule = &mod
	}
	if req.ActorID != "" {
		aid := req.ActorID
		evt.ActorID = &aid
	}
	if req.IdempotencyKey != "" {
		key := req.IdempotencyKey
		evt.IdempotencyKey = &key
	}
	return evt
}

// project applies an event to relevant typed stores. Phase 1: only
// cache-invalidation side effects for soul and preferences events (stores are
// already updated synchronously in the Mutate path). Future phases add Patterns,
// Decisions, Collective projections here.
func project(ctx context.Context, evt eventlog.Event) {
	switch evt.EventType {
	case eventlog.EventTypeSoulUpdated, eventlog.EventTypeSoulCleared:
		if evt.UserID != nil {
			invalidateSoulCache(evt.TenantID, *evt.UserID)
		}
	case eventlog.EventTypePreferenceSet, eventlog.EventTypePreferenceCleared:
		if evt.UserID != nil {
			invalidatePrefsCache(evt.TenantID, *evt.UserID)
		}
	case eventlog.EventTypeFactExtracted:
		// Extractor handed us a legacy-shaped fact. The classifier maps it
		// onto a typed store (patterns / decisions / collective / preferences)
		// and projectFactFromEvent writes it. Failures are logged; the event
		// remains durable in the log and can be replayed.
		if err := projectFactFromEvent(ctx, evt); err != nil {
			slog.Warn("memory.project: fact.extracted projection failed",
				"error", err, "tenant", evt.TenantID)
		}
	default:
		// Phase 1 ignores other event types. They still land in the log and can
		// be replayed by Phase 2+ projections.
	}
}
