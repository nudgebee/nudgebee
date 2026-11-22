package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Severity: SolarWinds reports every Kubernetes row as INFO, so the real level
// is the one the line declares about itself.
// ============================================================================

func TestSWLevelToken(t *testing.T) {
	cases := map[string]string{
		`level=error ts=2026-10-07T10:27:46Z msg="push failed"`: "error",
		`level="warn" msg=x`: "warn",
		`{"time":"2026-10-07T10:27:11Z","level":"ERROR","msg":"x"}`: "error",
		`{"level": "warning", "msg": "x"}`:                          "warn",
		`{"level":"debug"}`:                                         "debug",
		`level=info msg="processed with 0 errors"`:                  "info",
		`level=fatal msg=boom`:                                      "error",
		`loglevel=error`:                                            "",
		`connection error occurred`:                                 "",
		`level=`:                                                    "",
	}
	for msg, want := range cases {
		assert.Equal(t, want, swLevelToken(msg), msg)
	}
}

func TestConvertSWLogsToOutputLogs_InfoRowTakesLevelFromLine(t *testing.T) {
	out := convertSWLogsToOutputLogs([]map[string]any{
		{"message": `level=error msg="push failed"`, "severity": "INFO"},
		{"message": `{"level":"WARN","msg":"slow"}`, "severity": "INFO"},
		// No level token: SolarWinds' INFO stands, even though "errors" appears.
		{"message": `processed with 0 errors`, "severity": "INFO"},
		// A real SolarWinds severity is never overridden by the line.
		{"message": `level=info msg=x`, "severity": "ERROR"},
	})
	require.Len(t, out, 4)
	assert.Equal(t, "error", out[0].Severity)
	assert.Equal(t, "warn", out[1].Severity)
	assert.Equal(t, "info", out[2].Severity)
	assert.Equal(t, "error", out[3].Severity)
}

// level=error on the filter side: severity:ERROR matches no Kubernetes row, while the
// line's own token does (live: 0 rows against 1,000+ on the same window).
func TestBuildSWBinaryClause_SeverityMatchesLineLevel(t *testing.T) {
	result, err := buildSWBinaryClause("severity", query.Eq, "error")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(result, `(severity:ERROR OR message:"level=error" OR message:"\"level\":\"error\"" OR `), result)
	assert.Contains(t, result, `message:"level=fatal"`)

	// No severity:DEBUG term: search matches every Kubernetes row as DEBUG, so
	// "debug" would return everything.
	result, err = buildSWBinaryClause("severity", query.Eq, "debug")
	require.NoError(t, err)
	assert.Equal(t, `(message:"level=debug" OR message:"\"level\":\"debug\"" OR `+
		`message:"level=trace" OR message:"\"level\":\"trace\"")`, result)

	// The search cannot tell levels apart exactly (a line can say level=debug in its
	// text), so "not debug" is applied to the rows by the level they are shown with.
	result, err = buildSWBinaryClause("severity", query.Nq, "debug")
	require.NoError(t, err)
	assert.Equal(t, "", result)

	result, err = buildSWBinaryClause("severity", query.In, []any{"warn", "error"})
	require.NoError(t, err)
	assert.Equal(t, "("+swSeverityTerm("WARN")+" OR "+swSeverityTerm("ERROR")+")", result)
}

// The filter must find every line the reader labels with that level: a line saying
// level=fatal or "level":"warning" is shown as error / warn, so filtering on error /
// warn has to return it.
func TestSWSeverityTerm_CoversEverySpellingTheReaderFolds(t *testing.T) {
	for level, spellings := range swLevelSpellings {
		term := swSeverityTerm(level)
		for _, spelling := range spellings {
			require.Equal(t, level, swLevelToken("level="+spelling))
			require.Equal(t, level, swLevelToken(`{"level":"`+spelling+`"}`))
			assert.Contains(t, term, `message:"level=`+spelling+`"`, level)
			assert.Contains(t, term, `message:"\"level\":\"`+spelling+`\""`, level)
		}
	}
}

// ============================================================================
// Ordering: SolarWinds returns the newest page with its rows oldest-first.
// ============================================================================

func TestQueryLogs_NewestFirst(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		return []byte(`{"logs":[
			{"time":"2026-10-07T10:00:01Z","message":"a"},
			{"time":"2026-10-07T10:00:02Z","message":"b"},
			{"time":"2026-10-07T10:00:03Z","message":"c"}]}`), http.StatusOK, nil
	}
	src := &SolarWindsLogSource{}

	out, err := src.QueryLogs(mockRequestContext(), FetchLogRequest{AccountId: "acc-1"})
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, []string{"c", "b", "a"}, []string{out[0].Message, out[1].Message, out[2].Message})

	asc := FetchLogRequest{AccountId: "acc-1", SortFields: []SortField{{ColumnName: "timestamp", Order: "asc"}}}
	out, err = src.QueryLogs(mockRequestContext(), asc)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, []string{out[0].Message, out[1].Message, out[2].Message})
}

// ============================================================================
// Timeouts: a search SolarWinds does not finish must read as an error, never empty.
// ============================================================================

func TestQueryLogs_TimeoutSurfacesAsError(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		return nil, 0, fmt.Errorf("request to SolarWinds API failed: %w", context.DeadlineExceeded)
	}

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{AccountId: "acc-1"})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Contains(t, err.Error(), fmt.Sprintf("SolarWinds did not answer within %d s", int(swRequestTimeout.Seconds())))
}

func TestSWRequestTimeoutBelowLLMRPCTimeout(t *testing.T) {
	// llm-server's default HTTP client gives up on api-server after 30 s; a slower
	// SolarWinds call would surface there as a generic timeout instead of this one.
	assert.Less(t, swRequestTimeout, 30*time.Second)
}

// ============================================================================
// Pickers: /v1/entities pages at 1,000; follow skipToken within a budget.
// ============================================================================

func TestQuerySWEntityNames_FollowsSkipToken(t *testing.T) {
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	var tokens []string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		tokens = append(tokens, params["skipToken"])
		if params["skipToken"] == "" {
			return []byte(`{"entities":[{"name":"pod-a"}],"pageInfo":{"nextPage":"/v1/entities?type=KubernetesPod&pageSize=1000&skipToken=tok2"}}`), http.StatusOK, nil
		}
		return []byte(`{"entities":[{"name":"pod-b"}],"pageInfo":{"nextPage":""}}`), http.StatusOK, nil
	}

	names, err := querySWEntityNames("token", swTestBaseURL, "KubernetesPod")
	require.NoError(t, err)
	assert.Equal(t, []string{"pod-a", "pod-b"}, names)
	assert.Equal(t, []string{"", "tok2"}, tokens)
}

func TestQuerySWEntityNames_StopsAtPageBudget(t *testing.T) {
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	calls := 0
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		calls++
		body := fmt.Sprintf(`{"entities":[{"name":"pod-%d"}],"pageInfo":{"nextPage":"/v1/entities?skipToken=t%d"}}`, calls, calls)
		return []byte(body), http.StatusOK, nil
	}

	names, err := querySWEntityNames("token", swTestBaseURL, "KubernetesPod")
	require.NoError(t, err)
	assert.Equal(t, swEntityPageBudget, calls)
	assert.Len(t, names, swEntityPageBudget)
}

// ============================================================================
// Log groups: page to a budget, say when the result is a sample, and take the
// namespace from the pod entities.
// ============================================================================

// swTestLogPage builds a /v1/logs page of n rows, oldest first, ending at end.
func swTestLogPage(n int, end time.Time, next string) []byte {
	logs := make([]map[string]any, 0, n)
	for i := n - 1; i >= 0; i-- {
		logs = append(logs, map[string]any{
			"time":     end.Add(-time.Duration(i) * time.Second).UTC().Format(time.RFC3339),
			"message":  `level=error msg="upstream failed"`,
			"hostname": "web-7d9f8c6b5-abcde",
			"program":  "web",
			"severity": "INFO",
		})
	}
	body, _ := json.Marshal(map[string]any{"logs": logs, "pageInfo": map[string]any{"nextPage": next}})
	return body
}

func TestQueryLogGroup_PagesToBudgetAndMarksSample(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	end := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	pages := 0
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			return []byte(`{"entities":[{"name":"web-7d9f8c6b5-abcde","attributes":{"namespaceName":"shop"}}]}`), http.StatusOK, nil
		}
		pageEnd := end.Add(-time.Duration(pages) * 1000 * time.Second)
		pages++
		return swTestLogPage(1000, pageEnd, "/v1/logs?skipToken=more"), http.StatusOK, nil
	}

	out, err := (&SolarWindsLogSource{}).QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{
		AccountId: "acc-1",
		StartTime: end.Add(-24 * time.Hour).UnixMilli(),
		EndTime:   end.UnixMilli(),
	})
	require.NoError(t, err)
	assert.Equal(t, swLogGroupRowBudget/1000, pages)
	assert.True(t, out.Sampled)
	oldest := end.Add(-time.Duration(swLogGroupRowBudget-1) * time.Second)
	assert.Equal(t, oldest.UnixMilli(), out.SampleStartTime)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, int64(swLogGroupRowBudget), out.Groups[0].Count)
	assert.Equal(t, "shop", out.Groups[0].Namespace)
	assert.Equal(t, "error", out.Groups[0].Level)
}

func TestQueryLogGroup_WholeWindowIsNotASample(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			return []byte(`{"entities":[]}`), http.StatusOK, nil
		}
		return swTestLogPage(3, time.Now(), ""), http.StatusOK, nil
	}

	out, err := (&SolarWindsLogSource{}).QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{AccountId: "acc-1"})
	require.NoError(t, err)
	assert.False(t, out.Sampled)
	assert.Zero(t, out.SampleStartTime)
}

// The namespace used to come from the first 1,000 of the org's pod entities, which
// missed nearly every pod. It is now looked up for exactly the pods in the sample.
func TestQueryLogGroup_NamespaceLookedUpForSampledPods(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	resetSWPodNamespaceCache()
	var entitySearch string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			entitySearch = params["search"]
			return []byte(`{"entities":[{"name":"web-7d9f8c6b5-abcde","attributes":{"namespaceName":"shop"}}]}`), http.StatusOK, nil
		}
		return swTestLogPage(2, time.Now(), ""), http.StatusOK, nil
	}

	out, err := (&SolarWindsLogSource{}).QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{AccountId: "acc-1"})
	require.NoError(t, err)
	assert.Equal(t, `name:"web-7d9f8c6b5-abcde"`, entitySearch)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, "shop", out.Groups[0].Namespace)
	assert.Equal(t, "/k8s/shop/web", out.Groups[0].ContainerID)
}

func TestQueryLogGroup_SelectedNamespaceNarrowsSearch(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	var filter string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			return []byte(`{"entities":[{"name":"web-7d9f8c6b5-abcde","attributes":{"namespaceName":"shop"}}]}`), http.StatusOK, nil
		}
		filter = params["filter"]
		return swTestLogPage(2, time.Now(), ""), http.StatusOK, nil
	}

	out, err := (&SolarWindsLogSource{}).QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{
		AccountId: "acc-1",
		Request:   map[string]any{"selectedNamespace": "shop"},
	})
	require.NoError(t, err)
	assert.Contains(t, filter, `k8s.namespace.name:"shop"`)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, "shop", out.Groups[0].Namespace)
}

// "error" anywhere in the line used to make a group error-level; the line's own
// level token wins now.
func TestGroupSWLogsByPattern_LevelFromLineToken(t *testing.T) {
	logs := convertSWLogsToOutputLogs([]map[string]any{
		{"message": `level=info msg="retried, 0 errors"`, "severity": "INFO", "hostname": "web-7d9f8c6b5-abcde"},
		{"message": `ERROR upstream refused`, "severity": "INFO", "hostname": "web-7d9f8c6b5-abcde"},
	})
	out := groupSWLogsByPattern(logs, nil, "", "", 0)
	levels := map[string]string{}
	for _, g := range out.Groups {
		levels[strings.Fields(g.Sample)[0]] = g.Level
	}
	assert.Equal(t, "info", levels["level=info"])
	assert.Equal(t, "error", levels["ERROR"])
}

// ============================================================================
// Paging: SolarWinds searches the window newest-first in slices, so a window whose
// newest part is quiet starts with an EMPTY page that still links to the next one.
// QueryLogs read only that first page, so "services-server logs, last 3h" answered 0.
// ============================================================================

// swPagedLogs serves /v1/logs from a fixed list of pages, each linking to the next.
func swPagedLogs(t *testing.T, pages ...[]map[string]any) (*int, func()) {
	t.Helper()
	restoreConfigs := stubSWConfigs()
	resetSWPodNamespaceCache()
	orig := solarWindsDoGET
	calls := 0
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			return []byte(`{"entities":[]}`), http.StatusOK, nil
		}
		i := 0
		if tok := params["skipToken"]; tok != "" {
			if _, err := fmt.Sscanf(tok, "p%d", &i); err != nil {
				return nil, http.StatusBadRequest, err
			}
		}
		calls++
		next := ""
		if i+1 < len(pages) {
			next = fmt.Sprintf("/v1/logs?skipToken=p%d", i+1)
		}
		body, _ := json.Marshal(map[string]any{"logs": pages[i], "pageInfo": map[string]any{"nextPage": next}})
		return body, http.StatusOK, nil
	}
	return &calls, func() { solarWindsDoGET = orig; restoreConfigs() }
}

func swRows(n int, minute int, host, msg string) []map[string]any {
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{
			"time": fmt.Sprintf("2026-10-07T15:%02d:%02dZ", minute, i%60), "hostname": host,
			"program": "app", "message": msg, "severity": "INFO",
		})
	}
	return rows
}

func TestQueryLogs_FollowsCursorPastEmptyPages(t *testing.T) {
	calls, restore := swPagedLogs(t, nil, nil, swRows(3, 10, "web-0", "hello"))
	defer restore()

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{AccountId: "acc-1", Limit: 100})
	require.NoError(t, err)
	assert.Len(t, out, 3)
	assert.Equal(t, 3, *calls)
}

func TestQueryLogs_KeepsPagingUntilTheRowCheckFillsTheLimit(t *testing.T) {
	// The search for "canary%" also returns loki-canary pods; the row check drops them.
	calls, restore := swPagedLogs(t,
		swRows(5, 20, "loki-canary-4xwzv", "x"),
		append(swRows(2, 19, "canary-web-0", "x"), swRows(3, 19, "loki-canary-4xwzv", "x")...),
		swRows(4, 18, "canary-web-0", "x"),
		swRows(4, 17, "canary-web-0", "x"),
	)
	defer restore()

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{
		AccountId: "acc-1", Limit: 5,
		QueryRequest: LogsQueryBuilderRequest{Where: swBin("hostname", query.Like, "canary%")},
	})
	require.NoError(t, err)
	require.Len(t, out, 5)
	for _, l := range out {
		assert.Equal(t, "canary-web-0", l.Labels["hostname"])
	}
	assert.Equal(t, 3, *calls, "stops once the limit is filled")
	assert.Equal(t, "2026-10-07T15:19:01Z", out[0].Timestamp, "newest first across pages")
}

func TestQueryLogs_OutOfTimeWithNothingFoundIsAnError(t *testing.T) {
	defer stubSWConfigs()()
	origGET, origBudget := solarWindsDoGET, swQueryTimeBudget
	defer func() { solarWindsDoGET, swQueryTimeBudget = origGET, origBudget }()
	swQueryTimeBudget = 50 * time.Millisecond
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		time.Sleep(20 * time.Millisecond)
		return []byte(`{"logs":[],"pageInfo":{"nextPage":"/v1/logs?skipToken=more"}}`), http.StatusOK, nil
	}

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{AccountId: "acc-1"})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Contains(t, err.Error(), "SolarWinds did not finish searching the window")
}

func TestQueryLogs_OutOfTimeWithMatchesReturnsThem(t *testing.T) {
	defer stubSWConfigs()()
	origGET, origBudget := solarWindsDoGET, swQueryTimeBudget
	defer func() { solarWindsDoGET, swQueryTimeBudget = origGET, origBudget }()
	swQueryTimeBudget = 50 * time.Millisecond
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		time.Sleep(20 * time.Millisecond)
		body, _ := json.Marshal(map[string]any{"logs": swRows(1, 10, "web-0", "x"), "pageInfo": map[string]any{"nextPage": "/v1/logs?skipToken=more"}})
		return body, http.StatusOK, nil
	}

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{AccountId: "acc-1", Limit: 100})
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

func TestQueryLabelValues_SamplesPastEmptyPages(t *testing.T) {
	_, restore := swPagedLogs(t, nil, []map[string]any{{"custom_field": "v1"}, {"custom_field": "v2"}})
	defer restore()

	out, err := (&SolarWindsLogSource{}).QueryLabelValues(mockRequestContext(), FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "custom_field"})
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

// Log groups stopped on the first short page, so "3h to now" (quiet newest hours) read one
// empty page and returned no groups.
func TestQueryLogGroup_ReadsPastEmptyPages(t *testing.T) {
	_, restore := swPagedLogs(t, nil, swRows(3, 10, "web-7d9f8c6b5-bcdfg", "level=error boom"))
	defer restore()

	out, err := (&SolarWindsLogSource{}).QueryLogGroup(mockRequestContext(), FetchLogGroupRequest{AccountId: "acc-1"})
	require.NoError(t, err)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, int64(3), out.Groups[0].Count)
	assert.False(t, out.Sampled)
}

// level != info returned 824 of 1,000 rows the reader itself shows as info: the search
// cannot see the level the reader infers, so the rows are checked by it.
func TestQueryLogs_LevelFilterMatchesTheShownLevel(t *testing.T) {
	_, restore := swPagedLogs(t, append(swRows(2, 10, "web-0", "level=info msg=ok"), swRows(1, 10, "web-0", "level=error msg=bad")...))
	defer restore()

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{
		AccountId: "acc-1", Limit: 100,
		QueryRequest: LogsQueryBuilderRequest{Where: swBin("severity", query.Nq, "info")},
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "error", out[0].Severity)

	out, err = (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{
		AccountId: "acc-1", Limit: 100,
		QueryRequest: LogsQueryBuilderRequest{Where: swBin("severity", query.Eq, "info")},
	})
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

// service.name values came back null: log lines do not carry it and SolarWinds has no
// service entities. The collector sets it from the app label, i.e. the workload name.
func TestQueryLabelValues_ServiceFromWorkloadEntities(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		require.Equal(t, swTestEntitiesP, path)
		return []byte(`{"entities":[{"name":"` + params["type"] + `-x"}]}`), http.StatusOK, nil
	}

	out, err := (&SolarWindsLogSource{}).QueryLabelValues(mockRequestContext(), FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: "service"})
	require.NoError(t, err)
	assert.Len(t, out, 3)
	for _, label := range []string{"k8s.statefulset.name", "k8s.daemonset.name"} {
		out, err = (&SolarWindsLogSource{}).QueryLabelValues(mockRequestContext(), FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: label})
		require.NoError(t, err, label)
		assert.Len(t, out, 1, label)
	}
}

// The log-group workload filter kept loki-canary groups for workload "loki" and dropped
// loki-0 (extractWorkloadFromPodName reads them as "loki" and "loki-0").
func TestGroupSWLogsByPattern_WorkloadOwnershipFromPodName(t *testing.T) {
	logs := []OutputLog{
		{Message: "error a", Severity: "error", Labels: map[string]any{"hostname": "loki-0"}},
		{Message: "error b", Severity: "error", Labels: map[string]any{"hostname": "loki-canary-4xwzv"}},
	}
	out := groupSWLogsByPattern(logs, nil, "", "loki", 0)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, "error a", out.Groups[0].Sample)
	assert.Equal(t, "loki", out.Groups[0].Workload)
}

// "level ilike error" searched severity:ERROR alone, which matches no Kubernetes line (all
// INFO); a pattern that names a level now gets the same expansion and row check as _eq.
func TestBuildSWBinaryClause_LevelPatternsAreLevelEquality(t *testing.T) {
	want := swSeverityTerm("ERROR")
	for _, tc := range []struct {
		op  query.BinaryWhereClauseType
		val string
	}{
		{query.ILike, "error"}, {query.ILike, "%error%"}, {query.Like, "ERROR"},
		{query.IContains, "error"}, {query.Contains, "err"},
	} {
		got, err := buildSWBinaryClause("severity", tc.op, tc.val)
		require.NoError(t, err, "%s %s", tc.op, tc.val)
		if tc.val == "err" {
			assert.Equal(t, swSeverityTerm("ERR"), got)
			continue
		}
		assert.Equal(t, want, got, "%s %s", tc.op, tc.val)
	}
	got, err := buildSWBinaryClause("severity", query.NIContains, "debug")
	require.NoError(t, err)
	assert.Equal(t, "", got, "not-debug is applied to the rows")

	for _, tc := range []struct {
		op  query.BinaryWhereClauseType
		val any
	}{
		{query.ILike, "%oops%"}, {query.Eq, "custom"}, {query.In, []any{"error", "custom"}},
	} {
		_, err := buildSWBinaryClause("severity", tc.op, tc.val)
		assert.Error(t, err, "%s %v", tc.op, tc.val)
	}
}

func TestQueryLogs_LevelPatternKeepsTheShownLevel(t *testing.T) {
	_, restore := swPagedLogs(t, append(swRows(2, 10, "web-0", "level=info msg=ok"), swRows(1, 10, "web-0", "level=error msg=bad")...))
	defer restore()

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{
		AccountId: "acc-1", Limit: 100,
		QueryRequest: LogsQueryBuilderRequest{Where: swBin("severity", query.ILike, "error")},
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "error", out[0].Severity)
}

// "app != loki" sent no exclusion, so error lines were read 1,000 at a time and the loki
// ones dropped: 85 rows in 20 s against 5,639. The workload's own pods are now excluded by
// SolarWinds when they are few enough to name.
func TestQueryLogs_WorkloadExclusionUsesItsPods(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	swWorkloadPodsCache.Lock()
	swWorkloadPodsCache.entries = map[string]swWorkloadPodsEntry{}
	swWorkloadPodsCache.Unlock()
	var filter string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			require.Equal(t, "loki", params["name"])
			return []byte(`{"entities":[{"name":"loki-0"},{"name":"loki-canary-4xwzv"},{"name":"loki-gateway-75d5f46c-c56xr"}]}`), http.StatusOK, nil
		}
		filter = params["filter"]
		body, _ := json.Marshal(map[string]any{"logs": swRows(1, 10, "web-0", "level=error x")})
		return body, http.StatusOK, nil
	}

	src := &SolarWindsLogSource{}
	req := FetchLogRequest{AccountId: "acc-1", Limit: 100, QueryRequest: LogsQueryBuilderRequest{Where: swBin(swMapField("app"), query.Nq, "loki")}}
	_, err := src.QueryLogs(mockRequestContext(), req)
	require.NoError(t, err)
	assert.Equal(t, `-(host:"loki-0")`, filter)
	got, err := src.GetQuery(mockRequestContext(), req)
	require.NoError(t, err)
	assert.Equal(t, filter, got, "the query reported is the query sent")
}

func TestQueryLogs_WorkloadExclusionFallsBackWhenPodsAreMany(t *testing.T) {
	defer stubSWConfigs()()
	origGET := solarWindsDoGET
	defer func() { solarWindsDoGET = origGET }()
	swWorkloadPodsCache.Lock()
	swWorkloadPodsCache.entries = map[string]swWorkloadPodsEntry{}
	swWorkloadPodsCache.Unlock()
	var filter string
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		if path == swTestEntitiesP {
			var entities []map[string]any
			for i := 0; i <= swWorkloadExclusionMaxPods; i++ {
				const alphabet = "bcdfghjklmnpqrstvwxz2456789"
				suffix := "bcd" + string(alphabet[i/len(alphabet)]) + string(alphabet[i%len(alphabet)])
				entities = append(entities, map[string]any{"name": "api-7d9f8c6b5-" + suffix})
			}
			body, _ := json.Marshal(map[string]any{"entities": entities})
			return body, http.StatusOK, nil
		}
		filter = params["filter"]
		return []byte(`{"logs":[]}`), http.StatusOK, nil
	}

	_, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{AccountId: "acc-1",
		QueryRequest: LogsQueryBuilderRequest{Where: swBin(swMapField("app"), query.Nq, "api")}})
	require.NoError(t, err)
	assert.Equal(t, "", filter, "too many pods to name: the row check excludes them")
}

// limit 2000 used to be cut to one 1,000-line page without saying so.
func TestQueryLogs_LimitAboveOnePageIsFilledByPaging(t *testing.T) {
	_, restore := swPagedLogs(t, swRows(1000, 30, "web-0", "x"), swRows(1000, 29, "web-0", "x"), swRows(1000, 28, "web-0", "x"))
	defer restore()

	out, err := (&SolarWindsLogSource{}).QueryLogs(mockRequestContext(), FetchLogRequest{AccountId: "acc-1", Limit: 2000})
	require.NoError(t, err)
	assert.Len(t, out, 2000)
}

func TestQueryLogsPartial_MarksABudgetStop(t *testing.T) {
	defer stubSWConfigs()()
	origGET, origBudget := solarWindsDoGET, swQueryTimeBudget
	defer func() { solarWindsDoGET, swQueryTimeBudget = origGET, origBudget }()
	swQueryTimeBudget = 50 * time.Millisecond
	solarWindsDoGET = func(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
		time.Sleep(20 * time.Millisecond)
		body, _ := json.Marshal(map[string]any{"logs": swRows(1, 10, "web-0", "x"), "pageInfo": map[string]any{"nextPage": "/v1/logs?skipToken=more"}})
		return body, http.StatusOK, nil
	}

	out, partial, err := (&SolarWindsLogSource{}).QueryLogsPartial(mockRequestContext(), FetchLogRequest{AccountId: "acc-1", Limit: 100})
	require.NoError(t, err)
	assert.NotEmpty(t, out)
	assert.True(t, partial)

	_, restore := swPagedLogs(t, swRows(3, 10, "web-0", "x"))
	defer restore()
	_, partial, err = (&SolarWindsLogSource{}).QueryLogsPartial(mockRequestContext(), FetchLogRequest{AccountId: "acc-1", Limit: 100})
	require.NoError(t, err)
	assert.False(t, partial, "the window was read to its end")
}
