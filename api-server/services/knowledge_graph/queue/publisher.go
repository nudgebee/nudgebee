package queue

import (
	"context"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"

	"github.com/google/uuid"
)

// PublishKGUpdate publishes a KG update message for a tenant. ctx carries the
// caller's trace context so the consumer continues the same distributed trace.
func PublishKGUpdate(ctx context.Context, tenantID string, source string) error {
	message := KGUpdateMessage{
		TenantID:      tenantID,
		Source:        source,
		RequestedAt:   time.Now().UTC(),
		CorrelationID: uuid.New().String(),
	}

	return common.MqPublish(
		config.Config.RabbitMqKGUpdateExchange,
		config.Config.RabbitMqKGUpdateQueue,
		message,
		common.MqPublishWithContext(ctx),
	)
}
