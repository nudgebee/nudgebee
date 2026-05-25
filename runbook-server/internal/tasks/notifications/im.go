package notifications

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// ImSendTask defines a task for sending a notification via IM.
type ImSendTask struct{}

func (t *ImSendTask) GetName() string {
	return "notifications.im"
}

// GetDescription returns a brief description of the task.
func (t *ImSendTask) GetDescription() string {
	return "Send a message to Slack, MS Teams, or Google Chat."
}

// GetDisplayName returns a human-readable name for the task.
func (t *ImSendTask) GetDisplayName() string {
	return "Send IM"
}

func (t *ImSendTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Notification Task", "params", params)

	if params["provider"] == nil || params["provider"] == "" {
		return nil, errors.New("provider is required")
	}

	if params["channel"] == nil || params["channel"] == "" {
		return nil, errors.New("channel is required")
	}

	channel := params["channel"].(string)

	if params["message"] == nil || params["message"] == "" {
		return nil, errors.New("message is required")
	}
	messageBody := params["message"].(string)

	requestContext := taskCtx.GetNewRequestContext()
	request := notification.SendImNotificationRequest{
		Body:      messageBody,
		Channel:   channel,
		AccountID: taskCtx.GetAccountID(),
		Platform:  params["provider"].(string),
	}

	// Attach workflow metadata for tracing in notification footers
	if wfName := taskCtx.GetWorkflowName(); wfName != "" {
		metadata := map[string]any{
			"workflow_name": wfName,
		}
		if wfID := taskCtx.GetWorkflowID(); wfID != "" {
			metadata["workflow_id"] = wfID
		}
		if accountID := taskCtx.GetAccountID(); accountID != "" {
			metadata["account_id"] = accountID
		}
		if triggeredBy := taskCtx.GetUserDisplayName(); triggeredBy != "" {
			metadata["triggered_by"] = triggeredBy
		}
		request.Parameters = map[string]any{
			"workflow_metadata": metadata,
		}
	}
	if thread, ok := params["message_thread_id"].(string); ok {
		request.ThreadId = thread
	}
	if team, ok := params["team_id"].(string); ok {
		request.TeamId = team
	}

	resp, err := notification.SendNotification(requestContext, request)

	return map[string]any{
		"channel":    resp.ChannelId,
		"message_id": resp.MessageTs,
		"team":       resp.TeamId,
		"provider":   resp.Platform,
	}, err
}

func (t *ImSendTask) InputSchema() *types.Schema {
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
				Description: "MSTeams TeamId",
				Required:    false,
				Order:       2,
			},
			"channel": {
				Type:        "string",
				Description: "Provider Channel",
				Required:    true,
				Order:       3,
			},
			"message": {
				Type:        "string",
				Description: "Notification Message Body",
				Required:    true,
				Order:       4,
			},
			"message_thread_id": {
				Type:        "string",
				Description: "Message ID for Response on Threads",
				Required:    false,
				Order:       5,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *ImSendTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"channel": {
				Type:        "string",
				Description: "IM channel.",
				Required:    true,
			},
			"message_id": {
				Type:        "string",
				Description: "IM MessageId.",
				Required:    true,
			},
			"team": {
				Type:        "string",
				Description: "IM Team.",
				Required:    true,
			},
			"platform": {
				Type:        "string",
				Description: "IM Platform.",
				Required:    true,
			},
		},
	}
}
