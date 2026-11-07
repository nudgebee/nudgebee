package integrations

import (
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
)

func TestGetOpenObserveConfigs_ActiveSelection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rows      []core.IntegrationDto
		wantURL   string
		wantError string
	}{
		{name: "no active integration", wantError: "not found"},
		{name: "one active integration", rows: []core.IntegrationDto{{Configs: openobserveCfg(validOpenObserveCfg())}}, wantURL: "https://cloud.openobserve.ai"},
		{name: "ambiguous active integrations", rows: []core.IntegrationDto{{Id: "a"}, {Id: "b"}}, wantError: "multiple active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.ApplyFunc(core.ListActiveIntegrationConfigs, func(_ *security.RequestContext, account, kind string) ([]core.IntegrationDto, error) {
				require.Equal(t, "account", account)
				require.Equal(t, IntegrationOpenObserve, kind)
				return tc.rows, nil
			})
			defer patches.Reset()
			cfg, err := GetOpenObserveConfigs(nil, "account")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantURL, cfg.URL)
		})
	}
}
