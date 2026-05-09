package notifications

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// ReadThreadTask defines a task for reading messages and reactions from a thread.
type ReadThreadTask struct{}

func (t *ReadThreadTask) GetName() string {
	return "notifications.read_thread"
}

// GetDescription returns a brief description of the task.
func (t *ReadThreadTask) GetDescription() string {
	return "Fetch replies and reactions from a chat thread."
}

// GetDisplayName returns a human-readable name for the task.
func (t *ReadThreadTask) GetDisplayName() string {
	return "Read Thread Messages"
}

func (t *ReadThreadTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Read Thread Task", "params", params)

	if params["provider"] == nil || params["provider"] == "" {
		return nil, errors.New("provider is required")
	}

	provider := params["provider"].(string)
	if provider != "slack" {
		return nil, errors.New("only slack provider is supported for reading threads")
	}

	if params["channel_id"] == nil || params["channel_id"] == "" {
		return nil, errors.New("channel_id is required")
	}

	if params["thread_ts"] == nil || params["thread_ts"] == "" {
		return nil, errors.New("thread_ts is required")
	}

	channelID := params["channel_id"].(string)
	threadTs := params["thread_ts"].(string)

	request := notification.GetThreadMessagesRequest{
		ChannelID: channelID,
		ThreadTs:  threadTs,
	}

	if team, ok := params["team_id"].(string); ok {
		request.TeamID = team
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := notification.GetThreadMessages(requestContext, request)
	if err != nil {
		return map[string]any{
			"success":       false,
			"error":         err.Error(),
			"messages":      []any{},
			"has_responses": false,
			"has_reactions": false,
			"reply_count":   0,
		}, nil
	}

	if !resp.Success {
		return map[string]any{
			"success":       false,
			"error":         resp.Error,
			"messages":      []any{},
			"has_responses": false,
			"has_reactions": false,
			"reply_count":   0,
		}, nil
	}

	// Convert messages to a serializable format
	messages := make([]map[string]any, 0, len(resp.Messages))
	hasReactions := false
	replyCount := 0

	for _, msg := range resp.Messages {
		msgMap := map[string]any{
			"ts":        msg.Ts,
			"text":      msg.Text,
			"is_parent": msg.IsParent,
		}

		if msg.UserID != "" {
			msgMap["user_id"] = msg.UserID
		}

		if msg.User != nil {
			msgMap["user"] = map[string]any{
				"id":           msg.User.ID,
				"name":         msg.User.Name,
				"real_name":    msg.User.RealName,
				"display_name": msg.User.DisplayName,
				"is_bot":       msg.User.IsBot,
			}
		}

		if len(msg.Reactions) > 0 {
			hasReactions = true
			reactions := make([]map[string]any, 0, len(msg.Reactions))
			for _, r := range msg.Reactions {
				reactions = append(reactions, map[string]any{
					"name":  r.Name,
					"count": r.Count,
					"users": r.Users,
				})
			}
			msgMap["reactions"] = reactions
		} else {
			msgMap["reactions"] = []map[string]any{}
		}

		if msg.IsParent && msg.ReplyCount > 0 {
			msgMap["reply_count"] = msg.ReplyCount
			replyCount = msg.ReplyCount
		}

		messages = append(messages, msgMap)
	}

	// has_responses is true if there are reactions OR thread replies
	hasResponses := hasReactions || replyCount > 0

	return map[string]any{
		"success":       true,
		"error":         "",
		"channel_id":    resp.ChannelID,
		"thread_ts":     resp.ThreadTs,
		"messages":      messages,
		"has_more":      resp.HasMore,
		"has_responses": hasResponses,
		"has_reactions": hasReactions,
		"reply_count":   replyCount,
	}, nil
}

func (t *ReadThreadTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"provider": {
				Type:        "string",
				Description: "Notification Provider (only 'slack' supported)",
				Required:    true,
				Options:     []string{"slack"},
				Order:       1,
			},
			"channel_id": {
				Type:        "string",
				Description: "Channel ID where the thread exists",
				Required:    true,
				Order:       2,
			},
			"thread_ts": {
				Type:        "string",
				Description: "Thread timestamp (parent message ts)",
				Required:    true,
				Order:       3,
			},
			"team_id": {
				Type:        "string",
				Description: "Team/workspace ID (optional if tenant has single workspace)",
				Required:    false,
				Order:       4,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *ReadThreadTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"success": {
				Type:        "boolean",
				Description: "Whether the request was successful",
				Required:    true,
			},
			"error": {
				Type:        "string",
				Description: "Error message if request failed",
				Required:    false,
			},
			"channel_id": {
				Type:        "string",
				Description: "Channel ID",
				Required:    true,
			},
			"thread_ts": {
				Type:        "string",
				Description: "Thread timestamp",
				Required:    true,
			},
			"messages": {
				Type:        "array",
				Description: "Messages in the thread with reactions",
				Required:    true,
			},
			"has_more": {
				Type:        "boolean",
				Description: "Whether there are more messages to fetch",
				Required:    true,
			},
			"has_responses": {
				Type:        "boolean",
				Description: "True if there are reactions or thread replies",
				Required:    true,
			},
			"has_reactions": {
				Type:        "boolean",
				Description: "True if there are reactions on the parent message",
				Required:    true,
			},
			"reply_count": {
				Type:        "number",
				Description: "Number of replies in the thread",
				Required:    true,
			},
		},
	}
}
