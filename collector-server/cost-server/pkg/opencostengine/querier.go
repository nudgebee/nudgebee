package opencostengine

import (
	"fmt"
	"time"

	"github.com/opencost/opencost/core/pkg/source"
	"github.com/opencost/opencost/core/pkg/util/timeutil"
	"github.com/opencost/opencost/modules/prometheus-source/pkg/prom"
)

// Kube-state-metrics queries that replace OpenCost's exporter-specific metrics
// (container_cpu_allocation / container_memory_allocation_bytes), so cost can be
// computed centrally without deploying an OpenCost exporter in each cluster.
// Allocation = requests, with usage as a fallback (`or` fills usage-based values
// only for containers that have no explicit resource request).
// %s slots: (1) extra label filter inside {...}, (2) range duration, (3) extra
// `by` label. The filter and by-label are injected with a leading ", " only when
// non-empty (see clusterArgs) so an empty cluster filter/label can't produce a
// trailing comma and invalid PromQL.
const (
	queryFmtCPUAllocated = `avg(avg_over_time(kube_pod_container_resource_requests{resource="cpu", unit="core", container!="", container!="POD", node!=""%s}[%s])) by (container, pod, namespace, node%s)` +
		` or ` +
		`avg(rate(container_cpu_usage_seconds_total{container!="", container!="POD", node!=""%s}[%s])) by (container, pod, namespace, node%s)`
	queryFmtRAMAllocated = `avg(avg_over_time(kube_pod_container_resource_requests{resource="memory", unit="byte", container!="", container!="POD", node!=""%s}[%s])) by (container, pod, namespace, node%s)` +
		` or ` +
		`avg(avg_over_time(container_memory_working_set_bytes{container!="", container!="POD", node!=""%s}[%s])) by (container, pod, namespace, node%s)`
)

// nudgebeeQuerier wraps the upstream Prometheus MetricsQuerier and overrides only
// the two "allocated" queries (which otherwise rely on the in-cluster OpenCost
// exporter's metrics). The other ~73 MetricsQuerier methods delegate to the
// embedded upstream querier unchanged.
type nudgebeeQuerier struct {
	source.MetricsQuerier // embedded upstream querier (delegated methods)
	contexts              *prom.ContextFactory
	cfg                   *prom.OpenCostPrometheusConfig
}

func newNudgebeeQuerier(pds *prom.PrometheusDataSource) *nudgebeeQuerier {
	return &nudgebeeQuerier{
		MetricsQuerier: pds.Metrics(),
		contexts:       pds.PrometheusContexts(),
		cfg:            pds.PrometheusConfig(),
	}
}

// clusterArgs returns the optional label-filter and `by`-clause fragments, each
// already prefixed with ", " when non-empty (and "" otherwise) so they slot into
// the query format strings without ever producing a trailing comma.
func (q *nudgebeeQuerier) clusterArgs() (filter, byLabel string) {
	if q.cfg.ClusterFilter != "" {
		filter = ", " + q.cfg.ClusterFilter
	}
	if q.cfg.ClusterLabel != "" {
		byLabel = ", " + q.cfg.ClusterLabel
	}
	return filter, byLabel
}

func (q *nudgebeeQuerier) QueryRAMBytesAllocated(start, end time.Time) *source.Future[source.RAMBytesAllocatedResult] {
	dur := timeutil.DurationString(end.Sub(start))
	filter, byLabel := q.clusterArgs()
	query := fmt.Sprintf(queryFmtRAMAllocated, filter, dur, byLabel, filter, dur, byLabel)
	ctx := q.contexts.NewNamedContext(prom.AllocationContextName)
	return source.NewFuture(source.DecodeRAMBytesAllocatedResult, ctx.QueryAtTime(query, end))
}

func (q *nudgebeeQuerier) QueryCPUCoresAllocated(start, end time.Time) *source.Future[source.CPUCoresAllocatedResult] {
	dur := timeutil.DurationString(end.Sub(start))
	filter, byLabel := q.clusterArgs()
	query := fmt.Sprintf(queryFmtCPUAllocated, filter, dur, byLabel, filter, dur, byLabel)
	ctx := q.contexts.NewNamedContext(prom.AllocationContextName)
	return source.NewFuture(source.DecodeCPUCoresAllocatedResult, ctx.QueryAtTime(query, end))
}
