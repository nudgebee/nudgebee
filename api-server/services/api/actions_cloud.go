package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
	"time"

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
	case "cloud_execute_command":
		handleCloudExecuteCommand(actionPayload, c, ctx)
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

	if request.AccountId == "" {
		c.JSON(400, []common.Error{
			{
				Message: "account_id is required",
			},
		})
		return
	}

	// Gate on access to the target account. tenant_admin passes for any
	// account in the tenant; account_admin only for its assigned accounts.
	// Required because callers now include account_admin (actions.yaml).
	if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
		c.JSON(403, []common.Error{
			{
				Message: "access denied for account: " + request.AccountId,
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

func handleCloudExecuteCommand(actionPayload *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var request cloud.ExecuteCloudCommandRequest
	err := common.UnmarshalMapToStruct(actionPayload.Input, &request)
	if err != nil {
		slog.Error("cloud_execute_command: failed to decode request", "error", err)
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

	// Gate on access to the target account. tenant_admin passes for any
	// account in the tenant; account_admin only for its assigned accounts.
	// Required because callers now include account_admin (actions.yaml).
	if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
		c.JSON(403, []common.Error{
			{
				Message: "access denied for account: " + request.AccountId,
			},
		})
		return
	}

	// Reject execution on read-only accounts (same guard as handleCloudApplyCommand).
	{
		databaseManager, dbErr := database.GetDatabaseManager(database.Metastore)
		if dbErr != nil {
			slog.Error("cloud_execute_command: failed to get database manager", "error", dbErr)
			c.JSON(500, []common.Error{{Message: "internal server error"}})
			return
		}
		var accountAccess sql.NullString
		dbErr = databaseManager.Db.Get(&accountAccess, `SELECT account_access FROM cloud_accounts WHERE id = $1 AND tenant = $2 AND status = 'active'`, request.AccountId, ctx.GetSecurityContext().GetTenantId())
		if dbErr != nil {
			if dbErr == sql.ErrNoRows {
				c.JSON(404, []common.Error{{Message: "account not found"}})
			} else {
				slog.Error("cloud_execute_command: failed to query account", "error", dbErr)
				c.JSON(500, []common.Error{{Message: "internal server error"}})
			}
			return
		}
		if accountAccess.Valid && accountAccess.String == "readonly" {
			c.JSON(403, []common.Error{{Message: "cannot execute commands on read-only account"}})
			return
		}
	}

	// Upsert an InProgress resolution record before executing, if a recommendation is linked.
	// Reuse an existing InProgress record (created within 2 hours) to avoid duplicates on retry.
	var resolutionId string
	if request.RecommendationId != "" {
		dbms, dbErr := database.GetDatabaseManager(database.Metastore)
		if dbErr != nil {
			slog.Error("cloud_execute_command: failed to get database manager", "error", dbErr)
			c.JSON(500, []common.Error{{Message: "internal server error"}})
			return
		}
		userId := ctx.GetSecurityContext().GetUserId()
		dbErr = dbms.Db.QueryRow(`
			SELECT id FROM recommendation_resolution
			WHERE recommendation_id = $1
			  AND type = 'CloudResource'
			  AND resolver_type = $2
			  AND resolver_id = $3
			  AND status = 'InProgress'
			  AND created_at > NOW() - INTERVAL '2 hours'
			ORDER BY created_at DESC LIMIT 1
		`, request.RecommendationId, models.RecommendationResolutionResolverTypeUser, userId).Scan(&resolutionId)
		if errors.Is(dbErr, sql.ErrNoRows) {
			// No existing InProgress resolution — create one.
			resolutionId = common.GenerateUUID()
			now := time.Now().UTC().Format(time.RFC3339)
			_, dbErr = dbms.Db.Exec(
				`INSERT INTO recommendation_resolution (id, created_at, updated_at, recommendation_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				resolutionId, now, now,
				request.RecommendationId,
				models.RecommendationResolutionTypeCloudResource,
				"{}",
				models.RecommendationResolutionStatusInProgress,
				"cli_execution",
				models.RecommendationResolutionResolverTypeUser,
				userId,
				"Executing",
			)
			if dbErr != nil {
				slog.Error("cloud_execute_command: failed to insert resolution", "error", dbErr)
				c.JSON(500, []common.Error{{Message: "internal server error"}})
				return
			}
		} else if dbErr != nil {
			slog.Error("cloud_execute_command: failed to query existing resolution", "error", dbErr)
			c.JSON(500, []common.Error{{Message: "internal server error"}})
			return
		}
	}

	request.ResolutionId = resolutionId
	resp, err := cloud.ExecuteCloudCommand(ctx, request)

	if request.RecommendationId != "" {
		dbms, dbErr := database.GetDatabaseManager(database.Metastore)
		if dbErr != nil {
			slog.Error("cloud_execute_command: failed to get database manager for resolution update", "error", dbErr)
		} else {
			now := time.Now().UTC().Format(time.RFC3339)
			userId := ctx.GetSecurityContext().GetUserId()

			resolutionStatus := models.RecommendationResolutionStatusFailed
			statusMessage := "Command Execution Failed"
			if err == nil {
				anyFailed := false
				for _, r := range resp.Results {
					if r.Status == "FAILED" || r.Status == "NOT_EXECUTED" {
						anyFailed = true
						break
					}
				}
				if anyFailed {
					resolutionStatus = models.RecommendationResolutionStatusFailed
					statusMessage = "Command Execution Failed"
				} else {
					resolutionStatus = models.RecommendationResolutionStatusSuccess
					statusMessage = "Command Execution Succeeded"
				}
			}

			if resolutionId != "" {
				if _, dbErr = dbms.Db.Exec(
					`UPDATE recommendation_resolution SET status = $1, status_message = $2, updated_at = $3 WHERE id = $4`,
					resolutionStatus, statusMessage, now, resolutionId,
				); dbErr != nil {
					slog.Error("cloud_execute_command: failed to update resolution status", "error", dbErr)
				}
			}
			if resolutionStatus == models.RecommendationResolutionStatusSuccess {
				if userId != "" {
					if _, dbErr = dbms.Db.Exec(
						`UPDATE recommendation SET status = $1, updated_at = $2, updated_by = $3 WHERE id = $4`,
						models.RecommendationStatusInProgress, now, userId, request.RecommendationId,
					); dbErr != nil {
						slog.Error("cloud_execute_command: failed to update recommendation status", "error", dbErr)
					}
				} else {
					if _, dbErr = dbms.Db.Exec(
						`UPDATE recommendation SET status = $1, updated_at = $2 WHERE id = $3`,
						models.RecommendationStatusInProgress, now, request.RecommendationId,
					); dbErr != nil {
						slog.Error("cloud_execute_command: failed to update recommendation status", "error", dbErr)
					}
				}
			} else {
				if userId != "" {
					if _, dbErr = dbms.Db.Exec(
						`UPDATE recommendation SET updated_at = $1, updated_by = $2 WHERE id = $3`,
						now, userId, request.RecommendationId,
					); dbErr != nil {
						slog.Error("cloud_execute_command: failed to stamp recommendation updated_at", "error", dbErr)
					}
				} else {
					if _, dbErr = dbms.Db.Exec(
						`UPDATE recommendation SET updated_at = $1 WHERE id = $2`,
						now, request.RecommendationId,
					); dbErr != nil {
						slog.Error("cloud_execute_command: failed to stamp recommendation updated_at", "error", dbErr)
					}
				}
			}
		}
	}

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
