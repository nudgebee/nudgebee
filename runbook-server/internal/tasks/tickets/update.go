package tickets

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsUpdateTask defines a task for updating ticket fields.
type TicketsUpdateTask struct{}

// updateUniversalParams are the params with a shared meaning across ticket
// platforms; everything else is platform-specific and goes through AdditionalFields.
var updateUniversalParams = map[string]bool{
	"account_id": true, "integration_id": true, "ticket_tool": true, "ticket_id": true,
	"project_key": true, "status": true, "severity": true, "assignee": true,
	"description": true, "labels": true,
}

func buildUpdateAdditionalFields(params map[string]any) map[string]any {
	additionalFields := map[string]any{}
	for key, value := range params {
		if !updateUniversalParams[key] {
			additionalFields[key] = value
		}
	}
	return additionalFields
}

func (t *TicketsUpdateTask) GetName() string {
	return "tickets.update"
}

// GetDescription returns a brief description of the task.
func (t *TicketsUpdateTask) GetDescription() string {
	return "Updates ticket fields such as status, severity (Jira) or impact/urgency/resolution (ServiceNow), assignee, description, or labels."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsUpdateTask) GetDisplayName() string {
	return "Update Ticket"
}

func (t *TicketsUpdateTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Update Ticket Task", "params", params)

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

	status, err := extractOptionalString(params, "status")
	if err != nil {
		return nil, err
	}

	severity, err := extractOptionalString(params, "severity")
	if err != nil {
		return nil, err
	}

	assignee, err := extractOptionalString(params, "assignee")
	if err != nil {
		return nil, err
	}

	description, err := extractOptionalString(params, "description")
	if err != nil {
		return nil, err
	}

	labels, err := extractOptionalStringSlice(params, "labels")
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

	additionalFields := buildUpdateAdditionalFields(params)

	// Validate that at least one field is being updated
	if status == "" && severity == "" && assignee == "" && description == "" && len(labels) == 0 && len(additionalFields) == 0 {
		return nil, errors.New("at least one field to update must be provided")
	}

	if taskCtx.IsDryRun() {
		taskCtx.GetLogger().Info("Dry Run: skipping ticket update")
		return map[string]any{
			"ticket_id": ticketId,
			"status":    "dry_run",
			"message":   "Dry run: ticket update skipped",
		}, nil
	}

	request := ticket.UpdateTicketRequest{
		TicketId:         ticketId,
		IntegrationId:    integrationId,
		AccountId:        accountId,
		ProjectKey:       projectKey,
		Status:           status,
		Severity:         severity,
		Assignee:         assignee,
		Description:      description,
		Labels:           labels,
		AdditionalFields: additionalFields,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.UpdateTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"ticket_id": resp.TicketID,
		"status":    resp.Status,
		"message":   resp.Message,
	}, nil
}

func (t *TicketsUpdateTask) InputSchema() *types.Schema {
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
			// pagerduty/zenduty) and uses it to drive VisibleWhen on the
			// provider-specific fields below. Hidden from the rendered form.
			"ticket_tool": {
				Type:        types.PropertyTypeString,
				Description: "Internal: derived ticket tool type for the selected integration.",
				Required:    false,
				Hidden:      true,
				Order:       3,
			},
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID to update (e.g. PROJ-123 for Jira, issue number for GitHub)",
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
			"status": {
				Type:        types.PropertyTypeString,
				Description: "New status (triggers workflow transition for Jira/ServiceNow, open/closed for GitHub/GitLab)",
				Required:    false,
				Order:       6,
			},
			"severity": {
				Type:        types.PropertyTypeString,
				Description: "New priority (e.g. High, Medium, Low). Supported on Jira; ServiceNow uses Impact and Urgency instead.",
				Required:    false,
				Order:       7,
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"jira"},
				},
			},
			"impact": {
				Type:        types.PropertyTypeString,
				Description: "New impact. ServiceNow derives the incident's priority from Impact and Urgency; a field left empty keeps its current value.",
				Required:    false,
				Order:       7,
				Options:     []string{"High", "Medium", "Low"},
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"servicenow"},
				},
			},
			"urgency": {
				Type:        types.PropertyTypeString,
				Description: "New urgency. ServiceNow derives the incident's priority from Impact and Urgency; a field left empty keeps its current value.",
				Required:    false,
				Order:       7,
				Options:     []string{"High", "Medium", "Low"},
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"servicenow"},
				},
			},
			"resolution_code": {
				Type: types.PropertyTypeString,
				Description: "Resolution code (ServiceNow's Resolution code / close_code). Many instances require this — and Resolution notes — " +
					"before status can become Resolved/Closed; the exact accepted values are set per instance (ServiceNow: Incident > Resolution code choice list).",
				Required:  false,
				Order:     11,
				DependsOn: []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"servicenow"},
				},
			},
			"resolution_notes": {
				Type:        types.PropertyTypeString,
				Description: "Resolution notes (ServiceNow's close_notes). Required alongside Resolution code on instances that enforce it.",
				Required:    false,
				Order:       12,
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"servicenow"},
				},
			},
			"work_notes": {
				Type:        types.PropertyTypeString,
				Description: "Internal work note (ServiceNow's work_notes). Not customer-visible; independent of any status change.",
				Required:    false,
				Order:       13,
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"servicenow"},
				},
			},
			"assignee": {
				Type:        types.PropertyTypeString,
				Description: "New assignee (Jira account ID or email, GitHub/GitLab username). Not supported on PagerDuty/ZenDuty.",
				Required:    false,
				Order:       8,
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"jira", "github", "gitlab", "servicenow"},
				},
				OptionsSource: &types.OptionsSource{
					Type:              "ticket_assignees",
					DependencyMapping: map[string]string{"integration_id": "integration_id", "project_key": "project_key"},
				},
			},
			"description": {
				Type:        types.PropertyTypeString,
				Description: "New ticket description (replaces the existing description). Not supported on PagerDuty/ZenDuty.",
				Required:    false,
				Order:       9,
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"jira", "github", "gitlab", "servicenow"},
				},
			},
			"labels": {
				Type:        types.PropertyTypeArray,
				Description: "Labels/tags to set on the ticket (replaces existing labels). Supported on Jira, GitHub, and GitLab.",
				Required:    false,
				Order:       10,
				DependsOn:   []string{"integration_id", "ticket_tool"},
				VisibleWhen: &types.VisibleWhen{
					Field: "ticket_tool",
					Value: []string{"jira", "github", "gitlab"},
				},
			},
		},
	}
}

func (t *TicketsUpdateTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "Ticket ID",
				Required:    true,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "Update status",
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
