package events

import (
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

// WorkflowExecutor defines the interface for executing workflows.
// This decouples the consumer from the concrete Service implementation.
type WorkflowExecutor interface {
	ExecuteWorkflow(ctx *security.RequestContext, accountId, id string, triggerType model.WorkflowTrigger, inputs map[string]any) (string, error)
}

type Consumer struct {
	registry *EventRegistry
	executor WorkflowExecutor
	logger   *slog.Logger
}

func NewConsumer(registry *EventRegistry, executor WorkflowExecutor, logger *slog.Logger) *Consumer {
	return &Consumer{
		registry: registry,
		executor: executor,
		logger:   logger,
	}
}

// Start begins consuming messages from the configured exchange/queue.
// It uses common.MqConsume which runs the consumer loop in a goroutine.
func (c *Consumer) Start(exchange, routingKey, queue string) error {
	c.logger.Info("starting event consumer", "exchange", exchange, "queue", queue)

	return common.MqConsume(exchange, routingKey, queue, c.ProcessMessage)
}

func (c *Consumer) ProcessMessage(data []byte) error {
	var event map[string]any
	if err := common.UnmarshalJson(data, &event); err != nil {
		c.logger.Error("failed to parse event message", "error", err, "data", string(data))
		return nil
	}

	eventType := ""
	if et, ok := event["event_type"].(string); ok {
		eventType = et
	} else if ak, ok := event["aggregation_key"].(string); ok {
		eventType = ak
	}

	if eventType == "" {
		c.logger.Warn("event message missing event_type, aggregation_key, or type field", "data", string(data))
		return nil // Ack and drop
	}

	// Internal events (sourced from the events table) carry `aggregation_key` but
	// no `event_type` field. Trigger filters are authored against `event.event_type`
	// (e.g. {{ event.event_type == "Anomaly" }}), so normalize it here from the
	// resolved eventType — which already falls back to aggregation_key — so those
	// filters evaluate correctly for every event source (k8s alerts, anomalies, etc.).
	event["event_type"] = eventType

	// Extract account_id for tenancy check
	accountID := ""
	if aid, ok := event["account_id"].(string); ok {
		accountID = aid
	} else if caid, ok := event["cloud_account_id"].(string); ok {
		accountID = caid
	}

	if accountID == "" {
		c.logger.Warn("event message missing account_id or cloud_account_id, cannot route to tenant workflows", "data", string(data))
		return nil // Ack and drop, or maybe dlq? For now drop.
	}

	// 1. Match Workflows
	matches := c.registry.Match(eventType, accountID, event)
	if len(matches) == 0 {
		return nil
	}

	c.logger.Info("event matched workflows", "event_type", eventType, "match_count", len(matches))

	// 2. Execute Workflows
	for _, rule := range matches {
		reqCtx := security.NewRequestContextForTenantAccountAdmin(rule.TenantID, "00000000-0000-0000-0000-000000000000", []string{rule.AccountID})

		// Wrap the event payload in an "event" key so it's accessible as a single object in the workflow inputs.
		inputs := map[string]any{
			"event": event,
		}

		triggerType := rule.TriggerType
		if triggerType == "" {
			triggerType = model.WorkflowTriggerEvent
		}
		runID, err := c.executor.ExecuteWorkflow(reqCtx, rule.AccountID, rule.WorkflowID, triggerType, inputs)
		if err != nil {
			c.logger.Error("failed to execute matched workflow",
				"workflow_id", rule.WorkflowID,
				"event_type", eventType,
				"event_id", event["id"],
				"error", err,
			)
		} else {
			c.logger.Info("triggered workflow", "workflow_id", rule.WorkflowID, "run_id", runID)
		}
	}

	return nil
}
