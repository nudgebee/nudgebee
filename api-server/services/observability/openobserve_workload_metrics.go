package observability

import (
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"strings"
	"time"
)

// openObserveWorkloadMetricCandidate is one (family, namespace-label, workload-label)
// convention a workload's CPU series may be published under.
//
// OpenObserve is a storage backend, not a metrics convention: what lands in it depends
// entirely on the collector writing to it. A single instance routinely carries several
// of these at once — the OTel kubeletstats receiver (k8s_pod_*), the OTel container
// stats receiver (container_*), the NudgeBee agent (container_resources_*) and plain
// cAdvisor all use different family names AND different namespace/pod label spellings.
// Picking one and hoping is how a workload with perfectly good CPU data renders an
// empty chart, which reads as "used no CPU" rather than "queried the wrong family".
//
// So the family is DISCOVERED, never assumed — mirroring the rule the Prometheus
// series-discovery path already states (see seriesMatchCandidate in prometheus_series.go).
// Prometheus discovers via a bare label selector on /label/__name__/values; OpenObserve
// rejects that ("match[] argument must start with a metric name"), so discovery here
// runs each candidate query and keeps the first that actually returns series.
type openObserveWorkloadMetricCandidate struct {
	Family         string
	NamespaceLabel string
	WorkloadLabel  string
	// Counter marks a monotonically increasing series, which must be rate()d to be
	// meaningful. The OTel "usage" gauges are already expressed in cores.
	Counter bool
}

// OpenObserveWorkloadCPUCandidates is the probe order for workload CPU. Package-level so
// tests can override it. Ordering decides only which family wins when several carry the
// same workload; every entry is tried until one returns series.
var OpenObserveWorkloadCPUCandidates = []openObserveWorkloadMetricCandidate{
	// OTel kubeletstats receiver — pod-level CPU already in cores.
	{Family: "k8s_pod_cpu_usage", NamespaceLabel: "k8s_namespace_name", WorkloadLabel: "k8s_pod_name"},
	// OTel container stats — same labels, container granularity.
	{Family: "container_cpu_usage", NamespaceLabel: "k8s_namespace_name", WorkloadLabel: "k8s_pod_name"},
	// NudgeBee agent — counter, and it publishes the plain namespace/pod spellings.
	{Family: "container_resources_cpu_usage_seconds_total", NamespaceLabel: "namespace", WorkloadLabel: "pod", Counter: true},
	// cAdvisor / kube-prometheus-stack.
	{Family: "container_cpu_usage_seconds_total", NamespaceLabel: "namespace", WorkloadLabel: "pod", Counter: true},
}

// openObserveWorkloadProbeLookback bounds the window a discovery probe looks at when the
// event carries no usable window. Short enough to stay cheap, long enough that a workload
// scraped at a 1m interval still has points.
const openObserveWorkloadProbeLookback = 15 * time.Minute

// buildOpenObserveWorkloadCPUQuery renders the PromQL for one candidate.
//
// The workload value is matched as a POD PREFIX rather than against a deployment label:
// pod names carry a replica-hash suffix, and the prefix form is the only one that works
// for every workload kind (deployment, statefulset, daemonset, job) — several of these
// families carry no k8s_deployment_name at all.
func buildOpenObserveWorkloadCPUQuery(c openObserveWorkloadMetricCandidate, workload, namespace string) string {
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
func (s *OpenObserveMetricSource) CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool {
	return ctx.GetEvent().SubjectName != "" && getEventNamespace(ctx.GetEvent()) != ""
}

// GenerateQuery discovers which CPU family actually carries this workload and returns the
// matching PromQL. Returning an error when nothing matches is deliberate: the caller then
// falls back to generateWorkloadMetricQueries, which surfaces an explicit "no workload
// metric queries available" rather than charting an empty series as though it were zero.
func (s *OpenObserveMetricSource) GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error) {
	workload := ctx.GetEvent().SubjectName
	namespace := getEventNamespace(ctx.GetEvent())
	if workload == "" || namespace == "" {
		return "", nil, fmt.Errorf("openobserve: workload name and namespace required to generate a metric query")
	}

	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	startMs, endMs := openObserveWorkloadProbeWindow(ctx)

	var attempted []string
	for _, candidate := range OpenObserveWorkloadCPUCandidates {
		promQL := buildOpenObserveWorkloadCPUQuery(candidate, workload, namespace)
		attempted = append(attempted, candidate.Family)

		result, err := s.FetchMetricsQuery(requestCtx, FetchMetricsRequest{
			AccountId:    ctx.GetAccountId(),
			Queries:      map[string]string{"cpu": promQL},
			StartTime:    startMs,
			EndTime:      endMs,
			StepInterval: 60,
		})
		if err != nil {
			// One family erroring says nothing about the others — a family absent from
			// this instance is the expected case, not a failure. Keep probing.
			ctx.GetLogger().Debug("openobserve: workload metric probe failed",
				"family", candidate.Family, "error", err)
			continue
		}
		if openObserveQueryHasSeries(result) {
			return promQL, map[string]any{}, nil
		}
	}

	return "", nil, fmt.Errorf(
		"openobserve: no CPU metric family carries workload %q in namespace %q (probed: %s)",
		workload, namespace, strings.Join(attempted, ", "))
}

// openObserveWorkloadProbeWindow picks the window to probe, preferring the event's own
// span so discovery reflects the period under investigation rather than "now" — a
// workload that has since scaled to zero must still resolve while its incident is open.
func openObserveWorkloadProbeWindow(ctx playbooks.PlaybookActionContext) (startMs, endMs int64) {
	if ended := ctx.GetEvent().EndedAt; ended != nil {
		endMs = ended.UnixMilli()
	} else {
		endMs = time.Now().UnixMilli()
	}
	if started := ctx.GetEvent().StartedAt; started != nil {
		startMs = started.Add(-openObserveWorkloadProbeLookback).UnixMilli()
	} else {
		startMs = endMs - openObserveWorkloadProbeLookback.Milliseconds()
	}
	if startMs >= endMs {
		startMs = endMs - openObserveWorkloadProbeLookback.Milliseconds()
	}
	return startMs, endMs
}

// openObserveQueryHasSeries reports whether a probe actually produced data points. A
// query for an absent family answers success-with-zero-series rather than an error, so
// an empty payload is the signal that this candidate is the wrong convention.
func openObserveQueryHasSeries(result OutputMetricQuery) bool {
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
