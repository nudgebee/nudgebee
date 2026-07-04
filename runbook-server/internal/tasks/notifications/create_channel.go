package notifications

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// CreateChannelTask defines a task for find-or-creating a channel/space.
type CreateChannelTask struct{}

func (t *CreateChannelTask) GetName() string {
	return "notifications.create_channel"
}

// GetDescription returns a brief description of the task.
func (t *CreateChannelTask) GetDescription() string {
	return "Find or create a Slack channel, MS Teams channel, or Google Chat space."
}

// GetDisplayName returns a human-readable name for the task.
func (t *CreateChannelTask) GetDisplayName() string {
	return "Create Channel"
}

func (t *CreateChannelTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Create Channel Task", "params", params)

	provider, ok := params["provider"].(string)
	if !ok || provider == "" {
		return nil, errors.New("provider is required and must be a string")
	}

	name, ok := params["name"].(string)
	if !ok || name == "" {
		return nil, errors.New("name is required and must be a string")
	}

	request := notification.CreateChannelRequest{
		Platform:  provider,
		Name:      name,
		AccountID: taskCtx.GetAccountID(),
	}

	if isPrivate, ok := params["is_private"].(bool); ok {
		request.IsPrivate = isPrivate
	}
	if description, ok := params["description"].(string); ok {
		request.Description = description
	}
	if team, ok := params["team_id"].(string); ok {
		request.TeamID = team
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := notification.CreateChannel(requestContext, request)

	return map[string]any{
		"channel":  resp.ChannelID,
		"name":     resp.Name,
		"team":     resp.TeamID,
		"url":      resp.URL,
		"provider": resp.Platform,
		"created":  resp.Created,
	}, err
}

func (t *CreateChannelTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"provider": {
				Type:        "string",
				Description: "Notification Provider",
				Required:    true,
				Options:     []string{"slack", "ms_teams", "google_chat"},
				Order:       1,
			},
			"name": {
				Type:        "string",
				Description: "Channel/space name to find or create",
				Required:    true,
				Order:       2,
			},
			"team_id": {
				Type:        "string",
				Description: "MS Teams TeamId (required for ms_teams)",
				Required:    false,
				Order:       3,
			},
			"is_private": {
				Type:        "boolean",
				Description: "Create a private channel",
				Required:    false,
				Order:       4,
			},
			"description": {
				Type:        "string",
				Description: "Channel description (MS Teams)",
				Required:    false,
				Order:       5,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *CreateChannelTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"channel": {
				Type:        "string",
				Description: "Channel/space ID.",
				Required:    true,
			},
			"name": {
				Type:        "string",
				Description: "Channel/space name.",
				Required:    true,
			},
			"team": {
				Type:        "string",
				Description: "MS Teams TeamId.",
				Required:    false,
			},
			"url": {
				Type:        "string",
				Description: "Channel/space URL.",
				Required:    false,
			},
			"provider": {
				Type:        "string",
				Description: "Notification Provider.",
				Required:    true,
			},
			"created": {
				Type:        "boolean",
				Description: "True if a new channel was created, false if an existing one was reused.",
				Required:    true,
			},
		},
	}
}
