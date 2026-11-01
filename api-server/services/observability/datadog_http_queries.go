package observability

import (
	"fmt"
	"strconv"
	"strings"

	"nudgebee/services/security"
)

// Datadog HTTP metrics for a workload: the latency table (throughput, p95, p99, total
// time, error rate per endpoint) and the status-code and max-response-time charts.
//
// These come from APM trace metrics, which differ from the Kubernetes metrics the rest
// of the Datadog builder reads in two ways:
//
//   - The metric name depends on the tracer. Datadog names a trace metric after the
//     span's operation: OpenTelemetry records http.server.request, Datadog's Java tracer
//     servlet.request, Go http.request, and so on. There is no name every app shares,
//     so the operation is probed per request (probeDatadogAPMOperation).
//   - They carry no kube_* tags, only service, env, resource_name, http.status_code.
//     The workload is matched on the APM service being named after it.

// datadogAPMOperationCandidates are the operation names probed for a workload's
// inbound HTTP spans. Order only breaks ties; the candidate with the most hits wins.
// Package-level so a tracer not listed here is a one-line addition.
var datadogAPMOperationCandidates = []string{
	"http.server.request", // OpenTelemetry
	"servlet.request",     // Datadog Java
	"http.request",        // Datadog Go (net/http)
	"web.request",         // Datadog Node / PHP generic web spans
	"express.request",     // Datadog Node, Express
	"flask.request",       // Datadog Python, Flask
	"django.request",      // Datadog Python, Django
	"asgi.request",        // Datadog Python, ASGI
	"rack.request",        // Datadog Ruby
	"aspnet_core.request", // Datadog .NET
}

// datadogHTTPKeys are the utilisation keys answered from APM trace metrics.
var datadogHTTPKeys = map[UtilisationKey]bool{
	KeyHTTPThroughput:      true,
	KeyHTTPLatencyP95:      true,
	KeyHTTPLatencyP99:      true,
	KeyHTTPLatencySum:      true,
	KeyHTTPErrorRate:       true,
	KeyHTTPStatus:          true,
	KeyHTTPMaxResponseTime: true,
}

func requestsDatadogHTTPKeys(keys []string) bool {
	for _, k := range keys {
		if datadogHTTPKeys[UtilisationKey(k)] {
			return true
		}
	}
	return false
}

// probeDatadogAPMOperation finds the operation the workload's HTTP spans are recorded
// under, in one call: every candidate's hit count, comma-separated in a single query.
// Datadog answers only for the families that exist, each series named by __name__.
// Returns "" when none has hits (or the probe fails), which leaves the HTTP keys unbuilt
// and the table in its empty state — what it showed before, now for a real reason.
func probeDatadogAPMOperation(ctx *security.RequestContext, req GetUtilisationTrendRequest, provider, source string, meta RequestMetadata) string {
	if !datadogMetricTagValueSafe(meta.Name) {
		return ""
	}
	window := datadogRollupSeconds(meta.RangeWindow)
	parts := make([]string, 0, len(datadogAPMOperationCandidates))
	for _, op := range datadogAPMOperationCandidates {
		parts = append(parts, fmt.Sprintf("sum:trace.%s.hits{service:%s}.as_count().rollup(sum, %d)", op, meta.Name, window))
	}
	out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:            req.AccountId,
		MetricProvider:       provider,
		MetricProviderSource: source,
		Queries:              map[string]string{"apm_operation_probe": strings.Join(parts, ",")},
		StartTime:            req.StartTime,
		EndTime:              req.EndTime,
	})
	if err != nil {
		ctx.GetLogger().Warn("datadog: APM operation probe failed; HTTP metrics left empty",
			"account_id", req.AccountId, "service", meta.Name, "error", err)
		return ""
	}
	return pickDatadogAPMOperation(out)
}

// pickDatadogAPMOperation returns the candidate with the most hits in a probe result.
// A workload instrumented twice (an OTel collector beside a Datadog tracer) reports
// under both; the busier one is the one serving its traffic.
func pickDatadogAPMOperation(out OutputMetricQuery) string {
	hits := map[string]float64{}
	for _, r := range out.Results {
		for _, series := range r.Payload {
			op := strings.TrimSuffix(strings.TrimPrefix(series.Metric["__name__"], "trace."), ".hits")
			for _, v := range series.Values {
				hits[op] += v
			}
		}
	}
	best, bestHits := "", 0.0
	for _, op := range datadogAPMOperationCandidates {
		if hits[op] > bestHits {
			best, bestHits = op, hits[op]
		}
	}
	return best
}

// buildDatadogHTTPQuery renders one HTTP key for a workload. The table's keys return a
// single value per endpoint (rolled up over the whole window), because the table reads
// the first point of each series as that endpoint's number; the two chart keys stay
// time series. Returns "" when the operation or workload is unknown.
func buildDatadogHTTPQuery(key string, meta RequestMetadata) string {
	op, service := meta.APMOperation, meta.Name
	if op == "" || !datadogMetricTagValueSafe(service) {
		return ""
	}
	metric := "trace." + op
	scope := fmt.Sprintf("{service:%s}", service)
	w := datadogRollupSeconds(meta.RangeWindow)
	switch UtilisationKey(key) {
	case KeyHTTPThroughput:
		return fmt.Sprintf("sum:%s.hits%s by {resource_name}.as_count().rollup(sum, %d)", metric, scope, w)
	case KeyHTTPLatencyP95:
		return fmt.Sprintf("p95:%s%s by {resource_name}.rollup(%d)", metric, scope, w)
	case KeyHTTPLatencyP99:
		return fmt.Sprintf("p99:%s%s by {resource_name}.rollup(%d)", metric, scope, w)
	case KeyHTTPLatencySum:
		return fmt.Sprintf("sum:%s%s by {resource_name}.rollup(sum, %d)", metric, scope, w)
	case KeyHTTPErrorRate:
		// Datadog counts a span as an error when the tracer marks it failed — in
		// practice 5xx — so this reads lower than the Prometheus rate, which counts 4xx too.
		return fmt.Sprintf("(sum:%s.errors%s by {resource_name}.as_count().rollup(sum, %d) / sum:%s.hits%s by {resource_name}.as_count().rollup(sum, %d)) * 100",
			metric, scope, w, metric, scope, w)
	case KeyHTTPStatus:
		return fmt.Sprintf("sum:%s.hits.by_http_status%s by {http.status_code}.as_rate()", metric, scope)
	case KeyHTTPMaxResponseTime:
		return fmt.Sprintf("max:%s%s", metric, scope)
	}
	return ""
}

// datadogMetricTagValueSafe reports whether a value can be placed inside a Datadog
// metric scope ({service:<v>}) as-is. The workload name arrives in the request, and a
// "}" or "," in it would close the scope or add clauses to the query. There is no
// escape syntax for a metric scope (escapeDatadogTagValue is for log search), so this
// is an allowlist: Kubernetes names are [a-z0-9.-] and APM service names add "_" and
// capitals, so no real name is refused. Anything else leaves the HTTP keys unbuilt.
func datadogMetricTagValueSafe(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !isDatadogTagChar(r) {
			return false
		}
	}
	return true
}

func isDatadogTagChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.':
		return true
	}
	return false
}

// datadogRollupSeconds converts the "<N>s" window promAggWindow produces into the
// seconds Datadog's rollup() takes. Anything unparseable falls back to one hour, the
// window the Prometheus HTTP keys use.
func datadogRollupSeconds(window string) int {
	if n, err := strconv.Atoi(strings.TrimSuffix(window, "s")); err == nil && n > 0 {
		return n
	}
	return 3600
}

// datadogHTTPLabels renames Datadog's trace tags to the labels the Prometheus HTTP keys
// emit, which is what the latency table and the status chart read:
//
//   - resource_name "post_/rpc/metrics" -> method "POST", path "/rpc/metrics". Datadog
//     normalises the resource ("POST /rpc/metrics") to lower case with "_" for the
//     space, so the split is at the first "_/". A resource with no path ("get") is a
//     method alone.
//   - http.status_code -> status.
//
// Only HTTP keys are touched; a user's own query is not.
func datadogHTTPLabels(results []QueryResult) {
	for i := range results {
		if !datadogHTTPKeys[UtilisationKey(results[i].QueryKey)] {
			continue
		}
		for j := range results[i].Payload {
			m := results[i].Payload[j].Metric
			if m == nil {
				continue
			}
			if resource, ok := m["resource_name"]; ok {
				m["method"], m["path"] = splitDatadogResource(resource)
			}
			if code, ok := m["http.status_code"]; ok {
				m["status"] = code
			}
		}
	}
}

func splitDatadogResource(resource string) (method, path string) {
	if i := strings.Index(resource, "_/"); i >= 0 {
		return strings.ToUpper(resource[:i]), resource[i+1:]
	}
	if !strings.Contains(resource, "/") {
		return strings.ToUpper(resource), ""
	}
	return "", resource
}
