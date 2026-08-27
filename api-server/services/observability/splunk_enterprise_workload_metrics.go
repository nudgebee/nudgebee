package observability

import (
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strings"
	"time"
)

// This file implements PlaybookQueryGenerator for Splunk Enterprise.
//
// It is NOT optional the way it is for a PromQL-speaking backend. Every metric query the
// playbook layer generates is PromQL, and Splunk cannot execute PromQL at all — so
// without this, each metric evidence lookup on a Splunk-backed account would hand mstats
// a PromQL string and fail. The equivalent for OpenObserve (openobserve_workload_metrics.go)
// exists to pick the right metric family; here it also bridges the query language.

// splunkEnterpriseCPUNamePatterns are matched, in order, against the metric names the
// index actually reports. The first pattern with a matching metric that returns data
// wins.
//
// Discovery is by catalog lookup rather than a hardcoded candidate list because Splunk
// has a real metrics catalog (`| mcatalog values(metric_name)`), unlike OpenObserve where
// each candidate had to be probed blind. What lands in a metrics index still depends
// entirely on the collector writing to it: the OTel kubeletstats receiver, the Splunk
// OTel distribution and a hand-rolled HEC pipeline all use different names.
var splunkEnterpriseCPUNamePatterns = []string{
	"k8s.pod.cpu.usage",
	"k8s.pod.cpu.utilization",
	"container.cpu.usage",
	"container.cpu.utilization",
	"cpu.usage",
	"cpu",
}

// splunkEnterpriseWorkloadProbeLookback bounds a discovery probe when the event carries
// no usable window: long enough that a workload scraped once a minute still has points.
const splunkEnterpriseWorkloadProbeLookback = 15 * time.Minute

// splunkEnterpriseWorkloadSpanSeconds is the bucket width for a generated workload chart.
const splunkEnterpriseWorkloadSpanSeconds = 60

// CanGenerateQuery reports whether the event identifies a workload AND this account has
// a metrics index configured. Both are required: a Splunk holding only logs has no
// metrics to chart, and saying otherwise produces an empty panel that reads as "used no
// CPU" rather than "metrics are not configured here".
func (s *SplunkEnterpriseMetricSource) CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool {
	if ctx.GetEvent().SubjectName == "" || getEventNamespace(ctx.GetEvent()) == "" {
		return false
	}
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	cfg, err := integrations.GetSplunkEnterpriseConfig(requestCtx, ctx.GetAccountId())
	if err != nil {
		return false
	}
	return cfg.MetricIndex != ""
}

// GenerateQuery discovers which CPU metric actually carries this workload and returns the
// matching mstats search.
//
// Returning an error when nothing matches is deliberate: the caller then falls back to
// its own query generation, which surfaces an explicit "no workload metric queries
// available" rather than charting an empty series as though it were a measured zero.
func (s *SplunkEnterpriseMetricSource) GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error) {
	workload := ctx.GetEvent().SubjectName
	namespace := getEventNamespace(ctx.GetEvent())
	if workload == "" || namespace == "" {
		return "", nil, fmt.Errorf("splunk enterprise: workload name and namespace required to generate a metric query")
	}

	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	cfg, index, err := splunkEnterpriseMetricContext(requestCtx, ctx.GetAccountId())
	if err != nil {
		return "", nil, err
	}

	startMs, endMs := splunkEnterpriseWorkloadProbeWindow(ctx)

	names, err := s.FetchMetricList(requestCtx, FetchMetricsListRequest{
		AccountId: ctx.GetAccountId(),
		StartTime: startMs,
		EndTime:   endMs,
	})
	if err != nil {
		return "", nil, fmt.Errorf("splunk enterprise: failed to list metrics in index %q: %w", index, err)
	}

	candidates := rankSplunkCPUMetricNames(names)
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf(
			"splunk enterprise: no CPU-like metric found in index %q (metrics present: %d)", index, len(names))
	}

	var attempted []string
	for _, metric := range candidates {
		spl, buildErr := buildSplunkWorkloadCPUQuery(metric, workload, namespace, index)
		if buildErr != nil {
			continue
		}
		attempted = append(attempted, metric)

		if vErr := validateSplunkEnterpriseMetricQuery(spl, index); vErr != nil {
			continue
		}
		startTime, endTime := splunkEnterpriseTimeRangeSeconds(startMs, endMs, time.Now())
		rows, runErr := execSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
			splunkEnterpriseMetricMaxRows, splunkEnterpriseMetricSearchTimeout)
		if runErr != nil {
			// One metric erroring says nothing about the others. Keep probing.
			ctx.GetLogger().Debug("splunk enterprise: workload metric probe failed",
				"metric", metric, "error", runErr)
			continue
		}
		if len(convertSplunkMetricRows("probe", spl, rows).Payload) > 0 {
			return spl, map[string]any{}, nil
		}
	}

	return "", nil, fmt.Errorf(
		"splunk enterprise: no CPU metric carries workload %q in namespace %q (probed: %s)",
		workload, namespace, strings.Join(attempted, ", "))
}

// rankSplunkCPUMetricNames orders the metrics present in the index by how well they match
// the known CPU spellings, most specific first. Metrics that look nothing like CPU are
// dropped rather than probed, so a large index does not turn discovery into a scan.
func rankSplunkCPUMetricNames(metrics []OutputMetrics) []string {
	var ranked []string
	seen := map[string]bool{}
	for _, pattern := range splunkEnterpriseCPUNamePatterns {
		for _, m := range metrics {
			name := m.Metric
			if seen[name] {
				continue
			}
			if strings.EqualFold(name, pattern) {
				seen[name] = true
				ranked = append(ranked, name)
			}
		}
	}
	// Second pass: substring matches, for spellings the exact list does not carry.
	for _, pattern := range splunkEnterpriseCPUNamePatterns {
		for _, m := range metrics {
			name := m.Metric
			if seen[name] {
				continue
			}
			if strings.Contains(strings.ToLower(name), strings.ToLower(pattern)) {
				seen[name] = true
				ranked = append(ranked, name)
			}
		}
	}
	return ranked
}

// buildSplunkWorkloadCPUQuery renders the mstats search for one workload.
//
// The workload is matched as a POD NAME PREFIX rather than against a deployment
// dimension: pod names carry a replica-hash suffix, and the prefix form is the only one
// that works for every workload kind (deployment, statefulset, daemonset, job). Several
// collectors publish no k8s.deployment.name dimension at all.
func buildSplunkWorkloadCPUQuery(metric, workload, namespace, index string) (string, error) {
	item := QueryItem{
		Metric: metric,
		LabelMatchers: []LabelMatcher{
			{Label: "k8s.namespace.name", Operator: "_eq", Value: namespace},
			{Label: "k8s.pod.name", Operator: "_prefix", Value: workload + "-"},
		},
		AggregateOperator: "avg",
	}
	return buildSplunkMStatsQuery(item, nil, index, splunkEnterpriseWorkloadSpanSeconds, false)
}

// splunkEnterpriseWorkloadProbeWindow prefers the event's own span so discovery reflects
// the period under investigation rather than "now" — a workload that has since scaled to
// zero must still resolve while its incident is open.
func splunkEnterpriseWorkloadProbeWindow(ctx playbooks.PlaybookActionContext) (startMs, endMs int64) {
	if ended := ctx.GetEvent().EndedAt; ended != nil {
		endMs = ended.UnixMilli()
	} else {
		endMs = time.Now().UnixMilli()
	}
	if started := ctx.GetEvent().StartedAt; started != nil {
		startMs = started.Add(-splunkEnterpriseWorkloadProbeLookback).UnixMilli()
	} else {
		startMs = endMs - splunkEnterpriseWorkloadProbeLookback.Milliseconds()
	}
	if startMs >= endMs {
		startMs = endMs - splunkEnterpriseWorkloadProbeLookback.Milliseconds()
	}
	return startMs, endMs
}
