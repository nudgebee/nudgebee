package integrations

import (
	"encoding/base64"
	"fmt"
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
			},
			"username": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence username",
				Default:     "",
				IsTestable:  true,
			},
			"token": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence API token",
				Default:     "",
				IsTestable:  true,
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence host URL",
				Default:     "",
				IsTestable:  true,
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence namespace",
				Default:     "",
			},
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Confluence Integration",
				Default:          "",
				AutoGenerateFunc: "",
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
		return nil
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("invalid Confluence credentials (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for Confluence (HTTP 403)")}
	default:
		return []error{fmt.Errorf("Confluence API returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}
