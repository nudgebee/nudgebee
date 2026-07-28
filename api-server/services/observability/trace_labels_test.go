package observability

import (
	"encoding/json"
	"errors"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mockTraceSource is a minimal TraceSource for testing getMergedTraceLabelMapping
// without touching any external provider. Only GetLabelMapping is meaningful; the
// rest satisfy the interface.
type mockTraceSource struct {
	staticMapping map[string]string
}

func (m *mockTraceSource) GetLabelMapping() map[string]string { return m.staticMapping }
func (m *mockTraceSource) GetSupportedOperators() []string    { return nil }
func (m *mockTraceSource) QueryTraces(_ *security.RequestContext, _ TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return nil, errors.New(errNotImplemented)
}
func (m *mockTraceSource) GetQuery(_ *security.RequestContext, _ TracesV3Request) (string, error) {
	return "", errors.New(errNotImplemented)
}
func (m *mockTraceSource) CountTraces(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{}, errors.New(errNotImplemented)
}
func (m *mockTraceSource) GetLabelValues(_ *security.RequestContext, _ TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	return common.OpenTelemetryTraceLabelValues{}, errors.New(errNotImplemented)
}
func (m *mockTraceSource) QueryGroupedTraces(_ *security.RequestContext, _ TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, errors.New(errNotImplemented)
}
func (m *mockTraceSource) QueryGroupedTracesCount(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{}, errors.New(errNotImplemented)
}
func (m *mockTraceSource) QueryTracesHeatmap(_ *security.RequestContext, _ TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	return nil, errors.New(errNotImplemented)
}
func (m *mockTraceSource) QueryLabels(_ *security.RequestContext, _ FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return []OutputTraceLabel{}, nil
}
func (m *mockTraceSource) QueryRootSpansByTrace(_ *security.RequestContext, _ TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return nil, errors.New(errNotImplemented)
}
func (m *mockTraceSource) CountTracesByTrace(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{}, errors.New(errNotImplemented)
}

// mockDynamicTraceSource extends mockTraceSource with a configurable
// DynamicLabelMappingSource implementation.
type mockDynamicTraceSource struct {
	mockTraceSource
	dynamicMapping map[string]string
}

func (m *mockDynamicTraceSource) GetDynamicLabelMapping(_ *security.RequestContext, _ string) map[string]string {
	return m.dynamicMapping
}

// seedTraceCache writes value into the trace-labels cache namespace and registers
// cleanup. Mirrors seedCache in log_labels_test.go but for nb_trace_labels.
func seedTraceCache(t *testing.T, key string, value map[string]string) {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	err = common.CacheSet(traceLabelsCacheNamespace, key, b)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = common.CacheDelete(traceLabelsCacheNamespace, key)
	})
}

// clearTraceCache removes a cache key before the test runs and re-clears on cleanup.
func clearTraceCache(t *testing.T, key string) {
	t.Helper()
	_ = common.CacheDelete(traceLabelsCacheNamespace, key)
	t.Cleanup(func() {
		_ = common.CacheDelete(traceLabelsCacheNamespace, key)
	})
}

// ---------------------------------------------------------------------------
// getCustomTraceLabels
// ---------------------------------------------------------------------------

func TestGetCustomTraceLabels_EmptyAccountID(t *testing.T) {
	result := getCustomTraceLabels(newLogLabelCtx(), "")
	assert.Empty(t, result)
}

func TestGetCustomTraceLabels_CacheHit(t *testing.T) {
	const key = "trace-unit-cache-hit"
	clearTraceCache(t, key)

	want := map[string]string{"workload_name": "service", "span_name": "operation_name"}
	seedTraceCache(t, key, want)

	// Fail the test if DB is reached — a cache hit must not touch the DB.
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(database.GetDatabaseManager, func(_ database.DatabaseManagerType) (*database.DatabaseManager, error) {
		t.Fatal("DB must not be called on a cache hit")
		return nil, nil
	})
	defer patches.Reset()

	result := getCustomTraceLabels(newLogLabelCtx(), key)
	assert.Equal(t, want, result)
}

func TestGetCustomTraceLabels_DBManagerError(t *testing.T) {
	const key = "trace-unit-db-error"
	clearTraceCache(t, key)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	result := getCustomTraceLabels(newLogLabelCtx(), key)
	assert.Empty(t, result, "should return empty map when DB is unavailable")
}

func TestGetTenantTraceLabels_EmptyTenantID(t *testing.T) {
	result := getTenantTraceLabels(newLogLabelCtx(), "")
	assert.Empty(t, result)
}

// ---------------------------------------------------------------------------
// getMergedTraceLabelMapping
// ---------------------------------------------------------------------------

func TestGetMergedTraceLabelMapping_EmptyCustom(t *testing.T) {
	const key = "trace-merge-no-custom"
	clearTraceCache(t, key)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	staticMap := map[string]string{
		"workload_name":      "service",
		"span_name":          "operation_name",
		"workload_namespace": "kube_namespace",
	}
	result := getMergedTraceLabelMapping(newLogLabelCtx(), key, &mockTraceSource{staticMapping: staticMap})
	assert.Equal(t, staticMap, result)
}

func TestGetMergedTraceLabelMapping_CustomOverridesStatic(t *testing.T) {
	const key = "trace-merge-override"
	clearTraceCache(t, key)

	seedTraceCache(t, key, map[string]string{"workload_name": "custom_service", "span_name": "custom_op"})

	staticMap := map[string]string{
		"workload_name":      "service",        // overridden
		"span_name":          "operation_name", // overridden
		"workload_namespace": "kube_namespace", // preserved
	}
	result := getMergedTraceLabelMapping(newLogLabelCtx(), key, &mockTraceSource{staticMapping: staticMap})

	assert.Equal(t, "custom_service", result["workload_name"], "custom should override static")
	assert.Equal(t, "custom_op", result["span_name"], "custom should override static")
	assert.Equal(t, "kube_namespace", result["workload_namespace"], "non-overridden key should remain")
	assert.Len(t, result, 3)
}

func TestGetMergedTraceLabelMapping_CustomAddsNewKey(t *testing.T) {
	const key = "trace-merge-new-key"
	clearTraceCache(t, key)

	seedTraceCache(t, key, map[string]string{"resource": "resource_name"}) // not in static

	staticMap := map[string]string{"workload_name": "service"}
	result := getMergedTraceLabelMapping(newLogLabelCtx(), key, &mockTraceSource{staticMapping: staticMap})

	assert.Equal(t, "resource_name", result["resource"], "new key from custom should appear")
	assert.Equal(t, "service", result["workload_name"], "existing static key should remain")
	assert.Len(t, result, 2)
}

func TestGetMergedTraceLabelMapping_DynamicOverridesAccount(t *testing.T) {
	const key = "trace-merge-dynamic-vs-account"
	clearTraceCache(t, key)

	seedTraceCache(t, key, map[string]string{"workload_name": "account_service", "span_name": "account_op"})

	source := &mockDynamicTraceSource{
		mockTraceSource: mockTraceSource{staticMapping: map[string]string{
			"workload_name": "static_service",
			"resource":      "static_resource",
		}},
		dynamicMapping: map[string]string{"workload_name": "dynamic_service"},
	}
	result := getMergedTraceLabelMapping(newLogLabelCtx(), key, source)

	assert.Equal(t, "dynamic_service", result["workload_name"], "dynamic should win over account")
	assert.Equal(t, "account_op", result["span_name"], "account-only key should remain")
	assert.Equal(t, "static_resource", result["resource"], "static-only key should remain")
}

func TestGetMergedTraceLabelMapping_DynamicEmpty_FallsBackToStatic(t *testing.T) {
	const key = "trace-merge-dynamic-empty"
	clearTraceCache(t, key)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	staticMap := map[string]string{"workload_name": "service"}
	source := &mockDynamicTraceSource{
		mockTraceSource: mockTraceSource{staticMapping: staticMap},
		dynamicMapping:  map[string]string{},
	}
	result := getMergedTraceLabelMapping(newLogLabelCtx(), key, source)
	assert.Equal(t, staticMap, result)
}

// ---------------------------------------------------------------------------
// FetchTraceLabels
// ---------------------------------------------------------------------------

func TestFetchTraceLabels_EmptyAccountID(t *testing.T) {
	_, err := FetchTraceLabels(newLogLabelCtx(), FetchTraceLabelRequest{})
	assert.Error(t, err)
}

// TestBuildTraceLabels_UnionsCanonicalAndMerged verifies the pure union that
// FetchTraceLabels relies on: canonical fields (canonical-first, each once, carrying
// their type in attributes) unioned with merged-mapping keys, with a merged key that
// duplicates a canonical field not doubling up.
func TestBuildTraceLabels_UnionsCanonicalAndMerged(t *testing.T) {
	merged := map[string]string{
		"custom_attr":  "backend_attr", // brand-new key
		"service_name": "svc",          // duplicates a canonical field — must not double up
	}

	labels := buildTraceLabels(merged, nil)

	byLabel := make(map[string]OutputTraceLabel)
	for _, l := range labels {
		byLabel[l.Label] = l
	}

	// All canonical fields present, exactly once, with their declared type in attributes.
	for _, f := range canonicalTraceFields {
		l, ok := byLabel[f.name]
		assert.True(t, ok, "canonical field %q should be present", f.name)
		assert.NotNil(t, l.Attributes, "attributes must default to an empty object, never nil")
		assert.Equal(t, f.typ, l.Attributes["type"], "canonical field %q should carry its type", f.name)
	}
	// Merged-only key present with empty (typeless) attributes.
	assert.NotNil(t, byLabel["custom_attr"].Attributes, "override key attributes must be {}, not nil")
	assert.Empty(t, byLabel["custom_attr"].Attributes, "override key should have no type attribute")
	// No duplicate for the colliding name, canonical type retained.
	assert.Equal(t, "string", byLabel["service_name"].Attributes["type"], "canonical type must win over override")
	assert.Len(t, labels, len(canonicalTraceFields)+1)

	// Canonical fields come first, in declared order.
	for i, f := range canonicalTraceFields {
		assert.Equal(t, f.name, labels[i].Label, "canonical fields should lead in order")
	}
}

func TestBuildTraceLabels_EmptyMerged(t *testing.T) {
	labels := buildTraceLabels(map[string]string{}, nil)
	assert.Len(t, labels, len(canonicalTraceFields))
}

// TestBuildTraceLabels_IncludesDiscoveredKeys verifies backend-discovered keys are
// unioned in with empty attributes, deduped against canonical fields and mapping keys.
func TestBuildTraceLabels_IncludesDiscoveredKeys(t *testing.T) {
	merged := map[string]string{"custom_attr": "backend_attr"}
	discovered := []OutputTraceLabel{
		{Label: "http.method", Attributes: map[string]any{}},  // brand-new discovered key
		{Label: "service_name", Attributes: map[string]any{}}, // collides with canonical — keep canonical type
		{Label: "custom_attr", Attributes: map[string]any{}},  // collides with mapping key — no double up
		{Label: "http.method", Attributes: map[string]any{}},  // duplicate discovered key — no double up
	}

	labels := buildTraceLabels(merged, discovered)

	byLabel := make(map[string]OutputTraceLabel)
	for _, l := range labels {
		byLabel[l.Label] = l
	}

	httpMethod, ok := byLabel["http.method"]
	assert.True(t, ok, "discovered key should be present")
	assert.Empty(t, httpMethod.Attributes, "discovered key should have no type")
	assert.Equal(t, "string", byLabel["service_name"].Attributes["type"], "canonical type retained over discovered collision")
	// canonical set + custom_attr + http.method, each once.
	assert.Len(t, labels, len(canonicalTraceFields)+2)
}

// ---------------------------------------------------------------------------
// getProviderCapabilities — traces branch
// ---------------------------------------------------------------------------

// TestGetProviderCapabilities_TracesStaticWhenNoOverride verifies the traces branch
// returns the provider's static label mapping when there are no account/tenant overrides.
func TestGetProviderCapabilities_TracesStaticWhenNoOverride(t *testing.T) {
	const account = "trace-caps-no-override"
	clearTraceCache(t, account)

	patches := mockDBUnavailable(t)
	defer patches.Reset()

	staticMap := (&DatadogTraceSource{}).GetLabelMapping()
	caps := getProviderCapabilities(newLogLabelCtx(), account, "datadog", "user", "traces")

	assert.Equal(t, staticMap, caps.LabelMappings, "no overrides → static datadog map")
	assert.NotEmpty(t, caps.SupportedOperators)
}

// TestGetProviderCapabilities_TracesMergesAccountOverride verifies an account-level
// trace_labels override is merged into the traces-branch capabilities.
func TestGetProviderCapabilities_TracesMergesAccountOverride(t *testing.T) {
	const account = "trace-caps-override"
	clearTraceCache(t, account)
	seedTraceCache(t, account, map[string]string{"custom_attr": "backend_attr"})

	caps := getProviderCapabilities(newLogLabelCtx(), account, "datadog", "user", "traces")

	assert.Equal(t, "backend_attr", caps.LabelMappings["custom_attr"], "account override should be merged")
	// A datadog static key must still be present.
	assert.Contains(t, caps.LabelMappings, "workload_name", "static datadog keys should remain")
}
