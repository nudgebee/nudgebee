package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInjectSharedLogConfigProperties_AllowsLogLabelMappings is the test that fails
// if the auto-allow injection is ever dropped. Without it CreateIntegrationConfig
// rejects the key ("not found in schema") and every save from the Advanced Settings
// mapping editor 400s — a failure that looks like a frontend bug.
func TestInjectSharedLogConfigProperties_AllowsLogLabelMappings(t *testing.T) {
	for _, category := range []IntegrationCategory{IntegrationCategoryLog, IntegrationCategoryObservabilityPlatform} {
		t.Run(string(category), func(t *testing.T) {
			schema := injectSharedLogConfigProperties(IntegrationSchema{
				Type:       ToolSchemaTypeObject,
				Properties: map[string]IntegrationSchemaProperty{"url": {Type: ToolSchemaTypeString}},
			}, category)

			require.Contains(t, schema.Properties, LogLabelMappingsConfigName)
			assert.True(t, schema.Properties[LogLabelMappingsConfigName].Hidden,
				"the blob is rendered by dedicated cards, never by the generic dynamic form")
			assert.Contains(t, schema.Properties, "default_filters")
			assert.Contains(t, schema.Properties, "url", "the integration's own fields must survive")
		})
	}
}

// TestInjectSharedLogConfigProperties_OtherCategoriesUntouched keeps the capability
// scoped: a ticketing or messaging integration has no log query path, so accepting a
// log mapping there would only store config nothing reads.
func TestInjectSharedLogConfigProperties_OtherCategoriesUntouched(t *testing.T) {
	schema := injectSharedLogConfigProperties(IntegrationSchema{
		Properties: map[string]IntegrationSchemaProperty{"url": {Type: ToolSchemaTypeString}},
	}, IntegrationCategoryTicketing)

	assert.NotContains(t, schema.Properties, LogLabelMappingsConfigName)
	assert.NotContains(t, schema.Properties, "default_filters")
	assert.NotContains(t, schema.Properties, DefaultTraceFiltersConfigName)
}

// TestInjectSharedLogConfigProperties_AllowsDefaultTraceFilters is the trace twin of
// the log-label-mappings test above, and exists for the same reason: without the
// injection CreateIntegrationConfig rejects `default_trace_filters` with "not found
// in schema", so saving a Default Trace Filters card 400s and reads as a frontend
// bug. Both categories are covered because getTraceSource serves trace providers
// from each — otel_clickhouse and ES are IntegrationCategoryLog, while datadog,
// dynatrace, jaeger and the rest are IntegrationCategoryObservabilityPlatform.
func TestInjectSharedLogConfigProperties_AllowsDefaultTraceFilters(t *testing.T) {
	for _, category := range []IntegrationCategory{IntegrationCategoryLog, IntegrationCategoryObservabilityPlatform} {
		t.Run(string(category), func(t *testing.T) {
			schema := injectSharedLogConfigProperties(IntegrationSchema{
				Type:       ToolSchemaTypeObject,
				Properties: map[string]IntegrationSchemaProperty{"url": {Type: ToolSchemaTypeString}},
			}, category)

			require.Contains(t, schema.Properties, DefaultTraceFiltersConfigName)
			assert.True(t, schema.Properties[DefaultTraceFiltersConfigName].Hidden,
				"the blob is rendered by a dedicated card, never by the generic dynamic form")
		})
	}
}

// The trace filters must be their OWN key: one integration record commonly serves
// both logs and traces (datadog, dynatrace, chronosphere, ES), so sharing
// `default_filters` would apply an operator's log filters to trace queries.
func TestDefaultTraceFiltersConfigNameIsDistinctFromLogs(t *testing.T) {
	assert.NotEqual(t, "default_filters", DefaultTraceFiltersConfigName)
	assert.Equal(t, "default_trace_filters", DefaultTraceFiltersConfigName)
}

// TestInjectSharedLogConfigProperties_DoesNotMutateInput pins the clone. ConfigSchema()
// implementations commonly return a shared/static map; mutating it would leak the
// injection into every integration built from it, including categories that must not
// accept these keys.
func TestInjectSharedLogConfigProperties_DoesNotMutateInput(t *testing.T) {
	original := map[string]IntegrationSchemaProperty{"url": {Type: ToolSchemaTypeString}}
	injectSharedLogConfigProperties(IntegrationSchema{Properties: original}, IntegrationCategoryLog)

	assert.Len(t, original, 1, "the caller's Properties map must be untouched")
	assert.NotContains(t, original, LogLabelMappingsConfigName)
}

// TestInjectSharedLogConfigProperties_RespectsOwnDeclaration lets an integration that
// declares the key itself keep its own definition.
func TestInjectSharedLogConfigProperties_RespectsOwnDeclaration(t *testing.T) {
	schema := injectSharedLogConfigProperties(IntegrationSchema{
		Properties: map[string]IntegrationSchemaProperty{
			LogLabelMappingsConfigName: {Type: ToolSchemaTypeString, Description: "custom"},
		},
	}, IntegrationCategoryLog)

	assert.Equal(t, "custom", schema.Properties[LogLabelMappingsConfigName].Description)
}

// TestInjectSharedLogConfigProperties_AllowsTraceLabelMappings is the trace twin of
// TestInjectSharedLogConfigProperties_AllowsLogLabelMappings, and fails for the same
// reason: without the injection CreateIntegrationConfig rejects `trace_label_mappings`
// with "not found in schema", so saving a Trace Label Mapping card 400s.
func TestInjectSharedLogConfigProperties_AllowsTraceLabelMappings(t *testing.T) {
	for _, category := range []IntegrationCategory{IntegrationCategoryLog, IntegrationCategoryObservabilityPlatform} {
		t.Run(string(category), func(t *testing.T) {
			schema := injectSharedLogConfigProperties(IntegrationSchema{
				Type:       ToolSchemaTypeObject,
				Properties: map[string]IntegrationSchemaProperty{"url": {Type: ToolSchemaTypeString}},
			}, category)

			require.Contains(t, schema.Properties, TraceLabelMappingsConfigName)
			assert.True(t, schema.Properties[TraceLabelMappingsConfigName].Hidden,
				"the blob is rendered by dedicated cards, never by the generic dynamic form")
		})
	}
}

// The trace mapping must be its OWN key, for the same reason default_trace_filters is:
// signoz, datadog, dynatrace, chronosphere and ES are each one integration record
// serving both signals, so sharing `log_label_mappings` would apply an operator's log
// field mapping to their trace queries.
func TestTraceLabelMappingsConfigNameIsDistinctFromLogs(t *testing.T) {
	assert.NotEqual(t, LogLabelMappingsConfigName, TraceLabelMappingsConfigName)
	assert.Equal(t, "trace_label_mappings", TraceLabelMappingsConfigName)
}

// ---------------------------------------------------------------------------
// validateLabelMappings
//
// Run against BOTH config names: the two blobs are the same shape and share one
// implementation, and this is what keeps the trace contract from drifting away from
// the log one it was copied from.
// ---------------------------------------------------------------------------

func TestValidateLabelMappings(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "absent", value: ""},
		{name: "blank", value: "   "},
		{name: "empty array", value: `[]`},
		{
			name:  "well formed",
			value: `[{"accountId":"acc-1","mappings":{"pod":"kubernetes.pod_name.keyword"}}]`,
		},
		{
			// Lenient on purpose: an unknown account or a blank field name contributes
			// nothing at resolution time, so rejecting them would turn a harmless typo
			// into a failed save.
			name:  "blank mapping values are tolerated",
			value: `[{"accountId":"acc-1","mappings":{"pod":""}}]`,
		},
		{
			name:    "not an array",
			value:   `{"accountId":"acc-1"}`,
			wantErr: "must be a JSON array",
		},
		{
			name:    "malformed json",
			value:   `[{"accountId":`,
			wantErr: "must be a JSON array",
		},
		{
			name:    "entry without accountId",
			value:   `[{"mappings":{"pod":"p"}}]`,
			wantErr: "is missing accountId",
		},
		{
			name:    "entry with blank accountId",
			value:   `[{"accountId":"  ","mappings":{"pod":"p"}}]`,
			wantErr: "is missing accountId",
		},
	}

	for _, configName := range []string{LogLabelMappingsConfigName, TraceLabelMappingsConfigName} {
		for _, tc := range cases {
			t.Run(configName+"/"+tc.name, func(t *testing.T) {
				err := validateLabelMappings([]IntegrationConfigValue{
					{Name: "url", Value: "http://es:9200"},
					{Name: configName, Value: tc.value},
				}, configName)
				if tc.wantErr == "" {
					assert.NoError(t, err)
					return
				}
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Contains(t, err.Error(), configName, "the error must name the key that failed")
			})
		}
	}
}

// TestValidateLabelMappings_NoValue verifies the validator is a no-op when the config
// carries no mapping at all.
func TestValidateLabelMappings_NoValue(t *testing.T) {
	for _, configName := range []string{LogLabelMappingsConfigName, TraceLabelMappingsConfigName} {
		assert.NoError(t, validateLabelMappings([]IntegrationConfigValue{{Name: "url", Value: "http://es:9200"}}, configName))
		assert.NoError(t, validateLabelMappings(nil, configName))
	}
}

// TestValidateLabelMappings_OnlyReadsItsOwnKey is the isolation this split exists for:
// a malformed LOG mapping must not fail the TRACE validation pass, and vice versa —
// otherwise one bad blob would block saving the other.
func TestValidateLabelMappings_OnlyReadsItsOwnKey(t *testing.T) {
	values := []IntegrationConfigValue{
		{Name: LogLabelMappingsConfigName, Value: `[{"accountId":`},
		{Name: TraceLabelMappingsConfigName, Value: `[{"accountId":"acc-1","mappings":{"service_name":"svc"}}]`},
	}

	require.Error(t, validateLabelMappings(values, LogLabelMappingsConfigName))
	assert.NoError(t, validateLabelMappings(values, TraceLabelMappingsConfigName))
}
