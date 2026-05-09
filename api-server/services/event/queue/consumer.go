package queue

import (
	"context"
	"encoding/json"
	"log/slog"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event"
	"nudgebee/services/security"
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

func processEventPostProcessMessage(data []byte) error {
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

	// Build request context (same as Hasura webhook handler uses)
	ctx := security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForSuperAdmin(),
		logger, nil, nil,
	)

	// Fetch the full event from DB
	eventObj, err := event.GetEvent(ctx, message.EventID)
	if err != nil {
		logger.Error("event_queue: failed to fetch event", "error", err)
		return nil // Don't requeue — event may not exist
	}

	// Rebuild context with tenant from the event so downstream queries have proper tenant scoping
	if eventObj.Tenant != nil && *eventObj.Tenant != "" {
		ctx = security.NewRequestContext(
			context.Background(),
			security.NewSecurityContextForSuperAdminAndTenant(*eventObj.Tenant),
			logger, nil, nil,
		)
	}

	// Record whether evidences exist before clearing — llm processor uses this
	// to gate event processing.
	hasEvidences := eventObj.Evidences != nil

	// Clear evidences before structToMap to avoid massive JSON marshal/unmarshal
	// allocations. No processor reads evidence data from the map: triage re-fetches
	// from DB, llm only checks existence, others forward metadata only.
	eventObj.Evidences = nil

	// Convert Event struct to map[string]any (same format as Hasura event.data.new)
	eventMap, err := structToMap(eventObj)
	if err != nil {
		logger.Error("event_queue: failed to convert event to map", "error", err)
		return nil
	}

	eventMap["has_evidences"] = hasEvidences

	// Run all event processors
	event.PostProcessEvent(ctx, eventMap)

	return nil
}

// structToMap converts a struct to map[string]any using JSON round-trip.
// The JSON tags on models.Event match Hasura column names.
func structToMap(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(data, &result)
	return result, err
}
