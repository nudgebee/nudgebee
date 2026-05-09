package api

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/workflow"
	"nudgebee/runbook/services/security"
)

func (s *Server) getRequestDetails(c *gin.Context) (*security.RequestContext, string, bool) {
	sc, err := s.securityContextBuilder.BuildContextFromRequestPayload(c.Request.Context(), c, map[string]string{}, s.tracer, s.meter, s.logger)
	if err != nil || sc == nil || sc.GetSecurityContext() == nil {
		if err != nil {
			s.logger.Error("failed to build security context", "error", err)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, "", false
	}
	accountId := c.Query("account_id")
	if accountId == "" {
		accountId = c.Request.Header.Get("X-Account-ID")
	}
	if accountId == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unable to get account_id"})
		return nil, "", false
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, "", false
	}

	return sc, accountId, true

}

func (s *Server) createWorkflow(c *gin.Context) {

	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	var wf model.Workflow
	contentType := c.ContentType()

	if contentType == "application/yaml" || contentType == "application/x-yaml" {
		err := c.ShouldBindYAML(&wf)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow definition: " + err.Error()})
			return
		}
	} else {
		err := c.ShouldBindJSON(&wf)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow definition: " + err.Error()})
			return
		}
	}

	if wf.Definition.Version == "" {
		wf.Definition.Version = "v1"
	}

	if err := model.ValidateWorkflow(wf); err != nil {
		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			errs := make(map[string]string)
			for _, fieldError := range validationErrors {
				errs[fieldError.Field()] = fieldError.Tag()
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed", "details": errs})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow definition: " + err.Error()})
		}
		return
	}

	wokflowId, token, err := s.workflowService.CreateWorkflow(sc, accountID, wf)
	if err != nil {
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to create workflow", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workflow"})
		return
	}

	resp := gin.H{
		"id": wokflowId,
	}
	if token != "" {
		resp["token"] = token
	}

	c.JSON(http.StatusCreated, resp)
}

func (s *Server) listWorkflows(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	search := model.ListWorkflowRequest{}
	tagsParam := c.Query("tags")
	if tagsParam != "" {
		tagsMap := map[string]string{}
		tags := strings.Split(tagsParam, ",")
		for _, tag := range tags {
			kv := strings.SplitN(tag, ":", 2)
			if len(kv) == 2 {
				tagsMap[kv[0]] = kv[1]
			}
		}
		if len(tagsMap) > 0 {
			search.Tags = tagsMap
		}
	}
	if nameParam := c.Query("name"); nameParam != "" {
		search.Name = nameParam
	}
	if statusParam := c.Query("status"); statusParam != "" {
		search.Status = model.WorkflowStatus(statusParam)
	}
	if lastExecStatusParam := c.Query("last_execution_status"); lastExecStatusParam != "" {
		search.LastExecutionStatus = model.WorkflowExecutionStatus(lastExecStatusParam)
	}
	if typeParam := c.Query("type"); typeParam != "" {
		search.TriggerType = typeParam
	}

	if nextPageToken := c.Query("next_page_token"); nextPageToken != "" {
		search.NextPageToken = nextPageToken
	}
	if limitParam := c.Query("limit"); limitParam != "" {
		var limit int
		_, err := fmt.Sscanf(limitParam, "%d", &limit)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit parameter"})
			return
		}
		search.Limit = limit
	}

	workflows, err := s.workflowService.ListWorkflows(sc, accountID, search)
	if err != nil {
		s.logger.Error("failed to list workflows", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve workflows"})
		return
	}
	c.JSON(http.StatusOK, workflows)
}

func (s *Server) getWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	wf, err := s.workflowService.GetWorkflow(sc, accountID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		} else {
			s.logger.Error("failed to get workflow", "id", id, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve workflow"})
		}
		return
	}
	c.JSON(http.StatusOK, wf)
}

func (s *Server) getWorkflowState(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	state, err := s.workflowService.GetWorkflowState(sc, accountID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		} else {
			s.logger.Error("failed to get workflow state", "id", id, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve workflow state"})
		}
		return
	}
	c.JSON(http.StatusOK, state)
}

func (s *Server) updateWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	var wf model.Workflow
	contentType := c.ContentType()

	if contentType == "application/yaml" || contentType == "application/x-yaml" {
		err := c.ShouldBindYAML(&wf)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow definition: " + err.Error()})
			return
		}
	} else {
		err := c.ShouldBindJSON(&wf)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow definition: " + err.Error()})
			return
		}
	}

	if wf.Definition.Version == "" {
		wf.Definition.Version = "v1"
	}

	if err := model.ValidateWorkflow(wf); err != nil {
		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			errs := make(map[string]string)
			for _, fieldError := range validationErrors {
				errs[fieldError.Field()] = fieldError.Tag()
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed", "details": errs})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow definition: " + err.Error()})
		}
		return
	}

	updatedWf, err := s.workflowService.UpdateWorkflow(sc, accountID, id, wf)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
			return
		}
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to update workflow", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workflow"})
		return
	}

	c.JSON(http.StatusOK, updatedWf)
}

// listWorkflowRuns lists the execution history for a specific workflow ID.
func (s *Server) listWorkflowExecutions(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	workflowID := c.Param("id")
	if workflowID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID is required"})
		return
	}

	request := model.ListWorkflowExecutionRequest{}
	if nextPageToken := c.Query("next_page_token"); nextPageToken != "" {
		request.NextPageToken = nextPageToken
	}
	if limitParam := c.Query("limit"); limitParam != "" {
		var limit int
		_, err := fmt.Sscanf(limitParam, "%d", &limit)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit parameter"})
			return
		}
		request.Limit = limit
	}
	if status := c.Query("status"); status != "" {
		request.Status = model.WorkflowExecutionStatus(status)
	}
	if triggerType := c.Query("type"); triggerType != "" {
		request.TriggerType = triggerType
	}
	if triggeredBy := c.Query("triggered_by"); triggeredBy != "" {
		request.TriggeredBy = triggeredBy
	}
	if orderBy := c.Query("order_by"); orderBy != "" {
		request.OrderBy = orderBy
	}
	if orderDir := c.Query("order_dir"); orderDir != "" {
		request.OrderDir = orderDir
	}

	iter, err := s.workflowService.ListWorkflowExecutions(sc, accountID, workflowID, request)

	if err != nil {
		s.logger.Error("failed to list workflow executions", "workflowID", workflowID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to iterate workflow runs"})
		return
	}
	c.JSON(http.StatusOK, iter)
}

// getWorkflowRun retrieves the detailed history for a single workflow run.
func (s *Server) getWorkflowExecution(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	workflowID := c.Param("id")
	runID := c.Param("execution_id")
	if workflowID == "" || runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID and execution ID are required"})
		return
	}

	details, err := s.workflowService.GetDetailedWorkflowExecution(sc, accountID, workflowID, runID)
	if err != nil {
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow execution not found"})
			return
		}
		s.logger.Error("failed to get workflow execution details", "workflowID", workflowID, "runID", runID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to describe workflow run"})
		return
	}

	c.JSON(http.StatusOK, details)
}

func (s *Server) triggerWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	var inputs map[string]any

	// Limit request body size
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024) // 1MB limit

	if err := c.ShouldBindJSON(&inputs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	we, err := s.workflowService.ExecuteWorkflow(sc, accountID, id, "manual", inputs)
	if err != nil {
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		} else {
			s.logger.Error("failed to trigger workflow", "id", id, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start workflow execution"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"workflow_id":  id,
		"execution_id": we,
	})
}

func (s *Server) retriggerWorkflowExecution(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	executionID := c.Param("execution_id")
	if id == "" || executionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID and execution ID are required"})
		return
	}

	var inputs map[string]any

	// Limit request body size
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024) // 1MB limit

	if err := c.ShouldBindJSON(&inputs); err != nil && err != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	we, err := s.workflowService.RetriggerWorkflowExecution(sc, accountID, id, executionID, inputs)
	if err != nil {
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		} else {
			s.logger.Error("failed to retrigger workflow", "id", id, "executionID", executionID, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrigger workflow execution"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"workflow_id":  id,
		"execution_id": we,
	})
}

func (s *Server) updateWorkflowExecution(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	workflowID := c.Param("id")
	executionID := c.Param("execution_id")
	if workflowID == "" || executionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID and execution ID are required"})
		return
	}

	// Limit request body size
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024) // 1MB limit

	var inputs map[string]any
	if err := c.ShouldBindJSON(&inputs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	err := s.workflowService.UpdateWorkflowExecution(sc, accountID, workflowID, executionID, inputs)
	if err != nil {
		s.logger.Error("failed to update workflow execution", "workflowID", workflowID, "executionID", executionID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workflow execution"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow execution updated successfully"})
}

func (s *Server) handleGenericWebhook(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	workflowID := c.Param("workflowId")
	if workflowID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID is required in the path"})
		return
	}

	// Retrieve the workflow to ensure it exists and has an active webhook trigger
	wf, err := s.workflowService.GetWorkflow(sc, accountID, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
			return
		}
		s.logger.Error("failed to get workflow for webhook", "workflowID", workflowID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve workflow"})
		return
	}

	if wf.Status != model.WorkflowStatusActive {
		s.logger.Warn("webhook trigger rejected for inactive/paused workflow", "workflowID", workflowID, "status", wf.Status)
		c.JSON(http.StatusOK, gin.H{"error": "workflow is not active (status: " + string(wf.Status) + ")"})
		return
	}

	// Check if the webhook trigger is still configured for this workflow
	webhookActive := false
	var configuredSecret string
	var triggerFilter string
	for _, trigger := range wf.Definition.Triggers {
		if trigger.Type == model.WorkflowTriggerWebhook {
			webhookActive = true
			if sec, ok := trigger.Params["secret"].(string); ok {
				configuredSecret = sec
			}
			if f, ok := trigger.Params["filter"].(string); ok {
				triggerFilter = f
			}
			break
		}
	}
	if !webhookActive {
		c.JSON(http.StatusGone, gin.H{"error": "webhook trigger no longer active for this workflow"})
		return
	}

	// Optional: Validate secret if provided in workflow config
	if configuredSecret != "" {
		// Secure comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Webhook-Secret")), []byte(configuredSecret)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized webhook access"})
			return
		}
	}

	// Limit request body size
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024) // 1MB limit

	webhookPayload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		s.logger.Error("failed to read workflow from webhook", "workflowID", workflowID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to execute workflow"})
		return
	}

	// Filter processing
	if triggerFilter != "" {
		var payloadData any
		if json.Unmarshal(webhookPayload, &payloadData) != nil {
			payloadData = string(webhookPayload)
		}

		tplCtx := workflow.NewTemplateContext(nil, nil)
		tplCtx.Secrets = nil // Clear secrets to prevent exfiltration via filter expressions
		tplCtx.Inputs["webhook_payload"] = payloadData
		tplCtx.Vars["webhook_payload"] = payloadData

		rendered, err := workflow.Render(triggerFilter, tplCtx)
		if err != nil {
			s.logger.Error("failed to render webhook filter", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "filter evaluation failed"})
			return
		}

		if strings.ToLower(strings.TrimSpace(rendered)) != "true" {
			s.logger.Info("webhook filtered out", "workflowID", workflowID)
			c.JSON(http.StatusOK, gin.H{"message": "webhook ignored by filter"})
			return
		}
	}

	inputs := map[string]any{
		"webhook_payload": string(webhookPayload),
	}

	runID, err := s.workflowService.ExecuteWorkflow(sc, accountID, workflowID, "webhook", inputs)
	if err != nil {
		s.logger.Error("failed to execute workflow from webhook", "workflowID", workflowID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to execute workflow"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":     "webhook received and workflow execution started",
		"workflowId":  workflowID,
		"executionId": runID,
	})
}

func (s *Server) deleteWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID is required"})
		return
	}

	err := s.workflowService.DeleteWorkflow(sc, accountID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		} else {
			s.logger.Error("failed to delete workflow", "id", id, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete workflow"})
		}
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

func (s *Server) listTemplatingFunctions(c *gin.Context) {
	// Require authentication to avoid exposing internal details publicly
	_, _, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in listTemplatingFunctions", "error", r)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
	}()

	filters, tests := workflow.GetTemplatingDocs()

	c.JSON(http.StatusOK, gin.H{
		"filters": filters,
		"tests":   tests,
	})
}

func (s *Server) handleApproval(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "approval token is required"})
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"` // "approved" or "rejected"
		Result any    `json:"result"`                    // Optional result data
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	sc := security.NewRequestContextForSuperAdmin()
	err := s.workflowService.CompleteApprovalTask(sc, token, req.Status, req.Result)
	if err != nil {
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to complete approval task", "token", token, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete approval task"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "approval task completed successfully"})
}

func (s *Server) cancelWorkflowExecution(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	workflowID := c.Param("id")
	runID := c.Param("execution_id")
	if workflowID == "" || runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID and execution ID are required"})
		return
	}

	err := s.workflowService.CancelWorkflowExecution(sc, accountID, workflowID, runID)
	if err != nil {
		s.logger.Error("failed to cancel workflow execution", "workflowID", workflowID, "runID", runID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel workflow execution"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow execution canceled successfully"})
}

func (s *Server) pauseWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID is required"})
		return
	}

	err := s.workflowService.PauseWorkflow(sc, accountID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		} else {
			s.logger.Error("failed to pause workflow", "id", id, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to pause workflow"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow paused successfully"})
}

func (s *Server) resumeWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow ID is required"})
		return
	}

	err := s.workflowService.ResumeWorkflow(sc, accountID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		} else {
			s.logger.Error("failed to resume workflow", "id", id, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resume workflow"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow resumed successfully"})
}

func (s *Server) listTasks(c *gin.Context) {
	sc, _, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}
	tasks := s.workflowService.ListAllTasks(sc)
	c.JSON(http.StatusOK, tasks)
}

func (s *Server) executeTask(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	taskType := c.Param("task_type")
	if taskType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task type is required"})
		return
	}

	var params map[string]any
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	result, err := s.workflowService.ExecuteTask(sc, accountID, taskType, params)
	if err != nil {
		s.logger.Error("failed to execute task", "taskType", taskType, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": result})
}

func (s *Server) validateWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	var wf model.Workflow
	if err := c.ShouldBindJSON(&wf); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow definition: " + err.Error()})
		return
	}

	if wf.Definition.Version == "" {
		wf.Definition.Version = "v1"
	}

	if err := s.workflowService.ValidateWorkflow(sc, accountID, wf); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow is valid"})
}

func (s *Server) dryRunWorkflow(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	var req model.DryRunWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload: " + err.Error()})
		return
	}

	if req.Definition.Version == "" {
		req.Definition.Version = "v1"
	}

	resp, err := s.workflowService.DryRunWorkflow(sc, accountID, req)
	if err != nil {
		if commonErr, ok := err.(common.Error); ok {
			c.JSON(commonErr.Code, gin.H{"error": commonErr.Message})
			return
		}
		s.logger.Error("failed to dry-run workflow", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to dry-run workflow: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) saveConfig(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	var config model.Config
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config definition: " + err.Error()})
		return
	}

	valueToLog := config.Value
	if config.Type == model.ConfigTypeSecret {
		valueToLog = "*****"
	}
	s.logger.Debug("Validating config", "key", config.Key, "value", valueToLog, "type", config.Type)

	// NEW: Validate the config struct
	if err := model.ValidateConfig(config); err != nil {
		if validationErrors, ok := err.(validator.ValidationErrors); ok {
			errs := make(map[string]string)
			for _, fieldError := range validationErrors {
				errs[fieldError.Field()] = fieldError.Tag()
			}
			s.logger.Info("Config validation failed", "key", config.Key, "error", errs) // Added log
			c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed", "details": errs})
		} else {
			s.logger.Info("Config validation failed", "key", config.Key, "error", err.Error()) // Added log
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config definition: " + err.Error()})
		}
		return
	}

	config.TenantID = sc.GetSecurityContext().GetTenantId()
	config.AccountID = accountID
	config.UpdatedBy = sc.GetSecurityContext().GetUserId()
	if config.CreatedBy == "" {
		config.CreatedBy = sc.GetSecurityContext().GetUserId()
	}

	id, err := s.configService.SaveConfig(c.Request.Context(), config)
	if err != nil {
		s.logger.Error("failed to save config", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (s *Server) listConfigs(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	labels := c.QueryMap("labels")

	configs, err := s.configService.ListConfigs(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, labels)
	if err != nil {
		s.logger.Error("failed to list configs", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list configs"})
		return
	}

	c.JSON(http.StatusOK, configs)
}

func (s *Server) getConfig(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config key is required"})
		return
	}

	// By default, we do NOT return decrypted secrets in API for security.
	// If needed, we can add ?decrypt=true logic, but usually avoided.
	config, err := s.configService.GetConfig(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, key, false)
	if err != nil {
		s.logger.Error("failed to get config", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get config"})
		return
	}

	if config == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "config not found"})
		return
	}

	c.JSON(http.StatusOK, config)
}

func (s *Server) deleteConfig(c *gin.Context) {
	sc, accountID, processRequest := s.getRequestDetails(c)
	if !processRequest {
		return
	}

	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config key is required"})
		return
	}

	err := s.configService.DeleteConfig(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, key)
	if err != nil {
		s.logger.Error("failed to delete config", "key", key, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete config"})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}
