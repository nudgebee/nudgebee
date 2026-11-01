package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/notification"
	"nudgebee/services/security"
)

// Indirection seams so processEventPostProcessMessage is unit-testable without
// a live DB / MQ. Overridden in tests.
var (
	postProcessEventFn     = event.PostProcessEvent
	reEmitForWorkflowsFn   = event.ReEmitForWorkflows
	processEventResolvedFn = notification.ProcessEventResolved
	processEventReturnedFn = notification.ProcessEventReturned
)

func init() {
	err := common.MqConsume(
		config.Config.RabbitMqEventPostProcessExchange,
		config.Config.RabbitMqEventPostProcessQueue,
		config.Config.RabbitMqEventPostProcessQueue,
		config.Config.RabbitMqEventPostProcessConcurrency,
		processEventPostProcessMessage,
	)
	if err != nil {
		slog.Error("event_queue: failed to start consumer", "error", err)
	}
}

func processEventPostProcessMessage(msgCtx context.Context, data []byte) error {
	var message EventPostProcessMessage
	if err := common.UnmarshalJson(data, &message); err != nil {
		slog.Error("event_queue: failed to unmarshal message", "error", err)
		return nil // Don't requeue malformed messages
	}

	if message.EventID == "" {
		slog.Error("event_queue: message missing event_id")
		return nil
	}

	logger := slog.Default().With("event_id", message.EventID)

	ctx, eventMap, err := loadEventMapFn(msgCtx, message.EventID, logger)
	if err != nil {
		return nil // Already logged; don't requeue (malformed / missing event)
	}

	if message.Phase == EventPostProcessPhaseReturned {
		// RESOLVED→FIRING update of an event that had cleared: deliver the
		// returned notification only; the full pipeline ran on first insert.
		// Checked before workflow_only, which the producer also sets so that a
		// consumer predating phase re-delivers to workflows rather than running
		// the full pipeline again.
		if err := processEventReturnedFn(ctx, eventMap); err != nil {
			logger.Error("event_queue: failed to publish returned notification", "error", err)
		}
		return nil
	}

	if message.WorkflowOnly {
		// Re-fire of an existing event: re-deliver to event-trigger workflows only,
		// without re-running triage/llm/notification (which ran on first insert).
		reEmitForWorkflowsFn(ctx, eventMap)
		return nil
	}

	if message.NotifyResolved {
		// FIRING→RESOLVED update: deliver the resolved notification only; the
		// full pipeline ran on first insert.
		if err := processEventResolvedFn(ctx, eventMap); err != nil {
			logger.Error("event_queue: failed to publish resolved notification", "error", err)
		}
		return nil
	}

	// Run all event processors
	postProcessEventFn(ctx, eventMap)

	return nil
}

// errEventNotConvertible marks a loadEventMap failure that happened after the
// event row was fetched. The conversion reads nothing else, so a retry fails
// the same way: a consumer that requeues on error must ACK it instead.
var errEventNotConvertible = errors.New("event cannot be converted to a map")

// loadEventMap fetches the event by ID, builds a tenant-scoped request context,
// and converts the event to the map[string]any shape the event processors
// expect (the same format as RPC event.data.new). Evidences are cleared from
// the struct before conversion to avoid massive JSON marshal/unmarshal
// allocations — no processor reads evidence data from the map (triage
// re-fetches from DB, llm only checks existence, others forward metadata only);
// has_evidences records whether they existed. Shared by the post-process
// consumer and the investigation-completed consumer.
func loadEventMap(msgCtx context.Context, eventID string, logger *slog.Logger) (*security.RequestContext, map[string]any, error) {
	// Fail fast on an empty id rather than issuing a guaranteed-miss query.
	// Both callers already guard this, so it is defensive belt-and-suspenders;
	// the error is permanent (callers ACK), never requeued.
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		logger.Error("event_queue: eventID is empty")
		return nil, nil, fmt.Errorf("eventID is empty")
	}

	// Build request context (same as RPC webhook handler uses). msgCtx carries
	// the trace context extracted from the consumed message's headers, so the
	// trace continues from the publisher into event post-processing.
	ctx := security.NewRequestContext(
		msgCtx,
		security.NewSecurityContextForSuperAdmin(),
		logger, nil, nil,
	)

	eventObj, err := event.GetEvent(ctx, eventID)
	if err != nil {
		logger.Error("event_queue: failed to fetch event", "error", err)
		return nil, nil, err
	}

	// Rebuild context with tenant from the event so downstream queries have proper tenant scoping
	if eventObj.Tenant != nil && *eventObj.Tenant != "" {
		ctx = security.NewRequestContext(
			msgCtx,
			security.NewSecurityContextForTenantAdmin(*eventObj.Tenant),
			logger, nil, nil,
		)
	}

	hasEvidences := eventObj.Evidences != nil
	eventObj.Evidences = nil

	eventMap, err := structToMap(eventObj)
	if err != nil {
		logger.Error("event_queue: failed to convert event to map", "error", err)
		return nil, nil, fmt.Errorf("%w: %w", errEventNotConvertible, err)
	}

	eventMap["has_evidences"] = hasEvidences

	return ctx, eventMap, nil
}

// structToMap converts a struct to map[string]any using JSON round-trip.
// The JSON tags on models.Event match RPC column names.
func structToMap(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(data, &result)
	return result, err
}
