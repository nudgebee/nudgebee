package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(Slack{})
}

const (
	IntegrationSlack = "slack"

	// Secret config values — must stay in sync with the encrypted set written by
	// notifications-server (services/messaging_installations.py).
	SlackConfigBotToken     = "bot_token"
	SlackConfigRefreshToken = "refresh_token"

	SlackConfigTokenExpiresAt        = "token_expires_at"
	SlackConfigRefreshTokenExpiresAt = "refresh_token_expires_at"
	SlackConfigBotId                 = "bot_id"
	SlackConfigAppId                 = "app_id"
	SlackConfigClientId              = "client_id"
	SlackConfigInstalledBy           = "installed_by"
	SlackConfigScopes                = "scopes"
	SlackConfigTeamName              = "team_name"
	SlackConfigDefaultChannelId      = "default_channel_id"
	SlackConfigDefaultChannelName    = "default_channel_name"
)

// Slack connects a Slack workspace (one per tenant, code-enforced at install) to
// a Nudgebee tenant for notification delivery. integrations.name holds the Slack
// team (workspace) ID. The OAuth bot token and refresh token are stored encrypted
// in integration_config_values; the default destination is the scalar
// default_channel_id (sending uses the ID). default_channel_name is a cached,
// non-authoritative display label kept only so the UI can show the channel name
// without a live Slack lookup.
type Slack struct{}

func (Slack) Name() string {
	return IntegrationSlack
}

func (Slack) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessaging
}

func (Slack) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			SlackConfigBotToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Slack bot OAuth token",
				IsEncrypted: true,
				Hidden:      true,
			},
			SlackConfigRefreshToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Slack OAuth refresh token",
				IsEncrypted: true,
				Hidden:      true,
			},
			SlackConfigTokenExpiresAt:        {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigRefreshTokenExpiresAt: {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigBotId:                 {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigAppId:                 {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigClientId:              {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigInstalledBy:           {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigScopes:                {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigTeamName:              {Type: core.ToolSchemaTypeString, Description: "Slack workspace name", Hidden: true},
			SlackConfigDefaultChannelId:      {Type: core.ToolSchemaTypeString, Description: "Default Slack channel ID for notifications"},
			SlackConfigDefaultChannelName:    {Type: core.ToolSchemaTypeString, Description: "Cached display name of the default Slack channel", Hidden: true},
		},
	}
}

func (Slack) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}

// slackMembersResponse mirrors the Slack users.list payload.
type slackMembersResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Members []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		Deleted  bool   `json:"deleted"`
		IsBot    bool   `json:"is_bot"`
		Profile  struct {
			Email       string `json:"email"`
			RealName    string `json:"real_name"`
			DisplayName string `json:"display_name"`
		} `json:"profile"`
	} `json:"members"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// ListUsers enumerates Slack workspace members (email from profile) via the stored
// bot token, calling slack.com directly. Bots and deactivated users are skipped.
// Implements core.UserLister.
func (Slack) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	token := core.ConfigValue(values, SlackConfigBotToken)
	if token == "" {
		return nil, fmt.Errorf("slack: missing bot token")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var out []core.ExternalUser
	cursor := ""
	for page := 0; page < 100; page++ { // safety cap: 100 pages * 200 = 20k users
		endpoint := "https://slack.com/api/users.list?limit=200"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return out, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return out, fmt.Errorf("slack: list users: %w", err)
		}
		var parsed slackMembersResponse
		decErr := json.NewDecoder(resp.Body).Decode(&parsed)
		_ = resp.Body.Close()
		if decErr != nil {
			return out, fmt.Errorf("slack: decode users: %w", decErr)
		}
		if !parsed.OK {
			return out, fmt.Errorf("slack: api error: %s", parsed.Error)
		}

		for _, m := range parsed.Members {
			if m.Deleted || m.IsBot || m.ID == "USLACKBOT" || m.Profile.Email == "" {
				continue
			}
			name := m.Profile.DisplayName
			if name == "" {
				name = m.Profile.RealName
			}
			if name == "" {
				name = m.Name
			}
			out = append(out, core.ExternalUser{ID: m.ID, Username: m.Name, Email: m.Profile.Email, DisplayName: name})
		}

		cursor = parsed.ResponseMetadata.NextCursor
		if cursor == "" {
			break
		}
	}
	return out, nil
}
