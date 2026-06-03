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

// handleCloudAction routes cloud actions to their handlers
func handleCloudAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(500, []common.Error{
			{
				Message: err.Error(),
			},
		})
		return
	}

	switch actionPayload.Action.Name {
	case "cloud_metrics", "cloud_list_metrics":
		handleCloudMetrics(actionPayload, c, ctx)
	case "cloud_resources":
		handleCloudResources(actionPayload, c, ctx)
	case "cloud_logs":
		handleCloudLogs(actionPayload, c, ctx)
	case "cloud_service_map":
		handleCloudServiceMap(actionPayload, c, ctx)
	case "database_performance_insights":
		handleDatabasePerformance(actionPayload, c, ctx)
	case "trigger_cloud_account_sync", "accounts_sync":
		handleTriggerCloudSync(actionPayload, c, ctx)
	case "cloud_apply_command":
		handleCloudApplyCommand(actionPayload, c, ctx)
	default:
		c.JSON(400, []common.Error{
			{
				Message: "invalid action name - " + actionPayload.Action.Name,
			},
		})
		return
	}
}

// handleCloudMetrics handles cloud metrics queries
func handleCloudMetrics(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryMetricsRequest
	err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
	if err != nil {
		slog.Error("cloud_list_metrics: failed to decode request", "error", err)
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
func handleCloudResources(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryResourceRequest
	err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
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
func handleCloudLogs(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryLogsRequest
	err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
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
func handleCloudServiceMap(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryServiceMapRequest
	err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
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
func handleDatabasePerformance(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.QueryDatabasePerformanceRequest
	err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
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
func handleTriggerCloudSync(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	accountId, ok := actionPayload.Input["account_id"].(string)
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
func handleCloudApplyCommand(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.ApplyCommandRequest
	err := common.UnmarshalMapToStruct(actionPayload.Input, &request)
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
