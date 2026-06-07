package gcloud

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/logging/logadmin"
	"google.golang.org/api/iterator"
)

const (
	defaultLogLimit    = 1000
	maxLogLimit        = 50000
	defaultLogDuration = 1 * time.Hour
)

func queryGcloudLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	logger := ctx.GetLogger()

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		logger.Error("failed to get gcloud session for QueryLogs", "error", err, "accountNumber", account.AccountNumber)
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := logadmin.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		logger.Error("failed to create logadmin client", "error", err, "projectId", session.ProjectId)
		return providers.QueryLogsResponse{}, fmt.Errorf("failed to create logging client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			logger.Error("failed to close logadmin client", "error", cerr)
		}
	}()

	// For GCP log-based metric alerts, fetch the metric's filter and apply it
	if query.LogMetricName != "" {
		metricFilter, err := getLogMetricFilter(ctx, client, query.LogMetricName)
		if err != nil {
			logger.Warn("failed to fetch log metric filter, proceeding without it",
				"metricName", query.LogMetricName, "error", err)
		} else if metricFilter != "" {
			if query.QueryString != "" {
				query.QueryString = query.QueryString + "\n" + metricFilter
			} else {
				query.QueryString = metricFilter
			}
			logger.Info("applied log metric filter", "metricName", query.LogMetricName, "filter", metricFilter)
		}
	}

	// Resolve log filter from service if not provided
	resourceFilter := ""
	if query.LogGroupName == "" && query.ServiceName != "" {
		if service, ok := GetGcloudService(query.ServiceName); ok {
			resourceFilter = service.GetLogFilter(ctx, account, query.ResourceId)
		}
	}

	filter := buildLogFilter(query, resourceFilter)
	if filter == "" {
		logger.Warn("empty log filter, returning no results", "service", query.ServiceName, "resource", query.ResourceId)
		return providers.QueryLogsResponse{Status: "Complete", Results: []providers.LogMessage{}}, nil
	}

	limit := defaultLogLimit
	if query.Limit != nil && *query.Limit > 0 {
		limit = int(*query.Limit)
	}
	// Defense-in-depth cap (api-server side already caps for cloud paths). Keeps
	// the upfront make() allocation bounded if a future caller bypasses the
	// outer cap.
	if limit > maxLogLimit {
		limit = maxLogLimit
	}

	logger.Info("querying GCP logs", "projectId", session.ProjectId, "filter", filter, "limit", limit)

	it := client.Entries(ctx.GetContext(), logadmin.Filter(filter))

	messages := make([]providers.LogMessage, 0, limit)
	for i := 0; i < limit; i++ {
		entry, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			logger.Error("error reading log entry", "error", err)
			break
		}
		messages = append(messages, logEntryToMessage(entry))
	}

	return providers.QueryLogsResponse{
		Status:  "Complete",
		Results: messages,
	}, nil
}

func buildLogFilter(query providers.QueryLogsRequest, resourceFilter string) string {
	var parts []string

	// Log name filter (equivalent to AWS log group)
	if query.LogGroupName != "" {
		parts = append(parts, fmt.Sprintf(`logName="%s"`, query.LogGroupName))
	}

	// Resource-based filter from service
	if resourceFilter != "" {
		parts = append(parts, resourceFilter)
	}

	// Time range
	endTime := time.Now()
	if query.EndTime != nil {
		endTime = *query.EndTime
	}
	startTime := endTime.Add(-defaultLogDuration)
	if query.StartTime != nil {
		startTime = *query.StartTime
	}

	parts = append(parts, fmt.Sprintf(`timestamp>="%s"`, startTime.UTC().Format(time.RFC3339)))
	parts = append(parts, fmt.Sprintf(`timestamp<="%s"`, endTime.UTC().Format(time.RFC3339)))

	// Custom query string as additional filter (skip AWS CloudWatch Insights syntax)
	if query.QueryString != "" && !strings.HasPrefix(query.QueryString, "fields ") {
		parts = append(parts, query.QueryString)
	}

	return strings.Join(parts, "\n")
}

func logEntryToMessage(entry *logging.Entry) providers.LogMessage {
	msg := providers.LogMessage{
		Timestamp: entry.Timestamp.UnixMilli(),
		Labels:    []providers.LogLabel{},
	}

	// Extract message from payload
	switch p := entry.Payload.(type) {
	case string:
		msg.Message = p
	default:
		if b, err := json.Marshal(p); err == nil {
			msg.Message = string(b)
		}
	}

	// Add severity as label
	if entry.Severity != logging.Default {
		msg.Labels = append(msg.Labels, providers.LogLabel{Label: "severity", Value: entry.Severity.String()})
	}

	// Add log name
	if entry.LogName != "" {
		msg.Labels = append(msg.Labels, providers.LogLabel{Label: "logName", Value: entry.LogName})
	}

	// Add entry labels
	for k, v := range entry.Labels {
		msg.Labels = append(msg.Labels, providers.LogLabel{Label: k, Value: v})
	}

	// Add resource labels
	if entry.Resource != nil {
		for k, v := range entry.Resource.Labels {
			msg.Labels = append(msg.Labels, providers.LogLabel{Label: "resource." + k, Value: v})
		}
		if entry.Resource.Type != "" {
			msg.Labels = append(msg.Labels, providers.LogLabel{Label: "resource.type", Value: entry.Resource.Type})
		}
	}

	return msg
}

// getLogMetricFilter fetches the filter string for a GCP user-defined log-based metric.
// metricName is the short metric ID (e.g., "dev-pg-slow-queries"), not the full type path.
func getLogMetricFilter(ctx providers.CloudProviderContext, client *logadmin.Client, metricName string) (string, error) {
	metric, err := client.Metric(ctx.GetContext(), metricName)
	if err != nil {
		return "", fmt.Errorf("failed to fetch log metric %q: %w", metricName, err)
	}
	return metric.Filter, nil
}
