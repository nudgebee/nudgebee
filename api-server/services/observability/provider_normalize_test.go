package observability

import (
	"testing"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProviderName(t *testing.T) {
	cases := map[string]string{
		"prometheus":     "prometheus",
		"Prometheus":     "prometheus",
		"PROMETHEUS":     "prometheus",
		"  prometheus  ": "prometheus",
		"\tDatadog\n":    "datadog",
		"aws_CloudWatch": "aws_cloudwatch",
		// Elasticsearch is stored as "ES", the one upper-case provider type.
		"ES":   "ES",
		"es":   "ES",
		" Es ": "ES",
		// Whitespace-only means "not set", so the account default is used.
		"   ": "",
		"":    "",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeProviderName(in), "input %q", in)
	}
}

func TestNormalizeProviderSource(t *testing.T) {
	cases := map[string]string{
		"agent":   "agent",
		"Agent":   "agent",
		" USER ":  "user",
		"  ":      "",
		"":        "",
		"\tagent": "agent",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeProviderSource(in), "input %q", in)
	}
}

// The reported failure: a Query Metrics task with Metric Provider "Prometheus" was
// refused with "unsupported metric provider/source combination".
func TestGetMetricsSourceForAccount_ProviderMatchesRegardlessOfCaseAndSpaces(t *testing.T) {
	ctx := tenantCarryingContext("test-tenant")
	for _, tc := range []struct{ provider, source string }{
		{"prometheus", "agent"},
		{"Prometheus", "agent"},
		{"PROMETHEUS", "AGENT"},
		{"  Prometheus ", " Agent "},
	} {
		source, err := getMetricsSourceForAccount(ctx, "acc-1", tc.provider, tc.source)
		require.NoError(t, err, "provider %q source %q", tc.provider, tc.source)
		assert.IsType(t, &PrometheusMetricSource{}, source, "provider %q source %q", tc.provider, tc.source)
	}
}

func TestGetLogSourceForAccount_ESMatchesRegardlessOfCase(t *testing.T) {
	ctx := tenantCarryingContext("test-tenant")
	source, err := getLogSourceForAccount(ctx, "acc-1", "es", "User")
	require.NoError(t, err)
	assert.IsType(t, &ElasticSaasSource{}, source)
}

// With a provider but no source the funnel looks the source up by integration type,
// which is stored lower-case: the lookup must receive the normalised name.
func TestProviderFunnel_SourceLookupUsesTheNormalisedProvider(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var lookedUp string
	patches.ApplyFunc(core.GetIntegrationByTypePreferringDefault,
		func(_ *security.RequestContext, _, integrationType, _ string) (*core.IntegrationDto, error) {
			lookedUp = integrationType
			return &core.IntegrationDto{Type: integrationType, Source: "user"}, nil
		})

	provider, source, _, err := getLogsMetricsTracesProviderWithIntegration(tenantCarryingContext("test-tenant"), "acc-1", " Datadog ", "metrics", "")
	require.NoError(t, err)
	assert.Equal(t, "datadog", lookedUp)
	assert.Equal(t, "datadog", provider)
	assert.Equal(t, "user", source)
}

func TestProviderFunnel_WhitespaceOnlyProviderUsesTheAccountDefault(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(core.GetIntegrationByConfigNameValues,
		func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
			return &core.IntegrationDto{Type: "datadog", Source: "user"}, nil
		})

	provider, source, _, err := getLogsMetricsTracesProviderWithIntegration(tenantCarryingContext("test-tenant"), "acc-1", "   ", "metrics", "")
	require.NoError(t, err)
	assert.Equal(t, "datadog", provider)
	assert.Equal(t, "user", source)
}

// Readers that still see the request value as typed must agree with the resolver,
// which trims it: a padded name must not change their answer.
func TestRawProviderReadersIgnoreSurroundingSpaces(t *testing.T) {
	s := &NewRelicMetricSource{}
	start, end := int64(1704067200), int64(1706659200) // 30 days
	assert.Equal(t,
		s.calculateStepInterval(0, start, end, "newrelic"),
		s.calculateStepInterval(0, start, end, " NewRelic "),
		"the New Relic bucket limit must apply to a padded provider name")

	q := "up"
	row, ok := BuildMetricsQueryHistory(
		FetchMetricsRequest{AccountId: "a1", MetricProvider: " Prometheus "},
		OutputMetricQuery{Results: []QueryResult{{QueryKey: "a", Query: q}}}, nil, time.Second)
	require.True(t, ok)
	assert.Equal(t, "metrics_query_prometheus", row.Module)
}
