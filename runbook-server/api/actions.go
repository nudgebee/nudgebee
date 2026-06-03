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

func (s *Server) handleAction(c *gin.Context) {
	var actionReq ActionRequest
	if err := c.ShouldBindJSON(&actionReq); err != nil {
		s.logger.Warn("invalid RPC request format", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid request format")}))
		return
	}

	sc, err := s.securityContextBuilder.BuildContextFromPayload(c.Request.Context(), c, &actionReq, s.tracer, s.meter, s.logger)
	if err != nil || sc == nil {
		if err != nil {
			s.logger.Error("failed to build security context for RPC", "error", err)
		}
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized")}))
		return
	}

	args := actionReq.Input
	if input, ok := actionReq.Input["input"].(map[string]any); ok {
		args = input
	} else if arg, ok := actionReq.Input["arg1"].(map[string]any); ok {
		args = arg
	} else if arg, ok := actionReq.Input["request"].(map[string]any); ok {
		args = arg
	}

	switch actionReq.Action.Name {
	case "workflow_create":
		s.handleCreateWorkflow(c, sc, args)
	case "workflow_list":
		s.handleListWorkflows(c, sc, args)
	case "workflow_get":
		s.handleGetWorkflow(c, sc, args)
	case "workflow_update":
		s.handleUpdateWorkflow(c, sc, args)
	case "workflow_delete":
		s.handleDeleteWorkflow(c, sc, args)
	case "workflow_list_callers":
		s.handleListWorkflowCallers(c, sc, args)
	case "workflow_trigger", "workflow_execute":
		s.handleTriggerWorkflow(c, sc, args)
	case "workflow_retrigger_execution", "workflow_replay_execution":
		s.handleRetriggerWorkflowExecution(c, sc, args)
	case "workflow_list_executions":
		s.handleListWorkflowExecutions(c, sc, args)
	case "workflow_get_execution":
		s.handleGetWorkflowExecution(c, sc, args)
	case "workflow_cancel_execution":
		s.handleCancelWorkflowExecution(c, sc, args)
	case "workflow_complete_approval":
		s.handleCompleteWorkflowApproval(c, sc, args)
	case "workflow_validate", "workflow_check":
		s.handleValidateWorkflow(c, sc, args)
	case "workflow_trigger_dryrun", "workflow_dryrun_execute":
		s.handleDryRunWorkflow(c, sc, args)
	case "workflow_list_taskdefinitions":
		s.handleListTasks(c, sc)
	case "workflow_trigger_task", "workflow_execute_task":
		s.handleExecuteTask(c, sc, args)
	case "workflow_pause":
		s.handlePauseWorkflow(c, sc, args)
	case "workflow_resume":
		s.handleResumeWorkflow(c, sc, args)
	case "config_save":
		s.handleSaveConfig(c, sc, args)
	case "config_get":
		s.handleGetConfig(c, sc, args)
	case "config_list":
		s.handleListConfigs(c, sc, args)
	case "config_delete":
		s.handleDeleteConfig(c, sc, args)
	case "workflow_list_templating_functions":
		s.handleListTemplatingFunctions(c, sc)
	case "auto_optimize_trigger":
		s.handleTriggerAutoOptimize(c, sc, args)
	case "workflow_get_count", "workflows_count":
		s.handleWorkflowCount(c, sc, args)
	case "workflow_get_execution_count", "workflows_count_executions":
		s.handleWorkflowExecutionCount(c, sc, args)
	case "workflow_list_template":
		s.handleListTemplates(c, sc, args)
	case "workflow_get_template":
		s.handleGetTemplate(c, sc, args)
	case "workflow_list_mcp_tools":
		s.handleListMCPTools(c, sc, args)
	case "workflow_list_versions":
		s.handleListWorkflowVersions(c, sc, args)
	case "workflow_get_version":
		s.handleGetWorkflowVersion(c, sc, args)
	case "workflow_restore_version", "workflows_update_definition":
		s.handleRestoreWorkflowVersion(c, sc, args)
	case "workflows_publish_version", "workflows_create_version":
		s.handlePublishWorkflowVersion(c, sc, args)
	case "workflows_make_version_live", "workflows_update_live_version":
		s.handleMakeWorkflowVersionLive(c, sc, args)
	case "workflows_update_version_metadata":
		s.handleUpdateWorkflowVersionMetadata(c, sc, args)
	default:
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("action '%s' not supported", actionReq.Action.Name)}))
	}
}

func (s *Server) handleTriggerAutoOptimize(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Error("failed to trigger auto optimize via RPC", "error", err)
		handleServiceError(c, err, "failed to trigger auto optimize")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "auto optimize triggered successfully"})
}

func (s *Server) handleCreateWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Warn("invalid workflow payload from RPC", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid workflow payload")}))
		return
	}

	if workflow.Definition.Version == "" {
		workflow.Definition.Version = "v1"
	}
	if err := model.ValidateWorkflow(workflow); err != nil {
		s.logger.Warn("invalid workflow definition from RPC", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("%s", formatValidationError(err))}))
		return
	}

	workflowID, token, err := s.workflowService.CreateWorkflow(sc, accountID, workflow)
	if err != nil {
		s.logger.Error("failed to create workflow via RPC", "error", err)
		handleServiceError(c, err, "failed to create workflow")
		return
	}

	responsePayload := gin.H{"id": workflowID}
	if token != "" {
		responsePayload["token"] = token
	}
	c.JSON(http.StatusOK, responsePayload)
}

func (s *Server) handleListWorkflows(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Error("failed to list workflows via RPC", "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to list workflows"))
		return
	}

	c.JSON(http.StatusOK, workflows)
}

func (s *Server) handleGetWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
			s.logger.Error("failed to get workflow via RPC", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to get workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, wf)
}

func (s *Server) handleUpdateWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Warn("invalid workflow payload from RPC", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid workflow payload")}))
		return
	}

	if err := model.ValidateWorkflow(workflow); err != nil {
		s.logger.Warn("invalid workflow definition from RPC", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("%s", formatValidationError(err))}))
		return
	}

	updatedWf, err := s.workflowService.UpdateWorkflow(sc, accountID, workflowID, workflow)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
		} else {
			s.logger.Error("failed to update workflow via RPC", "workflowID", workflowID, "error", err)
			handleServiceError(c, err, "failed to update workflow")
		}
		return
	}

	c.JSON(http.StatusOK, updatedWf)
}

func (s *Server) handleListWorkflowCallers(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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

	resp, err := s.workflowService.ListWorkflowCallers(sc, accountID, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
		} else {
			s.logger.Error("failed to list workflow callers", "workflowID", workflowID, "error", err)
			handleServiceError(c, err, "failed to list workflow callers")
		}
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleDeleteWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
			s.logger.Error("failed to delete workflow via RPC", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to delete workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow deleted successfully"})
}

func (s *Server) handleTriggerWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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

	// use_draft_definition routes the trigger to the draft-snapshot path
	// (canvas "Run current" button) instead of the live version. Event-
	// resolution callers below never set this flag.
	useDraft, _ := args["use_draft_definition"].(bool)

	var (
		we     string
		err    error
		callTy = "live"
	)
	if useDraft {
		callTy = "draft"
		we, err = s.workflowService.TriggerWorkflowFromDraft(sc, accountID, workflowID, inputs)
	} else {
		we, err = s.workflowService.ExecuteWorkflow(sc, accountID, workflowID, "manual", inputs)
	}
	if err != nil {
		s.logger.Error("failed to trigger workflow via RPC", "workflowID", workflowID, "mode", callTy, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to trigger workflow"))
		return
	}

	// If triggered from an event context, create an event_resolution record to track this execution
	if eventID, ok := args["event_id"].(string); ok && eventID != "" {
		s.createWorkflowEventResolution(sc, eventID, workflowID, we)
	}

	c.JSON(http.StatusOK, gin.H{"id": workflowID, "execution_id": we})
}

func (s *Server) handleRetriggerWorkflowExecution(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Error("failed to retrigger workflow via RPC", "workflowID", workflowID, "executionID", executionID, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to relay workflow"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": workflowID, "execution_id": we})
}

func (s *Server) handleListWorkflowExecutions(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Error("failed to list workflow executions via RPC", "workflowID", workflowID, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to list workflow executions"))
		return
	}

	c.JSON(http.StatusOK, executions)
}

func (s *Server) handleGetWorkflowExecution(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
			s.logger.Error("failed to get workflow execution details via RPC", "workflowID", workflowID, "runID", runID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to get workflow execution"))
		}
		return
	}

	c.JSON(http.StatusOK, details)
}

func (s *Server) handleCancelWorkflowExecution(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
			s.logger.Error("failed to cancel workflow execution via RPC", "workflowID", workflowID, "runID", runID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to cancel workflow execution"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow execution canceled successfully"})
}

func (s *Server) handleCompleteWorkflowApproval(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
	executionID, ok := args["execution_id"].(string)
	if !ok || executionID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("execution_id is required")}))
		return
	}
	taskID, ok := args["task_id"].(string)
	if !ok || taskID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("task_id is required")}))
		return
	}
	status, ok := args["status"].(string)
	if !ok || status == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("status is required")}))
		return
	}
	comments, _ := args["comments"].(string)

	if err := s.workflowService.CompleteApprovalTaskFromUI(sc, accountID, workflowID, executionID, taskID, status, comments); err != nil {
		var commonErr common.Error
		if errors.As(err, &commonErr) {
			s.logger.Warn("failed to complete approval via RPC", "workflowID", workflowID, "executionID", executionID, "taskID", taskID, "error", err)
			c.JSON(commonErr.Code, buildApiResponse(nil, []error{errors.New(commonErr.Message)}))
			return
		}
		s.logger.Error("failed to complete approval via RPC", "workflowID", workflowID, "executionID", executionID, "taskID", taskID, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to complete approval"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "approval recorded"})
}

func (s *Server) handleValidateWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	if _, ok := args["workflow"].(map[string]any); !ok {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("workflow is required")}))
		return
	}

	var workflow model.Workflow
	if err := common.DecodeMapToStruct(args["workflow"].(map[string]any), &workflow); err != nil {
		s.logger.Warn("invalid workflow payload for validation from RPC", "error", err)
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
		s.logger.Warn("workflow validation failed from RPC", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("%s", formatValidationError(err))}))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow definition is valid"})
}

func (s *Server) handleDryRunWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": "account_id is required"})
		return
	}

	var req model.DryRunWorkflowRequest
	if err := common.DecodeMapToStruct(args, &req); err != nil {
		s.logger.Warn("invalid dry-run request from RPC", "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": "invalid dry-run request payload"})
		return
	}

	if req.Definition.Version == "" {
		req.Definition.Version = "v1"
	}

	dryRunID, executionID, err := s.workflowService.DryRunWorkflowAsync(sc, accountID, req)
	if err != nil {
		s.logger.Error("failed to start dry-run workflow via RPC", "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       "RUNNING",
		"dryrun_id":    dryRunID,
		"execution_id": executionID,
	})
}

func (s *Server) handleListTasks(c *gin.Context, sc *security.RequestContext) {
	tasks := s.workflowService.ListAllTasks(sc)
	c.JSON(http.StatusOK, tasks)
}

func (s *Server) handleExecuteTask(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	taskType, ok := args["task_type"].(string)
	if !ok || taskType == "" {
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": "task_type is required", "result": nil})
		return
	}

	var params map[string]any
	if p, ok := args["params"].(map[string]any); ok {
		params = p
	}

	// account_id may be supplied as the top-level RPC action arg or as a
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
		s.logger.Error("failed to execute task via RPC", "taskType", taskType, "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "FAILED", "error": err.Error(), "result": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "COMPLETED", "result": result})
}

func (s *Server) handleListMCPTools(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to list MCP tools"))
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) handlePauseWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
			s.logger.Error("failed to pause workflow via RPC", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to pause workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow paused successfully"})

}

func (s *Server) handleResumeWorkflow(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
			s.logger.Error("failed to resume workflow via RPC", "workflowID", workflowID, "error", err)
			c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to resume workflow"))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workflow resumed successfully"})

}

func (s *Server) handleSaveConfig(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, _ := args["account_id"].(string)

	if _, ok := args["config"].(map[string]any); !ok {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("config is required")}))
		return
	}

	var config model.Config
	if err := common.DecodeMapToStruct(args["config"].(map[string]any), &config); err != nil {
		s.logger.Warn("invalid config payload from RPC", "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("invalid config payload")}))
		return
	}

	// Empty account_id means tenant-level. Writing tenant-level rows requires
	// tenant-admin (or super-admin); account-level writes require write access
	// to that specific account.
	if accountID == "" {
		if !sc.GetSecurityContext().HasTenantAccess(security.SecurityAccessTypeUpdate) {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized to save tenant-level config")}))
			return
		}
		config.AccountID = nil
	} else {
		if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate) {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized to save config")}))
			return
		}
		acc := accountID
		config.AccountID = &acc
	}

	config.TenantID = sc.GetSecurityContext().GetTenantId()
	config.UpdatedBy = sc.GetSecurityContext().GetUserId()
	if config.CreatedBy == "" {
		config.CreatedBy = sc.GetSecurityContext().GetUserId()
	}

	id, err := s.configService.SaveConfig(c.Request.Context(), config)
	if err != nil {
		s.logger.Error("failed to save config via RPC", "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to save config"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// configAccountScope reads the optional account_id arg and returns:
//   - the *string to pass to ConfigService (nil when tenant-scoped)
//   - whether the caller is authorized to read at that scope
//   - whether the caller is authorized to write at that scope
func configAccountScope(sc *security.RequestContext, args map[string]any) (*string, bool, bool) {
	accountID, _ := args["account_id"].(string)
	if accountID == "" {
		hasRead := sc.GetSecurityContext().HasTenantAccess(security.SecurityAccessTypeRead)
		hasWrite := sc.GetSecurityContext().HasTenantAccess(security.SecurityAccessTypeUpdate)
		return nil, hasRead, hasWrite
	}
	hasRead := sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeRead)
	hasWrite := sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeUpdate)
	acc := accountID
	return &acc, hasRead, hasWrite
}

func (s *Server) handleGetConfig(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, hasRead, _ := configAccountScope(sc, args)
	if !hasRead {
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized to get config")}))
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
		s.logger.Error("failed to get config via RPC", "key", key, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to get config"))
		return
	}

	if config == nil {
		c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{fmt.Errorf("config not found")}))
		return
	}

	c.JSON(http.StatusOK, config)
}

func (s *Server) handleListConfigs(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, hasRead, _ := configAccountScope(sc, args)
	if !hasRead {
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized to list configs")}))
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
		s.logger.Error("failed to list configs via RPC", "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to list configs"))
		return
	}

	c.JSON(http.StatusOK, configs)
}

func (s *Server) handleDeleteConfig(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, _, hasWrite := configAccountScope(sc, args)
	if !hasWrite {
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized to delete config")}))
		return
	}
	key, ok := args["key"].(string)
	if !ok || key == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("key is required")}))
		return
	}

	err := s.configService.DeleteConfig(c.Request.Context(), sc.GetSecurityContext().GetTenantId(), accountID, key)
	if err != nil {
		s.logger.Error("failed to delete config via RPC", "key", key, "error", err)
		c.JSON(http.StatusBadRequest, common.ErrorActionInternal("failed to delete config"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "config deleted successfully"})
}

func (s *Server) handleListTemplatingFunctions(c *gin.Context, sc *security.RequestContext) {
	filters, tests := workflow.GetTemplatingDocs()

	c.JSON(http.StatusOK, gin.H{
		"filters": filters,
		"tests":   tests,
	})
}

func (s *Server) handleWorkflowCount(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Error("failed to count workflows via RPC", "error", err)
		handleServiceError(c, err, "failed to count workflows")
		return
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleWorkflowExecutionCount(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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
		s.logger.Error("failed to count workflow executions via RPC", "error", err)
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
