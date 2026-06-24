package googlechat

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

type GoogleChatJoinSpaceTask struct{}

func (t *GoogleChatJoinSpaceTask) GetName() string {
	return "google_chat.join_space"
}

func (t *GoogleChatJoinSpaceTask) GetDescription() string {
	return "Add the bot to a Google Chat space so it can post messages."
}

func (t *GoogleChatJoinSpaceTask) GetDisplayName() string {
	return "Google Chat Join Space"
}

func (t *GoogleChatJoinSpaceTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Google Chat Join Space Task", "params", params)

	channelID, ok := params["channel_id"].(string)
	if !ok || channelID == "" {
		return nil, errors.New("channel_id is required")
	}

	text, _ := params["text"].(string)

	req := notification.JoinChannelRequest{
		Platform:  "google_chat",
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

func (t *GoogleChatJoinSpaceTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"channel_id": {
				Type:        "string",
				Description: "Google Chat space for the bot to join. Select one from the list, or provide a space ID (e.g. spaces/AAAA) or template.",
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

func (t *GoogleChatJoinSpaceTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"response": {
				Type:        "object",
				Description: "The raw response from the join space API.",
				Required:    true,
			},
		},
	}
}
