package webhook_queue

import (
	"log/slog"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	err := common.MqConsume(
		config.Config.RabbitMqWebhookProcessExchange,
		config.Config.RabbitMqWebhookProcessQueue,
		config.Config.RabbitMqWebhookProcessQueue,
		config.Config.RabbitMqWebhookProcessConcurrency,
		processWebhookMessage,
	)
	if err != nil {
		slog.Error("webhook_queue: failed to start consumer", "error", err)
	}
}

func processWebhookMessage(data []byte) error {
	var message WebhookProcessMessage
	if err := common.UnmarshalJson(data, &message); err != nil {
		slog.Error("webhook_queue: failed to unmarshal message", "error", err)
		return nil // Don't requeue malformed messages
	}

	if message.WebhookRowID == "" {
		slog.Error("webhook_queue: message missing webhook_row_id")
		return nil
	}

	logger := slog.Default().With("webhook_row_id", message.WebhookRowID)
	sc := security.NewRequestContextForSuperAdmin(logger, nil, nil)

	if err := core.ProcessStoredWebhook(sc, message.WebhookRowID); err != nil {
		logger.Error("webhook_queue: failed to process stored webhook", "error", err)
		return nil // Don't requeue — errors are handled internally
	}

	return nil
}
