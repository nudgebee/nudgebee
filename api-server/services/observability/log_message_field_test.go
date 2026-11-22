package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// messageFieldCases pins, for every registered log source, what the canonical log-text
// spelling `message` resolves to (#39573).
//
// `message` is the one name every producer is converging on — the UI log builder, the
// llm-server log agents and the event enrichers. A provider that does not resolve it
// receives a filter on a field that does not exist and returns nothing, with a success
// status and no error. That failure is invisible, so it has to be pinned rather than
// noticed.
//
// want == "" means "correctly unmapped": the backend's own column is already called
// `message`, so the name reaches it verbatim and a mapping would be a no-op. Those are
// marked with the reason, because "no entry" otherwise reads like an oversight — which
// is exactly how Loki's missing entry survived.
var messageFieldCases = []struct {
	provider string
	source   string
	want     string
	why      string
}{
	{"loki", "agent", "log", "`log` is special-cased into a LogQL line filter"},
	{"loki", "user", "log", "delegates to the agent-mode source"},
	{"signoz", "agent", "body", "SigNoz calls the log line `body`"},
	{"signoz", "user", "body", "SigNoz Cloud speaks the same attribute names"},
	{"datadog", "user", "", "empty string means free-text search, not a facet"},
	{"loggly", "user", "json.log", ""},
	{"newrelic", "user", "message", ""},
	{"splunk_observability_platform", "user", "message", ""},
	{"dynatrace", "user", "content", "Dynatrace calls the log line `content`"},
	{"openobserve", "user", "body", ""},
	{"cubeapm", "user", "_msg", ""},
	{"otel_clickhouse", "agent", "log", "Loki's spelling: logql2ch reads `log` as the line"},
	{"splunk_enterprise", "user", "body", ""},

	// Correctly unmapped — the backend's own column is `message`.
	{"solarwinds", "user", "", "SolarWinds' schema is id/time/message/hostname/severity/program"},
	{"observe", "user", "", "Observe's own column is `message`; ToOpal renders it verbatim"},

	// Correctly unmapped — these never resolve a where clause for log text at all.
	{"ES", "agent", "", "resolved through esCanonicalK8sFields, not GetLabelMapping"},
	{"ES", "user", "", "resolved through esCanonicalK8sFields, not GetLabelMapping"},
	{"azure_app_insights", "user", "", "QueryLogs takes a raw KQL string, never a where clause"},
	{"aws_cloudwatch", "user", "", "GetQuery returns empty; no where-clause filtering"},
	{"pinot", "agent", "", "uses the pinot_message_col request param"},
	{"pinot", "user", "", "GetDynamicLabelMapping sets `message` per account"},
	{"hive", "user", "", "GetDynamicLabelMapping sets `message` per account"},
}

// TestMessageResolvesOnEveryLogProvider is the regression net for the whole change.
func TestMessageResolvesOnEveryLogProvider(t *testing.T) {
	for _, tc := range messageFieldCases {
		t.Run(tc.provider+"/"+tc.source, func(t *testing.T) {
			source, err := getLogSource(tc.provider, tc.source)
			require.NoError(t, err)

			got := source.GetLabelMapping()["message"]
			assert.Equal(t, tc.want, got,
				"`message` resolution changed for %s/%s (%s)", tc.provider, tc.source, tc.why)
		})
	}
}

// TestMessageFieldCases_CoverEveryRegisteredSource keeps the table above honest: a log
// source added to getLogSource without an entry would otherwise be silently exempt from
// the check, which is how this class of gap goes unnoticed in the first place.
func TestMessageFieldCases_CoverEveryRegisteredSource(t *testing.T) {
	pinned := make(map[string]struct{}, len(messageFieldCases))
	for _, tc := range messageFieldCases {
		pinned[tc.provider+"/"+tc.source] = struct{}{}
	}
	for _, tc := range logSourceDispatchTable {
		assert.Contains(t, pinned, tc.provider+"/"+tc.source,
			"log source %q/%q has no `message` expectation in messageFieldCases",
			tc.provider, tc.source)
	}
}

// TestSignozMessageBecomesBodyAttribute exercises the full canonical→provider→query
// path for SigNoz, which had no mapping at all before #39573.
//
// The literal shape matters: FlattenSignozQuery emits the field as an attribute key
// verbatim, so an unmapped `message` would reach SigNoz as an attribute named
// `message`, match nothing, and return HTTP 200 with zero rows.
func TestSignozMessageBecomesBodyAttribute(t *testing.T) {
	s := &SignozSource{}
	where := query.QueryWhereClause{
		Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
			"message": {query.Contains: "timeout"},
		},
	}

	resolved := convertWhereClauseWithMApping(where, s.GetLabelMapping())
	clauses, err := FlattenSignozQuery(resolved)
	require.NoError(t, err)
	require.Len(t, clauses, 1)

	assert.Equal(t, "body", clauses[0].Key["key"],
		"`message` must reach SigNoz as its `body` attribute, not as a literal `message`")
	assert.Equal(t, "timeout", clauses[0].Value)
}

// TestElasticMessageUsesBodyCandidates pins that `message` goes through the per-shipper
// candidate expansion rather than reaching Elasticsearch as a literal field.
//
// esCandidateFields returns nil for an unrecognised name and the caller then renders it
// verbatim — so an index whose log line is in `log` or `msg` would match nothing.
func TestElasticMessageUsesBodyCandidates(t *testing.T) {
	got := esCandidateFields("message")
	require.NotNil(t, got, "`message` must be a candidate-expansion key, not rendered verbatim")

	assert.ElementsMatch(t, esCandidateFields(esCanonicalBodyField), got,
		"`message` and the legacy `_body` token must expand to the same shipper spellings")
}
