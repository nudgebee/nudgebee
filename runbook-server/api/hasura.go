package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/workflow"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (s *Server) handleHasuraAction(c *gin.Context) {
	var hasuraReq HasuraActionRequest
	if err := c.ShouldBindJSON(&hasuraReq); err != nil {
		s.logger.Warn("invalid Hasura request format", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid request format")}))
		return
	}

	sc, err := s.securityContextBuilder.BuildContextFromHasuraPayload(c.Request.Context(), c, &hasuraReq, s.tracer, s.meter, s.logger)
	if err != nil || sc == nil {
		if err != nil {
			s.logger.Error("failed to build security context for Hasura", "error", err)
		}
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized")}))
		return
	}

	args := hasuraReq.Input
	if input, ok := hasuraReq.Input["input"].(map[string]any); ok {
		args = input
	} else if arg, ok := hasuraReq.Input["arg1"].(map[string]any); ok {
		args = arg
	} else if arg, ok := hasuraReq.Input["request"].(map[string]any); ok {
		args = arg
	}

	switch hasuraReq.Action.Name {
	case "workflow_create":
		s.handleHasuraCreateWorkflow(c, sc, args)
	case "workflow_list":
		s.handleHasuraListWorkflows(c, sc, args)
	case "workflow_get":
		s.handleHasuraGetWorkflow(c, sc, args)
	case "workflow_update":
		s.handleHasuraUpdateWorkflow(c, sc, args)
	case "workflow_delete":
		s.handleHasuraDeleteWorkflow(c, sc, args)
	case "workflow_trigger":
		s.handleHasuraTriggerWorkflow(c, sc, args)
	case "workflow_retrigger_execution":
		s.handleHasuraRetriggerWorkflowExecution(c, sc, args)
	case "workflow_list_executions":
		s.handleHasuraListWorkflowExecutions(c, sc, args)
	case "workflow_get_execution":
		s.handleHasuraGetWorkflowExecution(c, sc, args)
	case "workflow_cancel_execution":
		s.handleHasuraCancelWorkflowExecution(c, sc, args)
	case "workflow_validate":
		s.handleHasuraValidateWorkflow(c, sc, args)
	case "workflow_trigger_dryrun":
		s.handleHasuraDryRunWorkflow(c, sc, args)
	case "workflow_list_taskdefinitions":
		s.handleHasuraListTasks(c, sc)
	case "workflow_trigger_task":
		s.handleHasuraExecuteTask(c, sc, args)
	case "workflow_pause":
		s.handleHasuraPauseWorkflow(c, sc, args)
	case "workflow_resume":
		s.handleHasuraResumeWorkflow(c, sc, args)
	case "config_save":
		s.handleHasuraSaveConfig(c, sc, args)
	case "config_get":
		s.handleHasuraGetConfig(c, sc, args)
	case "config_list":
		s.handleHasuraListConfigs(c, sc, args)
	case "config_delete":
		s.handleHasuraDeleteConfig(c, sc, args)
	case "workflow_list_templating_functions":
		s.handleHasuraListTemplatingFunctions(c, sc)
	case "auto_optimize_trigger":
		s.handleHasuraTriggerAutoOptimize(c, sc, args)
	case "workflow_get_count":
		s.handleHasuraWorkflowCount(c, sc, args)
	case "workflow_get_execution_count":
		s.handleHasuraWorkflowExecutionCount(c, sc, args)
	case "workflow_list_template":
		s.handleHasuraListTemplates(c, sc, args)
	case "workflow_get_template":
		s.handleHasuraGetTemplate(c, sc, args)
	case "workflow_list_mcp_tools":
		s.handleHasuraListMCPTools(c, sc, args)
	default:
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("action '%s' not supported", hasuraReq.Action.Name)}))
	}
}

func (s *Server) handleHasuraTriggerAutoOptimize(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) {
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized to trigger auto optimize")}))
		return
	}

	autoOptimizeID, ok := args["id"].(string)
	if !ok || autoOptimizeID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("id is required")}))
		return
	}

	aoID, err := uuid.Parse(autoOptimizeID)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("invalid auto optimize id")}))
		return
	}

	if err := s.optimizerService.ExecuteAutoOptimize(c.Request.Context(), aoID); err != nil {
		s.logger.Error("failed to trigger auto optimize via Hasura", "error", err)
		handleServiceError(c, err, "failed to trigger auto optimize")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "auto optimize triggered successfully"})
}

func (s *Server) handleHasuraCreateWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	if _, ok := args["workflow"].(map[string]any); !ok {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow is required")}))
		return
	}

	var workflow model.Workflow
	if err := common.DecodeMapToStruct(args["workflow"].(map[string]any), &workflow); err != nil {
		s.logger.Warn("invalid workflow payload from Hasura", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid workflow payload")}))
		return
	}

	if workflow.Definition.Version == "" {
		workflow.Definition.Version = "v1"
	}
	if err := model.ValidateWorkflow(workflow); err != nil {
		s.logger.Warn("invalid workflow definition from Hasura", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("%s", formatValidationError(err))}))
		return
	}

	workflowID, token, err := s.workflowService.CreateWorkflow(sc, accountID, workflow)
	if err != nil {
		s.logger.Error("failed to create workflow via Hasura", "error", err)
		handleServiceError(c, err, "failed to create workflow")
		return
	}

	responsePayload := gin.H{"id": workflowID}
	if token != "" {
		responsePayload["token"] = token
	}
	c.JSON(http.StatusOK, responsePayload)
}

func (s *Server) handleHasuraListWorkflows(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	var search model.ListWorkflowRequest
	if name, ok := args["name"].(string); ok {
		search.Name = name
	}
	if status, ok := args["status"].(string); ok {
		search.Status = model.WorkflowStatus(status)
	}
	if lastExecStatus, ok := args["last_execution_status"].(string); ok {
		search.LastExecutionStatus = model.WorkflowExecutionStatus(lastExecStatus)
	}
	if triggerType, ok := args["type"].(string); ok {
		search.TriggerType = triggerType
	}
	if tagsStr, ok := args["tags"].(string); ok {
		tagsStr = strings.TrimSpace(tagsStr)
		if tagsStr != "" {
			tagsMap := map[string]string{}
			var simpleTags []string
			for _, tag := range strings.Split(tagsStr, ",") {
				tag = strings.TrimSpace(tag)
				if tag == "" {
					continue
				}
				if kv := strings.SplitN(tag, ":", 2); len(kv) == 2 {
					tagsMap[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				} else {
					simpleTags = append(simpleTags, tag)
				}
			}
			if len(tagsMap) > 0 {
				search.Tags = tagsMap
			}
			if len(simpleTags) > 0 {
				search.TagsText = simpleTags
			}
		}
	}
	if limit, ok := args["limit"].(float64); ok {
		search.Limit = int(limit)
	}
	if createdBy, ok := args["created_by"].(string); ok {
		search.CreatedBy = createdBy
	}
	if nextPageToken, ok := args["next_page_token"].(string); ok {
		search.NextPageToken = nextPageToken
	}

	workflows, err := s.workflowService.ListWorkflows(sc, accountID, search)
	if err != nil {
		s.logger.Error("failed to list workflows via Hasura", "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to list workflows"))
		return
	}

	c.JSON(http.StatusOK, workflows)
}

func (s *Server) handleHasuraGetWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}

	wf, err := s.workflowService.GetWorkflow(sc, accountID, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
		} else {
			s.logger.Error("failed to get workflow via Hasura", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to get workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, wf)
}

func (s *Server) handleHasuraUpdateWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}

	if _, ok := args["workflow"].(map[string]any); !ok {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow is required")}))
		return
	}

	var workflow model.Workflow
	if err := common.DecodeMapToStruct(args["workflow"].(map[string]any), &workflow); err != nil {
		s.logger.Warn("invalid workflow payload from Hasura", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid workflow payload")}))
		return
	}

	if err := model.ValidateWorkflow(workflow); err != nil {
		s.logger.Warn("invalid workflow definition from Hasura", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("%s", formatValidationError(err))}))
		return
	}

	updatedWf, err := s.workflowService.UpdateWorkflow(sc, accountID, workflowID, workflow)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
		} else {
			s.logger.Error("failed to update workflow via Hasura", "workflowID", workflowID, "error", err)
			handleServiceError(c, err, "failed to update workflow")
		}
		return
	}

	c.JSON(http.StatusOK, updatedWf)
}

func (s *Server) handleHasuraDeleteWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}

	err := s.workflowService.DeleteWorkflow(sc, accountID, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
		} else {
			s.logger.Error("failed to delete workflow via Hasura", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to delete workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow deleted successfully"})
}

func (s *Server) handleHasuraTriggerWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}

	var inputs map[string]any
	if i, ok := args["inputs"].(map[string]any); ok {
		inputs = i
	}

	we, err := s.workflowService.ExecuteWorkflow(sc, accountID, workflowID, "manual", inputs)
	if err != nil {
		s.logger.Error("failed to trigger workflow via Hasura", "workflowID", workflowID, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to trigger workflow"))
		return
	}

	// If triggered from an event context, create an event_resolution record to track this execution
	if eventID, ok := args["event_id"].(string); ok && eventID != "" {
		s.createWorkflowEventResolution(sc, eventID, workflowID, we)
	}

	c.JSON(http.StatusOK, gin.H{"id": workflowID, "execution_id": we})
}

func (s *Server) handleHasuraRetriggerWorkflowExecution(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["workflow_id"].(string)
	if !ok || workflowID == "" {
		workflowID, ok = args["id"].(string)
		if !ok || workflowID == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow_id is required")}))
			return
		}
	}
	executionID, ok := args["execution_id"].(string)
	if !ok || executionID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("execution_id is required")}))
		return
	}

	var inputs map[string]any
	if i, ok := args["inputs"].(map[string]any); ok {
		inputs = i
	}

	we, err := s.workflowService.RetriggerWorkflowExecution(sc, accountID, workflowID, executionID, inputs)
	if err != nil {
		s.logger.Error("failed to retrigger workflow via Hasura", "workflowID", workflowID, "executionID", executionID, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to relay workflow"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": workflowID, "execution_id": we})
}

func (s *Server) handleHasuraListWorkflowExecutions(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}

	var request model.ListWorkflowExecutionRequest
	if limit, ok := args["limit"].(float64); ok {
		request.Limit = int(limit)
	}
	if nextPageToken, ok := args["next_page_token"].(string); ok {
		request.NextPageToken = nextPageToken
	}
	if status, ok := args["status"].(string); ok {
		request.Status = model.WorkflowExecutionStatus(status)
	}
	if triggerType, ok := args["type"].(string); ok {
		request.TriggerType = triggerType
	}
	if triggeredBy, ok := args["triggered_by"].(string); ok {
		request.TriggeredBy = triggeredBy
	}
	if orderBy, ok := args["order_by"].(string); ok {
		request.OrderBy = orderBy
	}
	if orderDir, ok := args["order_dir"].(string); ok {
		request.OrderDir = orderDir
	}

	executions, err := s.workflowService.ListWorkflowExecutions(sc, accountID, workflowID, request)
	if err != nil {
		s.logger.Error("failed to list workflow executions via Hasura", "workflowID", workflowID, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to list workflow executions"))
		return
	}

	c.JSON(http.StatusOK, executions)
}

func (s *Server) handleHasuraGetWorkflowExecution(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["workflow_id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow_id is required")}))
		return
	}
	runID, ok := args["id"].(string)
	if !ok || runID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("id is required")}))
		return
	}

	details, err := s.workflowService.GetDetailedWorkflowExecution(sc, accountID, workflowID, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow execution or definition not found")}))
		} else {
			s.logger.Error("failed to get workflow execution details via Hasura", "workflowID", workflowID, "runID", runID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to get workflow execution"))
		}
		return
	}

	c.JSON(http.StatusOK, details)
}

func (s *Server) handleHasuraCancelWorkflowExecution(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}
	runID, ok := args["execution_id"].(string)
	if !ok || runID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("execution_id is required")}))
		return
	}

	err := s.workflowService.CancelWorkflowExecution(sc, accountID, workflowID, runID)
	if err != nil {
		var commonErr common.Error
		if (errors.As(err, &commonErr) && commonErr.Code == 404) || errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow execution not found")}))
		} else {
			s.logger.Error("failed to cancel workflow execution via Hasura", "workflowID", workflowID, "runID", runID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to cancel workflow execution"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow execution canceled successfully"})
}

func (s *Server) handleHasuraValidateWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	if _, ok := args["workflow"].(map[string]any); !ok {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow is required")}))
		return
	}

	var workflow model.Workflow
	if err := common.DecodeMapToStruct(args["workflow"].(map[string]any), &workflow); err != nil {
		s.logger.Warn("invalid workflow payload for validation from Hasura", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid workflow payload")}))
		return
	}

	if workflow.Definition.Version == "" {
		workflow.Definition.Version = "v1"
	}

	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	if err := s.workflowService.ValidateWorkflow(sc, accountID, workflow); err != nil {
		s.logger.Warn("workflow validation failed from Hasura", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("%s", formatValidationError(err))}))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow definition is valid"})
}

func (s *Server) handleHasuraDryRunWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": "account_id is required"})
		return
	}

	var req model.DryRunWorkflowRequest
	if err := common.DecodeMapToStruct(args, &req); err != nil {
		s.logger.Warn("invalid dry-run request from Hasura", "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": "invalid dry-run request payload"})
		return
	}

	if req.Definition.Version == "" {
		req.Definition.Version = "v1"
	}

	dryRunID, executionID, err := s.workflowService.DryRunWorkflowAsync(sc, accountID, req)
	if err != nil {
		s.logger.Error("failed to start dry-run workflow via Hasura", "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       "RUNNING",
		"dryrun_id":    dryRunID,
		"execution_id": executionID,
	})
}

func (s *Server) handleHasuraListTasks(c *gin.Context, sc *security.RequestContext) {
	tasks := s.workflowService.ListAllTasks(sc)
	c.JSON(http.StatusOK, tasks)
}

func (s *Server) handleHasuraExecuteTask(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	taskType, ok := args["task_type"].(string)
	if !ok || taskType == "" {
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": "task_type is required", "result": nil})
		return
	}

	var params map[string]any
	if p, ok := args["params"].(map[string]any); ok {
		params = p
	}

	// account_id may be supplied as the top-level Hasura action arg or as a
	// task param. The task param wins when both are present and is the only
	// required source — callers that already select an account in the form
	// (e.g. observability.metrics) need not duplicate it on the outer arg.
	accountID, _ := args["account_id"].(string)
	if v, ok := params["account_id"].(string); ok && v != "" {
		accountID = v
	}
	if accountID == "" {
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": "account_id is required", "result": nil})
		return
	}

	result, err := s.workflowService.ExecuteTask(sc, accountID, taskType, params)
	if err != nil {
		s.logger.Error("failed to execute task via Hasura", "taskType", taskType, "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": err.Error(), "result": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "COMPLETED", "result": result})
}

func (s *Server) handleHasuraListMCPTools(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	var params map[string]any
	if p, ok := args["params"].(map[string]any); ok {
		params = p
	}

	result, err := s.workflowService.ListMCPTools(sc, accountID, params)
	if err != nil {
		s.logger.Error("failed to list MCP tools", "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to list MCP tools"))
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) handleHasuraPauseWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}

	err := s.workflowService.PauseWorkflow(sc, accountID, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
		} else {
			s.logger.Error("failed to pause workflow via Hasura", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to pause workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow paused successfully"})

}

func (s *Server) handleHasuraResumeWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	workflowID, ok := args["id"].(string)
	if !ok || workflowID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow id is required")}))
		return
	}

	err := s.workflowService.ResumeWorkflow(sc, accountID, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
		} else {
			s.logger.Error("failed to resume workflow via Hasura", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to resume workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow resumed successfully"})

}

func (s *Server) handleHasuraSaveConfig(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	if _, ok := args["config"].(map[string]any); !ok {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("config is required")}))
		return
	}

	var config model.Config
	if err := common.DecodeMapToStruct(args["config"].(map[string]any), &config); err != nil {
		s.logger.Warn("invalid config payload from Hasura", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid config payload")}))
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
		s.logger.Error("failed to save config via Hasura", "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to save config"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

func (s *Server) handleHasuraGetConfig(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	key, ok := args["key"].(string)
	if !ok || key == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("key is required")}))
		return
	}

	// Check if decryption is requested, default to false (masked)
	decrypt := false
	if d, ok := args["decrypt"].(bool); ok {
		decrypt = d
	}

	config, err := s.configService.GetConfig(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, key, decrypt)
	if err != nil {
		s.logger.Error("failed to get config via Hasura", "key", key, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to get config"))
		return
	}

	if config == nil {
		c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{fmt.Errorf("config not found")}))
		return
	}

	c.JSON(http.StatusOK, config)
}

func (s *Server) handleHasuraListConfigs(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	var labels map[string]string
	if labelsArg, ok := args["labels"].(map[string]any); ok {
		labels = make(map[string]string)
		for k, v := range labelsArg {
			if strVal, ok := v.(string); ok {
				labels[k] = strVal
			}
		}
	}

	// Check if decryption is requested, default to false (masked)
	decrypt := false
	if d, ok := args["decrypt"].(bool); ok {
		decrypt = d
	}

	var configs []model.Config
	var err error

	if decrypt {
		configs, err = s.configService.ListConfigsDecrypted(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, labels)
	} else {
		configs, err = s.configService.ListConfigs(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, labels)
	}

	if err != nil {
		s.logger.Error("failed to list configs via Hasura", "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to list configs"))
		return
	}

	c.JSON(http.StatusOK, configs)
}

func (s *Server) handleHasuraDeleteConfig(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}
	key, ok := args["key"].(string)
	if !ok || key == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("key is required")}))
		return
	}

	err := s.configService.DeleteConfig(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, key)
	if err != nil {
		s.logger.Error("failed to delete config via Hasura", "key", key, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal("failed to delete config"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "config deleted successfully"})
}

func (s *Server) handleHasuraListTemplatingFunctions(c *gin.Context, sc *security.RequestContext) {
	filters, tests := workflow.GetTemplatingDocs()

	c.JSON(http.StatusOK, gin.H{
		"filters": filters,
		"tests":   tests,
	})
}

func (s *Server) handleHasuraWorkflowCount(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	req := model.WorkflowCountRequest{
		AccountID: accountID,
	}

	if status, ok := args["status"].(string); ok && status != "" {
		req.Status = model.WorkflowStatus(status)
	}

	if triggerType, ok := args["trigger_type"].(string); ok && triggerType != "" {
		req.TriggerType = triggerType
	}

	response, err := s.workflowService.CountWorkflows(sc, req)
	if err != nil {
		s.logger.Error("failed to count workflows via Hasura", "error", err)
		handleServiceError(c, err, "failed to count workflows")
		return
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleHasuraWorkflowExecutionCount(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("account_id is required")}))
		return
	}

	req := model.WorkflowExecutionCountRequest{
		AccountID: accountID,
	}

	if status, ok := args["status"].(string); ok && status != "" {
		req.Status = model.WorkflowExecutionStatus(status)
	}

	if triggerType, ok := args["trigger_type"].(string); ok && triggerType != "" {
		req.TriggerType = triggerType
	}

	if workflowID, ok := args["workflow_id"].(string); ok && workflowID != "" {
		req.WorkflowID = workflowID
	}

	if startDateStr, ok := args["start_date"].(string); ok && startDateStr != "" {
		t, err := parseTimestamp(startDateStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("invalid start_date format: %v", err)}))
			return
		}
		req.StartDate = &t
	}

	if endDateStr, ok := args["end_date"].(string); ok && endDateStr != "" {
		t, err := parseTimestamp(endDateStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("invalid end_date format: %v", err)}))
			return
		}
		req.EndDate = &t
	}

	response, err := s.workflowService.CountWorkflowExecutions(sc, req)
	if err != nil {
		s.logger.Error("failed to count workflow executions via Hasura", "error", err)
		handleServiceError(c, err, "failed to count workflow executions")
		return
	}

	c.JSON(http.StatusOK, response)
}

// parseTimestamp parses a timestamp string in various formats
func parseTimestamp(s string) (time.Time, error) {
	// Try RFC3339 first
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}

	// Try without timezone
	t, err = time.Parse("2006-01-02T15:04:05", s)
	if err == nil {
		return t, nil
	}

	// Try date only
	t, err = time.Parse("2006-01-02", s)
	if err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", s)
}

// createWorkflowEventResolution creates an event_resolution record linking a workflow execution to an event.
// This is a best-effort operation — failures are logged but don't block the trigger response.
func (s *Server) createWorkflowEventResolution(sc *security.RequestContext, eventID, workflowID, runID string) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		s.logger.Error("failed to get DB for event resolution", "error", err)
		return
	}

	typeRefID := workflowID + ":" + runID
	resolverID := sc.GetSecurityContext().GetUserId()

	_, err = db.Db.Exec(`
		INSERT INTO event_resolution (id, event_id, type, status, type_reference_id, resolver_type, resolver_id, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'WorkflowExecution', 'InProgress', $2, 'User', $3, now(), now())`,
		eventID, typeRefID, resolverID)
	if err != nil {
		s.logger.Error("failed to create workflow event resolution", "eventID", eventID, "workflowID", workflowID, "error", err)
	}
}
