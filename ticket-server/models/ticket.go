package models

import (
	"time"
)

type Ticket struct {
	ID               string      `json:"id,omitempty" db:"id"`
	Title            string      `json:"title,omitempty" db:"title"`
	Description      string      `json:"description,omitempty" db:"description"`
	Severity         string      `json:"severity,omitempty" db:"severity"`
	Tags             string      `json:"tags,omitempty" db:"tags"`
	TicketType       string      `json:"ticket_type,omitempty" db:"ticket_type"`
	ProjectKey       string      `json:"project_key,omitempty" db:"project_key"`
	Assignee         string      `json:"assignee,omitempty" db:"assignee"`
	Reporter         string      `json:"reporter,omitempty" db:"reporter"`
	Platform         string      `json:"platform,omitempty" db:"platform"`
	Source           string      `json:"source" db:"source"`
	ReferenceID      string      `json:"reference_id" db:"reference_id"`
	CreatedBy        string      `json:"created_by,omitempty" db:"created_by"`
	Tenant           string      `json:"tenant,omitempty" db:"tenant"`
	AccountID        string      `json:"account_id" db:"account_id"`
	Status           string      `json:"status,omitempty" db:"status"`
	TicketID         string      `json:"ticket_id" db:"ticket_id"`
	IntegrationID    string      `json:"integration_id" db:"integration_id"`
	URL              string      `json:"url,omitempty" db:"url"`
	New              bool        `json:"is_new,omitempty" db:"-"`
	CreatedAt        *time.Time  `json:"created_at,omitempty" db:"created_at"`
	AdditionalFields interface{} `json:"additional_fields,omitempty" db:"-"`
	Comment          string      `json:"comment,omitempty" db:"-"`
	// Raw carries every field returned by the source platform on Get,
	// including provider-specific fields not in the normalized Ticket struct
	// (e.g. ServiceNow cmdb_ci, business_service, all u_* custom fields).
	// Populated only by Get; nil otherwise. Reference fields keep their
	// {"value": "...", "display_value": "..."} shape from the source.
	Raw map[string]any `json:"raw,omitempty" db:"-"`
}

type Comments struct {
	Author  string `json:"author"`
	Comment string `json:"comment"`
	Created string `json:"created_at"`
	Updated string `json:"updated_at"`
}

type CommentsResponse struct {
	TicketID string     `json:"ticket_id"`
	Error    string     `json:"error"`
	Comments []Comments `json:"comments"`
}

type ResponseData struct {
	JiraConfigurations []TicketConfigurations `json:"jira_configurations"`
}

type ConfigResponse struct {
	Data ResponseData `json:"data"`
}

type TicketResponse struct {
	Data struct {
		Tickets []Ticket `json:"tickets"`
	} `json:"data"`
}

type TicketInsertResponse struct {
	Data Data `json:"data"`
}

type Data struct {
	InsertTicketsOne InsertTicketsOne `json:"insert_tickets_one"`
}

type InsertTicketsOne struct {
	ID          string `json:"id"`
	Severity    string `json:"severity,omitempty"`
	Platform    string `json:"platform,omitempty"`
	ReferenceId string `json:"reference_id,omitempty"`
	TicketID    string `json:"ticket_id,omitempty"`
	URL         string `json:"url,omitempty"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
	Action      string `json:"action,omitempty"`
	Message     string `json:"message,omitempty"`
}

type TicketFilter struct {
	Source      string `json:"source"`
	ReferenceId string `json:"reference_id"`
}

type UpdateFields struct {
	Status      string   `json:"status,omitempty"`
	Severity    string   `json:"severity,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Description string   `json:"description,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	ProjectKey  string   `json:"project_key,omitempty"`
}

type TicketRequest struct {
	Action           Action           `json:"action"`
	Input            TicketInput      `json:"input"`
	RequestQuery     string           `json:"request_query"`
	SessionVariables SessionVariables `json:"session_variables"`
}

type Action struct {
	Name string `json:"name"`
}

type TicketInput struct {
	Object Ticket `json:"object"`
}

type SessionVariables struct {
	Role         string `json:"role"`
	UserID       string `json:"user_id"`
	UserTenantID string `json:"tenant_id"`
}

// ListParams defines common filters for listing tickets across all providers.
type ListParams struct {
	ProjectKey    string `json:"project_key"`              // Required: Jira project key, GitHub "owner/repo", GitLab project path, PD/ZD service ID
	Status        string `json:"status,omitempty"`         // Filter by status (provider-normalized)
	Priority      string `json:"priority,omitempty"`       // Filter by priority/urgency/severity
	Assignee      string `json:"assignee,omitempty"`       // Filter by assignee
	Limit         int    `json:"limit,omitempty"`          // Page size (default 20, max 100)
	Offset        int    `json:"offset,omitempty"`         // Offset for pagination
	CreatedAfter  string `json:"created_after,omitempty"`  // ISO 8601 datetime
	CreatedBefore string `json:"created_before,omitempty"` // ISO 8601 datetime
	SortBy        string `json:"sort_by,omitempty"`        // "created_at" or "updated_at" (default: "created_at")
	SortOrder     string `json:"sort_order,omitempty"`     // "asc" or "desc" (default: "desc")
}

// ListResult is the normalized response from listing tickets.
type ListResult struct {
	Tickets []Ticket `json:"tickets"`
	Total   int      `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

// ListTicketsRequest is the request payload for the list tickets endpoint.
type ListTicketsRequest struct {
	IntegrationID string     `json:"integration_id"`
	AccountID     string     `json:"account_id"`
	Params        ListParams `json:"params"`
}

type TemplateIssueType struct {
	Name   string            `json:"name"`
	Fields map[string]Fields `json:"fields"`
}

type Fields struct {
	Name            string      `json:"name"`
	Key             string      `json:"key"`
	Required        bool        `json:"required"`
	AutoCompleteUrl string      `json:"autoCompleteUrl,omitempty"`
	Type            string      `json:"type"`
	AllowedValues   interface{} `json:"allowedValues,omitempty"`
}
