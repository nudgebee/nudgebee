package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
)

func parseVersionArgs(args map[string]any) (accountID, workflowID string, versionNumber int, err error) {
	accountID, ok := args["account_id"].(string)
	if !ok || accountID == "" {
		return "", "", 0, fmt.Errorf("account_id is required")
	}
	workflowID, ok = args["id"].(string)
	if !ok || workflowID == "" {
		return "", "", 0, fmt.Errorf("workflow id is required")
	}
	switch v := args["version_number"].(type) {
	case float64:
		versionNumber = int(v)
	case int:
		versionNumber = v
	case int64:
		versionNumber = int(v)
	default:
		return "", "", 0, fmt.Errorf("version_number is required and must be an integer")
	}
	if versionNumber <= 0 {
		return "", "", 0, fmt.Errorf("version_number must be positive")
	}
	return accountID, workflowID, versionNumber, nil
}

func (s *Server) handleListWorkflowVersions(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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

	versions, err := s.workflowService.ListWorkflowVersions(sc, accountID, workflowID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
			return
		}
		s.logger.Error("failed to list workflow versions", "workflowID", workflowID, "error", err)
		handleServiceError(c, err, "failed to list workflow versions")
		return
	}
	if versions == nil {
		versions = []model.WorkflowVersion{}
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (s *Server) handleGetWorkflowVersion(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, workflowID, versionNumber, err := parseVersionArgs(args)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}

	version, err := s.workflowService.GetWorkflowVersion(sc, accountID, workflowID, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow version not found")}))
			return
		}
		s.logger.Error("failed to get workflow version", "workflowID", workflowID, "versionNumber", versionNumber, "error", err)
		handleServiceError(c, err, "failed to get workflow version")
		return
	}
	c.JSON(http.StatusOK, version)
}

func (s *Server) handleRestoreWorkflowVersion(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, workflowID, versionNumber, err := parseVersionArgs(args)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}

	wf, err := s.workflowService.RestoreWorkflowVersion(sc, accountID, workflowID, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow version not found")}))
			return
		}
		s.logger.Error("failed to restore workflow version", "workflowID", workflowID, "versionNumber", versionNumber, "error", err)
		handleServiceError(c, err, "failed to restore workflow version")
		return
	}
	c.JSON(http.StatusOK, wf)
}

func (s *Server) handlePublishWorkflowVersion(c *gin.Context, sc *security.RequestContext, args map[string]any) {
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

	name := nullableStringArg(args, "name")
	description := nullableStringArg(args, "description")
	setLive := true
	if v, ok := args["set_live"].(bool); ok {
		setLive = v
	}
	// status decides what state the new version (and, when setLive=true, the
	// workflow row) lands in. Empty / missing falls through to the service
	// default (PAUSED) so older clients that omit the arg continue to work.
	var status model.WorkflowStatus
	if s, ok := args["status"].(string); ok && s != "" {
		status = model.WorkflowStatus(s)
	}

	v, err := s.workflowService.PublishWorkflow(sc, accountID, workflowID, name, description, setLive, status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow not found")}))
			return
		}
		s.logger.Error("failed to publish workflow version", "workflowID", workflowID, "error", err)
		handleServiceError(c, err, "failed to publish workflow version")
		return
	}
	c.JSON(http.StatusOK, v)
}

func (s *Server) handleMakeWorkflowVersionLive(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, workflowID, versionNumber, err := parseVersionArgs(args)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}

	wf, err := s.workflowService.SetLiveWorkflowVersion(sc, accountID, workflowID, versionNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow or version not found")}))
			return
		}
		s.logger.Error("failed to set live workflow version", "workflowID", workflowID, "versionNumber", versionNumber, "error", err)
		handleServiceError(c, err, "failed to set live workflow version")
		return
	}
	c.JSON(http.StatusOK, wf)
}

func (s *Server) handleUpdateWorkflowVersionMetadata(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, workflowID, versionNumber, err := parseVersionArgs(args)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}
	name := nullableStringArg(args, "name")
	description := nullableStringArg(args, "description")

	v, err := s.workflowService.UpdateWorkflowVersionMetadata(sc, accountID, workflowID, versionNumber, name, description)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow version not found")}))
			return
		}
		s.logger.Error("failed to update workflow version metadata", "workflowID", workflowID, "versionNumber", versionNumber, "error", err)
		handleServiceError(c, err, "failed to update workflow version metadata")
		return
	}
	c.JSON(http.StatusOK, v)
}

func (s *Server) handleUpdateWorkflowVersionStatus(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	accountID, workflowID, versionNumber, err := parseVersionArgs(args)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}
	statusStr, ok := args["status"].(string)
	if !ok || statusStr == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("status is required")}))
		return
	}
	v, err := s.workflowService.UpdateWorkflowVersionStatus(sc, accountID, workflowID, versionNumber, model.WorkflowStatus(statusStr))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{errors.New("workflow version not found")}))
			return
		}
		s.logger.Error("failed to update workflow version status", "workflowID", workflowID, "versionNumber", versionNumber, "error", err)
		handleServiceError(c, err, "failed to update workflow version status")
		return
	}
	c.JSON(http.StatusOK, v)
}

// nullableStringArg returns a *string only if the key is present in args. A
// missing key yields nil (leave column unchanged); an explicit empty string
// yields a pointer to "" (clear column).
func nullableStringArg(args map[string]any, key string) *string {
	raw, present := args[key]
	if !present {
		return nil
	}
	if raw == nil {
		s := ""
		return &s
	}
	if s, ok := raw.(string); ok {
		v := s
		return &v
	}
	return nil
}
