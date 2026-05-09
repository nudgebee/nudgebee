package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsResolveTask defines a task for resolving an incident ticket.
type TicketsResolveTask struct{}

func (t *TicketsResolveTask) GetName() string {
	return "tickets.resolve"
}

// GetDescription returns a brief description of the task.
func (t *TicketsResolveTask) GetDescription() string {
	return "Mark an incident as resolved (PagerDuty, ZenDuty)."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsResolveTask) GetDisplayName() string {
	return "Resolve Incident"
}

func (t *TicketsResolveTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Resolve Ticket Task", "params", params)

	ticketId, err := extractRequiredString(params, "ticket_id")
	if err != nil {
		return nil, err
	}

	integrationId, err := extractRequiredString(params, "integration_id")
	if err != nil {
		return nil, err
	}
	integrationId, err = resolveTicketIntegrationID(taskCtx, integrationId, incidentPlatforms)
	if err != nil {
		return nil, err
	}

	// Validate that the integration is an incident management platform
	if err := validateIncidentPlatform(taskCtx, integrationId); err != nil {
		return nil, err
	}

	resolution, err := extractOptionalString(params, "resolution")
	if err != nil {
		return nil, err
	}

	accountId, err := extractAccountId(params, taskCtx)
	if err != nil {
		return nil, err
	}

	request := ticket.ResolveTicketRequest{
		TicketId:      ticketId,
		IntegrationId: integrationId,
		Resolution:    resolution,
		AccountId:     accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.ResolveTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"status":    resp.Status,
		"message":   resp.Message,
	}, nil
}

func (t *TicketsResolveTask) InputSchema() *types.Schema {
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
				Description: "Incident management integration (PagerDuty or ZenDuty only)",
				Required:    true,
				Order:       2,
			},
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Incident/ticket ID to resolve",
				Required:    true,
				Order:       3,
			},
			"resolution": {
				Type:        types.PropertyTypeString,
				Description: "Resolution message or notes",
				Required:    false,
				Order:       4,
			},
		},
	}
}

func (t *TicketsResolveTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "New status (resolved)",
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
