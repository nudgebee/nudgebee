package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/ownership"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleOwnershipAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	input := actionPayload.Input

	switch actionPayload.Action.Name {
	case "ownership_get":
		var req ownership.GetOwnerRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := ownership.GetOwner(ctx, req)
		respondOwnership(c, resp, err)

	case "ownership_resolve":
		var req ownership.ResolveRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := ownership.Resolve(ctx, req)
		respondOwnership(c, resp, err)

	case "ownership_list":
		var req ownership.ListOwnersRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := ownership.ListOwners(ctx, req)
		respondOwnership(c, resp, err)

	case "ownership_assign":
		var req ownership.AssignOwnerRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := ownership.AssignOwner(ctx, req)
		respondOwnership(c, resp, err)

	case "ownership_delete":
		var req ownership.DeleteOwnerRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := ownership.RemoveOwner(ctx, req)
		respondOwnership(c, resp, err)

	case "ownership_cleanup":
		resp, err := ownership.CleanupOrphans(ctx, struct{}{})
		respondOwnership(c, resp, err)

	case "ownership_list_rules":
		resp, err := ownership.ListRules(ctx)
		respondOwnership(c, resp, err)

	case "ownership_upsert_rule":
		var req ownership.UpsertRuleRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := ownership.UpsertRule(ctx, req)
		respondOwnership(c, resp, err)

	case "ownership_delete_rule":
		var req ownership.DeleteRuleRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := ownership.DeleteRule(ctx, req)
		respondOwnership(c, resp, err)

	default:
		c.JSON(400, common.ErrorActionBadRequest("unknown ownership action: "+actionPayload.Action.Name))
	}
}

func respondOwnership(c *gin.Context, resp any, err error) {
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	c.JSON(200, resp)
}
