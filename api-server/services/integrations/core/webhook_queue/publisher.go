package webhook_queue

import (
	"context"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
)

// PublishWebhookProcess publishes a webhook row ID for async processing.
// ctx carries the ingest request's trace context so the consumer continues
// the same distributed trace.
func PublishWebhookProcess(ctx context.Context, webhookRowID string) error {
	message := WebhookProcessMessage{
		WebhookRowID: webhookRowID,
	}

	return common.MqPublish(
		config.Config.RabbitMqWebhookProcessExchange,
		config.Config.RabbitMqWebhookProcessQueue,
		message,
		common.MqPublishWithExpiration(1*time.Hour),
		common.MqPublishWithContext(ctx),
	)
}
