package services_server

import (
	"encoding/json"
	"errors"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grantContext is a request context in tenant t (accounts acct-a, acct-b)
// whose principal holds only the given grants on acct-a.
func grantContext(t *testing.T, grants map[string]bool) *security.RequestContext {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"TenantId": "t", "UserId": "u", "AccountIds": []string{"acct-a", "acct-b"},
		"ScopedCustomPermissions": map[string]any{"acct-a": grants},
	})
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	return security.NewRequestContext(t.Context(), &sc, nil, nil, nil)
}

func stubProviderForUse(t *testing.T, provider ObservabilityProvider, err error) *int {
	t.Helper()
	calls := 0
	orig := resolveProviderForUse
	t.Cleanup(func() { resolveProviderForUse = orig })
	resolveProviderForUse = func(string, string, string) (ObservabilityProvider, error) {
		calls++
		return provider, err
	}
	return &calls
}

func TestObservabilityUseIsCheckedOnlyInAGrantScopedRun(t *testing.T) {
	calls := stubProviderForUse(t, ObservabilityProvider{Provider: "datadog", IntegrationSource: "user"}, nil)
	person := grantContext(t, nil)
	assert.NoError(t, checkObservabilityUse(*person, "acct-a", "logs", "", ""))
	assert.Zero(t, *calls, "no lookup outside a grant-scoped run")
}

// The same module services-server's query actions check: logs, metrics or
// traces Read on the account.
func TestObservabilityUseFromTheAgentsOwnBackendNeedsTheDataModule(t *testing.T) {
	stubProviderForUse(t, ObservabilityProvider{Provider: "loki", IntegrationSource: "agent"}, nil)
	bee := security.WithGrantScopedRun(grantContext(t, map[string]bool{"logs:Read": true}))
	assert.NoError(t, checkObservabilityUse(*bee, "acct-a", "logs", "", ""))
	err := checkObservabilityUse(*bee, "acct-b", "logs", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logs:Read on account acct-b")
	err = checkObservabilityUse(*bee, "acct-a", "metrics", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics:Read on account acct-a")

	k8sOnly := security.WithGrantScopedRun(grantContext(t, map[string]bool{"k8s:Read": true}))
	assert.ErrorContains(t, checkObservabilityUse(*k8sOnly, "acct-a", "logs", "", ""), "logs:Read", "a cluster grant is not a logs grant")
}

func TestObservabilityUseFromAnIntegrationNeedsItsTypesGrant(t *testing.T) {
	// A typed tool names its provider; the integration that answers decides.
	calls := stubProviderForUse(t, ObservabilityProvider{Provider: "signoz", IntegrationSource: "user"}, nil)
	bee := security.WithGrantScopedRun(grantContext(t, map[string]bool{"logs:Read": true, "traces:Read": true}))
	err := checkObservabilityUse(*bee, "acct-a", "logs", "signoz", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrations.signoz:Read on account acct-a")
	assert.Equal(t, 1, *calls)

	bee = security.WithGrantScopedRun(grantContext(t, map[string]bool{"logs:Read": true, "traces:Read": true, "integrations.signoz:Read": true}))
	assert.NoError(t, checkObservabilityUse(*bee, "acct-a", "logs", "signoz", ""))
	assert.ErrorContains(t, checkObservabilityUse(*security.WithGrantScopedRun(grantContext(t, map[string]bool{"integrations.signoz:Read": true})), "acct-a", "logs", "signoz", ""),
		"logs:Read", "an integration grant alone does not read logs")

	// A caller that already resolved the source is not asked again.
	*calls = 0
	err = checkObservabilityUse(*bee, "acct-a", "traces", "datadog", "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrations.datadog:Read")
	assert.Zero(t, *calls)
}

func TestObservabilityUseFromCloudMonitoringNeedsOnlyTheDataModule(t *testing.T) {
	stubProviderForUse(t, ObservabilityProvider{Provider: "gcp", IntegrationSource: "user"}, nil)
	bee := security.WithGrantScopedRun(grantContext(t, map[string]bool{"k8s:Read": true}))
	assert.ErrorContains(t, checkObservabilityUse(*bee, "acct-a", "traces", "", ""), "traces:Read on account acct-a")
	bee = security.WithGrantScopedRun(grantContext(t, map[string]bool{"traces:Read": true}))
	assert.NoError(t, checkObservabilityUse(*bee, "acct-a", "traces", "", ""), "no integration behind a cloud provider's monitoring")
}

func TestObservabilityUseIsRefusedWhenTheProviderCannotBeResolved(t *testing.T) {
	stubProviderForUse(t, ObservabilityProvider{}, errors.New("services-server down"))
	bee := security.WithGrantScopedRun(grantContext(t, map[string]bool{"k8s:Read": true}))
	err := checkObservabilityUse(*bee, "acct-a", "metrics", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be resolved")
}

// Series and label names come from the same source as the data, and are held
// to the same grant.
func TestObservabilityMetadataIsHeldToTheSameGrant(t *testing.T) {
	stubProviderForUse(t, ObservabilityProvider{Provider: "datadog", IntegrationSource: "user"}, nil)
	bee := security.WithGrantScopedRun(grantContext(t, map[string]bool{"logs:Read": true, "metrics:Read": true, "traces:Read": true}))
	_, err := ListMetricsSeries(*bee, "acct-a", "datadog", "")
	assert.ErrorContains(t, err, "integrations.datadog:Read")
	_, err = ListMetricsSeriesLabels(*bee, "acct-a", "datadog", "up")
	assert.ErrorContains(t, err, "integrations.datadog:Read")
	_, err = ListMetricsSeriesLabelValues(*bee, "acct-a", "datadog", "pod")
	assert.ErrorContains(t, err, "integrations.datadog:Read")
	_, err = QueryLogLabels(*bee, "acct-a", ObservabilityProvider{Provider: "datadog", IntegrationSource: "user"})
	assert.ErrorContains(t, err, "integrations.datadog:Read")
	_, err = QueryTraceLabels(*bee, "acct-a", ObservabilityProvider{Provider: "datadog", IntegrationSource: "user"})
	assert.ErrorContains(t, err, "integrations.datadog:Read")
}
