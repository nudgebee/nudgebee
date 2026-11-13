package observability

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
		{"Loki", "loki", func(ctx *security.RequestContext) error { _, err := GetLokiConfig(ctx, "account"); return err }},
		{"Signoz", "signoz", func(ctx *security.RequestContext) error {
			_, err := (&SignozSaasSource{}).GetSignozAuth(ctx, "account")
			return err
		}},
		{"Elasticsearch", "ES", func(ctx *security.RequestContext) error { _, err := GetElasticsearchConfig(ctx, "account"); return err }},
		{"Chronosphere", "chronosphere", func(ctx *security.RequestContext) error { _, _, err := GetChronosphereAuth(ctx, "account"); return err }},
		{"Pinot", "pinot", func(ctx *security.RequestContext) error { _, err := GetPinotConfig(ctx, "account"); return err }},
		{"Hive", "hive", func(ctx *security.RequestContext) error { _, err := GetHiveConfig(ctx, "account"); return err }},
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

func TestActiveLokiConfigKeepsUserSourceSelection(t *testing.T) {
	patch := gomonkey.ApplyFunc(core.ListActiveIntegrationConfigs, func(*security.RequestContext, string, string) ([]core.IntegrationDto, error) {
		return []core.IntegrationDto{
			{Source: "agent", Configs: []core.IntegrationConfigValue{{Name: LokiUrl, Value: "https://agent.example"}}},
			{Source: "user", Configs: []core.IntegrationConfigValue{{Name: LokiUrl, Value: "https://replacement.example"}}},
		}, nil
	})
	defer patch.Reset()
	ctx := security.NewRequestContextForTenantAdmin("tenant", slog.Default(), nil, nil)
	cfg, err := GetLokiConfig(ctx, "account")
	if err != nil || cfg.Url != "https://replacement.example" {
		t.Fatalf("got config=%v error=%v, want user-source replacement", cfg, err)
	}
}
