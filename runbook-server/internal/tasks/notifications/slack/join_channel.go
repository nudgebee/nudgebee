package slack

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

type SlackJoinChannelTask struct{}

func (t *SlackJoinChannelTask) GetName() string {
	return "slack.join_channel"
}

func (t *SlackJoinChannelTask) GetDescription() string {
	return "Add the bot to a Slack channel so it can post messages."
}

func (t *SlackJoinChannelTask) GetDisplayName() string {
	return "Slack Join Channel"
}

func (t *SlackJoinChannelTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Slack Join Channel Task", "params", params)

	channelID, ok := params["channel_id"].(string)
	if !ok || channelID == "" {
		return nil, errors.New("channel_id is required")
	}

	text, _ := params["text"].(string)

	req := notification.JoinChannelRequest{
		Platform:  "slack",
		ChannelID: channelID,
		AccountID: taskCtx.GetAccountID(),
		TeamID:    "",
		Text:      text,
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := notification.JoinChannel(requestContext, req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (t *SlackJoinChannelTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"channel_id": {
				Type:        "string",
				Description: "The ID of the Public Slack channel to join.",
				Required:    true,
			},
			"text": {
				Type:        "string",
				Description: "Optional message to send upon joining.",
				Required:    false,
			},
		},
	}
}

func (t *SlackJoinChannelTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"response": {
				Type:        "object",
				Description: "The raw response from the join channel API.",
				Required:    true,
			},
		},
	}
}
