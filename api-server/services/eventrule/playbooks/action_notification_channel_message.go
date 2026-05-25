package playbooks

import (
	"encoding/json"
	"fmt"
	"io"
	"nudgebee/services/common"
	"nudgebee/services/config"
)

type notificationChannelMessageAction struct{}

type notificationChannelMessageParams struct {
	Platform   string `json:"platform"`
	ChannelId  string `json:"channel_id"`
	IncidentID string `json:"incident_id"`
	TeamId     string `json:"team_id,omitempty"`
	Text       string `json:"text"`
	Title      string `json:"title,omitempty"`
}

func (a *notificationChannelMessageAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params notificationChannelMessageParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal params: %w", err)
	}

	if params.ChannelId == "" {
		ctx.GetLogger().Error("channel id is mandatory for messaging", "params", rawParams)
		return nil, fmt.Errorf("channel id is mandatory for messaging")
	}

	if params.Platform == "" {
		return nil, fmt.Errorf("platform is required")
	}
	if params.ChannelId == "" {
		return nil, fmt.Errorf("channel_id is required")
	}
	if params.IncidentID == "" {
		return nil, fmt.Errorf("incident_id is required")
	}
	if params.Text == "" {
		return nil, fmt.Errorf("text is required")
	}

	notificationPayload := map[string]any{
		"platform":   params.Platform,
		"tenant_id":  ctx.GetTenantId(),
		"channel_id": params.ChannelId,
		"session_id": params.IncidentID,
		"text":       params.Text,
	}

	notificationServiceURL := config.Config.NotificationServiceUrl
	if notificationServiceURL == "" {
		return nil, fmt.Errorf("notifications service URL not configured")
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	ctx.GetLogger().Debug("sending message to notification channel",
		"platform", params.Platform,
		"channel_id", params.ChannelId,
		"session_id", params.IncidentID,
	)

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/api/channels/message", notificationServiceURL),
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(notificationPayload),
	)
	if err != nil {
		ctx.GetLogger().Error("failed to send message to notification channel", "error", err)
		return nil, fmt.Errorf("failed to send message to notification channel: %w", err)
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
		ctx.GetLogger().Error("channel message failed",
			"status_code", resp.StatusCode,
			"response", string(bodyBytes),
		)
		return nil, fmt.Errorf("notification service returned error: %s (status: %d)", string(bodyBytes), resp.StatusCode)
	}

	// Parse response
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
				Message:  fmt.Sprintf("Sent message to %s channel %s", platformDisplay, params.ChannelId),
				Severity: "info",
			},
		},
	}, nil
}
