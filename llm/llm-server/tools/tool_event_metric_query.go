package tools

import (
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

// maxMetricQueryBytes bounds the query text carried on a metric evidence card.
// Stored alert expressions run past 1 KB; the head names the metric and the
// aggregation, which is what a reader needs to interpret the numbers.
const maxMetricQueryBytes = 300

// metricCardQuery says what a metric evidence card's series measure, from the
// card's metadata.query: the query it ran and, only when that query settles it,
// the unit of its values.
func metricCardQuery(metadata map[string]any) (query, unit string) {
	switch q := metadata["query"].(type) {
	case string:
		query = compactQuery(q)
		if query == "" {
			return "", ""
		}
		return query, promQLUnit(q)
	case map[string]any:
		// Cards that ran a provider-neutral metric key record the request, not the
		// query. The key names what was measured; the request alone does not say
		// which layer answered it, so the unit is left to a caller that knows the
		// card (see utilisationUnit).
		var parts []string
		if metrics, ok := q["metrics"].([]any); ok {
			var names []string
			for _, m := range metrics {
				if s, ok := m.(string); ok && s != "" {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				parts = append(parts, "metrics="+strings.Join(names, ","))
			}
		}
		if rt, ok := q["resource_type"].(string); ok && rt != "" {
			parts = append(parts, "resource_type="+rt)
		}
		return strings.Join(parts, " "), ""
	}
	return "", ""
}

// utilisationKeyUnits are the units the api-server's utilisation layer
// (observability.FetchMetricUtilisation) returns for these keys on every backend it
// serves. Keys not listed get no unit.
//
//   - cpu_usage → cores. Native on buildPrometheusWorkloadQueries (rate of
//     container_cpu_usage_seconds_total; also used for OpenObserve),
//     buildNewRelicWorkloadQueries (cpuUsedCores), buildDynatraceWorkloadQueries
//     (cAdvisor cpu-seconds irate) and buildSolarWindsWorkloadQueries
//     (k8s.pod.cpu.usage.seconds.rate). Converted from nanocores by
//     datadogNanocoresToCores (Datadog) and by the nanocoresToCores scale in
//     esWorkloadCPUSources (Elasticsearch).
//   - memory_usage → bytes. Working-set bytes on every builder:
//     container_memory_working_set_bytes (Prometheus, Dynatrace),
//     kubernetes.memory.working_set (Datadog), memoryWorkingSetBytes (New Relic),
//     k8s.pod.memory.working_set (SolarWinds), esWorkloadMemSources (Elasticsearch,
//     unscaled).
var utilisationKeyUnits = map[string]string{"cpu_usage": "cores", "memory_usage": "bytes"}

// utilisationUnit is the unit of a card that ran utilisation keys, when every key it
// ran has the same known unit: the pod_metric card (pod_metric_enricher reads
// cpu_usage or memory_usage by resource_type, see podMetricUsageKey) and the
// `metrics` action's workload card (its request lists the keys).
func utilisationUnit(metadata map[string]any) string {
	q, _ := metadata["query"].(map[string]any)
	var keys []string
	if rt, ok := q["resource_type"].(string); ok {
		switch {
		case strings.EqualFold(rt, "cpu"):
			keys = append(keys, "cpu_usage")
		case strings.EqualFold(rt, "memory"):
			keys = append(keys, "memory_usage")
		default:
			return ""
		}
	}
	if metrics, ok := q["metrics"].([]any); ok {
		for _, m := range metrics {
			s, _ := m.(string)
			keys = append(keys, s)
		}
	}
	unit := ""
	for i, k := range keys {
		u := utilisationKeyUnits[k]
		if u == "" || (i > 0 && u != unit) {
			return ""
		}
		unit = u
	}
	return unit
}

// compactQuery puts a query on one line and bounds it. Comment-only lines are
// dropped first: joined onto one line, a `#` would comment out the rest.
func compactQuery(q string) string {
	var kept []string
	for _, line := range strings.Split(q, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	out := strings.Join(strings.Fields(strings.Join(kept, " ")), " ")
	if len(out) > maxMetricQueryBytes {
		out = truncateAtRuneBoundary(out, maxMetricQueryBytes) + "…"
	}
	return out
}

// promQLUnit returns the unit of a PromQL query's values when the query reads a
// single metric through steps that keep its unit, and the metric's name follows
// a Prometheus naming convention that fixes the unit. Anything else — ratios,
// arithmetic, counts, non-PromQL — returns "": no unit is better than a wrong one.
func promQLUnit(query string) string {
	// __CLUSTER__ is a placeholder the metrics layer expands; it is not PromQL.
	expr, err := promQLParser.ParseExpr(strings.ReplaceAll(query, "__CLUSTER__", ""))
	if err != nil {
		return ""
	}
	name, perSecond, ok := unitSource(expr)
	if !ok {
		return ""
	}
	switch {
	case perSecond && (strings.HasSuffix(name, "cpu_usage_seconds_total") || strings.HasSuffix(name, "cpu_seconds_total")):
		return "cores"
	case perSecond && strings.HasSuffix(name, "_bytes_total"):
		return "bytes/s"
	case perSecond && strings.HasSuffix(name, "_total") && !hasUnitSuffix(strings.TrimSuffix(name, "_total")):
		// A plain count (requests, errors) per second. A counter of some unit is that
		// unit per second (seconds per second is a utilisation), so it gets none.
		return "per second"
	case !perSecond && strings.HasSuffix(name, "_bytes"):
		return "bytes"
	}
	return ""
}

// metricUnitSuffixes are the unit suffixes of the Prometheus naming convention
// (base units plus the scaled forms seen in practice).
var metricUnitSuffixes = []string{
	"_seconds", "_milliseconds", "_microseconds", "_nanoseconds", "_bytes", "_bits",
	"_joules", "_grams", "_meters", "_volts", "_amperes", "_celsius", "_ratio", "_percent",
}

func hasUnitSuffix(name string) bool {
	for _, suffix := range metricUnitSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// unitSource walks a query down to the one metric it reads, through steps that
// keep the metric's unit, and reports whether rate()/irate() made it per second.
func unitSource(expr parser.Expr) (name string, perSecond, ok bool) {
	switch e := expr.(type) {
	case *parser.VectorSelector:
		return e.Name, false, e.Name != ""
	case *parser.ParenExpr:
		return unitSource(e.Expr)
	case *parser.AggregateExpr:
		switch e.Op {
		case parser.SUM, parser.AVG, parser.MIN, parser.MAX, parser.TOPK, parser.BOTTOMK:
			return unitSource(e.Expr)
		}
	case *parser.BinaryExpr:
		// `x > 0.9` drops series but keeps the values of x.
		if _, scalar := e.RHS.(*parser.NumberLiteral); scalar && e.Op.IsComparisonOperator() && !e.ReturnBool {
			return unitSource(e.LHS)
		}
	case *parser.Call:
		if len(e.Args) != 1 {
			return "", false, false
		}
		ms, isRange := e.Args[0].(*parser.MatrixSelector)
		if !isRange {
			return "", false, false
		}
		vs, isSelector := ms.VectorSelector.(*parser.VectorSelector)
		if !isSelector || vs.Name == "" {
			return "", false, false
		}
		switch e.Func.Name {
		case "rate", "irate":
			return vs.Name, true, true
		case "avg_over_time", "min_over_time", "max_over_time", "last_over_time":
			return vs.Name, false, true
		}
	}
	return "", false, false
}
