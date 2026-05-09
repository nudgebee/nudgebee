package traces

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/observability"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"
)

type TraceServiceMapBuilder struct {
	spans          []TraceSpan
	config         *ServiceMapConfig
	excludeFilters []LabelFilter // Filters to exclude external services (e.g., redis, kafka)
}

func NewTraceServiceMapBuilder() *TraceServiceMapBuilder {
	return &TraceServiceMapBuilder{
		spans:          make([]TraceSpan, 0),
		config:         DefaultServiceMapConfig(),
		excludeFilters: nil,
	}
}

func NewTraceServiceMapBuilderWithConfig(config *ServiceMapConfig) *TraceServiceMapBuilder {
	return &TraceServiceMapBuilder{
		spans:          make([]TraceSpan, 0),
		config:         config,
		excludeFilters: nil,
	}
}

// SetExcludeFilters configures filters to exclude external services from the service map
func (t *TraceServiceMapBuilder) SetExcludeFilters(filters []LabelFilter) {
	t.excludeFilters = filters
}

func (t *TraceServiceMapBuilder) AddSpans(spans []TraceSpan) {
	t.spans = append(t.spans, spans...)
}

func (t *TraceServiceMapBuilder) LoadSpansFromJSON(jsonData []byte) error {
	var response struct {
		Rows []TraceSpan `json:"rows"`
	}

	err := json.Unmarshal(jsonData, &response)
	if err != nil {
		return fmt.Errorf("failed to unmarshal trace data: %w", err)
	}

	t.spans = response.Rows
	return nil
}

func (t *TraceServiceMapBuilder) LoadSpansFromRows(requestContext *security.RequestContext, rows []common.OpenTelemetryTrace, params TraceQueryParams) error {
	spans := make([]TraceSpan, 0, len(rows))
	errorCount := 0

	for _, row := range rows {
		var span TraceSpan
		span.AccountID = params.AccountID
		span.DestinationName = row.DestinationName
		span.DestinationWorkloadName = row.DestinationWorkload
		span.DestinationWorkloadNamespace = row.DestinationNamespace
		span.DurationNs = float64(row.DurationNs)
		span.Headers = row.Headers
		span.HTTPResponse = row.HTTPResponse
		span.HTTPStatusCode = row.HTTPStatusCode
		span.ParentSpanID = row.ParentSpanID
		span.RequestPayload = row.RequestPayload
		span.Resource = row.Resource
		span.SpanID = row.SpanID
		span.SpanName = row.SpanName
		span.SpanAttributes = row.SpanAttributes
		span.StatusCode = row.StatusCode
		span.TenantID = requestContext.GetTraceId()
		span.Timestamp = row.Timestamp
		span.TraceID = row.TraceID
		span.TraceSource = row.TraceSource
		span.WorkloadName = row.WorkloadName
		span.WorkloadNamespace = row.WorkloadNamespace
		spans = append(spans, span)
	}

	slog.Info("LoadSpansFromRows completed", "total_rows", len(rows), "parsed_spans", len(spans), "parsing_errors", errorCount)
	t.spans = spans
	return nil
}

func (t *TraceServiceMapBuilder) BuildServiceMap() (*ServiceMap, error) {
	return t.BuildServiceMapWithTimeWindow(time.Time{}, time.Time{})
}

// ParsedSpan holds a pre-parsed version of TraceSpan to avoid repeated parsing.
type ParsedSpan struct {
	Span        TraceSpan
	ParsedTime  time.Time
	ParsedAttrs ParsedSpanAttributes
}

func (t *TraceServiceMapBuilder) BuildServiceMapWithTimeWindow(queryStartTime, queryEndTime time.Time) (*ServiceMap, error) {
	if len(t.spans) == 0 {
		return &ServiceMap{
			Applications: make([]ServiceApplication, 0),
			GeneratedAt:  time.Now(),
		}, nil
	}
	Labels := []string{}
	keySet := make(map[string]bool) // Track unique keys
	for i := range t.spans {
		sp := t.spans[i]
		for key := range sp.SpanAttributes {
			if !keySet[key] {
				keySet[key] = true
				Labels = append(Labels, key)
			}
		}
	}

	// ---- Preprocess: parse timestamps and attributes once ----
	parsedSpans := make([]ParsedSpan, 0, len(t.spans))
	byTrace := make(map[string][]*ParsedSpan, len(t.spans))
	bySpanID := make(map[string]*ParsedSpan, len(t.spans))
	parentMap := make(map[string][]*ParsedSpan, len(t.spans)) // parent_span_id -> children

	var earliestTime, latestTime time.Time

	for i := range t.spans {
		s := t.spans[i]
		// Parse time once
		var pt time.Time
		if ts := s.Timestamp; ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				pt = parsed
				if earliestTime.IsZero() || parsed.Before(earliestTime) {
					earliestTime = parsed
				}
				if latestTime.IsZero() || parsed.After(latestTime) {
					latestTime = parsed
				}
			}
		}

		// Parse attributes once (ignore error same as original behavior)
		parsedAttrs, _ := t.parseSpanAttributes(s.SpanAttributes)

		ps := ParsedSpan{
			Span:        s,
			ParsedTime:  pt,
			ParsedAttrs: *parsedAttrs,
		}

		parsedSpans = append(parsedSpans, ps)
		// store pointers for quick lookup
		psp := &parsedSpans[len(parsedSpans)-1]
		bySpanID[psp.Span.SpanID] = psp
		byTrace[psp.Span.TraceID] = append(byTrace[psp.Span.TraceID], psp)
		parentMap[psp.Span.ParentSpanID] = append(parentMap[psp.Span.ParentSpanID], psp)
	}

	// If query window explicitly provided, use that instead of derived times
	if !queryStartTime.IsZero() && !queryEndTime.IsZero() {
		earliestTime = queryStartTime
		latestTime = queryEndTime
	}

	// ---- Duration calculation (same logic as original) ----
	durationMinutes := t.config.DefaultFallbackDuration
	if !earliestTime.IsZero() && !latestTime.IsZero() {
		duration := latestTime.Sub(earliestTime)
		if dmin := duration.Minutes(); dmin > 0 {
			durationMinutes = dmin
		}
	}

	// ---- Build initial stats & dependency maps (single pass over parsed spans) ----
	serviceStats := make(map[string]*serviceMetrics)
	externalServices := make(map[string]*ExternalServiceInfo)
	dependencyMap := make(map[string]*ServiceDependency)

	// traceMap remains useful for other lookups; use prebuilt byTrace
	traceMap := make(map[string][]TraceSpan)
	for traceID, psList := range byTrace {
		arr := make([]TraceSpan, 0)
		for _, p := range psList {
			arr = append(arr, p.Span)
		}
		traceMap[traceID] = arr
	}

	// Helper to get or create serviceMetrics fast
	getOrCreateService := func(serviceName string, span TraceSpan, attrs *SpanAttributes) *serviceMetrics {
		if s, ok := serviceStats[serviceName]; ok {
			return s
		}
		telemetryLabels := t.extractTelemetryLabels(span, attrs)
		s := &serviceMetrics{
			ServiceName:      serviceName,
			Namespace:        span.WorkloadNamespace,
			Environment:      attrs.DeploymentEnv,
			CallCount:        0,
			ErrorCount:       0,
			TotalDuration:    0,
			TelemetryLabels:  telemetryLabels,
			ApplicationTypes: make(map[string]bool),
		}
		serviceStats[serviceName] = s
		return s
	}

	// First pass: populate serviceStats counts and basic deps from explicit destination_name
	for i := range parsedSpans {
		p := &parsedSpans[i]
		span := p.Span
		attrs := p.ParsedAttrs.Structured
		rawAttrs := p.ParsedAttrs.Raw

		// determine serviceName same as original logic
		serviceName := attrs.ServiceName
		if serviceName == "" {
			serviceName = span.WorkloadName
		}
		if serviceName == "" {
			if span.DestinationName != "" {
				serviceName = span.DestinationName
			} else {
				// skip like original
				slog.Info("Skipping span with no identifiable service name", "span_id", span.SpanID, "trace_id", span.TraceID, "attributes", slog.AnyValue(rawAttrs), "workload_name", span.WorkloadName)
				continue
			}
		}

		// Extract workload name from k8s attributes to avoid pod-level applications
		// Priority order: deployment > statefulset > daemonset > replicaset > pod name extraction
		if deploymentName, exists := rawAttrs["k8s.deployment.name"]; exists && deploymentName != "" {
			serviceName = deploymentName
		} else if statefulsetName, exists := rawAttrs["k8s.statefulset.name"]; exists && statefulsetName != "" {
			serviceName = statefulsetName
		} else if daemonsetName, exists := rawAttrs["k8s.daemonset.name"]; exists && daemonsetName != "" {
			serviceName = daemonsetName
		} else if replicasetName, exists := rawAttrs["k8s.replicaset.name"]; exists && replicasetName != "" {
			serviceName = replicasetName
		} else if podName, exists := rawAttrs["k8s.pod.name"]; exists && podName != "" && podName == serviceName {
			// Fallback: extract workload from pod name using regex
			serviceName = extractWorkloadFromPodName(serviceName)
		}

		duration := span.DurationNs
		if duration == 0 {
			duration = 0
		}

		isError := t.isErrorSpan(span, attrs)

		stats := getOrCreateService(serviceName, span, attrs)

		// merge telemetry labels opportunistically (cheap check first)
		if addLabels := t.extractTelemetryLabels(span, attrs); len(addLabels) > 0 {
			// only add missing keys to avoid extra allocations
			for k, v := range addLabels {
				if _, ok := stats.TelemetryLabels[k]; !ok {
					stats.TelemetryLabels[k] = v
				}
			}
		}

		// application type detection when span is the source (same logic)
		isServiceSource := (attrs.ServiceName != "" && attrs.ServiceName == serviceName) ||
			(attrs.ServiceName == "" && span.WorkloadName == serviceName)
		if isServiceSource {
			appType := t.detectApplicationType(span, attrs, rawAttrs, stats.TelemetryLabels)
			if appType != "" {
				stats.ApplicationTypes[appType] = true
			}
		}

		stats.CallCount++
		stats.TotalDuration += duration
		if isError {
			stats.ErrorCount++
		}

		// PRIORITY 1: Check for explicit caller from custom attributes (most accurate)
		// This handles customer-specific attributes like p44.caller without hardcoding schemas
		if caller, found := t.detectDependencyFromCustomAttributes(span, attrs); found {
			targetService := serviceName
			if targetService != "" && caller != targetService {
				depKey := caller + "->" + targetService
				if dep, exists := dependencyMap[depKey]; exists {
					t.updateDependency(dep, span, attrs, duration, isError)
				} else {
					newDep := t.createNewDependency(caller, targetService, span, attrs, duration, isError)
					newDep.DependencyType = "custom_caller_attribute"
					dependencyMap[depKey] = newDep
					// Note: Don't track as external service - this is an explicit internal service call
				}
			}
		}

		// PRIORITY 2: handle explicit destination_name dependency (fast path)
		if span.DestinationName != "" && span.DestinationName != serviceName {
			// Check if this is a consumer operation (messaging system)
			isConsumer := t.isConsumerOperation(span, attrs)

			var source, target string
			if isConsumer {
				// For consumers: Topic → Service (topic triggers service)
				source = span.DestinationName
				target = serviceName
			} else {
				// For producers: Service → Topic (service calls topic)
				source = serviceName
				target = span.DestinationName
			}

			depKey := source + "->" + target
			if dep, exists := dependencyMap[depKey]; exists {
				t.updateDependency(dep, span, attrs, duration, isError)
			} else {
				newDep := t.createNewDependency(source, target, span, attrs, duration, isError)
				dependencyMap[depKey] = newDep
				t.trackExternalService(externalServices, newDep, span, attrs)
			}
		}

		// handle HTTP client calls using http.host (for services that don't emit spans)
		// http.host indicates the target service for HTTP client calls
		if attrs.HTTPHost != "" && attrs.HTTPHost != serviceName && attrs.HTTPMethod != "" {
			depKey := serviceName + "->" + attrs.HTTPHost
			if dep, exists := dependencyMap[depKey]; exists {
				t.updateDependency(dep, span, attrs, duration, isError)
			} else {
				newDep := t.createNewDependency(serviceName, attrs.HTTPHost, span, attrs, duration, isError)
				newDep.DependencyType = "http_client"
				dependencyMap[depKey] = newDep
				t.trackExternalService(externalServices, newDep, span, attrs)
			}
		}
	}

	// Second pass: build dependencies using parent-child relationships (trace relationships)
	// This avoids O(N^2) scanning of siblings.
	for i := range parsedSpans {
		p := &parsedSpans[i]
		span := p.Span
		attrs := p.ParsedAttrs.Structured

		// children of this span (i.e., spans where this span is parent)
		children := parentMap[span.SpanID]
		if len(children) == 0 {
			continue
		}

		// For each child, if child's service differs from parent's service, create/update dependency
		// Use bySpanID to get child's parsed attrs quickly (already have children as ParsedSpan pointers)
		parentService := attrs.ServiceName
		if parentService == "" {
			parentService = span.WorkloadName
		}
		for _, childP := range children {
			child := childP.Span
			childAttrs := childP.ParsedAttrs.Structured

			childService := childAttrs.ServiceName
			if childService == "" {
				childService = child.WorkloadName
			}
			if childService == "" || parentService == "" || childService == parentService {
				continue
			}

			depKey := parentService + "->" + childService
			duration := child.DurationNs
			if duration == 0 {
				duration = 0
			}
			isError := t.isErrorSpan(child, childAttrs)

			if dep, exists := dependencyMap[depKey]; exists {
				t.updateDependency(dep, child, childAttrs, duration, isError)
			} else {
				newDep := t.createNewDependency(parentService, childService, child, childAttrs, duration, isError)
				newDep.DependencyType = "trace_relationship"
				dependencyMap[depKey] = newDep
				t.trackExternalService(externalServices, newDep, child, childAttrs)
			}
		}
	}

	// Build Applications (same output logic but using prepared maps)
	applications := make([]ServiceApplication, 0, len(serviceStats)+len(externalServices))

	for _, stats := range serviceStats {
		appId := ServiceApplicationId{
			Name:      stats.ServiceName,
			Kind:      "Service",
			Namespace: stats.Namespace,
		}

		upstreams := t.buildServiceLinks(dependencyMap, serviceStats, externalServices, stats.ServiceName, stats.Namespace, durationMinutes, earliestTime, latestTime, true)
		downstreams := t.buildServiceLinks(dependencyMap, serviceStats, externalServices, stats.ServiceName, stats.Namespace, durationMinutes, earliestTime, latestTime, false)

		instances := []Instance{
			{
				Id:       appId,
				IsFailed: stats.ErrorCount > 0,
			},
		}

		labels := t.buildServiceLabels(stats)
		appTypes := t.buildApplicationTypes(stats)

		upstreamsLinks, ok := upstreams.([]UpstreamLink)
		if !ok {
			slog.Error("Expected upstreams to be of type []UpstreamLink")
			upstreamsLinks = []UpstreamLink{}
		}
		downstreamsLinks, ok := downstreams.([]DownstreamLink)
		if !ok {
			slog.Error("Expected downstreams to be of type []DownstreamLink")
			downstreamsLinks = []DownstreamLink{}
		}

		app := ServiceApplication{
			Id:               appId,
			Category:         ServiceCategory{Category: "application"},
			Labels:           labels,
			Status:           nil,
			Indicators:       []string{},
			Upstreams:        upstreamsLinks,
			Downstreams:      downstreamsLinks,
			Instances:        instances,
			Type:             appTypes,
			DesiredInstances: 1,
			FailedInstances:  0,
			IsHealthy:        stats.ErrorCount == 0,
			HealthReason:     t.getHealthReason(stats.ErrorCount),
		}

		if stats.ErrorCount > 0 {
			app.FailedInstances = 1
		}

		applications = append(applications, app)
	}

	// External apps from dependencyMap (reuse your helper)
	externalApps := t.createExternalServiceApplications(dependencyMap, serviceStats, durationMinutes)
	applications = append(applications, externalApps...)

	// Only sort if necessary (kept from original behavior). If you don't need order, remove.
	sort.Slice(applications, func(i, j int) bool {
		return applications[i].Id.Name < applications[j].Id.Name
	})

	// Extract K8s infrastructure metadata from the same spans
	k8sMetadata := t.extractK8sMetadataFromSpans()

	return &ServiceMap{
		Applications: applications,
		GeneratedAt:  time.Now(),
		K8sMetadata:  k8sMetadata,
		Labels:       Labels,
	}, nil
}

type serviceMetrics struct {
	ServiceName   string
	Namespace     string
	Environment   string
	CallCount     int64
	ErrorCount    int64
	TotalDuration float64
	// Aggregate telemetry attributes as labels
	TelemetryLabels map[string]string
	// Track all application types detected for this service (multi-container support)
	ApplicationTypes map[string]bool
}

// ParsedSpanAttributes contains both structured and raw attribute data
type ParsedSpanAttributes struct {
	Structured *SpanAttributes
	Raw        map[string]string
}

// FetchTracesAndBuildServiceMap fetches traces using existing query infrastructure and builds a service map
func FetchTracesAndBuildServiceMap(requestContext *security.RequestContext, params TraceQueryParams) (*ServiceMap, error) {
	startTime := time.Now()
	slog.Info("Starting FetchTracesAndBuildServiceMap",
		"account_id", params.AccountID,
		"start_time", params.StartTime.Format(time.RFC3339),
		"end_time", params.EndTime.Format(time.RFC3339))

	if requestContext == nil {
		return nil, fmt.Errorf("request context is required")
	}

	if params.AccountID == "" {
		return nil, fmt.Errorf("account ID is required")
	}

	if params.EndTime.IsZero() {
		params.EndTime = time.Now()
	}

	if params.StartTime.IsZero() {
		params.StartTime = params.EndTime.Add(-15 * time.Minute)
	}

	builder := NewTraceServiceMapBuilder()

	// Apply exclusion filters if provided (e.g., to exclude Redis and Kafka)
	if len(params.ExcludeFilters) > 0 {
		builder.SetExcludeFilters(params.ExcludeFilters)
		slog.Info("Applied exclusion filters to service map builder", "exclude_filter_count", len(params.ExcludeFilters))
	}

	// Use focused query strategy for better trace coverage
	serviceMap, err := FetchTracesWithFocusedStrategy(requestContext, params, builder)

	elapsed := time.Since(startTime)
	slog.Info("Completed FetchTracesAndBuildServiceMap",
		"duration_ms", elapsed.Milliseconds(),
		"duration_seconds", elapsed.Seconds(),
		"error", err != nil)

	return serviceMap, err
}

func FetchTracesWithFocusedStrategy(requestContext *security.RequestContext, params TraceQueryParams, builder *TraceServiceMapBuilder) (*ServiceMap, error) {
	strategyStartTime := time.Now()

	// Generate focused queries using different filtering strategies
	focusedQueries := generateFocusedQueries(params)

	successfulQueries := 0

	slog.Info("Executing focused trace queries",
		"total_queries", len(focusedQueries),
		"time_range_minutes", params.EndTime.Sub(params.StartTime).Minutes())

	rows := []common.OpenTelemetryTrace{}
	queryStartTime := time.Now()

	const maxConcurrent = 2
	type queryJob struct {
		index  int
		params FocusedQueryParams
	}
	type queryResult struct {
		index    int
		strategy string
		rows     []common.OpenTelemetryTrace
		err      error
	}

	jobs := make(chan queryJob, len(focusedQueries))
	results := make(chan queryResult, len(focusedQueries))

	for w := 0; w < maxConcurrent; w++ {
		go func() {
			for job := range jobs {
				response, err := executeFocusedQuery(requestContext, params, job.params)
				result := queryResult{
					index:    job.index,
					strategy: job.params.strategy,
					err:      err,
				}
				if err == nil {
					result.rows = response
				}
				results <- result
			}
		}()
	}

	for i, queryParams := range focusedQueries {
		jobs <- queryJob{index: i, params: queryParams}
	}
	close(jobs)

	for i := 0; i < len(focusedQueries); i++ {
		result := <-results
		if result.err != nil {
			continue
		}
		rows = append(rows, result.rows...)
		successfulQueries++
	}
	queryElapsed := time.Since(queryStartTime)

	slog.Info("Focused queries completed",
		"successful_queries", successfulQueries,
		"traces_found", len(rows),
		"query_duration_ms", queryElapsed.Milliseconds())

	err := builder.LoadSpansFromRows(requestContext, rows, params)
	if err != nil {
		return nil, fmt.Errorf("failed to load spans")
	}
	// Build service map
	buildStartTime := time.Now()
	serviceMap, err := builder.BuildServiceMapWithTimeWindow(params.StartTime, params.EndTime)
	buildElapsed := time.Since(buildStartTime)
	strategyElapsed := time.Since(strategyStartTime)

	slog.Info("Completed FetchTracesWithFocusedStrategy",
		"total_duration_ms", strategyElapsed.Milliseconds(),
		"build_duration_ms", buildElapsed.Milliseconds(),
		"applications_count", func() int {
			if serviceMap != nil {
				return len(serviceMap.Applications)
			}
			return 0
		}())

	return serviceMap, err
}

// FocusedQueryParams represents a strategic query with its filtering approach
type FocusedQueryParams struct {
	strategy   string
	timeWindow *TimeWindow
}

type TimeWindow struct {
	start time.Time
	end   time.Time
}

// generateFocusedQueries creates multiple strategic queries to maximize coverage
func generateFocusedQueries(params TraceQueryParams) []FocusedQueryParams {
	queries := []FocusedQueryParams{}

	if params.WorkloadName != "" {
		// Two separate queries to get all spans involving this workload:
		// Query 1: spans where this workload is the SOURCE (e.g. service.name = 'hasura')
		queries = append(queries, FocusedQueryParams{
			strategy: "workloadNameSourceFilter",
		})
		// Query 2: spans where this workload is the DESTINATION (e.g. server.address = 'hasura')
		queries = append(queries, FocusedQueryParams{
			strategy: "workloadNameDestinationFilter",
		})
	} else {
		queries = append(queries, FocusedQueryParams{
			timeWindow: &TimeWindow{start: params.StartTime, end: params.EndTime},
		})
	}
	return queries
}

// executeFocusedQuery executes a single focused query using traces_v2 format
func executeFocusedQuery(requestContext *security.RequestContext, params TraceQueryParams, focusedParams FocusedQueryParams) ([]common.OpenTelemetryTrace, error) {
	// Build query request in traces_v2 format
	queryRequest := observability.TracesQueryBuilderRequest{
		Where: buildFocusedWhereClause(params, focusedParams),
		OrderBy: []query.QueryOrderBy{
			{Column: "timestamp", Order: query.Desc},
		},
	}

	// Execute query
	// response, err := query.ExecuteQuery(requestContext, queryRequest)
	traceRequest := observability.TracesV3Request{}
	traceRequest.AccountId = params.AccountID
	traceRequest.QueryRequest = queryRequest
	startTime := params.StartTime
	endTime := params.EndTime
	if focusedParams.timeWindow != nil {
		startTime = focusedParams.timeWindow.start
		endTime = focusedParams.timeWindow.end
	}
	traceRequest.StartTime = startTime.UnixMilli()
	traceRequest.EndTime = endTime.UnixMilli()
	response, err := observability.GetTraces(requestContext, traceRequest)
	if err != nil {
		return []common.OpenTelemetryTrace{}, fmt.Errorf("focused query failed: %w", err)
	}
	return response, nil
}

// buildFocusedWhereClause builds the WHERE clause for focused queries
func buildFocusedWhereClause(params TraceQueryParams, focusedParams FocusedQueryParams) query.QueryWhereClause {
	whereClause := query.QueryWhereClause{
		Binary: map[string]map[query.BinaryWhereClauseType]any{
			"trace_source": {
				query.Eq: "otel",
			},
		},
	}

	// Add workload/namespace filters based on strategy.
	// Source and destination filters are split into separate queries (not AND-ed together)
	// to find spans where the workload is either the caller OR the callee.
	switch focusedParams.strategy {
	case "workloadNameSourceFilter":
		if params.WorkloadName != "" {
			whereClause.Binary["workload_name"] = map[query.BinaryWhereClauseType]any{
				query.Eq: params.WorkloadName,
			}
		}
		if params.WorkloadNamespace != "" {
			whereClause.Binary["workload_namespace"] = map[query.BinaryWhereClauseType]any{
				query.Eq: params.WorkloadNamespace,
			}
		}
	case "workloadNameDestinationFilter":
		if params.WorkloadName != "" {
			whereClause.Binary["destination_workload_name"] = map[query.BinaryWhereClauseType]any{
				query.Eq: params.WorkloadName,
			}
		}
		if params.WorkloadNamespace != "" {
			whereClause.Binary["destination_workload_namespace"] = map[query.BinaryWhereClauseType]any{
				query.Eq: params.WorkloadNamespace,
			}
		}
	}

	if len(params.LabelFilters) > 0 {
		spanAttrFilters := make(map[query.BinaryWhereClauseType]any)
		for _, filter := range params.LabelFilters {
			if existingFilter, exists := spanAttrFilters[filter.Operator]; exists {
				// Merge with existing filters for the same operator
				if filterMap, ok := existingFilter.(map[string]any); ok {
					filterMap[filter.Key] = filter.Value
				}
			} else {
				// Create new filter map for this operator
				spanAttrFilters[filter.Operator] = map[string]any{
					filter.Key: filter.Value,
				}
			}
		}
		whereClause.Binary["spanattributes"] = spanAttrFilters
	}
	return whereClause
}

// createExternalServiceApplications creates application nodes for external dependencies
// like databases, messaging systems, caches, etc. that are not actual microservices
func (t *TraceServiceMapBuilder) createExternalServiceApplications(dependencyMap map[string]*ServiceDependency, serviceStats map[string]*serviceMetrics, durationMinutes float64) []ServiceApplication {
	var externalApps []ServiceApplication
	externalServices := make(map[string]*ExternalServiceInfo)

	// Collect all external service targets from dependencies
	for _, dep := range dependencyMap {
		// Only create external applications for non-service dependencies
		if t.isExternalDependency(dep.DependencyType) {
			// Skip if this external service matches exclusion filters
			if t.shouldExcludeDependency(dep) {
				continue
			}

			if _, exists := serviceStats[dep.Target]; !exists { // Not an actual service
				if _, exists := externalServices[dep.Target]; !exists {
					externalServices[dep.Target] = &ExternalServiceInfo{
						Name:           dep.Target,
						Protocol:       dep.Protocol,
						DependencyType: dep.DependencyType,
						Environment:    dep.Environment,
						CallCount:      0,
						ErrorCount:     0,
						Applications:   make(map[string]bool),
					}
				}

				// Aggregate stats from all dependencies to this external service
				external := externalServices[dep.Target]
				external.CallCount += dep.CallCount
				external.ErrorCount += dep.ErrorCount
				external.Applications[dep.Source] = true
			}
		}
	}

	// Pre-resolve DNS concurrently
	dnsCache := t.ResolveDNSForExternalServices(externalServices)

	// First pass: Resolve DNS and create CNAME nodes
	cnameNodes := make(map[string]*DNSResolutionInfo)
	// Iterate over results from concurrent resolution
	for _, dnsInfo := range dnsCache {
		if dnsInfo != nil && dnsInfo.CNAME != "" {
			// Store CNAME info for creating nodes
			if _, exists := cnameNodes[dnsInfo.CNAME]; !exists {
				cnameNodes[dnsInfo.CNAME] = dnsInfo
			}
		}
	}

	// Create application objects for each external service
	for serviceName, info := range externalServices {
		appTypes := t.detectExternalApplicationTypes(info)

		appId := ServiceApplicationId{
			Name:      serviceName,
			Kind:      "ExternalService",
			Namespace: "", // External services don't have namespaces
		}

		labels := map[string]string{
			"external": "true",
			"protocol": info.Protocol,
		}
		if info.Environment != "" {
			labels["environment"] = info.Environment
		}
		if info.DependencyType != "" {
			labels["dependency_type"] = info.DependencyType
		}

		// Enrich with DNS resolution and create upstream to CNAME
		var upstreams []UpstreamLink
		if dnsInfo := dnsCache[serviceName]; dnsInfo != nil {
			if dnsInfo.CNAME != "" {
				labels["dns.cname"] = dnsInfo.CNAME
				labels["dns.resolved_to"] = dnsInfo.CNAME

				// Create upstream link to CNAME (not IPs!)
				upstreams = append(upstreams, UpstreamLink{
					Id:            fmt.Sprintf(":ExternalService:%s", dnsInfo.CNAME),
					Status:        0,
					Stats:         []string{"DNS CNAME", ""},
					Weight:        0,
					Latency:       0,
					RequestCount:  0,
					FailureCount:  0,
					Protocol:      info.Protocol,
					BytesSent:     0,
					BytesReceived: 0,
					DrillDown:     nil,
				})
			}
			if dnsInfo.CloudVendor != "" {
				labels["dns.cloud_vendor"] = dnsInfo.CloudVendor
			}
			if dnsInfo.ServiceType != "" {
				labels["dns.service_type"] = dnsInfo.ServiceType
			}
			if len(dnsInfo.IPs) > 0 {
				labels["dns.ips"] = strings.Join(dnsInfo.IPs, ",")
				labels["dns.backend_count"] = fmt.Sprintf("%d", len(dnsInfo.IPs))
			}
		}

		// Create downstreams pointing back to the services that depend on this external service
		var downstreams []DownstreamLink
		dependencyStats := t.collectDependencyStatsForExternalService(dependencyMap, serviceName)

		for dependentService := range info.Applications {
			if stats, exists := serviceStats[dependentService]; exists {
				// Get actual metrics for this specific dependency
				depStats := dependencyStats[dependentService]
				if depStats == nil {
					continue // Skip if no dependency found
				}

				errorRate := float64(0)
				if depStats.CallCount > 0 {
					errorRate = (float64(depStats.ErrorCount) / float64(depStats.CallCount)) * 100
				}

				avgLatency := float64(0)
				if depStats.CallCount > 0 {
					// TotalDuration is in nanoseconds, convert to milliseconds
					avgLatency = (depStats.TotalDuration / float64(depStats.CallCount)) / 1e6
				}

				downstream := DownstreamLink{
					Id: ServiceApplicationId{
						Name:      dependentService,
						Kind:      "Service",
						Namespace: stats.Namespace,
					},
					Status:       t.getLinkStatusCode(errorRate),
					Stats:        t.formatLinkStats(depStats.CallCount, avgLatency, durationMinutes),
					Weight:       float64(depStats.CallCount),
					Latency:      avgLatency,
					RequestCount: float64(depStats.CallCount),
					FailureCount: float64(depStats.ErrorCount),
					Protocol:     info.Protocol,
				}
				downstreams = append(downstreams, downstream)
			}
		}

		app := ServiceApplication{
			Id:               appId,
			Category:         ServiceCategory{Category: "external"}, // Different category
			Labels:           labels,
			Status:           nil,
			Indicators:       []string{"external"},
			Upstreams:        upstreams, // DNS-resolved backend IPs
			Downstreams:      downstreams,
			Instances:        []Instance{{Id: appId, IsFailed: false}},
			Type:             appTypes,
			DesiredInstances: 1,
			FailedInstances:  0,
			IsHealthy:        info.ErrorCount == 0,
			HealthReason:     t.getHealthReason(info.ErrorCount),
		}

		if info.ErrorCount > 0 {
			app.FailedInstances = 1
		}

		externalApps = append(externalApps, app)
	}

	// Second pass: Create nodes for CNAMEs (actual cloud endpoints)
	for cname, dnsInfo := range cnameNodes {
		appId := ServiceApplicationId{
			Name:      cname,
			Kind:      "ExternalService",
			Namespace: "",
		}

		labels := map[string]string{
			"external":         "true",
			"dns.is_cname":     "true",
			"dns.cloud_vendor": dnsInfo.CloudVendor,
		}

		if len(dnsInfo.IPs) > 0 {
			labels["dns.ips"] = strings.Join(dnsInfo.IPs, ",")
			labels["dns.backend_count"] = fmt.Sprintf("%d", len(dnsInfo.IPs))
		}

		// Determine application type from cloud service detection
		appTypes := []string{"external"}
		if dnsInfo.ServiceType != "" {
			appTypes = []string{dnsInfo.ServiceType}
			labels["dns.service_type"] = dnsInfo.ServiceType
		}

		app := ServiceApplication{
			Id:               appId,
			Category:         ServiceCategory{Category: "external"},
			Labels:           labels,
			Status:           nil,
			Indicators:       []string{"external", "dns_cname"},
			Upstreams:        []UpstreamLink{},   // CNAMEs don't have upstreams
			Downstreams:      []DownstreamLink{}, // Will be populated by services that point to this CNAME
			Instances:        []Instance{{Id: appId, IsFailed: false}},
			Type:             appTypes,
			DesiredInstances: 1,
			FailedInstances:  0,
			IsHealthy:        true,
			HealthReason:     "",
		}

		externalApps = append(externalApps, app)
	}

	return externalApps
}

// detectExternalApplicationTypes determines the application type for external services
func (t *TraceServiceMapBuilder) detectExternalApplicationTypes(info *ExternalServiceInfo) []string {
	protocol := strings.ToLower(info.Protocol)

	switch protocol {
	// Database protocols
	case "postgresql", "postgres":
		return []string{"postgres", "database"}
	case "mysql":
		return []string{"mysql", "database"}
	case "mongodb":
		return []string{"mongodb", "database"}
	case "elasticsearch":
		return []string{"elasticsearch", "database", "search"}

	// Cache protocols
	case "redis":
		return []string{"redis", "cache"}

	// Messaging protocols
	case "kafka":
		return []string{"kafka", "messaging"}
	case "rabbitmq":
		return []string{"rabbitmq", "messaging"}
	case "sqs":
		return []string{"sqs", "messaging"}
	case "amqp":
		return []string{"amqp", "messaging"}

	// HTTP protocols
	case "http", "https":
		return []string{"http", "external_api"}

	default:
		// Try to infer from dependency type
		switch info.DependencyType {
		case "messaging_system":
			return []string{"messaging", strings.ToLower(info.Protocol)}
		case "db_connection":
			return []string{"database", strings.ToLower(info.Protocol)}
		default:
			return []string{"external", strings.ToLower(info.Protocol)}
		}
	}
}

// ExternalServiceInfo holds information about external services for aggregation
type ExternalServiceInfo struct {
	Name           string
	Protocol       string
	DependencyType string
	Environment    string
	CallCount      int64
	ErrorCount     int64
	Applications   map[string]bool // Services that depend on this external service
}

// dependencyMetrics holds metrics for a specific dependency relationship
type dependencyMetrics struct {
	CallCount     int64
	ErrorCount    int64
	TotalDuration float64
}

// collectDependencyStatsForExternalService collects actual metrics from dependency map for an external service
func (t *TraceServiceMapBuilder) collectDependencyStatsForExternalService(dependencyMap map[string]*ServiceDependency, externalServiceName string) map[string]*dependencyMetrics {
	stats := make(map[string]*dependencyMetrics)

	for _, dep := range dependencyMap {
		if dep.Target == externalServiceName && t.isExternalDependency(dep.DependencyType) {
			if _, exists := stats[dep.Source]; !exists {
				stats[dep.Source] = &dependencyMetrics{}
			}

			metrics := stats[dep.Source]
			metrics.CallCount += dep.CallCount
			metrics.ErrorCount += dep.ErrorCount
			metrics.TotalDuration += dep.TotalDuration
		}
	}

	return stats
}

// formatLinkStats formats statistics for display in the link
func (t *TraceServiceMapBuilder) formatLinkStats(callCount int64, avgLatencyMs float64, durationMinutes float64) []string {
	if callCount == 0 {
		return []string{"0 req/min", "0ms"}
	}

	// Calculate req/min based on actual time window duration
	reqPerMin := float64(0)
	if durationMinutes > 0 {
		reqPerMin = float64(callCount) / durationMinutes
	} else {
		// Fallback to assuming 1 minute if duration is invalid
		reqPerMin = float64(callCount)
	}

	if reqPerMin < 0.01 {
		reqPerMin = 0.01 // Minimum display value for very low rates
	}

	return []string{
		fmt.Sprintf("%.1f req/min", reqPerMin),
		fmt.Sprintf("%.1fms", avgLatencyMs),
	}
}
