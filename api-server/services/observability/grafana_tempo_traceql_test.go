package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempoWhereOf(binary query.BinaryWhereClause) query.QueryWhereClause {
	return query.QueryWhereClause{Binary: binary}
}

func TestTempoSelector_EmptyMatchesEverything(t *testing.T) {
	sel, err := tempoSelector(query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, "{}", sel)
}

func TestTempoSelector_CanonicalAndScopedFields(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{
		"workload_namespace":    {query.Eq: "demo"},
		"resource.k8s.pod.name": {query.Eq: "p-1"},
	}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.k8s.pod.name = "p-1" && resource.k8s.namespace.name = "demo" }`, sel)
}

func TestTempoCondition_Status(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"status_code": {query.Contains: "STATUS_CODE_ERROR"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ status = error }`, sel)

	sel, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"status_code": {query.Nq: "STATUS_CODE_OK"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ status != ok }`, sel)

	_, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"status_code": {query.Eq: "BROKEN"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown span status")
}

func TestTempoCondition_Operators(t *testing.T) {
	cases := []struct {
		name  string
		label string
		op    query.BinaryWhereClauseType
		val   any
		want  string
	}{
		{"numeric eq", "http_status_code", query.Eq, "503", `span.http.status_code = 503`},
		{"in", "workload_name", query.In, []any{"a", "b"}, `(resource.service.name = "a" || resource.service.name = "b")`},
		{"not in", "workload_name", query.NotIn, []any{"a", "b"}, `(resource.service.name != "a" && resource.service.name != "b")`},
		{"contains", "resource", query.Contains, "/api", `span.http.target =~ "(?i).*/api.*"`},
		{"not contains", "resource", query.NIContains, "/api", `span.http.target !~ "(?i).*/api.*"`},
		{"like", "workload_name", query.Like, "%pay%", `resource.service.name =~ "(?i).*pay.*"`},
		{"regex", "span_name", query.Regex, "GET .*", `name =~ "GET .*"`},
		{"duration gt", "duration_ns", query.Gt, "500000000", `duration > 500000000ns`},
		{"trace id", "trace_id", query.Eq, "abc", `trace:id = "abc"`},
		{"destination", "destination_workload_name", query.Eq, "payments", `span.net.peer.name = "payments"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{tc.label: {tc.op: tc.val}}))
			require.NoError(t, err)
			assert.Equal(t, "{ "+tc.want+" }", sel)
		})
	}
}

func TestTempoCondition_Escaping(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.Eq: `a"b`}}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.service.name = "a\"b" }`, sel)

	sel, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"resource": {query.Contains: "/api/v1/*"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ span.http.target =~ "(?i).*/api/v1/\\*.*" }`, sel)
}

func TestTempoWhere_StripsUnsupportedAndRejectsUnknown(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"destination_workload_namespace": {query.Eq: "demo"}}))
	require.NoError(t, err)
	assert.Equal(t, "{}", sel, "a field Tempo cannot express is dropped, not turned into an always-false match")

	_, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"podname": {query.Eq: "x"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported trace field "podname"`)

	_, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.HasKey: "x"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported operator")
}

func TestTempoWhere_OrAndGroups(t *testing.T) {
	sel, err := tempoSelector(query.QueryWhereClause{
		Or: []query.QueryWhereClause{
			tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.Eq: "a"}}),
			tempoWhereOf(query.BinaryWhereClause{"workload_name": {query.Eq: "b"}}),
		},
		And: []query.QueryWhereClause{tempoWhereOf(query.BinaryWhereClause{"workload_namespace": {query.Eq: "demo"}})},
	})
	require.NoError(t, err)
	assert.Equal(t, `{ (resource.k8s.namespace.name = "demo") && (resource.service.name = "a" || resource.service.name = "b") }`, sel)
}

func TestTempoSearchQuery_AppendsSelectAndMostRecent(t *testing.T) {
	q, err := tempoSearchQuery("", tempoWhereOf(query.BinaryWhereClause{"workload_namespace": {query.Eq: "demo"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | `+tempoSelectClause+` with (most_recent=true)`, q)

	q, err = tempoSearchQuery(`{ status = error }`, query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, `{ status = error } | `+tempoSelectClause+` with (most_recent=true)`, q)

	raw := `{ status = error } | select(span.http.target) with (most_recent=true)`
	q, err = tempoSearchQuery(raw, query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, raw, q, "a raw query that already selects and orders is sent unchanged")
}

func TestBuildTempoGroupQueries(t *testing.T) {
	g, err := buildTempoGroupQueries("", tempoWhereOf(query.BinaryWhereClause{"workload_namespace": {query.Eq: "demo"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | count_over_time() `+tempoGroupBy, g.Count)
	assert.Equal(t, `{ (resource.k8s.namespace.name = "demo") && status = error } | count_over_time() `+tempoGroupBy, g.Errors)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | quantile_over_time(duration, .95, .99) `+tempoGroupBy, g.Quantiles)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" } | max_over_time(duration) `+tempoGroupBy, g.Max)

	g, err = buildTempoGroupQueries("", query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, `{ status = error } | count_over_time() `+tempoGroupBy, g.Errors)

	_, err = buildTempoGroupQueries(`{ status = error }`, query.QueryWhereClause{})
	require.Error(t, err)
}

// service.go rewrites every where clause through the label mapping before any source
// method runs, so the builder receives the mapped names — TraceQL intrinsics included.
func TestTempoSelector_AcceptsMappedFieldNames(t *testing.T) {
	cases := []struct {
		name  string
		label string
		op    query.BinaryWhereClauseType
		val   any
		want  string
	}{
		{"span name", "span_name", query.Nq, "query", `name != "query"`},
		{"status", "status_code", query.Contains, "STATUS_CODE_ERROR", `status = error`},
		{"trace id", "trace_id", query.Eq, "abc", `trace:id = "abc"`},
		{"span id", "span_id", query.Eq, "def", `span:id = "def"`},
		{"parent id", "parent_id", query.Eq, "0f", `span:parentID = "0f"`},
		{"duration", "duration_ns", query.Gt, "500000000", `duration > 500000000ns`},
		{"http status", "http_status_code", query.Eq, "503", `span.http.status_code = 503`},
		{"namespace", "workload_namespace", query.Eq, "demo", `resource.k8s.namespace.name = "demo"`},
		{"destination", "destination_workload_name", query.Eq, "payments", `span.net.peer.name = "payments"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := convertWhereClauseWithMApping(tempoWhereOf(query.BinaryWhereClause{tc.label: {tc.op: tc.val}}), tempoTraceLabelMapping)
			sel, err := tempoSelector(w)
			require.NoError(t, err)
			assert.Equal(t, "{ "+tc.want+" }", sel)
		})
	}
}

func TestTempoSelector_KindIsAnUnquotedEnum(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"kind": {query.Eq: "SPAN_KIND_SERVER"}}))
	require.NoError(t, err)
	assert.Equal(t, `{ kind = server }`, sel, "Tempo rejects a quoted kind: binary operations must operate on the same type")
}

func TestBuildTempoGroupQueries_MappedDefaultFilter(t *testing.T) {
	// The grouped view's default filter, as it reaches the source.
	w := convertWhereClauseWithMApping(tempoWhereOf(query.BinaryWhereClause{"span_name": {query.Nq: "query"}}), tempoTraceLabelMapping)
	g, err := buildTempoGroupQueries("", w)
	require.NoError(t, err)
	assert.Equal(t, `{ name != "query" } | count_over_time() `+tempoGroupBy, g.Count)
}

func TestTempoCondition_EmptyNumericIsNil(t *testing.T) {
	// The UI sends http_status_code _eq X together with _neq ''.
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{"http_status_code": {query.Eq: "503", query.Nq: ""}}))
	require.NoError(t, err)
	assert.Equal(t, `{ span.http.status_code = 503 && span.http.status_code != nil }`, sel)

	sel, err = tempoSelector(tempoWhereOf(query.BinaryWhereClause{"http_status_code": {query.Eq: ""}}))
	require.NoError(t, err)
	assert.Equal(t, `{ span.http.status_code = nil }`, sel)
}

func TestTempoWhere_StripsHeaders(t *testing.T) {
	sel, err := tempoSelector(tempoWhereOf(query.BinaryWhereClause{
		"headers":            {query.Like: "%x-request-id%"},
		"workload_namespace": {query.Eq: "demo"},
	}))
	require.NoError(t, err)
	assert.Equal(t, `{ resource.k8s.namespace.name = "demo" }`, sel)
}

func TestTempoSearchQuery_SelectGoesBeforeTrailingWith(t *testing.T) {
	q, err := tempoSearchQuery(`{ status = error } with (most_recent=true)`, query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, `{ status = error } | `+tempoSelectClause+` with (most_recent=true)`, q)

	q, err = tempoSearchQuery(`{ status = error } with(sample=0.5)`, query.QueryWhereClause{})
	require.NoError(t, err)
	assert.Equal(t, `{ status = error } | `+tempoSelectClause+` with(sample=0.5) with (most_recent=true)`, q)
}

func TestTempoSelectClause_SelectsSpanName(t *testing.T) {
	// Tempo 3.0.3 search spans carry no name unless it is selected.
	assert.Contains(t, tempoSelectClause, ", name)")
}

func TestTempoAttrFor_AcceptsEveryMappingValue(t *testing.T) {
	for canonical, mapped := range tempoTraceLabelMapping {
		attr, err := tempoAttrFor(mapped)
		require.NoError(t, err, "%s maps to %s, which the builder must accept once service.go has applied the mapping", canonical, mapped)
		assert.Equal(t, mapped, attr)
	}
}
