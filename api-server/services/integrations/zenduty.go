package integrations

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	ZenDutyDefaultURL = "https://www.zenduty.com/api"
)

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

	// Extract config values
	for _, config := range values {
		if config.Name == ZenDutyConfigPassword {
			apiKey = config.Value
		}
	}

	// Validate required fields
	if apiKey == "" {
		return []error{fmt.Errorf("zenduty api key is required")}
	}

	// Test connection by listing teams
	err := validateZenDutyConnection(apiKey)
	if err != nil {
		return []error{fmt.Errorf("zenduty authentication failed: %w", err)}
	}

	return nil
}

// validateZenDutyConnection tests the ZenDuty API connection using the provided API key.
func validateZenDutyConnection(apiKey string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("GET", ZenDutyDefaultURL+"/account/teams/", nil)
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
