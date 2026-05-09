package observability

import (
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/relay"
	"strconv"
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

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "prometheus_queries_enricher",
			ActionParams: map[string]any{
				"duration": map[string]any{
					"ends_at":   endTime.UTC().Format("2006-01-02 15:04:05 UTC"),
					"starts_at": startTime.UTC().Format("2006-01-02 15:04:05 UTC"),
				},
				"instant": false,
				"promql_queries": []playbooks.NamedQuery{
					{Key: "A", Query: query},
				},
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		ctx.GetLogger().Warn("aggregated_alert_label_enricher: relay query failed", "error", err, "query", query)
		return nil, err
	}

	// Parse response: relayResponse["data"] -> data["A"] -> series_list_result
	topMetric := pickTopSeriesMetric(relayResponse, ctx)
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

// pickTopSeriesMetric parses the relay response and returns the metric labels of the series with the highest value.
func pickTopSeriesMetric(relayResponse map[string]any, ctx playbooks.PlaybookActionContext) map[string]any {
	// Extract data from response
	var data map[string]any
	if relayResponse["data"] != nil {
		switch d := relayResponse["data"].(type) {
		case map[string]any:
			data = d
		case string:
			err := common.UnmarshalJson([]byte(d), &data)
			if err != nil {
				ctx.GetLogger().Warn("aggregated_alert_label_enricher: failed to parse data", "error", err)
				return nil
			}
		}
	}
	if data == nil {
		return nil
	}

	// Get query result (key "A")
	queryResult, ok := data["A"]
	if !ok {
		return nil
	}

	// The relay response for "A" can be:
	// 1. []any — flat array of series (from ExecuteAndExtractResponse parsing)
	// 2. map[string]any — with "vector_result" or "series_list_result" keys
	var seriesList []any
	switch qr := queryResult.(type) {
	case []any:
		seriesList = qr
	case map[string]any:
		seriesList, _ = qr["vector_result"].([]any)
		if len(seriesList) == 0 {
			seriesList, _ = qr["series_list_result"].([]any)
		}
	}
	if len(seriesList) == 0 {
		return nil
	}

	var topMetric map[string]any
	topValue := -1.0

	for _, item := range seriesList {
		series, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric, ok := series["metric"].(map[string]any)
		if !ok {
			continue
		}

		// For instant queries, value is [timestamp, "value_string"]
		value, ok := series["value"].([]any)
		if ok && len(value) >= 2 {
			if vStr, ok := value[1].(string); ok {
				if v, err := strconv.ParseFloat(vStr, 64); err == nil && v > topValue {
					topValue = v
					topMetric = metric
				}
			}
		}

		// For range queries, values can be flat strings ["v1", "v2"] or
		// nested [timestamp, value] pairs after relay transformation.
		values, ok := series["values"].([]any)
		if ok && len(values) > 0 {
			lastVal := values[len(values)-1]
			var vStr string
			switch lv := lastVal.(type) {
			case string:
				vStr = lv
			case []any:
				if len(lv) >= 2 {
					vStr, _ = lv[1].(string)
				}
			}
			if vStr != "" {
				if v, err := strconv.ParseFloat(vStr, 64); err == nil && v > topValue {
					topValue = v
					topMetric = metric
				}
			}
		}
	}

	return topMetric
}
