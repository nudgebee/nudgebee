package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsAddCommentTask defines a task for adding a comment to a ticket.
type TicketsAddCommentTask struct{}

func (t *TicketsAddCommentTask) GetName() string {
	return "tickets.add_comment"
}

// GetDescription returns a brief description of the task.
func (t *TicketsAddCommentTask) GetDescription() string {
	return "Post a comment on an existing ticket."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsAddCommentTask) GetDisplayName() string {
	return "Add Comment"
}

func (t *TicketsAddCommentTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Add Comment Task", "params", params)

	ticketId, err := extractRequiredString(params, "ticket_id")
	if err != nil {
		return nil, err
	}

	comment, err := extractRequiredString(params, "comment")
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

	projectKey, err := extractOptionalString(params, "project_key")
	if err != nil {
		return nil, err
	}

	accountId, err := extractAccountId(params, taskCtx)
	if err != nil {
		return nil, err
	}

	request := ticket.AddTicketCommentRequest{
		TicketId:      ticketId,
		Comment:       comment,
		IntegrationId: integrationId,
		ProjectKey:    projectKey,
		AccountId:     accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.AddTicketComment(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"comment":   comment,
	}, nil
}

func (t *TicketsAddCommentTask) InputSchema() *types.Schema {
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
			// Synthetic field. The frontend (ActionDetailsSidebar
			// applyIntegrationFieldChange) derives this from the selected
			// ticket integration's `tool` (jira/github/gitlab/servicenow/
			// pagerduty/zenduty) and uses it to drive VisibleWhen/RequiredWhen
			// on provider-specific fields below. Hidden from the rendered form.
			"ticket_tool": {
				Type:        types.PropertyTypeString,
				Description: "Internal: derived ticket tool type for the selected integration.",
				Required:    false,
				Hidden:      true,
				Order:       3,
			},
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID to comment on",
				Required:    true,
				Order:       4,
			},
			"comment": {
				Type:        types.PropertyTypeString,
				SubType:     "textarea",
				Description: "Comment text to add",
				Required:    true,
				Order:       5,
			},
			"project_key": {
				Type:        types.PropertyTypeString,
				Description: "Project key (owner/repo for GitHub/GitLab)",
				Required:    false,
				Order:       6,
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"github", "gitlab"},
				},
				RequiredWhen: &types.RequiredWhen{
					Field: "ticket_tool",
					Value: []string{"github", "gitlab"},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "ticket_projects",
					DependencyMapping: map[string]string{"integration_id": "integration_id"},
				},
			},
		},
	}
}

func (t *TicketsAddCommentTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"comment": {
				Type:        types.PropertyTypeString,
				Description: "Comment that was added",
				Required:    true,
			},
		},
	}
}
