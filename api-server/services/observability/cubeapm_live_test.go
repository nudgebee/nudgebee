package observability

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/security"
)

// Live end-to-end checks against a real CubeAPM instance, driving the same
// LogSource / TraceSource / MetricSource the UI calls rather than curling the
// API. Everything else in this package is tested against hand-built payloads,
// so this is the only check that exercises request construction, auth and
// response parsing against the actual service.
//
// Skipped unless explicitly enabled, so it never runs in CI:
//
//	LIVE_CUBEAPM_TENANT_ID=<tenant-uuid> \
//	LIVE_CUBEAPM_ACCOUNT_ID=<cloud-account-uuid> \
//	go test ./observability/ -run TestLiveCubeAPM -v
//
// The process needs the same environment the api-server runs with (database
// connection + encryption key), because the connection details are read from
// the stored integration rather than passed in.
//
// Read-only: issues queries only, and writes nothing to CubeAPM or the database.
func liveCubeAPMContext(t *testing.T) (*security.RequestContext, string) {
	t.Helper()

	tenantId := os.Getenv("LIVE_CUBEAPM_TENANT_ID")
	accountId := os.Getenv("LIVE_CUBEAPM_ACCOUNT_ID")
	if tenantId == "" || accountId == "" {
		t.Skip("set LIVE_CUBEAPM_TENANT_ID and LIVE_CUBEAPM_ACCOUNT_ID to run this against a real CubeAPM instance")
	}
	return security.NewRequestContextForTenantAdmin(tenantId, slog.Default(), nil, nil), accountId
}

// liveCubeAPMWindow is the lookback every live query uses. Wide enough that a
// demo workload has data, narrow enough to stay cheap.
func liveCubeAPMWindow() (startMs, endMs int64) {
	now := time.Now()
	return now.Add(-time.Hour).UnixMilli(), now.UnixMilli()
}

func TestLiveCubeAPMConfig(t *testing.T) {
	ctx, accountId := liveCubeAPMContext(t)

	cfg, err := integrations.GetCubeAPMConfigs(ctx, accountId)
	if err != nil {
		t.Fatalf("GetCubeAPMConfigs failed: %v", err)
	}
	if cfg.URL == "" {
		t.Fatal("resolved config has no URL")
	}
	t.Logf("resolved CubeAPM url=%s env=%q adminURL=%q", cfg.URL, cfg.Env, cfg.AdminURL)
}

func TestLiveCubeAPMMetrics(t *testing.T) {
	ctx, accountId := liveCubeAPMContext(t)
	startMs, endMs := liveCubeAPMWindow()
	s := &CubeAPMMetricSource{}

	metrics, err := s.FetchMetricList(ctx, FetchMetricsListRequest{
		AccountId: accountId, StartTime: startMs, EndTime: endMs,
	})
	if err != nil {
		t.Fatalf("FetchMetricList failed: %v", err)
	}
	if len(metrics) == 0 {
		t.Fatal("no metric families returned; the metric picker would be empty")
	}
	t.Logf("METRICS: %d families (e.g. %s)", len(metrics), metrics[0].Metric)

	labels, err := s.FetchMetricsLabels(ctx, FetchMetricLabelsRequest{
		AccountId: accountId, StartTime: startMs, EndTime: endMs,
	})
	if err != nil {
		t.Fatalf("FetchMetricsLabels failed: %v", err)
	}
	if len(labels) == 0 {
		t.Fatal("no metric labels returned; label suggestions would be empty")
	}
	t.Logf("METRIC LABELS: %d (e.g. %s)", len(labels), labels[0].Label)

	// Label VALUES are what the filter dropdown offers once a label is picked.
	values, err := s.FetchMetricLabelValues(ctx, FetchMetricsLabelValueRequest{
		AccountId: accountId, Label: "service", StartTime: startMs, EndTime: endMs,
		Request: map[string]any{},
	})
	if err != nil {
		t.Fatalf("FetchMetricLabelValues failed: %v", err)
	}
	t.Logf("METRIC LABEL VALUES for service: %d", len(values))
	for i, v := range values {
		if i >= 5 {
			break
		}
		t.Logf("    %s", v.Value)
	}

	// A real range query through the full PromQL path.
	out, err := s.FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:    accountId,
		Queries:      map[string]string{"calls": "sum(rate(cube_apm_calls_total[5m])) by (service)"},
		StartTime:    startMs,
		EndTime:      endMs,
		StepInterval: 60,
	})
	if err != nil {
		t.Fatalf("FetchMetricsQuery failed: %v", err)
	}
	for _, r := range out.Results {
		if r.Error != nil {
			t.Fatalf("query %q returned error: %s", r.QueryKey, *r.Error)
		}
		total := 0
		for _, p := range r.Payload {
			total += len(p.Values)
		}
		t.Logf("METRIC QUERY: %d series, %d points", len(r.Payload), total)
		if len(r.Payload) == 0 {
			t.Error("range query returned no series; charts would render empty")
		}
	}
}

func TestLiveCubeAPMLogs(t *testing.T) {
	ctx, accountId := liveCubeAPMContext(t)
	startMs, endMs := liveCubeAPMWindow()
	s := &CubeAPMLogSource{}

	logs, err := s.QueryLogs(ctx, FetchLogRequest{
		AccountId: accountId, StartTime: startMs, EndTime: endMs, Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryLogs failed: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("no logs returned; the Logs tab would be empty")
	}
	t.Logf("LOGS: %d rows; first: severity=%q ts=%q msg=%.60q",
		len(logs), logs[0].Severity, logs[0].Timestamp, logs[0].Message)
	if logs[0].Timestamp == "" || logs[0].Message == "" {
		t.Error("log row is missing timestamp or message; the table would show blanks")
	}

	// Label discovery is what populates the filter dropdown.
	labels, err := s.QueryLabels(ctx, FetchLogLabelRequest{
		AccountId: accountId, StartTime: startMs, EndTime: endMs,
	})
	if err != nil {
		t.Fatalf("QueryLabels failed: %v", err)
	}
	if len(labels) == 0 {
		t.Fatal("no log labels discovered; label suggestions would be empty")
	}
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Label)
	}
	t.Logf("LOG LABELS (%d): %v", len(labels), names)

	// Values for the canonical service label, which maps to the provider field.
	values, err := s.QueryLabelValues(ctx, FetchLogLabelValuesRequest{
		AccountId: accountId, LabelName: "service", StartTime: startMs, EndTime: endMs,
	})
	if err != nil {
		t.Fatalf("QueryLabelValues failed: %v", err)
	}
	if len(values) == 0 {
		t.Error("no values for service; the label mapping resolves to a field with no data")
	}
	vals := make([]string, 0, len(values))
	for _, v := range values {
		vals = append(vals, v.Value)
	}
	t.Logf("LOG LABEL VALUES for service (%d): %v", len(values), vals)

	groups, err := s.QueryLogGroup(ctx, FetchLogGroupRequest{
		AccountId: accountId, StartTime: startMs, EndTime: endMs, Request: map[string]any{},
	})
	if err != nil {
		t.Fatalf("QueryLogGroup failed: %v", err)
	}
	t.Logf("LOG GROUPS: %d", len(groups.Groups))
	for i, g := range groups.Groups {
		if i >= 3 {
			break
		}
		t.Logf("    count=%d level=%s sample=%.50q", g.Count, g.Level, g.Sample)
	}
}

func TestLiveCubeAPMTraces(t *testing.T) {
	ctx, accountId := liveCubeAPMContext(t)
	startMs, endMs := liveCubeAPMWindow()
	s := &CubeAPMTraceSource{}

	req := TracesV3Request{
		AccountId: accountId, StartTime: startMs, EndTime: endMs,
		QueryRequest: TracesQueryBuilderRequest{Limit: 20},
	}

	spans, err := s.QueryTraces(ctx, req)
	if err != nil {
		t.Fatalf("QueryTraces failed: %v", err)
	}
	if len(spans) == 0 {
		t.Fatal("no spans returned; the Traces tab would be empty")
	}
	sp := spans[0]
	t.Logf("TRACES: %d spans; first: service=%q op=%q dur=%dns trace=%s",
		len(spans), sp.ServiceName, sp.SpanName, sp.DurationNs, sp.TraceID)
	if sp.TraceID == "" || sp.SpanID == "" {
		t.Error("span is missing trace/span id; the waterfall could not link spans")
	}
	if sp.ServiceName == "" {
		t.Error("span has no service name; grouping by service would collapse")
	}
	if sp.DurationNs <= 0 {
		t.Error("span duration is zero; every waterfall bar would be flat")
	}

	labels, err := s.QueryLabels(ctx, FetchTraceLabelRequest{
		AccountId: accountId, StartTime: startMs, EndTime: endMs,
	})
	if err != nil {
		t.Fatalf("trace QueryLabels failed: %v", err)
	}
	t.Logf("TRACE LABELS: %d", len(labels))
	for i, l := range labels {
		if i >= 8 {
			break
		}
		t.Logf("    %s", l.Label)
	}

	vals, err := s.GetLabelValues(ctx, TracesV3LabelValuesRequest{
		AccountId: accountId, Label: "workload_name", StartTime: startMs, EndTime: endMs,
	})
	if err != nil {
		t.Fatalf("GetLabelValues failed: %v", err)
	}
	t.Logf("TRACE LABEL VALUES for workload_name: %v", vals.Values)
	if len(vals.Values) == 0 {
		t.Error("no service values discovered for traces")
	}

	groups, err := s.QueryGroupedTraces(ctx, req)
	if err != nil {
		t.Fatalf("QueryGroupedTraces failed: %v", err)
	}
	t.Logf("TRACE GROUPS: %d", len(groups))
	for i, g := range groups {
		if i >= 3 {
			break
		}
		t.Logf("    %s / %s calls=%d errors=%d p95=%dns",
			g.WorkloadName, g.SpanName, g.Count, g.ErrorCount, g.P95Latency)
	}

	// The waterfall: fetch one full trace by id.
	heat, err := s.QueryTracesHeatmap(ctx, TracesHeatMapRequest{
		AccountId: accountId, TraceId: sp.TraceID, StartTime: startMs, EndTime: endMs,
	})
	if err != nil {
		t.Fatalf("QueryTracesHeatmap failed: %v", err)
	}
	t.Logf("TRACE WATERFALL for %s: %d spans", sp.TraceID, len(heat))
	if len(heat) == 0 {
		t.Error("trace detail returned no spans; the waterfall would be blank")
	}
}
