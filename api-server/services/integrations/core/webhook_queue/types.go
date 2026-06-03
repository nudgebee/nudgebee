package webhook_queue

// WebhookProcessMessage is the message published to RabbitMQ for async webhook processing.
type WebhookProcessMessage struct {
	WebhookRowID string `json:"webhook_row_id"`
}
