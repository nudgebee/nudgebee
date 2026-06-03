package api

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// manualDepHandlerSetup centralizes the boilerplate every handler needs:
// security context, db manager, tenant id, repository. Returning the
// gin.Context unchanged lets the caller .JSON the failure as it sees fit.
func manualDepHandlerSetup(
	actionPayload *ActionRequest,
	c *gin.Context,
	tracer *trace.Tracer,
	meter *metric.Meter,
	logger *slog.Logger,
) (*security.RequestContext, *flow_sources.ManualDependencyRepository, string, error) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
		return nil, nil, "", err
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
		return nil, nil, "", err
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return nil, nil, "", errors.New("tenant_id missing from auth context")
	}
	repo := flow_sources.NewManualDependencyRepository(dbManager, ctx.GetLogger())
	return ctx, repo, tenantID, nil
}

// handleKgListManualDependencies returns every active row for the tenant,
// optionally filtered by resolution_status.
func handleKgListManualDependencies(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, _ := actionPayload.Input["request"].(map[string]interface{})

	var req struct {
		StatusFilter []string `json:"status_filter,omitempty"`
	}
	if kgRequest != nil {
		if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
			return
		}
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	deps, err := repo.List(ctx.GetContext(), tenantID, req.StatusFilter)
	if err != nil {
		ctx.GetLogger().Error("list manual dependencies failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("Failed to list manual dependencies: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": deps, "count": len(deps)})
}

// handleKgCreateManualDependency creates one row from the request body,
// runs the resolver synchronously, and returns the post-resolve row so the
// caller knows immediately whether the declaration resolved cleanly.
func handleKgCreateManualDependency(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: input must contain a 'request' object"))
		return
	}

	var dep flow_sources.ManualDependency
	if err := common.UnmarshalMapToStruct(kgRequest, &dep); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	dep.TenantID = tenantID
	dep.DeclaredByUserID = ctx.GetSecurityContext().GetUserId()

	created, err := repo.Create(ctx.GetContext(), dep)
	if err != nil {
		ctx.GetLogger().Error("create manual dependency failed", "error", err)
		c.JSON(400, common.ErrorActionBadRequest("Failed to create manual dependency: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": created})
}

// handleKgUpdateManualDependency edits an existing row and re-resolves it.
func handleKgUpdateManualDependency(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format"))
		return
	}

	var req struct {
		ID         int64                         `json:"id"`
		Dependency flow_sources.ManualDependency `json:"dependency"`
	}
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}
	if req.ID == 0 {
		c.JSON(400, common.ErrorActionBadRequest("id is required"))
		return
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	updated, err := repo.Update(ctx.GetContext(), tenantID, req.ID, req.Dependency)
	if err != nil {
		ctx.GetLogger().Error("update manual dependency failed", "error", err)
		c.JSON(400, common.ErrorActionBadRequest("Failed to update manual dependency: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": updated})
}

// handleKgDeleteManualDependency soft-deletes one row and removes the
// matching KG edge atomically.
func handleKgDeleteManualDependency(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format"))
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}
	if req.ID == 0 {
		c.JSON(400, common.ErrorActionBadRequest("id is required"))
		return
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	if err := repo.SoftDelete(ctx.GetContext(), tenantID, req.ID); err != nil {
		ctx.GetLogger().Error("delete manual dependency failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("Failed to delete manual dependency: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": map[string]any{"id": req.ID, "deleted": true}})
}

// handleKgImportManualDependencies accepts a CSV body and per-row imports
// each declaration with synchronous resolution. Returns per-row results
// (imported with status + match_count, or rejected with error).
func handleKgImportManualDependencies(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format"))
		return
	}

	var req struct {
		CSV string `json:"csv"`
	}
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}
	if req.CSV == "" {
		c.JSON(400, common.ErrorActionBadRequest("csv body is required"))
		return
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	userID := ctx.GetSecurityContext().GetUserId()

	result, err := repo.ImportCSV(ctx.GetContext(), tenantID, userID, bytes.NewBufferString(req.CSV))
	if err != nil {
		ctx.GetLogger().Error("csv import failed", "error", err)
		c.JSON(400, common.ErrorActionBadRequest("CSV import failed: "+err.Error()))
		return
	}

	ctx.GetLogger().Info("csv import complete",
		"tenant_id", tenantID,
		"imported", len(result.Imported),
		"rejected", len(result.Rejected))
	c.JSON(200, map[string]any{"data": result})
}

// handleKgResolveManualDependency pins one or both endpoints of an
// ambiguous row to operator-chosen candidate node IDs.
func handleKgResolveManualDependency(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format"))
		return
	}

	var req struct {
		ID                int64  `json:"id"`
		SourceNodeID      string `json:"source_node_id"`
		DestinationNodeID string `json:"destination_node_id"`
	}
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}
	if req.ID == 0 {
		c.JSON(400, common.ErrorActionBadRequest("id is required"))
		return
	}
	if req.SourceNodeID == "" && req.DestinationNodeID == "" {
		c.JSON(400, common.ErrorActionBadRequest("at least one of source_node_id or destination_node_id is required"))
		return
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	updated, err := repo.SetResolvedNodes(ctx.GetContext(), tenantID, req.ID, req.SourceNodeID, req.DestinationNodeID)
	if err != nil {
		ctx.GetLogger().Error("resolve manual dependency failed", "error", err)
		c.JSON(400, common.ErrorActionBadRequest("Failed to resolve manual dependency: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": updated})
}

// handleKgReresolveManualDependency forces a single row through the
// resolver regardless of its current status.
func handleKgReresolveManualDependency(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format"))
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}
	if req.ID == 0 {
		c.JSON(400, common.ErrorActionBadRequest("id is required"))
		return
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	updated, err := repo.ResolveAndPersist(ctx.GetContext(), tenantID, req.ID)
	if err != nil {
		ctx.GetLogger().Error("re-resolve manual dependency failed", "error", err)
		c.JSON(400, common.ErrorActionBadRequest("Failed to re-resolve manual dependency: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": updated})
}

// handleKgReresolveManualDependencies runs the resolver across every active
// row matching the status filter. Default filter is "everything that isn't
// already resolved" so re-resolved rows don't churn their last_resolved_at.
func handleKgReresolveManualDependencies(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, _ := actionPayload.Input["request"].(map[string]interface{})

	var req struct {
		StatusFilter []string `json:"status_filter,omitempty"`
		AllRows      bool     `json:"all_rows,omitempty"`
	}
	if kgRequest != nil {
		if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
			return
		}
	}

	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	filter := req.StatusFilter
	if len(filter) == 0 && !req.AllRows {
		filter = []string{
			flow_sources.ManualResolutionPending,
			flow_sources.ManualResolutionSourceUnmatched,
			flow_sources.ManualResolutionDestUnmatched,
			flow_sources.ManualResolutionSourceAmbiguous,
			flow_sources.ManualResolutionDestAmbiguous,
			flow_sources.ManualResolutionSourceTooManyMatches,
			flow_sources.ManualResolutionDestTooManyMatches,
			flow_sources.ManualResolutionNodeInactive,
		}
	}

	results, err := repo.ReresolveAll(ctx.GetContext(), tenantID, filter)
	if err != nil {
		ctx.GetLogger().Error("bulk re-resolve failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("Bulk re-resolve failed: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": results, "count": len(results)})
}

// handleKgDeleteAllManualDependencies is the panic button: deactivate every
// manual row and remove every source='manual' edge for the tenant.
func handleKgDeleteAllManualDependencies(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, repo, tenantID, err := manualDepHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	rowsDeactivated, edgesDeleted, err := repo.SoftDeleteAll(ctx.GetContext(), tenantID)
	if err != nil {
		ctx.GetLogger().Error("delete-all manual dependencies failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("Failed to delete all manual dependencies: "+err.Error()))
		return
	}

	ctx.GetLogger().Info("manual dependencies wiped",
		"tenant_id", tenantID,
		"rows_deactivated", rowsDeactivated,
		"edges_deleted", edgesDeleted)
	c.JSON(200, map[string]any{
		"data": map[string]any{
			"rows_deactivated": rowsDeactivated,
			"edges_deleted":    edgesDeleted,
			"message":          fmt.Sprintf("deactivated %d rows and removed %d edges", rowsDeactivated, edgesDeleted),
		},
	})
}
