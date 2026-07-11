package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(MsTeams{})
}

const (
	IntegrationMsTeams = "ms_teams"

	// Secret config values — must stay in sync with the encrypted set written by
	// notifications-server (services/messaging_installations.py).
	MsTeamsConfigAccessToken  = "access_token"
	MsTeamsConfigRefreshToken = "refresh_token"

	MsTeamsConfigTokenExpiresAt        = "token_expires_at"
	MsTeamsConfigRefreshTokenExpiresAt = "refresh_token_expires_at"
	MsTeamsConfigAppId                 = "app_id"
	MsTeamsConfigClientId              = "client_id"
	MsTeamsConfigInstalledBy           = "installed_by"
	MsTeamsConfigScopes                = "scopes"
	MsTeamsConfigDefaultTeamId         = "default_team_id"
	MsTeamsConfigDefaultChannelId      = "default_channel_id"
	MsTeamsConfigDefaultTeamName       = "default_team_name"
	MsTeamsConfigDefaultChannelName    = "default_channel_name"
)

// MsTeams connects Microsoft Teams (one per tenant, code-enforced at install) to
// a Nudgebee tenant for notification delivery. integrations.name holds the Azure
// AD tenant ID. The Graph access token and refresh token are stored encrypted in
// integration_config_values; the default destination is the scalar pair
// default_team_id + default_channel_id (a Teams channel is a (team_id,channel_id)
// compound; sending uses the IDs). default_team_name / default_channel_name are
// cached, non-authoritative display labels kept only so the UI can show the
// team/channel names without a live Microsoft Graph lookup.
type MsTeams struct{}

func (MsTeams) Name() string {
	return IntegrationMsTeams
}

func (MsTeams) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessaging
}

func (MsTeams) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			MsTeamsConfigAccessToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Microsoft Graph access token",
				IsEncrypted: true,
				Hidden:      true,
			},
			MsTeamsConfigRefreshToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Microsoft Graph refresh token",
				IsEncrypted: true,
				Hidden:      true,
			},
			MsTeamsConfigTokenExpiresAt:        {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigRefreshTokenExpiresAt: {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigAppId:                 {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigClientId:              {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigInstalledBy:           {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigScopes:                {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigDefaultTeamId:         {Type: core.ToolSchemaTypeString, Description: "Default Microsoft Teams team ID for notifications"},
			MsTeamsConfigDefaultChannelId:      {Type: core.ToolSchemaTypeString, Description: "Default Microsoft Teams channel ID for notifications"},
			MsTeamsConfigDefaultTeamName:       {Type: core.ToolSchemaTypeString, Description: "Cached display name of the default Microsoft Teams team", Hidden: true},
			MsTeamsConfigDefaultChannelName:    {Type: core.ToolSchemaTypeString, Description: "Cached display name of the default Microsoft Teams channel", Hidden: true},
		},
	}
}

func (MsTeams) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}

const (
	msTeamsGraphUsersURL = "https://graph.microsoft.com/v1.0/users?$select=id,displayName,mail,userPrincipalName,accountEnabled&$top=100"
	msTeamsMaxPages      = 200 // safety cap → up to 20k users
)

type msGraphUser struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
	AccountEnabled    *bool  `json:"accountEnabled"`
}

type msGraphUsersResponse struct {
	Value    []msGraphUser `json:"value"`
	NextLink string        `json:"@odata.nextLink"`
}

// ListUsers enumerates Microsoft 365 users via Graph for identity sync. It uses the
// stored Graph access_token as-is — token refresh lives in notifications-server
// (which persists fresh tokens back into config), and the Teams app client secret
// isn't available here. A clearly-expired or 401 token yields a skip (the sync
// loop logs and continues) rather than a refresh. Implements core.UserLister.
func (MsTeams) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	token := core.ConfigValue(values, MsTeamsConfigAccessToken)
	if token == "" {
		return nil, fmt.Errorf("ms_teams: no access token configured")
	}
	if msTeamsTokenExpired(core.ConfigValue(values, MsTeamsConfigTokenExpiresAt)) {
		return nil, nil // refreshed out-of-band by notifications-server; skip this run
	}

	client := &http.Client{Timeout: 20 * time.Second}
	var out []core.ExternalUser
	next := msTeamsGraphUsersURL
	for page := 0; page < msTeamsMaxPages && next != ""; page++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, fmt.Errorf("ms_teams: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ms_teams: graph request failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("ms_teams: read graph response: %w", readErr)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("ms_teams: graph token unauthorized (stale access token)")
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ms_teams: graph returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var parsed msGraphUsersResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("ms_teams: decode graph response: %w", err)
		}
		for _, u := range parsed.Value {
			if eu, ok := mapGraphUser(u); ok {
				out = append(out, eu)
			}
		}
		next = parsed.NextLink
	}
	return out, nil
}

// mapGraphUser converts a Graph user to an ExternalUser, skipping disabled accounts.
// Prefers mail, falling back to userPrincipalName (UPN is email-shaped). Pure for
// unit testing.
func mapGraphUser(u msGraphUser) (core.ExternalUser, bool) {
	if u.ID == "" {
		return core.ExternalUser{}, false
	}
	if u.AccountEnabled != nil && !*u.AccountEnabled {
		return core.ExternalUser{}, false
	}
	email := strings.TrimSpace(u.Mail)
	if email == "" {
		email = strings.TrimSpace(u.UserPrincipalName)
	}
	return core.ExternalUser{
		ID:          u.ID,
		Username:    u.UserPrincipalName,
		Email:       email,
		DisplayName: u.DisplayName,
	}, true
}

// msTeamsTokenExpired reports whether a stored token_expires_at is in the past.
// Accepts RFC3339 or epoch-seconds; unknown/blank formats are treated as
// not-expired so a real 401 (not a parse guess) decides.
func msTeamsTokenExpired(expiresAt string) bool {
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" {
		return false
	}
	if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
		return time.Now().After(t)
	}
	if secs, err := strconv.ParseInt(expiresAt, 10, 64); err == nil {
		return time.Now().After(time.Unix(secs, 0))
	}
	return false
}
