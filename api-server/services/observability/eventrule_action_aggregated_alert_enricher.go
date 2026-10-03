package observability

import (
	"fmt"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"strings"
	"time"
)

// sanitizePromQLLabel escapes double quotes and backslashes in a label value
// to prevent PromQL injection when interpolating into queries.
func sanitizePromQLLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

func init() {
	playbooks.RegisterAction("aggregated_alert_label_enricher", &aggregatedAlertLabelEnricher{})
}

type aggregatedAlertLabelEnricher struct{}

// apiFailureLabelsToExtract are the per-series labels we want from ApplicationAPIFailures
var apiFailureLabelsToExtract = []string{"path", "method", "status"}

// logFailureLabelsToExtract are the per-series labels we want from HighErrorCriticalLogs
var logFailureLabelsToExtract = []string{"container_id", "sample"}

func (a *aggregatedAlertLabelEnricher) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	event := ctx.GetEvent()
	labels := event.Labels

	// Don't check container_id here — other actions like prometheus_enricher may have already
	// set it on the cloned event labels from an unrelated series. We always run for aggregated
	// alerts and let InvestigateEvent merge our labels into the original event.

	switch event.AggregationKey {
	case "ApplicationAPIFailures":
		return labels["destination_workload_name"] != "" && labels["destination_workload_namespace"] != ""
	case "HighErrorCriticalLogs":
		return labels["app_id"] != ""
	default:
		return false
	}
}

func (a *aggregatedAlertLabelEnricher) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	event := ctx.GetEvent()
	labels := event.Labels

	var query string
	var labelsToExtract []string

	switch event.AggregationKey {
	case "ApplicationAPIFailures":
		query = fmt.Sprintf(
			`sum by (path, method, status) (increase(container_http_requests_total{destination_workload_name="%s", destination_workload_namespace="%s", status=~"5..|4.."}[5m]))`,
			sanitizePromQLLabel(labels["destination_workload_name"]),
			sanitizePromQLLabel(labels["destination_workload_namespace"]),
		)
		labelsToExtract = apiFailureLabelsToExtract
	case "HighErrorCriticalLogs":
		query = fmt.Sprintf(
			`increase(container_log_messages_total{app_id="%s", level=~"error|critical"}[5m])`,
			sanitizePromQLLabel(labels["app_id"]),
		)
		labelsToExtract = logFailureLabelsToExtract
	default:
		return nil, fmt.Errorf("unsupported aggregation key: %s", event.AggregationKey)
	}

	// Use a range query (not instant) because the PagerDuty webhook often arrives
	// hours after the alert condition was true, making the metric data stale for
	// instant queries. The prometheus_enricher uses the same approach and finds data.
	endTime := time.Now()
	startTime := endTime.Add(-10 * time.Minute)
	if event.StartedAt != nil {
		startTime = *event.StartedAt
	}
	if event.EndedAt != nil {
		endTime = *event.EndedAt
	}

	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	output, err := FetchMetricsQuery(requestCtx, FetchMetricsRequest{
		AccountId:      ctx.GetAccountId(),
		MetricProvider: integrationPrometheus,
		Queries:        map[string]string{"A": query},
		StartTime:      startTime.UnixMilli(),
		EndTime:        endTime.UnixMilli(),
	})
	if err != nil {
		ctx.GetLogger().Warn("aggregated_alert_label_enricher: metrics query failed", "error", err, "query", query)
		return nil, err
	}

	topMetric := pickTopSeriesMetric(output)
	if topMetric == nil {
		ctx.GetLogger().Info("aggregated_alert_label_enricher: no series found", "query", query)
		return nil, fmt.Errorf("no series found for query")
	}

	// Extract only the labels we care about
	extracted := map[string]any{}
	for _, key := range labelsToExtract {
		if v, ok := topMetric[key]; ok {
			extracted[key] = v
		}
	}

	ctx.GetLogger().Info("aggregated_alert_label_enricher: enriched labels",
		"aggregation_key", event.AggregationKey, "labels", extracted)

	resp := playbooks.NewPlaybookActionResponseJson(
		map[string]any{"enriched_labels": extracted},
		map[string]any{},
		[]playbooks.PlaybookActionResponseInsight{},
		map[string]any{"query": query},
	)
	resp.Labels = extracted
	return resp, nil
}

func (a *aggregatedAlertLabelEnricher) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

// pickTopSeriesMetric returns the labels of the series whose latest sample is
// the highest — the (path, method, status) or container that failed most.
func pickTopSeriesMetric(output OutputMetricQuery) map[string]any {
	var top map[string]any
	topValue := -1.0
	for _, result := range output.Results {
		for _, series := range result.Payload {
			if len(series.Values) == 0 {
				continue
			}
			if v := series.Values[len(series.Values)-1]; v > topValue {
				topValue = v
				top = promQLMetricLabels(series.Metric)
			}
		}
	}
	return top
}
