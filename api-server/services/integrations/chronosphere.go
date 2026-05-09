package integrations

import (
	"fmt"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
)

func init() {
	core.RegisterIntegration(Chronosphere{})
}

const IntegrationChronosphere = "chronosphere"

type Chronosphere struct {
}

func (m Chronosphere) Name() string {
	return IntegrationChronosphere
}

func (m Chronosphere) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m Chronosphere) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"chronosphere_url", "chronosphere_token"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"chronosphere_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the Chronosphere API (e.g., https://<tenant>.chronosphere.io)",
			},
			"chronosphere_token": {
				Type:        core.ToolSchemaTypeString,
				Description: "Chronosphere API (Bearer) access token used for authentication",
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "List of available accounts that can be linked with Chronosphere integration",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Custom name for this Chronosphere integration configuration",
				Default:          "",
				AutoGenerateFunc: "",
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Chronosphere default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Chronosphere default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
		},
	}
}

func (m Chronosphere) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	var chronosphereURL, chronosphereToken string
	for _, c := range config {
		switch c.Name {
		case "chronosphere_url":
			chronosphereURL = c.Value
		case "chronosphere_token":
			chronosphereToken = c.Value
		}
	}

	if chronosphereURL == "" {
		return []error{fmt.Errorf("chronosphere_url is required")}
	}
	if chronosphereToken == "" {
		return []error{fmt.Errorf("chronosphere_token is required")}
	}

	chronosphereURL = strings.TrimRight(chronosphereURL, "/")
	resp, err := common.HttpGet(
		fmt.Sprintf("%s/api/v1/label/__name__/values", chronosphereURL),
		common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", chronosphereToken),
			"Content-Type":  "application/json",
		}),
		common.HttpWithQueryParams(map[string]string{"limit": "1"}),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to Chronosphere API: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("invalid Chronosphere API token (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for Chronosphere API token (HTTP 403)")}
	default:
		return []error{fmt.Errorf("Chronosphere API returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}
