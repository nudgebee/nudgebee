package webhook_queue

import (
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
)

// PublishWebhookProcess publishes a webhook row ID for async processing.
func PublishWebhookProcess(webhookRowID string) error {
	message := WebhookProcessMessage{
		WebhookRowID: webhookRowID,
	}

	return common.MqPublish(
		config.Config.RabbitMqWebhookProcessExchange,
		config.Config.RabbitMqWebhookProcessQueue,
		message,
		common.MqPublishWithExpiration(1*time.Hour),
	)
}
