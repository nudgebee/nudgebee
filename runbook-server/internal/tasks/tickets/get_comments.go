package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsGetCommentsTask defines a task for getting ticket comments.
type TicketsGetCommentsTask struct{}

func (t *TicketsGetCommentsTask) GetName() string {
	return "tickets.get_comments"
}

// GetDescription returns a brief description of the task.
func (t *TicketsGetCommentsTask) GetDescription() string {
	return "Fetch all comments from a ticket."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsGetCommentsTask) GetDisplayName() string {
	return "Get Ticket Comments"
}

func (t *TicketsGetCommentsTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Get Ticket Comments Task", "params", params)

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

	request := ticket.GetTicketCommentsRequest{
		TicketId:      ticketId,
		IntegrationId: integrationId,
		ProjectKey:    projectKey,
		AccountId:     accountId,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.GetTicketComments(requestContext, request)
	if err != nil {
		return nil, err
	}

	comments := make([]map[string]any, len(resp.Comments))
	for i, c := range resp.Comments {
		comments[i] = map[string]any{
			"author":     c.Author,
			"comment":    c.Comment,
			"created_at": c.Created,
			"updated_at": c.Updated,
		}
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"comments":  comments,
		"count":     len(comments),
	}, nil
}

func (t *TicketsGetCommentsTask) InputSchema() *types.Schema {
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
				Description: "Ticket integration (required if ticket was not created via Nudgebee)",
				Required:    false,
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
				Description: "Ticket ID to get comments for",
				Required:    true,
				Order:       4,
			},
			"project_key": {
				Type:        types.PropertyTypeString,
				Description: "Project key (owner/repo for GitHub/GitLab)",
				Required:    false,
				Order:       5,
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

func (t *TicketsGetCommentsTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"comments": {
				Type:        types.PropertyTypeArray,
				Description: "List of comments (each with author, comment, created_at, updated_at)",
				Required:    true,
			},
			"count": {
				Type:        types.PropertyTypeNumber,
				Description: "Number of comments",
				Required:    true,
			},
		},
	}
}
