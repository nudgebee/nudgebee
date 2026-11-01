package observability

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"nudgebee/services/security"
)

// SigNoz utilisation queries.
//
// SigNoz's Kubernetes collector (k8s-infra: kubeletstats + k8s_cluster receivers)
// publishes OTel metric names, not cAdvisor/kube-state-metrics ones, so none of the
// Prometheus builders apply. The usage metrics are gauges already in cores and bytes
// (container.cpu.usage, container.memory.working_set), so nothing is rate()d; requests
// and limits come per container from the k8s_cluster receiver
// (k8s.container.cpu_request, …) in the same units. Names are written quoted, the only
// way PromQL can address a dotted name.
//
// Keys not handled here are answered with the standard "not available" note by
// completeUtilisationResults, rather than charted from a family SigNoz does not have.

const (
	signozContainerCPU    = "container.cpu.usage"
	signozContainerMemory = "container.memory.working_set"
	signozNodeCPU         = "k8s.node.cpu.usage"
	signozNodeMemory      = "k8s.node.memory.working_set"

	signozLabelNamespace = `"k8s.namespace.name"`
	signozLabelPod       = `"k8s.pod.name"`
	signozLabelContainer = `"k8s.container.name"`
	signozLabelNode      = `"k8s.node.name"`
)

// signozSelector renders {"<metric>", matchers...}.
func signozSelector(metric string, matchers ...string) string {
	parts := []string{fmt.Sprintf("%q", metric)}
	for _, m := range matchers {
		if m != "" {
			parts = append(parts, m)
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// signozWorkloadMatchers scopes a workload the way the Prometheus builder does: an exact
// pod list when the caller resolved one (an empty list matches nothing), the pod name for
// a pod, otherwise the workload's pod-name prefix.
func signozWorkloadMatchers(meta RequestMetadata) []string {
	if meta.Namespace == "" {
		return nil
	}
	matchers := []string{fmt.Sprintf(`%s="%s"`, signozLabelNamespace, escapePromQLString(meta.Namespace))}
	switch {
	case meta.PodNamesSupplied && len(meta.PodNames) == 0:
		matchers = append(matchers, signozLabelPod+`="__no_pods_resolved__"`)
	case meta.PodNamesSupplied:
		matchers = append(matchers, fmt.Sprintf(`%s=~"%s"`, signozLabelPod, promPodNameAlternation(meta.PodNames)))
	case meta.Name != "" && meta.Kind == "pod":
		matchers = append(matchers, fmt.Sprintf(`%s="%s"`, signozLabelPod, escapePromQLString(meta.Name)))
	// The collector puts the owning controller's name on every pod series, so a
	// workload is matched exactly; the pod-name prefix would also catch "api-gateway"
	// for workload "api".
	case meta.Name != "" && signozControllerLabels[meta.Kind]:
		matchers = append(matchers, fmt.Sprintf(`"k8s.%s.name"="%s"`, meta.Kind, escapePromQLString(meta.Name)))
	case meta.Name != "":
		matchers = append(matchers, fmt.Sprintf(`%s=~"%s-.*"`, signozLabelPod, escapePromQLString(meta.Name)))
	}
	if meta.ContainerName != "" {
		matchers = append(matchers, fmt.Sprintf(`%s="%s"`, signozLabelContainer, escapePromQLString(meta.ContainerName)))
	}
	return matchers
}

// signozControllerLabels are the workload kinds whose name SigNoz's collector puts on
// pod series as k8s.<kind>.name (its k8sattributes extract list).
var signozControllerLabels = map[string]bool{
	"deployment": true, "statefulset": true, "daemonset": true, "cronjob": true, "job": true,
}

// signozReplicaMetrics names the desired/ready pair per controller kind.
var signozReplicaMetrics = map[string][2]string{
	"deployment":  {"k8s.deployment.desired", "k8s.deployment.available"},
	"statefulset": {"k8s.statefulset.desired_pods", "k8s.statefulset.ready_pods"},
	"daemonset":   {"k8s.daemonset.desired_scheduled_nodes", "k8s.daemonset.ready_nodes"},
	"replicaset":  {"k8s.replicaset.desired", "k8s.replicaset.available"},
}

// buildSignozUtilisationQueries renders the keys SigNoz can answer for a workload,
// namespace, node or the whole cluster.
func buildSignozUtilisationQueries(meta RequestMetadata, metrics []string) map[string]string {
	if meta.Kind == "node" {
		return buildSignozNodeQueries(meta, metrics)
	}
	scope := signozWorkloadMatchers(meta)
	usage := func(metric string) string { return signozSelector(metric, scope...) }
	byContainer := fmt.Sprintf("by (%s, %s, %s)", signozLabelNamespace, signozLabelPod, signozLabelContainer)
	byPod := fmt.Sprintf("by (%s, %s)", signozLabelNamespace, signozLabelPod)
	// One value per container, then summed: a container's request is one gauge per
	// pod, so max collapses duplicate series without double counting.
	specTotal := func(metric string) string { return fmt.Sprintf("sum(max %s (%s))", byContainer, usage(metric)) }

	queries := map[string]string{}
	for _, m := range metrics {
		switch UtilisationKey(m) {
		case KeyCPUUsage:
			queries[m] = fmt.Sprintf("sum(%s)", usage(signozContainerCPU))
		case KeyMemoryUsage:
			queries[m] = fmt.Sprintf("sum(%s)", usage(signozContainerMemory))
		case KeyCPUUsageByPod:
			queries[m] = fmt.Sprintf("sum %s (%s)", byPod, usage(signozContainerCPU))
		case KeyMemoryUsageByPod:
			queries[m] = fmt.Sprintf("sum %s (%s)", byPod, usage(signozContainerMemory))
		case KeyCPUUsageMaxPod:
			queries[m] = fmt.Sprintf("max(sum by (%s) (%s))", signozLabelPod, usage(signozContainerCPU))
		case KeyMemoryUsageMaxPod:
			queries[m] = fmt.Sprintf("max(sum by (%s) (%s))", signozLabelPod, usage(signozContainerMemory))
		case KeyPodCount:
			queries[m] = fmt.Sprintf("count(count by (%s) (%s))", signozLabelPod, usage(signozContainerMemory))
		case KeyContainerCPUUsageByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, usage(signozContainerCPU))
		case KeyContainerMemoryWorkingSetByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, usage(signozContainerMemory))
		case KeyContainerCPURequestByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, usage("k8s.container.cpu_request"))
		case KeyContainerCPULimitByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, usage("k8s.container.cpu_limit"))
		case KeyContainerMemoryRequestByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, usage("k8s.container.memory_request"))
		case KeyContainerMemoryLimitByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, usage("k8s.container.memory_limit"))
		case KeyCPURequest:
			queries[m] = specTotal("k8s.container.cpu_request")
		case KeyCPULimit:
			queries[m] = specTotal("k8s.container.cpu_limit")
		case KeyMemoryRequest:
			queries[m] = specTotal("k8s.container.memory_request")
		case KeyMemoryLimit:
			queries[m] = specTotal("k8s.container.memory_limit")
		case KeyReplicaDefined, KeyReplicaReady:
			pair, ok := signozReplicaMetrics[meta.Kind]
			if !ok || meta.Namespace == "" || meta.Name == "" {
				continue
			}
			metric := pair[0]
			if UtilisationKey(m) == KeyReplicaReady {
				metric = pair[1]
			}
			ownerLabel := fmt.Sprintf(`"k8s.%s.name"`, meta.Kind)
			queries[m] = fmt.Sprintf("max(%s)", signozSelector(metric,
				fmt.Sprintf(`%s="%s"`, signozLabelNamespace, escapePromQLString(meta.Namespace)),
				fmt.Sprintf(`%s="%s"`, ownerLabel, escapePromQLString(meta.Name))))
		// Cluster-wide: no namespace in scope.
		// The window's average, as Prometheus's rate over the whole range is.
		case KeyCPUReal:
			if meta.Namespace == "" && meta.RangeWindow != "" && meta.Step != "" {
				queries[m] = fmt.Sprintf("avg_over_time(sum(%s)[%s:%s])", signozSelector(signozNodeCPU), meta.RangeWindow, meta.Step)
			}
		case KeyCPUUsageTrend:
			if meta.Namespace == "" {
				queries[m] = fmt.Sprintf("sum(%s)", signozSelector(signozNodeCPU))
			}
		case KeyMemReal, KeyMemUsageTrend:
			if meta.Namespace == "" {
				queries[m] = fmt.Sprintf("sum(%s)", signozSelector(signozNodeMemory))
			}
		case KeyCPUTotal:
			if meta.Namespace == "" {
				queries[m] = fmt.Sprintf("sum(%s)", signozSelector("k8s.node.allocatable_cpu"))
			}
		case KeyMemTotal:
			if meta.Namespace == "" {
				queries[m] = fmt.Sprintf("sum(%s)", signozSelector("k8s.node.allocatable_memory"))
			}
		// CPU percentiles and both peaks are over the request's window, like the
		// Prometheus keys of the same name (self-windowed, so no aggregate is re-applied).
		case KeyP90CPU, KeyP50CPU, KeyMaxUsageCPU, KeyMaxUsageMem:
			if meta.Namespace == "" && meta.RangeWindow != "" && meta.Step != "" {
				queries[m] = signozClusterOverTime(UtilisationKey(m), meta.RangeWindow, meta.Step)
			}
		// Memory percentiles are ACROSS nodes at each instant, as on Prometheus.
		case KeyP90Mem, KeyP50Mem:
			if meta.Namespace == "" {
				q := "0.9"
				if UtilisationKey(m) == KeyP50Mem {
					q = "0.5"
				}
				queries[m] = fmt.Sprintf("quantile(%s, sum by (%s) (%s))", q, signozLabelNode, signozSelector(signozNodeMemory))
			}
		}
	}
	promApplyAggregate(queries, meta.Aggregate, meta.RangeWindow, meta.Step)
	return queries
}

// signozClusterOverTime renders a cluster CPU percentile or peak key over [window:step].
func signozClusterOverTime(key UtilisationKey, window, step string) string {
	metric := signozNodeCPU
	if key == KeyMaxUsageMem {
		metric = signozNodeMemory
	}
	sub := fmt.Sprintf("sum(%s)[%s:%s]", signozSelector(metric), window, step)
	switch key {
	case KeyP90CPU:
		return fmt.Sprintf("quantile_over_time(0.9, %s)", sub)
	case KeyP50CPU:
		return fmt.Sprintf("quantile_over_time(0.5, %s)", sub)
	default:
		return fmt.Sprintf("max_over_time(%s)", sub)
	}
}

// signozK8sInfraProbes remembers, per account, the last probe's answer, so a page of
// charts probes once rather than per chart. A positive answer is kept for
// signozK8sInfraFoundTTL; a negative one only for signozK8sInfraMissingTTL, so a
// collector installed a minute ago shows up almost at once.
var signozK8sInfraProbes sync.Map // accountId -> signozK8sInfraProbe

type signozK8sInfraProbe struct {
	found     bool
	checkedAt time.Time
}

const (
	signozK8sInfraFoundTTL   = 10 * time.Minute
	signozK8sInfraMissingTTL = time.Minute
)

// signozHasK8sInfraMetrics reports whether SigNoz holds the Kubernetes collector's
// metrics. A failed probe answers true and is not remembered: the queries that follow
// will report their own error, which says more than a guess that the collector is missing.
func signozHasK8sInfraMetrics(ctx *security.RequestContext, accountId string) bool {
	if v, ok := signozK8sInfraProbes.Load(accountId); ok {
		probe := v.(signozK8sInfraProbe)
		ttl := signozK8sInfraMissingTTL
		if probe.found {
			ttl = signozK8sInfraFoundTTL
		}
		if time.Since(probe.checkedAt) < ttl {
			return probe.found
		}
	}
	src := &SignozMetricSource{}
	metricsList, err := src.FetchMetricList(ctx, FetchMetricsListRequest{AccountId: accountId, Metric: signozK8sInfraProbeMetric})
	if err != nil {
		ctx.GetLogger().Warn("signoz: could not check for Kubernetes metrics; querying anyway", "account_id", accountId, "error", err)
		return true
	}
	found := false
	for _, m := range metricsList {
		if m.Metric == signozK8sInfraProbeMetric {
			found = true
			break
		}
	}
	signozK8sInfraProbes.Store(accountId, signozK8sInfraProbe{found: found, checkedAt: time.Now()})
	return found
}

// signozK8sInfraProbeMetric is the series every SigNoz Kubernetes collector install
// publishes; its absence means the charts have nothing to draw from.
const signozK8sInfraProbeMetric = signozContainerCPU

// signozK8sInfraNote is the note every key carries when SigNoz has no Kubernetes
// metrics, so an empty chart reads as "not collected" rather than "idle".
const signozK8sInfraNote = "SigNoz has no Kubernetes metrics (" + signozK8sInfraProbeMetric + "): " +
	"workload and node charts need SigNoz's Kubernetes collector (k8s-infra) on this cluster"

// fetchSignozUtilisation answers utilisation keys from SigNoz. It checks once that the
// Kubernetes collector's metrics exist before querying; a SigNoz that receives only
// application metrics, or an older release whose metric names are not the dotted OTel
// ones, would otherwise answer every chart with an empty series.
func fetchSignozUtilisation(ctx *security.RequestContext, req GetUtilisationTrendRequest, meta RequestMetadata, integrationSource string, instant bool) (OutputMetricQuery, map[string]string, error) {
	if !signozHasK8sInfraMetrics(ctx, req.AccountId) {
		results := make([]QueryResult, 0, len(meta.RequestedMetrics))
		for _, key := range meta.RequestedMetrics {
			results = append(results, QueryResult{QueryKey: key, Payload: []Result{}, Note: signozK8sInfraNote})
		}
		return OutputMetricQuery{Results: results}, nil, nil
	}

	queries := buildSignozUtilisationQueries(meta, meta.RequestedMetrics)
	if len(queries) == 0 {
		return OutputMetricQuery{Results: []QueryResult{}}, queries, nil
	}
	out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:            req.AccountId,
		MetricProvider:       "signoz",
		MetricProviderSource: integrationSource,
		Queries:              queries,
		StartTime:            req.StartTime,
		EndTime:              req.EndTime,
		StepInterval:         req.StepInterval,
		Instant:              instant,
	})
	return out, queries, err
}

// buildSignozNodeQueries renders the node-scoped keys: the node's own usage and
// allocatable capacity, and the requests/limits of the containers scheduled on it.
func buildSignozNodeQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := map[string]string{}
	if meta.NodeName == "" {
		return queries
	}
	node := fmt.Sprintf(`%s="%s"`, signozLabelNode, escapePromQLString(meta.NodeName))
	byContainer := fmt.Sprintf("by (%s, %s, %s)", signozLabelNamespace, signozLabelPod, signozLabelContainer)
	onNode := func(metric string) string { return signozSelector(metric, node) }
	specTotal := func(metric string) string { return fmt.Sprintf("sum(max %s (%s))", byContainer, onNode(metric)) }

	for _, m := range metrics {
		switch UtilisationKey(m) {
		case KeyCPUUsage:
			queries[m] = fmt.Sprintf("sum(%s)", onNode(signozNodeCPU))
		case KeyMemoryUsage:
			queries[m] = fmt.Sprintf("sum(%s)", onNode(signozNodeMemory))
		case KeyCPUTotal:
			queries[m] = fmt.Sprintf("sum(%s)", onNode("k8s.node.allocatable_cpu"))
		case KeyMemTotal:
			queries[m] = fmt.Sprintf("sum(%s)", onNode("k8s.node.allocatable_memory"))
		case KeyCPURequest:
			queries[m] = specTotal("k8s.container.cpu_request")
		case KeyCPULimit:
			queries[m] = specTotal("k8s.container.cpu_limit")
		case KeyMemoryRequest:
			queries[m] = specTotal("k8s.container.memory_request")
		case KeyMemoryLimit:
			queries[m] = specTotal("k8s.container.memory_limit")
		case KeyContainerCPUUsageByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, onNode(signozContainerCPU))
		case KeyContainerMemoryWorkingSetByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, onNode(signozContainerMemory))
		case KeyContainerCPURequestByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, onNode("k8s.container.cpu_request"))
		case KeyContainerCPULimitByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, onNode("k8s.container.cpu_limit"))
		case KeyContainerMemoryRequestByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, onNode("k8s.container.memory_request"))
		case KeyContainerMemoryLimitByPod:
			queries[m] = fmt.Sprintf("max %s (%s)", byContainer, onNode("k8s.container.memory_limit"))
		}
	}
	return queries
}
