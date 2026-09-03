package integrations

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	// Register under "prometheus:user" so a UI-created integration (source="user")
	// resolves to this full schema — URL plus the auth schemes the agent supports —
	// instead of the minimal PrometheusAgent one.
	// No plain RegisterIntegration call: that key ("prometheus") belongs to
	// PrometheusAgent, whose row is created by the k8s agent itself.
	core.RegisterIntegrationWithSource(IntegrationPrometheus, "user", Prometheus{})
}

type Prometheus struct {
}

func (m Prometheus) Name() string {
	return IntegrationPrometheus
}

func (m Prometheus) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMetrics
}

func (m Prometheus) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{PrometheusURLKey, PrometheusAuthTypeKey},
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			PrometheusURLKey: {
				Type: core.ToolSchemaTypeString,
				Description: "Base URL of the Prometheus-compatible endpoint, reachable from Nudgebee " +
					"(e.g. https://prometheus.example.com). Include any path prefix the API is served under — " +
					"Amazon Managed Prometheus uses /workspaces/<workspace-id>, and Thanos or Mimir behind an " +
					"ingress often use /prometheus.",
				Priority:   85,
				IsTestable: true,
			},
			PrometheusAuthTypeKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "How Nudgebee authenticates to this endpoint",
				Default:     PrometheusAuthNone,
				Enum: []any{
					PrometheusAuthNone,
					PrometheusAuthBasic,
					PrometheusAuthBearerToken,
					PrometheusAuthAWSSigV4,
					PrometheusAuthCoralogix,
					PrometheusAuthAzureAD,
				},
				Priority:   90,
				IsTestable: true,
			},
			PrometheusUsernameKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Username for basic authentication",
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthBasic},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthBasic},
				Priority:     80,
				IsTestable:   true,
			},
			PrometheusPasswordKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Password for basic authentication",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthBasic},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthBasic},
				Priority:     78,
				IsTestable:   true,
			},
			PrometheusBearerTokenKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Bearer token sent in the Authorization header (e.g. a Kubernetes service-account token or a Grafana Cloud access policy token)",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthBearerToken},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthBearerToken},
				Priority:     76,
				IsTestable:   true,
			},
			PrometheusAWSAccessKeyIDKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "AWS access key ID used to sign requests to Amazon Managed Prometheus",
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthAWSSigV4},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthAWSSigV4},
				Priority:     74,
				IsTestable:   true,
			},
			PrometheusAWSSecretAccessKeyKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "AWS secret access key used to sign requests to Amazon Managed Prometheus",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthAWSSigV4},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthAWSSigV4},
				Priority:     72,
				IsTestable:   true,
			},
			PrometheusAWSRegionKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "AWS region of the Prometheus workspace (e.g. us-east-1)",
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthAWSSigV4},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthAWSSigV4},
				Priority:     70,
				IsTestable:   true,
			},
			PrometheusAWSServiceNameKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "SigV4 service name. Leave as aps unless signing for a different AWS service.",
				Default:     PrometheusDefaultAWSService,
				ShowWhen:    map[string]any{PrometheusAuthTypeKey: PrometheusAuthAWSSigV4},
				IsTestable:  true,
				Priority:    68,
			},
			PrometheusCoralogixTokenKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Coralogix API token, sent in the token header",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthCoralogix},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthCoralogix},
				Priority:     66,
				IsTestable:   true,
			},
			PrometheusAzureClientIDKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Azure AD application (client) ID used to request an access token",
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthAzureAD},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthAzureAD},
				Priority:     64,
				IsTestable:   true,
			},
			PrometheusAzureClientSecretKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Azure AD client secret for the application above",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthAzureAD},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthAzureAD},
				Priority:     62,
				IsTestable:   true,
			},
			PrometheusAzureTenantIDKey: {
				Type:         core.ToolSchemaTypeString,
				Description:  "Azure AD directory (tenant) ID",
				ShowWhen:     map[string]any{PrometheusAuthTypeKey: PrometheusAuthAzureAD},
				RequiredWhen: map[string]any{PrometheusAuthTypeKey: PrometheusAuthAzureAD},
				Priority:     60,
				IsTestable:   true,
			},
			PrometheusAzureResourceKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "Token audience. Leave as the default unless Azure Monitor tells you otherwise.",
				Default:     PrometheusDefaultAzureResource,
				ShowWhen:    map[string]any{PrometheusAuthTypeKey: PrometheusAuthAzureAD},
				IsTestable:  true,
				Priority:    58,
			},
			// Always visible, and applied under every auth type — the same
			// contract as the agent's PROMETHEUS_HEADERS. Multi-tenant backends
			// need a routing header (X-Scope-OrgID) IN ADDITION TO whatever
			// authenticates the call, so this cannot be an auth_type of its own.
			PrometheusExtraHeadersKey: {
				Type: core.ToolSchemaTypeString,
				Description: "Optional extra headers sent on every request, either a JSON object " +
					"({\"X-Scope-OrgID\": \"tenant-1\"}) or one \"Header: value\" per line. " +
					"Use for multi-tenant routing (e.g. X-Scope-OrgID: tenant-1 for Cortex, Mimir or Thanos) " +
					"or any header the endpoint requires alongside the authentication above.",
				IsEncrypted: true,
				// Testable because the probe sends these headers alongside auth.
				// Without the flag the dynamic form treats a headers-only edit as
				// "nothing testable changed" and sends skip_validation=true, so a
				// malformed value would save unvalidated.
				IsTestable: true,
				Multiline:  true,
				Priority:   40,
			},
			// The k8s agent reports its own cluster label via connection_status,
			// which the relay substitutes into __CLUSTER__. A direct connection has
			// no agent, so a shared multi-cluster backend (Mimir, Thanos, Cortex)
			// needs the label configured here or every query spans all clusters.
			PrometheusAdditionalLabelsKey: {
				Type: core.ToolSchemaTypeString,
				Description: "Optional labels identifying this cluster's series, as a JSON object " +
					"({\"cluster\": \"prod\"}). Required only when one endpoint serves several " +
					"clusters (Mimir, Thanos, Cortex); leave empty for a single-cluster Prometheus.",
				// Testable for the same reason as extra headers: the dynamic form uses
				// is_testable to decide whether an edit re-runs validation, so without
				// it a labels-only edit would save an invalid label name unvalidated.
				IsTestable: true,
				Priority:   35,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Custom name for this Prometheus integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Accounts that should query this Prometheus endpoint",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Prometheus default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         15,
			},
		},
	}
}

// ValidateConfig checks the shape of the settings and then proves them against
// the live endpoint. The probe lives here rather than in a TestConnection method
// so that SAVING is gated too, not just the Test button — the same choice SigNoz
// and Chronosphere made. Values arrive already decrypted (CreateIntegrationConfig
// decrypts before calling this).
func (m Prometheus) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	values := make(map[string]string, len(config))
	for _, c := range config {
		values[c.Name] = c.Value
	}

	rawURL := strings.TrimSpace(values[PrometheusURLKey])
	cfg, err := NewPrometheusUserConfig(values)

	var errs []error
	if rawURL == "" {
		errs = append(errs, fmt.Errorf("%s is required", PrometheusURLKey))
	} else if urlErr := validateEgressURL(rawURL); urlErr != nil {
		errs = append(errs, fmt.Errorf("%s %w", PrometheusURLKey, urlErr))
	}
	if err != nil {
		// NewPrometheusUserConfig parses headers then labels; attribute the failure
		// to whichever field actually failed so the form highlights the right one.
		key := PrometheusExtraHeadersKey
		if _, hErr := ParsePrometheusHeaders(values[PrometheusExtraHeadersKey]); hErr == nil {
			key = PrometheusAdditionalLabelsKey
		}
		errs = append(errs, fmt.Errorf("%s: %w", key, err))
	}
	errs = append(errs, validatePrometheusAuth(cfg)...)
	if len(errs) > 0 {
		return errs
	}

	return probePrometheus(cfg)
}

// validatePrometheusAuth checks that the credentials the selected auth_type needs
// are present.
func validatePrometheusAuth(cfg PrometheusUserConfig) []error {
	var errs []error
	missing := func(key, authType string) {
		errs = append(errs, fmt.Errorf("%s is required for %s auth", key, authType))
	}

	switch cfg.AuthType {
	case PrometheusAuthNone:
	case PrometheusAuthBasic:
		if cfg.Username == "" {
			missing(PrometheusUsernameKey, PrometheusAuthBasic)
		}
		if cfg.Password == "" {
			missing(PrometheusPasswordKey, PrometheusAuthBasic)
		}
	case PrometheusAuthBearerToken:
		if cfg.BearerToken == "" {
			missing(PrometheusBearerTokenKey, PrometheusAuthBearerToken)
		}
	case PrometheusAuthAWSSigV4:
		if cfg.AWSAccessKeyID == "" {
			missing(PrometheusAWSAccessKeyIDKey, PrometheusAuthAWSSigV4)
		}
		if cfg.AWSSecretAccessKey == "" {
			missing(PrometheusAWSSecretAccessKeyKey, PrometheusAuthAWSSigV4)
		}
		if cfg.AWSRegion == "" {
			missing(PrometheusAWSRegionKey, PrometheusAuthAWSSigV4)
		}
	case PrometheusAuthCoralogix:
		if cfg.CoralogixToken == "" {
			missing(PrometheusCoralogixTokenKey, PrometheusAuthCoralogix)
		}
	case PrometheusAuthAzureAD:
		if cfg.AzureClientID == "" {
			missing(PrometheusAzureClientIDKey, PrometheusAuthAzureAD)
		}
		if cfg.AzureClientSecret == "" {
			missing(PrometheusAzureClientSecretKey, PrometheusAuthAzureAD)
		}
		if cfg.AzureTenantID == "" {
			missing(PrometheusAzureTenantIDKey, PrometheusAuthAzureAD)
		}
	default:
		errs = append(errs, fmt.Errorf("%s must be one of none, basic, bearer_token, aws_sigv4, coralogix, azure_ad (got %q)",
			PrometheusAuthTypeKey, cfg.AuthType))
	}
	return errs
}

// probePrometheus performs the cheapest read that proves both reachability and
// credentials: the label-values lookup every Prometheus-compatible engine serves.
func probePrometheus(cfg PrometheusUserConfig) []error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := cfg.DoGet(ctx, "/api/v1/label/__name__/values", neturl.Values{"limit": []string{"1"}})
	if err != nil {
		return []error{fmt.Errorf("failed to connect to Prometheus at %s: %w", cfg.URL, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("Prometheus rejected the credentials (HTTP 401) — check the %s settings", PrometheusAuthTypeKey)}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for this Prometheus endpoint (HTTP 403)")}
	case http.StatusNotFound:
		return []error{fmt.Errorf("Prometheus API not found at %s/api/v1/label/__name__/values — check %s, including any path prefix the API is served under", cfg.URL, PrometheusURLKey)}
	default:
		return []error{fmt.Errorf("Prometheus returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}
