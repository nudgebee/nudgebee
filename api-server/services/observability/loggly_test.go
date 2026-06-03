package observability

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testLogglyHost        = "example.loggly.com"
	testLogglyAuthzHeader = "Bearer fake-token"
)

// --- Helpers ---
func mockHTTPResponseNew(body any, statusCode int) *http.Response {
	b, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewReader(b)),
		Header:     make(http.Header),
	}
}

func mockRequestContext() *security.RequestContext {
	// var securityContext *security.SecurityContext
	return security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)
}

// mockRoundTripper lets tests intercept HTTP calls made through common.HttpClient.
// It avoids gomonkey-patching common.HttpGet, which is silently dropped on darwin/arm64
// due to Go compiler inlining of the one-line wrapper.
type mockRoundTripper struct {
	handler func(*http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.handler(req)
}

// installMockTransport swaps common.HttpClient().Transport with one that delegates
// to handler, and restores the original on test cleanup.
func installMockTransport(t *testing.T, handler func(*http.Request) (*http.Response, error)) {
	client := common.HttpClient()
	orig := client.Transport
	client.Transport = &mockRoundTripper{handler: handler}
	t.Cleanup(func() { client.Transport = orig })
}

func jsonResponse(body any, statusCode int) *http.Response {
	resp := mockHTTPResponseNew(body, statusCode)
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

// --- Tests ---

func TestConvertLogglyToOutputLogs(t *testing.T) {
	source := &LogglySource{}
	logs := LogglyLog{
		Events: []LogglyLogEvent{
			{
				Id:        "123",
				Timestamp: time.Now().UnixNano(),
				LogMsg:    "test log message",
				Raw:       "raw-log",
				Event:     map[string]any{"field1": "value1"},
				LogTypes:  []string{"error"},
			},
		},
	}

	output, err := source.convertLogglyToOutputLogs(logs)
	assert.NoError(t, err)
	assert.Len(t, output, 1)
	assert.Equal(t, "test log message", output[0].Message)
	assert.Equal(t, "value1", output[0].Labels["field1"])
}

func TestConvertLogglyToOutputLabels(t *testing.T) {
	source := &LogglySource{}
	resp := LogglyGetLabelResponse{
		Fields: []LogglyGetLabelFields{
			{Name: "hostname"},
			{Name: "service"},
		},
	}

	output, err := source.convertLogglyToOutputLabels(resp)
	assert.NoError(t, err)
	assert.Len(t, output, 2)
	assert.Equal(t, "hostname", output[0].Label)
}

func TestConvertLogglyToOutputLabelsValues(t *testing.T) {
	source := &LogglySource{}
	resp := LogglyGetLabelValueResponse{
		Values: []LogglyGetLabelValueObj{
			{Term: "valueA", Count: 5},
			{Term: "valueB", Count: 10},
		},
	}

	output, err := source.convertLogglyToOutputLabelsValues(resp)
	assert.NoError(t, err)
	assert.Len(t, output, 2)
	assert.Equal(t, "valueA", output[0].Value)
	assert.Equal(t, 5.0, output[0].Attributes["count"])
}

func TestLogglySource_QueryLogs(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetLogglyConfigs, func(ctx *security.RequestContext, accountId string) (integrations.LogglyConfig, error) {
		return integrations.LogglyConfig{ApiToken: "fake-token", Subdomain: "example"}, nil
	})

	logs := LogglyLog{
		Events: []LogglyLogEvent{
			{
				Id:        "log-1",
				Timestamp: time.Now().UnixNano(),
				LogMsg:    "hello world",
				Raw:       "raw-log",
				Event:     map[string]any{"foo": "bar"},
				LogTypes:  []string{"info"},
			},
		},
	}
	installMockTransport(t, func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "/apiv2/events/iterate", req.URL.Path)
		assert.Equal(t, testLogglyHost, req.URL.Host)
		assert.Equal(t, testLogglyAuthzHeader, req.Header.Get("Authorization"))
		return jsonResponse(logs, 200), nil
	})

	ctx := mockRequestContext()
	src := &LogglySource{}
	req := FetchLogRequest{LogProvider: "loggly", AccountId: "acc-1", Query: "*", StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Unix(), Limit: 100, LogProviderSource: "agent"}

	out, err := src.QueryLogs(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "hello world", out[0].Message)
	assert.Equal(t, "bar", out[0].Labels["foo"])
}

func TestLogglySource_QueryLabels(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetLogglyConfigs, func(ctx *security.RequestContext, accountId string) (integrations.LogglyConfig, error) {
		return integrations.LogglyConfig{ApiToken: "fake-token", Subdomain: "example"}, nil
	})

	fields := LogglyGetLabelResponse{
		Fields: []LogglyGetLabelFields{
			{Name: "hostname"},
			{Name: "service"},
		},
	}
	installMockTransport(t, func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "/apiv2/fields", req.URL.Path)
		assert.Equal(t, testLogglyHost, req.URL.Host)
		assert.Equal(t, testLogglyAuthzHeader, req.Header.Get("Authorization"))
		return jsonResponse(fields, 200), nil
	})

	ctx := mockRequestContext()
	src := &LogglySource{}
	req := FetchLogLabelRequest{AccountId: "acc-1"}

	out, err := src.QueryLabels(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, out, 2)
	assert.Equal(t, "hostname", out[0].Label)
}

func TestBuildLogglyWhereClause(t *testing.T) {
	cases := []struct {
		name    string
		where   query.QueryWhereClause
		want    string
		wantErr bool
	}{
		{
			name:  "empty where returns empty string",
			where: query.QueryWhereClause{},
			want:  "",
		},
		{
			name: "single eq bare value",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"level": {query.Eq: "ERROR"},
				},
			},
			want: "level:ERROR",
		},
		{
			name: "eq with whitespace gets quoted",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {query.Eq: "connection timeout"},
				},
			},
			want: `message:"connection timeout"`,
		},
		{
			name: "neq emits NOT prefix",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"level": {query.Nq: "INFO"},
				},
			},
			want: "NOT level:INFO",
		},
		{
			name: "contains wraps in wildcards",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {query.Contains: "oom"},
				},
			},
			want: "message:*oom*",
		},
		{
			name: "like translates SQL wildcards to Lucene",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service": {query.Like: "api-%"},
				},
			},
			// '-' is a Lucene special char and is escaped to '\-' (literal hyphen).
			want: `service:api\-*`,
		},
		{
			name: "like translates underscore to ?",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"code": {query.Like: "40_"},
				},
			},
			want: "code:40?",
		},
		{
			name: "unsupported operator returns error",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"count": {query.Between: 5},
				},
			},
			wantErr: true,
		},
		// --- New operators (validated against live Loggly /apiv2/events/iterate) ---
		{
			name: "ilike collapses to like emission",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.ILike: "NUDGE%"},
				},
			},
			want: "namespace:NUDGE*",
		},
		{
			name: "icontains collapses to contains emission",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.IContains: "NUDGE"},
				},
			},
			want: "namespace:*NUDGE*",
		},
		{
			name: "nicontains negates contains",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {query.NIContains: "debug"},
				},
			},
			want: "NOT message:*debug*",
		},
		{
			name: "nlike negates like with SQL wildcards",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service": {query.NLike: "test%"},
				},
			},
			want: "NOT service:test*",
		},
		{
			name: "regex emits Lucene /pat/",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.Regex: "nudge.*"},
				},
			},
			want: "namespace:/nudge.*/",
		},
		{
			name: "nregex emits NOT Lucene /pat/",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.NRegex: "nudge.*"},
				},
			},
			want: "NOT namespace:/nudge.*/",
		},
		{
			name: "regex empty value rejected",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.Regex: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "in joins values with OR in parens",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.In: []interface{}{"loki", "opensearch"}},
				},
			},
			want: "namespace:(loki OR opensearch)",
		},
		{
			name: "in supports []string slice",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.In: []string{"a", "b"}},
				},
			},
			want: "namespace:(a OR b)",
		},
		{
			name: "not_in negates the OR group",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.NotIn: []interface{}{"loki", "opensearch"}},
				},
			},
			want: "NOT namespace:(loki OR opensearch)",
		},
		{
			name: "in non-array value rejected",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.In: "not-an-array"},
				},
			},
			wantErr: true,
		},
		{
			name: "in empty array rejected to avoid silent drop",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.In: []interface{}{}},
				},
			},
			wantErr: true,
		},
		{
			name: "regex escapes literal forward slash",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"path": {query.Regex: "/var/log/.*"},
				},
			},
			want: `path:/\/var\/log\/.*/`,
		},
		{
			name: "gt numeric uses Lucene shorthand",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"app_time": {query.Gt: 100},
				},
			},
			want: "app_time:>100",
		},
		{
			name: "gte numeric",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"app_time": {query.Gte: 100},
				},
			},
			want: "app_time:>=100",
		},
		{
			name: "lt numeric",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"app_time": {query.Lt: "999999"},
				},
			},
			want: "app_time:<999999",
		},
		{
			name: "lte float",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"latency": {query.Lte: 12.5},
				},
			},
			want: "latency:<=12.5",
		},
		{
			name: "gt non-numeric value rejected",
			where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {query.Gt: "n"},
				},
			},
			wantErr: true,
		},
		{
			name: "and of two eqs joins with AND in parens",
			where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{"level": {query.Eq: "ERROR"}}},
					{Binary: query.BinaryWhereClause{"service": {query.Eq: "api"}}},
				},
			},
			want: "(level:ERROR AND service:api)",
		},
		{
			name: "or of two eqs joins with OR in parens",
			where: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{"level": {query.Eq: "ERROR"}}},
					{Binary: query.BinaryWhereClause{"level": {query.Eq: "WARN"}}},
				},
			},
			want: "(level:ERROR OR level:WARN)",
		},
		{
			name: "not wraps with NOT (...)",
			where: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					Binary: query.BinaryWhereClause{"level": {query.Eq: "DEBUG"}},
				},
			},
			want: "NOT (level:DEBUG)",
		},
		{
			name: "and of one eq collapses to single clause without parens",
			where: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{"level": {query.Eq: "ERROR"}}},
				},
			},
			want: "level:ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildLogglyWhereClause(tc.where)
			if tc.wantErr {
				require.Error(t, err)
				assert.True(t, strings.HasPrefix(err.Error(), "loggly:"),
					"error should be prefixed with 'loggly:' got: %v", err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLogglySource_GetQuery_RawQueryTakesPrecedence(t *testing.T) {
	src := &LogglySource{}
	got, err := src.GetQuery(mockRequestContext(), FetchLogRequest{
		Query: "foo AND bar",
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"level": {query.Eq: "ERROR"}},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "foo AND bar", got)
}

func TestLogglySource_GetQuery_WhereOnly(t *testing.T) {
	src := &LogglySource{}
	got, err := src.GetQuery(mockRequestContext(), FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"level": {query.Eq: "ERROR"}},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "level:ERROR", got)
}

func TestLogglySource_GetQuery_EmptyReturnsEmpty(t *testing.T) {
	src := &LogglySource{}
	got, err := src.GetQuery(mockRequestContext(), FetchLogRequest{})
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

// --- LogGroupSource Tests ---

func TestLogglySource_BuildLogGroupLuceneQuery(t *testing.T) {
	src := &LogglySource{}
	errorFilter := `(json.log:*error* OR json.log:*ERROR* OR json.log:*critical* OR json.log:*CRITICAL* OR json.log:*fatal* OR json.log:*FATAL*)`

	cases := []struct {
		name string
		req  FetchLogGroupRequest
		want string
	}{
		{
			name: "no scope — error filter only",
			req:  FetchLogGroupRequest{},
			want: errorFilter,
		},
		{
			name: "namespace scope",
			req: FetchLogGroupRequest{
				Request: map[string]any{"selectedNamespace": "opensearch"},
			},
			want: errorFilter + ` AND json.kubernetes.namespace_name:opensearch`,
		},
		{
			name: "workload scope",
			req: FetchLogGroupRequest{
				Request: map[string]any{"selectedWorkload": "fluent-bit"},
			},
			// '-' is a Lucene special char and gets escaped by EscapeO11yQueryString.
			want: errorFilter + ` AND json.kubernetes.pod_name:fluent\-bit-*`,
		},
		{
			name: "namespace and workload scope",
			req: FetchLogGroupRequest{
				Request: map[string]any{
					"selectedNamespace": "opensearch",
					"selectedWorkload":  "fluent-bit",
				},
			},
			want: errorFilter + ` AND json.kubernetes.namespace_name:opensearch AND json.kubernetes.pod_name:fluent\-bit-*`,
		},
		{
			name: "namespace with whitespace gets quoted",
			req: FetchLogGroupRequest{
				Request: map[string]any{"selectedNamespace": "my namespace"},
			},
			want: errorFilter + ` AND json.kubernetes.namespace_name:"my namespace"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := src.buildLogGroupLuceneQuery(tc.req)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExtractLogglyK8sMeta(t *testing.T) {
	t.Run("flattened labels populated", func(t *testing.T) {
		log := OutputLog{
			Message: "outer-blob",
			Labels: map[string]any{
				"json.kubernetes.namespace_name": "opensearch",
				"json.kubernetes.pod_name":       "fluent-bit-5f9c8d67b-q6b8l",
				"json.kubernetes.container_name": "fluent-bit",
				"json.log":                       "failed to flush chunk",
			},
		}
		ns, pod, container, logLine := extractLogglyK8sMeta(log)
		assert.Equal(t, "opensearch", ns)
		assert.Equal(t, "fluent-bit-5f9c8d67b-q6b8l", pod)
		assert.Equal(t, "fluent-bit", container)
		assert.Equal(t, "failed to flush chunk", logLine)
	})

	t.Run("falls back to raw JSON", func(t *testing.T) {
		raw := `{"log":"inner line","kubernetes":{"namespace_name":"ns1","pod_name":"pod-abc123-xyz","container_name":"c1"}}`
		log := OutputLog{
			Message: "outer-blob",
			Labels:  map[string]any{"raw": raw},
		}
		ns, pod, container, logLine := extractLogglyK8sMeta(log)
		assert.Equal(t, "ns1", ns)
		assert.Equal(t, "pod-abc123-xyz", pod)
		assert.Equal(t, "c1", container)
		assert.Equal(t, "inner line", logLine)
	})

	t.Run("no k8s data falls through to message", func(t *testing.T) {
		log := OutputLog{Message: "just-a-message", Labels: map[string]any{}}
		ns, pod, container, logLine := extractLogglyK8sMeta(log)
		assert.Empty(t, ns)
		assert.Empty(t, pod)
		assert.Empty(t, container)
		assert.Equal(t, "just-a-message", logLine)
	})

	t.Run("malformed raw falls back to message", func(t *testing.T) {
		log := OutputLog{
			Message: "fallback",
			Labels:  map[string]any{"raw": "{not json"},
		}
		_, _, _, logLine := extractLogglyK8sMeta(log)
		assert.Equal(t, "fallback", logLine)
	})
}

func TestGroupLogglyLogsByPattern(t *testing.T) {
	src := &LogglySource{}
	baseLabels := func(logLine string) map[string]any {
		return map[string]any{
			"json.kubernetes.namespace_name": "opensearch",
			"json.kubernetes.pod_name":       "fluent-bit-5f9c8d67b-q6b8l",
			"json.kubernetes.container_name": "fluent-bit",
			"json.log":                       logLine,
		}
	}

	t.Run("identical lines cluster into one group", func(t *testing.T) {
		logs := []OutputLog{
			{Labels: baseLabels("error: connection refused")},
			{Labels: baseLabels("error: connection refused")},
			{Labels: baseLabels("error: connection refused")},
		}
		out := src.groupLogglyLogsByPattern(logs, 1776880000000)
		require.Len(t, out.Groups, 1)
		g := out.Groups[0]
		assert.Equal(t, int64(3), g.Count)
		assert.Equal(t, "opensearch", g.Namespace)
		assert.Equal(t, "fluent-bit", g.Workload)
		assert.Equal(t, "fluent-bit", g.Container)
		assert.Equal(t, "/k8s/opensearch/fluent-bit", g.ContainerID)
		assert.Equal(t, "error", g.Level)
		assert.Equal(t, "error: connection refused", g.Sample)
		assert.NotEmpty(t, g.PatternHash)
		assert.Equal(t, []int64{1776880000}, g.Timestamps)
		assert.Equal(t, []float64{3}, g.Values)
	})

	t.Run("different namespaces yield separate groups", func(t *testing.T) {
		logs := []OutputLog{
			{Labels: baseLabels("error: timeout")},
			{Labels: map[string]any{
				"json.kubernetes.namespace_name": "other-ns",
				"json.kubernetes.pod_name":       "fluent-bit-5f9c8d67b-q6b8l",
				"json.kubernetes.container_name": "fluent-bit",
				"json.log":                       "error: timeout",
			}},
		}
		out := src.groupLogglyLogsByPattern(logs, 0)
		assert.Len(t, out.Groups, 2)
	})

	t.Run("empty log lines are skipped", func(t *testing.T) {
		logs := []OutputLog{
			{Labels: map[string]any{}},
			{Labels: baseLabels("error: real")},
		}
		out := src.groupLogglyLogsByPattern(logs, 0)
		require.Len(t, out.Groups, 1)
		assert.Equal(t, "error: real", out.Groups[0].Sample)
	})

	t.Run("sorted by count desc", func(t *testing.T) {
		logs := []OutputLog{
			{Labels: baseLabels("rare error")},
			{Labels: baseLabels("frequent error")},
			{Labels: baseLabels("frequent error")},
			{Labels: baseLabels("frequent error")},
			{Labels: baseLabels("medium error")},
			{Labels: baseLabels("medium error")},
		}
		out := src.groupLogglyLogsByPattern(logs, 0)
		require.Len(t, out.Groups, 3)
		assert.Equal(t, int64(3), out.Groups[0].Count)
		assert.Equal(t, int64(2), out.Groups[1].Count)
		assert.Equal(t, int64(1), out.Groups[2].Count)
	})

	t.Run("sample truncated to 500 runes", func(t *testing.T) {
		longLine := ""
		for i := 0; i < 600; i++ {
			longLine += "x"
		}
		logs := []OutputLog{{Labels: baseLabels(longLine)}}
		out := src.groupLogglyLogsByPattern(logs, 0)
		require.Len(t, out.Groups, 1)
		assert.Len(t, out.Groups[0].Sample, 500)
	})
}

func TestLogglySource_QueryLogGroup(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetLogglyConfigs, func(ctx *security.RequestContext, accountId string) (integrations.LogglyConfig, error) {
		return integrations.LogglyConfig{ApiToken: "fake-token", Subdomain: "example"}, nil
	})

	logs := LogglyLog{
		Events: []LogglyLogEvent{
			{
				Id:        "1",
				Timestamp: 1776843010079,
				LogMsg:    `{"log":"error: boom","kubernetes":{"namespace_name":"opensearch","pod_name":"fluent-bit-q6b8l","container_name":"fluent-bit"}}`,
				Raw:       `{"log":"error: boom","kubernetes":{"namespace_name":"opensearch","pod_name":"fluent-bit-q6b8l","container_name":"fluent-bit"}}`,
				Event: map[string]any{
					"log": "error: boom",
					"kubernetes": map[string]any{
						"namespace_name": "opensearch",
						"pod_name":       "fluent-bit-q6b8l",
						"container_name": "fluent-bit",
					},
				},
			},
			{
				Id:        "2",
				Timestamp: 1776843010080,
				LogMsg:    `{"log":"error: boom","kubernetes":{"namespace_name":"opensearch","pod_name":"fluent-bit-q6b8l","container_name":"fluent-bit"}}`,
				Raw:       `{"log":"error: boom","kubernetes":{"namespace_name":"opensearch","pod_name":"fluent-bit-q6b8l","container_name":"fluent-bit"}}`,
				Event: map[string]any{
					"log": "error: boom",
					"kubernetes": map[string]any{
						"namespace_name": "opensearch",
						"pod_name":       "fluent-bit-q6b8l",
						"container_name": "fluent-bit",
					},
				},
			},
		},
	}

	var capturedQuery string
	installMockTransport(t, func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "/apiv2/events/iterate", req.URL.Path)
		capturedQuery = req.URL.Query().Get("q")
		return jsonResponse(logs, 200), nil
	})

	ctx := mockRequestContext()
	src := &LogglySource{}
	out, err := src.QueryLogGroup(ctx, FetchLogGroupRequest{
		AccountId: "acc-1",
		StartTime: time.Now().Add(-time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
		Request:   map[string]any{"selectedNamespace": "opensearch"},
	})
	require.NoError(t, err)
	require.Len(t, out.Groups, 1)
	assert.Equal(t, int64(2), out.Groups[0].Count)
	assert.Equal(t, "opensearch", out.Groups[0].Namespace)
	assert.Contains(t, capturedQuery, "json.kubernetes.namespace_name:opensearch")
}

func TestNormalizeLogglyEndTimeSec(t *testing.T) {
	assert.Equal(t, int64(1776843), normalizeLogglyEndTimeSec(1776843))
	assert.Equal(t, int64(1776843010), normalizeLogglyEndTimeSec(1776843010079))
	now := normalizeLogglyEndTimeSec(0)
	assert.InDelta(t, time.Now().Unix(), now, 2)
}

func TestLogglySource_QueryLabelValues(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetLogglyConfigs, func(ctx *security.RequestContext, accountId string) (integrations.LogglyConfig, error) {
		return integrations.LogglyConfig{ApiToken: "fake-token", Subdomain: "example"}, nil
	})

	// "hostname" is a canonical key — the source must translate it to the
	// provider-specific field "host" before hitting /apiv2/fields/{name}.
	const requested = "hostname"
	const mapped = "host"
	resp := map[string]any{
		mapped: []any{
			map[string]any{"term": "host1", "count": 10},
			map[string]any{"term": "host2", "count": 20},
		},
		"total_events":       float64(30),
		"unique_field_count": float64(2),
	}
	installMockTransport(t, func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "/apiv2/fields/"+mapped, req.URL.Path)
		assert.Equal(t, testLogglyHost, req.URL.Host)
		assert.Equal(t, testLogglyAuthzHeader, req.Header.Get("Authorization"))
		// Default 7-day window so sparse tenants still surface values.
		assert.Equal(t, "-7d", req.URL.Query().Get("from"))
		assert.Equal(t, "now", req.URL.Query().Get("until"))
		return jsonResponse(resp, 200), nil
	})

	ctx := mockRequestContext()
	src := &LogglySource{}
	req := FetchLogLabelValuesRequest{AccountId: "acc-1", LabelName: requested}

	out, err := src.QueryLabelValues(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, out, 2)
	assert.Equal(t, "host1", out[0].Value)
	assert.Equal(t, 10.0, out[0].Attributes["count"])
}

// TestLogglySource_QueryLabelValues_UnmappedNamePassesThrough guards the
// fallback path: a label that is not in logglyLogLabelMapping must be sent to
// Loggly as-is (e.g. a raw provider-specific field a user pasted in).
func TestLogglySource_QueryLabelValues_UnmappedNamePassesThrough(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(integrations.GetLogglyConfigs, func(ctx *security.RequestContext, accountId string) (integrations.LogglyConfig, error) {
		return integrations.LogglyConfig{ApiToken: "fake-token", Subdomain: "example"}, nil
	})

	const raw = "json.kubernetes.namespace_name"
	resp := map[string]any{
		raw:                  []any{map[string]any{"term": "default", "count": 1}},
		"total_events":       float64(1),
		"unique_field_count": float64(1),
	}
	installMockTransport(t, func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "/apiv2/fields/"+raw, req.URL.Path)
		return jsonResponse(resp, 200), nil
	})

	out, err := (&LogglySource{}).QueryLabelValues(mockRequestContext(), FetchLogLabelValuesRequest{
		AccountId: "acc-1",
		LabelName: raw,
	})
	require.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "default", out[0].Value)
}

// TestLogglySource_QueryLabelValues_EscapesLabelInURL guards against URL
// injection through the user-supplied label name. The label is interpolated
// into the path of /apiv2/fields/{name}, so untrusted characters (spaces,
// slashes, query chars) must be percent-encoded — but dots have to survive
// since real Loggly fields are dotted (json.kubernetes.namespace_name).
func TestLogglySource_QueryLabelValues_EscapesLabelInURL(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetLogglyConfigs, func(ctx *security.RequestContext, accountId string) (integrations.LogglyConfig, error) {
		return integrations.LogglyConfig{ApiToken: "fake-token", Subdomain: "example"}, nil
	})

	const dirty = "weird name/with?stuff"
	installMockTransport(t, func(req *http.Request) (*http.Response, error) {
		// req.URL.Path is the *decoded* path; req.URL.EscapedPath() is the wire form.
		assert.Equal(t, "/apiv2/fields/weird%20name%2Fwith%3Fstuff", req.URL.EscapedPath())
		return jsonResponse(map[string]any{
			dirty:                []any{},
			"total_events":       float64(0),
			"unique_field_count": float64(0),
		}, 200), nil
	})

	_, err := (&LogglySource{}).QueryLabelValues(mockRequestContext(), FetchLogLabelValuesRequest{
		AccountId: "acc-1",
		LabelName: dirty,
	})
	require.NoError(t, err)
}

// TestLogglySource_QueryLabelValues_MissingFieldDoesNotPanic ensures the
// service does not crash when Loggly returns a response with the requested
// label key absent and total_events / unique_field_count missing — a real
// failure mode for stale or unknown fields.
func TestLogglySource_QueryLabelValues_MissingFieldDoesNotPanic(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(integrations.GetLogglyConfigs, func(ctx *security.RequestContext, accountId string) (integrations.LogglyConfig, error) {
		return integrations.LogglyConfig{ApiToken: "fake-token", Subdomain: "example"}, nil
	})

	installMockTransport(t, func(req *http.Request) (*http.Response, error) {
		// Empty body — none of the expected keys present.
		return jsonResponse(map[string]any{}, 200), nil
	})

	out, err := (&LogglySource{}).QueryLabelValues(mockRequestContext(), FetchLogLabelValuesRequest{
		AccountId: "acc-1",
		LabelName: "namespace",
	})
	require.NoError(t, err)
	assert.Empty(t, out)
}

// TestLogglySource_GetLabelMapping pins the canonical → Loggly field mapping
// so the LogSource framework's WHERE-clause translation
// (convertWhereClauseWithMApping) and output-label aliasing
// (normalizeOutputLogLabels) keep producing the field names Loggly actually
// indexes for K8s log events.
func TestLogglySource_GetLabelMapping(t *testing.T) {
	mapping := (&LogglySource{}).GetLabelMapping()

	want := map[string]string{
		"timestamp": "timestamp",
		"body":      "json.log",
		"message":   "json.log",
		"namespace": "json.kubernetes.namespace_name",
		"container": "json.kubernetes.container_name",
		"pod":       "json.kubernetes.pod_name",
		"node":      "json.kubernetes.host",
		"host":      "host",
		"hostname":  "host",
		"service":   "json.kubernetes.labels.app_kubernetes_io/name",
		"app":       "json.kubernetes.labels.app_kubernetes_io/name",
		"level":     "json.level",
		"severity":  "json.level",
	}
	for k, v := range want {
		assert.Equalf(t, v, mapping[k], "mapping[%q]", k)
	}
}

// TestLogglySource_LabelMappingTranslatesWhereClause exercises the end-to-end
// path: a canonical WHERE clause goes through convertWhereClauseWithMApping
// (using LogglySource.GetLabelMapping) and emerges as a Loggly Lucene query
// targeting json.kubernetes.namespace_name. Without the mapping the clause
// would emit `namespace:foo`, which never matches K8s events.
func TestLogglySource_LabelMappingTranslatesWhereClause(t *testing.T) {
	src := &LogglySource{}
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"namespace": {query.Eq: "nudgebee"},
		},
	}
	translated := convertWhereClauseWithMApping(where, src.GetLabelMapping())

	q, err := buildLogglyWhereClause(translated)
	require.NoError(t, err)
	assert.Equal(t, `json.kubernetes.namespace_name:nudgebee`, q)
}

// TestLogglySource_AppCanonicalMapsToAppKubernetesIoName guards the Helm-aware
// app mapping: canonical `app:X` must emit `labels.app_kubernetes_io\/name:X`,
// with the slash escaped by EscapeO11yQueryString at emit time. Loggly indexes
// app.kubernetes.io/name with the dot collapsed to underscore but the slash
// preserved, and its API rejects the unescaped slash form.
func TestLogglySource_AppCanonicalMapsToAppKubernetesIoName(t *testing.T) {
	src := &LogglySource{}
	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"app": {query.Eq: "rag-server"},
		},
	}
	translated := convertWhereClauseWithMApping(where, src.GetLabelMapping())

	q, err := buildLogglyWhereClause(translated)
	require.NoError(t, err)
	assert.Equal(t, `json.kubernetes.labels.app_kubernetes_io\/name:rag\-server`, q)
}

// TestLogglyGetSupportedOperators guards against drift between the advertised
// capability list and the operators buildLogglyOperatorClause can translate.
// Range operators (_gt/_gte/_lt/_lte) are intentionally omitted: Loggly returns
// HTTP 500 when they target non-numeric fields, and the Query Builder cannot
// distinguish numeric from string fields up front. See issue #29268.
func TestLogglyGetSupportedOperators(t *testing.T) {
	want := []string{
		"_eq", "_neq",
		"_contains", "_icontains", "_nicontains",
		"_like", "_ilike", "_nlike",
		"_regex", "_nregex",
	}
	got := (&LogglySource{}).GetSupportedOperators()
	assert.ElementsMatch(t, want, got)
}

// TestLogglyAdvertisedOperatorsAllTranslate exercises every advertised operator
// through buildLogglyOperatorClause with a value shape it accepts. Drift either
// way (advertised-but-not-translated, or translated-but-emits-(?i)) fails here.
func TestLogglyAdvertisedOperatorsAllTranslate(t *testing.T) {
	for _, op := range (&LogglySource{}).GetSupportedOperators() {
		t.Run(op, func(t *testing.T) {
			var val any = "abc"
			switch op {
			case "_in", "_not_in":
				val = []interface{}{"a", "b"}
			case "_gt", "_gte", "_lt", "_lte":
				val = "1"
			case "_regex", "_nregex":
				val = "abc.*"
			}
			got, err := buildLogglyOperatorClause("f", query.BinaryWhereClauseType(op), val)
			require.NoError(t, err, "op=%s", op)
			assert.NotEmpty(t, got, "op=%s should emit a non-empty clause", op)
			// Loggly silently ignores Lucene inline (?i); we must never emit it.
			assert.NotContains(t, got, "(?i)", "op=%s emitted forbidden inline flag", op)
		})
	}
}

func TestSqlLikeToLuceneWildcard(t *testing.T) {
	cases := map[string]string{
		"nudge%":     "nudge*",
		"nudge_":     "nudge?",
		"%error%":    "*error*",
		"a_b%c":      "a?b*c",
		"plain":      "plain",
		"with space": "with space",
		"colons:bad": `colons\:bad`,
	}
	for in, want := range cases {
		assert.Equal(t, want, sqlLikeToLuceneWildcard(in), "input=%q", in)
	}
}

func TestAssertNumericValue(t *testing.T) {
	for _, ok := range []string{"100", "12.5", "-3", "0"} {
		assert.NoError(t, assertNumericValue(ok), "value=%q", ok)
	}
	for _, bad := range []string{"abc", "", "1.2.3", "n"} {
		assert.Error(t, assertNumericValue(bad), "value=%q", bad)
	}
}
