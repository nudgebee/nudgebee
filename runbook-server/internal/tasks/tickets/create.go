package tickets

import (
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/ticket"
)

// TicketsCreateTask defines a task for creating a ticket.
type TicketsCreateTask struct{}

func (t *TicketsCreateTask) GetName() string {
	return "tickets.create"
}

// GetDescription returns a brief description of the task.
func (t *TicketsCreateTask) GetDescription() string {
	return "Create a new ticket or incident in your ticketing platform."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TicketsCreateTask) GetDisplayName() string {
	return "Create Ticket"
}

func (t *TicketsCreateTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Create Ticket Task", "params", params)

	integrationId, err := extractRequiredString(params, "integration_id")
	if err != nil {
		return nil, err
	}
	integrationId, err = resolveTicketIntegrationID(taskCtx, integrationId, ticketPlatforms)
	if err != nil {
		return nil, err
	}

	title, err := extractRequiredString(params, "title")
	if err != nil {
		return nil, err
	}

	description, err := extractRequiredString(params, "description")
	if err != nil {
		return nil, err
	}

	projectKey, err := extractRequiredString(params, "project_key")
	if err != nil {
		return nil, err
	}

	severity, err := extractOptionalString(params, "severity")
	if err != nil {
		return nil, err
	}

	accountId, err := extractAccountId(params, taskCtx)
	if err != nil {
		return nil, err
	}

	referenceId, err := extractOptionalString(params, "reference_id")
	if err != nil {
		return nil, err
	}
	if referenceId == "" {
		referenceId = taskCtx.GetWorkflowID()
	}

	ticketType, err := extractOptionalString(params, "ticket_type")
	if err != nil {
		return nil, err
	}
	if ticketType == "" {
		ticketType = "Task"
	}

	var additionalFields map[string]any
	if af, ok := params["additional_fields"]; ok {
		if afMap, ok2 := af.(map[string]any); ok2 {
			additionalFields = afMap
		}
	}

	assignee := extractAssignee(additionalFields)
	if assignee == "" {
		// Back-compat: legacy workflows stored assignee at the top level.
		// New workflows carry it inside additional_fields (Platform Fields).
		if legacy, lerr := extractOptionalString(params, "assignee"); lerr != nil {
			return nil, lerr
		} else if legacy != "" {
			taskCtx.GetLogger().Warn("tickets.create: reading legacy top-level assignee; re-save the workflow to migrate into additional_fields")
			assignee = legacy
		}
	}

	// Back-compat: priority/urgency are basic (severity) fields now. Older saved
	// workflows may still carry them inside additional_fields — fold into severity
	// (when severity wasn't set explicitly) so the chosen value still applies.
	if severity == "" {
		if legacy := additionalFieldString(additionalFields, "priority"); legacy != "" {
			severity = legacy
		} else if legacy := additionalFieldString(additionalFields, "urgency"); legacy != "" {
			severity = legacy
		}
	}

	// Once consumed, ensure these basic fields aren't forwarded as custom fields.
	if additionalFields != nil {
		delete(additionalFields, "assignee")
		delete(additionalFields, "priority")
		delete(additionalFields, "urgency")
	}

	request := ticket.CreateTicketRequest{
		IntegrationId:    integrationId,
		Title:            title,
		Description:      description,
		ProjectKey:       projectKey,
		Severity:         severity,
		AccountId:        accountId,
		ReferenceId:      referenceId,
		TicketType:       ticketType,
		Assignee:         assignee,
		AdditionalFields: additionalFields,
		Source:           "runbook",
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := ticket.CreateTicket(requestContext, request)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"id":           resp.Id,
		"platform":     resp.Platform,
		"reference_id": resp.ReferenceId,
		"severity":     resp.Severity,
		"status":       resp.Status,
		"ticket_id":    resp.TicketId,
		"url":          resp.URL,
	}, nil
}

func (t *TicketsCreateTask) InputSchema() *types.Schema {
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
			"project_key": {
				Type:        types.PropertyTypeString,
				Description: "Project key (e.g. 'PROJ' for Jira, 'owner/repo' for GitHub/GitLab)",
				Required:    true,
				Order:       3,
				DependsOn:   []string{"integration_id"},
				OptionsSource: &types.OptionsSource{
					Type:              "ticket_projects",
					DependencyMapping: map[string]string{"integration_id": "integration_id"},
				},
			},
			"title": {
				Type:        types.PropertyTypeString,
				Description: "Ticket title",
				Required:    true,
				Order:       4,
			},
			"description": {
				Type:        types.PropertyTypeString,
				Description: "Ticket description",
				Required:    true,
				Order:       5,
			},
			"ticket_type": {
				Type:        types.PropertyTypeString,
				Description: "Issue type (e.g. Task, Bug, Incident, Story). Platform-specific.",
				Required:    false,
				Default:     "Task",
				Order:       6,
				DependsOn:   []string{"integration_id", "project_key"},
				OptionsSource: &types.OptionsSource{
					Type: "ticket_issue_types",
					DependencyMapping: map[string]string{
						"integration_id": "integration_id",
						"project_key":    "project_key",
					},
				},
			},
			"severity": {
				Type:        types.PropertyTypeString,
				Description: "Priority/severity level. Platform-specific.",
				Required:    false,
				Order:       7,
				DependsOn:   []string{"integration_id", "project_key", "ticket_type"},
				OptionsSource: &types.OptionsSource{
					Type: "ticket_field_options",
					DependencyMapping: map[string]string{
						"integration_id": "integration_id",
						"project_key":    "project_key",
						"ticket_type":    "ticket_type",
						"field_key":      "priority",
					},
				},
			},
			"reference_id": {
				Type:        types.PropertyTypeString,
				Description: "Reference ID for tracking (defaults to Workflow ID)",
				Required:    false,
				Order:       9,
			},
			"additional_fields": {
				Type:        types.PropertyTypeObject,
				Description: "Additional platform-specific fields (e.g., Jira custom fields, Sprint, Components)",
				Required:    false,
				Order:       10,
				DependsOn:   []string{"integration_id", "project_key", "ticket_type"},
				DynamicFieldsSource: &types.DynamicFieldsSource{
					Type: "ticket_meta",
					DependencyMapping: map[string]string{
						"integration_id": "integration_id",
						"project_key":    "project_key",
						"ticket_type":    "ticket_type",
					},
				},
			},
		},
	}
}

func (t *TicketsCreateTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"id": {
				Type:        types.PropertyTypeString,
				Description: "Internal ticket ID",
				Required:    true,
			},
			"ticket_id": {
				Type:        types.PropertyTypeString,
				Description: "External ticket ID (e.g. PROJ-123)",
				Required:    true,
			},
			"url": {
				Type:        types.PropertyTypeString,
				Description: "Ticket URL on the platform",
				Required:    true,
			},
			"platform": {
				Type:        types.PropertyTypeString,
				Description: "Platform name (jira, github, etc.)",
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
			"reference_id": {
				Type:        types.PropertyTypeString,
				Description: "Reference ID",
				Required:    true,
			},
		},
	}
}
