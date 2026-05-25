package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsEscalateTask defines a task for escalating an incident ticket.
type TicketsEscalateTask struct{}

func (t *TicketsEscalateTask) GetName() string {
	return "tickets.escalate"
}

// GetDescription returns a brief description of the task.
func (t *TicketsEscalateTask) GetDescription() string {
	return "Escalate an incident to the next responder or policy (PagerDuty, ZenDuty)."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsEscalateTask) GetDisplayName() string {
	return "Escalate Incident"
}

func (t *TicketsEscalateTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Escalate Ticket Task", "params", params)

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

	escalationPolicy, err := extractOptionalString(params, "escalation_policy")
	if err != nil {
		return nil, err
	}

	accountId, err := extractAccountId(params, taskCtx)
	if err != nil {
		return nil, err
	}

	request := ticket.EscalateTicketRequest{
		TicketId:         ticketId,
		IntegrationId:    integrationId,
		EscalationPolicy: escalationPolicy,
		AccountId:        accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.EscalateTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"status":    resp.Status,
		"message":   resp.Message,
	}, nil
}

func (t *TicketsEscalateTask) InputSchema() *types.Schema {
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
				Description: "Incident/ticket ID to escalate",
				Required:    true,
				Order:       3,
			},
			"escalation_policy": {
				Type:        types.PropertyTypeString,
				Description: "Escalation policy ID (PagerDuty-specific, optional for ZenDuty)",
				Required:    false,
				Order:       4,
			},
		},
	}
}

func (t *TicketsEscalateTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "New status (escalated)",
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
