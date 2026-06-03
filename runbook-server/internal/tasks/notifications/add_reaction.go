package notifications

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// AddReactionTask defines a task for adding a reaction to a message via IM.
type AddReactionTask struct{}

func (t *AddReactionTask) GetName() string {
	return "notifications.add_reaction"
}

// GetDescription returns a brief description of the task.
func (t *AddReactionTask) GetDescription() string {
	return "Adds a reaction (emoji) to a message on Slack, MS Teams, or Google Chat."
}

// GetDisplayName returns a human-readable name for the task.
func (t *AddReactionTask) GetDisplayName() string {
	return "Add Reaction"
}

func (t *AddReactionTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Add Reaction Task", "params", params)

	provider, ok := params["provider"].(string)
	if !ok || provider == "" {
		return nil, errors.New("provider is required")
	}

	channelID, ok := params["channel_id"].(string)
	if !ok || channelID == "" {
		return nil, errors.New("channel_id is required")
	}

	messageID, ok := params["message_id"].(string)
	if !ok || messageID == "" {
		return nil, errors.New("message_id is required")
	}

	emoji, ok := params["emoji"].(string)
	if !ok || emoji == "" {
		return nil, errors.New("emoji is required")
	}

	// Map provider names for consistency
	platform := provider
	if platform == "teams" {
		platform = "ms_teams"
	}

	requestContext := taskCtx.GetNewRequestContext()
	request := notification.AddReactionRequest{
		Platform:  platform,
		ChannelID: channelID,
		MessageID: messageID,
		Emoji:     emoji,
	}

	if teamID, ok := params["team_id"].(string); ok {
		request.TeamID = teamID
	}

	resp, err := notification.AddReaction(requestContext, request)
	if err != nil {
		return map[string]any{
			"success":  false,
			"provider": platform,
			"error":    err.Error(),
		}, err
	}

	return map[string]any{
		"success":    resp.Success,
		"provider":   resp.Provider,
		"channel_id": resp.ChannelID,
		"message_id": resp.MessageID,
		"reaction":   resp.Reaction,
	}, nil
}

func (t *AddReactionTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"provider": {
				Type:        "string",
				Description: "Notification Provider",
				Required:    true,
				Options:     []string{"slack", "ms_teams", "google_chat"},
				Order:       1,
			},
			"team_id": {
				Type:        "string",
				Description: "Team ID (required for MS Teams)",
				Required:    false,
				Order:       2,
			},
			"channel_id": {
				Type:        "string",
				Description: "Channel or Space ID where the message exists",
				Required:    true,
				Order:       3,
			},
			"message_id": {
				Type:        "string",
				Description: "Message timestamp (Slack) or message ID (Teams/Google Chat)",
				Required:    true,
				Order:       4,
			},
			"emoji": {
				Type:        "string",
				Description: "Emoji to add (name without colons for Slack e.g. 'thumbsup', unicode for Teams/Google Chat e.g. '👍')",
				Required:    true,
				Order:       5,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *AddReactionTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"success": {
				Type:        "boolean",
				Description: "Whether the reaction was added successfully.",
				Required:    true,
			},
			"provider": {
				Type:        "string",
				Description: "The platform used.",
				Required:    true,
			},
			"channel_id": {
				Type:        "string",
				Description: "The channel ID.",
				Required:    false,
			},
			"message_id": {
				Type:        "string",
				Description: "The message ID.",
				Required:    false,
			},
			"reaction": {
				Type:        "string",
				Description: "The emoji that was added.",
				Required:    false,
			},
		},
	}
}
