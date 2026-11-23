package integrations

import (
	"fmt"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/integrations/orionclient"
	"nudgebee/services/integrations/promforager"
	"nudgebee/services/security"
)

// GetOrionConfig resolves the Orion integration a server-side client connects to
// for an account and decrypts its secrets.
//
// It mirrors GetPrometheusUserConfigs: an account can hold more than one candidate
// row, and a datasource the account's forager registered is usable too, because the
// forager holds the URL and dials it from inside the customer's network.
func GetOrionConfig(sc *security.RequestContext, accountId string) (orionclient.Config, error) {
	integrationDtos, err := core.ListIntegrationConfigs(sc, accountId, IntegrationOrion)
	if err != nil {
		return orionclient.Config{}, fmt.Errorf("failed to list Orion integration configs: %w", err)
	}

	dto, found, err := core.PickServerReachableIntegration(sc, accountId, core.DefaultMetricsProvider, integrationDtos)
	if err != nil {
		return orionclient.Config{}, fmt.Errorf("failed to pick Orion integration: %w", err)
	}
	if !found {
		return orionclient.Config{}, fmt.Errorf("no Orion integration found for account: %s", accountId)
	}

	values := make(map[string]string, len(dto.Configs))
	for _, config := range dto.Configs {
		value := config.Value
		if config.IsEncrypted && value != "" {
			decrypted, derr := common.Decrypt(value)
			if derr != nil {
				return orionclient.Config{}, fmt.Errorf("failed to decrypt Orion config %s: %w", config.Name, derr)
			}
			value = decrypted
		}
		values[config.Name] = value
	}

	cfg := NewOrionConfig(values)
	// Through a forager the URL is the forager's own, so an empty one is only a
	// problem for a server that Nudgebee dials itself.
	if cfg.BaseURL == "" && !cfg.ViaForager {
		return cfg, fmt.Errorf("Orion integration for account %s has no %s configured", accountId, OrionURLKey)
	}
	if cfg.ViaForager {
		// The datasource key is the integration id unless the forager defines the
		// datasource locally and the row carries a datasource_key.
		datasourceKey := values[OrionDatasourceKey]
		if datasourceKey == "" {
			datasourceKey = dto.Id
		}
		transport := promforager.NewTransport(accountId, datasourceKey, values["agent_type"])
		cfg.ProxyTransport = orionclient.ProxyTransport(transport)
	}
	return cfg, nil
}
