package observability

import (
	"errors"
	"net/http"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants to avoid duplicate literals.
const (
	swTestBaseURL    = "https://api.ap-01.cloud.solarwinds.com"
	swTestSeverityEq = "severity:INFO"
	swTestMsgError   = "message:error"
	swTestPodABC     = "pod-abc"
	swTestHostPodX   = `host:"pod-x"`
	swTestISO2023    = "2023-01-01T00:00:00Z"
	swTestEntitiesP  = "/v1/entities"
)

// Note: mockHTTPResponseNew and mockRequestContext are defined in loggly_test.go

// ============================================================================
// swEscapeValue
// ============================================================================

func TestSWEscapeValue(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain identifier", "nginx", "nginx"},
		{"contains space", "my pod", `"my pod"`},
		{"contains tab", "a\tb", `"a` + "\t" + `b"`},
		{"contains colon", "host:8080", `"host:8080"`},
		{"contains open paren", "(foo)", `"(foo)"`},
		{"contains close paren", "foo)", `"foo)"`},
		{"contains backslash", `a\b`, `"a\\b"`},
		{"contains double-quote", `say "hi"`, `"say \"hi\""`},
		{"empty string", "", ""},
		{"no special chars", "pod-name-123", "pod-name-123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, swEscapeValue(tc.input))
		})
	}
}

// ============================================================================
// swMatch / swNegate
// ============================================================================

func TestSWMatch(t *testing.T) {
	// The log response names the pod "hostname", but the search syntax only
	// matches it as "host" — "hostname:<pod>" silently matches nothing.
	assert.Equal(t, "host:pod-1", swMatch("hostname", "pod-1"))
	assert.Equal(t, `program:"my app"`, swMatch("program", "my app"))
	assert.Equal(t, swTestSeverityEq, swMatch("severity", "INFO"))
	assert.Equal(t, "k8s.namespace.name:demo", swMatch("k8s.namespace.name", "demo"))
}

func TestSWNegate(t *testing.T) {
	assert.Equal(t, "-(host:pod-1)", swNegate("host:pod-1"))
	assert.Equal(t, `-(program:"my app")`, swNegate(`program:"my app"`))
}

// ============================================================================
// swMapField
// ============================================================================

func TestSWMapField(t *testing.T) {
	cases := []struct{ in, out string }{
		{"pod", "hostname"},
		{"container", "program"},
		{"level", "severity"},
		{"timestamp", "time"},
		{"body", "message"},
		{"namespace", "k8s.namespace.name"},
		{"app", swWorkloadField},
		{"workload", swWorkloadField},
		{"node", "k8s.node.name"},
		{"cluster", "k8s.cluster.name"},
		{"service", "service.name"},
		// passthrough — not in mapping
		{"hostname", "hostname"},
		{"severity", "severity"},
		{"program", "program"},
		{"region", "region"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.out, swMapField(tc.in))
		})
	}
}

// ============================================================================
// swMsToISO8601
// ============================================================================

func TestSWMsToISO8601(t *testing.T) {
	// 2023-01-01T00:00:00Z = 1672531200000 ms
	assert.Equal(t, swTestISO2023, swMsToISO8601(1672531200000))
}

// ============================================================================
// inferSWLogSeverity
// ============================================================================

func TestInferSWLogSeverity(t *testing.T) {
	cases := []struct {
		msg      string
		expected string
	}{
		{"connection error occurred", "error"},
		{"FATAL: process crashed", "error"},
		{"critical failure detected", "error"},
		{"WARNING: high memory usage", "warn"},
		{"warn: disk nearly full", "warn"},
		{"debug mode active", "debug"},
		{"trace id: abc123", "debug"},
		{"server started on port 8080", "info"},
		{"", "info"},
		{"all systems operational", "info"},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			assert.Equal(t, tc.expected, inferSWLogSeverity(tc.msg))
		})
	}
}

// ============================================================================
// buildSWBinaryClause — every operator
// ============================================================================

func TestBuildSWBinaryClause_EqualityOperators(t *testing.T) {
	cases := []struct {
		name     string
		op       query.BinaryWhereClauseType
		expected string
	}{
		// Quoted: SolarWinds matches a quoted value exactly (and case-sensitively) on
		// host and program, but an unquoted one as a case-insensitive substring, so
		// "pod = api" used to match every api-gateway pod too.
		{"Eq", query.Eq, `host:"my-pod"`},
		{"EqF", query.EqF, `host:"my-pod"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildSWBinaryClause("hostname", tc.op, "my-pod")
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildSWBinaryClause_NotEqualOperators(t *testing.T) {
	cases := []struct {
		name string
		op   query.BinaryWhereClauseType
	}{
		{"Nq", query.Nq},
		{"NqF", query.NqF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildSWBinaryClause("hostname", tc.op, "my-pod")
			assert.NoError(t, err)
			assert.Equal(t, `-(host:"my-pod")`, result)
		})
	}
}

func TestBuildSWBinaryClause_ContainsOperators(t *testing.T) {
	cases := []struct {
		name     string
		op       query.BinaryWhereClauseType
		expected string
	}{
		{"Contains", query.Contains, swTestMsgError},
		{"IContains", query.IContains, swTestMsgError},
		// SolarWinds search has no wildcards: "message:*error*" matches nothing,
		// while "message:error" is already a case-insensitive substring match.
		{"NIContains", query.NIContains, "-(message:error)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildSWBinaryClause("message", tc.op, "error")
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildSWBinaryClause_LikeOperators(t *testing.T) {
	cases := []struct {
		name     string
		op       query.BinaryWhereClauseType
		expected string
	}{
		{"Like", query.Like, swTestMsgError},
		{"ILike", query.ILike, swTestMsgError},
		{"LikeF", query.LikeF, swTestMsgError},
		{"ILikeF", query.ILikeF, swTestMsgError},
		// Case-sensitive: no exact search form, so the exclusion is applied to the rows
		// and the search fetches everything.
		{"NLike", query.NLike, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := buildSWBinaryClause("message", tc.op, "%error%")
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildSWBinaryClause_LikeInteriorWildcardSplitsIntoTerms(t *testing.T) {
	result, err := buildSWBinaryClause("message", query.ILike, "%invalid%customer%")
	assert.NoError(t, err)
	assert.Equal(t, "(message:invalid AND message:customer)", result)

	result, err = buildSWBinaryClause("message", query.NLike, "invalid*customer")
	assert.NoError(t, err)
	assert.Equal(t, "", result, "applied to the rows; a search exclusion would over-exclude")
}

func TestBuildSWBinaryClause_LikeOnlyWildcardsMatchesEverything(t *testing.T) {
	result, err := buildSWBinaryClause("message", query.Like, "%")
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

// The comparison operators are neither advertised nor rendered: SolarWinds answers
// "time:>…" with HTTP 500, and the time range belongs in startTime/endTime.
func TestSolarWindsLogs_ComparisonOperatorsNotAdvertised(t *testing.T) {
	ops := (&SolarWindsLogSource{}).GetSupportedOperators()
	for _, op := range []string{"_gt", "_gte", "_lt", "_lte"} {
		assert.NotContains(t, ops, op)
	}
}

func TestBuildSWBinaryClause_ComparisonOperatorsRefused(t *testing.T) {
	for _, op := range []query.BinaryWhereClauseType{query.Gt, query.GtF, query.Gte, query.GteF, query.Lt, query.LtF, query.Lte, query.LteF} {
		result, err := buildSWBinaryClause("time", op, "2026-10-07T18:20:00Z")
		assert.Error(t, err, op)
		assert.Contains(t, err.Error(), "time range")
		assert.Empty(t, result)
	}
}

// SolarWinds search has no IN operator ("program IN (a, b)" matches nothing),
// so a value list becomes an OR group.
func TestBuildSWBinaryClause_InOperator(t *testing.T) {
	t.Run("[]any values", func(t *testing.T) {
		result, err := buildSWBinaryClause("program", query.In, []any{"nginx", "envoy"})
		assert.NoError(t, err)
		assert.Equal(t, `(program:"nginx" OR program:"envoy")`, result)
	})

	t.Run("[]string values", func(t *testing.T) {
		result, err := buildSWBinaryClause("program", query.In, []string{"nginx", "envoy"})
		assert.NoError(t, err)
		assert.Equal(t, `(program:"nginx" OR program:"envoy")`, result)
	})

	t.Run("single string fallback", func(t *testing.T) {
		result, err := buildSWBinaryClause("program", query.In, "nginx")
		assert.NoError(t, err)
		assert.Equal(t, `program:"nginx"`, result)
	})

	t.Run("pod field searched as host", func(t *testing.T) {
		result, err := buildSWBinaryClause("hostname", query.In, []any{"pod-a", "pod-b"})
		assert.NoError(t, err)
		assert.Equal(t, `(host:"pod-a" OR host:"pod-b")`, result)
	})

	t.Run("empty slice is refused, not dropped", func(t *testing.T) {
		result, err := buildSWBinaryClause("program", query.In, []any{})
		assert.Error(t, err)
		assert.Equal(t, "", result)
	})

	t.Run("value with space escaped", func(t *testing.T) {
		result, err := buildSWBinaryClause("program", query.In, []any{"my app"})
		assert.NoError(t, err)
		assert.Equal(t, `program:"my app"`, result)
	})
}

func TestBuildSWBinaryClause_NotInOperator(t *testing.T) {
	result, err := buildSWBinaryClause("program", query.NotIn, []any{"nginx", "envoy"})
	assert.NoError(t, err)
	assert.Equal(t, `-(program:"nginx" OR program:"envoy")`, result)
}

// SolarWinds search cannot test whether a field is empty ("IS EMPTY" / "IS NOT
// EMPTY" match nothing), so the operator is refused rather than silently
// returning no logs.
func TestBuildSWBinaryClause_IsNullOperatorUnsupported(t *testing.T) {
	for _, val := range []any{true, false} {
		result, err := buildSWBinaryClause("hostname", query.IsNull, val)
		assert.Error(t, err)
		assert.Empty(t, result)
	}
	assert.NotContains(t, (&SolarWindsLogSource{}).GetSupportedOperators(), "_is_null")
}

func TestBuildSWBinaryClause_SeverityUppercased(t *testing.T) {
	t.Run("Eq on severity lowercases input uppercased in output", func(t *testing.T) {
		result, err := buildSWBinaryClause("severity", query.Eq, "info")
		assert.NoError(t, err)
		assert.Equal(t, swTestSeverityEq, result)
	})

	t.Run("In on severity uppercases all items", func(t *testing.T) {
		result, err := buildSWBinaryClause("severity", query.In, []any{"warn", "error"})
		assert.NoError(t, err)
		assert.Equal(t, "(severity:WARN OR severity:ERROR)", result)
	})

	t.Run("Contains on severity uppercases value", func(t *testing.T) {
		result, err := buildSWBinaryClause("severity", query.Contains, "err")
		assert.NoError(t, err)
		assert.Equal(t, "severity:ERR", result)
	})
}

// An operator the builder has no case for must be refused. It used to render "",
// which dropped the filter, so a _regex query returned every row in the window.
func TestBuildSWBinaryClause_UnknownOperator(t *testing.T) {
	for _, op := range []query.BinaryWhereClauseType{query.Between, query.Regex, query.NRegex, query.HasKey} {
		result, err := buildSWBinaryClause("hostname", op, "a")
		assert.Error(t, err, op)
		assert.Contains(t, err.Error(), string(op))
		assert.Empty(t, result)
	}
}

// Workloads of every kind: the collector tags a pod with the attribute for its
// owner's kind, so one deployment-only term missed StatefulSet and DaemonSet logs.
func TestBuildSWBinaryClause_WorkloadMatchesEveryKind(t *testing.T) {
	result, err := buildSWBinaryClause(swMapField("app"), query.Eq, "loki")
	require.NoError(t, err)
	assert.Equal(t, `(k8s.deployment.name:"loki" OR k8s.daemonset.name:"loki" OR k8s.statefulset.name:"loki")`, result)

	// The search term is a substring match (it also finds loki-canary), so excluding it
	// would over-exclude; "not loki" is applied to the rows instead.
	result, err = buildSWBinaryClause(swMapField("workload"), query.Nq, "loki")
	require.NoError(t, err)
	assert.Equal(t, "", result)

	result, err = buildSWBinaryClause(swMapField("app"), query.In, []any{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, `((k8s.deployment.name:"a" OR k8s.daemonset.name:"a" OR k8s.statefulset.name:"a") OR `+
		`(k8s.deployment.name:"b" OR k8s.daemonset.name:"b" OR k8s.statefulset.name:"b"))`, result)

	// Case-sensitive contains cannot be checked on a field the rows do not carry.
	_, err = buildSWBinaryClause(swMapField("app"), query.Contains, "lok")
	require.Error(t, err)
	result, err = buildSWBinaryClause(swMapField("app"), query.IContains, "lok")
	require.NoError(t, err)
	assert.Equal(t, `(k8s.deployment.name:lok OR k8s.daemonset.name:lok OR k8s.statefulset.name:lok)`, result)
}

// Node, cluster and service reach the attributes SolarWinds search matches; as bare
// canonical names they matched nothing.
func TestBuildSWBinaryClause_CanonicalK8sFields(t *testing.T) {
	cases := map[string]string{
		"node":    `k8s.node.name:"node-1"`,
		"cluster": `k8s.cluster.name:"node-1"`,
		"service": `service.name:"node-1"`,
	}
	for canonical, want := range cases {
		result, err := buildSWBinaryClause(swMapField(canonical), query.Eq, "node-1")
		require.NoError(t, err, canonical)
		assert.Equal(t, want, result, canonical)
	}
}

// No trace id field has been confirmed in SolarWinds log search, so a trace_id
// filter is refused instead of being sent as a term that matches nothing.
func TestBuildSWBinaryClause_TraceIDRefused(t *testing.T) {
	result, err := buildSWBinaryClause("trace_id", query.Eq, "abc")
	assert.Error(t, err)
	assert.Empty(t, result)
}

// ============================================================================
// buildSWFilterClause
// ============================================================================

func TestBuildSWFilterClause_Empty(t *testing.T) {
	result, err := buildSWFilterClause(query.QueryWhereClause{})
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestBuildSWFilterClause_SingleBinary(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"hostname": {query.Eq: swTestPodABC},
		},
	}
	result, err := buildSWFilterClause(where)
	assert.NoError(t, err)
	assert.Equal(t, `host:"pod-abc"`, result)
}

func TestBuildSWFilterClause_AndSubClauses(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"hostname": {query.Eq: "pod-a"}}},
			{Binary: query.BinaryWhereClause{"severity": {query.Eq: "error"}}},
		},
	}
	result, err := buildSWFilterClause(where)
	assert.NoError(t, err)
	assert.Equal(t, `(host:"pod-a" AND severity:ERROR)`, result)
}

func TestBuildSWFilterClause_OrSubClauses(t *testing.T) {
	where := query.QueryWhereClause{
		Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"hostname": {query.Eq: "pod-a"}}},
			{Binary: query.BinaryWhereClause{"hostname": {query.Eq: "pod-b"}}},
		},
	}
	result, err := buildSWFilterClause(where)
	assert.NoError(t, err)
	assert.Equal(t, `(host:"pod-a" OR host:"pod-b")`, result)
}

func TestBuildSWFilterClause_NotClause(t *testing.T) {
	inner := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"severity": {query.Eq: "debug"}},
	}
	where := query.QueryWhereClause{Not: &inner}
	result, err := buildSWFilterClause(where)
	assert.NoError(t, err)
	assert.Equal(t, "-(severity:DEBUG)", result)
}

func TestBuildSWFilterClause_BinaryAndNot(t *testing.T) {
	inner := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"program": {query.Eq: "sidecar"}},
	}
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"hostname": {query.Eq: "pod-x"}},
		Not:    &inner,
	}
	result, err := buildSWFilterClause(where)
	assert.NoError(t, err)
	// Both parts joined by AND
	assert.Contains(t, result, swTestHostPodX)
	assert.Contains(t, result, `-(program:"sidecar")`)
	assert.Contains(t, result, " AND ")
}

// ============================================================================
// buildLabelValuesFilter
// ============================================================================

// The sampling filter carries only the cascading filters: SolarWinds cannot
// express "<field> IS NOT EMPTY", which made every sample come back empty.
func TestBuildLabelValuesFilter_NoCurrentFilters(t *testing.T) {
	assert.Equal(t, "", buildLabelValuesFilter(nil))
}

func TestBuildLabelValuesFilter_WithCurrentFilters(t *testing.T) {
	result := buildLabelValuesFilter(map[string]string{
		"severity": "INFO",
		"pod":      "pod-a",
	})
	assert.Contains(t, result, swTestSeverityEq)
	assert.Contains(t, result, `host:"pod-a"`)
	assert.Contains(t, result, " AND ")
	assert.NotContains(t, result, "EMPTY")
}

func TestBuildLabelValuesFilter_CanonicalNameMapped(t *testing.T) {
	// "level" in CurrentFilters → mapped to "severity" before applying
	result := buildLabelValuesFilter(map[string]string{
		"level": "error",
	})
	assert.Equal(t, "severity:error", result)
}

// ============================================================================
// buildQueryParams
// ============================================================================

func TestBuildQueryParams_Defaults(t *testing.T) {
	src := &SolarWindsLogSource{}
	params := src.buildQueryParams(FetchLogRequest{})
	assert.Equal(t, "100", params["pageSize"])
	assert.Equal(t, "backward", params["direction"])
	assert.NotContains(t, params, "startTime")
	assert.NotContains(t, params, "endTime")
}

func TestBuildQueryParams_CustomLimit(t *testing.T) {
	src := &SolarWindsLogSource{}
	params := src.buildQueryParams(FetchLogRequest{Limit: 50})
	assert.Equal(t, "50", params["pageSize"])
}

func TestBuildQueryParams_LimitCappedAt1000(t *testing.T) {
	src := &SolarWindsLogSource{}
	params := src.buildQueryParams(FetchLogRequest{Limit: 9999})
	assert.Equal(t, "1000", params["pageSize"])
}

func TestBuildQueryParams_SortTimeAsc(t *testing.T) {
	src := &SolarWindsLogSource{}
	params := src.buildQueryParams(FetchLogRequest{
		SortFields: []SortField{{ColumnName: "time", Order: "asc"}},
	})
	assert.Equal(t, "forward", params["direction"])
}

func TestBuildQueryParams_SortTimestampAscending(t *testing.T) {
	src := &SolarWindsLogSource{}
	params := src.buildQueryParams(FetchLogRequest{
		SortFields: []SortField{{ColumnName: "timestamp", Order: "ascending"}},
	})
	assert.Equal(t, "forward", params["direction"])
}

func TestBuildQueryParams_SortTimeDesc(t *testing.T) {
	src := &SolarWindsLogSource{}
	params := src.buildQueryParams(FetchLogRequest{
		SortFields: []SortField{{ColumnName: "time", Order: "desc"}},
	})
	assert.Equal(t, "backward", params["direction"])
}

func TestBuildQueryParams_TimeRange(t *testing.T) {
	src := &SolarWindsLogSource{}
	params := src.buildQueryParams(FetchLogRequest{
		StartTime: 1672531200000, // 2023-01-01T00:00:00Z
		EndTime:   1672617600000, // 2023-01-02T00:00:00Z
	})
	assert.Equal(t, swTestISO2023, params["startTime"])
	assert.Equal(t, "2023-01-02T00:00:00Z", params["endTime"])
}

// ============================================================================
// buildFilter
// ============================================================================

func TestBuildFilter_WhereClauseTakesPriority(t *testing.T) {
	src := &SolarWindsLogSource{}
	req := FetchLogRequest{
		Query: "raw query string",
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"hostname": {query.Eq: "pod-x"}},
			},
		},
	}
	result, err := src.buildFilter(req)
	assert.NoError(t, err)
	assert.Equal(t, swTestHostPodX, result)
}

func TestBuildFilter_FallsBackToRawQuery(t *testing.T) {
	src := &SolarWindsLogSource{}
	req := FetchLogRequest{Query: swTestHostPodX}
	result, err := src.buildFilter(req)
	assert.NoError(t, err)
	assert.Equal(t, swTestHostPodX, result)
}

func TestBuildFilter_EmptyRequest(t *testing.T) {
	src := &SolarWindsLogSource{}
	result, err := src.buildFilter(FetchLogRequest{})
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

// ============================================================================
// convertSWLogsToOutputLogs
// ============================================================================

func TestConvertSWLogsToOutputLogs_FullEvent(t *testing.T) {
	events := []map[string]any{
		{
			"id":       "log-1",
			"time":     "2023-01-01T00:00:01Z",
			"message":  "server started",
			"hostname": swTestPodABC,
			"severity": "INFO",
			"program":  "nginx",
		},
	}
	out := convertSWLogsToOutputLogs(events)
	require.Len(t, out, 1)
	assert.Equal(t, "2023-01-01T00:00:01Z", out[0].Timestamp)
	assert.Equal(t, "server started", out[0].Message)
	assert.Equal(t, "info", out[0].Severity) // lowercased
	assert.Equal(t, swTestPodABC, out[0].Labels["hostname"])
	assert.Equal(t, "nginx", out[0].Labels["program"])
	assert.Equal(t, "log-1", out[0].Labels["id"])
}

func TestConvertSWLogsToOutputLogs_SeverityLowercased(t *testing.T) {
	events := []map[string]any{
		{"message": "crash", "severity": "ERROR"},
	}
	out := convertSWLogsToOutputLogs(events)
	require.Len(t, out, 1)
	assert.Equal(t, "error", out[0].Severity)
}

func TestConvertSWLogsToOutputLogs_MissingSeverityInferred(t *testing.T) {
	events := []map[string]any{
		{"message": "fatal: out of memory"},
	}
	out := convertSWLogsToOutputLogs(events)
	require.Len(t, out, 1)
	assert.Equal(t, "error", out[0].Severity)
}

func TestConvertSWLogsToOutputLogs_EmptyInput(t *testing.T) {
	out := convertSWLogsToOutputLogs(nil)
	assert.Empty(t, out)
}

// ============================================================================
// extractDistinctFieldValues
// ============================================================================

func TestExtractDistinctFieldValues_Deduplication(t *testing.T) {
	entries := []map[string]any{
		{"hostname": "pod-a"},
		{"hostname": "pod-b"},
		{"hostname": "pod-a"}, // duplicate
	}
	vals := extractDistinctFieldValues(entries, "hostname")
	assert.Len(t, vals, 2)
	values := []string{vals[0].Value, vals[1].Value}
	sort.Strings(values)
	assert.Equal(t, []string{"pod-a", "pod-b"}, values)
}

func TestExtractDistinctFieldValues_MissingField(t *testing.T) {
	entries := []map[string]any{
		{"program": "nginx"},
	}
	vals := extractDistinctFieldValues(entries, "hostname")
	assert.Empty(t, vals)
}

func TestExtractDistinctFieldValues_NilValue(t *testing.T) {
	entries := []map[string]any{
		{"hostname": nil},
	}
	vals := extractDistinctFieldValues(entries, "hostname")
	assert.Empty(t, vals)
}

func TestExtractDistinctFieldValues_NumericValue(t *testing.T) {
	entries := []map[string]any{
		{"duration": float64(42)},
	}
	vals := extractDistinctFieldValues(entries, "duration")
	require.Len(t, vals, 1)
	assert.Equal(t, "42", vals[0].Value)
}

// ============================================================================
// stringsToLabelValues
// ============================================================================

func TestStringsToLabelValues(t *testing.T) {
	out := stringsToLabelValues([]string{"a", "b"})
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Value)
	assert.Equal(t, "b", out[1].Value)
	assert.NotNil(t, out[0].Attributes)
}

func TestStringsToLabelValues_Nil(t *testing.T) {
	out := stringsToLabelValues(nil)
	assert.Empty(t, out)
}

// ============================================================================
// QueryLabels — static, no mocks needed
// ============================================================================

func TestSolarWindsSource_QueryLabels(t *testing.T) {
	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabels(ctx, FetchLogLabelRequest{AccountId: "acc-1"})
	assert.NoError(t, err)
	require.Len(t, out, 10)
	labels := make([]string, 0, len(out))
	for _, l := range out {
		labels = append(labels, l.Label)
	}
	sort.Strings(labels)
	// Every field a canonical label maps to is listed, so the picker and the
	// empty-result diagnosis both know it.
	assert.Equal(t, []string{
		"hostname", "k8s.cluster.name", "k8s.daemonset.name", "k8s.deployment.name", "k8s.namespace.name",
		"k8s.node.name", "k8s.statefulset.name", "program", "service.name", "severity",
	}, labels)
}

// ============================================================================
// querySWEntityNames
// ============================================================================

func TestQuerySWEntityNames_Success(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		body := `{"entities":[{"name":"pod-a"},{"name":"pod-b"},{"name":"pod-a"}]}`
		return []byte(body), http.StatusOK, nil
	}

	names, err := querySWEntityNames("token", swTestBaseURL, "KubernetesPod")
	assert.NoError(t, err)
	assert.Len(t, names, 2) // pod-a deduplicated
	sort.Strings(names)
	assert.Equal(t, []string{"pod-a", "pod-b"}, names)
}

func TestQuerySWEntityNames_EmptyEntities(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		return []byte(`{"entities":[]}`), http.StatusOK, nil
	}

	names, err := querySWEntityNames("token", swTestBaseURL, "KubernetesPod")
	assert.NoError(t, err)
	assert.Nil(t, names)
}

func TestQuerySWEntityNames_EmptyNameSkipped(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		return []byte(`{"entities":[{"name":""},{"name":"pod-a"}]}`), http.StatusOK, nil
	}

	names, err := querySWEntityNames("token", swTestBaseURL, "KubernetesPod")
	assert.NoError(t, err)
	assert.Equal(t, []string{"pod-a"}, names)
}

func TestQuerySWEntityNames_Non200Status(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		return []byte(`{"code":"Unauthorized"}`), http.StatusUnauthorized, nil
	}

	_, err := querySWEntityNames("bad-token", swTestBaseURL, "KubernetesPod")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestQuerySWEntityNames_NetworkError(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		return nil, 0, errors.New("connection refused")
	}

	_, err := querySWEntityNames("token", swTestBaseURL, "KubernetesPod")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

// ============================================================================
// QueryLabelValues
// ============================================================================

// stubSWConfigs sets solarWindsGetConfigs to return a fake token + ap-01.
// Returns a restore function; call it with defer.
func stubSWConfigs() func() {
	orig := solarWindsGetConfigs
	solarWindsGetConfigs = func(ctx *security.RequestContext, accountId string) (string, string, error) {
		return "fake-token", "ap-01", nil
	}
	return func() { solarWindsGetConfigs = orig }
}

func TestSolarWindsSource_QueryLabelValues_Severity(t *testing.T) {
	defer stubSWConfigs()()
	// solarWindsDoGET must NOT be called for severity
	callCount := 0
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		callCount++
		return nil, 0, errors.New("should not be called")
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "severity"})
	assert.NoError(t, err)
	assert.Equal(t, 0, callCount)
	require.Len(t, out, 5)
	vals := make([]string, len(out))
	for i, v := range out {
		vals[i] = v.Value
	}
	assert.ElementsMatch(t, []string{"DEBUG", "INFO", "WARN", "ERROR", "CRITICAL"}, vals)
}

func TestSolarWindsSource_QueryLabelValues_SeverityViaCanonicalAlias(t *testing.T) {
	defer stubSWConfigs()()

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	// "level" maps to "severity" via swMapField
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "level"})
	assert.NoError(t, err)
	assert.Len(t, out, 5)
}

func TestSolarWindsSource_QueryLabelValues_HostnameFromEntities(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		assert.Equal(t, swTestEntitiesP, path)
		assert.Equal(t, "KubernetesPod", params["type"])
		body := `{"entities":[{"name":"pod-a"},{"name":"pod-b"}]}`
		return []byte(body), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "hostname"})
	assert.NoError(t, err)
	require.Len(t, out, 2)
	vals := []string{out[0].Value, out[1].Value}
	sort.Strings(vals)
	assert.Equal(t, []string{"pod-a", "pod-b"}, vals)
}

func TestSolarWindsSource_QueryLabelValues_HostnameViaCanonicalAlias(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		body := `{"entities":[{"name":"pod-x"}]}`
		return []byte(body), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	// "pod" → "hostname" via swMapField
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "pod"})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "pod-x", out[0].Value)
}

func TestSolarWindsSource_QueryLabelValues_HostnameFallsBackWhenEntitiesEmpty(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()

	callPaths := []string{}
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		callPaths = append(callPaths, path)
		if path == swTestEntitiesP {
			return []byte(`{"entities":[]}`), http.StatusOK, nil
		}
		// Fallback log sampling
		return []byte(`{"logs":[{"hostname":"pod-fallback"}]}`), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "hostname"})
	assert.NoError(t, err)
	assert.Contains(t, callPaths, swTestEntitiesP)
	assert.Contains(t, callPaths, "/v1/logs")
	require.Len(t, out, 1)
	assert.Equal(t, "pod-fallback", out[0].Value)
}

func TestSolarWindsSource_QueryLabelValues_HostnameFallsBackOnEntitiesError(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			return nil, 0, errors.New("API unavailable")
		}
		return []byte(`{"logs":[{"hostname":"pod-fallback"}]}`), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "hostname"})
	assert.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "pod-fallback", out[0].Value)
}

func TestSolarWindsSource_QueryLabelValues_ProgramFromEntitiesDeduped(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		assert.Equal(t, "KubernetesContainer", params["type"])
		// "nginx" appears twice — should be deduplicated
		body := `{"entities":[{"name":"nginx"},{"name":"envoy"},{"name":"nginx"}]}`
		return []byte(body), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "program"})
	assert.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestSolarWindsSource_QueryLabelValues_ProgramViaCanonicalAlias(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		body := `{"entities":[{"name":"nginx"}]}`
		return []byte(body), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	// "container" → "program" via swMapField
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "container"})
	assert.NoError(t, err)
	assert.Len(t, out, 1)
}

func TestSolarWindsSource_QueryLabelValues_NamespaceFromEntities(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		assert.Equal(t, swTestEntitiesP, path)
		assert.Equal(t, "KubernetesNamespace", params["type"])
		return []byte(`{"entities":[{"name":"demo"},{"name":"kube-system"}]}`), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	// canonical "namespace" → "k8s.namespace.name" via swMapField
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "namespace"})
	assert.NoError(t, err)
	require.Len(t, out, 2)
	assert.ElementsMatch(t, []string{"demo", "kube-system"}, []string{out[0].Value, out[1].Value})
}

func TestSolarWindsSource_QueryLabelValues_WorkloadFromEntities(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		assert.Equal(t, swTestEntitiesP, path)
		switch params["type"] {
		case "KubernetesDeployment":
			return []byte(`{"entities":[{"name":"hotrod"}]}`), http.StatusOK, nil
		case "KubernetesStatefulSet":
			return []byte(`{"entities":[{"name":"db"}]}`), http.StatusOK, nil
		case "KubernetesDaemonSet":
			return []byte(`{"entities":[{"name":"agent"},{"name":"hotrod"}]}`), http.StatusOK, nil
		}
		t.Errorf("unexpected entity type %q", params["type"])
		return nil, http.StatusBadRequest, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	// canonical "app" → every workload kind
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "app"})
	assert.NoError(t, err)
	values := make([]string, 0, len(out))
	for _, v := range out {
		values = append(values, v.Value)
	}
	assert.ElementsMatch(t, []string{"hotrod", "db", "agent"}, values)
}

func TestSolarWindsSource_QueryLabelValues_NodeAndClusterFromEntities(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		assert.Equal(t, swTestEntitiesP, path)
		return []byte(`{"entities":[{"name":"` + params["type"] + `-1"}]}`), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	for label, want := range map[string]string{"node": "KubernetesNode-1", "cluster": "KubernetesCluster-1"} {
		out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: label})
		require.NoError(t, err, label)
		require.Len(t, out, 1, label)
		assert.Equal(t, want, out[0].Value)
	}
}

func TestSolarWindsSource_QueryLabelValues_UnknownFieldUsesLogSampling(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		assert.Equal(t, "/v1/logs", path)
		assert.NotContains(t, params, "filter")
		return []byte(`{"logs":[{"custom_field":"val1"},{"custom_field":"val2"}]}`), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "custom_field"})
	assert.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestSolarWindsSource_QueryLabelValues_FallbackAppliesTimeRange(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()

	var capturedParams map[string]string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		capturedParams = params
		return []byte(`{"logs":[]}`), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	out, err := src.QueryLabelValues(ctx, FetchLogLabelValuesRequest{
		AccountId: "acc-1",
		LabelName: "custom_field",
		StartTime: 1672531200000, // 2023-01-01T00:00:00Z
		EndTime:   1672617600000, // 2023-01-02T00:00:00Z
	})
	assert.NoError(t, err)
	assert.Empty(t, out)
	assert.Equal(t, swTestISO2023, capturedParams["startTime"])
	assert.Equal(t, "2023-01-02T00:00:00Z", capturedParams["endTime"])
}

// ============================================================================
// swExtractMsgForHash
// ============================================================================

func TestSWExtractMsgForHash_PlainText(t *testing.T) {
	// Non-JSON: returned as-is.
	assert.Equal(t, "server crashed", swExtractMsgForHash("server crashed"))
}

func TestSWExtractMsgForHash_JSONWithMsgField(t *testing.T) {
	// Go-style structured log: extracts "msg".
	raw := `{"time":"2024-01-01T00:00:00Z","level":"error","msg":"Server error","trace":"abc"}`
	assert.Equal(t, "Server error", swExtractMsgForHash(raw))
}

func TestSWExtractMsgForHash_JSONWithMessageField(t *testing.T) {
	// Python-style structured log: falls back to "message".
	raw := `{"timestamp":"2024-01-01","level":"ERROR","message":"DB connection failed"}`
	assert.Equal(t, "DB connection failed", swExtractMsgForHash(raw))
}

func TestSWExtractMsgForHash_StripsTrailingJSONArgs(t *testing.T) {
	// Non-JSON line with trailing JSON argument blob stripped.
	raw := `Executing query {"table":"users","limit":100}`
	assert.Equal(t, "Executing query", swExtractMsgForHash(raw))
}

func TestSWExtractMsgForHash_EmptyString(t *testing.T) {
	assert.Equal(t, "", swExtractMsgForHash(""))
}

// ============================================================================
// groupSWLogsByPattern
// ============================================================================

func TestGroupSWLogsByPattern_PatternHashMatchesGroupingHash(t *testing.T) {
	// For JSON-format logs the PatternHash must equal
	// generatePatternHash(swExtractMsgForHash(message)) — the same normalization
	// used when building the composite grouping key.  Before the fix PatternHash
	// was computed from the raw sample, so it differed from the grouping hash and
	// broke ticket linking.
	rawMsg := `{"time":"2024-01-01T00:00:00Z","level":"error","msg":"DB connection failed","trace":"xyz"}`
	logs := []OutputLog{
		{Message: rawMsg, Severity: "error", Labels: map[string]any{"hostname": "myapp-abc12-xyz98", "program": "api"}},
	}
	podNamespaces := map[string]string{"myapp-abc12-xyz98": "production"}

	out := groupSWLogsByPattern(logs, podNamespaces, "", "", 0)
	require.Len(t, out.Groups, 1)

	wantHash := generatePatternHash(swExtractMsgForHash(rawMsg))
	assert.Equal(t, wantHash, out.Groups[0].PatternHash,
		"PatternHash must match the hash used for grouping (extracted message, not raw sample)")
}

func TestGroupSWLogsByPattern_NamespaceFilter(t *testing.T) {
	// When selectedNamespace is set, only groups from that namespace are returned.
	logs := []OutputLog{
		{Message: "error in prod", Severity: "error", Labels: map[string]any{"hostname": "svc-a-abc12-xyz98", "program": "api"}},
		{Message: "error in staging", Severity: "error", Labels: map[string]any{"hostname": "svc-b-abc12-xyz98", "program": "api"}},
	}
	podNamespaces := map[string]string{
		"svc-a-abc12-xyz98": "production",
		"svc-b-abc12-xyz98": "staging",
	}

	out := groupSWLogsByPattern(logs, podNamespaces, "production", "", 0)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, "production", out.Groups[0].Namespace)
}

func TestGroupSWLogsByPattern_NoNamespaceFilterReturnsAll(t *testing.T) {
	// Empty selectedNamespace returns groups from all namespaces.
	logs := []OutputLog{
		{Message: "error alpha", Severity: "error", Labels: map[string]any{"hostname": "svc-a-abc12-xyz98", "program": "api"}},
		{Message: "error beta", Severity: "error", Labels: map[string]any{"hostname": "svc-b-abc12-xyz98", "program": "api"}},
	}
	podNamespaces := map[string]string{
		"svc-a-abc12-xyz98": "production",
		"svc-b-abc12-xyz98": "staging",
	}

	out := groupSWLogsByPattern(logs, podNamespaces, "", "", 0)
	assert.Len(t, out.Groups, 2)
}

func TestGroupSWLogsByPattern_WorkloadFilter(t *testing.T) {
	// When selectedWorkload is set, only groups from that workload are returned.
	logs := []OutputLog{
		{Message: "error in svc-a", Severity: "error", Labels: map[string]any{"hostname": "svc-a-abc12-xyz98", "program": "api"}},
		{Message: "error in svc-b", Severity: "error", Labels: map[string]any{"hostname": "svc-b-abc12-xyz98", "program": "api"}},
	}
	podNamespaces := map[string]string{
		"svc-a-abc12-xyz98": "production",
		"svc-b-abc12-xyz98": "production",
	}

	out := groupSWLogsByPattern(logs, podNamespaces, "", "svc-a", 0)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, "svc-a", out.Groups[0].Workload)
}

func TestGroupSWLogsByPattern_EmptyLogs(t *testing.T) {
	out := groupSWLogsByPattern(nil, nil, "", "", 0)
	assert.Empty(t, out.Groups)
}

func TestGroupSWLogsByPattern_GroupsByPattern(t *testing.T) {
	// Two logs with the same message → merged into one group with count=2.
	msg := "connection refused"
	logs := []OutputLog{
		{Message: msg, Severity: "error", Labels: map[string]any{"hostname": "svc-abc12-xyz98", "program": "app"}},
		{Message: msg, Severity: "error", Labels: map[string]any{"hostname": "svc-abc12-xyz98", "program": "app"}},
	}
	out := groupSWLogsByPattern(logs, nil, "", "", 0)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, int64(2), out.Groups[0].Count)
}

// ============================================================================
// QueryLogGroup — workload filter is a host prefix match
// ============================================================================

func TestQueryLogGroup_WorkloadFilterUsesPrefix(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()

	var capturedFilter string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == "/v1/logs" {
			capturedFilter = params["filter"]
			return []byte(`{"logs":[]}`), http.StatusOK, nil
		}
		// entities API for pod namespaces
		return []byte(`{"entities":[]}`), http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	ctx := mockRequestContext()
	_, err := src.QueryLogGroup(ctx, FetchLogGroupRequest{
		AccountId: "acc-1",
		Request:   map[string]any{"selectedWorkload": "my-service"},
	})
	assert.NoError(t, err)
	// "host:<workload>" already matches every pod name that starts with it; a
	// trailing "*" or the response field name "hostname" matches nothing.
	assert.Contains(t, capturedFilter, "AND host:my-service")
	assert.NotContains(t, capturedFilter, "*")
	assert.NotContains(t, capturedFilter, "hostname:")
}
