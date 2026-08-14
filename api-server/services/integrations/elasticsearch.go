package integrations

import (
	"encoding/base64"
	"encoding/json"
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
	// Register under "es:user" so that when the UI creates an ES integration
	// with source="user", it resolves to this full schema (with URL, auth, etc.)
	// instead of the minimal ElasticsearchAgent schema.
	// No plain RegisterIntegration call — that key ("es") belongs to ElasticsearchAgent.
	core.RegisterIntegrationWithSource("ES", "user", Elasticsearch{})
}

type Elasticsearch struct {
}

func (m Elasticsearch) Name() string {
	return "ES"
}

func (m Elasticsearch) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (m Elasticsearch) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"url", "auth_type"},
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the Elasticsearch/OpenSearch endpoint (e.g., https://my-domain.us-east-1.es.amazonaws.com)",
				Priority:    85,
				IsTestable:  true,
			},
			"kibana_url": {
				Type: core.ToolSchemaTypeString,
				// Rendered as a caption under the field (DynamicForm renders
				// `description` below the input), so it explains why the field
				// exists rather than just restating its name.
				Description: "Base URL of Kibana, e.g. https://my-kibana:5601. " +
					"Needed only to import Kibana alerting rules as event rules — Kibana runs on its own host and port, " +
					"so it cannot be derived from the Elasticsearch URL above. " +
					"The same credentials are reused (Kibana authenticates through Elasticsearch), but the role must also grant " +
					"Kibana privileges — e.g. feature_stackAlerts.read on kibana-.kibana — or rule import fails with 403. " +
					"Leave blank to skip rule import; logs, metrics and traces are unaffected.",
				Default:  "",
				Priority: 84,
			},
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     "basic",
				Enum:        []any{"basic", "cognito", "api_key", "bearer_token"},
				Priority:    90,
				IsTestable:  true,
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Username for basic auth or Cognito User Pool",
				ShowWhen:     map[string]any{"auth_type": []any{"basic", "cognito"}},
				RequiredWhen: map[string]any{"auth_type": []any{"basic", "cognito"}},
				Priority:     80,
				IsTestable:   true,
			},
			"password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Password for basic auth or Cognito User Pool",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": []any{"basic", "cognito"}},
				RequiredWhen: map[string]any{"auth_type": []any{"basic", "cognito"}},
				Priority:     78,
				IsTestable:   true,
			},
			"api_key": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Elasticsearch API key (Base64-encoded id:api_key)",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "api_key"},
				RequiredWhen: map[string]any{"auth_type": "api_key"},
				Priority:     76,
				IsTestable:   true,
			},
			"bearer_token": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth2 or service-account bearer token",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "bearer_token"},
				RequiredWhen: map[string]any{"auth_type": "bearer_token"},
				Priority:     74,
				IsTestable:   true,
			},
			"region": {
				Type:        core.ToolSchemaTypeString,
				Description: "AWS region (e.g., us-east-1)",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
				Priority:    72,
				IsTestable:  true,
			},
			"user_pool_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cognito User Pool ID (e.g., us-east-1_xxxxxx)",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
				Priority:    68,
				IsTestable:  true,
			},
			"identity_pool_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cognito Identity Pool ID (e.g., us-east-1:xxxxx-xxxx-xxx)",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
				Priority:    66,
				IsTestable:  true,
			},
			"app_client_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cognito App Client ID",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
				Priority:    64,
				IsTestable:  true,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Elasticsearch integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Elasticsearch default Logs Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         30,
			},
			"log_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Index pattern or exact name, e.g. logs-* or logs-generic.otel-default.",
				ShowWhen:    map[string]any{core.DefaultLogProvider: true},
				Priority:    29,
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Elasticsearch default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         25,
			},
			"metrics_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Index pattern or exact name, e.g. metrics-* or metrics-kubeletstatsreceiver.otel-default. Leave blank to use metrics-*.",
				ShowWhen:    map[string]any{core.DefaultMetricsProvider: true},
				Priority:    24,
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Elasticsearch default Traces Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         20,
			},
			"trace_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Index pattern or exact name for Data Prepper spans, e.g. otel-v1-apm-span-*. Leave blank to use otel-v1-apm-span-*.",
				ShowWhen:    map[string]any{core.DefaultTraceProvider: true},
				Priority:    19,
			},
			"es_tls_skip_verify": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Skip TLS certificate verification (only for self-signed Elasticsearch/OpenSearch deployments — leaves credentials vulnerable to MITM)",
				Default:     false,
				Priority:    15,
			},
			// Advanced Settings: per-account log/metrics/trace index overrides for one
			// shared ES endpoint. Stored as a JSON array of {account_id, log_index,
			// metrics_index, trace_index}; shape-validated in ValidateConfig. Hidden
			// because the UI renders it via dedicated Per-Account Index cards, not the
			// generic dynamic form.
			"index_account_mapping": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of per-account index overrides",
				Default:     "",
				Hidden:      true,
			},
		},
	}
}

func (m Elasticsearch) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	rawURL := strings.TrimSpace(configMap["url"])
	esURL := normalizeElasticsearchURL(rawURL)

	authType := strings.TrimSpace(configMap["auth_type"])
	if authType == "" {
		authType = "basic"
	}

	var errs []error
	if esURL == "" {
		errs = append(errs, fmt.Errorf("url is required"))
	} else if !strings.HasPrefix(esURL, "http://") && !strings.HasPrefix(esURL, "https://") {
		errs = append(errs, fmt.Errorf("url must start with http:// or https:// (got %q)", esURL))
	} else if hasURLPath(rawURL) {
		errs = append(errs, fmt.Errorf("url must be the base URL only — remove the path after the host (use %q, not %q)", esURL, rawURL))
	}

	switch authType {
	case "basic":
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for basic auth"))
		}
		if configMap["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for basic auth"))
		}
	case "cognito":
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for cognito auth"))
		}
		if configMap["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for cognito auth"))
		}
		if configMap["region"] == "" {
			errs = append(errs, fmt.Errorf("region is required for cognito auth"))
		}
		if configMap["user_pool_id"] == "" {
			errs = append(errs, fmt.Errorf("user_pool_id is required for cognito auth"))
		}
		if configMap["identity_pool_id"] == "" {
			errs = append(errs, fmt.Errorf("identity_pool_id is required for cognito auth"))
		}
		if configMap["app_client_id"] == "" {
			errs = append(errs, fmt.Errorf("app_client_id is required for cognito auth"))
		}
	case "api_key":
		if configMap["api_key"] == "" {
			errs = append(errs, fmt.Errorf("api_key is required for api_key auth"))
		}
	case "bearer_token":
		if configMap["bearer_token"] == "" {
			errs = append(errs, fmt.Errorf("bearer_token is required for bearer_token auth"))
		}
	default:
		errs = append(errs, fmt.Errorf("auth_type must be one of basic, cognito, api_key, bearer_token (got %q)", authType))
	}

	// Advanced Settings: per-account index mapping (optional). Validate only the
	// shape — a JSON array whose entries each carry an account_id. Blank index
	// strings and unknown account ids are tolerated (resolution falls back to the
	// top-level index), mirroring the lenient webhook account_mapping contract.
	if raw := strings.TrimSpace(configMap["index_account_mapping"]); raw != "" {
		var rows []struct {
			AccountId string `json:"account_id"`
		}
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			errs = append(errs, fmt.Errorf("index_account_mapping must be a JSON array: %w", err))
		} else {
			for i, r := range rows {
				if strings.TrimSpace(r.AccountId) == "" {
					errs = append(errs, fmt.Errorf("index_account_mapping[%d] is missing account_id", i))
				}
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}

	// Cognito auth requires AWS SDK flow; skip connection test here
	if authType == "cognito" {
		return nil
	}

	// Build auth header based on auth type (values are already decrypted by the framework)
	var authHeader string
	switch authType {
	case "basic":
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(configMap["username"]+":"+configMap["password"]))
	case "api_key":
		authHeader = "ApiKey " + configMap["api_key"]
	case "bearer_token":
		authHeader = "Bearer " + configMap["bearer_token"]
	}

	httpOpts := []common.HttpOption{
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Accept":        "application/json",
		}),
		common.HttpWithTimeout(15 * time.Second),
	}
	if strings.EqualFold(strings.TrimSpace(configMap["es_tls_skip_verify"]), "true") {
		httpOpts = append(httpOpts, common.HttpWithInsecureSkipVerify())
	}
	resp, err := common.HttpGet(fmt.Sprintf("%s/_cluster/health", esURL), httpOpts...)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to Elasticsearch at %s: %w", esURL, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("invalid Elasticsearch credentials (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for Elasticsearch (HTTP 403)")}
	case http.StatusNotFound:
		return []error{fmt.Errorf("Elasticsearch /_cluster/health not found at %s — check url", esURL)}
	default:
		return []error{fmt.Errorf("Elasticsearch returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}

// normalizeElasticsearchURL trims whitespace and strips any path/query/fragment
// so users can paste the URL they have open in the browser (e.g.
// https://my-domain.us-east-1.es.amazonaws.com/_dashboards/app/home).
func normalizeElasticsearchURL(raw string) string {
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
