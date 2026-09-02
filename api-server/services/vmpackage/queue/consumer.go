package queue

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/vmpackage"
)

// vmScanProcessingTimeout bounds one datasource's sweep -> inventory ->
// vuln-match -> persist run. Sweep and batch inventory each carry their own
// multi-minute relay timeouts (see vmpackage/discovery.go), so this needs
// enough headroom for both plus the vuln-matcher call and persistence.
const vmScanProcessingTimeout = 20 * time.Minute

func init() {
	err := common.MqConsume(
		config.Config.RabbitMqVMScanExchange,
		config.Config.RabbitMqVMScanQueue,
		config.Config.RabbitMqVMScanQueue,
		config.Config.RabbitMqVMScanConcurrency,
		processVMScanMessage,
	)
	if err != nil {
		slog.Error("vmpackage_queue: failed to start consumer", "error", err)
	}
}

func processVMScanMessage(msgCtx context.Context, data []byte) error {
	var message VMScanMessage
	if err := common.UnmarshalJson(data, &message); err != nil {
		slog.Error("vmpackage_queue: failed to unmarshal message", "error", err)
		return nil // Don't requeue malformed messages
	}

	logger := common.LoggerWithTrace(msgCtx, slog.Default()).With(
		"tenant_id", message.TenantID,
		"account_id", message.AccountID,
		"integration_id", message.IntegrationID,
		"correlation_id", message.CorrelationID,
	)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		logger.Error("vmpackage_queue: failed to get database manager", "error", err)
		return nil // Don't requeue on DB errors, log and move on
	}

	// Re-resolve current state rather than trusting the message: labels may
	// have changed (or the integration may have been disabled/removed)
	// between publish and consume.
	ds, ok, err := vmpackage.GetDiscoveryDatasourceByID(dbms, message.IntegrationID, message.AccountID)
	if err != nil {
		logger.Error("vmpackage_queue: failed to resolve discovery datasource", "error", err)
		return nil
	}
	if !ok {
		logger.Info("vmpackage_queue: skipping - datasource no longer eligible")
		return nil
	}

	ctx, cancel := context.WithTimeout(msgCtx, vmScanProcessingTimeout)
	defer cancel()
	reqCtx := security.NewRequestContext(ctx, security.NewSecurityContextForTenantAdmin(message.TenantID), logger, nil, nil)

	logger.Info("vmpackage_queue: starting discovery scan")
	vmpackage.ScanDiscoveryDatasource(reqCtx, dbms, ds)
	logger.Info("vmpackage_queue: completed discovery scan")
	return nil
}
