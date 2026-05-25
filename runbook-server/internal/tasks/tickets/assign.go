package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsAssignTask defines a task for assigning a ticket to a user.
type TicketsAssignTask struct{}

func (t *TicketsAssignTask) GetName() string {
	return "tickets.assign"
}

// GetDescription returns a brief description of the task.
func (t *TicketsAssignTask) GetDescription() string {
	return "Assign a ticket to a specific person or team."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsAssignTask) GetDisplayName() string {
	return "Assign Ticket"
}

func (t *TicketsAssignTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Assign Ticket Task", "params", params)

	ticketId, err := extractRequiredString(params, "ticket_id")
	if err != nil {
		return nil, err
	}

	integrationId, err := extractRequiredString(params, "integration_id")
	if err != nil {
		return nil, err
	}
	integrationId, err = resolveTicketIntegrationID(taskCtx, integrationId, ticketPlatforms)
	if err != nil {
		return nil, err
	}

	assignee, err := extractRequiredString(params, "assignee")
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

	request := ticket.AssignTicketRequest{
		TicketId:      ticketId,
		IntegrationId: integrationId,
		Assignee:      assignee,
		ProjectKey:    projectKey,
		AccountId:     accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.AssignTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"assignee":  resp.Assignee,
		"message":   resp.Message,
	}, nil
}

func (t *TicketsAssignTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "Account override (defaults to workflow context)",
				Required:    false,
				Order:       1,
			},
			"integration_id": {
				Type:        types.PropertyTypeTicket,
				Description: "Ticket integration to use",
				Required:    true,
				Order:       2,
			},
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID to assign",
				Required:    true,
				Order:       3,
			},
			"assignee": {
				Type:        types.PropertyTypeString,
				Description: "Assignee (Jira: account ID or email; GitHub/GitLab: username; ServiceNow: sys_id or email)",
				Required:    true,
				Order:       4,
				DependsOn:   []string{"integration_id"},
				OptionsSource: &types.OptionsSource{
					Type:              "ticket_assignees",
					DependencyMapping: map[string]string{"integration_id": "integration_id"},
				},
			},
			"project_key": {
				Type:        types.PropertyTypeString,
				Description: "Project key (required for GitHub/GitLab in owner/repo format)",
				Required:    false,
				Order:       5,
				DependsOn:   []string{"integration_id"},
				OptionsSource: &types.OptionsSource{
					Type:              "ticket_projects",
					DependencyMapping: map[string]string{"integration_id": "integration_id"},
				},
			},
		},
	}
}

func (t *TicketsAssignTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"assignee": {
				Type:        types.PropertyTypeString,
				Description: "Assigned user",
				Required:    true,
			},
			"message": {
				Type:        types.PropertyTypeString,
				Description: "Result message",
				Required:    true,
			},
		},
	}
}
