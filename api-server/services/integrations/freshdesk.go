package integrations

import (
	"encoding/base64"
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
	FreshdeskConfigUrl           = "url"
	FreshdeskConfigPassword      = "password"
	FreshdeskConfigAuthType      = "auth_type"
	FreshdeskConfigProjects      = "projects"
	FreshdeskConfigLastConnected = "last_connected"
)

// freshdeskHTTPTimeout bounds the credential-validation probe so an unreachable
// domain fails fast instead of stalling the create/edit request.
const freshdeskHTTPTimeout = 15 * time.Second

func init() {
	core.RegisterIntegration(Freshdesk{})
}

const IntegrationFreshdesk = "freshdesk"

type Freshdesk struct{}

func (f Freshdesk) Name() string {
	return IntegrationFreshdesk
}

func (f Freshdesk) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (f Freshdesk) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{FreshdeskConfigUrl, FreshdeskConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			FreshdeskConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "Freshdesk URL, e.g. https://yourco.freshdesk.com",
			},
			FreshdeskConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "Freshdesk API key",
				IsEncrypted: true,
			},
			FreshdeskConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type (basic)",
				Default:     "basic",
			},
			FreshdeskConfigProjects: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of Freshdesk groups",
			},
			FreshdeskConfigLastConnected: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last sync timestamp",
			},
		},
	}
}

func (f Freshdesk) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	url := ""
	apiKey := ""

	// Extract config values. Freshdesk basic auth is <api_key>:X, so the API key
	// is stored in the encrypted password field; there is no separate username.
	for _, config := range values {
		switch config.Name {
		case FreshdeskConfigUrl:
			url = config.Value
		case FreshdeskConfigPassword:
			apiKey = config.Value
		}
	}

	if url == "" {
		return []error{fmt.Errorf("freshdesk url is required")}
	}
	if apiKey == "" {
		return []error{fmt.Errorf("freshdesk api key is required")}
	}

	if err := freshdeskProbe(url, apiKey); err != nil {
		return []error{err}
	}

	return nil
}

// freshdeskProbe performs an auth-only check against the Freshdesk REST API v2 by
// listing a single ticket. Freshdesk basic auth places the API key in the username
// position and a literal "X" in the password position (SetBasicAuth(apiKey, "X")).
func freshdeskProbe(rawURL, apiKey string) error {
	base := strings.TrimSuffix(rawURL, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	endpoint := base + "/api/v2/tickets?per_page=1"

	// Route through the shared client (SSRF protection + connection reuse), as
	// other integrations do. Freshdesk basic auth = API key in the username,
	// literal "X" in the password.
	resp, err := common.HttpGet(
		endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(apiKey+":X")),
			"Accept":        "application/json",
		}),
		common.HttpWithTimeout(freshdeskHTTPTimeout),
	)
	if err != nil {
		return fmt.Errorf("freshdesk connection failed (URL: %s): %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("freshdesk authentication failed: invalid API key")
	case http.StatusForbidden:
		return fmt.Errorf("freshdesk authentication failed: API key lacks permission to read tickets")
	case http.StatusNotFound:
		return fmt.Errorf("freshdesk connection failed: domain not found (check the URL field)")
	default:
		return fmt.Errorf("freshdesk connection failed: unexpected status %d", resp.StatusCode)
	}
}
