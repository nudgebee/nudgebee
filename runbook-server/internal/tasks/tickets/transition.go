package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsTransitionTask defines a task for transitioning a ticket's status.
type TicketsTransitionTask struct{}

func (t *TicketsTransitionTask) GetName() string {
	return "tickets.transition"
}

// GetDescription returns a brief description of the task.
func (t *TicketsTransitionTask) GetDescription() string {
	return "Move a ticket to a new status following workflow rules (e.g., Jira transitions)."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsTransitionTask) GetDisplayName() string {
	return "Transition Ticket"
}

func (t *TicketsTransitionTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Transition Ticket Task", "params", params)

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

	status, err := extractRequiredString(params, "status")
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

	request := ticket.TransitionTicketRequest{
		TicketId:      ticketId,
		IntegrationId: integrationId,
		Status:        status,
		ProjectKey:    projectKey,
		AccountId:     accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.TransitionTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"status":    resp.Status,
		"message":   resp.Message,
	}, nil
}

func (t *TicketsTransitionTask) InputSchema() *types.Schema {
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
				Description: "Ticket ID to transition",
				Required:    true,
				Order:       3,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "Target status (e.g. 'In Progress', 'Done' for Jira; 'open'/'closed' for GitHub/GitLab)",
				Required:    true,
				Order:       4,
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

func (t *TicketsTransitionTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "New status after transition",
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
