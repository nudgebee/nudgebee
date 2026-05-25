package playbooks

import (
	"errors"
	"fmt"
	"strings"
)

// pod_node_metrics_enricher_memory renders a Prometheus graph of the OOM
// pod's memory usage vs its container memory limit/request over the last
// hour. Three queries (working-set, kube_pod_container_resource_limits,
// kube_pod_container_resource_requests) are passed through
// prometheus_queries_enricher and the UI overlays them.
type podNodeMetricsAction struct {
	resourceType string // "memory" — kept extensible for future cpu/disk variants
}

var podNodeMetricsAggKeys = map[string]bool{
	"pod_oom_killer_enricher": true,
	"report_crash_loop":       true,
}

func (a *podNodeMetricsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if a.resourceType == "" {
		return false
	}
	if !podNodeMetricsAggKeys[ctx.GetEvent().AggregationKey] {
		return false
	}
	name, ns := subjectPodNamespace(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *podNodeMetricsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := subjectPodNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"pod_name": name, "namespace": ns, "resource_type": a.resourceType})
}

func (a *podNodeMetricsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	podName, _ := rawParams["pod_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	resourceType, _ := rawParams["resource_type"].(string)
	if podName == "" || namespace == "" {
		return nil, errors.New("pod_node_metrics_enricher: pod_name + namespace required")
	}
	if resourceType == "" {
		resourceType = a.resourceType
	}
	if !strings.EqualFold(resourceType, "memory") {
		return nil, fmt.Errorf("pod_node_metrics_enricher: unsupported resource_type %q (memory only)", resourceType)
	}

	usageQ := fmt.Sprintf(
		`container_memory_working_set_bytes{__CLUSTER__ pod="%s", namespace="%s", container!="", container!="POD", image!=""}`,
		podName, namespace,
	)
	limitQ := fmt.Sprintf(
		`kube_pod_container_resource_limits{__CLUSTER__ pod="%s", namespace="%s", resource="memory"}`,
		podName, namespace,
	)
	requestQ := fmt.Sprintf(
		`kube_pod_container_resource_requests{__CLUSTER__ pod="%s", namespace="%s", resource="memory"}`,
		podName, namespace,
	)

	results, err := promRangeQueries(ctx, []NamedQuery{
		{Key: "memory_usage", Query: usageQ},
		{Key: "memory_limit", Query: limitQ},
		{Key: "memory_request", Query: requestQ},
	}, 60)
	if err != nil {
		return nil, fmt.Errorf("pod_node_metrics_enricher: prom: %w", err)
	}

	payload := map[string]any{
		"name":          "pod_node_metrics",
		"resource_type": "memory",
		"data":          results,
	}
	additionalInfo := map[string]any{
		"title":              "Pod memory vs limit",
		"action_name":        "pod_node_metrics_enricher",
		"actual_action_name": "pod_node_metrics_enricher",
		"metric_name":        "memory",
		"pod_name":           podName,
		"namespace":          namespace,
	}
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	return NewPlaybookActionResponseJson(payload, additionalInfo, []PlaybookActionResponseInsight{}, metadata), nil
}
