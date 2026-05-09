package workflow

import (
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

// ListTemplates returns a paginated list of workflow templates.
func (s *Service) ListTemplates(ctx *security.RequestContext, request model.ListWorkflowTemplateRequest) (model.ListWorkflowTemplateResponse, error) {
	templates, totalCount, err := s.templateStore.ListGlobal(ctx.GetContext(), request)
	if err != nil {
		return model.ListWorkflowTemplateResponse{}, fmt.Errorf("failed to list templates: %w", err)
	}

	response := model.ListWorkflowTemplateResponse{
		Templates:  templates,
		TotalCount: totalCount,
	}

	if request.Limit > 0 && len(templates) == request.Limit {
		offset := 0
		if request.NextPageToken != "" {
			_, _ = fmt.Sscanf(request.NextPageToken, "%d", &offset)
		}
		response.NextPageToken = fmt.Sprintf("%d", offset+request.Limit)
	}

	return response, nil
}

// GetTemplate retrieves a single workflow template by ID.
func (s *Service) GetTemplate(ctx *security.RequestContext, id string) (*model.WorkflowTemplate, error) {
	tmpl, err := s.templateStore.FindGlobal(ctx.GetContext(), id)
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	return tmpl, nil
}
