package cloudfoundry

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"sync"
	"time"
)

const (
	maxEnrichedEvents    = 50
	enrichmentTimeout    = 5 * time.Second
	enrichmentWorkerPool = 5
)

// enrichEvents adds evidence context to HIGH and MEDIUM severity events.
// It fetches app details, process stats, recent builds, deployments, and bindings
// for each event's target resource.
func enrichEvents(ctx providers.CloudProviderContext, client *cfClient, events []providers.Event) []providers.Event {
	logger := ctx.GetLogger()

	// Collect events that need enrichment (HIGH + MEDIUM only)
	var toEnrich []int
	for i, ev := range events {
		if ev.EventSeverity == providers.EventSeverityHigh || ev.EventSeverity == providers.EventSeverityMedium {
			toEnrich = append(toEnrich, i)
		}
	}

	if len(toEnrich) == 0 {
		return events
	}

	// Cap at maxEnrichedEvents
	if len(toEnrich) > maxEnrichedEvents {
		toEnrich = toEnrich[:maxEnrichedEvents]
	}

	logger.Info("CloudFoundry: enriching events with evidence", "count", len(toEnrich))

	// Worker pool for concurrent enrichment
	type enrichResult struct {
		index    int
		evidence []providers.EventEvidence
	}

	jobs := make(chan int, len(toEnrich))
	results := make(chan enrichResult, len(toEnrich))

	var wg sync.WaitGroup
	for w := 0; w < enrichmentWorkerPool; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				ev := events[idx]
				evidence := fetchEvidence(ctx, client, ev)
				results <- enrichResult{index: idx, evidence: evidence}
			}
		}()
	}

	for _, idx := range toEnrich {
		jobs <- idx
	}
	close(jobs)

	wg.Wait()
	close(results)

	for r := range results {
		events[r.index].AdditionalContext = append(events[r.index].AdditionalContext, r.evidence...)
	}

	return events
}

// fetchEvidence fetches relevant context for a single event based on its resource type.
// Each enrichment call is bounded by enrichmentTimeout.
func fetchEvidence(ctx providers.CloudProviderContext, client *cfClient, ev providers.Event) []providers.EventEvidence {
	logger := ctx.GetLogger()
	targetType := strings.ToLower(ev.ResourceType)
	targetGUID := ev.ResourceId

	if targetGUID == "" {
		return nil
	}

	enrichCtx, cancel := context.WithTimeout(context.Background(), enrichmentTimeout)
	defer cancel()

	var evidence []providers.EventEvidence

	switch targetType {
	case "app":
		evidence = enrichAppEvent(ctx, client, enrichCtx, targetGUID, ev)
	case "build":
		evidence = enrichBuildEvent(ctx, client, enrichCtx, targetGUID)
	case "service_instance":
		evidence = enrichServiceInstanceEvent(ctx, client, enrichCtx, targetGUID)
	default:
		logger.Debug("CloudFoundry: no enrichment defined for target type", "type", targetType)
	}

	return evidence
}

// enrichAppEvent fetches evidence for app-related events.
func enrichAppEvent(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, appGUID string, ev providers.Event) []providers.EventEvidence {
	logger := ctx.GetLogger()
	var evidence []providers.EventEvidence

	// App details
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/apps/%s", appGUID), "App Details", "app_details"); e != nil {
		evidence = append(evidence, *e)
	}

	// Process stats
	if processEvidence := fetchProcessStats(ctx, client, enrichCtx, appGUID); len(processEvidence) > 0 {
		evidence = append(evidence, processEvidence...)
	}

	// Recent builds
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/builds?app_guids=%s&per_page=5&order_by=-created_at", appGUID), "Recent Builds", "recent_builds"); e != nil {
		evidence = append(evidence, *e)
	}

	// Recent deployments
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/deployments?app_guids=%s&per_page=3&order_by=-created_at", appGUID), "Recent Deployments", "recent_deployments"); e != nil {
		evidence = append(evidence, *e)
	}

	// Service bindings
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/service_credential_bindings?app_guids=%s", appGUID), "Service Bindings", "service_bindings"); e != nil {
		evidence = append(evidence, *e)
	}

	// Routes (networking context)
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/routes?app_guids=%s", appGUID), "App Routes", "app_routes"); e != nil {
		evidence = append(evidence, *e)
	}

	// Fetch recent logs for crash, error, and failure events when Log Cache is available
	if client.logCacheURL != "" && isLogWorthyEvent(ev.EventName) {
		if logEvidence := fetchRecentLogs(ctx, client, enrichCtx, appGUID, ev.Date); logEvidence != nil {
			evidence = append(evidence, *logEvidence)
		}
	}

	// Audit event timeline — recent management actions on this app
	if timelineEvidence := fetchAuditEventTimeline(ctx, client, enrichCtx, appGUID, ev.Date); timelineEvidence != nil {
		evidence = append(evidence, *timelineEvidence)
	}

	logger.Debug("CloudFoundry: enriched app event", "app_guid", appGUID, "evidence_count", len(evidence))
	return evidence
}

// isLogWorthyEvent returns true if the event type should have log evidence attached.
func isLogWorthyEvent(eventName string) bool {
	lower := strings.ToLower(eventName)
	return strings.Contains(lower, "crash") ||
		strings.Contains(lower, "error") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "instances_down")
}

// fetchRecentLogs fetches recent application logs around an event time from Log Cache.
// Time window: event time - 5 minutes → event time + 1 minute.
func fetchRecentLogs(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, appGUID string, eventTime time.Time) *providers.EventEvidence {
	logger := ctx.GetLogger()

	startTime := eventTime.Add(-5 * time.Minute)
	endTime := eventTime.Add(1 * time.Minute)

	envelopes, err := fetchLogsFromLogCache(ctx, client, enrichCtx, appGUID, startTime, endTime, 50)
	if err != nil {
		logger.Warn("CloudFoundry: failed to fetch logs from Log Cache for enrichment", "app_guid", appGUID, "error", err)
		return nil
	}

	if len(envelopes) == 0 {
		return nil
	}

	entries, errMessages := formatLogEntries(envelopes)
	if len(entries) == 0 {
		return nil
	}

	// Build insights from ERR logs
	var insights []string
	insights = append(insights, fmt.Sprintf("Fetched %d log entries around event time", len(entries)))
	// Include up to 3 key error messages as insights
	for i, msg := range errMessages {
		if i >= 3 {
			break
		}
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		insights = append(insights, msg)
	}

	dataJSON, _ := json.Marshal(entries)

	return &providers.EventEvidence{
		Type:    providers.EventEvidenceTypeJson,
		Insight: insights,
		Data:    string(dataJSON),
		AdditionalInfo: map[string]string{
			"action_name": "Application Logs",
			"action_type": "app_logs",
		},
	}
}

// enrichBuildEvent fetches evidence for build-related events.
func enrichBuildEvent(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, appGUID string) []providers.EventEvidence {
	logger := ctx.GetLogger()
	var evidence []providers.EventEvidence

	// Fetch recent builds for this app (the failed build + context)
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/builds?app_guids=%s&per_page=5&order_by=-created_at", appGUID), "Recent Builds", "recent_builds"); e != nil {
		evidence = append(evidence, *e)
	}

	// Fetch app details for context
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/apps/%s", appGUID), "App Details", "app_details"); e != nil {
		evidence = append(evidence, *e)
	}

	// Recent deployments (shows deploy history around the failed build)
	if e := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/deployments?app_guids=%s&per_page=3&order_by=-created_at", appGUID), "Recent Deployments", "recent_deployments"); e != nil {
		evidence = append(evidence, *e)
	}

	// Staging logs from Log Cache (buildpack output)
	if client.logCacheURL != "" {
		if logEvidence := fetchRecentLogs(ctx, client, enrichCtx, appGUID, time.Now()); logEvidence != nil {
			evidence = append(evidence, *logEvidence)
		}
	}

	// Audit event timeline
	if timelineEvidence := fetchAuditEventTimeline(ctx, client, enrichCtx, appGUID, time.Now()); timelineEvidence != nil {
		evidence = append(evidence, *timelineEvidence)
	}

	logger.Debug("CloudFoundry: enriched build event", "app_guid", appGUID, "evidence_count", len(evidence))
	return evidence
}

// enrichServiceInstanceEvent fetches evidence for service-instance-related events.
func enrichServiceInstanceEvent(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, siGUID string) []providers.EventEvidence {
	logger := ctx.GetLogger()
	var evidence []providers.EventEvidence

	// Service instance details
	if ev := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/service_instances/%s", siGUID), "Service Instance Details", "service_instance_details"); ev != nil {
		evidence = append(evidence, *ev)
	}

	// Service plan details (offering, plan name, broker)
	if planEvidence := fetchServicePlanDetails(ctx, client, enrichCtx, siGUID); planEvidence != nil {
		evidence = append(evidence, *planEvidence)
	}

	// Bindings for this service instance (shows impacted apps)
	if ev := fetchAndMakeEvidence(ctx, client, enrichCtx, fmt.Sprintf("/v3/service_credential_bindings?service_instance_guids=%s", siGUID), "Service Bindings (Impacted Apps)", "service_bindings"); ev != nil {
		evidence = append(evidence, *ev)
	}

	// Audit event timeline for this service instance
	if timelineEvidence := fetchAuditEventTimeline(ctx, client, enrichCtx, siGUID, time.Now()); timelineEvidence != nil {
		evidence = append(evidence, *timelineEvidence)
	}

	logger.Debug("CloudFoundry: enriched service instance event", "si_guid", siGUID, "evidence_count", len(evidence))
	return evidence
}

// fetchProcessStats fetches process stats for an app and returns evidence items.
func fetchProcessStats(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, appGUID string) []providers.EventEvidence {
	logger := ctx.GetLogger()

	body, err := client.getWithContext(enrichCtx, fmt.Sprintf("/v3/apps/%s/processes", appGUID))
	if err != nil {
		logger.Warn("CloudFoundry: failed to fetch processes for enrichment", "app_guid", appGUID, "error", err)
		return nil
	}

	var processesResp struct {
		Resources []cfProcess `json:"resources"`
	}
	if err := json.Unmarshal(body, &processesResp); err != nil {
		logger.Warn("CloudFoundry: failed to parse processes for enrichment", "error", err)
		return nil
	}

	var evidence []providers.EventEvidence
	for _, process := range processesResp.Resources {
		statsBody, err := client.getWithContext(enrichCtx, fmt.Sprintf("/v3/processes/%s/stats", process.GUID))
		if err != nil {
			logger.Warn("CloudFoundry: failed to fetch process stats for enrichment", "process_guid", process.GUID, "error", err)
			continue
		}

		var stats cfProcessStats
		if err := json.Unmarshal(statsBody, &stats); err != nil {
			continue
		}

		// Build insights from stats
		var insights []string
		running, crashed, down := 0, 0, 0
		for _, inst := range stats.Resources {
			switch inst.State {
			case "RUNNING":
				running++
			case "CRASHED":
				crashed++
			case "DOWN":
				down++
			}
		}

		insights = append(insights, fmt.Sprintf("Process %s (%s): %d running, %d crashed, %d down of %d desired",
			process.GUID, process.Type, running, crashed, down, process.Instances))

		if crashed > 0 {
			insights = append(insights, fmt.Sprintf("WARNING: %d instances in CRASHED state", crashed))
		}
		if down > 0 {
			insights = append(insights, fmt.Sprintf("WARNING: %d instances DOWN", down))
		}

		dataJSON, _ := json.Marshal(stats)
		evidence = append(evidence, providers.EventEvidence{
			Type:    providers.EventEvidenceTypeJson,
			Insight: insights,
			Data:    string(dataJSON),
			AdditionalInfo: map[string]string{
				"action_name": fmt.Sprintf("Process Stats (%s)", process.Type),
				"action_type": "process_stats",
			},
		})
	}

	return evidence
}

// fetchAuditEventTimeline fetches recent audit events for a target resource GUID
// to provide a timeline of management actions around the event time.
func fetchAuditEventTimeline(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, targetGUID string, eventTime time.Time) *providers.EventEvidence {
	logger := ctx.GetLogger()

	startTime := eventTime.Add(-30 * time.Minute)
	path := fmt.Sprintf("/v3/audit_events?target_guids=%s&per_page=10&order_by=-created_at&created_ats[gte]=%s",
		targetGUID, startTime.Format(time.RFC3339))

	body, err := client.getWithContext(enrichCtx, path)
	if err != nil {
		logger.Debug("CloudFoundry: failed to fetch audit event timeline", "target_guid", targetGUID, "error", err)
		return nil
	}

	var resp struct {
		Resources []cfAuditEvent `json:"resources"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || len(resp.Resources) == 0 {
		return nil
	}

	// Build a summarized timeline
	type timelineEntry struct {
		Time      string `json:"time"`
		EventType string `json:"event_type"`
		Actor     string `json:"actor"`
		ActorType string `json:"actor_type"`
	}

	var entries []timelineEntry
	var insights []string
	for _, ev := range resp.Resources {
		entries = append(entries, timelineEntry{
			Time:      ev.CreatedAt.Format(time.RFC3339),
			EventType: ev.Type,
			Actor:     ev.Actor.Name,
			ActorType: ev.Actor.Type,
		})
	}
	insights = append(insights, fmt.Sprintf("%d recent management actions in the last 30 minutes", len(entries)))

	dataJSON, _ := json.Marshal(entries)

	return &providers.EventEvidence{
		Type:    providers.EventEvidenceTypeJson,
		Insight: insights,
		Data:    string(dataJSON),
		AdditionalInfo: map[string]string{
			"action_name": "Audit Event Timeline",
			"action_type": "audit_timeline",
		},
	}
}

// fetchServicePlanDetails fetches the service plan and offering details for a service instance.
func fetchServicePlanDetails(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, siGUID string) *providers.EventEvidence {
	logger := ctx.GetLogger()

	// First fetch the service instance to get the service plan GUID
	siBody, err := client.getWithContext(enrichCtx, fmt.Sprintf("/v3/service_instances/%s", siGUID))
	if err != nil {
		return nil
	}

	var si cfServiceInstance
	if err := json.Unmarshal(siBody, &si); err != nil {
		return nil
	}

	planGUID := si.Relations.ServicePlan.Data.GUID
	if planGUID == "" {
		return nil // user-provided service instances have no plan
	}

	// Fetch service plan (includes service offering reference)
	planBody, err := client.getWithContext(enrichCtx, fmt.Sprintf("/v3/service_plans/%s", planGUID))
	if err != nil {
		logger.Debug("CloudFoundry: failed to fetch service plan details", "plan_guid", planGUID, "error", err)
		return nil
	}

	return &providers.EventEvidence{
		Type:    providers.EventEvidenceTypeJson,
		Insight: []string{fmt.Sprintf("Service plan details for plan %s", planGUID)},
		Data:    string(planBody),
		AdditionalInfo: map[string]string{
			"action_name": "Service Plan Details",
			"action_type": "service_plan_details",
		},
	}
}

// fetchAndMakeEvidence fetches a CF API endpoint and wraps the response as EventEvidence.
// Returns nil if the fetch fails (graceful degradation).
func fetchAndMakeEvidence(ctx providers.CloudProviderContext, client *cfClient, enrichCtx context.Context, path string, actionName string, actionType string) *providers.EventEvidence {
	logger := ctx.GetLogger()

	body, err := client.getWithContext(enrichCtx, path)
	if err != nil {
		logger.Warn("CloudFoundry: enrichment fetch failed, skipping", "path", path, "error", err)
		return nil
	}

	return &providers.EventEvidence{
		Type:    providers.EventEvidenceTypeJson,
		Insight: []string{fmt.Sprintf("Fetched %s from CF API", actionName)},
		Data:    string(body),
		AdditionalInfo: map[string]string{
			"action_name": actionName,
			"action_type": actionType,
		},
	}
}
