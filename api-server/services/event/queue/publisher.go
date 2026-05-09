package queue

import (
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
)

// PublishEventPostProcess publishes an event ID for async post-processing
func PublishEventPostProcess(eventID string) error {
	message := EventPostProcessMessage{
		EventID: eventID,
	}

	return common.MqPublish(
		config.Config.RabbitMqEventPostProcessExchange,
		config.Config.RabbitMqEventPostProcessQueue,
		message,
		common.MqPublishWithExpiration(1*time.Hour),
	)
}
