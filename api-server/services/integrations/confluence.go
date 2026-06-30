package integrations

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
)

func init() {
	core.RegisterIntegration(Confluence{})
}

const IntegrationConfluence = "confluence"

type Confluence struct {
}

func (m Confluence) Name() string {
	return IntegrationConfluence
}

func (m Confluence) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDocs
}

func (m Confluence) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{"username", "token", "host"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"username": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence username",
				Default:     "",
				IsTestable:  true,
				Priority:    70,
			},
			"token": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence API token",
				Default:     "",
				IsTestable:  true,
				IsEncrypted: true,
				Priority:    68,
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence host URL",
				Default:     "",
				IsTestable:  true,
				Priority:    80,
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence namespace",
				Default:     "",
				Priority:    50,
			},
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Confluence Integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
		},
	}
}

func (m Confluence) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range integrationConfig {
		configMap[c.Name] = c.Value
	}

	host := configMap["host"]
	username := configMap["username"]
	token := configMap["token"]

	if host == "" {
		return []error{fmt.Errorf("host is required")}
	}
	if username == "" {
		return []error{fmt.Errorf("username is required")}
	}
	if token == "" {
		return []error{fmt.Errorf("token is required")}
	}

	host = strings.TrimRight(host, "/")
	parsedHost, err := url.Parse(host)
	if err != nil || (parsedHost.Scheme != "http" && parsedHost.Scheme != "https") || parsedHost.Host == "" {
		return []error{fmt.Errorf("host must be a valid URL with http or https scheme (e.g. https://your-domain.atlassian.net)")}
	}
	authToken := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
	resp, err := common.HttpGet(
		fmt.Sprintf("%s/wiki/rest/api/space", host),
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Basic " + authToken,
			"Accept":        "application/json",
		}),
		common.HttpWithQueryParams(map[string]string{"limit": "1"}),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to Confluence API: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// credentials/host valid; fall through to namespace validation below
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("invalid Confluence credentials (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for Confluence (HTTP 403)")}
	default:
		return []error{fmt.Errorf("Confluence API returned unexpected status: HTTP %d", resp.StatusCode)}
	}

	// namespace is the Confluence space key the RAG scraper walks. When set it must
	// resolve to a real, accessible space — otherwise the connection test would report
	// success for a space that yields zero pages. An empty namespace means "all spaces"
	// and is left unvalidated by design.
	namespace := strings.TrimSpace(configMap["namespace"])
	if namespace == "" {
		return nil
	}

	// Query the space-key-filtered list endpoint. Unlike the per-space path, this
	// returns HTTP 200 with an EMPTY results list for an unknown/inaccessible key
	// (it does not 404), so the response body must be inspected to decide presence.
	spaceResp, err := common.HttpGet(
		fmt.Sprintf("%s/wiki/rest/api/space", host),
		common.HttpWithHeaders(map[string]string{
			"Authorization": "Basic " + authToken,
			"Accept":        "application/json",
		}),
		common.HttpWithQueryParams(map[string]string{
			"spaceKey": namespace,
			"limit":    "1",
		}),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to verify Confluence space %q: %w", namespace, err)}
	}
	defer func() { _ = spaceResp.Body.Close() }()

	if spaceResp.StatusCode != http.StatusOK {
		return []error{fmt.Errorf("failed to verify Confluence space %q: Confluence API returned HTTP %d", namespace, spaceResp.StatusCode)}
	}

	body, err := io.ReadAll(spaceResp.Body)
	if err != nil {
		return []error{fmt.Errorf("failed to read Confluence space response for %q: %w", namespace, err)}
	}

	var spaceList struct {
		Results []struct {
			Key string `json:"key"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &spaceList); err != nil {
		return []error{fmt.Errorf("failed to parse Confluence space response for %q: %w", namespace, err)}
	}

	for _, s := range spaceList.Results {
		if strings.EqualFold(s.Key, namespace) {
			return nil
		}
	}
	return []error{fmt.Errorf("Confluence space %q does not exist or is not accessible", namespace)}
}
