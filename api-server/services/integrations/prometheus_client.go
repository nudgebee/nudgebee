package integrations

import (
	"fmt"
	"net/http"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/integrations/promclient"
	"nudgebee/services/security"
)

// The Prometheus client itself lives in integrations/promclient, a leaf package
// with no nudgebee imports: observability/alertrule needs the same authenticated
// client for ruler writes, and it cannot import this package (integrations →
// eventrule → alertrule). What stays here is the piece that needs the
// integration registry — loading the account's row — plus aliases so the
// existing call sites in this package and in observability read unchanged.

const IntegrationPrometheus = "prometheus"

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

// GetPrometheusUserConfigs resolves the user-source Prometheus integration for an
// account and decrypts its secrets.
func GetPrometheusUserConfigs(sc *security.RequestContext, accountId string) (PrometheusUserConfig, error) {
	integrationDtos, err := core.ListIntegrationConfigs(sc, accountId, IntegrationPrometheus)
	if err != nil {
		return PrometheusUserConfig{}, fmt.Errorf("failed to list Prometheus integration configs: %w", err)
	}

	// An account can carry BOTH an agent-source prometheus row (created by the
	// k8s agent, which holds no URL or credentials) and this user-source one,
	// because they share the integration type name. Taking configs[0] would
	// intermittently pick the agent row and fail with an empty URL.
	dto, found := pickPrometheusUserIntegration(integrationDtos)
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
	if cfg.URL == "" {
		return cfg, fmt.Errorf("prometheus integration for account %s has no %s configured", accountId, PrometheusURLKey)
	}
	return cfg, nil
}

// pickPrometheusUserIntegration returns the first user-source row, ignoring
// agent-source rows for the same integration type.
func pickPrometheusUserIntegration(dtos []core.IntegrationDto) (core.IntegrationDto, bool) {
	for _, dto := range dtos {
		if strings.EqualFold(dto.Source, "user") {
			return dto, true
		}
	}
	return core.IntegrationDto{}, false
}
