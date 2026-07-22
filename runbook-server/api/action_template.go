package api

import (
	"errors"
	"fmt"
	"net/http"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleListTemplates(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	templateType, ok := args["type"].(string)
	if !ok || templateType == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("type is required")}))
		return
	}
	if templateType != "system" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("unsupported template type: %s", templateType)}))
		return
	}

	var request model.ListWorkflowTemplateRequest
	if category, ok := args["category"].(string); ok {
		request.Category = category
	}
	if name, ok := args["name"].(string); ok {
		request.Name = name
	}
	if limit, ok := args["limit"].(float64); ok {
		request.Limit = int(limit)
	}
	if nextPageToken, ok := args["next_page_token"].(string); ok {
		request.NextPageToken = nextPageToken
	}
	if sources, ok := args["event_sources"].([]interface{}); ok {
		for _, v := range sources {
			if s, ok := v.(string); ok {
				request.EventSources = append(request.EventSources, s)
			}
		}
	}
	if names, ok := args["alert_names"].([]interface{}); ok {
		for _, v := range names {
			if s, ok := v.(string); ok {
				request.AlertNames = append(request.AlertNames, s)
			}
		}
	}
	if subjectTypes, ok := args["subject_types"].([]interface{}); ok {
		for _, v := range subjectTypes {
			if s, ok := v.(string); ok {
				request.SubjectTypes = append(request.SubjectTypes, s)
			}
		}
	}
	if labels, ok := args["labels"].([]interface{}); ok {
		for _, v := range labels {
			if s, ok := v.(string); ok {
				request.Labels = append(request.Labels, s)
			}
		}
	}

	response, err := s.workflowService.ListTemplates(sc, request)
	if err != nil {
		s.logger.Error("failed to list templates via RPC", "error", err)
		c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{errors.New("failed to list templates")}))
		return
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleGetTemplate(c *gin.Context, sc *security.RequestContext, args map[string]any) {
	templateType, ok := args["type"].(string)
	if !ok || templateType == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("type is required")}))
		return
	}
	if templateType != "system" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("unsupported template type: %s", templateType)}))
		return
	}

	templateID, ok := args["id"].(string)
	if !ok || templateID == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{fmt.Errorf("template id is required")}))
		return
	}

	tmpl, err := s.workflowService.GetTemplate(sc, templateID)
	if err != nil {
		s.logger.Error("failed to get template via RPC", "templateID", templateID, "error", err)
		c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{errors.New("failed to get template")}))
		return
	}

	c.JSON(http.StatusOK, tmpl)
}

// handleSyncTemplates forces an immediate system-template reconcile from the
// configured source. Tenant-admin only; system templates are tenant-wide.
func (s *Server) handleSyncTemplates(c *gin.Context, sc *security.RequestContext, _ map[string]any) {
	if !sc.GetSecurityContext().HasTenantAccess(security.SecurityAccessTypeUpdate) {
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("unauthorized: tenant admin required to sync templates")}))
		return
	}
	if s.templateSyncer == nil {
		c.JSON(http.StatusServiceUnavailable, buildApiResponse(nil, []error{errors.New("template sync is not configured")}))
		return
	}

	res, err := s.templateSyncer.SyncOnce(c.Request.Context())
	if err != nil {
		s.logger.Error("failed to sync templates via RPC", "error", err)
		c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{errors.New("failed to sync templates")}))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "templates synced",
		"upserted":    res.Upserted,
		"deactivated": res.Deactivated,
	})
}
