package traces

import (
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	playbooks.RegisterAction("traces_dependency_map", &tracesDependencyMapAction{})
}

// TracesDependencyMapResponse embeds the service map data directly to match expected format
type TracesDependencyMapResponse struct {
	// Embed all the expected fields directly in the struct
	Data              []any  `json:"data"`
	Type              string `json:"type"`
	Success           bool   `json:"success"`
	StartTime         string `json:"start_time"`
	EndTime           string `json:"end_time"`
	WorkloadName      string `json:"workload_name"`
	WorkloadNamespace string `json:"workload_namespace"`
	RequestID         string `json:"request_id"`

	// Store additional info separately for interface compliance (but don't serialize)
	additionalInfo map[string]any                            `json:"-"`
	insight        []playbooks.PlaybookActionResponseInsight `json:"-"`
	metadata       map[string]any                            `json:"-"`
}

func (r *TracesDependencyMapResponse) GetData() any {
	return r.Data
}

func (r *TracesDependencyMapResponse) GetAdditionalInfo() map[string]any {
	return r.additionalInfo
}

func (r *TracesDependencyMapResponse) GetInsights() []playbooks.PlaybookActionResponseInsight {
	return r.insight
}

func (r *TracesDependencyMapResponse) GetFormatName() string {
	return "service_map"
}

// ExtractLabels implements PlaybookActionResponseLabelExtractor interface
// Exposes upstream services data for use in subsequent workflow actions
func (r *TracesDependencyMapResponse) ExtractLabels() map[string]any {
	if r.additionalInfo == nil {
		return map[string]any{}
	}

	labels := map[string]any{
		"upstream_services":        r.additionalInfo["upstream_services"],        // []string - list of service names
		"upstream_services_detail": r.additionalInfo["upstream_services_detail"], // []map[string]any - contains service_name, namespace, language
		"target_service":           r.additionalInfo["target_service"],           // string - the target service name
	}

	// Extract individual language labels keyed by service name for easy access in auto-execute actions
	// e.g., "language_global_worker": "ruby", "language_api_server": "nodejs"
	if upstreamDetails, ok := r.additionalInfo["upstream_services_detail"].([]map[string]any); ok {
		for _, detail := range upstreamDetails {
			if serviceName, ok := detail["service_name"].(string); ok && serviceName != "" {
				if lang, ok := detail["language"].(string); ok && lang != "" {
					// Replace hyphens and dots with underscores to make valid label keys
					safeServiceName := strings.NewReplacer("-", "_", ".", "_").Replace(serviceName)
					labels[fmt.Sprintf("language_%s", safeServiceName)] = lang
				}
			}
		}
	}

	return labels
}

// prometheusEnricherAction is the action for prometheus_enricher.

type tracesDependencyMapAction struct{}
type tracesDependencyMapParams struct {
	ServiceName    string        `json:"service_name,omitempty"`
	StartTime      string        `json:"start_time,omitempty"`
	EndTime        string        `json:"end_time,omitempty"`
	Duration       string        `json:"duration,omitempty"` // e.g., "30m", "1h", "2h30m"
	Namespace      string        `json:"namespace,omitempty"`
	LabelFilter    []LabelFilter `json:"label_filter,omitempty"`
	ExcludeFilters []LabelFilter `json:"exclude_filters,omitempty"` // Exclude external services matching these filters
	UpstreamOnly   bool          `json:"upstream_only,omitempty"`   // If true, only show upstream services (callers) of the target
}

func (a *tracesDependencyMapAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params tracesDependencyMapParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	// Parse start and end times
	var startTime, endTime time.Time

	// If duration is specified, calculate start/end times from current time
	if params.Duration != "" {
		duration, err := time.ParseDuration(params.Duration)
		if err != nil {
			return nil, fmt.Errorf("invalid duration format: %v", err)
		}

		endTime = time.Now()
		startTime = endTime.Add(-duration)
	} else {
		// Use explicit start/end times or defaults
		if params.StartTime != "" {
			startTime, err = time.Parse(time.RFC3339, params.StartTime)
			if err != nil {
				return nil, fmt.Errorf("invalid start_time format: %v", err)
			}
		} else {
			startTime = time.Now().Add(-10 * time.Minute)
		}

		if params.EndTime != "" {
			endTime, err = time.Parse(time.RFC3339, params.EndTime)
			if err != nil {
				return nil, fmt.Errorf("invalid end_time format: %v", err)
			}
		} else {
			endTime = time.Now()
		}
	}

	// Use event times if available
	if ctx.GetEvent().StartedAt != nil {
		startTime = *ctx.GetEvent().StartedAt
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = *ctx.GetEvent().EndedAt
	}

	// Create a basic request context for the service map builder
	// Note: This is a simplified context since we don't have full user context in playbook actions
	requestContext := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	slog.Info("tracesDependencyMapAction: fetching trace data",
		"account_id", ctx.GetAccountId(), "service", params.ServiceName)
	// Use the existing FetchTracesAndBuildServiceMap function
	// The batching is now handled automatically at the agent_warehouse level for Chronosphere
	traceQueryParams := TraceQueryParams{
		WorkloadName:      params.ServiceName,
		WorkloadNamespace: params.Namespace,
		StartTime:         startTime,
		EndTime:           endTime,
		AccountID:         ctx.GetAccountId(),
		LabelFilters:      params.LabelFilter,
		ExcludeFilters:    params.ExcludeFilters,
		UpstreamOnly:      params.UpstreamOnly,
	}

	serviceMap, err := FetchTracesAndBuildServiceMap(requestContext, traceQueryParams)
	if err != nil {
		return nil, fmt.Errorf("failed to build service map: %v", err)
	}

	// Apply upstream-only filtering if requested
	if params.UpstreamOnly {
		targetService := params.ServiceName

		// Auto-detect target service from http.host label filter if service_name not provided
		if targetService == "" {
			for _, filter := range params.LabelFilter {
				if filter.Key == "http.host" {
					targetService = strings.Trim(filter.Value, ".*%")
					targetService = strings.TrimSpace(targetService)
					slog.Info("Auto-detected target service from http.host filter", "target_service", targetService)
					break
				}
			}
		}

		if targetService != "" {
			serviceMap = filterToUpstreamServices(serviceMap, targetService)
			slog.Info("Applied upstream-only filter", "target_service", targetService, "filtered_apps_count", len(serviceMap.Applications))
		} else {
			slog.Warn("upstream_only=true but no target service found (neither service_name nor http.host filter provided)")
		}
	}

	// Transform service map to the required response format (strict expectation match)
	responseData := map[string]any{
		"data":               serviceMap.Applications,
		"type":               "service_map",
		"success":            true,
		"start_time":         startTime.UTC().Format(time.RFC3339),
		"end_time":           endTime.UTC().Format(time.RFC3339),
		"workload_name":      params.ServiceName,
		"workload_namespace": params.Namespace,
		"request_id":         fmt.Sprintf("%d", time.Now().UnixNano()),
	}

	// Extract insights from the service map response
	insights := a.extractServiceMapInsights(responseData, params.ServiceName)

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"start_time":           startTime.UTC().Format(time.RFC3339),
		"end_time":             endTime.UTC().Format(time.RFC3339),
		"service_name":         params.ServiceName,
		"namespace":            params.Namespace,
	}

	// Extract upstream services for use in subsequent metric queries
	upstreamServices := []map[string]any{}
	upstreamServiceNames := []string{}

	// Find the target service in the map
	targetServiceName := params.ServiceName
	if targetServiceName == "" && len(params.LabelFilter) > 0 {
		for _, filter := range params.LabelFilter {
			if filter.Key == "http.host" {
				targetServiceName = strings.Trim(filter.Value, ".*%")
				targetServiceName = strings.TrimSpace(targetServiceName)
				break
			}
		}
	}

	// Create a map for quick lookup of service applications by name
	serviceAppMap := make(map[string]ServiceApplication)
	for _, app := range serviceMap.Applications {
		serviceAppMap[app.Id.Name] = app
	}

	// Helper function to extract language from a service application
	extractLanguage := func(app ServiceApplication) string {
		// Priority 1: Use Type array (detected application type)
		if len(app.Type) > 0 && app.Type[0] != "" {
			return app.Type[0]
		}
		// Priority 2: Check telemetry.sdk.language in Labels
		if lang, ok := app.Labels["telemetry.sdk.language"]; ok && lang != "" {
			return lang
		}
		// Priority 3: Check process.runtime.name in Labels
		if runtime, ok := app.Labels["process.runtime.name"]; ok && runtime != "" {
			return runtime
		}
		return "" // Unknown language
	}

	for _, app := range serviceMap.Applications {
		if app.Id.Name == targetServiceName {
			// Extract downstream services (services calling the target)
			for _, downstream := range app.Downstreams {
				upstreamServiceNames = append(upstreamServiceNames, downstream.Id.Name)

				// Look up the actual service application to get language info
				language := ""
				if downstreamApp, found := serviceAppMap[downstream.Id.Name]; found {
					language = extractLanguage(downstreamApp)
				}

				upstreamServices = append(upstreamServices, map[string]any{
					"service_name": downstream.Id.Name,
					"namespace":    downstream.Id.Namespace,
					"language":     language,
				})
			}
			break
		}
	}

	additionalInfo := map[string]any{
		"title":                    fmt.Sprintf("Traces Dependency Map for %s", params.ServiceName),
		"action_name":              "traces_dependency_map",
		"service_name":             params.ServiceName,
		"namespace":                params.Namespace,
		"upstream_services":        upstreamServiceNames, // List of service names for iteration
		"upstream_services_detail": upstreamServices,     // Contains service_name, namespace, language for each upstream service
		"target_service":           targetServiceName,
	}

	// Extract fields from responseData and create properly structured response
	data, _ := responseData["data"].([]ServiceApplication)

	// Convert []ServiceApplication to []any for JSON serialization
	dataAny := make([]any, len(data))
	for i, app := range data {
		dataAny[i] = app
	}

	return &TracesDependencyMapResponse{
		Data:              dataAny,
		Type:              responseData["type"].(string),
		Success:           responseData["success"].(bool),
		StartTime:         responseData["start_time"].(string),
		EndTime:           responseData["end_time"].(string),
		WorkloadName:      responseData["workload_name"].(string),
		WorkloadNamespace: responseData["workload_namespace"].(string),
		RequestID:         responseData["request_id"].(string),
		additionalInfo:    additionalInfo,
		insight:           insights,
		metadata:          metadata,
	}, nil
}

// Note: All helper methods removed - now using application.FetchTracesAndBuildServiceMap

func (a *tracesDependencyMapAction) extractServiceMapInsights(serviceMapData map[string]any, serviceName string) []playbooks.PlaybookActionResponseInsight {
	insights := []playbooks.PlaybookActionResponseInsight{}

	// Check if the service map has any data
	if len(serviceMapData) == 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("No service map data found for service %s", serviceName),
			Severity: "High",
		})
		return insights
	}

	// Look for application/service data in the new format
	if applicationsData, ok := serviceMapData["data"].([]ServiceApplication); ok {
		unhealthyCount := 0
		totalCount := len(applicationsData)
		totalConnections := 0
		errorConnections := 0
		exceptionsFound := []string{}

		for _, app := range applicationsData {
			// Check if this application is unhealthy
			if !app.IsHealthy {
				unhealthyCount++
			}

			// Count connections and check for high error rates in upstreams
			for _, upstream := range app.Upstreams {
				totalConnections++
				if upstream.FailureCount > 0 && upstream.RequestCount > 0 {
					errorRate := (upstream.FailureCount / upstream.RequestCount) * 100
					if errorRate > 5.0 { // More than 5% error rate
						errorConnections++
					}
				}
			}

			// Look for exceptions by checking upstream/downstream connections for error indicators
			// Since trace spans contain the exception information in their attributes
			for _, upstream := range app.Upstreams {
				// Check if we have error status codes that might indicate exceptions
				if upstream.Status >= 400 && upstream.Status < 600 {
					// This indicates an error response that might include exception details
					exceptionsFound = append(exceptionsFound, fmt.Sprintf("Service %s: HTTP %d errors detected in upstream connections", app.Id.Name, upstream.Status))
				}
			}

			for _, downstream := range app.Downstreams {
				// Check downstream error rates
				if downstream.Status >= 400 && downstream.Status < 600 {
					exceptionsFound = append(exceptionsFound, fmt.Sprintf("Service %s: HTTP %d errors detected in downstream connections", app.Id.Name, downstream.Status))
				}
			}
		}

		if unhealthyCount > 0 {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("Found %d unhealthy services out of %d total services in service map", unhealthyCount, totalCount),
				Severity: "Critical",
			})
		}

		if totalCount == 0 {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("No services found in service map for service %s", serviceName),
				Severity: "High",
			})
		}

		if errorConnections > 0 {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("Found %d connections with high error rates out of %d total connections", errorConnections, totalConnections),
				Severity: "High",
			})
		}

		// Add exception insights
		if len(exceptionsFound) > 0 {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("Detected %d exception(s) in traces: %s", len(exceptionsFound), strings.Join(exceptionsFound, "; ")),
				Severity: "Critical",
			})
		}
	}

	return insights
}
