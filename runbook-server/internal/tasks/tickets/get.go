package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsGetTask defines a task for getting ticket details.
type TicketsGetTask struct{}

func (t *TicketsGetTask) GetName() string {
	return "tickets.get"
}

// GetDescription returns a brief description of the task.
func (t *TicketsGetTask) GetDescription() string {
	return "Fetch the details of an existing ticket by its ID."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsGetTask) GetDisplayName() string {
	return "Get Ticket"
}

func (t *TicketsGetTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Get Ticket Task", "params", params)

	ticketId, err := extractRequiredString(params, "ticket_id")
	if err != nil {
		return nil, err
	}

	integrationId, err := extractOptionalString(params, "integration_id")
	if err != nil {
		return nil, err
	}
	integrationId, err = resolveTicketIntegrationID(taskCtx, integrationId, ticketPlatforms)
	if err != nil {
		return nil, err
	}

	projectKey, err := extractOptionalString(params, "project_key")
	if err != nil {
		return nil, err
	}

	accountId, err := extractAccountId(params, taskCtx)
	if err != nil {
		return nil, err
	}

	request := ticket.GetTicketRequest{
		TicketId:      ticketId,
		IntegrationId: integrationId,
		ProjectKey:    projectKey,
		AccountId:     accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.GetTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"id":          resp.ID,
		"title":       resp.Title,
		"description": resp.Description,
		"status":      resp.Status,
		"severity":    resp.Severity,
		"assignee":    resp.Assignee,
		"url":         resp.URL,
		"platform":    resp.Platform,
		"ticket_id":   resp.TicketID,
		"created_at":  resp.CreatedAt,
		"raw":         resp.Raw,
	}, nil
}

func (t *TicketsGetTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "Account override (defaults to workflow context)",
				Required:    false,
				Order:       1,
			},
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID to retrieve",
				Required:    true,
				Order:       2,
			},
			"integration_id": {
				Type:        types.PropertyTypeTicket,
				Description: "Ticket integration (required if ticket was not created via Nudgebee)",
				Required:    false,
				Order:       3,
			},
			"project_key": {
				Type:        types.PropertyTypeString,
				Description: "Project key (required for GitHub/GitLab in owner/repo format)",
				Required:    false,
				Order:       4,
				DependsOn:   []string{"integration_id"},
				OptionsSource: &types.OptionsSource{
					Type:              "ticket_projects",
					DependencyMapping: map[string]string{"integration_id": "integration_id"},
				},
			},
		},
	}
}

func (t *TicketsGetTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"id": {
				Type:        types.PropertyTypeString,
				Description: "Internal ticket ID",
				Required:    true,
			},
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "External ticket ID",
				Required:    true,
			},
			"title": {
				Type:        types.PropertyTypeString,
				Description: "Ticket title",
				Required:    true,
			},
			"description": {
				Type:        types.PropertyTypeString,
				Description: "Ticket description",
				Required:    true,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "Ticket status",
				Required:    true,
			},
			"severity": {
				Type:        types.PropertyTypeString,
				Description: "Ticket severity/priority",
				Required:    true,
			},
			"assignee": {
				Type:        types.PropertyTypeString,
				Description: "Ticket assignee",
				Required:    false,
			},
			"url": {
				Type:        types.PropertyTypeString,
				Description: "Ticket URL on the platform",
				Required:    true,
			},
			"platform": {
				Type:        types.PropertyTypeString,
				Description: "Platform name",
				Required:    true,
			},
			"created_at": {
				Type:        types.PropertyTypeString,
				Description: "Creation timestamp",
				Required:    false,
			},
			"raw": {
				Type:        types.PropertyTypeObject,
				Description: "Raw provider record with every field returned by the source platform (e.g. ServiceNow cmdb_ci, business_service, all u_* custom fields). nil when the source connector does not populate it. Reference fields are kept in {value, display_value} shape — pick .display_value for human-readable text, .value for the underlying sys_id.",
				Required:    false,
			},
		},
	}
}
