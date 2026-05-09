package api

import (
	"database/sql"
	"log/slog"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// handleHasuraCloudAction routes cloud actions to their handlers
func handleHasuraCloudAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	switch hasuraPayload.Action.Name {
	case "cloud_metrics":
		handleCloudMetrics(hasuraPayload, c, ctx)
	case "cloud_resources":
		handleCloudResources(hasuraPayload, c, ctx)
	case "cloud_logs":
		handleCloudLogs(hasuraPayload, c, ctx)
	case "cloud_service_map":
		handleCloudServiceMap(hasuraPayload, c, ctx)
	case "database_performance_insights":
		handleDatabasePerformance(hasuraPayload, c, ctx)
	case "trigger_cloud_account_sync":
		handleTriggerCloudSync(hasuraPayload, c, ctx)
	case "cloud_apply_command":
		handleCloudApplyCommand(hasuraPayload, c, ctx)
	default:
		c.JSON(400, []common.Error{
			{
				Message: "invalid action name - " + hasuraPayload.Action.Name,
			},
		})
		return
	}
}

// handleCloudMetrics handles cloud metrics queries
func handleCloudMetrics(hasuraPayload *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryMetricsRequest
	err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
	if err != nil {
		slog.Error("cloud_metrics: failed to decode request", "error", err)
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	resp, err := cloud.QueryMetrics(ctx, request)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(200, resp)
}

// handleCloudResources handles cloud resource queries
func handleCloudResources(hasuraPayload *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryResourceRequest
	err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
	if err != nil {
		slog.Error("cloud_resources: failed to decode request", "error", err)
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	resp, err := cloud.QueryResources(ctx, request)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(200, resp)
}

// handleCloudLogs handles cloud logs queries
func handleCloudLogs(hasuraPayload *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryLogsRequest
	err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
	if err != nil {
		slog.Error("cloud_logs: failed to decode request", "error", err)
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	resp, err := cloud.QueryLogs(ctx, request)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(200, resp)
}

// handleCloudServiceMap handles cloud service map queries
func handleCloudServiceMap(hasuraPayload *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryServiceMapRequest
	err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
	if err != nil {
		slog.Error("cloud_service_map: failed to decode request", "error", err)
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	// Validate the request fields (no struct validation needed for this)
	if request.AccountId == "" {
		c.JSON(400, []common.Error{
			{
				Message: "account_id is required",
			},
		})
		return
	}

	resp, err := cloud.QueryServiceMap(ctx, request)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(200, resp)
}

// handleDatabasePerformance handles database performance queries
func handleDatabasePerformance(hasuraPayload *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryDatabasePerformanceRequest
	err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
	if err != nil {
		slog.Error("database_performance: failed to decode request", "error", err)
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	resp, err := cloud.QueryDatabasePerformance(ctx, request)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(200, resp)
}

// handleTriggerCloudSync triggers a full data sync for a cloud account
func handleTriggerCloudSync(hasuraPayload *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	accountId, ok := hasuraPayload.Input["account_id"].(string)
	if !ok || accountId == "" {
		c.JSON(400, []common.Error{
			{
				Message: "account_id is required",
			},
		})
		return
	}

	resp, err := cloud.TriggerCloudAccountSync(ctx, accountId)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(200, resp)
}

// handleCloudApplyCommand handles cloud resource action commands
func handleCloudApplyCommand(hasuraPayload *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.ApplyCommandRequest
	err := common.UnmarshalMapToStruct(hasuraPayload.Input, &request)
	if err != nil {
		slog.Error("cloud_apply_command: failed to decode request", "error", err)
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	err = common.ValidateStruct(request)
	if err != nil {
		c.JSON(400, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	// Get database manager
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("cloud_apply_command: failed to get database manager", "error", err)
		c.JSON(500, []common.Error{
			{
				Message: "internal server error",
			},
		})
		return
	}

	// Query account access status
	var accountAccess sql.NullString
	query := `SELECT account_access FROM cloud_accounts WHERE id = $1 AND tenant = $2 AND status = 'active'`
	err = databaseManager.Db.Get(&accountAccess, query, request.AccountId, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(404, []common.Error{
				{
					Message: "account not found",
				},
			})
		} else {
			slog.Error("cloud_apply_command: failed to query account", "error", err)
			c.JSON(500, []common.Error{
				{
					Message: "internal server error",
				},
			})
		}
		return
	}

	// Check if account is read-only
	if accountAccess.Valid && accountAccess.String == "readonly" {
		c.JSON(403, []common.Error{
			{
				Message: "cannot execute commands on read-only account",
			},
		})
		return
	}

	// Execute command
	resp, err := cloud.ApplyCommand(ctx, request)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(200, resp)
}
