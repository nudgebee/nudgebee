package integrations

import (
	"fmt"
	"net/http"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/integrations/promclient"
	"nudgebee/services/integrations/promforager"
	"nudgebee/services/security"
)

// The Prometheus client itself lives in integrations/promclient, a leaf package
// with no nudgebee imports: observability/alertrule needs the same authenticated
// client for ruler writes, and it cannot import this package (integrations →
// eventrule → alertrule). What stays here is the piece that needs the
// integration registry — loading the account's row — plus aliases so the
// existing call sites in this package and in observability read unchanged.

const IntegrationPrometheus = "prometheus"

// connection_mode selects how Nudgebee reaches the endpoint. "direct" (the
// default, and what every row saved before this field existed means) dials
// prometheus_url from the server. "vm_agent" sends queries through a forager
// inside the customer's network, for a store the server cannot reach.
const (
	PrometheusConnectionModeKey     = promclient.PrometheusConnectionModeKey
	PrometheusConnectionModeDirect  = promclient.PrometheusConnectionModeDirect
	PrometheusConnectionModeVMAgent = promclient.PrometheusConnectionModeVMAgent
)

type PrometheusUserConfig = promclient.PrometheusUserConfig

const (
	PrometheusURLKey              = promclient.PrometheusURLKey
	PrometheusAuthTypeKey         = promclient.PrometheusAuthTypeKey
	PrometheusUsernameKey         = promclient.PrometheusUsernameKey
	PrometheusPasswordKey         = promclient.PrometheusPasswordKey
	PrometheusBearerTokenKey      = promclient.PrometheusBearerTokenKey
	PrometheusExtraHeadersKey     = promclient.PrometheusExtraHeadersKey
	PrometheusAdditionalLabelsKey = promclient.PrometheusAdditionalLabelsKey

	PrometheusAWSAccessKeyIDKey     = promclient.PrometheusAWSAccessKeyIDKey
	PrometheusAWSSecretAccessKeyKey = promclient.PrometheusAWSSecretAccessKeyKey
	PrometheusAWSRegionKey          = promclient.PrometheusAWSRegionKey
	PrometheusAWSServiceNameKey     = promclient.PrometheusAWSServiceNameKey

	PrometheusCoralogixTokenKey = promclient.PrometheusCoralogixTokenKey

	PrometheusAzureClientIDKey     = promclient.PrometheusAzureClientIDKey
	PrometheusAzureClientSecretKey = promclient.PrometheusAzureClientSecretKey
	PrometheusAzureTenantIDKey     = promclient.PrometheusAzureTenantIDKey
	PrometheusAzureResourceKey     = promclient.PrometheusAzureResourceKey

	PrometheusRulerTypeKey = promclient.PrometheusRulerTypeKey
	PrometheusRulerURLKey  = promclient.PrometheusRulerURLKey
)

const (
	PrometheusAuthNone        = promclient.PrometheusAuthNone
	PrometheusAuthBasic       = promclient.PrometheusAuthBasic
	PrometheusAuthBearerToken = promclient.PrometheusAuthBearerToken
	PrometheusAuthAWSSigV4    = promclient.PrometheusAuthAWSSigV4
	PrometheusAuthCoralogix   = promclient.PrometheusAuthCoralogix
	PrometheusAuthAzureAD     = promclient.PrometheusAuthAzureAD

	PrometheusRulerNone        = promclient.PrometheusRulerNone
	PrometheusRulerMimirCortex = promclient.PrometheusRulerMimirCortex

	PrometheusDefaultAWSService    = promclient.PrometheusDefaultAWSService
	PrometheusDefaultAzureResource = promclient.PrometheusDefaultAzureResource
)

func NewPrometheusUserConfig(values map[string]string) (PrometheusUserConfig, error) {
	return promclient.NewPrometheusUserConfig(values)
}

func ParsePrometheusHeaders(raw string) (http.Header, error) {
	return promclient.ParsePrometheusHeaders(raw)
}

func ParsePrometheusAdditionalLabels(raw string) (string, error) {
	return promclient.ParsePrometheusAdditionalLabels(raw)
}

func ExpandClusterPlaceholder(query, additionalLabels string) string {
	return promclient.ExpandClusterPlaceholder(query, additionalLabels)
}

// GetPrometheusUserConfigs resolves the Prometheus integration a server-side client
// connects to for an account (user-source, or a datasource its forager registered) and
// decrypts its secrets.
func GetPrometheusUserConfigs(sc *security.RequestContext, accountId string) (PrometheusUserConfig, error) {
	integrationDtos, err := core.ListIntegrationConfigs(sc, accountId, IntegrationPrometheus)
	if err != nil {
		return PrometheusUserConfig{}, fmt.Errorf("failed to list Prometheus integration configs: %w", err)
	}

	// An account can carry BOTH an agent-source prometheus row (created by the
	// k8s agent, which holds no URL or credentials) and this user-source one,
	// because they share the integration type name. Taking configs[0] would
	// intermittently pick the agent row and fail with an empty URL. A datasource
	// the account's forager registered is also usable: it is reached through the
	// forager, which holds its URL.
	dto, found, err := core.PickServerReachableIntegration(sc, accountId, core.DefaultMetricsProvider, integrationDtos)
	if err != nil {
		return PrometheusUserConfig{}, fmt.Errorf("failed to pick Prometheus integration: %w", err)
	}
	if !found {
		return PrometheusUserConfig{}, fmt.Errorf("no user-configured prometheus integration found for account: %s", accountId)
	}

	values := make(map[string]string, len(dto.Configs))
	for _, config := range dto.Configs {
		value := config.Value
		if config.IsEncrypted && value != "" {
			decrypted, derr := common.Decrypt(value)
			if derr != nil {
				return PrometheusUserConfig{}, fmt.Errorf("failed to decrypt prometheus config %s: %w", config.Name, derr)
			}
			value = decrypted
		}
		values[config.Name] = value
	}

	cfg, err := NewPrometheusUserConfig(values)
	if err != nil {
		return cfg, err
	}
	// Through a forager the URL is the forager's own: a datasource it registered
	// from its config has none on the server side, and the transport never dials it.
	if cfg.URL == "" && !cfg.ViaForager {
		return cfg, fmt.Errorf("prometheus integration for account %s has no %s configured", accountId, PrometheusURLKey)
	}
	if values[PrometheusConnectionModeKey] == PrometheusConnectionModeVMAgent {
		// The datasource key is the integration id unless the forager defines
		// the datasource locally and the row carries a datasource_key.
		datasourceKey := values["datasource_key"]
		if datasourceKey == "" {
			datasourceKey = dto.Id
		}
		cfg.ProxyTransport = promforager.NewTransport(accountId, datasourceKey, values["agent_type"])
	}
	return cfg, nil
}
