package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/entitlement"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleEntitlementApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	group := r.Group("/v1/entitlement")
	svc := entitlement.GetService()

	// Check entitlement for a dimension
	group.POST("/check", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "entitlement_check")

		var req entitlement.CheckEntitlementRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check", "invalid_json")
			logger.Error("entitlement: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		status, err := svc.CheckEntitlement(c.Request.Context(), req.TenantID, req.Dimension)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check", "check_failed")
			logger.Error("entitlement: check failed", "error", err, "tenantID", req.TenantID, "dimension", req.Dimension)
			c.JSON(500, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, entitlement.CheckEntitlementResponse{EntitlementStatus: *status})
	})

	// Record usage for a dimension
	group.POST("/record-usage", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "entitlement_record_usage")

		var req entitlement.RecordUsageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_record_usage", "invalid_json")
			logger.Error("entitlement: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := svc.RecordUsage(c.Request.Context(), req)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_record_usage", "record_failed")
			logger.Error("entitlement: record usage failed", "error", err, "tenantID", req.TenantID, "dimension", req.Dimension)
			c.JSON(500, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
	})

	// Check if this is a new incident or follow-up
	group.POST("/is-new-incident", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "entitlement_is_new_incident")

		var req entitlement.IsNewIncidentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_is_new_incident", "invalid_json")
			logger.Error("entitlement: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		isNew, err := svc.IsNewIncident(c.Request.Context(), req.TenantID, req.EventID, req.SessionID)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_is_new_incident", "check_failed")
			logger.Error("entitlement: is new incident check failed", "error", err, "tenantID", req.TenantID, "eventID", req.EventID)
			c.JSON(500, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, entitlement.IsNewIncidentResponse{IsNew: isNew})
	})

	// Get tenant entitlement status
	group.GET("/status/:tenant_id", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "entitlement_status")

		tenantID := c.Param("tenant_id")
		if tenantID == "" {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_status", "missing_tenant_id")
			c.JSON(400, common.ErrorHasuraActionBadRequest("tenant_id is required"))
			return
		}

		status, err := svc.GetTenantStatus(c.Request.Context(), tenantID)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_status", "get_status_failed")
			logger.Error("entitlement: get status failed", "error", err, "tenantID", tenantID)
			c.JSON(500, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, status)
	})

	// Check and record incident (combined operation for llm-server)
	group.POST("/check-incident", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "entitlement_check_incident")

		var req struct {
			TenantID string `json:"tenant_id" binding:"required"`
			EventID  string `json:"event_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check_incident", "invalid_json")
			logger.Error("entitlement: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		allowed, isNew, status, err := svc.CheckAndRecordIncident(c.Request.Context(), req.TenantID, req.EventID)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check_incident", "check_failed")
			logger.Error("entitlement: check incident failed", "error", err, "tenantID", req.TenantID, "eventID", req.EventID)
			c.JSON(500, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, gin.H{
			"allowed": allowed,
			"is_new":  isNew,
			"status":  status,
		})
	})

	// Check and record workflow execution
	group.POST("/check-workflow", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "entitlement_check_workflow")

		var req struct {
			TenantID   string `json:"tenant_id" binding:"required"`
			WorkflowID string `json:"workflow_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check_workflow", "invalid_json")
			logger.Error("entitlement: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		allowed, status, err := svc.CheckAndRecordWorkflowExecution(c.Request.Context(), req.TenantID, req.WorkflowID)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check_workflow", "check_failed")
			logger.Error("entitlement: check workflow failed", "error", err, "tenantID", req.TenantID, "workflowID", req.WorkflowID)
			c.JSON(500, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, gin.H{
			"allowed": allowed,
			"status":  status,
		})
	})

	// Check and record AI workflow step
	group.POST("/check-ai-step", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "entitlement_check_ai_step")

		var req struct {
			TenantID   string `json:"tenant_id" binding:"required"`
			WorkflowID string `json:"workflow_id" binding:"required"`
			TaskID     string `json:"task_id" binding:"required"`
			TaskType   string `json:"task_type" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check_ai_step", "invalid_json")
			logger.Error("entitlement: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		allowed, status, err := svc.CheckAndRecordAIWorkflowStep(c.Request.Context(), req.TenantID, req.WorkflowID, req.TaskID, req.TaskType)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "entitlement_check_ai_step", "check_failed")
			logger.Error("entitlement: check AI step failed", "error", err, "tenantID", req.TenantID, "taskID", req.TaskID)
			c.JSON(500, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, gin.H{
			"allowed": allowed,
			"status":  status,
		})
	})
}
