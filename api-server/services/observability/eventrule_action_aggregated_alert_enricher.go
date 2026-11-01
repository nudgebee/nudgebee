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

	if event.AggregationKey == "HighErrorCriticalLogs" {
		return a.enrichLogSpike(ctx)
	}

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

// enrichLogSpike handles the error-log-spike alert. Besides the event's
// container_id and sample labels it attaches which log patterns rose and by how
// much (see LogPatternBreakdown), and takes the labels from the pattern that
// rose most rather than the one logging most at the moment of the query.
func (a *aggregatedAlertLabelEnricher) enrichLogSpike(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	event := ctx.GetEvent()
	start, end := logSpikeWindow(event, time.Now())
	currentQuery, normalQuery, measuredQuery := logSpikeQueries(event.Labels["app_id"])
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	counts, err := fetchLogSpikeSeries(requestCtx, ctx.GetAccountId(), map[string]string{"current": currentQuery}, start, end, false)
	if err != nil {
		ctx.GetLogger().Warn("aggregated_alert_label_enricher: metrics query failed", "error", err, "query", currentQuery)
		return nil, err
	}
	current := counts["current"]
	if len(current) == 0 {
		ctx.GetLogger().Info("aggregated_alert_label_enricher: no series found", "query", currentQuery)
		return nil, fmt.Errorf("no series found for query")
	}

	// The normal counts are their own request: they look back hours, and if
	// that is slow or fails it must cost the breakdown, never the labels. It
	// is one evaluation at the window start, not a range — a range would
	// repeat the hours-long lookback at every step.
	var breakdown *LogPatternBreakdown
	baseline, err := fetchLogSpikeSeries(requestCtx, ctx.GetAccountId(), map[string]string{"normal": normalQuery, "measured": measuredQuery}, start, start, true)
	if err != nil {
		ctx.GetLogger().Warn("aggregated_alert_label_enricher: baseline query failed, enriching labels only", "error", err, "query", normalQuery)
	} else {
		breakdown = buildLogPatternBreakdown(current, baseline["normal"], baseline["measured"], start, end)
	}

	data := map[string]any{}
	insights := []playbooks.PlaybookActionResponseInsight{}
	var extracted map[string]any
	if breakdown != nil {
		extracted = breakdown.topLabels()
		data["pattern_breakdown"] = breakdown
		insights = append(insights, playbooks.PlaybookActionResponseInsight{Message: breakdown.summary(), Severity: "Info"})
	} else {
		extracted = map[string]any{}
		topMetric := pickTopSeriesMetric(OutputMetricQuery{Results: []QueryResult{{Payload: current}}})
		for _, key := range logFailureLabelsToExtract {
			if v, ok := topMetric[key]; ok {
				extracted[key] = v
			}
		}
	}
	data["enriched_labels"] = extracted

	ctx.GetLogger().Info("aggregated_alert_label_enricher: enriched labels",
		"aggregation_key", event.AggregationKey, "labels", extracted, "has_breakdown", breakdown != nil)

	resp := playbooks.NewPlaybookActionResponseJson(
		data,
		map[string]any{},
		insights,
		map[string]any{"query": currentQuery, "baseline_query": normalQuery},
	)
	resp.Labels = extracted
	return resp, nil
}

// fetchLogSpikeSeries runs the queries over the range, or evaluates them once
// at its end when instant, and returns each query's series by its key. The
// step is left to the metrics source — the relay path picks its own from the
// range and does not take one from here.
func fetchLogSpikeSeries(ctx *security.RequestContext, accountId string, queries map[string]string, start time.Time, end time.Time, instant bool) (map[string][]Result, error) {
	output, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:      accountId,
		MetricProvider: integrationPrometheus,
		Queries:        queries,
		StartTime:      start.UnixMilli(),
		EndTime:        end.UnixMilli(),
		Instant:        instant,
	})
	if err != nil {
		return nil, err
	}
	series := make(map[string][]Result, len(queries))
	for _, result := range output.Results {
		if result.Error != nil {
			return nil, fmt.Errorf("metrics query %q failed: %s", result.QueryKey, *result.Error)
		}
		series[result.QueryKey] = append(series[result.QueryKey], result.Payload...)
	}
	return series, nil
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
