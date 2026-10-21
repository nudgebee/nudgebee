package tools

import (
	"fmt"
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
			pod("checkout-7c9d8f-a1", 0.31),
			pod("checkout-7c9d8f-b2", 0.84),
			pod("checkoutv2-55d-c3", 0.99), // another workload: "checkoutv2" is not a pod of "checkout"
		}
		body, ok, err := FetchAlertMetricHistory(security.RequestContext{}, "acct", "mem", ">", map[string]string{"namespace": "demo", "pod": "checkout"}, end.Add(-time.Hour), end, time.Minute)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "checkout-7c9d8f-b2", body.(map[string]any)["metric"].(map[string]any)["pod"], "not the other workload checkoutv2, whose last value is higher")
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
}

func TestNarrowToAlert(t *testing.T) {
	alert := map[string]string{"namespace": "demo", "pod": "checkout", "rpc_method": "PlaceOrder", "summary": "High latency", "severity": "warning"}
	tests := []struct {
		name, series  string
		ok            bool
		want, wantNot []string
	}{
		{"grouped rule: labels it groups by", `histogram_quantile(0.95, sum by (le, namespace, pod, rpc_method) (rate(rpc_duration_bucket[2m])))`, true,
			[]string{`namespace="demo"`, `pod=~"checkout(-.*)?"`, `rpc_method="PlaceOrder"`}, []string{"summary", "severity"}},
		{"ratio of two grouped sums: both selectors", `sum by (namespace, pod) (container_memory_working_set_bytes) / sum by (namespace, pod) (kube_pod_container_resource_limits)`, true,
			[]string{`container_memory_working_set_bytes{namespace="demo",pod=~"checkout(-.*)?"}`, `kube_pod_container_resource_limits{namespace="demo",pod=~"checkout(-.*)?"}`}, nil},
		{"a matcher the rule already sets is left as it is", `sum by (namespace, pod) (x{namespace=~"demo|prod"})`, true,
			[]string{`namespace=~"demo|prod"`, `pod=~"checkout(-.*)?"`}, []string{`namespace="demo"`}},
		{"bare selector: identity labels only", `rate(errors_total[5m])`, true,
			[]string{`namespace="demo"`, `pod=~"checkout(-.*)?"`}, []string{"summary", "rpc_method", "severity"}},
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
	assert.Contains(t, queries[0], `pod=~"checkout(-.*)?"`, "the alert's series is asked for first")
	assert.Equal(t, `sum by (pod) (rate(x[5m]))`, queries[1], "then the whole rule when that finds nothing")
}
