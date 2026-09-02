package observability

import (
	"errors"
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"strconv"
	"strings"
)

func init() {
	playbooks.RegisterAction("pod_node_metrics_enricher_memory", &podNodeMetricsAction{resourceType: "memory"})
}

// podNodeMetricsAction renders the OOM-killed pod's memory usage against its
// container request and limit.
//
// It used to build three PromQL queries and run them through relay-server's
// /prometheus facade. That facade reaches the agent's `globalConfig.prometheus_url`,
// which a cluster shipping to Elasticsearch instead of Prometheus does not have — so
// on those clusters the OOM evidence card was permanently empty, with no error to
// say why. Going through FetchMetricUtilisation instead asks the account's own
// metrics provider for the same three abstract metrics, so it works on every backend
// the product supports.
//
// The action lives in observability rather than in playbooks because the metrics
// provider lookup is here, and playbooks cannot import this package (this package
// imports playbooks to register actions).
type podNodeMetricsAction struct {
	resourceType string // "memory" — kept extensible for future cpu/disk variants
}

var podNodeMetricsAggKeys = map[string]bool{
	"pod_oom_killer_enricher": true,
	"report_crash_loop":       true,
}

func (a *podNodeMetricsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if a.resourceType == "" {
		return false
	}
	if !podNodeMetricsAggKeys[ctx.GetEvent().AggregationKey] {
		return false
	}
	name, ns := playbooks.PodNamespaceFromEvent(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *podNodeMetricsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	name, ns := playbooks.PodNamespaceFromEvent(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"pod_name": name, "namespace": ns, "resource_type": a.resourceType})
}

func (a *podNodeMetricsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
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

	tenantID := ctx.GetTenantId()
	accountID := ctx.GetAccountId()
	if tenantID == "" || accountID == "" {
		return nil, errors.New("pod_node_metrics_enricher: tenant ID and account ID required")
	}
	requestCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), nil, nil)

	startTime, endTime := workloadMetricTimeRange(ctx.GetEvent())
	// Kind "pod" makes the builders match this exact pod rather than every pod of a
	// workload sharing the name prefix.
	metricsOutput, err := FetchMetricUtilisation(requestCtx, GetUtilisationTrendRequest{
		AccountId: accountID,
		StartTime: startTime,
		EndTime:   endTime,
		Request: map[string]any{
			"workload_namespace": namespace,
			"workload_name":      podName,
			"kind":               "pod",
			"metrics":            []string{"memory_usage", "memory_limit", "memory_request"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pod_node_metrics_enricher: metrics: %w", err)
	}

	data := seriesListByQueryKey(metricsOutput)
	if len(data) == 0 {
		// No series on any of the three metrics: nothing to draw. Returning nil skips
		// the evidence rather than rendering an empty chart.
		ctx.GetLogger().Info("pod_node_metrics_enricher: no series for pod",
			"pod", podName, "namespace", namespace, "account_id", accountID)
		return nil, nil
	}

	payload := map[string]any{
		"name":          "pod_node_metrics",
		"resource_type": "memory",
		"data":          data,
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
	return playbooks.NewPlaybookActionResponseJson(payload, additionalInfo, []playbooks.PlaybookActionResponseInsight{}, metadata), nil
}

// seriesListByQueryKey renders a utilisation result in the same wire shape
// relay-server's /prometheus facade produced, keyed by metric name.
//
// The UI consumes this payload verbatim, so the shape is a contract, not an
// implementation detail: {"<metric>": {"series_list_result": [{metric, timestamps,
// values}]}} with values and timestamps as strings — that is what
// transformToPrometheusValues on the relay side emits and what the existing
// consumers parse. Query keys with no payload are omitted so a metric the backend
// cannot answer is absent rather than present-and-empty.
func seriesListByQueryKey(out OutputMetricQuery) map[string]any {
	data := map[string]any{}
	for _, qr := range out.Results {
		if len(qr.Payload) == 0 {
			continue
		}
		seriesList := make([]any, 0, len(qr.Payload))
		for _, res := range qr.Payload {
			if len(res.Values) == 0 {
				continue
			}
			metric := make(map[string]any, len(res.Metric))
			for k, v := range res.Metric {
				metric[k] = v
			}
			// Both are filled by the loop over res.Values, so that is the capacity
			// they need — Timestamps can be shorter, and is padded with zero above.
			timestamps := make([]any, 0, len(res.Values))
			values := make([]any, 0, len(res.Values))
			for i, v := range res.Values {
				ts := int64(0)
				if i < len(res.Timestamps) {
					ts = res.Timestamps[i]
				}
				timestamps = append(timestamps, strconv.FormatInt(ts, 10))
				values = append(values, strconv.FormatFloat(v, 'f', -1, 64))
			}
			seriesList = append(seriesList, map[string]any{
				"metric":     metric,
				"timestamps": timestamps,
				"values":     values,
			})
		}
		if len(seriesList) == 0 {
			continue
		}
		data[qr.QueryKey] = map[string]any{"series_list_result": seriesList}
	}
	return data
}
