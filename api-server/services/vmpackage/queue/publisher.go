package queue

import (
	"context"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"

	"github.com/google/uuid"
)

// PublishVMScan publishes a request to scan one discovery-type integration.
// ctx carries the caller's trace context so the consumer continues the same
// distributed trace.
func PublishVMScan(ctx context.Context, integrationID, tenantID, accountID, source string) error {
	message := VMScanMessage{
		IntegrationID: integrationID,
		TenantID:      tenantID,
		AccountID:     accountID,
		Source:        source,
		RequestedAt:   time.Now().UTC(),
		CorrelationID: uuid.New().String(),
	}

	return common.MqPublish(
		config.Config.RabbitMqVMScanExchange,
		config.Config.RabbitMqVMScanQueue,
		message,
		common.MqPublishWithContext(ctx),
	)
}
