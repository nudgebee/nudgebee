package cloudfoundry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"
	"time"
)

// queryLogs fetches real application logs from Log Cache when available,
// falling back to audit events as activity logs if Log Cache is not configured.
func queryLogs(ctx providers.CloudProviderContext, client *cfClient, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	if query.ResourceId == "" {
		return providers.QueryLogsResponse{
			Results: []providers.LogMessage{},
			Status:  "Complete",
		}, nil
	}

	// Use Log Cache for real app logs when available
	if client.logCacheURL != "" {
		return queryLogsFromLogCache(ctx, client, query)
	}

	// Fall back to audit events
	return queryLogsFromAuditEvents(ctx, client, query)
}

// queryLogsFromLogCache fetches real application stdout/stderr from the Log Cache API.
func queryLogsFromLogCache(ctx providers.CloudProviderContext, client *cfClient, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	logger := ctx.GetLogger()

	// Build Log Cache API path
	params := url.Values{}
	params.Set("envelope_types", "LOG")
	params.Set("descending", "true")

	limit := int64(100)
	if query.Limit != nil && *query.Limit > 0 {
		limit = *query.Limit
	}
	params.Set("limit", strconv.FormatInt(limit, 10))

	if query.StartTime != nil {
		params.Set("start_time", strconv.FormatInt(query.StartTime.UnixNano(), 10))
	}
	if query.EndTime != nil {
		params.Set("end_time", strconv.FormatInt(query.EndTime.UnixNano(), 10))
	}

	path := fmt.Sprintf("/api/v1/read/%s?%s", query.ResourceId, params.Encode())
	logger.Info("CloudFoundry: querying Log Cache", "resource_id", query.ResourceId)

	reqCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	body, err := client.getFromLogCache(reqCtx, path)
	if err != nil {
		logger.Warn("CloudFoundry: Log Cache query failed, falling back to audit events", "error", err)
		return queryLogsFromAuditEvents(ctx, client, query)
	}

	var resp logCacheResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		logger.Warn("CloudFoundry: failed to parse Log Cache response, falling back to audit events", "error", err)
		return queryLogsFromAuditEvents(ctx, client, query)
	}

	var results []providers.LogMessage
	for _, env := range resp.Envelopes.Batch {
		if env.Log == nil {
			continue
		}

		// Decode base64 payload
		payload, err := base64.StdEncoding.DecodeString(env.Log.Payload)
		if err != nil {
			// Try raw payload (some deployments return plain text)
			payload = []byte(env.Log.Payload)
		}

		message := string(payload)
		if env.Log.Type == "ERR" {
			message = "[ERR] " + message
		}

		// Parse timestamp (nanoseconds string → milliseconds)
		tsNanos, _ := strconv.ParseInt(env.Timestamp, 10, 64)
		tsMillis := tsNanos / 1_000_000

		labels := []providers.LogLabel{
			{Label: "log_type", Value: env.Log.Type},
			{Label: "instance_id", Value: env.InstanceID},
		}
		if sourceType, ok := env.Tags["source_type"]; ok {
			labels = append(labels, providers.LogLabel{Label: "source_type", Value: sourceType})
		}
		if appName, ok := env.Tags["app_name"]; ok {
			labels = append(labels, providers.LogLabel{Label: "app_name", Value: appName})
		}
		if orgName, ok := env.Tags["organization_name"]; ok {
			labels = append(labels, providers.LogLabel{Label: "org_name", Value: orgName})
		}
		if spaceName, ok := env.Tags["space_name"]; ok {
			labels = append(labels, providers.LogLabel{Label: "space_name", Value: spaceName})
		}

		results = append(results, providers.LogMessage{
			Message:   message,
			Timestamp: tsMillis,
			Labels:    labels,
		})
	}

	logger.Info("CloudFoundry: Log Cache query completed", "record_count", len(results))

	return providers.QueryLogsResponse{
		Results: results,
		Status:  "Complete",
		Statistics: providers.LogQueryStatistics{
			RecordsMatched: float64(len(results)),
			RecordsScanned: float64(len(results)),
		},
	}, nil
}

// queryLogsFromAuditEvents is the fallback that uses CF audit events as activity logs.
func queryLogsFromAuditEvents(ctx providers.CloudProviderContext, client *cfClient, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	logger := ctx.GetLogger()

	// Build audit events query with proper URL encoding
	params := url.Values{}
	params.Set("target_guids", query.ResourceId)
	params.Set("per_page", "100")
	params.Set("order_by", "-created_at")
	if query.StartTime != nil {
		params.Set("created_ats[gte]", query.StartTime.Format(time.RFC3339))
	}
	if query.EndTime != nil {
		params.Set("created_ats[lte]", query.EndTime.Format(time.RFC3339))
	}
	path := "/v3/audit_events?" + params.Encode()

	logger.Info("CloudFoundry: querying audit events as logs", "resource_id", query.ResourceId, "path", path)

	cfEvents, err := getPaginated[cfAuditEvent](client, path)
	if err != nil {
		logger.Warn("CloudFoundry: failed to fetch audit events for log query", "error", err)
		return providers.QueryLogsResponse{
			Results: []providers.LogMessage{},
			Status:  "Failed",
		}, err
	}

	// Convert audit events to LogMessage format
	var results []providers.LogMessage
	for _, ev := range cfEvents {
		message := fmt.Sprintf("[%s] %s by %s (%s) on %s %s",
			ev.Type,
			ev.Type,
			ev.Actor.Name,
			ev.Actor.Type,
			ev.Target.Type,
			ev.Target.Name,
		)

		labels := []providers.LogLabel{
			{Label: "event_type", Value: ev.Type},
			{Label: "actor", Value: ev.Actor.Name},
			{Label: "actor_type", Value: ev.Actor.Type},
			{Label: "target_type", Value: ev.Target.Type},
			{Label: "target_name", Value: ev.Target.Name},
			{Label: "target_guid", Value: ev.Target.GUID},
		}
		if ev.Space.GUID != "" {
			labels = append(labels, providers.LogLabel{Label: "space_guid", Value: ev.Space.GUID})
		}
		if ev.Organization.GUID != "" {
			labels = append(labels, providers.LogLabel{Label: "org_guid", Value: ev.Organization.GUID})
		}

		results = append(results, providers.LogMessage{
			Message:   message,
			Timestamp: ev.CreatedAt.UnixMilli(),
			Labels:    labels,
		})
	}

	logger.Info("CloudFoundry: log query completed", "record_count", len(results))

	return providers.QueryLogsResponse{
		Results: results,
		Status:  "Complete",
		Statistics: providers.LogQueryStatistics{
			RecordsMatched: float64(len(results)),
			RecordsScanned: float64(len(results)),
		},
	}, nil
}

// fetchLogsFromLogCache fetches recent application logs from Log Cache for a specific app.
// Used by event enrichment to attach log evidence to crash events.
func fetchLogsFromLogCache(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, appGUID string, startTime, endTime time.Time, limit int) ([]logCacheEnvelope, error) {
	params := url.Values{}
	params.Set("envelope_types", "LOG")
	params.Set("descending", "true")
	params.Set("limit", strconv.Itoa(limit))
	params.Set("start_time", strconv.FormatInt(startTime.UnixNano(), 10))
	params.Set("end_time", strconv.FormatInt(endTime.UnixNano(), 10))

	path := fmt.Sprintf("/api/v1/read/%s?%s", appGUID, params.Encode())

	body, err := client.getFromLogCache(enrichCtx, path)
	if err != nil {
		return nil, err
	}

	var resp logCacheResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse Log Cache response: %w", err)
	}

	return resp.Envelopes.Batch, nil
}

// formatLogEntries converts Log Cache envelopes into a structured log entry list
// suitable for event evidence display.
func formatLogEntries(envelopes []logCacheEnvelope) ([]map[string]string, []string) {
	var entries []map[string]string
	var errMessages []string

	for _, env := range envelopes {
		if env.Log == nil {
			continue
		}

		payload, err := base64.StdEncoding.DecodeString(env.Log.Payload)
		if err != nil {
			payload = []byte(env.Log.Payload)
		}

		tsNanos, _ := strconv.ParseInt(env.Timestamp, 10, 64)
		ts := time.Unix(0, tsNanos).UTC().Format(time.RFC3339)

		sourceType := env.Tags["source_type"]
		if sourceType == "" {
			sourceType = "APP"
		}

		entry := map[string]string{
			"timestamp":   ts,
			"message":     strings.TrimSpace(string(payload)),
			"source_type": sourceType,
			"instance_id": env.InstanceID,
			"log_type":    env.Log.Type,
		}
		entries = append(entries, entry)

		// Collect ERR messages for insights
		if env.Log.Type == "ERR" {
			msg := strings.TrimSpace(string(payload))
			if msg != "" {
				errMessages = append(errMessages, msg)
			}
		}
	}

	return entries, errMessages
}
