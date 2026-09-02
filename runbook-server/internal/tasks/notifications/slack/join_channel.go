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
	bindAccount, _ := params["bind_account"].(bool)
	bindSessionID, _ := params["bind_session_id"].(string)

	req := notification.JoinChannelRequest{
		Platform:      "slack",
		ChannelID:     channelID,
		AccountID:     taskCtx.GetAccountID(),
		TeamID:        "",
		Text:          text,
		BindAccount:   bindAccount,
		BindSessionID: bindSessionID,
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
				Description: "Public Slack channel for the bot to join. Select one from the list, or provide a channel ID or template. Private channels can't be joined by the bot.",
				Required:    true,
			},
			"text": {
				Type:        "string",
				Description: "Optional message to send upon joining.",
				Required:    false,
			},
			"bind_account": {
				Type:        "boolean",
				Description: "Permanently bind this channel to the account so future @mentions skip the account picker. Use for dedicated incident/account channels only — not generic channels the bot is added to for other reasons. Pair with bind_session_id to also route future @mentions into the same investigation conversation; without it, future @mentions still skip the picker but start a fresh conversation.",
				Required:    false,
			},
			"bind_session_id": {
				Type:        "string",
				Description: "Set to {{ event.fingerprint }} together with bind_account to make future @mentions in this channel continue the same conversation llm.event_investigate produced for this event, instead of starting a fresh chat session. Leave unset if you only want bind_account's account-skip behavior.",
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
