package integrations

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func TestProviderGettersUseActiveIntegrationConfigs(t *testing.T) {
	lookupErr := errors.New("active integration lookup failed")
	ctx := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	for _, tc := range []struct {
		name     string
		provider string
		get      func(*security.RequestContext) error
	}{
		{"Datadog", IntegrationDatadog, func(ctx *security.RequestContext) error {
			_, _, _, err := GetDatadogConfigs(ctx, "account")
			return err
		}},
		{"NewRelic", IntegrationNewRelic, func(ctx *security.RequestContext) error {
			_, _, _, err := GetNewRelicConfigs(ctx, "account")
			return err
		}},
		{"NewRelicKG", IntegrationNewRelic, func(ctx *security.RequestContext) error { _, err := GetNewRelicKGSource(ctx, "account"); return err }},
		{"CubeAPM", IntegrationCubeAPM, func(ctx *security.RequestContext) error { _, err := GetCubeAPMConfigs(ctx, "account"); return err }},
		{"Dynatrace", IntegrationDynatrace, func(ctx *security.RequestContext) error { _, _, err := GetDynatraceConfigs(ctx, "account"); return err }},
		{"Last9", IntegrationLast9, func(ctx *security.RequestContext) error { _, _, _, err := GetLast9Configs(ctx, "account"); return err }},
		{"AzureAppInsights", IntegrationAzureAppInsights, func(ctx *security.RequestContext) error {
			_, err := GetAzureAppInsightConfigs(ctx, "account")
			return err
		}},
		{"Splunk", IntegrationSplunkO11y, func(ctx *security.RequestContext) error { _, err := GetSplunkO11yConfigs(ctx, "account"); return err }},
		{"SplunkEnterprise", IntegrationSplunkEnterprise, func(ctx *security.RequestContext) error {
			_, err := GetSplunkEnterpriseConfig(ctx, "account")
			return err
		}},
		{"SolarWinds", IntegrationSolarWinds, func(ctx *security.RequestContext) error {
			_, _, err := GetSolarWindsConfigs(ctx, "account")
			return err
		}},
		{"Observe", IntegrationObserve, func(ctx *security.RequestContext) error { _, err := GetObserveConfigs(ctx, "account"); return err }},
		{"Loggly", IntegrationLoggly, func(ctx *security.RequestContext) error { _, err := GetLogglyConfigs(ctx, "account"); return err }},
		{"GrafanaTempo", IntegrationGrafanaTempo, func(ctx *security.RequestContext) error { _, err := GetGrafanaTempoConfig(ctx, "account"); return err }},
		{"Prometheus", IntegrationPrometheus, func(ctx *security.RequestContext) error {
			_, err := GetPrometheusUserConfigs(ctx, "account")
			return err
		}},
		{"OpenObserve", IntegrationOpenObserve, func(ctx *security.RequestContext) error { _, err := GetOpenObserveConfigs(ctx, "account"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			patch := gomonkey.NewPatches()
			defer patch.Reset()
			patch.ApplyFunc(core.ListActiveIntegrationConfigs, func(_ *security.RequestContext, accountID, provider string) ([]core.IntegrationDto, error) {
				calls++
				if accountID != "account" || provider != tc.provider {
					t.Fatalf("lookup scoped to account=%q provider=%q", accountID, provider)
				}
				return nil, lookupErr
			})
			patch.ApplyFunc(core.ListIntegrationConfigs, func(*security.RequestContext, string, string) ([]core.IntegrationDto, error) {
				t.Fatal("runtime provider used listing that includes disabled credentials")
				return nil, nil
			})
			if err := tc.get(ctx); !errors.Is(err, lookupErr) || calls != 1 {
				t.Fatalf("got error=%v active lookups=%d, want propagated active lookup error", err, calls)
			}
		})
	}
}

func TestActivePrometheusConfigKeepsUserSourceSelection(t *testing.T) {
	patch := gomonkey.ApplyFunc(core.ListActiveIntegrationConfigs, func(*security.RequestContext, string, string) ([]core.IntegrationDto, error) {
		return []core.IntegrationDto{
			{Source: "agent", Configs: []core.IntegrationConfigValue{{Name: PrometheusURLKey, Value: "https://agent.example"}}},
			{Source: "user", Configs: []core.IntegrationConfigValue{{Name: PrometheusURLKey, Value: "https://replacement.example"}}},
		}, nil
	})
	defer patch.Reset()
	ctx := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	cfg, err := GetPrometheusUserConfigs(ctx, "account")
	if err != nil || cfg.URL != "https://replacement.example" {
		t.Fatalf("got URL=%q error=%v, want user-source replacement", cfg.URL, err)
	}
}
