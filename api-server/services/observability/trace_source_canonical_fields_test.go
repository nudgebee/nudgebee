package observability

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The advertised-canonical-field invariant
//
// FetchTraceLabels tells the trace agent which fields it may filter on, and the v2
// trace agent prompt treats that list as authoritative ("NEVER invent a field name").
// A field advertised for a provider that cannot resolve it produces an empty result
// and no error, which the agent reports as "there are no such traces" — an
// infrastructure gap turned into a false claim about the customer's system.
//
// The rule: a provider that publishes a static label mapping is taken at its word, and
// only the canonical fields that mapping (or live discovery) covers are advertised. A
// provider that publishes no mapping has declared nothing, so the full canonical set
// stays advertised for it.
//
// These tests pin the per-source consequence so that editing a label mapping — or
// adding a provider — shows up as a deliberate diff rather than a silent change in
// what we promise the model.
// ---------------------------------------------------------------------------

// canonicalTraceFieldNames returns the canonical field names present in labels.
func canonicalTraceFieldNames(labels []OutputTraceLabel) []string {
	canonical := make(map[string]struct{}, len(canonicalTraceFields))
	for _, f := range canonicalTraceFields {
		canonical[f.name] = struct{}{}
	}
	var out []string
	for _, l := range labels {
		if _, ok := canonical[l.Label]; ok {
			out = append(out, l.Label)
		}
	}
	sort.Strings(out)
	return out
}

// allCanonicalTraceFieldNames is the full vocabulary, sorted — what an undeclared
// (passthrough) provider still advertises.
func allCanonicalTraceFieldNames() []string {
	out := make([]string, 0, len(canonicalTraceFields))
	for _, f := range canonicalTraceFields {
		out = append(out, f.name)
	}
	sort.Strings(out)
	return out
}

// traceSourceCanonicalCases pins, per trace source, exactly which canonical fields it
// advertises with no live discovery and no tenant/account override — i.e. from its
// static label mapping alone.
var traceSourceCanonicalCases = []struct {
	name     string
	provider string
	source   string
	// declared is whether the source publishes a static label mapping at all.
	declared bool
	expected []string
}{
	{
		// Passthrough: consumes canonical names unchanged, publishes no mapping, so it
		// has declared nothing and keeps the full vocabulary. Each name really is a
		// column of ClickhouseTraceTableDefinition.
		name: "otel_clickhouse keeps the full vocabulary", provider: "otel_clickhouse", source: "agent",
		declared: false, expected: allCanonicalTraceFieldNames(),
	},
	{
		name: "jaeger agent keeps the full vocabulary", provider: "jaeger", source: "agent",
		declared: false, expected: allCanonicalTraceFieldNames(),
	},
	{
		name: "jaeger saas keeps the full vocabulary", provider: "jaeger", source: "user",
		declared: false, expected: allCanonicalTraceFieldNames(),
	},
	{
		name: "azure_app_insights keeps the full vocabulary", provider: "azure_app_insights", source: "user",
		declared: false, expected: allCanonicalTraceFieldNames(),
	},
	{
		// Known gap, tracked separately: Cloud Trace publishes no mapping so it keeps
		// the full set, but gcpCloudTraceFilter only pushes down service_name,
		// workload_name, span_name, http_status_code, resource and trace_id.
		name: "gcp keeps the full vocabulary (known over-advertising)", provider: "gcp", source: "agent",
		declared: false, expected: allCanonicalTraceFieldNames(),
	},
	{
		// The bug's worked example. Chronosphere's mapping declares only these two
		// canonical names, yet all ten were advertised; a latency question filtering on
		// duration_ns matched nothing and was reported as "no slow requests". Its own
		// code says resource_filter, min_duration, max_duration and status_code are
		// not supported.
		name: "chronosphere advertises only the two canonical fields it declares", provider: "chronosphere", source: "agent",
		declared: true, expected: []string{"service_name", "trace_id"},
	},
	{
		name: "chronosphere saas matches the agent source", provider: "chronosphere", source: "user",
		declared: true, expected: []string{"service_name", "trace_id"},
	},
	{
		name: "elasticsearch saas maps the whole canonical vocabulary", provider: "ES", source: "user",
		declared: true, expected: allCanonicalTraceFieldNames(),
	},
	{
		// service_name is withheld: Dynatrace's field is `service.name`, reached via
		// workload_name.
		name: "dynatrace withholds service_name", provider: "dynatrace", source: "user",
		declared: true,
		expected: []string{"destination_workload_name", "destination_workload_namespace", "duration_ns",
			"http_status_code", "resource", "span_name", "status_code", "trace_id", "workload_name"},
	},
	{
		// swFilterPart builds filter syntax for these three grouping attributes only.
		name: "solarwinds advertises its three grouping attributes", provider: "solarwinds", source: "user",
		declared: true, expected: []string{"span_name", "status_code", "workload_name"},
	},
	{
		// destination_* are deleted from the where clause by QueryTraces, and Datadog's
		// service tag is `service` (reached via workload_name). trace_id is also absent
		// from the mapping — see the accepted-cost note in TestKnownUnderAdvertisedFields.
		name: "datadog advertises what its mapping declares", provider: "datadog", source: "user",
		expected: []string{"duration_ns", "http_status_code", "resource", "span_name", "status_code", "workload_name"},
		declared: true,
	},
	{
		name: "newrelic advertises what its mapping declares", provider: "newrelic", source: "user",
		declared: true,
		expected: []string{"destination_workload_name", "http_status_code", "resource",
			"span_name", "status_code", "trace_id", "workload_name"},
	},
	{
		name: "openobserve advertises what its mapping declares", provider: "openobserve", source: "user",
		declared: true,
		expected: []string{"destination_workload_name", "http_status_code", "resource",
			"span_name", "status_code", "trace_id", "workload_name"},
	},
	{
		name: "splunk advertises what its mapping declares", provider: "splunk_observability_platform", source: "user",
		declared: true, expected: []string{"trace_id"},
	},
}

// TestTraceSources_AdvertiseOnlyResolvableCanonicalFields is the invariant that was
// missing: for every trace source that declares a label mapping, every canonical field
// FetchTraceLabels advertises is one that mapping can resolve.
func TestTraceSources_AdvertiseOnlyResolvableCanonicalFields(t *testing.T) {
	for _, tc := range traceSourceCanonicalCases {
		t.Run(tc.name, func(t *testing.T) {
			src, err := getTraceSource(tc.provider, tc.source)
			require.NoError(t, err)

			mapping := src.GetLabelMapping()
			declares := providerDeclaresTraceFields(src)
			assert.Equal(t, tc.declared, declares,
				"whether %s publishes a static label mapping decides which rule applies to it", tc.provider)

			// No live discovery: this is the static contract of the source alone.
			advertised := canonicalTraceFieldNames(buildTraceLabels(mapping, declares, nil))

			if declares {
				for _, name := range advertised {
					assert.Contains(t, mapping, name,
						"%s advertises canonical field %q but its label mapping cannot resolve it", tc.provider, name)
				}
			}
			assert.Equal(t, tc.expected, advertised,
				"advertised canonical set for %s changed; confirm the mapping edit is correct before updating this expectation",
				tc.provider)
		})
	}
}

// TestTraceSources_EveryRegisteredSourceIsPinned fails when a trace source is added to
// getTraceSource without an entry above, so a new provider cannot quietly inherit an
// unreviewed advertised field set.
func TestTraceSources_EveryRegisteredSourceIsPinned(t *testing.T) {
	// Mirrors the switch in getTraceSource; ElasticOtelTraceSource is selected by
	// resolveTraceSource from the configured index rather than by provider string.
	registered := []string{
		"datadog/user", "azure_app_insights/user", "chronosphere/user", "chronosphere/agent",
		"otel_clickhouse/agent", "jaeger/agent", "jaeger/user", "newrelic/user",
		"splunk_observability_platform/user", "ES/user", "dynatrace/user", "solarwinds/user",
		"gcp/agent", "openobserve/user",
	}
	pinned := make(map[string]struct{}, len(traceSourceCanonicalCases))
	for _, tc := range traceSourceCanonicalCases {
		pinned[fmt.Sprintf("%s/%s", tc.provider, tc.source)] = struct{}{}
	}
	for _, key := range registered {
		assert.Contains(t, pinned, key,
			"trace source %q has no advertised-canonical-field expectation in traceSourceCanonicalCases", key)
	}
}

// TestKnownUnderAdvertisedFields documents the accepted cost of trusting the mapping as
// the declaration. These four fields ARE resolvable on their backends, but the mapping is
// the only declaration this design reads, and none of them appears in it:
//
//   - duration_ns on New Relic — buildNRQLSpanWhereClause converts ns to `duration.ms`
//   - duration_ns on Splunk    — buildTraceQuery converts ns to microseconds
//   - trace_id on Datadog      — a span-search facet used verbatim
//   - service_name on OpenObserve — a real column of the trace stream
//
// Withholding them is the safe failure direction (under-advertising degrades an answer;
// over-advertising produces a confidently wrong one), but it is a real capability loss —
// notably, latency questions become unanswerable on New Relic and Splunk. The test exists
// so the loss is recorded rather than discovered, and so adding one of these to a mapping
// is a deliberate change.
func TestKnownUnderAdvertisedFields(t *testing.T) {
	for _, tc := range []struct{ provider, source, field string }{
		{"newrelic", "user", "duration_ns"},
		{"splunk_observability_platform", "user", "duration_ns"},
		{"datadog", "user", "trace_id"},
		{"openobserve", "user", "service_name"},
	} {
		t.Run(tc.provider+"/"+tc.field, func(t *testing.T) {
			src, err := getTraceSource(tc.provider, tc.source)
			require.NoError(t, err)
			advertised := canonicalTraceFieldNames(buildTraceLabels(src.GetLabelMapping(), providerDeclaresTraceFields(src), nil))
			assert.NotContains(t, advertised, tc.field,
				"%s no longer withholds %q — if its mapping now declares it, drop this entry", tc.provider, tc.field)
		})
	}
}

// aliasOnlyTraceSource is a passthrough source that also renames a name — the exact
// shape that regressed: ClickHouse was passthrough, gained one alias, and silently
// stopped advertising everything else.
type aliasOnlyTraceSource struct{ OtelClickhouseTraceSource }

func (s *aliasOnlyTraceSource) GetLabelMapping() map[string]string {
	return map[string]string{"namespace": "workload_namespace"}
}

// TestPassthroughSourceKeepsCanonicalFieldsDespiteAliases is the invariant that was
// missing when #36940 (advertise only what the mapping resolves) and #36866 (give
// ClickHouse an alias mapping) landed two days apart: each was correct alone, and
// together they collapsed ClickHouse's advertised vocabulary from ten canonical fields
// to three alias keys. providerDeclaresTraceFields inferred "declares its resolvable
// set" from "publishes a mapping"; a passthrough source that renames a name makes those
// two different claims.
func TestPassthroughSourceKeepsCanonicalFieldsDespiteAliases(t *testing.T) {
	src := &aliasOnlyTraceSource{}
	require.False(t, providerDeclaresTraceFields(src),
		"an alias mapping on a passthrough source must not count as declaring its field set")

	advertised := canonicalTraceFieldNames(buildTraceLabels(src.GetLabelMapping(), providerDeclaresTraceFields(src), nil))
	assert.Equal(t, allCanonicalTraceFieldNames(), advertised,
		"renaming one field must not withdraw the other nine")
}

// TestOtelClickhouse_CanonicalFieldsKeepTheirTypes guards the one place a value type is
// attached to a trace label: a passthrough provider must still get typed canonical
// fields, since the trace agent uses the type to build comparisons (duration_ns > N).
func TestOtelClickhouse_CanonicalFieldsKeepTheirTypes(t *testing.T) {
	src := &OtelClickhouseTraceSource{}
	mapping := src.GetLabelMapping()
	labels := buildTraceLabels(mapping, providerDeclaresTraceFields(src), nil)
	// ClickHouse publishes alias entries but marks itself passthrough, so it keeps every
	// canonical field AND advertises the aliases — never the aliases alone.
	require.Len(t, labels, len(canonicalTraceFields)+len(mapping),
		"passthrough aliases must extend the canonical vocabulary, not replace it")

	for i, f := range canonicalTraceFields {
		assert.Equal(t, f.name, labels[i].Label, "canonical fields lead, in declared order")
		assert.Equal(t, f.typ, labels[i].Attributes["type"], "canonical field %q must carry its type", f.name)
	}
}

// TestElasticOtelTraceSource_AdvertisesFullCanonicalSet covers the OTel-native Elastic
// source, which resolveTraceSource picks by trace index rather than provider string. It
// declares a mapping AND that mapping covers every canonical field, so nothing is lost.
func TestElasticOtelTraceSource_AdvertisesFullCanonicalSet(t *testing.T) {
	src := &ElasticOtelTraceSource{}
	advertised := canonicalTraceFieldNames(buildTraceLabels(src.GetLabelMapping(), providerDeclaresTraceFields(src), nil))
	assert.Equal(t, allCanonicalTraceFieldNames(), advertised,
		"elastic OTel maps every canonical field, so all of them stay advertised")
}
