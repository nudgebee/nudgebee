package playbooks

import (
	"encoding/json"
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/config"
)

type notificationChannelJoinAction struct{}

type notificationChannelJoinParams struct {
	Platform   string `json:"platform"`
	ChannelId  string `json:"channel_id"`
	IncidentID string `json:"incident_id"`
	TeamId     string `json:"team_id,omitempty"`
	Text       string `json:"text,omitempty"`
	Title      string `json:"title,omitempty"`
}

func (a *notificationChannelJoinAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params notificationChannelJoinParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal params: %w", err)
	}

	if params.ChannelId == "" {
		ctx.GetLogger().Error("channel id is mandatory for joining a channel", "params", rawParams)
		return nil, fmt.Errorf("channel id is mandatory for joining a channel")
	}

	if params.Platform == "" {
		return nil, fmt.Errorf("platform is required")
	}
	if params.ChannelId == "" {
		return nil, fmt.Errorf("channel_id is required")
	}
	if params.IncidentID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	notificationPayload := map[string]any{
		"platform":   params.Platform,
		"account_id": ctx.GetAccountId(),
		"tenant_id":  ctx.GetTenantId(),
		"channel_id": params.ChannelId,
		"session_id": params.IncidentID,
	}

	if params.TeamId != "" {
		notificationPayload["team_id"] = params.TeamId
	}
	if params.Text != "" {
		notificationPayload["text"] = params.Text
	}

	notificationServiceURL := config.Config.NotificationServiceUrl
	if notificationServiceURL == "" {
		return nil, fmt.Errorf("notifications service URL not configured")
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	ctx.GetLogger().Debug("joining notification channel",
		"platform", params.Platform,
		"channel_id", params.ChannelId,
		"session_id", params.IncidentID,
	)

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/api/channels/join", notificationServiceURL),
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(notificationPayload),
	)
	if err != nil {
		ctx.GetLogger().Error("failed to join notification channel", "error", err)
		return nil, fmt.Errorf("failed to join notification channel: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			ctx.GetLogger().Error("failed to close response body", "error", err)
		}
	}(resp.Body)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Error("failed to read response body", "error", err)
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		ctx.GetLogger().Error("join channel failed",
			"status_code", resp.StatusCode,
			"response", string(bodyBytes),
		)
		return nil, fmt.Errorf("notification service returned error: %s (status: %d)", string(bodyBytes), resp.StatusCode)
	}

	var responseData map[string]any
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		ctx.GetLogger().Warn("failed to parse response as JSON", "error", err)
		responseData = map[string]any{
			"raw_response": string(bodyBytes),
		}
	}

	platformDisplay := getPlatformDisplayName(params.Platform)

	return PlaybookActionResponseJson{
		Data:           "success",
		AdditionalInfo: responseData,
		Insight: []PlaybookActionResponseInsight{
			{
				Message:  fmt.Sprintf("Joined %s channel %s", platformDisplay, params.ChannelId),
				Severity: "info",
			},
		},
	}, nil
}
