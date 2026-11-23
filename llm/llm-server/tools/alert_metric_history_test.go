package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule shapes taken from the alerts dev and the customer environment fire.
func TestAlertLevelQuery(t *testing.T) {
	tests := []struct {
		name, expr, series, op string
		level                  float64
		ok                     bool
	}{
		{"level rule", `histogram_quantile(0.95, sum by (le) (rate(rpc_server_duration_bucket{service="checkout"}[5m]))) > 0.5`,
			`histogram_quantile(0.95, sum by (le) (rate(rpc_server_duration_bucket{service="checkout"}[5m])))`, ">", 0.5, true},
		{"level on the left of a guard", `rate(errors_total[5m]) / rate(requests_total[5m]) > 0.05 and rate(requests_total[5m]) > 1`,
			`rate(errors_total[5m]) / rate(requests_total[5m])`, ">", 0.05, true},
		{"number first", `10 >= free_bytes`, `free_bytes`, "<=", 10, true},
		{"parenthesised", `((node_load1) > 4)`, `node_load1`, ">", 4, true},
		{"state test", `kube_pod_status_ready{condition="true"} == 0`, "", "", 0, false},
		{"not equal", `kube_deployment_status_condition{condition="Progressing",status="false"} != 0`, "", "", 0, false},
		{"absent", `absent(up{job="kube-proxy"})`, "", "", 0, false},
		{"two series", `kube_deployment_spec_replicas > kube_deployment_status_replicas_available`, "", "", 0, false},
		{"or", `a > 1 or b > 2`, "", "", 0, false},
		{"empty", ``, "", "", 0, false},
		{"json, not PromQL", `{"MetricName":"CPUUtilization","Threshold":80}`, "", "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			series, op, level, ok := AlertLevelQuery(tc.expr)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.series, series)
			assert.Equal(t, tc.op, op)
			assert.Equal(t, tc.level, level)
		})
	}
}

func TestFetchAlertMetricHistory(t *testing.T) {
	var req core.ObservabilityMetricsQueryRequest
	payload := []core.ObservabilityMetricsQuerySeries{}
	orig := promQueryMetrics
	promQueryMetrics = func(_ security.RequestContext, r core.ObservabilityMetricsQueryRequest) (core.ObservabilityMetricsQueryResponse, error) {
		req = r
		return core.ObservabilityMetricsQueryResponse{Results: []core.ObservabilityMetricsQueryResult{{QueryKey: "query", Payload: payload}}}, nil
	}
	t.Cleanup(func() { promQueryMetrics = orig })
	series := func(labels map[string]string) core.ObservabilityMetricsQuerySeries {
		return core.ObservabilityMetricsQuerySeries{Metric: labels, Timestamps: []int64{1, 2}, Values: []float64{0.2, 0.3}}
	}
	end := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	alert := map[string]string{"service_name": "checkout", "namespace": "demo", "alertname": "SLO", "severity": "warning"}

	t.Run("picks the alert's own series", func(t *testing.T) {
		payload = []core.ObservabilityMetricsQuerySeries{
			series(map[string]string{"service_name": "cart", "namespace": "demo"}),
			series(map[string]string{"service_name": "checkout", "namespace": "demo"}),
		}
		body, ok, err := FetchAlertMetricHistory(security.RequestContext{}, "acct", "p95", ">", alert, end.Add(-72*time.Hour), end, 15*time.Minute)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, 900, req.StepInterval)
		assert.Equal(t, end.Add(-72*time.Hour).UnixMilli(), req.StartTime)
		picked, isMap := body.(map[string]any)
		require.True(t, isMap)
		assert.Equal(t, "checkout", picked["metric"].(map[string]any)["service_name"])
	})

	t.Run("a lone series is the alert's whatever its labels", func(t *testing.T) {
		payload = []core.ObservabilityMetricsQuerySeries{series(map[string]string{})}
		_, ok, err := FetchAlertMetricHistory(security.RequestContext{}, "acct", "p95", ">", alert, end.Add(-time.Hour), end, time.Minute)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("the pods of the alert's workload: the one nearest the level is the alerting one", func(t *testing.T) {
		pod := func(name string, last float64) core.ObservabilityMetricsQuerySeries {
			return core.ObservabilityMetricsQuerySeries{Metric: map[string]string{"namespace": "demo", "pod": name}, Timestamps: []int64{1, 2}, Values: []float64{0.1, last}}
		}
		payload = []core.ObservabilityMetricsQuerySeries{
			pod("checkout-7c9d8f5b4-x2lkp", 0.31),
			pod("checkout-7c9d8f5b4-q9zrt", 0.84),
			pod("checkoutv2-55d8c4-c7wbm", 0.99),     // another workload: "checkoutv2" is not a pod of "checkout"
			pod("checkout-v2-6b8f9d7c5-k2hvn", 0.98), // nor is "checkout-v2"
			pod("checkout-worker-m4tzq", 0.97),       // nor "checkout-worker"
		}
		body, ok, err := FetchAlertMetricHistory(security.RequestContext{}, "acct", "mem", ">", map[string]string{"namespace": "demo", "pod": "checkout"}, end.Add(-time.Hour), end, time.Minute)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "checkout-7c9d8f5b4-q9zrt", body.(map[string]any)["metric"].(map[string]any)["pod"], "not another workload, whose last value is higher")
	})

	t.Run("a pod replaced before the alert is not the alerting one, however high it ended", func(t *testing.T) {
		payload = []core.ObservabilityMetricsQuerySeries{
			{Metric: map[string]string{"namespace": "demo", "pod": "checkout-777fbd9c8-w5ndl"}, Timestamps: []int64{1, 2}, Values: []float64{0.9, 0.94}},
			{Metric: map[string]string{"namespace": "demo", "pod": "checkout-5cfd8b7d9-p8lxs"}, Timestamps: []int64{3, 4, 5}, Values: []float64{0.3, 0.4, 0.52}},
		}
		body, ok, err := FetchAlertMetricHistory(security.RequestContext{}, "acct", "cpu", ">", map[string]string{"namespace": "demo", "pod": "checkout"}, end.Add(-time.Hour), end, time.Minute)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "checkout-5cfd8b7d9-p8lxs", body.(map[string]any)["metric"].(map[string]any)["pod"])
	})

	t.Run("no candidate: nothing is saved", func(t *testing.T) {
		payload = []core.ObservabilityMetricsQuerySeries{
			series(map[string]string{"service_name": "cart"}),
			series(map[string]string{"service_name": "email"}),
		}
		_, ok, err := FetchAlertMetricHistory(security.RequestContext{}, "acct", "p95", ">", alert, end.Add(-time.Hour), end, time.Minute)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestCloudWatchAlarmCommand(t *testing.T) {
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	end := start.Add(72 * time.Hour)
	alarm := func(extra string) string {
		return `{"AlarmArn":"arn:aws:cloudwatch:us-east-1:123456789012:alarm:api-cpu","MetricName":"CPUUtilization","Namespace":"AWS/EC2",` +
			`"Statistic":"Average","Threshold":80,"ComparisonOperator":"GreaterThanThreshold",` +
			`"Dimensions":[{"Name":"InstanceId","Value":"i-0123456789abcdef0"}]` + extra + `}`
	}

	cmd, op, level, ok := CloudWatchAlarmCommand(alarm(""), start, end, 15*time.Minute)
	require.True(t, ok)
	assert.Equal(t, ">", op)
	assert.Equal(t, 80.0, level)
	assert.Equal(t, "aws cloudwatch get-metric-statistics --region us-east-1 --namespace 'AWS/EC2' --metric-name 'CPUUtilization' "+
		"--start-time 2026-10-18T12:00:00Z --end-time 2026-10-21T12:00:00Z --period 900 --statistics Average "+
		"--dimensions Name='InstanceId',Value='i-0123456789abcdef0' --output json", cmd)

	cmd, _, _, ok = CloudWatchAlarmCommand(alarm(`,"ExtendedStatistic":"p99"`), start, end, 15*time.Minute)
	require.True(t, ok)
	assert.Contains(t, cmd, "--extended-statistics p99")
	assert.NotContains(t, cmd, "--statistics Average")

	for name, expr := range map[string]string{
		"metric math":       alarm(`,"Metrics":[{"Id":"m1"}]`),
		"anomaly band":      strings.Replace(alarm(""), "GreaterThanThreshold", "LessThanLowerOrGreaterThanUpperThreshold", 1),
		"quote in a value":  strings.Replace(alarm(""), "i-0123456789abcdef0", "i-0'; rm -rf /", 1),
		"no threshold":      strings.Replace(alarm(""), `"Threshold":80,`, "", 1),
		"no region in arn":  strings.Replace(alarm(""), "arn:aws:cloudwatch:us-east-1:", "arn:aws:cloudwatch::", 1),
		"a Prometheus rule": `rate(x[5m]) > 1`,
		"empty":             ``,
	} {
		_, _, _, ok := CloudWatchAlarmCommand(expr, start, end, 15*time.Minute)
		assert.False(t, ok, name)
	}
}

func TestAnalyzeMetricSeries_AlertHistoryLeadsWithNow(t *testing.T) {
	// The file shape the analyzer saves: two views of one series.
	var tsN, vN, tsR, vR []string
	for i := 0; i < 24; i++ {
		tsN = append(tsN, fmt.Sprint(1760954400+i*900))
		vN = append(vN, "0.05")
	}
	for i := 0; i < 30; i++ {
		tsR = append(tsR, fmt.Sprint(1760954400+24*900+i*60))
		vR = append(vR, fmt.Sprint(0.1+float64(i)*0.01))
	}
	file := fmt.Sprintf(`{%q:[{"metric":{"pod":"checkout-1"},"timestamps":[%s],"values":[%s]}],%q:[{"metric":{"pod":"checkout-1"},"timestamps":[%s],"values":[%s]}]}`,
		AlertHistoryNormalView, strings.Join(tsN, ","), strings.Join(vN, ","), AlertHistoryNowView, strings.Join(tsR, ","), strings.Join(vR, ","))
	resp, out := callAnalyzeMetricSeries(t, `{"file_ref":"h.json","thresholds":[{"value":0.5,"label":"SLO 500 ms"}]}`, map[string]string{"h.json": file})
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, resp.Data)
	series := out["series"].([]any)
	require.Len(t, series, 2)
	assert.True(t, strings.HasPrefix(series[0].(map[string]any)["name"].(string), AlertHistoryNowView), "the now view leads")
	assert.Contains(t, fmt.Sprint(out["notes"]), "is the current one")

	// Each view gets a chart with the threshold as a flat line, the now view's
	// ref sorting first.
	require.NotNil(t, resp.Metadata)
	require.Len(t, resp.Metadata.Charts, 2)
	refs := make([]string, 0, 2)
	for ref := range resp.Metadata.Charts {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	var lead core.MetricChart
	require.NoError(t, json.Unmarshal(resp.Metadata.Charts[refs[0]], &lead))
	assert.Equal(t, AlertHistoryNowView+" (UTC)", lead.Title, "the title leaves out the label set")
	raw := string(resp.Metadata.Charts[refs[0]])
	assert.True(t, strings.Index(raw, `"key":"value"`) < strings.Index(raw, `"key":"SLO 500 ms`), "the series leads the thresholds: %s", raw)
	assert.Contains(t, raw, `"key":"SLO 500 ms (above 0.5)"`)
	assert.Equal(t, []string{"SLO 500 ms (above 0.5)"}, lead.Reference, "thresholds are drawn as reference lines")
	assert.NotContains(t, fmt.Sprint(resp.Data), "chart_", "the model is not told about the charts")
}

// podCheckout is the matcher narrowToAlert adds for an alert on the workload "checkout".
var podCheckout = `pod=~"checkout` + podSuffix + `?"`

func TestNarrowToAlert(t *testing.T) {
	alert := map[string]string{"namespace": "demo", "pod": "checkout", "rpc_method": "PlaceOrder", "summary": "High latency", "severity": "warning"}
	tests := []struct {
		name, series  string
		ok            bool
		want, wantNot []string
	}{
		{"grouped rule: labels it groups by", `histogram_quantile(0.95, sum by (le, namespace, pod, rpc_method) (rate(rpc_duration_bucket[2m])))`, true,
			[]string{`namespace="demo"`, podCheckout, `rpc_method="PlaceOrder"`}, []string{"summary", "severity"}},
		{"ratio of two grouped sums: both selectors", `sum by (namespace, pod) (container_memory_working_set_bytes) / sum by (namespace, pod) (kube_pod_container_resource_limits)`, true,
			[]string{`container_memory_working_set_bytes{namespace="demo",` + podCheckout + `}`, `kube_pod_container_resource_limits{namespace="demo",` + podCheckout + `}`}, nil},
		{"a matcher the rule already sets is left as it is", `sum by (namespace, pod) (x{namespace=~"demo|prod"})`, true,
			[]string{`namespace=~"demo|prod"`, podCheckout}, []string{`namespace="demo"`}},
		{"bare selector: identity labels only", `rate(errors_total[5m])`, true,
			[]string{`namespace="demo"`, podCheckout}, []string{"summary", "rpc_method", "severity"}},
		{"labels summed away are never added", `sum by (cluster) (rate(errors_total[5m]))`, false, nil, []string{"pod", "namespace"}},
		{"without keeps the rest", `sum without (pod) (rate(errors_total[5m]))`, true,
			[]string{`namespace="demo"`}, []string{"pod="}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := narrowToAlert(tc.series, alert)
			assert.Equal(t, tc.ok, ok, got)
			for _, w := range tc.want {
				assert.Contains(t, got, w)
			}
			for _, w := range tc.wantNot {
				assert.NotContains(t, got, w)
			}
		})
	}
}

func TestFetchAlertMetricHistory_NarrowsThenFallsBack(t *testing.T) {
	var queries []string
	orig := promQueryMetrics
	t.Cleanup(func() { promQueryMetrics = orig })
	promQueryMetrics = func(_ security.RequestContext, r core.ObservabilityMetricsQueryRequest) (core.ObservabilityMetricsQueryResponse, error) {
		q := r.Queries["query"]
		queries = append(queries, q)
		if strings.Contains(q, `pod=~`) {
			return core.ObservabilityMetricsQueryResponse{Results: []core.ObservabilityMetricsQueryResult{{QueryKey: "query"}}}, nil // nothing: the label is spelled differently
		}
		return core.ObservabilityMetricsQueryResponse{Results: []core.ObservabilityMetricsQueryResult{{QueryKey: "query", Payload: []core.ObservabilityMetricsQuerySeries{
			{Metric: map[string]string{"pod_name": "checkout-1"}, Timestamps: []int64{1, 2}, Values: []float64{1, 2}},
		}}}}, nil
	}
	end := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	_, ok, err := FetchAlertMetricHistory(security.RequestContext{}, "acct", `sum by (pod) (rate(x[5m]))`, ">", map[string]string{"pod": "checkout"}, end.Add(-time.Hour), end, time.Minute)
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, queries, 2)
	assert.Contains(t, queries[0], podCheckout, "the alert's series is asked for first")
	assert.Equal(t, `sum by (pod) (rate(x[5m]))`, queries[1], "then the whole rule when that finds nothing")
}

func TestLabelAgrees_PodsOfTheWorkloadOnly(t *testing.T) {
	matcher := regexp.MustCompile(`^(?:checkout` + podSuffix + `?)$`)
	for series, want := range map[string]bool{
		"checkout":                    true,
		"checkout-7c9d8f5b4-x2lkp":    true, // Deployment
		"checkout-x2lkp":              true, // DaemonSet, Job
		"checkout-0":                  true, // StatefulSet
		"checkout-29340120-x2lkp":     true, // CronJob
		"checkout-v2-6b8f9d7c5-k2hvn": false,
		"checkout-worker-m4tzq":       false,
		"checkout-worker":             false,
		"checkoutv2-55d8c4-c7wbm":     false,
	} {
		assert.Equal(t, want, labelAgrees("pod", series, "checkout"), series)
		// Prometheus anchors a regex matcher, so the narrowed query selects the same pods.
		assert.Equal(t, want, matcher.MatchString(series), "matcher: %s", series)
	}
}

// Only a pod label stands for its workload; any other label must match exactly.
func TestLabelAgrees_OtherLabelsExactly(t *testing.T) {
	assert.True(t, labelAgrees("container", "checkout", "checkout"))
	assert.False(t, labelAgrees("container", "checkout-x2lkp", "checkout"))
	assert.False(t, labelAgrees("namespace", "prod-eu", "prod"))
	assert.True(t, labelAgrees("pod_name", "checkout-7c9d8f5b4-x2lkp", "checkout"))
}

func TestAlertSeriesLimit(t *testing.T) {
	cases := []struct {
		series, op string
		limit      float64
		ok         bool
	}{
		{`hikaricp_connections_active / hikaricp_connections_max`, ">", 1, true},
		{`max by (svc) (a_inuse) / max by (svc) (db_sql_connection_max_open > 0)`, ">", 1, true},
		{`100 * (mem_used / container_spec_memory_limit_bytes)`, ">", 100, true},
		{`(mem_used * 100) / container_spec_memory_limit_bytes`, ">", 100, true},
		{`kubelet_volume_stats_available_bytes / kubelet_volume_stats_capacity_bytes`, "<", 0, true},
		{`rate(req_seconds_sum[1m]) / rate(req_seconds_count[1m])`, ">", 0, false},
		{`2 * (a / b_limit)`, ">", 0, false},
		{`node_load1`, ">", 0, false},
		{`not promql (`, ">", 0, false},
	}
	for _, c := range cases {
		limit, _, ok := AlertSeriesLimit(c.series, c.op)
		assert.Equal(t, c.ok, ok, c.series)
		assert.Equal(t, c.limit, limit, c.series)
	}

	_, scale, ok := AlertSeriesLimit(`100 * (free_bytes / volume_size_bytes)`, "<")
	assert.True(t, ok)
	assert.Equal(t, 100.0, scale, "a percentage falling toward 0 keeps its scale")
}

func TestProjectAlertHistory(t *testing.T) {
	series := func(vals []float64) []byte {
		ts := make([]int64, len(vals))
		for i := range vals {
			ts[i] = 1_792_000_000 + int64(i)*60
		}
		raw, _ := json.Marshal(map[string]any{
			AlertHistoryNowView: []any{map[string]any{"metric": map[string]string{}, "timestamps": ts, "values": vals}},
		})
		return raw
	}
	climb := make([]float64, 105, 120)
	for i := 0; i < 15; i++ {
		climb = append(climb, 0.05*float64(i/2+1))
	}

	p, ok := ProjectAlertHistory(series(climb), 1, "above", time.Time{})
	assert.True(t, ok)
	assert.False(t, p.Past)
	assert.True(t, p.Rising)
	assert.False(t, p.Earliest.IsZero())
	assert.InDelta(t, 0.4, p.Hours, 0.05)

	p, ok = ProjectAlertHistory(series(climb), 0.3, "above", time.Time{})
	assert.True(t, ok)
	assert.True(t, p.Past)
	assert.False(t, p.PastSince.IsZero())

	// An alert that has ended is read as it stood when it fired, not at its end.
	ended := append(slices.Clone(climb), 0, 0, 0, 0, 0)
	p, ok = ProjectAlertHistory(series(ended), 1, "above", time.Unix(1_792_000_000+119*60, 0))
	assert.True(t, ok)
	assert.False(t, p.Earliest.IsZero())
	assert.InDelta(t, 0.4, p.Current, 1e-9)

	_, ok = ProjectAlertHistory(series(climb), 1, "above", time.Unix(1, 0))
	assert.False(t, ok)

	_, ok = ProjectAlertHistory([]byte(`{"other view":[{"metric":{},"timestamps":[1,2],"values":[1,2]}]}`), 1, "above", time.Time{})
	assert.False(t, ok)
}
