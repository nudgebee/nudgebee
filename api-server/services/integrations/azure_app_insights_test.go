package integrations

import (
	"nudgebee/services/integrations/core"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func azureConfigValues(kv map[string]string) []core.IntegrationConfigValue {
	out := make([]core.IntegrationConfigValue, 0, len(kv))
	for k, v := range kv {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

func TestAzureAppInsights_AppIDNotGloballyRequired(t *testing.T) {
	assert.NotContains(t, AzureAppInsights{}.ConfigSchema().Required, AzureAppInsightsAppID)
}

// The required-field checks return before any network call, so leaving one
// credential blank keeps these tests offline while still exercising the rules.
func TestAzureAppInsights_ValidateConfig_AppIDOnlyRequiredForTraces(t *testing.T) {
	tests := []struct {
		name        string
		values      map[string]string
		wantAppIDEr bool
	}{
		{
			name: "logs only, no app id",
			values: map[string]string{
				AppClientID: "client", AppTenantID: "tenant",
				core.DefaultLogProvider: "true", LogsTableName: "ContainerLogV2", LogsWorkbookID: "ws",
			},
			wantAppIDEr: false,
		},
		{
			name: "traces enabled, no app id",
			values: map[string]string{
				AppClientID: "client", AppTenantID: "tenant",
				core.DefaultTraceProvider: "true", TracesTableName: "dependencies",
			},
			wantAppIDEr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := AzureAppInsights{}.ValidateConfig(nil, azureConfigValues(tt.values), "")
			require.NotEmpty(t, errs, "app_secret is blank, so validation must stop at required fields")
			joined := ""
			for _, e := range errs {
				joined += e.Error() + "\n"
			}
			assert.Contains(t, joined, requiredFieldErr(AppSecret).Error())
			if tt.wantAppIDEr {
				assert.Contains(t, joined, AzureAppInsightsAppID)
			} else {
				assert.NotContains(t, joined, AzureAppInsightsAppID)
			}
		})
	}
}

func TestAppInsightsQueryURL(t *testing.T) {
	_, err := AppInsightsQueryURL(AzureAppInsightsConfig{})
	assert.ErrorContains(t, err, AzureAppInsightsAppID)

	u, err := AppInsightsQueryURL(AzureAppInsightsConfig{AzureAppInsightsAppID: "app"})
	require.NoError(t, err)
	assert.Equal(t, "https://api.applicationinsights.io/v1/apps/app/query", u)
}
