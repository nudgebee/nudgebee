package queue

import (
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"

	"github.com/google/uuid"
)

// PublishKGUpdate publishes a KG update message for a tenant
func PublishKGUpdate(tenantID string, source string) error {
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
	)
}
