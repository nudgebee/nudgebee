package observability

import (
	"strings"
	"testing"

	"nudgebee/services/integrations"
	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildOpenObserveRawSQLCombinesFiltersAndBoundsLimit(t *testing.T) {
	sql, err := buildOpenObserveRawSQL(
		`SELECT * FROM "logs" WHERE body = 'x' OR severity = 'ERROR' ORDER BY _timestamp ASC LIMIT 50000`,
		"logs", "k8s_namespace_name = 'prod'", "_timestamp DESC", 100,
	)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "logs" WHERE (body = 'x' OR severity = 'ERROR') AND (k8s_namespace_name = 'prod') ORDER BY _timestamp ASC LIMIT 100`, sql)
}

func TestBuildOpenObserveRawSQLUsesStructuredFilterWithoutRawWhere(t *testing.T) {
	sql, err := buildOpenObserveRawSQL(`SELECT * FROM logs`, "logs", `service_name = 'api'`, "_timestamp DESC", 25)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "logs" WHERE service_name = 'api' ORDER BY _timestamp DESC LIMIT 25`, sql)
}

func TestOpenObserveRawSQLPreservesQuotedOrderIdentifiers(t *testing.T) {
	sql, err := buildOpenObserveRawSQL(`SELECT * FROM logs ORDER BY "order" ASC`, "logs", "", "_timestamp DESC", 10)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "logs" ORDER BY "order" ASC LIMIT 10`, sql)
}

func TestOpenObserveRawSQLKeepsQuotedKeywordsAndDoubledQuotesInPredicate(t *testing.T) {
	input := `SELECT * FROM logs WHERE body = 'it''s; -- from other_stream # /*' ORDER BY _timestamp DESC LIMIT 5`
	sql, err := buildOpenObserveRawSQL(input, "logs", "", "_timestamp DESC", 100)
	require.NoError(t, err)
	assert.Contains(t, sql, `body = 'it''s; -- from other_stream # /*'`)
	assert.Contains(t, sql, `LIMIT 5`)
}

func TestOpenObserveRawSQLRejectsUnsafeOrUnsupportedStatements(t *testing.T) {
	tests := []struct {
		name string
		sql  string
	}{
		{"other stream", `SELECT * FROM secrets`},
		{"cross stream", `SELECT * FROM logs, secrets`},
		{"join", `SELECT * FROM logs JOIN secrets ON true`},
		{"subquery", `SELECT * FROM logs WHERE trace_id IN (SELECT trace_id FROM secrets)`},
		{"union", `SELECT * FROM logs UNION SELECT * FROM secrets`},
		{"cte", `WITH x AS (SELECT * FROM logs) SELECT * FROM x`},
		{"table function", `SELECT * FROM logs(`},
		{"projection", `SELECT message FROM logs`},
		{"aggregate", `SELECT * FROM logs GROUP BY severity`},
		{"second statement", `SELECT * FROM logs; SELECT * FROM secrets`},
		{"line comment", `SELECT * FROM logs WHERE body = 'x' -- comment`},
		{"block comment", `SELECT * FROM logs /* comment */`},
		{"hash comment", `SELECT * FROM logs # comment`},
		{"dollar quote", `SELECT * FROM logs WHERE body = $$x$$`},
		{"backslash escape", `SELECT * FROM logs WHERE body = E'foo\\'`},
		{"unbalanced quote", `SELECT * FROM logs WHERE body = 'x`},
		{"unbalanced paren", `SELECT * FROM logs WHERE (body = 'x'`},
		{"trailing clause", `SELECT * FROM logs WHERE body = 'x' OFFSET 4`},
		{"fractional limit", `SELECT * FROM logs LIMIT 1.5`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildOpenObserveRawSQL(tt.sql, "logs", "", "_timestamp DESC", 100)
			assert.Error(t, err)
		})
	}
}

func TestOpenObserveRawSQLStreamMustMatchConfiguredNameExactly(t *testing.T) {
	_, err := buildOpenObserveRawSQL(`SELECT * FROM LOGS`, "logs", "", "_timestamp DESC", 10)
	require.Error(t, err)
	_, err = buildOpenObserveRawSQL(`SELECT * FROM "logs"`, "logs", "", "_timestamp DESC", 10)
	require.NoError(t, err)
	_, err = buildOpenObserveRawSQL(`SELECT * FROM logs`, `logs" UNION SELECT * FROM secrets`, "", "_timestamp DESC", 10)
	require.Error(t, err)
}

func TestOpenObserveRawSQLRejectsOversizedInput(t *testing.T) {
	_, err := buildOpenObserveRawSQL(`SELECT * FROM logs WHERE body = '`+strings.Repeat("x", openObserveRawQueryMaxBytes)+`'`, "logs", "", "_timestamp DESC", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestOpenObserveLogRawSQLIncludesMappedStructuredFilter(t *testing.T) {
	sql, err := (&OpenObserveLogSource{}).buildSQL(FetchLogRequest{
		Query: `SELECT * FROM default WHERE body ILIKE '%error%'`,
		Limit: 20,
		QueryRequest: LogsQueryBuilderRequest{Where: query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"namespace": {query.Eq: "prod"},
		}}},
	}, integrations.OpenObserveDefaultLogStream)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "default" WHERE (body ILIKE '%error%') AND (k8s_namespace_name = 'prod') ORDER BY _timestamp DESC LIMIT 20`, sql)
}

func TestOpenObserveTraceRawSQLIncludesRootSpanFilter(t *testing.T) {
	req := withOpenObserveRootSpans(TracesV3Request{
		Query:        `SELECT * FROM default WHERE service_name = 'checkout'`,
		QueryRequest: TracesQueryBuilderRequest{Limit: 50},
	})
	sql, err := (&OpenObserveTraceSource{}).buildSQL(req, integrations.OpenObserveDefaultTraceStream)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "default" WHERE (service_name = 'checkout') AND (reference_parent_span_id IS NULL) ORDER BY _timestamp DESC LIMIT 50`, sql)
}

func TestOpenObserveTraceRawSQLIncludesMappedStandingFilter(t *testing.T) {
	req := TracesV3Request{
		Query: `SELECT * FROM default WHERE service_name = 'frontend'`,
		QueryRequest: TracesQueryBuilderRequest{Where: traceFilterClause(defaultFilterRow{
			Key: "workload_name", Value: "checkout",
		})},
	}
	sql, err := (&OpenObserveTraceSource{}).buildSQL(req, integrations.OpenObserveDefaultTraceStream)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "default" WHERE (service_name = 'frontend') AND (service_name = 'checkout') ORDER BY _timestamp DESC LIMIT 10000`, sql)
}
