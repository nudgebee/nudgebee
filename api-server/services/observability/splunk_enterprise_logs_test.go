package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- escaping and identifier safety ---------------------------------------

func TestEscapeSplunkString(t *testing.T) {
	// The asterisk is the interesting one: unescaped it is an SPL wildcard, so a literal
	// value containing it would silently widen the filter instead of failing closed.
	assert.Equal(t, `pod\*name`, escapeSplunkString("pod*name"))
	assert.Equal(t, `say \"hi\"`, escapeSplunkString(`say "hi"`))
	assert.Equal(t, `a\\b`, escapeSplunkString(`a\b`))
	assert.Equal(t, "one two", escapeSplunkString("one\ntwo"))
	assert.Equal(t, "plain", escapeSplunkString("plain"))
}

// Backslash must be escaped before the quote and star, or the escapes introduced for
// those would themselves be escaped and the literal would break.
func TestEscapeSplunkString_OrderIsBackslashFirst(t *testing.T) {
	assert.Equal(t, `\\\"`, escapeSplunkString(`\"`))
}

func TestIsSafeSplunkFieldName(t *testing.T) {
	for _, name := range []string{"k8s.pod.name", "host.name", "_raw", "_time", "severity_text", "otel:scope", "my-field"} {
		assert.True(t, isSafeSplunkFieldName(name), "%q should be accepted", name)
	}
	for _, name := range []string{"", `foo" | delete`, "foo bar", "foo|bar", "foo(", "foo'", strings.Repeat("a", 256)} {
		assert.False(t, isSafeSplunkFieldName(name), "%q should be rejected", name)
	}
}

// ----- pipeline splitting and the read-only guard ---------------------------

func TestSplunkEnterpriseSplitPipeline_IgnoresPipesInsideQuotes(t *testing.T) {
	segments := splunkEnterpriseSplitPipeline(`search index="main" msg="a|b" | head 10`)
	require.Len(t, segments, 2)
	assert.Contains(t, segments[0], `msg="a|b"`)
	assert.Contains(t, segments[1], "head 10")
}

func TestSplunkEnterpriseSplitPipeline_EscapedQuoteDoesNotToggle(t *testing.T) {
	segments := splunkEnterpriseSplitPipeline(`search index="main" msg="say \"hi\" | now" | head 5`)
	require.Len(t, segments, 2)
	assert.Contains(t, segments[1], "head 5")
}

func TestValidateSplunkEnterpriseQuery_AcceptsReadOnlySearches(t *testing.T) {
	for _, spl := range []string{
		`search index="otel_logs" | head 100`,
		`search index="otel_logs" k8s.namespace.name="prod" | stats count by k8s.pod.name`,
		`search index="otel_logs" | top limit=100 k8s.pod.name | fields k8s.pod.name`,
	} {
		assert.NoError(t, validateSplunkEnterpriseQuery(spl), "%q should be accepted", spl)
	}
}

// A leading pipe is a generating command, which ignores the index scope entirely.
func TestValidateSplunkEnterpriseQuery_RejectsLeadingPipe(t *testing.T) {
	err := validateSplunkEnterpriseQuery(`| rest /services/authentication/users`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generating command")
}

func TestValidateSplunkEnterpriseQuery_RejectsWritingAndExfilCommands(t *testing.T) {
	for _, spl := range []string{
		`search index="otel_logs" | delete`,
		`search index="otel_logs" | outputlookup stolen.csv`,
		`search index="otel_logs" | collect index=elsewhere`,
		`search index="otel_logs" | sendemail to="attacker@example.com"`,
		`search index="otel_logs" | script python evil`,
		`search index="otel_logs" | rest /services/authentication/users`,
		`search index="otel_logs" | savedsearch privileged`,
		// map runs a fresh search per result, so its argument is SPL the guard never sees.
		`search index="otel_logs" | map search="search index=other"`,
		`search index="otel_logs" | loadjob savedsearch="admin:search:privileged"`,
	} {
		err := validateSplunkEnterpriseQuery(spl)
		require.Error(t, err, "%q must be rejected", spl)
		assert.Contains(t, err.Error(), "may only read")
	}
}

// A denylisted word inside a quoted search term is data, not a command.
func TestValidateSplunkEnterpriseQuery_AllowsCommandNameAsSearchTerm(t *testing.T) {
	assert.NoError(t, validateSplunkEnterpriseQuery(`search index="otel_logs" message="delete failed" | head 10`))
}

// The pipe splitter is agnostic to brackets, so a write command hidden in a subsearch
// still surfaces as its own segment and is rejected.
func TestValidateSplunkEnterpriseQuery_RejectsCommandInSubsearch(t *testing.T) {
	err := validateSplunkEnterpriseQuery(`search index="otel_logs" [ search index="other" | delete ]`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "may only read")
}

func TestValidateSplunkEnterpriseQuery_RejectsEmpty(t *testing.T) {
	assert.Error(t, validateSplunkEnterpriseQuery("   "))
}

// ----- where-clause construction --------------------------------------------

func TestBuildSplunkEnterpriseWhereClause_Operators(t *testing.T) {
	cases := []struct {
		name string
		op   query.BinaryWhereClauseType
		want string
	}{
		{"eq", query.Eq, `k8s.namespace.name="prod"`},
		{"neq", query.Nq, `NOT k8s.namespace.name="prod"`},
		{"contains", query.Contains, `k8s.namespace.name="*prod*"`},
		{"ilike", query.ILike, `k8s.namespace.name="*prod*"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where := query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {tc.op: "prod"},
				},
			}
			got, err := buildSplunkEnterpriseWhereClause(where)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildSplunkEnterpriseWhereClause_EscapesValues(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"pod": {query.Eq: `evil" | delete`},
		},
	}
	got, err := buildSplunkEnterpriseWhereClause(where)
	require.NoError(t, err)

	// The quote is neutralized, so the value stays inside the literal and the guard is
	// never even reached.
	assert.Equal(t, `k8s.pod.name="evil\" | delete"`, got)
	assert.NoError(t, validateSplunkEnterpriseQuery(`search index="main" `+got))
}

func TestBuildSplunkEnterpriseWhereClause_RejectsUnsafeFieldName(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			`bad" | delete`: {query.Eq: "x"},
		},
	}
	_, err := buildSplunkEnterpriseWhereClause(where)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe field name")
}

func TestBuildSplunkEnterpriseWhereClause_RejectsUnsupportedOperator(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"pod": {query.Gt: "x"},
		},
	}
	_, err := buildSplunkEnterpriseWhereClause(where)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported binary operator")
}

func TestBuildSplunkEnterpriseWhereClause_AndOrNot(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "prod"}}},
			{Or: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"pod": {query.Eq: "a"}}},
				{Binary: query.BinaryWhereClause{"pod": {query.Eq: "b"}}},
			}},
		},
	}
	got, err := buildSplunkEnterpriseWhereClause(where)
	require.NoError(t, err)
	assert.Equal(t, `(k8s.namespace.name="prod" AND (k8s.pod.name="a" OR k8s.pod.name="b"))`, got)

	notWhere := query.QueryWhereClause{
		Not: &query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "kube-system"}}},
	}
	gotNot, err := buildSplunkEnterpriseWhereClause(notWhere)
	require.NoError(t, err)
	assert.Equal(t, `NOT (k8s.namespace.name="kube-system")`, gotNot)
}

// Map iteration is random; the builder sorts so the rendered SPL is stable enough to
// assert on and to diff in logs.
func TestBuildSplunkEnterpriseWhereClause_DeterministicFieldOrder(t *testing.T) {
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"namespace": {query.Eq: "prod"},
			"pod":       {query.Eq: "api-0"},
			"container": {query.Eq: "api"},
		},
	}
	first, err := buildSplunkEnterpriseWhereClause(where)
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		again, err := buildSplunkEnterpriseWhereClause(where)
		require.NoError(t, err)
		assert.Equal(t, first, again)
	}
}

// ----- SPL assembly ---------------------------------------------------------

func TestSplunkEnterpriseLogSource_BuildSPL(t *testing.T) {
	s := &SplunkEnterpriseLogSource{}
	req := FetchLogRequest{
		Limit: 25,
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"namespace": {query.Eq: "prod"}},
			},
		},
	}

	spl, err := s.buildSPL(req, "otel_logs")
	require.NoError(t, err)
	assert.Equal(t, `search index="otel_logs" k8s.namespace.name="prod" | head 25 | fields *`, spl)
}

func TestSplunkEnterpriseLogSource_BuildSPL_NoFilters(t *testing.T) {
	s := &SplunkEnterpriseLogSource{}
	spl, err := s.buildSPL(FetchLogRequest{}, "main")
	require.NoError(t, err)
	assert.Equal(t, `search index="main" | head 100 | fields *`, spl)
}

func TestSplunkEnterpriseLogSource_BuildSPL_RejectsUnsafeIndex(t *testing.T) {
	s := &SplunkEnterpriseLogSource{}
	_, err := s.buildSPL(FetchLogRequest{}, `main" | delete`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe index name")
}

func TestSplunkEnterpriseLimit(t *testing.T) {
	assert.Equal(t, splunkEnterpriseDefaultLimit, splunkEnterpriseLimit(0))
	assert.Equal(t, splunkEnterpriseDefaultLimit, splunkEnterpriseLimit(-5))
	assert.Equal(t, 42, splunkEnterpriseLimit(42))
	assert.Equal(t, splunkEnterpriseMaxLimit, splunkEnterpriseLimit(999999))
}

// ----- value / time formatting ----------------------------------------------

func TestFormatSplunkValue(t *testing.T) {
	assert.Equal(t, "", formatSplunkValue(nil))
	assert.Equal(t, "hello", formatSplunkValue("hello"))
	assert.Equal(t, "true", formatSplunkValue(true))
	assert.Equal(t, "a, b", formatSplunkValue([]any{"a", "b"}))
	// json.Number keeps the literal digits rather than round-tripping through float64.
	assert.Equal(t, "1786612792471605", formatSplunkValue(json.Number("1786612792471605")))
}

func TestSplunkEnterpriseTimestamp(t *testing.T) {
	assert.Equal(t, "", splunkEnterpriseTimestamp(""))
	assert.Equal(t, "2026-08-26T10:11:12Z", splunkEnterpriseTimestamp("2026-08-26T10:11:12+00:00"))
	assert.Equal(t, "2026-08-26T10:11:12.5Z", splunkEnterpriseTimestamp("2026-08-26T10:11:12.500+00:00"))
	// Unparseable input is passed through rather than blanked.
	assert.Equal(t, "not-a-time", splunkEnterpriseTimestamp("not-a-time"))
}

func TestSplunkEnterpriseTimeRangeSeconds_DefaultsToLastHour(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	start, end := splunkEnterpriseTimeRangeSeconds(0, 0, now)
	assert.Equal(t, "1787745600.000", end, "end defaults to now")
	assert.Equal(t, "1787742000.000", start, "start defaults to one hour before now")

	// Guard the arithmetic rather than just the constants: the window must be exactly an
	// hour wide regardless of what `now` is.
	startF, err := strconv.ParseFloat(start, 64)
	require.NoError(t, err)
	endF, err := strconv.ParseFloat(end, 64)
	require.NoError(t, err)
	assert.InDelta(t, 3600.0, endF-startF, 0.001)
	assert.InDelta(t, float64(now.UnixMilli())/1000.0, endF, 0.001)
}

func TestSplunkEnterpriseTimeRangeSeconds_UsesRequestWindow(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	start, end := splunkEnterpriseTimeRangeSeconds(1787821200500, 1787824800250, now)
	assert.Equal(t, "1787821200.500", start)
	assert.Equal(t, "1787824800.250", end)
}

// ----- end-to-end over a stub Splunk ----------------------------------------

func patchSplunkEnterpriseConfig(t *testing.T, serverURL string) *gomonkey.Patches {
	t.Helper()
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(integrations.GetSplunkEnterpriseConfig,
		func(_ *security.RequestContext, _ string) (integrations.SplunkEnterpriseConfig, error) {
			return integrations.SplunkEnterpriseConfig{
				URL:      serverURL,
				AuthType: integrations.SplunkEnterpriseAuthToken,
				Token:    "tok",
				LogIndex: "otel_logs",
				App:      integrations.SplunkEnterpriseDefaultApp,
			}, nil
		})
	return patches
}

func TestSplunkEnterpriseLogSource_QueryLogs(t *testing.T) {
	var gotSearch, gotExecMode, gotOutputMode, gotEarliest string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/servicesNS/-/search/search/v2/jobs", r.URL.Path)
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		require.NoError(t, r.ParseForm())

		gotSearch = r.Form.Get("search")
		gotExecMode = r.Form.Get("exec_mode")
		gotOutputMode = r.Form.Get("output_mode")
		gotEarliest = r.Form.Get("earliest_time")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"messages": [],
			"results": [
				{
					"_time": "2026-08-26T10:11:12.000+00:00",
					"_raw": "connection refused",
					"severity_text": "ERROR",
					"k8s.pod.name": "api-0"
				}
			]
		}`))
	}))
	defer server.Close()

	patches := patchSplunkEnterpriseConfig(t, server.URL)
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	logs, err := s.QueryLogs(&security.RequestContext{}, FetchLogRequest{
		AccountId: "acct",
		StartTime: 1787821200000,
		EndTime:   1787824800000,
		Limit:     10,
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"namespace": {query.Eq: "prod"}},
			},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, `search index="otel_logs" k8s.namespace.name="prod" | head 10 | fields *`, gotSearch)
	assert.Equal(t, "oneshot", gotExecMode)
	assert.Equal(t, "json", gotOutputMode)
	assert.Equal(t, "1787821200.000", gotEarliest)

	require.Len(t, logs, 1)
	assert.Equal(t, "2026-08-26T10:11:12Z", logs[0].Timestamp)
	assert.Equal(t, "connection refused", logs[0].Message)
	assert.Equal(t, "ERROR", logs[0].Severity)
	assert.Equal(t, "api-0", logs[0].Labels["k8s.pod.name"])
}

// FetchLogs assigns the GetQuery result (or a user's code-mode SPL) to Query before
// calling QueryLogs, so a populated Query must be what actually runs.
func TestSplunkEnterpriseLogSource_QueryLogs_HonoursSuppliedQuery(t *testing.T) {
	var gotSearch string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotSearch = r.Form.Get("search")
		_, _ = w.Write([]byte(`{"messages": [], "results": []}`))
	}))
	defer server.Close()

	patches := patchSplunkEnterpriseConfig(t, server.URL)
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	_, err := s.QueryLogs(&security.RequestContext{}, FetchLogRequest{
		AccountId: "acct",
		Query:     `search index="otel_logs" error | head 5`,
	})
	require.NoError(t, err)
	assert.Equal(t, `search index="otel_logs" error | head 5`, gotSearch)
}

func TestSplunkEnterpriseLogSource_QueryLogs_RejectsDangerousSuppliedQuery(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"messages": [], "results": []}`))
	}))
	defer server.Close()

	patches := patchSplunkEnterpriseConfig(t, server.URL)
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	_, err := s.QueryLogs(&security.RequestContext{}, FetchLogRequest{
		AccountId: "acct",
		Query:     `search index="otel_logs" | delete`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "may only read")
	assert.False(t, called, "the request must never reach Splunk")
}

// Splunk reports a broken search inside a 200 response; without inspecting messages the
// caller would see "no logs" for what is actually a failed query.
func TestSplunkEnterpriseLogSource_QueryLogs_SurfacesFatalMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"messages": [{"type": "FATAL", "text": "Unknown search command 'searhc'."}],
			"results": []
		}`))
	}))
	defer server.Close()

	patches := patchSplunkEnterpriseConfig(t, server.URL)
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	_, err := s.QueryLogs(&security.RequestContext{}, FetchLogRequest{AccountId: "acct"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unknown search command")
}

func TestSplunkEnterpriseLogSource_QueryLogs_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"messages": [{"type": "ERROR", "text": "bad search"}]}`))
	}))
	defer server.Close()

	patches := patchSplunkEnterpriseConfig(t, server.URL)
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	_, err := s.QueryLogs(&security.RequestContext{}, FetchLogRequest{AccountId: "acct"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestSplunkEnterpriseLogSource_QueryLabelValues(t *testing.T) {
	var gotSearch string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotSearch = r.Form.Get("search")
		_, _ = w.Write([]byte(`{
			"messages": [],
			"results": [
				{"k8s.namespace.name": "prod"},
				{"k8s.namespace.name": "staging"},
				{"k8s.namespace.name": ""}
			]
		}`))
	}))
	defer server.Close()

	patches := patchSplunkEnterpriseConfig(t, server.URL)
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	values, err := s.QueryLabelValues(&security.RequestContext{}, FetchLogLabelValuesRequest{
		AccountId: "acct",
		LabelName: "namespace",
	})
	require.NoError(t, err)

	assert.Equal(t, `search index="otel_logs" k8s.namespace.name=* | top limit=100 k8s.namespace.name | fields k8s.namespace.name`, gotSearch)
	require.Len(t, values, 2, "blank values are dropped from the dropdown")
	assert.Equal(t, "prod", values[0].Value)
	assert.Equal(t, "staging", values[1].Value)
}

func TestSplunkEnterpriseLogSource_QueryLabelValues_RejectsUnsafeLabel(t *testing.T) {
	patches := patchSplunkEnterpriseConfig(t, "http://127.0.0.1:1")
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	_, err := s.QueryLabelValues(&security.RequestContext{}, FetchLogLabelValuesRequest{
		AccountId: "acct",
		LabelName: `bad" | delete`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe label name")
}

func TestSplunkEnterpriseLogSource_QueryLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"messages": [],
			"results": [
				{"_time": "2026-08-26T10:11:12.000+00:00", "_raw": "a", "k8s.pod.name": "api-0"},
				{"_time": "2026-08-26T10:11:13.000+00:00", "_raw": "b", "k8s.namespace.name": "prod"}
			]
		}`))
	}))
	defer server.Close()

	patches := patchSplunkEnterpriseConfig(t, server.URL)
	defer patches.Reset()

	s := &SplunkEnterpriseLogSource{}
	labels, err := s.QueryLabels(&security.RequestContext{}, FetchLogLabelRequest{AccountId: "acct"})
	require.NoError(t, err)

	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Label)
	}
	// Sorted union of the keys across the sample.
	assert.Equal(t, []string{"_raw", "_time", "k8s.namespace.name", "k8s.pod.name"}, names)
}

// ----- interface conformance -------------------------------------------------

func TestSplunkEnterpriseLogSource_ImplementsLogSource(t *testing.T) {
	var _ LogSource = &SplunkEnterpriseLogSource{}
	var _ QueryRequestKeyFilter = &SplunkEnterpriseLogSource{}
}

func TestSplunkEnterpriseLogSource_SupportedOperators(t *testing.T) {
	ops := (&SplunkEnterpriseLogSource{}).GetSupportedOperators()
	assert.ElementsMatch(t, []string{"_eq", "_neq", "_contains", "_ilike"}, ops)

	// Every advertised operator must actually build, or the builder UI offers a filter
	// that fails at query time.
	for _, op := range ops {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"pod": {query.BinaryWhereClauseType(op): "x"}},
		}
		_, err := buildSplunkEnterpriseWhereClause(where)
		assert.NoError(t, err, "advertised operator %q must be supported by the builder", op)
	}
}

func TestSplunkEnterpriseLogSource_LabelMappingCoversCanonicalFields(t *testing.T) {
	mapping := (&SplunkEnterpriseLogSource{}).GetLabelMapping()
	for _, canonical := range []string{"namespace", "pod", "container", "node", "cluster", "message", "severity"} {
		_, ok := mapping[canonical]
		assert.True(t, ok, "label mapping must cover %q", canonical)
	}
}

// Splunk returns a field only if the search references it. A generated search that
// filters on nothing therefore comes back with just Splunk's default fields, and every
// mapped label (severity_text, k8s.*) is silently absent — severity renders blank and
// QueryLabels, which derives the label set from returned rows, can only offer Splunk
// internals such as _bkt. Verified against Splunk 10.4.2: without the projection the
// response carried only _time/host/index/linecount/source/sourcetype/splunk_server.
func TestSplunkEnterpriseBuildSPLAlwaysProjectsFields(t *testing.T) {
	s := &SplunkEnterpriseLogSource{}

	cases := []struct {
		name string
		req  FetchLogRequest
	}{
		{name: "no filters", req: FetchLogRequest{Limit: 100}},
		{
			name: "with a where clause",
			req: FetchLogRequest{
				Limit: 50,
				QueryRequest: LogsQueryBuilderRequest{
					Where: query.QueryWhereClause{
						Binary: query.BinaryWhereClause{
							"namespace": {query.Eq: "prod"},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spl, err := s.buildSPL(tc.req, "otel_logs")
			assert.NoError(t, err)
			assert.True(t, strings.HasSuffix(spl, "| fields *"),
				"generated SPL must end with a field projection, got: %s", spl)
			// The projection must not trip the SPL command guard.
			assert.NoError(t, validateSplunkEnterpriseQuery(spl))
		})
	}
}
