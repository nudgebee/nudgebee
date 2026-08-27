package observability

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeCtx builds a tenant-admin context without a metastore, then layers the config
// override on it — the shape the integrations_list_log_fields handler produces.
func probeCtx(t *testing.T, o core.ConfigOverride) *security.RequestContext {
	t.Helper()
	p := gomonkey.NewPatches()
	p.ApplyFunc(security.GetAccountIdsByTenantId, func(_ string) ([]string, error) {
		return []string{o.AccountId}, nil
	})
	sc := security.NewSecurityContextForTenantAdmin("tenant-1")
	p.Reset()
	require.NotNil(t, sc)
	return core.WithConfigOverride(
		security.NewRequestContext(context.Background(), sc, slog.Default(), nil, nil), o)
}

// esMappingServer stands in for an Elasticsearch cluster, and records which path was
// asked for so the test can assert the index actually used.
func esMappingServer(t *testing.T, gotPath *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
          "logs-app-000001": {"mappings": {"properties": {
            "kubernetes": {"properties": {"pod_name": {"type": "keyword"}}},
            "message": {"type": "text"}
          }}}
        }`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFetchLogLabels_UsesOverriddenConfig is the regression for the reported bug: the
// field list was fetched against the SAVED Elasticsearch URL, so an operator who
// corrected the URL in the form kept getting "no such host" for the old one. Under an
// override the query must reach the supplied endpoint instead.
func TestFetchLogLabels_UsesOverriddenConfig(t *testing.T) {
	var gotPath string
	srv := esMappingServer(t, &gotPath)

	ctx := probeCtx(t, core.ConfigOverride{
		AccountId:       "acc-1",
		IntegrationName: "ES",
		Source:          "user",
		Values: []core.IntegrationConfigValue{
			{Name: "url", Value: srv.URL},
			{Name: "auth_type", Value: "basic"},
			{Name: "username", Value: "elastic"},
			{Name: "password", Value: "probe-secret"},
			{Name: "log_index", Value: "logs-app-*"},
		},
	})

	labels, err := FetchLogLabels(ctx, FetchLogLabelRequest{
		AccountId: "acc-1",
		// Both supplied so provider resolution short-circuits — the same thing the probe
		// handler does, and what keeps this test off the database.
		LogProvider:       "ES",
		LogProviderSource: "user",
		Request:           map[string]any{},
	})

	require.NoError(t, err)
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Label)
	}
	assert.Contains(t, names, "kubernetes.pod_name")
	assert.Contains(t, names, "message")
	assert.Equal(t, "/logs-app-*/_mapping", gotPath, "must read the index configured in the overridden values")
}

// TestFetchLogLabels_OverrideIndexWins covers the per-account index: a card asks for
// the index that account is mapped to, which is not necessarily the integration's
// top-level one. Offering the wrong index's fields is the failure this editor exists
// to prevent.
func TestFetchLogLabels_OverrideIndexWins(t *testing.T) {
	var gotPath string
	srv := esMappingServer(t, &gotPath)

	ctx := probeCtx(t, core.ConfigOverride{
		AccountId:       "acc-1",
		IntegrationName: "ES",
		Source:          "user",
		Values: []core.IntegrationConfigValue{
			{Name: "url", Value: srv.URL},
			{Name: "auth_type", Value: "basic"},
			{Name: "username", Value: "elastic"},
			{Name: "password", Value: "probe-secret"},
			{Name: "log_index", Value: "top-level-*"},
		},
	})

	_, err := FetchLogLabels(ctx, FetchLogLabelRequest{
		AccountId:         "acc-1",
		LogProvider:       "ES",
		LogProviderSource: "user",
		Request:           map[string]any{"index": "per-account-*"},
	})

	require.NoError(t, err)
	assert.Equal(t, "/per-account-*/_mapping", gotPath, "an explicit index must beat the configured default")
}

// TestFetchLogLabels_WithoutOverrideDoesNotUseProbeValues is the negative: absent an
// override, nothing about this path changes and the saved configuration is still what
// gets resolved.
func TestFetchLogLabels_WithoutOverrideDoesNotUseProbeValues(t *testing.T) {
	var gotPath string
	_ = esMappingServer(t, &gotPath)

	p := gomonkey.NewPatches()
	p.ApplyFunc(security.GetAccountIdsByTenantId, func(_ string) ([]string, error) {
		return []string{"acc-1"}, nil
	})
	sc := security.NewSecurityContextForTenantAdmin("tenant-1")
	p.Reset()
	require.NotNil(t, sc)
	ctx := security.NewRequestContext(context.Background(), sc, slog.Default(), nil, nil)

	_, err := FetchLogLabels(ctx, FetchLogLabelRequest{
		AccountId:         "acc-1",
		LogProvider:       "ES",
		LogProviderSource: "user",
		Request:           map[string]any{},
	})

	// No saved ES integration for this account, so it fails to resolve config — the
	// point is that it never reached the probe server.
	assert.Error(t, err)
	assert.Empty(t, gotPath, "without an override the probe endpoint must not be contacted")
}
