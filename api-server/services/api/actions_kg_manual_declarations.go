package api

import (
	"encoding/json"
	"log/slog"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// handleKgCreateManualDeclarations declares the nodes and then the
// dependencies of one JSON document: {document: {nodes: [...],
// dependencies: [...]}}. Document-level problems are a 400 with nothing
// written; everything else is reported item by item.
func handleKgCreateManualDeclarations(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format"))
		return
	}
	document, ok := kgRequest["document"]
	if !ok || document == nil {
		c.JSON(400, common.ErrorActionBadRequest("document is required"))
		return
	}
	// The document is parsed with encoding/json rather than the map decoder
	// the other handlers use: a dependency side is a ref string or an endpoint
	// object, which only a JSON unmarshaler can tell apart. It may arrive as
	// the parsed object or as its text.
	raw, isText := document.(string)
	var body []byte
	if isText {
		body = []byte(raw)
	} else {
		encoded, err := json.Marshal(document)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
			return
		}
		body = encoded
	}
	doc, err := flow_sources.ParseManualDeclarations(body)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	ctx, nodes, kg, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		c.JSON(500, common.ErrorActionInternal(err.Error()))
		return
	}
	sc := ctx.GetSecurityContext()
	// Every node is checked against its account and every dependency against
	// the accounts it touches; a refused item is rejected, the rest proceed.
	authorize := manualAccountAuthorizer(sc, security.SecurityAccessTypeCreate)
	deps := manualDepWriter(ctx, flow_sources.NewManualDependencyRepository(dbManager, ctx.GetLogger()), security.SecurityAccessTypeCreate)

	result, err := flow_sources.NewManualDeclarationsImporter(nodes, deps, kg).Import(ctx.GetContext(), tenantID, sc.GetUserId(), authorize, doc)
	if err != nil {
		ctx.GetLogger().Error("manual declarations import failed", "error", err)
		c.JSON(500, common.ErrorActionInternal("import failed: "+err.Error()))
		return
	}
	counts := map[string]any{
		"nodes_created": len(result.Nodes.Created), "nodes_existing": len(result.Nodes.Existing), "nodes_rejected": len(result.Nodes.Rejected),
		"dependencies_created": len(result.Dependencies.Created), "dependencies_rejected": len(result.Dependencies.Rejected),
	}
	ctx.GetLogger().Info("manual declarations import complete", "tenant_id", tenantID, "counts", counts)
	auditManualNode(ctx, audit.EventTypeKGDeclarationsImport, audit.EventActionCreate, tenantID, counts)
	c.JSON(200, map[string]any{"data": result})
}
