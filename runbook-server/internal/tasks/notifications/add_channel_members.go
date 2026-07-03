package notifications

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/notification"
)

// AddChannelMembersTask defines a task for adding users to a channel/space.
type AddChannelMembersTask struct{}

func (t *AddChannelMembersTask) GetName() string {
	return "notifications.add_channel_members"
}

// GetDescription returns a brief description of the task.
func (t *AddChannelMembersTask) GetDescription() string {
	return "Add users to a Slack channel, MS Teams team/channel, or Google Chat space."
}

// GetDisplayName returns a human-readable name for the task.
func (t *AddChannelMembersTask) GetDisplayName() string {
	return "Add Channel Members"
}

func (t *AddChannelMembersTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing Add Channel Members Task", "params", params)

	provider, ok := params["provider"].(string)
	if !ok || provider == "" {
		return nil, errors.New("provider is required and must be a string")
	}

	channelID, ok := params["channel_id"].(string)
	if !ok || channelID == "" {
		return nil, errors.New("channel_id is required and must be a string")
	}

	userIDs, err := toStringSlice(params["user_ids"])
	if err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return nil, errors.New("user_ids is required and must contain at least one user")
	}

	request := notification.AddChannelMembersRequest{
		Platform:  provider,
		ChannelID: channelID,
		UserIDs:   userIDs,
		AccountID: taskCtx.GetAccountID(),
	}
	if team, ok := params["team_id"].(string); ok {
		request.TeamID = team
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := notification.AddChannelMembers(requestContext, request)

	return map[string]any{
		"provider":        resp.Platform,
		"channel":         resp.ChannelID,
		"team":            resp.TeamID,
		"added":           resp.Added,
		"already_members": resp.AlreadyMembers,
		"failed":          resp.Failed,
	}, err
}

// toStringSlice coerces a schema "array" value into a []string. It accepts a
// []string directly, a []any of strings (the shape produced by JSON/template
// resolution), or a single string (convenience for one user).
func toStringSlice(value any) ([]string, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case []string:
		return v, nil
	case string:
		if v == "" {
			return nil, nil
		}
		return []string{v}, nil
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, errors.New("user_ids must be a list of strings")
			}
			if s != "" {
				result = append(result, s)
			}
		}
		return result, nil
	default:
		return nil, errors.New("user_ids must be a list of strings")
	}
}

func (t *AddChannelMembersTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"provider": {
				Type:        "string",
				Description: "Notification Provider",
				Required:    true,
				Options:     []string{"slack", "ms_teams", "google_chat"},
				Order:       1,
			},
			"channel_id": {
				Type:        "string",
				Description: "Channel/space ID to add users to. For MS Teams this is the channel in the team identified by team_id.",
				Required:    true,
				Order:       2,
			},
			"user_ids": {
				Type:        "array",
				SubType:     "string",
				Description: "User IDs to add. For MS Teams these are Azure AD object IDs; for Google Chat, people IDs.",
				Required:    true,
				Order:       3,
			},
			"team_id": {
				Type:        "string",
				Description: "MS Teams TeamId (required for ms_teams). Members are added at the team level, which grants access to its standard channels.",
				Required:    false,
				RequiredWhen: &types.RequiredWhen{
					Field: "provider",
					Value: []string{"ms_teams"},
				},
				VisibleWhen: &types.VisibleWhen{
					Field: "provider",
					Value: []string{"ms_teams"},
				},
				Order: 4,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *AddChannelMembersTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"provider": {
				Type:        "string",
				Description: "Notification Provider.",
				Required:    true,
			},
			"channel": {
				Type:        "string",
				Description: "Channel/space ID (team ID for MS Teams).",
				Required:    true,
			},
			"team": {
				Type:        "string",
				Description: "MS Teams TeamId.",
				Required:    false,
			},
			"added": {
				Type:        "array",
				SubType:     "string",
				Description: "User IDs successfully added.",
				Required:    true,
			},
			"already_members": {
				Type:        "array",
				SubType:     "string",
				Description: "User IDs that were already members.",
				Required:    true,
			},
			"failed": {
				Type:        "array",
				Description: "Users that could not be added, each with a user_id and error.",
				Required:    true,
			},
		},
	}
}
