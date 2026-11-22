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
func TestAzureAppInsights_ValidateConfig_TracesNeedAWorkspaceOrAppID(t *testing.T) {
	tests := []struct {
		name       string
		values     map[string]string
		wantErrFor string
	}{
		{
			name: "logs only, no app id",
			values: map[string]string{
				AppClientID: "client", AppTenantID: "tenant",
				core.DefaultLogProvider: "true", LogsTableName: "ContainerLogV2", LogsWorkbookID: "ws",
			},
		},
		{
			name: "traces enabled, workspace only",
			values: map[string]string{
				AppClientID: "client", AppTenantID: "tenant",
				core.DefaultTraceProvider: "true", TracesTableName: "dependencies", LogsWorkbookID: "ws",
			},
		},
		{
			name: "traces enabled, legacy app id only",
			values: map[string]string{
				AppClientID: "client", AppTenantID: "tenant",
				core.DefaultTraceProvider: "true", TracesTableName: "dependencies", AzureAppInsightsAppID: "app",
			},
		},
		{
			name: "traces enabled, neither",
			values: map[string]string{
				AppClientID: "client", AppTenantID: "tenant",
				core.DefaultTraceProvider: "true", TracesTableName: "dependencies",
			},
			wantErrFor: LogsWorkbookID,
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
			assert.NotContains(t, joined, AzureAppInsightsAppID)
			if tt.wantErrFor != "" {
				assert.Contains(t, joined, tt.wantErrFor+" is required")
			} else {
				assert.NotContains(t, joined, LogsWorkbookID)
			}
		})
	}
}

func TestAppInsightsQueryURL(t *testing.T) {
	_, err := AppInsightsQueryURL(AzureAppInsightsConfig{})
	assert.ErrorContains(t, err, LogsWorkbookID)

	legacy := AzureAppInsightsConfig{AzureAppInsightsAppID: "app", LogsWorkbookID: "ws"}
	u, err := AppInsightsQueryURL(legacy)
	require.NoError(t, err)
	assert.Equal(t, "https://api.applicationinsights.io/v1/apps/app/query", u)
	assert.Equal(t, AzureApplicationInsightURL, AppInsightsTokenResource(legacy))
	assert.Equal(t, "dependencies", AppInsightsTable(legacy, "dependencies"))

	workspace := AzureAppInsightsConfig{LogsWorkbookID: "ws"}
	u, err = AppInsightsQueryURL(workspace)
	require.NoError(t, err)
	assert.Equal(t, "https://api.loganalytics.io/v1/workspaces/ws/query", u)
	assert.Equal(t, AzureAnalyticsURL, AppInsightsTokenResource(workspace))
	assert.Equal(t, "AppDependencies", AppInsightsTable(workspace, "dependencies"))
	assert.Equal(t, "AppMetrics", AppInsightsTable(workspace, "customMetrics"))
	assert.Equal(t, "AppDependencies", AppInsightsTable(workspace, "AppDependencies"), "workspace names pass through")
}
