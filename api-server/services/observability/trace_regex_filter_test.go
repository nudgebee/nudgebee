package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func regexClause(op query.BinaryWhereClauseType) query.QueryWhereClause {
	return query.QueryWhereClause{Binary: query.BinaryWhereClause{"resource": {op: "central|edge"}}}
}

// A regex filter reaches a trace source only if that source says its builder
// compiles it. Everything else is refused — including the sources whose builder
// would have skipped the operator and answered with every span.
func TestValidateTraceWhere_RegexIsOptInPerSource(t *testing.T) {
	both := []query.BinaryWhereClauseType{query.Regex, query.NRegex}
	cases := []struct {
		name    string
		source  TraceSource
		accepts []query.BinaryWhereClauseType
	}{
		{"clickhouse", &OtelClickhouseTraceSource{}, both},
		{"dynatrace", &DynatraceTraceSource{}, both},
		// Its builder has a case for _regex and already refuses the negation.
		{"cubeapm", &CubeAPMTraceSource{}, []query.BinaryWhereClauseType{query.Regex}},
		// buildESBoolQuery has no case for either and skips what it does not know.
		{"elasticsearch", &ElasticSaasTraceSource{}, nil},
		{"elasticsearch otel", &ElasticOtelTraceSource{}, nil},
		// These read named operators out of the clause and ignore the rest.
		{"jaeger", &JaegerTraceSource{}, nil},
		{"jaeger saas", &JaegerSaasTraceSource{}, nil},
		{"gcp", &GcpTraceSource{}, nil},
		{"solarwinds", &SolarWindsTraceSource{}, nil},
		{"splunk", &SplunkTraceSource{}, nil},
		{"splunk enterprise", &SplunkEnterpriseTraceSource{}, nil},
		{"newrelic", &NewRelicTraceSource{}, nil},
		{"openobserve", &OpenObserveTraceSource{}, nil},
		{"datadog", &DatadogTraceSource{}, nil},
		{"azure app insights", &AzureAppInsightsTraceSource{}, nil},
		{"chronosphere", &ChronosphereTraceSource{}, nil},
		{"chronosphere saas", &ChronosphereTraceSaasSource{}, nil},
	}
	for _, tc := range cases {
		for _, op := range both {
			err := validateTraceWhere(tc.source, regexClause(op))
			accepted := false
			for _, a := range tc.accepts {
				accepted = accepted || a == op
			}
			if accepted {
				assert.NoError(t, err, "%s / %s", tc.name, op)
				continue
			}
			require.Error(t, err, "%s / %s", tc.name, op)
			assert.Contains(t, err.Error(), "is not supported by this account's trace provider", "%s / %s", tc.name, op)
			assert.Contains(t, err.Error(), traceRegexOperatorLabels[op], "%s / %s", tc.name, op)
		}
	}
}

// The refusal finds a regex wherever it sits in the clause, and leaves every
// other operator to the source's own builder.
func TestValidateTraceWhere_WalksTheWholeClause(t *testing.T) {
	es := &ElasticSaasTraceSource{}
	regex := regexClause(query.Regex)
	eq := query.QueryWhereClause{Binary: query.BinaryWhereClause{"span_name": {query.Eq: "GET /"}}}
	for name, where := range map[string]query.QueryWhereClause{
		"top level":     regex,
		"under _and":    {And: []query.QueryWhereClause{eq, regex}},
		"under _or":     {Or: []query.QueryWhereClause{eq, regex}},
		"under _not":    {Not: &regex},
		"nested deeper": {And: []query.QueryWhereClause{{Or: []query.QueryWhereClause{{Not: &regex}}}}},
	} {
		require.Error(t, validateTraceWhere(es, where), name)
	}
	for name, where := range map[string]query.QueryWhereClause{
		"empty": {},
		"other operators": {Binary: query.BinaryWhereClause{
			"resource":  {query.ILike: "%central%"},
			"span_name": {query.In: []any{"GET /", "POST /"}},
		}},
		"nested without regex": {And: []query.QueryWhereClause{eq}, Not: &eq},
	} {
		assert.NoError(t, validateTraceWhere(es, where), name)
	}
}

// ClickHouse names both operators because the SQL generator has a case for both;
// if one is ever dropped there, this is the list that has to shrink with it.
func TestValidateTraceWhere_ClickhouseDeclaresWhatItsGeneratorCompiles(t *testing.T) {
	assert.ElementsMatch(t,
		[]query.BinaryWhereClauseType{query.Regex, query.NRegex},
		(&OtelClickhouseTraceSource{}).TraceRegexOperators())
}
