package queue

import (
	"context"
	"log/slog"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	kgmodels "nudgebee/services/knowledge_graph/models"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// kgProcessingLockDuration is the window within which a new message for the same tenant
// is dropped if a previous processing run has already started. This prevents concurrent
// same-tenant graph builds, which cause PostgreSQL deadlocks on knowledge_graph_node upserts.
const kgProcessingLockDuration = time.Hour

func init() {
	err := common.MqConsume(
		config.Config.RabbitMqKGUpdateExchange,
		config.Config.RabbitMqKGUpdateQueue,
		config.Config.RabbitMqKGUpdateQueue,
		config.Config.RabbitMqKGUpdateConcurrency,
		processKGUpdateMessage,
	)
	if err != nil {
		slog.Error("kg_queue: failed to start consumer", "error", err)
	}
}

func processKGUpdateMessage(data []byte) error {
	// 1. Unmarshal message
	var message KGUpdateMessage
	if err := common.UnmarshalJson(data, &message); err != nil {
		slog.Error("kg_queue: failed to unmarshal message", "error", err)
		return nil // Don't requeue malformed messages
	}

	logger := slog.Default().With(
		"tenant_id", message.TenantID,
		"source", message.Source,
		"correlation_id", message.CorrelationID,
	)

	// 2. Atomically claim processing slot for this tenant (DB-level row lock).
	// Drops the message if another worker started processing this tenant within the lock window.
	claimed, err := tryClaimTenantProcessing(message.TenantID, logger)
	if err != nil {
		logger.Error("kg_queue: failed to claim tenant processing slot", "error", err)
		return nil // Don't requeue on DB errors, log and move on
	}
	if !claimed {
		logger.Info("kg_queue: skipping - processing already started within lock window")
		return nil
	}

	// 3. Process KG update for tenant
	logger.Info("kg_queue: starting KG update for tenant")
	if err := processKGUpdateForTenant(message.TenantID, message.CorrelationID, logger); err != nil {
		logger.Error("kg_queue: failed to process KG update", "error", err)
		return nil // Ack to prevent infinite retry, error is logged
	}

	logger.Info("kg_queue: successfully completed KG update")
	return nil
}

// tryClaimTenantProcessing atomically acquires a DB-level row lock on the tenant's filter
// rows and stamps last_process_started_at = NOW(). Returns false if another worker already
// claimed processing within kgProcessingLockDuration (works across all replicas).
func tryClaimTenantProcessing(tenantID string, logger *slog.Logger) (bool, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, err
	}
	filterRepo := core.NewFilterRepository(dbms, logger)
	return filterRepo.TryClaimTenantProcessing(tenantID, kgProcessingLockDuration)
}

// processKGUpdateForTenant processes all enabled filters for a specific tenant
func processKGUpdateForTenant(tenantID string, correlationID string, logger *slog.Logger) error {
	// Get database manager
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Create context with timeout
	timeout := time.Duration(config.Config.KGUpdateProcessingTimeoutMinutes) * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create security context for tenant
	reqCtx := security.NewRequestContextForTenantAdmin(tenantID, logger, nil, nil)
	reqCtx = security.NewRequestContext(ctx, reqCtx.GetSecurityContext(), logger, nil, nil)

	// Get filter repository
	filterRepo := core.NewFilterRepository(dbms, logger)

	// Get all enabled filters for this tenant
	filters, err := filterRepo.GetAllEnabledFiltersForTenant(reqCtx, tenantID)
	if err != nil {
		return err
	}

	if len(filters) == 0 {
		logger.Info("kg_queue: no enabled filters for tenant")
		return nil
	}

	// Create KG service
	kgService := core.NewService(reqCtx, logger, dbms)

	// Register sources (same as existing processFiltersAsync)
	if err := sources.RegisterAllSourcesToService(kgService, "", "", reqCtx); err != nil {
		logger.Warn("kg_queue: failed to register sources", "error", err)
	}
	if err := flow_sources.RegisterAllFlowSourcesToService(kgService, reqCtx); err != nil {
		logger.Warn("kg_queue: failed to register flow sources", "error", err)
	}
	if err := sources.RegisterAllEnrichersToService(kgService, reqCtx); err != nil {
		logger.Warn("kg_queue: failed to register enrichers", "error", err)
	}

	// Process each filter
	successCount := 0
	failureCount := 0
	for i, filter := range filters {
		logger.Info("kg_queue: processing filter",
			"correlation_id", correlationID,
			"filter_id", filter.ID,
			"filter_name", filter.FilterName,
			"index", i+1,
			"total", len(filters))

		if err := processFilter(reqCtx, kgService, filter, logger); err != nil {
			logger.Error("kg_queue: filter processing failed",
				"correlation_id", correlationID,
				"filter_id", filter.ID,
				"filter_name", filter.FilterName,
				"error", err)
			failureCount++
			// Continue with other filters
		} else {
			successCount++
			logger.Info("kg_queue: filter processed successfully",
				"correlation_id", correlationID,
				"filter_id", filter.ID,
				"filter_name", filter.FilterName)
		}
	}

	logger.Info("kg_queue: completed processing all filters",
		"correlation_id", correlationID,
		"total_filters", len(filters),
		"success_count", successCount,
		"failure_count", failureCount)

	return nil
}

// processFilter processes a single filter (reuses logic from existing processFiltersAsync)
func processFilter(ctx *security.RequestContext, kgService *core.Service,
	filter *kgmodels.KnowledgeGraphTenantFilter, logger *slog.Logger) error {

	// Convert filter to build request
	accountIDs, sourcesFromFilter, flowSourcesFromFilter := filter.ToSlices()

	// Set time range for last 24 hours
	now := time.Now()
	timeRange := &core.TimeRange{
		StartTime: now.Add(-24 * time.Hour),
		EndTime:   now,
	}

	buildRequest := &core.BuildRequest{
		TenantID:    filter.TenantID.String(),
		AccountIDs:  accountIDs,
		Sources:     sourcesFromFilter,
		FlowSources: flowSourcesFromFilter,
		Filters:     make(map[string]string),
		SaveToDB:    true,
		TimeRange:   timeRange,
	}

	// Copy additional filters from JSONB
	if filter.Filters != nil {
		for key, value := range filter.Filters {
			if strVal, ok := value.(string); ok {
				buildRequest.Filters[key] = strVal
			}
		}
	}

	logger.Info("kg_queue: building knowledge graph for filter",
		"tenant_id", filter.TenantID.String(),
		"filter_name", filter.FilterName,
		"account_ids", accountIDs,
		"sources", sourcesFromFilter,
		"flow_sources", flowSourcesFromFilter)

	// Build the graph
	response, err := kgService.BuildGraphs(ctx, buildRequest)
	if err != nil {
		return err
	}

	logger.Info("kg_queue: successfully built graph for filter",
		"tenant_id", filter.TenantID.String(),
		"filter_name", filter.FilterName,
		"nodes_saved", response.NodesSaved,
		"edges_saved", response.EdgesSaved,
		"accounts_processed", response.AccountsProcessed)

	return nil
}
