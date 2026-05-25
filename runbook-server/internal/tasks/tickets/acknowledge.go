package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsAcknowledgeTask defines a task for acknowledging an incident ticket.
type TicketsAcknowledgeTask struct{}

func (t *TicketsAcknowledgeTask) GetName() string {
	return "tickets.acknowledge"
}

// GetDescription returns a brief description of the task.
func (t *TicketsAcknowledgeTask) GetDescription() string {
	return "Acknowledge an incident to stop further escalation (PagerDuty, ZenDuty)."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsAcknowledgeTask) GetDisplayName() string {
	return "Acknowledge Incident"
}

func (t *TicketsAcknowledgeTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Acknowledge Ticket Task", "params", params)

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

	accountId, err := extractAccountId(params, taskCtx)
	if err != nil {
		return nil, err
	}

	request := ticket.AcknowledgeTicketRequest{
		TicketId:      ticketId,
		IntegrationId: integrationId,
		AccountId:     accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.AcknowledgeTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"status":    resp.Status,
		"message":   resp.Message,
	}, nil
}

func (t *TicketsAcknowledgeTask) InputSchema() *types.Schema {
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
				Description: "Incident/ticket ID to acknowledge",
				Required:    true,
				Order:       3,
			},
		},
	}
}

func (t *TicketsAcknowledgeTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "New status (acknowledged)",
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
