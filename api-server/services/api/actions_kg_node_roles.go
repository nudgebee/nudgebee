package api

import (
	"errors"
	"log/slog"
	"sort"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// The role a user gives a Knowledge Graph node (database, cache, ...): see
// knowledge_graph/core/node_roles.go.

// handleKgListNodeRoles returns the role vocabulary and the node types that
// may carry one, so the UI offers exactly what the server accepts.
func handleKgListNodeRoles(c *gin.Context) {
	types := core.NodeRoleTargetTypes()
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	c.JSON(200, map[string]any{"data": map[string]any{
		"roles":             core.NodeRoles,
		"target_node_types": types,
	}})
}

// handleKgWriteNodeRole sets (kg_upsert_node_role) or removes
// (kg_delete_node_role) a node's role. Access follows the manual-node rule:
// the node's account, or tenant-wide access for a node outside any account.
func handleKgWriteNodeRole(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger, set bool) {
	kgRequest, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: input must contain a 'request' object"))
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
		Role   string `json:"role"`
		Engine string `json:"engine"`
	}
	if err := common.UnmarshalMapToStruct(kgRequest, &req); err != nil {
		c.JSON(400, common.ErrorActionBadRequest("Invalid request format: "+err.Error()))
		return
	}
	if req.NodeID == "" {
		c.JSON(400, common.ErrorActionBadRequest("node_id is required"))
		return
	}

	ctx, _, kg, tenantID, err := manualNodeHandlerSetup(actionPayload, c, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	write := core.NodeRoleWrite{
		TenantID:  tenantID,
		NodeID:    req.NodeID,
		Role:      req.Role,
		Engine:    req.Engine,
		UserID:    ctx.GetSecurityContext().GetUserId(),
		Authorize: manualAccountAuthorizer(ctx, security.SecurityAccessTypeUpdate),
	}
	var result *core.NodeRoleResult
	if set {
		result, err = kg.SetNodeRole(write)
	} else {
		result, err = kg.ClearNodeRole(write)
	}
	if err != nil {
		status := nodeRoleWriteStatus(err)
		if status >= 500 {
			ctx.GetLogger().Error("node role write failed", "node_id", req.NodeID, "error", err)
			c.JSON(status, common.ErrorActionInternal("Failed to change the node's role: "+err.Error()))
			return
		}
		ctx.GetLogger().Warn("node role write refused", "node_id", req.NodeID, "error", err)
		if status == 403 {
			c.JSON(status, common.ErrorActionForbidden(err.Error()))
			return
		}
		c.JSON(status, common.ErrorActionBadRequest(err.Error()))
		return
	}
	auditNodeRoleChanges(ctx, result.Changes, set)
	c.JSON(200, map[string]any{"data": result})
}

// nodeRoleWriteStatus maps a role-write error to an HTTP status.
func nodeRoleWriteStatus(err error) int {
	switch {
	case errors.Is(err, core.ErrNodeRoleNotFound):
		return 404
	case errors.Is(err, core.ErrNodeRoleForbidden):
		return 403
	case errors.Is(err, core.ErrNodeRoleNotTarget), errors.Is(err, core.ErrNodeRoleInvalid):
		return 400
	}
	return 500
}

// auditNodeRoleChanges records one audit row per node whose role changed
// (a node and each linked instance that took the same change).
func auditNodeRoleChanges(ctx *security.RequestContext, changes []core.NodeRoleChange, set bool) {
	eventType, action := audit.EventTypeKGNodeRoleUpsert, audit.EventActionUpdate
	if !set {
		eventType, action = audit.EventTypeKGNodeRoleDelete, audit.EventActionDelete
	}
	for _, ch := range changes {
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryKnowledgeGraph,
			EventType:     eventType,
			EventAction:   action,
			TargetID:      ch.NodeID,
			AccountID:     ch.CloudAccountID,
			TableName:     "kg_node_roles",
			OldData:       nodeRoleAuditData(ch, ch.Before),
			NewData:       nodeRoleAuditData(ch, ch.After),
		})
	}
}

func nodeRoleAuditData(ch core.NodeRoleChange, s core.NodeRoleSnapshot) map[string]any {
	return map[string]any{
		"node_id":         ch.NodeID,
		"name":            ch.Name,
		"node_type":       ch.NodeType,
		"role":            s.Role,
		"engine":          s.Engine,
		"role_source":     s.RoleSource,
		"detected_role":   s.DetectedRole,
		"detected_engine": s.DetectedEngine,
	}
}
