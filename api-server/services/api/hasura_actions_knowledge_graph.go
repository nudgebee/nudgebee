package api

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	kgmodels "nudgebee/services/knowledge_graph/models"
	kgqueue "nudgebee/services/knowledge_graph/queue"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraKnowledgeGraphAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := hasuraPayload.Input["request"].(map[string]interface{})
	if !ok {
		if hasuraPayload.Action.Name == "build_knowledge_graph" || hasuraPayload.Action.Name == "kg_get_filter_options" || hasuraPayload.Action.Name == "kg_search_nodes" {
			kgRequest = make(map[string]interface{})
		} else {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "invalid_request_format")
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: input must contain a 'request' object"))
			return
		}
	}

	switch hasuraPayload.Action.Name {
	case "kg_get_complete_graph":
		// Parse filter request
		var filterRequest struct {
			AccountIDs    []string          `json:"account_ids,omitempty"`
			NodeTypes     []string          `json:"node_types,omitempty"`
			Labels        map[string]string `json:"labels,omitempty"`
			LabelKeys     []string          `json:"label_keys,omitempty"`
			Attributes    map[string]string `json:"attributes,omitempty"`
			AttributeKeys []string          `json:"attribute_keys,omitempty"`
			NodeIDs       []string          `json:"node_ids,omitempty"`
			Levels        int               `json:"levels,omitempty"` // 1, 2, or 3 - depth of neighbor traversal
			// Subgraph controls neighbor-mode edge shape. false (default when omitted) → BFS
			// spanning forest (only edges that connect consecutive depth layers, rooted at
			// NodeIDs); true → induced subgraph (every edge whose both endpoints are in the
			// discovered set). Pointer so we can distinguish "absent" from explicit "false".
			// Ignored when NodeIDs is empty.
			Subgraph *bool `json:"subgraph,omitempty"`
		}
		err := common.UnmarshalMapToStruct(kgRequest, &filterRequest)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "unmarshal_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		// Build security context
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		tenant_id := ctx.GetSecurityContext().GetTenantId()

		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}

		// Initialize knowledge graph service
		kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)

		var kg core.KnowledgeGraph

		// If NodeIDs are present, get neighbors for those specific nodes only
		if len(filterRequest.NodeIDs) > 0 {
			// Validate and default levels parameter
			levels := filterRequest.Levels
			if levels == 0 {
				levels = 1 // Default to 1 level (direct neighbors)
			}
			if levels < 1 || levels > 3 {
				common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "invalid_levels")
				c.JSON(400, common.ErrorHasuraActionBadRequest("levels must be 1, 2, or 3"))
				return
			}

			// Default to spanning-tree mode when the flag is omitted. Callers that want
			// the dense induced subgraph (e.g. graph-density inspection) must opt in by
			// passing subgraph=true explicitly.
			subgraph := false
			if filterRequest.Subgraph != nil {
				subgraph = *filterRequest.Subgraph
			}

			ctx.GetLogger().Info("retrieving neighbors for specific nodes",
				"tenant_id", tenant_id,
				"node_ids", filterRequest.NodeIDs,
				"node_count", len(filterRequest.NodeIDs),
				"levels", levels,
				"node_types_filter", filterRequest.NodeTypes,
				"subgraph", subgraph)

			// Convert string node types to NodeType for filtering neighbors
			nodeTypes := make([]core.NodeType, len(filterRequest.NodeTypes))
			for i, nt := range filterRequest.NodeTypes {
				nodeTypes[i] = core.NodeType(nt)
			}

			kg, err = kgService.GetMultipleNodeNeighbors(ctx, filterRequest.NodeIDs, levels, nodeTypes, subgraph)
			if err != nil {
				common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "get_node_neighbors_error")
				ctx.GetLogger().Error("failed to get node neighbors from database", "error", err)
				c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to retrieve node neighbors: "+err.Error()))
				return
			}

			ctx.GetLogger().Info("successfully retrieved node neighbors",
				"tenant_id", tenant_id,
				"requested_nodes", len(filterRequest.NodeIDs),
				"nodes_count", len(kg.Nodes),
				"edges_count", len(kg.Edges))
		} else {
			// No NodeIDs specified, get complete graph with optional filters
			// Build filters object
			var filters *core.GraphFilters
			if len(filterRequest.AccountIDs) > 0 || len(filterRequest.NodeTypes) > 0 || len(filterRequest.Labels) > 0 || len(filterRequest.LabelKeys) > 0 || len(filterRequest.Attributes) > 0 || len(filterRequest.AttributeKeys) > 0 {
				// Convert string node types to NodeType
				nodeTypes := make([]core.NodeType, len(filterRequest.NodeTypes))
				for i, nt := range filterRequest.NodeTypes {
					nodeTypes[i] = core.NodeType(nt)
				}

				filters = &core.GraphFilters{
					AccountIDs:    filterRequest.AccountIDs,
					NodeTypes:     nodeTypes,
					Labels:        filterRequest.Labels,
					LabelKeys:     filterRequest.LabelKeys,
					Attributes:    filterRequest.Attributes,
					AttributeKeys: filterRequest.AttributeKeys,
				}

				ctx.GetLogger().Info("applying filters to knowledge graph query",
					"account_ids", filterRequest.AccountIDs,
					"node_types", filterRequest.NodeTypes,
					"labels", filterRequest.Labels,
					"label_keys", filterRequest.LabelKeys,
					"attributes", filterRequest.Attributes,
					"attribute_keys", filterRequest.AttributeKeys)
			}

			// Retrieve complete graph from database using the core service layer with filters
			kg, err = kgService.GetCompleteGraphFromDatabaseWithFilters(ctx, tenant_id, filters)
			if err != nil {
				common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "get_graph_error")
				ctx.GetLogger().Error("failed to get complete knowledge graph from database", "error", err)
				c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to retrieve knowledge graph: "+err.Error()))
				return
			}

			ctx.GetLogger().Info("successfully retrieved complete knowledge graph",
				"tenant_id", tenant_id,
				"nodes_count", len(kg.Nodes),
				"edges_count", len(kg.Edges),
				"filters_applied", filters != nil)
		}

		c.JSON(200, map[string]any{
			"data": core.ConvertKnowledgeGraphToSlim(kg),
		})
		return

	case "build_knowledge_graph":
		// Build security context (for logging/auth only)
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		// Check if a specific tenant_id was provided in the request
		specificTenantID := ""
		if tid, ok := kgRequest["tenant_id"].(string); ok && tid != "" {
			specificTenantID = tid
			ctx.GetLogger().Info("specific tenant_id provided for knowledge graph build", "tenant_id", specificTenantID)
		}

		// If a specific tenant is requested, queue only that tenant
		if specificTenantID != "" {
			if err := kgqueue.PublishKGUpdate(specificTenantID, "cron"); err != nil {
				ctx.GetLogger().Error("failed to publish KG update message for specific tenant",
					"tenant_id", specificTenantID,
					"error", err)
				c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to queue KG update for tenant: "+err.Error()))
				return
			}

			ctx.GetLogger().Info("queued KG update for specific tenant", "tenant_id", specificTenantID)

			c.JSON(200, map[string]any{
				"data": map[string]any{
					"status":       "queued",
					"queued_count": 1,
					"failed_count": 0,
					"tenant_count": 1,
					"message":      fmt.Sprintf("Queued KG update for tenant %s", specificTenantID),
				},
			})
			return
		}

		// Get database manager
		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}

		// Get all enabled filters to find unique tenant IDs
		filterRepo := core.NewFilterRepository(dbManager, ctx.GetLogger())
		ctx.GetLogger().Info("starting knowledge graph build - fetching all enabled filters")
		filters, err := filterRepo.GetAllEnabledFilters(ctx)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "get_filters_error")
			ctx.GetLogger().Error("failed to get filters", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to retrieve filters: "+err.Error()))
			return
		}

		if len(filters) == 0 {
			c.JSON(200, map[string]any{
				"data": map[string]any{
					"status":       "no_tenants",
					"message":      "No enabled filters found in the system",
					"queued_count": 0,
				},
			})
			return
		}

		// Collect unique tenant IDs
		tenantIDSet := make(map[string]struct{})
		for _, f := range filters {
			tenantIDSet[f.TenantID.String()] = struct{}{}
		}

		// Publish message for each unique tenant
		queuedCount := 0
		failedCount := 0
		for tenantID := range tenantIDSet {
			if err := kgqueue.PublishKGUpdate(tenantID, "cron"); err != nil {
				ctx.GetLogger().Error("failed to publish KG update message",
					"tenant_id", tenantID,
					"error", err)
				failedCount++
			} else {
				queuedCount++
			}
		}

		ctx.GetLogger().Info("queued KG update messages",
			"total_tenants", len(tenantIDSet),
			"queued", queuedCount,
			"failed", failedCount)

		c.JSON(200, map[string]any{
			"data": map[string]any{
				"status":       "queued",
				"queued_count": queuedCount,
				"failed_count": failedCount,
				"tenant_count": len(tenantIDSet),
				"message":      fmt.Sprintf("Queued KG updates for %d tenants", queuedCount),
			},
		})
		return

	case "kg_get_filter_options":
		// Build security context
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		tenantID := ctx.GetSecurityContext().GetTenantId()

		// Get database manager
		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}

		// Initialize knowledge graph service
		kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)

		// Parse optional graph filters
		var filterRequest struct {
			AccountIDs    []string          `json:"account_ids,omitempty"`
			NodeTypes     []string          `json:"node_types,omitempty"`
			Labels        map[string]string `json:"labels,omitempty"`
			LabelKeys     []string          `json:"label_keys,omitempty"`
			Attributes    map[string]string `json:"attributes,omitempty"`
			AttributeKeys []string          `json:"attribute_keys,omitempty"`
			NodeIDs       []string          `json:"node_ids,omitempty"`
			Levels        int               `json:"levels,omitempty"` // TODO: implement graph-depth scoping in GetFilterOptions
		}
		if err := common.UnmarshalMapToStruct(kgRequest, &filterRequest); err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		var graphFilters *core.GraphFilters
		if len(filterRequest.AccountIDs) > 0 || len(filterRequest.NodeTypes) > 0 ||
			len(filterRequest.Labels) > 0 || len(filterRequest.LabelKeys) > 0 ||
			len(filterRequest.Attributes) > 0 || len(filterRequest.AttributeKeys) > 0 {
			nodeTypes := make([]core.NodeType, len(filterRequest.NodeTypes))
			for i, nt := range filterRequest.NodeTypes {
				nodeTypes[i] = core.NodeType(nt)
			}
			graphFilters = &core.GraphFilters{
				AccountIDs:    filterRequest.AccountIDs,
				NodeTypes:     nodeTypes,
				Labels:        filterRequest.Labels,
				LabelKeys:     filterRequest.LabelKeys,
				Attributes:    filterRequest.Attributes,
				AttributeKeys: filterRequest.AttributeKeys,
			}
		}

		// Get filter options
		filterOptions, err := kgService.GetFilterOptions(tenantID, graphFilters, filterRequest.NodeIDs)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "get_filter_options_error")
			ctx.GetLogger().Error("failed to get filter options", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to retrieve filter options: "+err.Error()))
			return
		}

		ctx.GetLogger().Info("successfully retrieved filter options",
			"tenant_id", tenantID,
			"account_ids_count", len(filterOptions.AccountIDs),
			"node_types_count", len(filterOptions.NodeTypes),
			"label_keys_count", len(filterOptions.LabelKeys),
			"attribute_keys_count", len(filterOptions.AttributeKeys))

		c.JSON(200, map[string]any{
			"data": filterOptions,
		})
		return

	case "kg_get_filter_values":
		// Build security context
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		tenantID := ctx.GetSecurityContext().GetTenantId()

		// Parse input from request
		requestData, ok := hasuraPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: input must contain a 'request' object"))
			return
		}

		// Extract filter_type and filter_key
		var input struct {
			FilterType string `json:"filter_type"`
			FilterKey  string `json:"filter_key"`
		}

		err = common.UnmarshalMapToStruct(requestData, &input)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		// Validate input
		if input.FilterType == "" || input.FilterKey == "" {
			c.JSON(400, common.ErrorHasuraActionBadRequest("filter_type and filter_key are required"))
			return
		}

		// Get database manager
		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}

		// Initialize knowledge graph service
		kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)

		// Get filter values
		filterValues, err := kgService.GetFilterValues(tenantID, input.FilterType, input.FilterKey)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "get_filter_values_error")
			ctx.GetLogger().Error("failed to get filter values",
				"filter_type", input.FilterType,
				"filter_key", input.FilterKey,
				"error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to retrieve filter values: "+err.Error()))
			return
		}

		ctx.GetLogger().Info("successfully retrieved filter values",
			"tenant_id", tenantID,
			"filter_type", input.FilterType,
			"filter_key", input.FilterKey,
			"values_count", len(filterValues.Values))

		c.JSON(200, map[string]any{
			"data": filterValues,
		})
		return

	case "kg_get_node":
		var req struct {
			NodeID string `json:"node_id"`
		}
		err := common.UnmarshalMapToStruct(kgRequest, &req)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "unmarshal_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}
		if req.NodeID == "" {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "missing_id")
			c.JSON(400, common.ErrorHasuraActionBadRequest("node_id is required"))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}
		kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)
		node, err := kgService.GetNodeByID(req.NodeID)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "get_node_error")
			ctx.GetLogger().Error("failed to get node by id", "error", err, "id", req.NodeID)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to retrieve node: "+err.Error()))
			return
		}
		if node == nil {
			c.JSON(404, common.ErrorHasuraActionBadRequest("node not found"))
			return
		}
		c.JSON(200, map[string]any{"data": node})
		return

	case "kg_get_edge":
		var req struct {
			EdgeID string `json:"edge_id"`
		}
		err := common.UnmarshalMapToStruct(kgRequest, &req)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "unmarshal_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}
		if req.EdgeID == "" {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "missing_id")
			c.JSON(400, common.ErrorHasuraActionBadRequest("edge_id is required"))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}
		kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)
		edge, err := kgService.GetEdgeByID(req.EdgeID)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "get_edge_error")
			ctx.GetLogger().Error("failed to get edge by id", "error", err, "id", req.EdgeID)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to retrieve edge: "+err.Error()))
			return
		}
		if edge == nil {
			c.JSON(404, common.ErrorHasuraActionBadRequest("edge not found"))
			return
		}
		c.JSON(200, map[string]any{"data": edge})
		return

	case "kg_search_nodes":
		var request core.SearchNodesParams
		if err := common.UnmarshalMapToStruct(kgRequest, &request); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "unmarshal_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}

		kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)
		tenantID := ctx.GetSecurityContext().GetTenantId()

		result, err := kgService.SearchNodes(tenantID, request)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "search_nodes_error")
			ctx.GetLogger().Error("failed to search nodes", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to search nodes: "+err.Error()))
			return
		}

		ctx.GetLogger().Info("successfully searched nodes",
			"tenant_id", tenantID,
			"result_count", len(result.Nodes),
			"total_count", result.TotalCount)

		c.JSON(200, map[string]any{"data": result})
		return

	case "kg_traverse":
		var request core.TraverseParams
		if err := common.UnmarshalMapToStruct(kgRequest, &request); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "unmarshal_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		dbManager, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
			ctx.GetLogger().Error("failed to get database manager", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to initialize database: "+err.Error()))
			return
		}

		kgService := core.NewService(ctx, ctx.GetLogger(), dbManager)
		tenantID := ctx.GetSecurityContext().GetTenantId()

		result, err := kgService.TraverseDirectional(tenantID, request)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "traverse_error")
			ctx.GetLogger().Error("failed to traverse graph", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("Failed to traverse graph: "+err.Error()))
			return
		}

		ctx.GetLogger().Info("successfully traversed graph",
			"tenant_id", tenantID,
			"seed_nodes", len(result.SeedNodeIDs),
			"result_nodes", len(result.Graph.Nodes),
			"result_edges", len(result.Graph.Edges),
			"truncated", result.Truncated)

		c.JSON(200, map[string]any{"data": result})
		return

	default:
		common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "unknown_action")
		c.JSON(400, common.ErrorHasuraActionBadRequest("Unknown action: "+hasuraPayload.Action.Name))
		return
	}
}

// processFiltersAsync processes all filters asynchronously
// Each filter may belong to different tenants
//
//nolint:unused // kept for reference, queue-based processing now used instead
func processFiltersAsync(ctx *security.RequestContext, kgService *core.Service, filters []*kgmodels.KnowledgeGraphTenantFilter, jobID string, cancel context.CancelFunc) {
	defer cancel() // Clean up context when goroutine completes
	logger := ctx.GetLogger()

	logger.Info("starting async filter processing",
		"job_id", jobID,
		"filter_count", len(filters))

	totalFilters := len(filters)
	successCount := 0
	failureCount := 0

	// Process each filter one by one
	for i, filter := range filters {
		if !filter.Enabled {
			logger.Info("skipping disabled filter",
				"job_id", jobID,
				"tenant_id", filter.TenantID.String(),
				"filter_name", filter.FilterName,
				"index", i+1,
				"total", totalFilters)
			continue
		}

		logger.Info("processing filter",
			"job_id", jobID,
			"tenant_id", filter.TenantID.String(),
			"filter_name", filter.FilterName,
			"index", i+1,
			"total", totalFilters)

		// Convert filter to build request
		accountIDs, sourcesFromFilter, flowSourcesFromFilter := filter.ToSlices()

		// Set time range for last 24 hours
		now := time.Now()
		timeRange := &core.TimeRange{
			StartTime: now.Add(-24 * time.Hour),
			EndTime:   now,
		}

		buildRequest := &core.BuildRequest{
			TenantID:    filter.TenantID.String(), // Use tenant_id from the filter
			AccountIDs:  accountIDs,
			Sources:     sourcesFromFilter,
			FlowSources: flowSourcesFromFilter,
			Filters:     make(map[string]string),
			SaveToDB:    true,      // Save to database
			TimeRange:   timeRange, // Last 24 hours
		}

		// Copy additional filters from JSONB
		if filter.Filters != nil {
			for key, value := range filter.Filters {
				if strVal, ok := value.(string); ok {
					buildRequest.Filters[key] = strVal
				}
			}
		}

		logger.Info("building knowledge graph for filter",
			"job_id", jobID,
			"tenant_id", filter.TenantID.String(),
			"filter_name", filter.FilterName,
			"account_ids", accountIDs,
			"sources", sourcesFromFilter,
			"flow_sources", flowSourcesFromFilter)

		// Register all sources for this specific request
		if err := sources.RegisterAllSourcesToService(kgService, "", "", ctx); err != nil {
			logger.Warn("failed to register sources",
				"job_id", jobID,
				"tenant_id", filter.TenantID.String(),
				"filter_name", filter.FilterName,
				"error", err)
		}

		// Register all flow sources
		if err := flow_sources.RegisterAllFlowSourcesToService(kgService, ctx); err != nil {
			logger.Warn("failed to register flow sources",
				"job_id", jobID,
				"tenant_id", filter.TenantID.String(),
				"filter_name", filter.FilterName,
				"error", err)
		}

		// Register all cross-source enrichers
		if err := sources.RegisterAllEnrichersToService(kgService, ctx); err != nil {
			logger.Warn("failed to register cross-source enrichers",
				"job_id", jobID,
				"tenant_id", filter.TenantID.String(),
				"filter_name", filter.FilterName,
				"error", err)
		}

		// Build the graph
		response, err := kgService.BuildGraphs(ctx, buildRequest)
		if err != nil {
			failureCount++
			logger.Error("failed to build graph for filter",
				"job_id", jobID,
				"tenant_id", filter.TenantID.String(),
				"filter_name", filter.FilterName,
				"error", err,
				"index", i+1,
				"total", totalFilters)
			continue
		}

		successCount++
		logger.Info("successfully built graph for filter",
			"job_id", jobID,
			"tenant_id", filter.TenantID.String(),
			"filter_name", filter.FilterName,
			"nodes_saved", response.NodesSaved,
			"edges_saved", response.EdgesSaved,
			"accounts_processed", response.AccountsProcessed,
			"index", i+1,
			"total", totalFilters)
	}

	logger.Info("completed async filter processing",
		"job_id", jobID,
		"total_filters", totalFilters,
		"success_count", successCount,
		"failure_count", failureCount)
}
