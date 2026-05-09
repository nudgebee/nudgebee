package cloudfoundry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"sync"
	"time"
)

const (
	healthMemoryThreshold = 90.0 // percentage
	healthDiskThreshold   = 90.0 // percentage

	logErrorRateThreshold = 20 // ERR log count threshold in check window
	logCheckWindow        = 15 * time.Minute
	logCheckWorkerPool    = 5
)

// criticalLogPatterns maps pattern categories to their search strings.
var criticalLogPatterns = map[string][]string{
	"oom":          {"out of memory", "OOM", "SIGKILL", "exit status 137"},
	"connectivity": {"connection refused", "connection reset", "ECONNREFUSED"},
	"crash":        {"panic", "fatal", "segmentation fault", "SIGSEGV"},
	"timeout":      {"timeout", "deadline exceeded"},
}

// checkAppHealth polls process stats for all running apps and generates
// synthetic events for unhealthy conditions (crashed instances, high memory/disk).
func checkAppHealth(ctx providers.CloudProviderContext, client *cfClient) []providers.Event {
	logger := ctx.GetLogger()

	apps, err := getPaginated[cfApp](client, "/v3/apps?per_page=200")
	if err != nil {
		logger.Warn("CloudFoundry: health check failed to list apps", "error", err)
		return nil
	}

	var events []providers.Event
	now := time.Now()

	for _, app := range apps {
		if app.State != "STARTED" {
			continue
		}

		appEvents := checkSingleAppHealth(ctx, client, app, now)
		events = append(events, appEvents...)
	}

	if len(events) > 0 {
		logger.Info("CloudFoundry: health check generated synthetic events", "count", len(events))
	}

	return events
}

// checkSingleAppHealth checks processes/stats for one app and returns synthetic events.
func checkSingleAppHealth(ctx providers.CloudProviderContext, client *cfClient, app cfApp, now time.Time) []providers.Event {
	logger := ctx.GetLogger()

	body, err := client.get(fmt.Sprintf("/v3/apps/%s/processes", app.GUID))
	if err != nil {
		logger.Warn("CloudFoundry: health check failed to get processes", "app_guid", app.GUID, "error", err)
		return nil
	}

	var processesResp struct {
		Resources []cfProcess `json:"resources"`
	}
	if err := json.Unmarshal(body, &processesResp); err != nil {
		return nil
	}

	var events []providers.Event

	for _, process := range processesResp.Resources {
		statsBody, err := client.get(fmt.Sprintf("/v3/processes/%s/stats", process.GUID))
		if err != nil {
			logger.Warn("CloudFoundry: health check failed to get process stats", "process_guid", process.GUID, "error", err)
			continue
		}

		var stats cfProcessStats
		if err := json.Unmarshal(statsBody, &stats); err != nil {
			continue
		}

		if len(stats.Resources) == 0 {
			continue
		}

		// Check for crashed/down instances
		running, crashed, down := 0, 0, 0
		var totalMemPct, totalDiskPct float64
		runningWithMemQuota, runningWithDiskQuota := 0, 0

		for _, inst := range stats.Resources {
			switch inst.State {
			case "RUNNING":
				running++
				if inst.MemQuota > 0 {
					totalMemPct += (float64(inst.Usage.Mem) / float64(inst.MemQuota)) * 100
					runningWithMemQuota++
				}
				if inst.DiskQuota > 0 {
					totalDiskPct += (float64(inst.Usage.Disk) / float64(inst.DiskQuota)) * 100
					runningWithDiskQuota++
				}
			case "CRASHED":
				crashed++
			case "DOWN":
				down++
			}
		}

		// Instances down event
		if crashed > 0 || (down > 0 && running < process.Instances) {
			raw := map[string]any{
				"app_guid":          app.GUID,
				"app_name":          app.Name,
				"process_type":      process.Type,
				"desired_instances": process.Instances,
				"running":           running,
				"crashed":           crashed,
				"down":              down,
			}
			events = append(events, providers.Event{
				Title:               fmt.Sprintf("App %s: instances unhealthy (%d crashed, %d down)", app.Name, crashed, down),
				Description:         fmt.Sprintf("App %s process %s has %d/%d instances running (%d crashed, %d down)", app.Name, process.Type, running, process.Instances, crashed, down),
				EventName:           "cf.app.instances_down",
				Date:                now,
				EventSource:         "cloudfoundry",
				EventId:             fmt.Sprintf("cf-health-instances-%s-%s", app.GUID, process.GUID),
				EventStatus:         providers.EventStatusFiring,
				EventSeverity:       providers.EventSeverityHigh,
				ResourceType:        "app",
				ResourceId:          app.GUID,
				ResourceServiceName: ServiceNameApps,
				Raw:                 raw,
				Labels: map[string]string{
					"event_type":   "health",
					"process_type": process.Type,
				},
			})
		}

		// High memory event
		if runningWithMemQuota > 0 {
			avgMemPct := totalMemPct / float64(runningWithMemQuota)
			if avgMemPct > healthMemoryThreshold {
				raw := map[string]any{
					"app_guid":         app.GUID,
					"app_name":         app.Name,
					"process_type":     process.Type,
					"memory_usage_pct": fmt.Sprintf("%.1f%%", avgMemPct),
					"threshold":        fmt.Sprintf("%.0f%%", healthMemoryThreshold),
				}
				events = append(events, providers.Event{
					Title:               fmt.Sprintf("App %s: high memory usage (%.1f%%)", app.Name, avgMemPct),
					Description:         fmt.Sprintf("App %s process %s average memory usage is %.1f%%, exceeding %.0f%% threshold", app.Name, process.Type, avgMemPct, healthMemoryThreshold),
					EventName:           "cf.app.high_memory",
					Date:                now,
					EventSource:         "cloudfoundry",
					EventId:             fmt.Sprintf("cf-health-memory-%s-%s", app.GUID, process.GUID),
					EventStatus:         providers.EventStatusFiring,
					EventSeverity:       providers.EventSeverityMedium,
					ResourceType:        "app",
					ResourceId:          app.GUID,
					ResourceServiceName: ServiceNameApps,
					Raw:                 raw,
					Labels: map[string]string{
						"event_type":   "health",
						"process_type": process.Type,
					},
				})
			}
		}

		// High disk event
		if runningWithDiskQuota > 0 {
			avgDiskPct := totalDiskPct / float64(runningWithDiskQuota)
			if avgDiskPct > healthDiskThreshold {
				raw := map[string]any{
					"app_guid":       app.GUID,
					"app_name":       app.Name,
					"process_type":   process.Type,
					"disk_usage_pct": fmt.Sprintf("%.1f%%", avgDiskPct),
					"threshold":      fmt.Sprintf("%.0f%%", healthDiskThreshold),
				}
				events = append(events, providers.Event{
					Title:               fmt.Sprintf("App %s: high disk usage (%.1f%%)", app.Name, avgDiskPct),
					Description:         fmt.Sprintf("App %s process %s average disk usage is %.1f%%, exceeding %.0f%% threshold", app.Name, process.Type, avgDiskPct, healthDiskThreshold),
					EventName:           "cf.app.high_disk",
					Date:                now,
					EventSource:         "cloudfoundry",
					EventId:             fmt.Sprintf("cf-health-disk-%s-%s", app.GUID, process.GUID),
					EventStatus:         providers.EventStatusFiring,
					EventSeverity:       providers.EventSeverityMedium,
					ResourceType:        "app",
					ResourceId:          app.GUID,
					ResourceServiceName: ServiceNameApps,
					Raw:                 raw,
					Labels: map[string]string{
						"event_type":   "health",
						"process_type": process.Type,
					},
				})
			}
		}
	}

	return events
}

// checkAppLogErrors polls Log Cache for ERR-type logs across all STARTED apps
// and generates events for high error rates and critical error patterns.
func checkAppLogErrors(ctx providers.CloudProviderContext, client *cfClient) []providers.Event {
	logger := ctx.GetLogger()

	if client.logCacheURL == "" {
		logger.Debug("CloudFoundry: skipping log error check — Log Cache not available")
		return nil
	}

	apps, err := getPaginated[cfApp](client, "/v3/apps?per_page=200")
	if err != nil {
		logger.Warn("CloudFoundry: log error check failed to list apps", "error", err)
		return nil
	}

	// Filter to STARTED apps
	var startedApps []cfApp
	for _, app := range apps {
		if app.State == "STARTED" {
			startedApps = append(startedApps, app)
		}
	}

	if len(startedApps) == 0 {
		return nil
	}

	now := time.Now()
	startTime := now.Add(-logCheckWindow)

	type appResult struct {
		events []providers.Event
	}

	jobs := make(chan cfApp, len(startedApps))
	results := make(chan appResult, len(startedApps))

	var wg sync.WaitGroup
	for w := 0; w < logCheckWorkerPool; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for app := range jobs {
				appEvents := checkSingleAppLogErrors(ctx, client, app, startTime, now)
				results <- appResult{events: appEvents}
			}
		}()
	}

	for _, app := range startedApps {
		jobs <- app
	}
	close(jobs)

	wg.Wait()
	close(results)

	var events []providers.Event
	for r := range results {
		events = append(events, r.events...)
	}

	if len(events) > 0 {
		logger.Info("CloudFoundry: log error check generated synthetic events", "count", len(events))
	}

	return events
}

// checkSingleAppLogErrors checks Log Cache ERR logs for a single app.
func checkSingleAppLogErrors(ctx providers.CloudProviderContext, client *cfClient, app cfApp, startTime, now time.Time) []providers.Event {
	logger := ctx.GetLogger()

	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	envelopes, err := fetchLogsFromLogCache(ctx, client, reqCtx, app.GUID, startTime, now, 100)
	if err != nil {
		logger.Debug("CloudFoundry: log error check failed for app", "app_guid", app.GUID, "error", err)
		return nil
	}

	// Filter to ERR-type envelopes and decode payloads
	var errCount int
	var errPayloads []string
	for _, env := range envelopes {
		if env.Log == nil || env.Log.Type != "ERR" {
			continue
		}
		errCount++

		payload, decErr := base64.StdEncoding.DecodeString(env.Log.Payload)
		if decErr != nil {
			payload = []byte(env.Log.Payload)
		}
		errPayloads = append(errPayloads, strings.TrimSpace(string(payload)))
	}

	if errCount == 0 {
		return nil
	}

	var events []providers.Event

	// Rate check
	if errCount > logErrorRateThreshold {
		var sampleMessages []string
		for i, msg := range errPayloads {
			if i >= 3 {
				break
			}
			if len(msg) > 200 {
				msg = msg[:200] + "..."
			}
			sampleMessages = append(sampleMessages, msg)
		}

		raw := map[string]any{
			"app_guid":        app.GUID,
			"app_name":        app.Name,
			"error_count":     errCount,
			"threshold":       logErrorRateThreshold,
			"window_minutes":  int(logCheckWindow.Minutes()),
			"sample_messages": sampleMessages,
		}
		events = append(events, providers.Event{
			Title:               fmt.Sprintf("App %s: high error rate (%d errors in %d min)", app.Name, errCount, int(logCheckWindow.Minutes())),
			Description:         fmt.Sprintf("App %s has %d ERR log entries in the last %d minutes, exceeding threshold of %d", app.Name, errCount, int(logCheckWindow.Minutes()), logErrorRateThreshold),
			EventName:           "cf.app.high_error_rate",
			Date:                now,
			EventSource:         "cloudfoundry",
			EventId:             fmt.Sprintf("cf-log-error-rate-%s", app.GUID),
			EventStatus:         providers.EventStatusFiring,
			EventSeverity:       providers.EventSeverityMedium,
			ResourceType:        "app",
			ResourceId:          app.GUID,
			ResourceServiceName: ServiceNameApps,
			Raw:                 raw,
			Labels: map[string]string{
				"event_type": "log_error",
			},
		})
	}

	// Pattern check — scan ERR payloads for critical patterns
	matchedCategories := map[string][]string{} // category → matching log lines
	for _, msg := range errPayloads {
		lowerMsg := strings.ToLower(msg)
		for category, patterns := range criticalLogPatterns {
			for _, pattern := range patterns {
				if strings.Contains(lowerMsg, strings.ToLower(pattern)) {
					if len(matchedCategories[category]) < 3 {
						truncated := msg
						if len(truncated) > 200 {
							truncated = truncated[:200] + "..."
						}
						matchedCategories[category] = append(matchedCategories[category], truncated)
					}
					break // one match per category per message is enough
				}
			}
		}
	}

	for category, matchingLines := range matchedCategories {
		raw := map[string]any{
			"app_guid":       app.GUID,
			"app_name":       app.Name,
			"pattern":        category,
			"matching_lines": matchingLines,
		}
		events = append(events, providers.Event{
			Title:               fmt.Sprintf("App %s: critical error detected (%s)", app.Name, category),
			Description:         fmt.Sprintf("App %s has log entries matching critical pattern '%s'", app.Name, category),
			EventName:           "cf.app.critical_error",
			Date:                now,
			EventSource:         "cloudfoundry",
			EventId:             fmt.Sprintf("cf-log-critical-%s-%s", app.GUID, category),
			EventStatus:         providers.EventStatusFiring,
			EventSeverity:       providers.EventSeverityHigh,
			ResourceType:        "app",
			ResourceId:          app.GUID,
			ResourceServiceName: ServiceNameApps,
			Raw:                 raw,
			Labels: map[string]string{
				"event_type":       "log_error",
				"pattern_category": category,
			},
		})
	}

	return events
}

// checkBuildFailures fetches recent failed builds and generates events for each.
func checkBuildFailures(ctx providers.CloudProviderContext, client *cfClient, query providers.ListEventRequest) []providers.Event {
	logger := ctx.GetLogger()

	builds, err := getPaginated[cfBuild](client, "/v3/builds?per_page=50&order_by=-created_at&states=FAILED")
	if err != nil {
		logger.Warn("CloudFoundry: failed to fetch builds for failure check", "error", err)
		return nil
	}

	// Determine time window
	now := time.Now()
	windowStart := now.Add(-24 * time.Hour)
	if query.StartDate != nil {
		windowStart = *query.StartDate
	}
	windowEnd := now
	if query.EndDate != nil {
		windowEnd = *query.EndDate
	}

	var events []providers.Event
	for _, build := range builds {
		if build.CreatedAt.Before(windowStart) || build.CreatedAt.After(windowEnd) {
			continue
		}

		errorMsg := "unknown error"
		if build.Error != nil && *build.Error != "" {
			errorMsg = *build.Error
		}

		appGUID := build.Relations.App.Data.GUID
		raw := map[string]any{
			"build_guid":   build.GUID,
			"app_guid":     appGUID,
			"error":        errorMsg,
			"lifecycle":    build.Lifecycle.Type,
			"created_at":   build.CreatedAt.Format(time.RFC3339),
			"created_by":   build.CreatedBy.Name,
			"package_guid": build.Package.GUID,
		}

		events = append(events, providers.Event{
			Title:               fmt.Sprintf("Build failed for app %s", appGUID),
			Description:         fmt.Sprintf("Build %s failed: %s", build.GUID, errorMsg),
			EventName:           "cf.build.failed",
			Date:                build.CreatedAt,
			EventSource:         "cloudfoundry",
			EventId:             fmt.Sprintf("cf-build-failed-%s", build.GUID),
			EventStatus:         providers.EventStatusClosed,
			EventSeverity:       providers.EventSeverityHigh,
			ResourceType:        "build",
			ResourceId:          appGUID,
			ResourceServiceName: ServiceNameApps,
			Raw:                 raw,
			Labels: map[string]string{
				"event_type": "build_failure",
				"build_guid": build.GUID,
			},
		})
	}

	if len(events) > 0 {
		logger.Info("CloudFoundry: build failure check generated events", "count", len(events))
	}

	return events
}

// checkTaskFailures fetches recent failed tasks and generates events for each.
func checkTaskFailures(ctx providers.CloudProviderContext, client *cfClient, query providers.ListEventRequest) []providers.Event {
	logger := ctx.GetLogger()

	tasks, err := getPaginated[cfTask](client, "/v3/tasks?per_page=50&order_by=-updated_at&states=FAILED")
	if err != nil {
		logger.Warn("CloudFoundry: failed to fetch tasks for failure check", "error", err)
		return nil
	}

	// Determine time window
	now := time.Now()
	windowStart := now.Add(-24 * time.Hour)
	if query.StartDate != nil {
		windowStart = *query.StartDate
	}
	windowEnd := now
	if query.EndDate != nil {
		windowEnd = *query.EndDate
	}

	var events []providers.Event
	for _, task := range tasks {
		if task.UpdatedAt.Before(windowStart) || task.UpdatedAt.After(windowEnd) {
			continue
		}

		appGUID := task.Relations.App.Data.GUID
		failureReason := task.Result.FailureReason
		if failureReason == "" {
			failureReason = "unknown failure"
		}

		raw := map[string]any{
			"task_guid":      task.GUID,
			"task_name":      task.Name,
			"app_guid":       appGUID,
			"failure_reason": failureReason,
			"command":        task.Command,
			"updated_at":     task.UpdatedAt.Format(time.RFC3339),
			"sequence_id":    task.SequenceID,
		}

		events = append(events, providers.Event{
			Title:               fmt.Sprintf("Task '%s' failed for app %s", task.Name, appGUID),
			Description:         fmt.Sprintf("Task %s (%s) failed: %s", task.GUID, task.Name, failureReason),
			EventName:           "cf.task.failed",
			Date:                task.UpdatedAt,
			EventSource:         "cloudfoundry",
			EventId:             fmt.Sprintf("cf-task-failed-%s", task.GUID),
			EventStatus:         providers.EventStatusClosed,
			EventSeverity:       providers.EventSeverityMedium,
			ResourceType:        "app",
			ResourceId:          appGUID,
			ResourceServiceName: ServiceNameApps,
			Raw:                 raw,
			Labels: map[string]string{
				"event_type": "task_failure",
				"task_guid":  task.GUID,
				"task_name":  task.Name,
			},
		})
	}

	if len(events) > 0 {
		logger.Info("CloudFoundry: task failure check generated events", "count", len(events))
	}

	return events
}

// checkServiceInstanceFailures fetches service instances with failed last operations
// and generates events for each.
func checkServiceInstanceFailures(ctx providers.CloudProviderContext, client *cfClient) []providers.Event {
	logger := ctx.GetLogger()

	instances, err := getPaginated[cfServiceInstance](client, "/v3/service_instances?per_page=200")
	if err != nil {
		logger.Warn("CloudFoundry: failed to fetch service instances for failure check", "error", err)
		return nil
	}

	now := time.Now()
	var events []providers.Event

	for _, si := range instances {
		if si.LastOperation.State != "failed" {
			continue
		}

		raw := map[string]any{
			"si_guid":               si.GUID,
			"si_name":               si.Name,
			"si_type":               si.Type,
			"operation_type":        si.LastOperation.Type,
			"operation_state":       si.LastOperation.State,
			"operation_description": si.LastOperation.Description,
			"operation_updated_at":  si.LastOperation.UpdatedAt.Format(time.RFC3339),
			"space_guid":            si.Relations.Space.Data.GUID,
		}

		events = append(events, providers.Event{
			Title:               fmt.Sprintf("Service instance '%s' %s failed", si.Name, si.LastOperation.Type),
			Description:         fmt.Sprintf("Service instance %s (%s) %s operation failed: %s", si.Name, si.GUID, si.LastOperation.Type, si.LastOperation.Description),
			EventName:           "cf.service_instance.operation_failed",
			Date:                now,
			EventSource:         "cloudfoundry",
			EventId:             fmt.Sprintf("cf-si-op-failed-%s-%d", si.GUID, si.LastOperation.UpdatedAt.Unix()),
			EventStatus:         providers.EventStatusFiring,
			EventSeverity:       providers.EventSeverityHigh,
			ResourceType:        "service_instance",
			ResourceId:          si.GUID,
			ResourceServiceName: ServiceNameServiceInstances,
			Raw:                 raw,
			Labels: map[string]string{
				"event_type":     "service_instance_failure",
				"operation_type": si.LastOperation.Type,
			},
		})
	}

	if len(events) > 0 {
		logger.Info("CloudFoundry: service instance failure check generated events", "count", len(events))
	}

	return events
}
