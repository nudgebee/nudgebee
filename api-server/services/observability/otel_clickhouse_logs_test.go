package observability

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/observability/logql2ch"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

// fakeOtelLogsExec stands in for the relay: it answers the schema probe with
// probeCols (or probeErr) and every other statement with rows (or err), and
// records the SQL it was sent.
type fakeOtelLogsExec struct {
	mu        sync.Mutex
	probeCols []string
	probeErr  error
	rows      RawTraceResult
	err       error
	sqls      []string
	probes    int
	// deadlines records whether each statement's context had a deadline,
	// probe first.
	deadlines []bool
}

func (f *fakeOtelLogsExec) exec(ctx context.Context, sql, _ string) (RawTraceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, hasDeadline := ctx.Deadline()
	f.deadlines = append(f.deadlines, hasDeadline)
	if strings.Contains(sql, "system.columns") {
		f.probes++
		if f.probeErr != nil {
			return RawTraceResult{}, f.probeErr
		}
		out := RawTraceResult{Columns: []string{"name"}}
		for _, c := range f.probeCols {
			out.Rows = append(out.Rows, []any{c})
		}
		return out, nil
	}
	f.sqls = append(f.sqls, sql)
	return f.rows, f.err
}

func (f *fakeOtelLogsExec) lastSQL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sqls) == 0 {
		return ""
	}
	return f.sqls[len(f.sqls)-1]
}

var runnerLabelColumns = []string{"container", "level", "namespace", "node", "pod", "stream", "workload"}

func withFakeOtelLogs(t *testing.T, f *fakeOtelLogsExec) {
	t.Helper()
	prev := otelLogsExec
	otelLogsExec = f.exec
	prevTable := otelLogsAgentTable
	otelLogsAgentTable = func(string) string { return "" }
	otelLogsSchemas.Lock()
	otelLogsSchemas.m = map[string]otelLogsSchemaEntry{}
	otelLogsSchemas.Unlock()
	t.Cleanup(func() { otelLogsExec, otelLogsAgentTable = prev, prevTable })
}

// The agent reports the table it checked; queries and the schema probe read that
// table. A value that is not db.table, or none, means the default otel_logs.
func TestOtelClickhouseLogs_TableFromTheAgent(t *testing.T) {
	for _, tc := range []struct {
		reported, from, probe string
	}{
		{"logs.app_logs", "FROM logs.app_logs", "database = 'logs' AND table = 'app_logs'"},
		{"otel_logs", "FROM otel_logs", "database = currentDatabase() AND table = 'otel_logs'"},
		{"", "FROM otel_logs", "table = 'otel_logs'"},
		{"logs.x; DROP TABLE y", "FROM otel_logs", "table = 'otel_logs'"},
		{"a.b.c", "FROM otel_logs", "table = 'otel_logs'"},
	} {
		t.Run(tc.reported, func(t *testing.T) {
			f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
			var probeSQL string
			exec := f.exec
			f2 := func(ctx context.Context, sql, acc string) (RawTraceResult, error) {
				if strings.Contains(sql, "system.columns") {
					probeSQL = sql
				}
				return exec(ctx, sql, acc)
			}
			withFakeOtelLogs(t, f)
			otelLogsExec = f2
			otelLogsAgentTable = func(string) string { return tc.reported }

			_, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
				AccountId: "acc", Query: `{namespace="demo"}`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
			})
			require.NoError(t, err)
			assert.Contains(t, f.lastSQL(), tc.from)
			assert.Contains(t, probeSQL, tc.probe)
		})
	}
}

func otelLogsCtx() *security.RequestContext {
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
}

var (
	otelLogsStartMs = time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC).UnixMilli()
	otelLogsEndMs   = time.Date(2026, 10, 12, 18, 30, 0, 0, time.UTC).UnixMilli()
)

func logRows() RawTraceResult {
	return RawTraceResult{
		Columns: []string{"ts", "Body", "l_namespace", "l_pod", "l_container", "l_app", "l_filename", "l_job", "l_level", "l_node_name", "l_service_name", "l_stream", "trace_id", "span_id"},
		Rows: [][]any{
			{"1791828000123456789", "payment failed", "demo", "payment-7d9f-x", "", "payment", "", "demo/payment", "error", "node-a", "payment", "", "4bf92f3577b34da6", "00f067aa0ba902b7"},
			{"1791827999000000000", "level=info msg=\"GET /healthz 200\"", "demo", "payment-7d9f-x", "", "payment", "", "demo/payment", "unknown", "node-a", "payment", "", "", ""},
		},
	}
}

func TestOtelClickhouseLogs_QueryLogsLogQL(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	s := &OtelClickhouseLogSource{}

	logs, err := s.QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `{namespace="demo"} |= "failed"`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	sql := f.lastSQL()
	assert.Contains(t, sql, "FROM otel_logs")
	assert.Contains(t, sql, "AND (namespace = 'demo' AND position(Body, 'failed') > 0)")
	assert.Contains(t, sql, "Timestamp >= fromUnixTimestamp64Nano(toInt64(1791828000000000000))")
	assert.Contains(t, sql, "LIMIT 5000")

	require.Len(t, logs, 2)
	assert.Equal(t, "2026-10-12T18:00:00.123456789Z", logs[0].Timestamp)
	assert.Equal(t, "payment failed", logs[0].Message)
	assert.Equal(t, "error", logs[0].Severity)
	assert.Equal(t, map[string]any{
		"namespace": "demo", "pod": "payment-7d9f-x", "app": "payment", "job": "demo/payment", "level": "error",
		"node_name": "node-a", "service_name": "payment", "trace_id": "4bf92f3577b34da6", "span_id": "00f067aa0ba902b7",
	}, logs[0].Labels, "empty labels are left out")
	// No severity on the row: classified from the text, as Loki's lines are.
	assert.Equal(t, "info", logs[1].Severity)
}

func TestOtelClickhouseLogs_BuilderWhereRunsDirectly(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	s := &OtelClickhouseLogSource{}
	where := query.QueryWhereClause{Or: []query.QueryWhereClause{
		{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "demo"}}},
		{Binary: query.BinaryWhereClause{"pod": {query.Eq: "p"}}},
	}}
	req := FetchLogRequest{AccountId: "acc", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs, QueryRequest: LogsQueryBuilderRequest{Where: where}}

	// A cross-field _or has no exact LogQL form, so nothing is displayed ...
	display, err := s.GetQuery(otelLogsCtx(), req)
	require.NoError(t, err)
	assert.Empty(t, display)
	// ... and the where-clause runs as SQL, OR intact.
	_, err = s.QueryLogs(otelLogsCtx(), req)
	require.NoError(t, err)
	assert.Contains(t, f.lastSQL(), "AND ((namespace = 'demo' OR pod = 'p'))")

	// When GetQuery's display LogQL comes back as Query (FetchLogs does that), the
	// where-clause still runs alone.
	req.QueryRequest.Where = query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.In: []any{"a", "b"}}}}
	display, err = s.GetQuery(otelLogsCtx(), req)
	require.NoError(t, err)
	require.NotEmpty(t, display)
	req.Query = display
	_, err = s.QueryLogs(otelLogsCtx(), req)
	require.NoError(t, err)
	assert.Contains(t, f.lastSQL(), "AND (1)\n  AND (namespace IN ('a', 'b'))")
}

// Code-mode LogQL and the account's default filters (ANDed into Where by
// FetchLogs) must both apply; neither may silently drop the other.
func TestOtelClickhouseLogs_LogQLAndDefaultFiltersBothApply(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	_, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `{app="payment"} |= "failed"`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
		QueryRequest: LogsQueryBuilderRequest{Where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "demo"}}}},
	})
	require.NoError(t, err)
	assert.Contains(t, f.lastSQL(), "AND (workload = 'payment' AND position(Body, 'failed') > 0)\n  AND (namespace = 'demo')")
}

func TestOtelClickhouseLogs_LokiParams(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	_, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc",
		Query:     "query=%7Bnamespace%3D%22demo%22%7D&start=1791828000000000000&end=1791828060000000000&limit=10&direction=forward",
	})
	require.NoError(t, err)
	sql := f.lastSQL()
	assert.Contains(t, sql, "Timestamp < fromUnixTimestamp64Nano(toInt64(1791828060000000000))")
	assert.Contains(t, sql, "ORDER BY Timestamp ASC\nLIMIT 10")

	// The query parameter is trimmed like raw LogQL, so a display sent back with
	// a trailing space is still recognised.
	q, _ := splitLokiParams("query=%7Bnamespace%3D%22demo%22%7D%20&limit=1")
	assert.Equal(t, `{namespace="demo"}`, q)
}

func TestOtelClickhouseLogs_MetricRows(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: RawTraceResult{
		Columns: []string{"series", "t", "v", "lv"},
		Rows: [][]any{
			{`namespace="demo",pod="p1"`, float64(1791828300), float64(12), []any{"demo", "p1"}},
			{`namespace="",pod="p2"`, "1791828300", "0.5", []any{nil, "p2"}},
		},
	}}
	withFakeOtelLogs(t, f)
	logs, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `sum by (pod, namespace) (count_over_time({namespace=~".+"}[5m]))`,
		StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	assert.Contains(t, f.lastSQL(), "GROUP BY series, t")
	require.Len(t, logs, 2)
	assert.Equal(t, OutputLog{Timestamp: "2026-10-12T18:05:00Z", Message: "12", Labels: map[string]any{"namespace": "demo", "pod": "p1"}, Severity: "unknown"}, logs[0])
	assert.Equal(t, map[string]any{"pod": "p2"}, logs[1].Labels, "Loki leaves an empty label out of the series")
	assert.Equal(t, "0.5", logs[1].Message)
}

// The metric SQL asks for one row past the cap; getting it back means the
// answer was cut, so the query fails instead of returning part of it.
func TestOtelClickhouseLogs_MetricRowCap(t *testing.T) {
	rows := RawTraceResult{Columns: []string{"series", "t", "v"}}
	for range logql2ch.MaxMetricRows + 1 {
		rows.Rows = append(rows.Rows, []any{"", "1791828300", "1"})
	}
	withFakeOtelLogs(t, &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: rows})
	_, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `sum(count_over_time({namespace="demo"}[5m]))`,
		StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than 50000 points")
}

// A query whose caller sets no timeout (label values, log groups) and the
// schema probe still run with a deadline, so a stalled relay cannot hang them.
func TestOtelClickhouseLogs_EveryQueryHasADeadline(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: RawTraceResult{Columns: []string{"v"}}}
	withFakeOtelLogs(t, f)
	_, err := (&OtelClickhouseLogSource{}).QueryLabelValues(otelLogsCtx(), FetchLogLabelValuesRequest{
		AccountId: "acc", LabelName: "pod", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	assert.Equal(t, []bool{true, true}, f.deadlines, "probe, then the label-values query")
}

func TestOtelClickhouseLogs_Errors(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns,
		err: errors.New("Code: 60. DB::Exception: Unknown table expression identifier 'otel_logs'. (UNKNOWN_TABLE)")}
	withFakeOtelLogs(t, f)
	s := &OtelClickhouseLogSource{}
	base := FetchLogRequest{AccountId: "acc", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs}

	req := base
	req.Query = `{namespace="demo"}`
	_, err := s.QueryLogs(otelLogsCtx(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logs collection is not enabled on this cluster")

	for _, tc := range []struct {
		mutate func(*FetchLogRequest)
		want   string
	}{
		{func(r *FetchLogRequest) { r.Query = `{namespace="demo"} | line_format "{{.x}}"` }, "unsupported LogQL: line_format"},
		{func(r *FetchLogRequest) { r.Query = `{cluster="x"}` }, `unknown label "cluster"`},
		{func(r *FetchLogRequest) { r.Query = `{namespace="demo"}`; r.Limit = 5001 }, "limit 5001"},
		{func(r *FetchLogRequest) { r.Query = `{namespace="demo"}`; r.StartTime = r.EndTime - 31*24*3600*1000 }, "longer than 30 days"},
		{func(r *FetchLogRequest) { r.AccountId = "" }, "account ID is required"},
		{func(r *FetchLogRequest) {}, "needs LogQL or a filter"},
	} {
		r := base
		tc.mutate(&r)
		_, err := s.QueryLogs(otelLogsCtx(), r)
		require.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestOtelClickhouseLogs_SchemaDetection(t *testing.T) {
	f := &fakeOtelLogsExec{probeErr: errors.New("relay down"), rows: logRows()}
	withFakeOtelLogs(t, f)
	s := &OtelClickhouseLogSource{}
	req := FetchLogRequest{AccountId: "acc", Query: `{namespace="demo"}`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs}

	// The probe failed: labels come from the attribute maps, which every otel_logs has.
	_, err := s.QueryLogs(otelLogsCtx(), req)
	require.NoError(t, err)
	assert.Contains(t, f.lastSQL(), "ResourceAttributes['k8s.namespace.name'] = 'demo'")

	// A table with the runner's columns reads them, and the answer is cached.
	f.probeErr, f.probeCols = nil, runnerLabelColumns
	otelLogsSchemas.Lock()
	otelLogsSchemas.m = map[string]otelLogsSchemaEntry{}
	otelLogsSchemas.Unlock()
	for range 3 {
		_, err = s.QueryLogs(otelLogsCtx(), req)
		require.NoError(t, err)
	}
	assert.Contains(t, f.lastSQL(), "AND (namespace = 'demo')")
	assert.Equal(t, 2, f.probes, "one probe per cache fill")

	// Missing even one label column means the expression schema.
	f.probeCols = runnerLabelColumns[1:]
	otelLogsSchemas.Lock()
	otelLogsSchemas.m = map[string]otelLogsSchemaEntry{}
	otelLogsSchemas.Unlock()
	_, err = s.QueryLogs(otelLogsCtx(), req)
	require.NoError(t, err)
	assert.Contains(t, f.lastSQL(), "ResourceAttributes['k8s.namespace.name'] = 'demo'")
}

func TestOtelClickhouseLogs_Labels(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: RawTraceResult{Columns: []string{"v"}, Rows: [][]any{{"node-a"}, {"node-b"}}}}
	withFakeOtelLogs(t, f)
	s := &OtelClickhouseLogSource{}

	labels, err := s.QueryLabels(otelLogsCtx(), FetchLogLabelRequest{AccountId: "acc"})
	require.NoError(t, err)
	var names []string
	for _, l := range labels {
		names = append(names, l.Label)
	}
	assert.Equal(t, []string{"app", "container", "filename", "job", "level", "namespace", "node_name", "pod", "service_name", "stream"}, names)

	// A canonical name (node) maps onto the translator's label; chips narrow the
	// list, and a chip on an unknown field is skipped rather than failing.
	values, err := s.QueryLabelValues(otelLogsCtx(), FetchLogLabelValuesRequest{
		AccountId: "acc", LabelName: "node", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
		CurrentFilters: map[string]string{"namespace": "demo", "cluster": "c1", "severity": "warn"},
	})
	require.NoError(t, err)
	assert.Equal(t, []OutputLogLabelValue{{Value: "node-a", Attributes: map[string]any{}}, {Value: "node-b", Attributes: map[string]any{}}}, values)
	sql := f.lastSQL()
	assert.True(t, strings.HasPrefix(sql, "SELECT DISTINCT node AS v FROM otel_logs WHERE"), sql)
	assert.Contains(t, sql, "(namespace = 'demo')")
	assert.Contains(t, sql, "(level = 'warning')")
	assert.NotContains(t, sql, "cluster")

	_, err = s.QueryLabelValues(otelLogsCtx(), FetchLogLabelValuesRequest{AccountId: "acc", LabelName: "instance"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown label "instance"`)
}

func TestOtelClickhouseLogs_LogGroup(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	out, err := (&OtelClickhouseLogSource{}).QueryLogGroup(otelLogsCtx(), FetchLogGroupRequest{
		AccountId: "acc", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
		Request: map[string]any{"selectedNamespace": "demo", "selectedWorkload": "payment"},
	})
	require.NoError(t, err)
	sql := f.lastSQL()
	assert.Contains(t, sql, "namespace = 'demo'")
	assert.Contains(t, sql, "workload = 'payment'")
	assert.Contains(t, sql, "SeverityNumber >= 17")
	assert.Contains(t, sql, "LIMIT 1000")
	assert.NotEmpty(t, out.Groups, "the error line is grouped")
}

// Only error lines are grouped, so the error-word match must not fetch lines whose
// record already has a lower level (on dev, info lines containing "err" filled the
// 1000-line fetch), nor severity-less stdout lines whose only error word is an
// err=<nil> field. The level label is not used: without a severity it reads both as
// errors.
func TestOtelClickhouseLogs_LogGroupTextMatchOnlyForUnleveledLines(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	_, err := (&OtelClickhouseLogSource{}).QueryLogGroup(otelLogsCtx(), FetchLogGroupRequest{
		AccountId: "acc", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	sql := f.lastSQL()
	assert.Contains(t, sql, "(SeverityNumber >= 17 OR (SeverityNumber = 0 AND match(replaceRegexpAll(Body, ")
	assert.NotContains(t, sql, "level IN", "the level label reads err=<nil> as an error")
}

// Every statement carries a ClickHouse-side time limit matching its deadline, so a
// query we stop waiting for also stops running in the customer's ClickHouse.
func TestOtelClickhouseLogs_QueriesStopInClickHouseAtTheDeadline(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	_, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `{namespace="demo"}`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(f.lastSQL(), " SETTINGS max_execution_time = 10 FORMAT JSONCompact"), f.lastSQL())

	_, err = (&OtelClickhouseLogSource{}).QueryLabelValues(otelLogsCtx(), FetchLogLabelValuesRequest{
		AccountId: "acc", LabelName: "pod", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(f.lastSQL(), " SETTINGS max_execution_time = 30 FORMAT JSONCompact"), "no caller deadline: ours, %s", f.lastSQL())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.Equal(t, "SELECT 1 SETTINGS max_execution_time = 2 FORMAT JSONCompact", withExecutionLimit(ctx, "SELECT 1"))
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	assert.Equal(t, "SELECT 1 SETTINGS max_execution_time = 1 FORMAT JSONCompact", withExecutionLimit(expired, "SELECT 1"))
	// A deadline past the reader user's ceiling asks for the ceiling: ClickHouse
	// refuses a setting above it (Code 452) instead of clamping it.
	long, cancelLong := context.WithTimeout(context.Background(), time.Hour)
	defer cancelLong()
	assert.Equal(t, "SELECT 1 SETTINGS max_execution_time = 600 FORMAT JSONCompact", withExecutionLimit(long, "SELECT 1"))
}

// The agent adds FORMAT JSONCompact only to a statement with no "FORMAT " anywhere in it,
// so a line filter carrying the word would come back as TabSeparated. Every statement,
// the schema probe included, names the format itself.
func TestOtelClickhouseLogs_LineFilterWithFormatStillAnswersJSON(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	var sent []string
	exec := f.exec
	otelLogsExec = func(ctx context.Context, sql, acc string) (RawTraceResult, error) {
		sent = append(sent, sql)
		return exec(ctx, sql, acc)
	}

	_, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `{namespace="demo"} |= "invalid format string"`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	require.Len(t, sent, 2, "the schema probe, then the query")
	assert.Contains(t, sent[1], "'invalid format string'")
	for _, sql := range sent {
		assert.True(t, strings.HasSuffix(sql, " FORMAT JSONCompact"), sql)
	}
}

// A readonly=1 ClickHouse user may not change settings, so ClickHouse refuses
// max_execution_time; the statement runs again without it rather than failing every
// logs query (and the schema probe) on such an install.
func TestOtelClickhouseLogs_ReadonlyUserRunsWithoutSettings(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	var sent []string
	exec := f.exec
	otelLogsExec = func(ctx context.Context, sql, acc string) (RawTraceResult, error) {
		sent = append(sent, sql)
		if strings.Contains(sql, " SETTINGS max_execution_time") {
			return RawTraceResult{}, errors.New("clickhouse: HTTP 500: Code: 164. DB::Exception: Cannot modify 'max_execution_time' setting in readonly mode. (READONLY) (version 24.12.6.70 (official build))")
		}
		return exec(ctx, sql, acc)
	}

	out, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `{namespace="demo"}`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out)
	require.Len(t, sent, 4, "probe and query, each refused once and run again")
	assert.True(t, strings.HasSuffix(sent[3], " FORMAT JSONCompact"), sent[3])
	assert.NotContains(t, sent[3], "SETTINGS")
	assert.Contains(t, sent[3], "AND (namespace = 'demo')", "the probe got through on its retry, so the runner's columns are read")

	// Any other error is not retried: every statement sent keeps its SETTINGS.
	sent = nil
	otelLogsExec = func(_ context.Context, sql, _ string) (RawTraceResult, error) {
		sent = append(sent, sql)
		return RawTraceResult{}, errors.New("clickhouse: HTTP 500: Code: 241. DB::Exception: Memory limit exceeded. (MEMORY_LIMIT_EXCEEDED)")
	}
	_, err = (&OtelClickhouseLogSource{}).QueryLabelValues(otelLogsCtx(), FetchLogLabelValuesRequest{
		AccountId: "acc", LabelName: "pod", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.Error(t, err)
	require.NotEmpty(t, sent)
	for _, sql := range sent {
		assert.Contains(t, sql, " SETTINGS max_execution_time", "retried without SETTINGS: %s", sql)
	}

	// Either form ClickHouse prints the refusal in is recognised; other codes are not.
	for msg, want := range map[string]bool{
		"Code: 164. DB::Exception: Cannot modify 'max_execution_time' setting in readonly mode":  true,
		"DB::Exception: Cannot modify 'max_execution_time' setting in readonly mode. (READONLY)": true,
		"code: 164, message: Cannot modify 'max_execution_time' setting in readonly mode":        true,
		"Code: 1640. DB::Exception: something else":                                              false,
		"Code: 242. DB::Exception: Table is in readonly mode. (TABLE_IS_READ_ONLY)":              false,
	} {
		assert.Equal(t, want, readonlySettings.MatchString(msg), msg)
	}
}

func TestOtelClickhouseLogs_WithLineFilter(t *testing.T) {
	s := &OtelClickhouseLogSource{}
	q, ok := s.withLineFilter(`{namespace="demo"} |= "x"`, `(?i)error`)
	assert.True(t, ok)
	assert.Equal(t, "{namespace=\"demo\"} |= \"x\" |~ `(?i)error`", q)
	_, ok = s.withLineFilter(`{"_binary":{"namespace":{"_eq":"demo"}}}`, `error`)
	assert.False(t, ok, "the where-clause JSON of a Builder query is not LogQL")
	_, ok = s.withLineFilter(`{namespace="demo"}`, "a`b")
	assert.False(t, ok)
}

func TestOtelClickhouseLogs_LabelMapping(t *testing.T) {
	s := &OtelClickhouseLogSource{}
	m := s.GetLabelMapping()
	assert.Equal(t, "log", m["message"])
	assert.Equal(t, "node_name", m["node"])
	assert.Equal(t, "service_name", m["service"])
	assert.Equal(t, "level", m["severity"])
	m["node"] = "changed"
	assert.Equal(t, "node_name", s.GetLabelMapping()["node"], "callers get a copy")

	var names []string
	for _, f := range resolvableCanonicalLogFields(s, s.GetLabelMapping()) {
		names = append(names, f.name)
	}
	assert.NotContains(t, names, "cluster", "cluster is refused, so it is not advertised")
	assert.Contains(t, names, "node")
}

// FetchLogs sends GetQuery's display back as Query. Whatever the where-clause,
// that must run (as the where-clause alone), and the display must be the same
// LogQL on every call, or QueryLogs takes it for Code-mode LogQL.
func TestOtelClickhouseLogs_DisplayComesBackAsQuery(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: logRows()}
	withFakeOtelLogs(t, f)
	s := &OtelClickhouseLogSource{}
	type bw = query.BinaryWhereClause
	type ops = map[query.BinaryWhereClauseType]any
	and := func(c ...query.QueryWhereClause) query.QueryWhereClause { return query.QueryWhereClause{And: c} }
	prod := query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "prod"}}}

	for _, tc := range []struct {
		name  string
		where query.QueryWhereClause
		// display is GetQuery's LogQL, "" when it has no exact LogQL form.
		display string
	}{
		// An _and (default filters wrap the request in one) renders with
		// trailing spaces, which the display drops.
		{"and", and(prod, query.QueryWhereClause{Binary: bw{"log": ops{query.Contains: "error"}}}), `{namespace="prod"}  |= "error"`},
		{"set", query.QueryWhereClause{Binary: bw{"namespace": ops{query.In: []any{"a", "b"}}}}, `{namespace=~"a|b"}`},
		{"negative pair", and(prod, query.QueryWhereClause{Binary: bw{"pod": ops{query.Nq: "x"}}}, query.QueryWhereClause{Binary: bw{"pod": ops{query.Nq: "y"}}}), `{namespace="prod", pod!~"x|y"}`},
		// BuildLokiQuery writes a regex into "..." unescaped: \d is no LogQL string.
		{"line regex", and(query.QueryWhereClause{Binary: bw{"log": ops{query.Regex: `\d{3}`}}}, prod), ""},
		{"dotted set", and(query.QueryWhereClause{Binary: bw{"node_name": ops{query.In: []any{"ip-10-0-1-5.ec2.internal", "n2"}}}}, prod), ""},
		{"dotted icontains", query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "prod"}, "log": ops{query.IContains: "conn.refused"}}}, ""},
		{"backslash value", query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: `a\tb`}}}, ""},
		// Two positives on one label merge into one alternation: OR, not AND.
		{"duplicate positive", and(query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "demo"}}}, prod), ""},
		{"set and regex", and(query.QueryWhereClause{Binary: bw{"namespace": ops{query.In: []any{"a"}}}}, query.QueryWhereClause{Binary: bw{"namespace": ops{query.Regex: "b.*"}}}), ""},
		// body is a body field to WhereCond but a stream label to BuildLokiQuery.
		{"body field", query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "prod"}, "body": ops{query.Eq: "boom"}}}, ""},
		// Two line filters in one clause render in map order.
		{"two line filters", query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "prod"}, "log": ops{query.Contains: "error", query.Nq: "debug"}}}, ""},
		{"empty set", query.QueryWhereClause{Binary: bw{"namespace": ops{query.In: []any{}}, "pod": ops{query.Eq: "p"}}}, ""},
		// A quote in an _in value is not escaped: it would end the string and
		// add a container="y" matcher to the display.
		{"quote in set", and(prod, query.QueryWhereClause{Binary: bw{"pod": ops{query.NotIn: []any{`x", container="y`}}}}), ""},
		{"quote in value", query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: `a"b`}}}, ""},
		// _not_in and _nregex both render as !~ and merge in map order.
		{"set and negative regex", query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "p"}, "pod": ops{query.NotIn: []any{"a", "b"}, query.NRegex: "c.*"}}}, ""},
		// BuildLokiQuery refuses this regex; the where-clause still runs.
		{"unrenderable regex", query.QueryWhereClause{Binary: bw{"namespace": ops{query.Eq: "prod"}, "log": ops{query.Regex: "(a+)+"}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := FetchLogRequest{AccountId: "acc", StartTime: otelLogsStartMs, EndTime: otelLogsEndMs, QueryRequest: LogsQueryBuilderRequest{Where: tc.where}}
			display, err := s.GetQuery(otelLogsCtx(), req)
			require.NoError(t, err)
			assert.Equal(t, tc.display, display)
			for i := 0; i < 50; i++ {
				again, _ := s.GetQuery(otelLogsCtx(), req)
				require.Equal(t, display, again, "the display changed between calls")
			}

			req.Query = display
			_, err = s.QueryLogs(otelLogsCtx(), req)
			require.NoError(t, err)
			assert.Contains(t, f.lastSQL(), "AND (1)\n  AND (", "the where-clause runs alone")
		})
	}
}

// A record's own severity is trusted; without one (SeverityNumber 0) the level
// label is the store's guess from error words, so the line is classified the
// way Loki's are.
func TestOtelClickhouseLogs_SeverityFromRecordOnly(t *testing.T) {
	f := &fakeOtelLogsExec{probeCols: runnerLabelColumns, rows: RawTraceResult{
		Columns: []string{"ts", "Body", "l_level", "severity_number"},
		Rows: [][]any{
			{"1791828000000000000", "payment failed", "error", float64(17)},
			{"1791828000000000001", `level=info msg="request ok" err=<nil>`, "error", float64(0)},
			{"1791828000000000002", "boom", "warning", "13"},
		},
	}}
	withFakeOtelLogs(t, f)
	logs, err := (&OtelClickhouseLogSource{}).QueryLogs(otelLogsCtx(), FetchLogRequest{
		AccountId: "acc", Query: `{namespace="demo"}`, StartTime: otelLogsStartMs, EndTime: otelLogsEndMs,
	})
	require.NoError(t, err)
	assert.Contains(t, f.lastSQL(), "SeverityNumber AS severity_number")
	require.Len(t, logs, 3)
	assert.Equal(t, "error", logs[0].Severity)
	assert.Equal(t, "info", logs[1].Severity)
	assert.Equal(t, "error", logs[1].Labels["level"], "the label is the store's value")
	assert.Equal(t, "warning", logs[2].Severity)
	_, isLabel := logs[0].Labels["severity_number"]
	assert.False(t, isLabel)
}

func TestParseLokiTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"1791828000":           time.Unix(1791828000, 0),
		"1791828000.5":         time.Unix(1791828000, 500_000_000),
		"1791828000123456789":  time.Unix(0, 1791828000123456789),
		"2026-10-12T18:00:00Z": time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC),
	} {
		got, err := parseLokiTime(in)
		require.NoError(t, err, in)
		assert.True(t, want.Equal(got), "%s: %v, want %v", in, got, want)
	}
	_, err := parseLokiTime("yesterday")
	assert.Error(t, err)
}

func TestOtelLogsError(t *testing.T) {
	for msg, missing := range map[string]bool{
		"Code: 60. DB::Exception: Unknown table expression identifier 'otel_logs'. (UNKNOWN_TABLE)": true,
		"code: 60, message: Unknown table expression identifier 'otel_logs'":                        true,
		"Code: 601. DB::Exception: something else":                                                  false,
		"Code: 210. DB::NetException: Connection refused":                                           false,
	} {
		got := strings.Contains(otelLogsError(errors.New(msg), otelLogsDefaultTable).Error(), "logs collection is not enabled")
		assert.Equal(t, missing, got, msg)
	}
}
