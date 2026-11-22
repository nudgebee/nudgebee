package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const (
	ZenDutyConfigUrl           = "url"
	ZenDutyConfigUsername      = "username" // Email for reference
	ZenDutyConfigPassword      = "password" // API key stored as password
	ZenDutyConfigAuthType      = "auth_type"
	ZenDutyConfigProjects      = "projects" // Services JSON
	ZenDutyConfigLastConnected = "last_connected"
)

const (
	// ZenDutyDefaultHost is used when a configuration's "url" value is blank —
	// the long-standing default for accounts that predate per-region hosting.
	ZenDutyDefaultHost = "www.zenduty.com"
	ZenDutyDefaultURL  = "https://" + ZenDutyDefaultHost + "/api"
)

// NormalizeZenDutyURL turns the stored ZenDuty "url" config value into an API
// base URL (scheme + host, no trailing slash, "/api" appended). ZenDuty
// (rebranded Xurrent IMR) hosts accounts on region-specific domains (e.g.
// imr.eu.xurrent.com for EU accounts) rather than one global www.zenduty.com,
// so this is per-integration, not a constant. A trailing /api left over from
// copying the old API base URL is stripped before re-appending it, so the
// result never doubles up into ".../api/api". Mirrors ticket-server's
// NormalizeZenDutyURL/NormalizeJiraURL.
func NormalizeZenDutyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ZenDutyDefaultURL
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/") + "/api"
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/api")
	return strings.TrimRight(u.String(), "/") + "/api"
}

// ZenDutyDashboardURL builds the web link for an incident on the account's
// own host, for callers (e.g. the webhook handler) that have no html_url to
// fall back on.
func ZenDutyDashboardURL(rawConfigURL, incidentID string) string {
	host := strings.TrimSuffix(NormalizeZenDutyURL(rawConfigURL), "/api")
	return host + "/dashboard/incidents/" + incidentID
}

func init() {
	core.RegisterIntegration(ZenDuty{})
}

const IntegrationZenDuty = "zenduty"

type ZenDuty struct{}

func (z ZenDuty) Name() string {
	return IntegrationZenDuty
}

func (z ZenDuty) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (z ZenDuty) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{ZenDutyConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			ZenDutyConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "ZenDuty API URL (default: www.zenduty.com)",
				Default:     ZenDutyDefaultURL,
			},
			ZenDutyConfigUsername: {
				Type:        core.ToolSchemaTypeString,
				Description: "ZenDuty email for reference",
			},
			ZenDutyConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "ZenDuty API key",
				IsEncrypted: true,
			},
			ZenDutyConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type (token)",
				Default:     "token",
			},
			ZenDutyConfigProjects: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of ZenDuty services",
			},
			ZenDutyConfigLastConnected: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last sync timestamp",
			},
		},
	}
}

func (z ZenDuty) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	apiKey := ""
	rawURL := ""

	// Extract config values
	for _, config := range values {
		switch config.Name {
		case ZenDutyConfigPassword:
			apiKey = config.Value
		case ZenDutyConfigUrl:
			rawURL = config.Value
		}
	}

	// Validate required fields
	if apiKey == "" {
		return []error{fmt.Errorf("zenduty api key is required")}
	}

	// Test connection by listing teams
	err := validateZenDutyConnection(apiKey, rawURL)
	if err != nil {
		return []error{fmt.Errorf("zenduty authentication failed: %w", err)}
	}

	return nil
}

// validateZenDutyConnection tests the ZenDuty API connection using the provided API key.
func validateZenDutyConnection(apiKey, rawURL string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("GET", NormalizeZenDutyURL(rawURL)+"/account/teams/", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to ZenDuty: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ZenDutyTeam represents a team in ZenDuty.
type ZenDutyTeam struct {
	UniqueID string `json:"unique_id"`
	Name     string `json:"name"`
}

// ZenDutyService represents a service in ZenDuty.
type ZenDutyService struct {
	UniqueID string `json:"unique_id"`
	Name     string `json:"name"`
}

// GetZenDutyTeams fetches all teams from ZenDuty.
func GetZenDutyTeams(apiKey string) ([]ZenDutyTeam, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("GET", ZenDutyDefaultURL+"/account/teams/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch teams: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch teams with status %d: %s", resp.StatusCode, string(body))
	}

	var teams []ZenDutyTeam
	if err := json.NewDecoder(resp.Body).Decode(&teams); err != nil {
		return nil, fmt.Errorf("failed to decode teams response: %w", err)
	}

	return teams, nil
}

// GetZenDutyServices fetches all services for a team from ZenDuty.
func GetZenDutyServices(apiKey, teamID string) ([]ZenDutyService, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	url := fmt.Sprintf("%s/account/teams/%s/services/", ZenDutyDefaultURL, teamID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch services: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch services with status %d: %s", resp.StatusCode, string(body))
	}

	var services []ZenDutyService
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, fmt.Errorf("failed to decode services response: %w", err)
	}

	return services, nil
}

// zenDutyUser mirrors the /account/users/ payload shape.
type zenDutyUser struct {
	UniqueID string `json:"unique_id"`
	User     struct {
		Username  string `json:"username"`
		Email     string `json:"email"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"user"`
}

// ListUsers enumerates ZenDuty account users (email + name) via an authenticated
// GET (Authorization: Token). Implements core.UserLister.
func (z ZenDuty) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	apiKey := core.ConfigValue(values, ZenDutyConfigPassword)
	if apiKey == "" {
		return nil, fmt.Errorf("zenduty: missing api key")
	}
	rawURL := core.ConfigValue(values, ZenDutyConfigUrl)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, NormalizeZenDutyURL(rawURL)+"/account/users/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zenduty: list users: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zenduty: unexpected status %d", resp.StatusCode)
	}

	var users []zenDutyUser
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("zenduty: decode users: %w", err)
	}

	out := make([]core.ExternalUser, 0, len(users))
	for _, u := range users {
		name := fmt.Sprintf("%s %s", u.User.FirstName, u.User.LastName)
		if name == " " {
			name = u.User.Username
		}
		out = append(out, core.ExternalUser{
			ID:          u.User.Username,
			Username:    u.User.Username,
			Email:       u.User.Email,
			DisplayName: name,
		})
	}
	return out, nil
}
