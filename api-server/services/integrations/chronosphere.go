package integrations

import (
	"fmt"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
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
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"chronosphere_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the Chronosphere API (e.g., https://<tenant>.chronosphere.io)",
				Priority:    85,
				IsTestable:  true,
			},
			"chronosphere_token": {
				Type:        core.ToolSchemaTypeString,
				Description: "Chronosphere API (Bearer) access token used for authentication",
				IsEncrypted: true,
				Priority:    70,
				IsTestable:  true,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Custom name for this Chronosphere integration configuration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "List of available accounts that can be linked with Chronosphere integration",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Chronosphere default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         15,
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Chronosphere default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         14,
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

	chronosphereURL = normalizeChronosphereURL(chronosphereURL)
	chronosphereToken = strings.TrimSpace(chronosphereToken)

	var errs []error
	if chronosphereURL == "" {
		errs = append(errs, fmt.Errorf("chronosphere_url is required"))
	} else if !strings.HasPrefix(chronosphereURL, "http://") && !strings.HasPrefix(chronosphereURL, "https://") {
		errs = append(errs, fmt.Errorf("chronosphere_url must start with http:// or https:// (got %q)", chronosphereURL))
	}
	if chronosphereToken == "" {
		errs = append(errs, fmt.Errorf("chronosphere_token is required"))
	}
	if len(errs) > 0 {
		return errs
	}

	resp, err := common.HttpGet(
		fmt.Sprintf("%s/api/v1/label/__name__/values", chronosphereURL),
		common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", chronosphereToken),
			"Content-Type":  "application/json",
		}),
		common.HttpWithQueryParams(map[string]string{"limit": "1"}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to Chronosphere at %s: %w", chronosphereURL, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("invalid Chronosphere API token (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for Chronosphere API token (HTTP 403)")}
	case http.StatusNotFound:
		return []error{fmt.Errorf("Chronosphere /api/v1/label/__name__/values not found at %s — check chronosphere_url", chronosphereURL)}
	default:
		return []error{fmt.Errorf("Chronosphere API returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}

// normalizeChronosphereURL trims whitespace and strips any path/query/fragment
// so users can paste the URL they have open in the browser (e.g.
// https://<tenant>.chronosphere.io/explorer/...).
func normalizeChronosphereURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	return parsed.Scheme + "://" + parsed.Host
}
