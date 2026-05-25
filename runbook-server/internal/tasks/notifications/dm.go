package notifications

import (
	"errors"
	"fmt"
	"net/url"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// DmSendTask defines a task for sending a direct message to a user.
type DmSendTask struct{}

func (t *DmSendTask) GetName() string {
	return "notifications.dm"
}

// GetDescription returns a brief description of the task.
func (t *DmSendTask) GetDescription() string {
	return "Send a direct message to a specific user."
}

// GetDisplayName returns a human-readable name for the task.
func (t *DmSendTask) GetDisplayName() string {
	return "DM"
}

func (t *DmSendTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing DM Task", "params", params)

	if params["provider"] == nil || params["provider"] == "" {
		return nil, errors.New("provider is required")
	}

	if params["user_id"] == nil || params["user_id"] == "" {
		return nil, errors.New("user_id is required")
	}

	if params["message"] == nil || params["message"] == "" {
		return nil, errors.New("message is required")
	}

	userID := params["user_id"].(string)
	messageBody := params["message"].(string)

	// Append workflow tracing footer for DMs
	if wfName := taskCtx.GetWorkflowName(); wfName != "" {
		label := fmt.Sprintf("*%s*", wfName)
		if link := buildWorkflowDmLink(taskCtx, wfName); link != "" {
			label = link
		}
		footer := fmt.Sprintf("\n---\nAutomation: %s", label)
		if triggeredBy := taskCtx.GetUserDisplayName(); triggeredBy != "" {
			footer += fmt.Sprintf(" | Triggered by: %s", triggeredBy)
		}
		messageBody += footer
	}

	requestContext := taskCtx.GetNewRequestContext()
	request := notification.SendDirectMessageRequest{
		Body:      messageBody,
		UserID:    userID,
		AccountID: taskCtx.GetAccountID(),
		Platform:  params["provider"].(string),
	}

	resp, err := notification.SendDirectMessage(requestContext, request)

	return map[string]any{
		"user_id":    resp.UserID,
		"channel_id": resp.ChannelID,
		"message_id": resp.MessageTs,
		"provider":   resp.Platform,
	}, err
}

func (t *DmSendTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"provider": {
				Type:        "string",
				Description: "Notification Provider",
				Required:    true,
				Options:     []string{"slack"},
				Order:       1,
			},
			"user_id": {
				Type:        "string",
				Description: "User ID to send message to",
				Required:    true,
				Order:       2,
			},
			"message": {
				Type:        "string",
				Description: "Message Body",
				Required:    true,
				Order:       3,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *DmSendTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"user_id": {
				Type:        "string",
				Description: "Target user ID.",
				Required:    true,
			},
			"channel_id": {
				Type:        "string",
				Description: "DM channel ID.",
				Required:    true,
			},
			"message_id": {
				Type:        "string",
				Description: "Message ID.",
				Required:    true,
			},
			"provider": {
				Type:        "string",
				Description: "IM Platform.",
				Required:    true,
			},
		},
	}
}

// buildWorkflowDmLink returns a Slack mrkdwn link to the workflow page, or "" if any required piece is missing.
func buildWorkflowDmLink(taskCtx types.TaskContext, wfName string) string {
	baseURL := config.Config.BaseUrl
	wfID := taskCtx.GetWorkflowID()
	accountID := taskCtx.GetAccountID()
	if baseURL == "" || wfID == "" {
		return ""
	}
	link := fmt.Sprintf("%s/workflow/%s", baseURL, wfID)
	if accountID != "" {
		link += "?" + url.Values{"accountId": []string{accountID}, "utm": []string{"slack"}}.Encode()
	} else {
		link += "?utm=slack"
	}
	return fmt.Sprintf("<%s|*%s*>", link, wfName)
}
