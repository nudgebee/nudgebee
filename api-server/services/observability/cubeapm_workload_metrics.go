package observability

import (
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"strings"
	"time"
)

// cubeAPMWorkloadMetricCandidate is one (family, namespace-label, workload-label)
// convention a workload's CPU series may be published under.
//
// CubeAPM is a storage backend, not a metrics convention: what lands in it depends
// on the collector writing to it. A single install routinely carries several of
// these at once — CubeAPM's own Kubernetes infra chart and kube-prometheus-stack
// both emit the cAdvisor names, the OTel kubeletstats receiver emits k8s_pod_*
// with underscored resource attributes, and the NudgeBee agent emits its own.
// Picking one and hoping is how a workload with perfectly good CPU data renders an
// empty chart, which reads as "used no CPU" rather than "queried the wrong family".
//
// So the family is DISCOVERED, never assumed — the same rule prometheus_series.go
// states for series-match discovery.
type cubeAPMWorkloadMetricCandidate struct {
	Family         string
	NamespaceLabel string
	WorkloadLabel  string
	// Counter marks a monotonically increasing series, which must be rate()d to
	// mean anything. The OTel "usage" gauges are already expressed in cores.
	Counter bool
}

// CubeAPMWorkloadCPUCandidates is the probe order for workload CPU. Package-level
// so tests can override it. Ordering decides only which family wins when several
// carry the same workload; every entry is tried until one returns series.
var CubeAPMWorkloadCPUCandidates = []cubeAPMWorkloadMetricCandidate{
	// cAdvisor / kube-prometheus-stack, which CubeAPM's own Kubernetes infra chart
	// scrapes — the most likely source on a CubeAPM-monitored cluster.
	{Family: "container_cpu_usage_seconds_total", NamespaceLabel: "namespace", WorkloadLabel: "pod", Counter: true},
	// OTel kubeletstats receiver — pod-level CPU already in cores. Resource
	// attributes lose their dots on the way into a Prometheus label space.
	{Family: "k8s_pod_cpu_usage", NamespaceLabel: "k8s_namespace_name", WorkloadLabel: "k8s_pod_name"},
	// OTel container stats — same labels, container granularity.
	{Family: "container_cpu_usage", NamespaceLabel: "k8s_namespace_name", WorkloadLabel: "k8s_pod_name"},
	// NudgeBee agent, when it writes into CubeAPM alongside the cluster's own collector.
	{Family: "container_resources_cpu_usage_seconds_total", NamespaceLabel: "namespace", WorkloadLabel: "pod", Counter: true},
}

// cubeAPMWorkloadProbeLookback bounds the window a discovery probe looks at when
// the event carries no usable window. Short enough to stay cheap, long enough that
// a workload scraped at a 1m interval still has points.
const cubeAPMWorkloadProbeLookback = 15 * time.Minute

// buildCubeAPMWorkloadCPUQuery renders the PromQL for one candidate.
//
// The workload is matched as a POD PREFIX rather than against a deployment label:
// pod names carry a replica-hash suffix, and the prefix form is the only one that
// works for every workload kind (deployment, statefulset, daemonset, job) — several
// of these families carry no deployment label at all.
func buildCubeAPMWorkloadCPUQuery(c cubeAPMWorkloadMetricCandidate, workload, namespace string) string {
	matcher := fmt.Sprintf(`%s="%s",%s=~"%s-.*"`,
		c.NamespaceLabel, escapePromQLLabelValue(namespace),
		c.WorkloadLabel, escapePromQLLabelValue(workload))

	if c.Counter {
		return fmt.Sprintf("sum(rate(%s{%s}[5m])) by (%s)", c.Family, matcher, c.WorkloadLabel)
	}
	return fmt.Sprintf("sum(%s{%s}) by (%s)", c.Family, matcher, c.WorkloadLabel)
}

// CanGenerateQuery reports whether the event carries enough to identify a workload.
// The family still has to be discovered in GenerateQuery, which may find none.
func (s *CubeAPMMetricSource) CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool {
	return ctx.GetEvent().SubjectName != "" && getEventNamespace(ctx.GetEvent()) != ""
}

// GenerateQuery discovers which CPU family actually carries this workload and
// returns the matching PromQL. Returning an error when nothing matches is
// deliberate: the caller then falls back to generateWorkloadMetricQueries, which
// surfaces an explicit "no workload metric queries available" rather than charting
// an empty series as though it were zero.
func (s *CubeAPMMetricSource) GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error) {
	workload := ctx.GetEvent().SubjectName
	namespace := getEventNamespace(ctx.GetEvent())
	if workload == "" || namespace == "" {
		return "", nil, fmt.Errorf("cubeapm: workload name and namespace required to generate a metric query")
	}
	// The context below is synthesized with tenant-admin rights, so an empty
	// tenant would build an admin context scoped to nothing and surface later as
	// an opaque database error rather than as the missing input it is.
	if ctx.GetTenantId() == "" {
		return "", nil, fmt.Errorf("cubeapm: tenant id is required to generate a metric query")
	}

	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	startMs, endMs := cubeAPMWorkloadProbeWindow(ctx)

	var attempted []string
	for _, candidate := range CubeAPMWorkloadCPUCandidates {
		promQL := buildCubeAPMWorkloadCPUQuery(candidate, workload, namespace)
		attempted = append(attempted, candidate.Family)

		result, err := s.FetchMetricsQuery(requestCtx, FetchMetricsRequest{
			AccountId:    ctx.GetAccountId(),
			Queries:      map[string]string{"cpu": promQL},
			StartTime:    startMs,
			EndTime:      endMs,
			StepInterval: 60,
		})
		if err != nil {
			// One family erroring says nothing about the others — a family absent
			// from this install is the expected case, not a failure. Keep probing.
			ctx.GetLogger().Debug("cubeapm: workload metric probe failed",
				"family", candidate.Family, "error", err)
			continue
		}
		if cubeAPMQueryHasSeries(result) {
			return promQL, map[string]any{}, nil
		}
	}

	return "", nil, fmt.Errorf(
		"cubeapm: no CPU metric family carries workload %q in namespace %q (probed: %s)",
		workload, namespace, strings.Join(attempted, ", "))
}

// cubeAPMWorkloadProbeWindow picks the window to probe, preferring the event's own
// span so discovery reflects the period under investigation rather than "now" — a
// workload that has since scaled to zero must still resolve while its incident is open.
func cubeAPMWorkloadProbeWindow(ctx playbooks.PlaybookActionContext) (startMs, endMs int64) {
	if ended := ctx.GetEvent().EndedAt; ended != nil {
		endMs = ended.UnixMilli()
	} else {
		endMs = time.Now().UnixMilli()
	}
	if started := ctx.GetEvent().StartedAt; started != nil {
		startMs = started.Add(-cubeAPMWorkloadProbeLookback).UnixMilli()
	} else {
		startMs = endMs - cubeAPMWorkloadProbeLookback.Milliseconds()
	}
	if startMs >= endMs {
		startMs = endMs - cubeAPMWorkloadProbeLookback.Milliseconds()
	}
	return startMs, endMs
}

// cubeAPMQueryHasSeries reports whether a probe actually produced data points. A
// query for an absent family answers success-with-zero-series rather than an error,
// so an empty payload is the signal that this candidate is the wrong convention.
func cubeAPMQueryHasSeries(result OutputMetricQuery) bool {
	for _, r := range result.Results {
		if r.Error != nil {
			continue
		}
		if len(r.Payload) > 0 {
			return true
		}
	}
	return false
}
