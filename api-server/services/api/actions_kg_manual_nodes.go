package api

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Manually declared Knowledge Graph nodes: see flow_sources/manual_node.go.

// manualNodeHandlerSetup is the boilerplate every handler needs: security
// context, tenant, repository, and the KG service that writes the node.
func manualNodeHandlerSetup(
	actionPayload *ActionRequest,
	c *gin.Context,
	tracer *trace.Tracer,
	meter *metric.Meter,
	logger *slog.Logger,
) (*security.RequestContext, *flow_sources.ManualNodeRepository, *core.Service, string, error) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "context_error")
		return nil, nil, nil, "", err
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		common.MetricsApiRequestsFailedTotal(c.Request.Context(), "knowledge_graph", "database_init_error")
		return nil, nil, nil, "", err
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" {
		return nil, nil, nil, "", errors.New("tenant_id missing from auth context")
	}
	repo := flow_sources.NewManualNodeRepository(dbManager, ctx.GetLogger())
	return ctx, repo, core.NewService(ctx, ctx.GetLogger(), dbManager), tenantID, nil
}

// auditManualNode records one declaration write under the Knowledge Graph
// category. Fire-and-forget, like every audit.LogChange caller.
func auditManualNode(ctx *security.RequestContext, eventType audit.EventType, action audit.EventAction, targetID string, data map[string]any) {
	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryKnowledgeGraph,
		EventType:     eventType,
		EventAction:   action,
		TargetID:      targetID,
		TableName:     "kg_manual_nodes",
		NewData:       data,
	})
}

// manualNodeSummary is what an audit row keeps of a declaration.
func manualNodeSummary(n *flow_sources.ManualNode) map[string]any {
	if n == nil {
		return nil
	}
	return map[string]any{
		"id":               n.ID,
		"name":             n.Name,
		"specific_type":    n.SpecificType,
		"node_type":        n.NodeType,
		"cloud_account_id": n.CloudAccountID,
		"kg_node_id":       n.KGNodeID,
		"status":           n.Status,
	}
}

// manualNodeAuthorizer answers whether the caller may write a declaration in
// an account. One outside any connected account belongs to the tenant as a
// whole, so it takes tenant-wide access; an account admin's reach stops at
// their own accounts.
func manualNodeAuthorizer(sc *security.SecurityContext, access security.SecurityAccessType) flow_sources.ManualNodeAuthorizer {
	return func(cloudAccountID string) bool {
		if cloudAccountID == "" {
			return sc.HasTenantAccess(access)
		}
		return sc.HasAccountAccess(cloudAccountID, access)
	}
}

// authorizeManualNodeWrite loads declaration id and checks the caller may
// write it, answering the request itself when it may not.
func authorizeManualNodeWrite(c *gin.Context, ctx *security.RequestContext, repo *flow_sources.ManualNodeRepository, tenantID string, id int64, access security.SecurityAccessType) bool {
	n, err := repo.Get(ctx.GetContext(), tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(404, common.ErrorActionBadRequest("manual node not found"))
		return false
	}
	if err != nil {
		c.JSON(500, common.ErrorActionInternal("Failed to load manual node: "+err.Error()))
		return false
	}
	if !manualNodeAuthorizer(ctx.GetSecurityContext(), access)(n.CloudAccountID) {
		c.JSON(403, common.ErrorActionForbidden(flow_sources.ErrManualNodeForbidden.Error()))
		return false
	}
	return true
}

// manualNodeWriteStatus maps a repository error to an HTTP status: 409 when
// the resource is already declared or collected, 403 when the caller may not
// write in the account, 404 for a missing row, 400 for the rest (validation,
// an adopted node).
func manualNodeWriteStatus(err error) int {
	var exists *flow_sources.ManualNodeExistsError
	switch {
	case errors.As(err, &exists), errors.Is(err, flow_sources.ErrManualNodeDuplicate):
		return 409
	case errors.Is(err, flow_sources.ErrManualNodeForbidden):
		return 403
	case errors.Is(err, sql.ErrNoRows):
		return 404
	}
	return 400
}

// manualNodeErrorBody carries the existing node's id on "already in the
// graph", so the UI can link to it (the gateway forwards the body as
// extensions.upstream.body).
func manualNodeErrorBody(err error) any {
	var exists *flow_sources.ManualNodeExistsError
	if errors.As(err, &exists) {
		return map[string]any{"message": err.Error(), "code": "already_in_graph", "node_id": exists.NodeID}
	}
	return common.ErrorActionBadRequest(err.Error())
}

// handleKgGetManualNodeSchema returns where nodes can be declared and, per
// place, the declarable types with their compulsory key fields and optional
// schema properties. It drives the declaration form.
func handleKgGetManualNodeSchema(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, repo, _, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	schema, err := repo.Schema(ctx.GetContext(), tenantID)
	if err != nil {
		ctx.GetLogger().Error("manual node schema failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("Failed to load the node schema: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": schema})
}

// handleKgListManualNodes returns the tenant's live declarations.
func handleKgListManualNodes(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, repo, _, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	nodes, err := repo.List(ctx.GetContext(), tenantID)
	if err != nil {
		ctx.GetLogger().Error("list manual nodes failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("Failed to list manual nodes: "+err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": nodes, "count": len(nodes)})
}

// handleKgCreateManualNode declares one node and writes it into the graph.
func handleKgCreateManualNode(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: input must contain a 'request' object"))
		return
	}
	var req flow_sources.ManualNodeCreate
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}

	ctx, repo, kg, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	req.TenantID = tenantID
	req.DeclaredByUserID = ctx.GetSecurityContext().GetUserId()
	req.Authorize = manualNodeAuthorizer(ctx.GetSecurityContext(), security.SecurityAccessTypeCreate)

	created, err := repo.Create(ctx.GetContext(), kg, req)
	if err != nil {
		ctx.GetLogger().Warn("create manual node failed", "error", err)
		c.JSON(manualNodeWriteStatus(err), manualNodeErrorBody(err))
		return
	}
	auditManualNode(ctx, audit.EventTypeKGNodeCreate, audit.EventActionCreate, fmt.Sprint(created.ID), manualNodeSummary(created))
	c.JSON(200, map[string]any{"data": created})
}

// handleKgUpdateManualNode edits a declaration's descriptive data. Identity
// fields are not part of the request: changing one would change the node id.
func handleKgUpdateManualNode(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format"))
		return
	}
	var req struct {
		ID   int64                               `json:"id"`
		Node flow_sources.ManualNodeDetailsPatch `json:"node"`
	}
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}
	if req.ID == 0 {
		c.JSON(400, common.ErrorActionBadRequest("id is required"))
		return
	}

	ctx, repo, kg, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	if !authorizeManualNodeWrite(c, ctx, repo, tenantID, req.ID, security.SecurityAccessTypeUpdate) {
		return
	}
	updated, err := repo.Update(ctx.GetContext(), kg, tenantID, req.ID, req.Node)
	if err != nil {
		ctx.GetLogger().Warn("update manual node failed", "id", req.ID, "error", err)
		c.JSON(manualNodeWriteStatus(err), common.ErrorActionBadRequest("Failed to update manual node: "+err.Error()))
		return
	}
	auditManualNode(ctx, audit.EventTypeKGNodeUpdate, audit.EventActionUpdate, fmt.Sprint(req.ID), manualNodeSummary(updated))
	c.JSON(200, map[string]any{"data": updated})
}

// handleKgDeleteManualNode withdraws a declaration and retires its node.
func handleKgDeleteManualNode(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
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

	ctx, repo, _, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	if !authorizeManualNodeWrite(c, ctx, repo, tenantID, req.ID, security.SecurityAccessTypeDelete) {
		return
	}
	edges, err := repo.SoftDelete(ctx.GetContext(), tenantID, req.ID)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(404, common.ErrorActionBadRequest("manual node not found"))
		return
	}
	if err != nil {
		ctx.GetLogger().Error("delete manual node failed", "id", req.ID, "error", err)
		c.JSON(500, common.ErrorActionInternal("Failed to delete manual node: "+err.Error()))
		return
	}
	auditManualNode(ctx, audit.EventTypeKGNodeDelete, audit.EventActionDelete, fmt.Sprint(req.ID), map[string]any{
		"id": req.ID, "edges_tombstoned": edges,
	})
	c.JSON(200, map[string]any{"data": map[string]any{"id": req.ID, "deleted": true, "edges_tombstoned": edges}})
}

// handleKgApplyManualNodeMerge folds a possible duplicate into the collected
// node it matches: pinned dependencies move to it, the declaration goes.
func handleKgApplyManualNodeMerge(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
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

	ctx, repo, _, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	if !authorizeManualNodeWrite(c, ctx, repo, tenantID, req.ID, security.SecurityAccessTypeUpdate) {
		return
	}
	merged, repointed, err := repo.Merge(ctx.GetContext(), tenantID, req.ID)
	if err != nil {
		ctx.GetLogger().Warn("merge manual node failed", "id", req.ID, "error", err)
		c.JSON(manualNodeWriteStatus(err), common.ErrorActionBadRequest("Failed to merge manual node: "+err.Error()))
		return
	}
	auditManualNode(ctx, audit.EventTypeKGNodeMerge, audit.EventActionUpdate, fmt.Sprint(req.ID), map[string]any{
		"id": req.ID, "kg_node_id": merged.KGNodeID, "merged_into": merged.DuplicateNodeID, "dependencies_repointed": repointed,
	})
	c.JSON(200, map[string]any{"data": map[string]any{
		"id": req.ID, "merged_into": merged.DuplicateNodeID, "dependencies_repointed": repointed,
	}})
}

// handleKgImportManualNodes declares one node per CSV row.
func handleKgImportManualNodes(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
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

	ctx, repo, kg, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	// Each row is checked against its own account; a refused row is rejected.
	authorize := manualNodeAuthorizer(ctx.GetSecurityContext(), security.SecurityAccessTypeCreate)
	result, err := repo.ImportCSV(ctx.GetContext(), kg, tenantID, ctx.GetSecurityContext().GetUserId(), authorize, bytes.NewBufferString(req.CSV))
	if err != nil {
		ctx.GetLogger().Warn("manual node csv import failed", "error", err)
		c.JSON(400, common.ErrorActionBadRequest("CSV import failed: "+err.Error()))
		return
	}
	auditManualNode(ctx, audit.EventTypeKGNodeImport, audit.EventActionCreate, tenantID, map[string]any{
		"imported": len(result.Imported), "rejected": len(result.Rejected),
	})
	c.JSON(200, map[string]any{"data": result})
}

// handleKgDeleteAllManualNodes is the panic button.
func handleKgDeleteAllManualNodes(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, repo, _, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	// Withdraws every account's declarations, so it takes tenant-wide access.
	if !ctx.GetSecurityContext().HasTenantAccess(security.SecurityAccessTypeDelete) {
		c.JSON(403, common.ErrorActionForbidden("deleting all manual nodes needs tenant admin access"))
		return
	}
	rows, nodes, err := repo.SoftDeleteAll(ctx.GetContext(), tenantID)
	if err != nil {
		ctx.GetLogger().Error("delete-all manual nodes failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("Failed to delete all manual nodes: "+err.Error()))
		return
	}
	auditManualNode(ctx, audit.EventTypeKGNodeDeleteAll, audit.EventActionDelete, tenantID, map[string]any{
		"rows_deactivated": rows, "nodes_tombstoned": nodes,
	})
	c.JSON(200, map[string]any{"data": map[string]any{
		"rows_deactivated": rows,
		"nodes_tombstoned": nodes,
		"message":          fmt.Sprintf("withdrew %d declarations and retired %d nodes", rows, nodes),
	}})
}
