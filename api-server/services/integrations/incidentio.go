package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const (
	IncidentIOConfigUrl           = "url"
	IncidentIOConfigPassword      = "password" // API key stored as password
	IncidentIOConfigAuthType      = "auth_type"
	IncidentIOConfigProjects      = "projects" // Incident types
	IncidentIOConfigLastConnected = "last_connected"
)

const (
	// IntegrationIncidentIO is the integration type. It must stay a single
	// lowercase word — it doubles as the ticket_tool_types enum value and the
	// tickets.platform column value, neither of which tolerates a dot.
	IntegrationIncidentIO = "incidentio"

	// incidentIODefaultURL is incident.io's public API host. Kept configurable
	// so EU-resident tenants can point at their own regional endpoint.
	incidentIODefaultURL = "https://api.incident.io"

	// incidentIOHTTPTimeout bounds the credential-validation probe so an
	// unreachable host fails fast instead of stalling the create/edit request.
	incidentIOHTTPTimeout = 15 * time.Second

	// incident.io versions its API per resource: severities is V1, users is V2.
	// Verified against the live API — the wrong version returns 404, not 401.
	incidentIOSeveritiesPath = "/v1/severities"
	incidentIOUsersPath      = "/v2/users"
)

func init() {
	core.RegisterIntegration(IncidentIO{})
}

type IncidentIO struct{}

func (i IncidentIO) Name() string {
	return IntegrationIncidentIO
}

func (i IncidentIO) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (i IncidentIO) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{core.IntegrationConfigName, IncidentIOConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "A unique name to identify this incident.io account configuration",
				Default:     "",
				Priority:    100,
			},
			IncidentIOConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "incident.io API URL (e.g., api.incident.io)",
				Priority:    99,
				Default:     "api.incident.io",
				AllowEdit:   false,
			},
			IncidentIOConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "incident.io API key",
				IsEncrypted: true,
				Priority:    98,
			},
			IncidentIOConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type (bearer)",
				Default:     "bearer",
				Hidden:      true,
			},
			IncidentIOConfigProjects: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of incident.io incident types",
				Hidden:      true,
			},
			IncidentIOConfigLastConnected: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last sync timestamp",
				Hidden:      true,
			},
		},
	}
}

func (i IncidentIO) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	apiKey := core.ConfigValue(values, IncidentIOConfigPassword)
	if apiKey == "" {
		return []error{fmt.Errorf("incident.io api key is required")}
	}

	if err := incidentIOProbe(core.ConfigValue(values, IncidentIOConfigUrl), apiKey); err != nil {
		return []error{err}
	}

	return nil
}

// normalizeIncidentIOURL turns a user-entered host into a usable base URL.
// Threading the configured URL through here means a typo in the form surfaces
// as a connection error at validation time rather than being silently replaced
// by the default endpoint.
func normalizeIncidentIOURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return incidentIODefaultURL
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	return strings.TrimRight(rawURL, "/")
}

// incidentIOProbe performs an auth-only check against the incident.io API.
//
// Severities is the cheapest authenticated read that every API key can perform.
// /v2/users needs an extra scope, so probing it would reject a correctly-scoped
// incident-management key.
//
// NOTE the V1 prefix: incident.io versions its API per resource, and
// severities is still V1. /v2/severities returns 404, which would surface as a
// bogus "endpoint not found" for every otherwise-valid key.
func incidentIOProbe(rawURL, apiKey string) error {
	base := normalizeIncidentIOURL(rawURL)

	// Route through the shared client (SSRF protection + connection reuse), as
	// the other integrations do.
	resp, err := common.HttpGet(
		base+incidentIOSeveritiesPath,
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Bearer " + apiKey,
			"Accept":        "application/json",
		}),
		common.HttpWithTimeout(incidentIOHTTPTimeout),
	)
	if err != nil {
		return fmt.Errorf("incident.io connection failed (URL: %s): %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("incident.io authentication failed: invalid API key")
	case http.StatusForbidden:
		return fmt.Errorf("incident.io authentication failed: API key lacks permission to read severities")
	case http.StatusNotFound:
		return fmt.Errorf("incident.io connection failed: endpoint not found (check the URL field)")
	default:
		return fmt.Errorf("incident.io connection failed: unexpected status %d", resp.StatusCode)
	}
}

// incidentIOUser mirrors the subset of GET /v2/users that identity sync needs.
type incidentIOUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ListUsers enumerates incident.io users (email + name). Implements core.UserLister.
func (i IncidentIO) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	apiKey := core.ConfigValue(values, IncidentIOConfigPassword)
	if apiKey == "" {
		return nil, fmt.Errorf("incidentio: missing api key")
	}
	base := normalizeIncidentIOURL(core.ConfigValue(values, IncidentIOConfigUrl))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+incidentIOUsersPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("incidentio: list users: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("incidentio: unexpected status %d", resp.StatusCode)
	}

	var payload struct {
		Users []incidentIOUser `json:"users"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("incidentio: decode users: %w", err)
	}

	out := make([]core.ExternalUser, 0, len(payload.Users))
	for _, u := range payload.Users {
		out = append(out, core.ExternalUser{
			ID:          u.ID,
			Username:    u.Email,
			Email:       u.Email,
			DisplayName: u.Name,
		})
	}
	return out, nil
}
