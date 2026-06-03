package observability

import (
	"encoding/json"
	"nudgebee/services/cloud"
	"nudgebee/services/security"
	"time"

	"github.com/samber/lo"
)

// getStepDuration returns a step duration for cloud metrics queries.
// If stepInterval is provided (> 0), use it. Otherwise calculate from the time range.
func getStepDuration(stepInterval int, startTime, endTime *time.Time) time.Duration {
	if stepInterval > 0 {
		return time.Duration(stepInterval) * time.Second
	}
	// Default: ~60 data points across the range, minimum 60s
	if startTime != nil && endTime != nil {
		rangeSeconds := int(endTime.Sub(*startTime).Seconds())
		step := rangeSeconds / 60
		if step < 60 {
			step = 60
		}
		return time.Duration(step) * time.Second
	}
	return 60 * time.Second
}

type cloudLogs struct{}

// QueryLabelValues implements LogSource.
func (c *cloudLogs) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	return []OutputLogLabelValue{}, nil
}

func (s *cloudLogs) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *cloudLogs) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like"}
}
func (s *cloudLogs) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return "", nil
}

// QueryLabels implements LogSource.
func (c *cloudLogs) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	return []OutputLogLabel{}, nil
}

// QueryLogs implements LogSource.
func (c *cloudLogs) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	var startTime *time.Time
	var endTime *time.Time

	if fetchLogRequest.StartTime != 0 {
		startTime1 := time.UnixMilli(fetchLogRequest.StartTime)
		startTime = &startTime1
	}

	if fetchLogRequest.EndTime != 0 {
		endTime1 := time.UnixMilli(fetchLogRequest.EndTime)
		endTime = &endTime1
	}

	region := ""
	serviceName := ""
	resourceId := ""
	logGroupName := ""
	if fetchLogRequest.Request != nil {
		for k, v := range fetchLogRequest.Request {
			switch k {
			case "aws_region", "region":
				if val, ok := v.(string); ok {
					region = val
				}
			case "service_name":
				if val, ok := v.(string); ok {
					serviceName = val
				}
			case "resource_id":
				if val, ok := v.(string); ok {
					resourceId = val
				}
			case "log_group":
				if val, ok := v.(string); ok {
					logGroupName = val
				}
			}
		}
	}

	limit := int64(100)
	if fetchLogRequest.Limit != 0 {
		limit = int64(fetchLogRequest.Limit)
	}

	resp, err := cloud.QueryLogs(ctx, cloud.QueryLogsRequest{
		AccountId: fetchLogRequest.AccountId,
		Query: cloud.LogQuery{
			StartTime:    startTime,
			EndTime:      endTime,
			QueryString:  fetchLogRequest.Query,
			Region:       region,
			ServiceName:  serviceName,
			ResourceId:   resourceId,
			LogGroupName: logGroupName,
			Limit:        &limit,
		},
	})

	if err != nil {
		return []OutputLog{}, err
	}

	//convert cloud logs to o/p
	outputLogs := make([]OutputLog, len(resp.Results))
	for i, result := range resp.Results {
		labels := map[string]any{}
		for _, v := range result.Labels {
			labels[v.Label] = v.Value
		}
		timestamp := ""
		if result.Timestamp > 0 {
			timestamp = time.UnixMilli(result.Timestamp).Format(time.RFC3339Nano)
		}

		outputLogs[i] = OutputLog{
			Timestamp: timestamp,
			Message:   result.Message,
			Labels:    labels,
		}
	}

	return outputLogs, nil
}

type cloudMetrics struct{}

// FetchMetricLabelValues implements MetricSource.
func (c *cloudMetrics) FetchMetricLabelValues(ctx *security.RequestContext, fetchMetricsLabelRequest FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	return []OutputMetricsLabelValues{}, nil
}

// FetchMetricList implements MetricSource.
func (c *cloudMetrics) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	serviceName := ""
	if req.Request != nil {
		if v, ok := req.Request["service_name"].(string); ok {
			serviceName = v
		}
	}

	resp, err := cloud.ListMetrics(ctx, req.AccountId, cloud.ListMetricsRequest{
		ServiceName: serviceName,
	})
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetrics, 0, len(resp.Metrics))
	for _, m := range resp.Metrics {
		output = append(output, OutputMetrics{
			Metric: m.Name,
			Attributes: map[string]any{
				"namespace":  m.Namespace,
				"statistics": m.Statistics,
			},
		})
	}
	return output, nil
}

// FetchMetricsLabels implements MetricSource.
func (c *cloudMetrics) FetchMetricsLabels(ctx *security.RequestContext, fetchMetricsRequest FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	return []OutputMetricLabels{}, nil
}

func (c *cloudMetrics) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like"}
}

func (c *cloudMetrics) GetQuery(_ *security.RequestContext, _ FetchMetricsRequest) (string, error) {
	return "", nil
}

// FetchMetricsQuery implements MetricSource.
func (c *cloudMetrics) FetchMetricsQuery(ctx *security.RequestContext, fetchMetricsRequest FetchMetricsRequest) (OutputMetricQuery, error) {

	var startTime *time.Time
	var endTime *time.Time

	if fetchMetricsRequest.StartTime != 0 {
		startTime1 := time.UnixMilli(fetchMetricsRequest.StartTime)
		startTime = &startTime1
	}

	if fetchMetricsRequest.EndTime != 0 {
		endTime1 := time.UnixMilli(fetchMetricsRequest.EndTime)
		endTime = &endTime1
	}

	region := ""
	serviceName := ""
	resourceIds := []string{}
	resourceType := ""
	metricsNames := []string{}
	metricNamespace := ""
	statistics := []string{}
	dimensions := []map[string]string{}

	if fetchMetricsRequest.Request != nil {
		for k, v := range fetchMetricsRequest.Request {
			switch k {
			case "aws_region", "azure_region", "gcp_region", "region":
				if val, ok := v.(string); ok {
					region = val
				}
			case "service_name":
				if val, ok := v.(string); ok {
					serviceName = val
				}
			case "resource_id":
				if val, ok := v.(string); ok {
					resourceIds = append(resourceIds, val)
				}
			case "resource_ids":
				switch vv := v.(type) {
				case []any:
					for _, id := range vv {
						if val, ok := id.(string); ok {
							resourceIds = append(resourceIds, val)
						}
					}
				case []string:
					resourceIds = append(resourceIds, vv...)
				}
			case "resource_type":
				if val, ok := v.(string); ok {
					resourceType = val
				}
			case "metric_name":
				if val, ok := v.(string); ok {
					metricsNames = append(metricsNames, val)
				}
			case "metric_names":
				switch vv := v.(type) {
				case []any:
					for _, id := range vv {
						if val, ok := id.(string); ok {
							metricsNames = append(metricsNames, val)
						}
					}
				case []string:
					metricsNames = append(metricsNames, vv...)
				}
			case "statistic":
				if val, ok := v.(string); ok {
					statistics = append(statistics, val)
				}
			case "statistics":
				switch vv := v.(type) {
				case []any:
					for _, id := range vv {
						if val, ok := id.(string); ok {
							statistics = append(statistics, val)
						}
					}
				case []string:
					statistics = append(statistics, vv...)
				}
			case "metric_namespace":
				if val, ok := v.(string); ok {
					metricNamespace = val
				}
			case "dimension":
				if val, ok := v.(map[string]string); ok {
					dimensions = append(dimensions, val)
				}
			case "dimensions":
				switch vv := v.(type) {
				case []any:
					for _, id := range vv {
						if val, ok := id.(map[string]string); ok {
							dimensions = append(dimensions, val)
						}
					}
				case []map[string]string:
					dimensions = append(dimensions, vv...)
				case string:
					err := json.Unmarshal([]byte(vv), &dimensions)
					if err != nil {
						ctx.GetLogger().Error("cloud: unable to parse dimensions data from alarms", "error", err, "dimensions", vv)
					}
				}
			}
		}
	}

	query := ""
	queryKey := ""
	if len(fetchMetricsRequest.Queries) > 0 {
		for k, q := range fetchMetricsRequest.Queries {
			query = q
			queryKey = k
			break
		}
	}

	resp, err := cloud.QueryMetrics(ctx, cloud.QueryMetricsRequest{
		AccountId: fetchMetricsRequest.AccountId,
		Query: cloud.MetricsQuery{
			ServiceName:     serviceName,
			Region:          region,
			StartDate:       startTime,
			EndDate:         endTime,
			ResourceIds:     resourceIds,
			ResourceType:    resourceType,
			MetricNames:     metricsNames,
			Step:            getStepDuration(fetchMetricsRequest.StepInterval, startTime, endTime),
			Dimensions:      dimensions,
			Statistics:      statistics,
			Query:           query,
			MetricNamespace: metricNamespace,
		},
	})
	if err != nil {
		return OutputMetricQuery{}, err
	}

	//convert cloud metrics to o/p
	result := []QueryResult{}
	for _, item := range resp.Items {
		metric := map[string]string{
			"name":         item.Name,
			"statistics":   item.Statistics,
			"resource_id":  item.ResourceId,
			"region":       item.Region,
			"service_name": item.ServiceName,
		}

		values := make([]Result, 0, 1)
		values = append(values, Result{
			Metric: metric,
			Timestamps: lo.Map(item.Timestamps, func(t time.Time, _ int) int64 {
				return t.UnixMilli()
			}),
			Values: item.Values,
		})
		result = append(result, QueryResult{
			QueryKey: queryKey,
			Payload:  values,
		})
	}

	return OutputMetricQuery{
		Results: result,
	}, nil
}
