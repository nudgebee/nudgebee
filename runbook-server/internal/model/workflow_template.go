package model

import "time"

// WorkflowTemplate represents a reusable workflow template with metadata about configurable variables.
type WorkflowTemplate struct {
	ID                string             `json:"id"`
	TenantID          string             `json:"tenant_id"`
	AccountID         string             `json:"account_id"`
	Name              string             `json:"name" validate:"required"`
	Description       string             `json:"description,omitempty"`
	Category          string             `json:"category,omitempty"`
	Icon              string             `json:"icon,omitempty"`
	Definition        WorkflowDefinition `json:"definition" validate:"required"`
	TemplateVariables []TemplateVariable `json:"template_variables,omitempty"`
	Tags              map[string]any     `json:"tags,omitempty"`
	IsSystem          bool               `json:"is_system"`
	Status            string             `json:"status,omitempty"`
	CreatedBy         string             `json:"created_by,omitempty"`
	CreatedByUser     *WorkflowUser      `json:"created_by_user,omitempty"`
	UpdatedBy         string             `json:"updated_by,omitempty"`
	UpdatedByUser     *WorkflowUser      `json:"updated_by_user,omitempty"`
	CreatedAt         time.Time          `json:"created_at,omitempty"`
	UpdatedAt         time.Time          `json:"updated_at,omitempty"`
}

// TemplateVariable provides UX metadata for a configurable field in a workflow template.
// It maps to a workflow definition input via InputRef.
type TemplateVariable struct {
	ID          string   `json:"id" validate:"required"`
	InputRef    string   `json:"input_ref,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	HelpText    string   `json:"help_text,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Validation  string   `json:"validation,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Group       string   `json:"group,omitempty"`
	Type        string   `json:"type,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// ListWorkflowTemplateRequest defines the filters for listing workflow templates.
type ListWorkflowTemplateRequest struct {
	Category      string   `json:"category,omitempty"`
	Name          string   `json:"name,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	NextPageToken string   `json:"next_page_token,omitempty"`
	IncludeSystem bool     `json:"include_system,omitempty"`
	EventSources  []string `json:"event_sources,omitempty"`
	AlertNames    []string `json:"alert_names,omitempty"`
	SubjectTypes  []string `json:"subject_types,omitempty"`
}

// ListWorkflowTemplateResponse contains the list of templates with pagination info.
type ListWorkflowTemplateResponse struct {
	Templates     []WorkflowTemplate `json:"templates"`
	NextPageToken string             `json:"next_page_token,omitempty"`
	TotalCount    int                `json:"total_count"`
}
