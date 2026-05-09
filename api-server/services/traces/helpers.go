package traces

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// buildServiceLinks creates Link objects for a service's dependencies
func (t *TraceServiceMapBuilder) buildServiceLinks(dependencyMap map[string]*ServiceDependency, serviceStats map[string]*serviceMetrics, externalServices map[string]*ExternalServiceInfo, serviceName, namespace string, durationMinutes float64, earliestTime, latestTime time.Time, isUpstream bool) interface{} {
	var links interface{}

	if isUpstream {
		// Upstream links
		links = make([]UpstreamLink, 0)
	} else {
		// Downstream links
		links = make([]DownstreamLink, 0)
	}

	// Loop through the dependency map and build links
	for _, dep := range dependencyMap {
		var shouldInclude bool
		var targetName string

		// CORRECT: Upstream = services I call (my dependencies, I am the source)
		// Downstream = services calling me (my consumers, I am the target)
		if isUpstream && dep.Source == serviceName {
			shouldInclude = true
			targetName = dep.Target // Services I call (my dependencies)
		} else if !isUpstream && dep.Target == serviceName {
			shouldInclude = true
			targetName = dep.Source // Services calling me (my consumers)
		}

		if !shouldInclude {
			continue
		}

		// Calculate metrics
		dep.AvgDuration = dep.TotalDuration / float64(dep.CallCount) / t.config.NanosecondsToMilliseconds
		dep.ErrorRate = float64(dep.ErrorCount) / float64(dep.CallCount) * 100
		reqPerMin := float64(dep.CallCount) / durationMinutes

		// Look up the target service's namespace and determine if it's external
		targetNamespace := namespace // fallback to source namespace if not found
		targetKind := "Service"
		if targetStats, exists := serviceStats[targetName]; exists {
			targetNamespace = targetStats.Namespace
		} else if _, exists := externalServices[targetName]; exists {
			// This is an external service
			targetNamespace = ""
			targetKind = "ExternalService"
		}

		link := UpstreamLink{
			Id:           fmt.Sprintf("%s:%s:%s", targetNamespace, targetKind, targetName),
			Status:       t.getLinkStatusCode(dep.ErrorRate),
			Stats:        []string{fmt.Sprintf("%.1f req/min", reqPerMin), fmt.Sprintf("%.1fms", dep.AvgDuration)},
			Weight:       float64(dep.CallCount),
			Latency:      dep.AvgDuration,
			RequestCount: float64(dep.CallCount),
			FailureCount: float64(dep.ErrorCount),
			Protocol:     dep.Protocol,
			DrillDown:    t.createLinkDrillDown(dep, earliestTime, latestTime),
		}

		if !isUpstream {
			appId := ServiceApplicationId{
				Name:      targetName,
				Kind:      targetKind,
				Namespace: targetNamespace, // Use target service's actual namespace
			}
			link := DownstreamLink{
				Id:           appId,
				Status:       t.getLinkStatusCode(dep.ErrorRate),
				Stats:        []string{fmt.Sprintf("%.1f req/min", reqPerMin), fmt.Sprintf("%.1fms", dep.AvgDuration)},
				Weight:       float64(dep.CallCount),
				Latency:      dep.AvgDuration,
				RequestCount: float64(dep.CallCount),
				FailureCount: float64(dep.ErrorCount),
				Protocol:     dep.Protocol,
				DrillDown:    t.createLinkDrillDown(dep, earliestTime, latestTime),
			}
			links = append(links.([]DownstreamLink), link)
		} else {
			links = append(links.([]UpstreamLink), link)
		}
	}

	return links
}

// buildServiceLabels creates the labels map for a service
func (t *TraceServiceMapBuilder) buildServiceLabels(stats *serviceMetrics) map[string]string {
	labels := make(map[string]string)
	labels["ns"] = stats.Namespace

	maps.Copy(labels, stats.TelemetryLabels)

	return labels
}

// buildApplicationTypes converts the ApplicationTypes map to a Type array
func (t *TraceServiceMapBuilder) buildApplicationTypes(stats *serviceMetrics) []string {
	var appTypes []string
	for appType := range stats.ApplicationTypes {
		appTypes = append(appTypes, appType)
	}

	if len(appTypes) == 0 {
		appTypes = DefaultApplicationTypes
	}

	return appTypes
}

// updateDependency updates an existing dependency with new span data
func (t *TraceServiceMapBuilder) updateDependency(dep *ServiceDependency, span TraceSpan, attrs *SpanAttributes, duration float64, isError bool) {
	dep.CallCount++
	dep.TotalDuration += duration

	// Limit trace IDs to prevent excessive memory usage
	if len(dep.TraceIds) < t.config.MaxTraceIDsForExpansion {
		dep.TraceIds = append(dep.TraceIds, span.TraceID)
	}

	if dep.Operations == nil {
		dep.Operations = make(map[string]int64)
	}
	dep.Operations[span.SpanName]++

	if attrs.HTTPStatusCode > 0 {
		if dep.StatusCodes == nil {
			dep.StatusCodes = make(map[int]int64)
		}
		dep.StatusCodes[attrs.HTTPStatusCode]++
	}

	if isError {
		dep.ErrorCount++

		// Limit failed trace IDs to prevent excessive memory usage
		if len(dep.FailedTraceIds) < t.config.MaxTraceIDsForExpansion {
			dep.FailedTraceIds = append(dep.FailedTraceIds, span.TraceID)
		}

		if dep.ErrorTypes == nil {
			dep.ErrorTypes = make(map[string]int64)
		}
		errorType := t.categorizeError(span, attrs)
		dep.ErrorTypes[errorType]++
	}
}

// createNewDependency creates a new ServiceDependency
func (t *TraceServiceMapBuilder) createNewDependency(source, target string, span TraceSpan, attrs *SpanAttributes, duration float64, isError bool) *ServiceDependency {
	protocol := t.inferProtocol(span, attrs)
	depType := t.detectDependencyType(span, attrs)

	dep := &ServiceDependency{
		Source:         source,
		Target:         target,
		CallCount:      1,
		TotalDuration:  duration,
		ErrorCount:     0,
		Protocol:       protocol,
		Environment:    attrs.DeploymentEnv,
		TraceIds:       []string{span.TraceID},
		FailedTraceIds: []string{},
		Operations:     make(map[string]int64),
		StatusCodes:    make(map[int]int64),
		ErrorTypes:     make(map[string]int64),
		DependencyType: depType,
		OriginalTarget: target,
	}

	dep.Operations[span.SpanName] = 1

	if attrs.HTTPStatusCode > 0 {
		dep.StatusCodes[attrs.HTTPStatusCode] = 1
	}

	if isError {
		dep.ErrorCount = 1
		dep.FailedTraceIds = append(dep.FailedTraceIds, span.TraceID)
		errorType := t.categorizeError(span, attrs)
		dep.ErrorTypes[errorType] = 1
	}

	return dep
}

// extractHostnameFromURL extracts the hostname from a full HTTP URL
// e.g., "http://ocean-api-staging.fourkites.com/api/v1/..." -> "ocean-api-staging.fourkites.com"
func extractHostnameFromURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	// Parse the URL
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, return empty string
		return ""
	}

	// Return the hostname (which includes the port if present, but we just want the host)
	return parsedURL.Hostname()
}

// parseSpanAttributes parses span attributes JSON and returns both structured and raw data
func (t *TraceServiceMapBuilder) parseSpanAttributes(spanAttributes map[string]string) (*ParsedSpanAttributes, error) {
	// Return empty attributes if no JSON provided
	if len(spanAttributes) == 0 {
		return &ParsedSpanAttributes{
			Structured: &SpanAttributes{},
			Raw:        make(map[string]string),
		}, nil
	}

	var rawAttrs = spanAttributes
	attrs := &SpanAttributes{}

	if val, ok := spanAttributes["service.name"]; ok {
		attrs.ServiceName = val
	}
	if val, ok := spanAttributes["span.kind"]; ok {
		attrs.SpanKind = val
	}
	if val, ok := spanAttributes["db.system"]; ok {
		attrs.DBSystem = val
	}
	if val, ok := spanAttributes["db.host"]; ok {
		attrs.DBHost = val
	}
	if val, ok := spanAttributes["db.name"]; ok {
		attrs.DBName = val
	}
	if val, ok := spanAttributes["messaging.system"]; ok {
		attrs.MessagingSystem = val
	}
	if val, ok := spanAttributes["messaging.destination"]; ok {
		attrs.MessagingDestination = val
	}
	if val, ok := spanAttributes["net.peer.name"]; ok {
		attrs.NetPeerName = val
	}
	if val, ok := spanAttributes["net.peer.port"]; ok {
		temp, err := strconv.Atoi(val)
		attrs.NetPeerPort = temp
		if err != nil {
			return &ParsedSpanAttributes{}, err
		}
	}
	if val, ok := spanAttributes["http.method"]; ok {
		attrs.HTTPMethod = val
	}
	if val, ok := spanAttributes["http.host"]; ok {
		attrs.HTTPHost = val
	}

	// If http.host is missing, try to extract hostname from http.url
	if attrs.HTTPHost == "" {
		if httpURL, ok := spanAttributes["http.url"]; ok && httpURL != "" {
			// Parse URL to extract hostname
			if hostname := extractHostnameFromURL(httpURL); hostname != "" {
				attrs.HTTPHost = hostname
			}
		}
	}

	if val, ok := spanAttributes["http.route"]; ok {
		attrs.HTTPRoute = val
	}
	if val, ok := spanAttributes["http.status_code"]; ok {
		temp, err := strconv.Atoi(val)
		attrs.HTTPStatusCode = temp
		if err != nil {
			return &ParsedSpanAttributes{}, err
		}
	}
	if val, ok := spanAttributes["deployment.environment"]; ok {
		attrs.DeploymentEnv = val
	}
	if val, ok := spanAttributes["k8s_cluster"]; ok {
		attrs.K8sCluster = val
	}

	return &ParsedSpanAttributes{
		Structured: attrs,
		Raw:        rawAttrs,
	}, nil
}

func (t *TraceServiceMapBuilder) isErrorSpan(span TraceSpan, attrs *SpanAttributes) bool {
	if attrs.HTTPStatusCode >= 400 {
		return true
	}

	if strings.Contains(strings.ToLower(span.StatusCode), "error") {
		return true
	}

	return false
}

// isConsumerOperation detects if a span represents a message consumer/processor operation
// Returns true for consumers (topic triggers service), false for producers (service sends to topic)
func (t *TraceServiceMapBuilder) isConsumerOperation(span TraceSpan, attrs *SpanAttributes) bool {
	// Check if this is a messaging system operation
	if attrs.MessagingSystem == "" {
		return false
	}

	// Check span.kind: CONSUMER indicates the service is consuming from the topic
	if strings.EqualFold(attrs.SpanKind, "CONSUMER") {
		return true
	}

	// Check span.kind: PRODUCER/CLIENT indicates the service is sending to the topic
	if strings.EqualFold(attrs.SpanKind, "PRODUCER") || strings.EqualFold(attrs.SpanKind, "CLIENT") {
		return false
	}

	// Fallback: Check span name patterns
	// "Kafka topic XXX send" = producer
	// "Kafka topic XXX process" / "Kafka topic XXX receive" = consumer
	spanName := strings.ToLower(span.SpanName)
	if strings.Contains(spanName, "process") || strings.Contains(spanName, "receive") || strings.Contains(spanName, "consume") {
		return true
	}
	if strings.Contains(spanName, "send") || strings.Contains(spanName, "produce") || strings.Contains(spanName, "publish") {
		return false
	}

	// Default: treat as producer (service calling destination)
	return false
}

func (t *TraceServiceMapBuilder) categorizeError(span TraceSpan, attrs *SpanAttributes) string {
	// HTTP errors
	if attrs.HTTPStatusCode >= 400 {
		if attrs.HTTPStatusCode >= 500 {
			return "HTTP_5XX_ERROR"
		}
		return "HTTP_4XX_ERROR"
	}

	// Database errors
	if attrs.DBSystem != "" {
		if strings.Contains(strings.ToLower(span.StatusCode), "error") {
			return fmt.Sprintf("%s_ERROR", strings.ToUpper(attrs.DBSystem))
		}
	}

	// Protocol-specific errors
	protocol := t.inferProtocol(span, attrs)
	if strings.Contains(strings.ToLower(span.StatusCode), "timeout") {
		return fmt.Sprintf("%s_TIMEOUT", protocol)
	}

	// Default error type
	return "UNKNOWN_ERROR"
}

func (t *TraceServiceMapBuilder) createLinkDrillDown(dep *ServiceDependency, startTime, endTime time.Time) *LinkDrillDown {
	drillDown := &LinkDrillDown{
		TimeRange: TimeRange{
			StartTime: startTime.Format(time.RFC3339),
			EndTime:   endTime.Format(time.RFC3339),
		},
		FilterHints: t.createFilterHints(dep),
	}

	// Deduplicate and limit trace IDs to prevent overly large responses
	maxTraceIds := t.config.MaxTraceIDsInDrillDown
	if len(dep.TraceIds) > 0 {
		// Deduplicate trace IDs using a map
		uniqueTraceIds := make(map[string]bool)
		for _, traceId := range dep.TraceIds {
			uniqueTraceIds[traceId] = true
		}

		// Convert back to slice
		deduplicatedTraceIds := make([]string, 0, len(uniqueTraceIds))
		for traceId := range uniqueTraceIds {
			deduplicatedTraceIds = append(deduplicatedTraceIds, traceId)
		}

		if len(deduplicatedTraceIds) > maxTraceIds {
			drillDown.SampleTraceIds = deduplicatedTraceIds[:maxTraceIds]
		} else {
			drillDown.SampleTraceIds = deduplicatedTraceIds
		}
	}

	if len(dep.FailedTraceIds) > 0 {
		// Deduplicate failed trace IDs using a map
		uniqueFailedTraceIds := make(map[string]bool)
		for _, traceId := range dep.FailedTraceIds {
			uniqueFailedTraceIds[traceId] = true
		}

		// Convert back to slice
		deduplicatedFailedTraceIds := make([]string, 0, len(uniqueFailedTraceIds))
		for traceId := range uniqueFailedTraceIds {
			deduplicatedFailedTraceIds = append(deduplicatedFailedTraceIds, traceId)
		}

		if len(deduplicatedFailedTraceIds) > maxTraceIds {
			drillDown.FailedTraceIds = deduplicatedFailedTraceIds[:maxTraceIds]
		} else {
			drillDown.FailedTraceIds = deduplicatedFailedTraceIds
		}
	}

	// Convert operations map to OperationStat slice
	if len(dep.Operations) > 0 {
		operations := make([]OperationStat, 0, len(dep.Operations))
		drillDown.FilterHints.Operations = make([]string, 0, len(dep.Operations))
		for op, count := range dep.Operations {
			operations = append(operations, OperationStat{
				Operation:  op,
				Count:      count,
				AvgLatency: dep.AvgDuration, // Simplified - could be per-operation
				ErrorCount: 0,               // Would need per-operation tracking
			})
			drillDown.FilterHints.Operations = append(drillDown.FilterHints.Operations, op)
		}
		drillDown.Operations = operations
	}

	// Convert status codes map to StatusCodeStat slice
	if len(dep.StatusCodes) > 0 {
		statusCodes := make([]StatusCodeStat, 0, len(dep.StatusCodes))
		totalRequests := dep.CallCount
		for code, count := range dep.StatusCodes {
			statusCodes = append(statusCodes, StatusCodeStat{
				StatusCode: code,
				Count:      count,
				Percentage: float64(count) / float64(totalRequests) * 100,
			})
			if code >= 400 {
				drillDown.FilterHints.ErrorStatusCodes = append(drillDown.FilterHints.ErrorStatusCodes, code)
			}
		}
		drillDown.HTTPStatusCodes = statusCodes
	}

	// Convert error types map to ErrorSummary slice
	if len(dep.ErrorTypes) > 0 {
		errorTypes := make([]ErrorSummary, 0, len(dep.ErrorTypes))
		totalRequests := dep.CallCount
		for errType, count := range dep.ErrorTypes {
			errorTypes = append(errorTypes, ErrorSummary{
				Type:       errType,
				Count:      count,
				Percentage: float64(count) / float64(totalRequests) * 100,
			})
		}
		drillDown.ErrorTypes = errorTypes
	}

	return drillDown
}

// createFilterHints creates appropriate filter hints based on how the dependency was detected
func (t *TraceServiceMapBuilder) createFilterHints(dep *ServiceDependency) FilterHints {
	hints := FilterHints{
		Protocol: dep.Protocol,
	}

	// Provide different filter hints based on dependency type
	switch dep.DependencyType {
	case "direct_service":
		// Direct service call - use service names
		hints.SourceService = dep.Source
		hints.TargetService = dep.Target

	case "trace_relationship":
		// Parent-child span relationship
		hints.SourceService = dep.Source
		hints.TargetService = dep.Target

	case "net_peer", "db_connection", "http_external", "external_address", "ip_address", "messaging_system":
		// External dependency - don't use target_service since it's not a real service
		hints.SourceService = dep.Source

		// Extract the actual hostname/address from the target
		externalHost := dep.Target
		if strings.Contains(dep.Target, ":Service:") {
			parts := strings.Split(dep.Target, ":Service:")
			if len(parts) > 1 {
				externalHost = parts[1]
			}
		}

		// Suggest using span attributes instead of service name based on dependency type
		hints.SpanAttributeFilters = make(map[string]string)
		switch dep.DependencyType {
		case "net_peer":
			hints.SpanAttributeFilters["net.peer.name"] = externalHost
		case "db_connection":
			hints.SpanAttributeFilters["db.host"] = externalHost
			hints.SpanAttributeFilters["net.peer.name"] = externalHost
		case "http_external":
			hints.SpanAttributeFilters["http.host"] = externalHost
			hints.SpanAttributeFilters["net.peer.name"] = externalHost
		case "external_address":
			hints.SpanAttributeFilters["server.address"] = externalHost
			hints.SpanAttributeFilters["net.peer.name"] = externalHost
		case "ip_address":
			hints.SpanAttributeFilters["net.peer.name"] = externalHost
			hints.SpanAttributeFilters["server.address"] = externalHost
		case "messaging_system":
			hints.SpanAttributeFilters["messaging.destination"] = externalHost
			hints.SpanAttributeFilters["messaging.destination.name"] = externalHost
			if dep.Protocol != "Unknown" {
				hints.SpanAttributeFilters["messaging.system"] = strings.ToLower(dep.Protocol)
			}
		}

	default:
		// Unknown dependency type - default to service names
		hints.SourceService = dep.Source
		hints.TargetService = dep.Target
	}

	return hints
}

// detectDependencyType determines if a dependency is an external service based on attributes and naming patterns
func (t *TraceServiceMapBuilder) detectDependencyType(span TraceSpan, attrs *SpanAttributes) string {
	// Parse span attributes to check for external service indicators
	// Check if destination came from net.peer.name or similar external attributes
	if len(span.SpanAttributes) > 0 {
		// Check for messaging systems first
		if _, hasMessagingSystem := span.SpanAttributes["messaging.system"]; hasMessagingSystem {
			return "messaging_system"
		}
		if _, hasMessagingDest := span.SpanAttributes["messaging.destination"]; hasMessagingDest {
			return "messaging_system"
		}
		// If net.peer.name exists, this is likely an external dependency
		if _, hasNetPeer := span.SpanAttributes["net.peer.name"]; hasNetPeer {
			return "net_peer"
		}
		if _, hasDbHost := span.SpanAttributes["db.host"]; hasDbHost {
			return "db_connection"
		}
		if _, hasDbSystem := span.SpanAttributes["db.system"]; hasDbSystem {
			return "db_connection"
		}
		if _, hasHttpHost := span.SpanAttributes["http.host"]; hasHttpHost {
			return "http_external"
		}
		if _, hasServerAddr := span.SpanAttributes["server.address"]; hasServerAddr {
			return "external_address"
		}
	}

	// Pattern-based detection for external services
	target := span.DestinationName
	if target != "" {
		// Check for domain patterns that indicate external services
		if strings.Contains(target, ".internal") ||
			strings.Contains(target, ".local") ||
			strings.Contains(target, ".com") ||
			strings.Contains(target, ".net") ||
			strings.Contains(target, ".org") ||
			strings.Contains(target, ".io") ||
			strings.HasPrefix(target, "redis-") ||
			strings.HasPrefix(target, "postgres-") ||
			strings.HasPrefix(target, "mysql-") {
			return "net_peer"
		}

		// Check for IP addresses
		if strings.Count(target, ".") == 3 {
			// Simple IP address check
			parts := strings.Split(target, ".")
			if len(parts) == 4 {
				allNumbers := true
				for _, part := range parts {
					if len(part) == 0 || len(part) > 3 {
						allNumbers = false
						break
					}
					for _, char := range part {
						if char < '0' || char > '9' {
							allNumbers = false
							break
						}
					}
					if !allNumbers {
						break
					}
				}
				if allNumbers {
					return "ip_address"
				}
			}
		}
	}

	// Database connections are typically external
	if attrs.DBSystem != "" {
		return "db_connection"
	}

	// Messaging systems are also external dependencies
	if attrs.MessagingSystem != "" {
		return "messaging_system"
	}

	// Default to direct service if no external indicators found
	return "direct_service"
}

func (t *TraceServiceMapBuilder) getLinkStatusCode(errorRate float64) int {
	if errorRate > 10 {
		return 2 // ERROR
	} else if errorRate > 5 {
		return 1 // WARNING
	}
	return 0 // OK
}

func (t *TraceServiceMapBuilder) getHealthReason(errorCount int64) string {
	if errorCount > 0 {
		return "Errors detected"
	}
	return ""
}

func (t *TraceServiceMapBuilder) inferProtocol(span TraceSpan, attrs *SpanAttributes) string {
	if attrs.DBSystem != "" {
		return strings.ToUpper(attrs.DBSystem)
	}

	if attrs.MessagingSystem != "" {
		return strings.ToUpper(attrs.MessagingSystem)
	}

	if attrs.HTTPMethod != "" {
		return "HTTP"
	}

	if strings.Contains(strings.ToLower(span.SpanName), "redis") {
		return "Redis"
	}

	if strings.Contains(strings.ToLower(span.SpanName), "grpc") {
		return "gRPC"
	}

	return "Unknown"
}

// extractTelemetryLabels extracts telemetry attributes from span for service labeling
// Keeps original key-value pairs from source with filtering and aggregation
func (t *TraceServiceMapBuilder) extractTelemetryLabels(span TraceSpan, attrs *SpanAttributes) map[string]string {
	labels := make(map[string]string)

	// Parse span attributes to extract telemetry information
	if len(span.SpanAttributes) > 0 {
		t.filterAndExtractAttributes(span.SpanAttributes, labels)
	}

	// Add basic attributes as fallback
	if attrs.DeploymentEnv != "" {
		labels["deployment.environment"] = attrs.DeploymentEnv
	}
	if span.WorkloadNamespace != "" {
		labels["k8s.namespace.name"] = span.WorkloadNamespace
	}

	return labels
}

// filterAndExtractAttributes extracts attributes with original keys while filtering high-cardinality values
func (t *TraceServiceMapBuilder) filterAndExtractAttributes(rawAttrs map[string]string, labels map[string]string) {
	// Define high-cardinality attributes to ignore (unique per span)
	highCardinalityKeys := map[string]bool{
		"trace.id":            true,
		"span.id":             true,
		"parent.span.id":      true,
		"trace_id":            true,
		"span_id":             true,
		"parent_span_id":      true,
		"request.id":          true,
		"transaction.id":      true,
		"correlation.id":      true,
		"session.id":          true,
		"user.id":             true,
		"process.pid":         true,
		"thread.id":           true,
		"container.id":        true, // Container IDs are unique per instance
		"service.instance.id": true, // Service instance IDs are unique
		"timestamp":           true,
		"start_time":          true,
		"end_time":            true,
		"duration":            true,
		"http.request.id":     true,
		"http.url":            true, // Full URLs can have high cardinality
		"sql.query":           true, // SQL queries are unique
	}

	// Define preferred attributes that are good for filtering/aggregation
	preferredAttributes := map[string]bool{
		// Language and runtime
		"telemetry.sdk.language":  true,
		"telemetry.sdk.name":      true,
		"telemetry.sdk.version":   true,
		"process.runtime.name":    true,
		"process.runtime.version": true,

		// Kubernetes and container info
		"k8s.cluster.name":     true,
		"k8s.namespace.name":   true,
		"k8s.deployment.name":  true,
		"k8s.pod.name":         false, // Pod names are unique, skip
		"k8s.node.name":        true,
		"container.image.name": true,
		"container.image.tag":  true,
		"container.name":       false, // Container names are unique, skip

		// Service info
		"service.name":      true,
		"service.version":   true,
		"service.namespace": true,

		// Environment and deployment
		"deployment.environment": true,
		"environment":            true,
		"env":                    true,
		"stage":                  true,

		// Host info
		"host.name": true,
		"host.arch": true,

		// Cloud info
		"cloud.provider":          true,
		"cloud.region":            true,
		"cloud.availability_zone": true,

		// Application type indicators
		"http.method":      true,
		"http.scheme":      true,
		"db.system":        true,
		"messaging.system": true,
		"rpc.system":       true,
		"faas.provider":    true,
	}

	for key, strValue := range rawAttrs {
		// Skip high-cardinality attributes
		if highCardinalityKeys[key] {
			continue
		}

		// Skip empty values
		if strValue == "" {
			continue
		}

		// For preferred attributes or any attributes not explicitly marked as high-cardinality
		if preferredAttributes[key] || !highCardinalityKeys[key] {
			// Handle aggregation: if key already exists, keep the existing value
			// This ensures one value per label key across all spans of a service
			if existingValue, exists := labels[key]; exists {
				// Keep the existing value (first wins strategy for aggregation)
				_ = existingValue // Acknowledge we're intentionally keeping the existing value
			} else {
				labels[key] = strValue
			}
		}
	}
}

// detectApplicationType intelligently detects application type based on telemetry data
func (t *TraceServiceMapBuilder) detectApplicationType(span TraceSpan, attrs *SpanAttributes, rawAttrs map[string]string, labels map[string]string) string {
	// NOTE: db.system and messaging.system indicate what the service is CONNECTING TO, not what the service itself is
	// Prioritize language/runtime detection over infrastructure attributes

	// Service name pattern detection (highest priority for database services)
	serviceName := strings.ToLower(span.WorkloadName)
	if serviceName == "" {
		if val, ok := rawAttrs["service.name"]; ok {
			serviceName = strings.ToLower(val)
		}
	}

	// Language/Runtime detection (PRIORITY 1: Determine what the application IS)
	// Check telemetry.sdk.language first
	if sdkLang, ok := labels["telemetry.sdk.language"]; ok && sdkLang != "" {
		switch strings.ToLower(sdkLang) {
		case "java":
			return "java"
		case "python":
			return "python"
		case "javascript", "nodejs", "node":
			return "nodejs"
		case "go", "golang":
			return "golang"
		case "dotnet", "csharp", "c#":
			return "dotnet"
		case "php":
			return "php"
		case "ruby":
			return "ruby"
		}
	}

	// Check process.runtime.name
	if runtimeName, ok := labels["process.runtime.name"]; ok && runtimeName != "" {
		switch strings.ToLower(runtimeName) {
		case "node", "nodejs":
			return "nodejs"
		case "python":
			return "python"
		case "go":
			return "golang"
		case "java":
			return "java"
		case "dotnet", ".net":
			return "dotnet"
		case "php":
			return "php"
		case "ruby":
			return "ruby"
		}
	}

	// Check language label (legacy)
	language := labels["language"]
	if language != "" {
		switch strings.ToLower(language) {
		case "java":
			return "java"
		case "python":
			return "python"
		case "javascript", "nodejs", "node":
			return "nodejs"
		case "go", "golang":
			return "golang"
		case "dotnet", "csharp", "c#":
			return "dotnet"
		case "php":
			return "php"
		case "ruby":
			return "ruby"
		}
	}

	// Messaging system detection (ONLY if service name indicates it IS a messaging service)
	// messaging.system indicates what messaging system the service is USING, not what it IS
	// Only return messaging type if the service name itself contains the messaging system name
	if attrs.MessagingSystem != "" {
		msgSystem := strings.ToLower(attrs.MessagingSystem)
		// Only classify as messaging service if the service name contains the messaging system
		if strings.Contains(serviceName, msgSystem) {
			switch msgSystem {
			case "kafka":
				return "kafka"
			case "rabbitmq":
				return "rabbitmq"
			case "activemq":
				return "activemq"
			case "nats":
				return "nats"
			case "pulsar":
				return "pulsar"
			case "rocketmq":
				return "rocketmq"
			}
		}
	}

	// Database services - only detect if the service name itself indicates it's a database
	dbPatterns := map[string]string{
		"postgres":      "postgres",
		"postgresql":    "postgres",
		"redis":         "redis",
		"mysql":         "mysql",
		"mongodb":       "mongodb",
		"mongo":         "mongodb",
		"elasticsearch": "elasticsearch",
		"elastic":       "elasticsearch",
		"clickhouse":    "clickhouse",
	}

	for pattern, appType := range dbPatterns {
		if strings.Contains(serviceName, pattern) {
			return appType
		}
	}

	// Pattern-based detection for other services (middleware, message queues, etc.)
	patterns := map[string]string{
		"cassandra":  "cassandra",
		"opensearch": "opensearch",
		"memcached":  "memcached",
		"rabbitmq":   "rabbitmq",
		"kafka":      "kafka",
		"zookeeper":  "zookeeper",
		"nginx":      "nginx",
		"envoy":      "envoy",
		"prometheus": "prometheus",
		"victoria":   "victoria-metrics",
		"pgbouncer":  "pgbouncer",
		"keydb":      "keydb",
		"valkey":     "valkey",
		"dragonfly":  "dragonfly",
		"nats":       "nats",
	}

	for pattern, appType := range patterns {
		if strings.Contains(serviceName, pattern) {
			return appType
		}
	}

	// Check span names for additional clues
	spanName := strings.ToLower(span.SpanName)
	for pattern, appType := range patterns {
		if strings.Contains(spanName, pattern) {
			return appType
		}
	}

	// AWS Service detection
	if strings.Contains(serviceName, "rds") || strings.Contains(serviceName, "aws-rds") {
		return "aws-rds"
	}
	if strings.Contains(serviceName, "elasticache") || strings.Contains(serviceName, "aws-elasticache") {
		return "aws-elasticache"
	}

	// Protocol-based detection as fallback
	if attrs.HTTPStatusCode > 0 || strings.Contains(spanName, "http") {
		// This is likely an HTTP service, determine language if possible
		return t.detectHTTPServiceType(labels, rawAttrs)
	}

	return "" // Unknown
}

// detectHTTPServiceType tries to detect the application type for HTTP services
func (t *TraceServiceMapBuilder) detectHTTPServiceType(labels map[string]string, rawAttrs map[string]string) string {
	// Check for framework-specific attributes
	if val, ok := rawAttrs["http.server_name"]; ok {
		serverName := strings.ToLower(val)
		if strings.Contains(serverName, "nginx") {
			return "nginx"
		}
		if strings.Contains(serverName, "envoy") {
			return "envoy"
		}
	}

	// Fall back to language detection for HTTP services
	if lang := labels["language"]; lang != "" {
		switch strings.ToLower(lang) {
		case "java":
			return "java"
		case "python":
			return "python"
		case "javascript", "nodejs", "node":
			return "nodejs"
		case "go", "golang":
			return "golang"
		case "dotnet", "csharp", "c#":
			return "dotnet"
		case "php":
			return "php"
		case "ruby":
			return "ruby"
		}
	}

	return "" // Unknown HTTP service
}

// trackExternalService tracks external services based on dependency information
func (t *TraceServiceMapBuilder) trackExternalService(externalServices map[string]*ExternalServiceInfo, dep *ServiceDependency, span TraceSpan, attrs *SpanAttributes) {
	// Only track if this is actually an external dependency
	if !t.isExternalDependency(dep.DependencyType) {
		return
	}

	// Skip if this external service matches exclusion filters
	if len(t.excludeFilters) > 0 {
		slog.Info("Checking exclusion for external service",
			"target", dep.Target,
			"dep_type", dep.DependencyType,
			"protocol", dep.Protocol,
			"db_system", attrs.DBSystem,
			"messaging_system", attrs.MessagingSystem,
			"http_host", attrs.HTTPHost)
	}

	if t.shouldExcludeExternalService(span.SpanAttributes, attrs) {
		slog.Info("EXCLUDED external service",
			"target", dep.Target,
			"reason", "matched exclude filter")
		return
	}

	slog.Info("TRACKING external service (not excluded)",
		"target", dep.Target,
		"http_host", attrs.HTTPHost,
		"protocol", dep.Protocol)

	targetName := dep.Target

	// Extract the actual service name from target if it has a prefix
	if strings.Contains(targetName, ":Service:") {
		parts := strings.Split(targetName, ":Service:")
		if len(parts) > 1 {
			targetName = parts[1]
		}
	}

	if _, exists := externalServices[targetName]; !exists {
		externalServices[targetName] = &ExternalServiceInfo{
			Name:           targetName,
			Protocol:       dep.Protocol,
			DependencyType: dep.DependencyType,
			Environment:    dep.Environment,
			CallCount:      0,
			ErrorCount:     0,
			Applications:   make(map[string]bool),
		}
	}
}

// shouldExcludeDependency checks if a dependency should be excluded based on protocol/type
func (t *TraceServiceMapBuilder) shouldExcludeDependency(dep *ServiceDependency) bool {
	if len(t.excludeFilters) == 0 {
		return false
	}

	// Check each exclusion filter against the dependency's protocol and type
	for _, filter := range t.excludeFilters {
		operatorStr := string(filter.Operator)

		// Check protocol (e.g., "REDIS", "KAFKA")
		if filter.Key == "messaging.system" && operatorStr == "_eq" {
			if strings.EqualFold(dep.Protocol, filter.Value) {
				slog.Debug("Excluding external service by protocol",
					"target", dep.Target,
					"protocol", dep.Protocol,
					"filter_value", filter.Value)
				return true
			}
		}

		// Check db.system by looking at protocol
		if filter.Key == "db.system" && operatorStr == "_eq" {
			if strings.EqualFold(dep.Protocol, filter.Value) {
				slog.Debug("Excluding external service by db.system",
					"target", dep.Target,
					"protocol", dep.Protocol,
					"filter_value", filter.Value)
				return true
			}
		}
	}

	return false
}

// shouldExcludeExternalService checks if an external service should be excluded based on exclusion filters
func (t *TraceServiceMapBuilder) shouldExcludeExternalService(spanAttrs map[string]string, attrs *SpanAttributes) bool {
	if len(t.excludeFilters) == 0 {
		return false
	}

	// Check each exclusion filter - if ANY matches, exclude the service
	for _, filter := range t.excludeFilters {
		attrValue, exists := spanAttrs[filter.Key]
		if !exists {
			continue
		}

		// Check if the filter matches
		matches := false
		operatorStr := string(filter.Operator)
		switch operatorStr {
		case "_eq":
			matches = attrValue == filter.Value
		case "_neq":
			matches = attrValue != filter.Value
		case "_like":
			matches = strings.Contains(attrValue, strings.Trim(filter.Value, ".*"))
		default:
			continue
		}

		if matches {
			slog.Info("Excluding external service",
				"filter_key", filter.Key,
				"filter_value", filter.Value,
				"attr_value", attrValue,
				"operator", operatorStr)
			return true // Exclude this service
		}
	}

	return false
}

// isExternalDependency checks if a dependency type indicates an external service
func (t *TraceServiceMapBuilder) isExternalDependency(depType string) bool {
	externalTypes := map[string]bool{
		"messaging_system": true,
		"net_peer":         true,
		"db_connection":    true,
		"http_external":    true,
		"http_client":      true, // HTTP client calls to services that don't emit spans
		"external_address": true,
		"ip_address":       true,
	}
	return externalTypes[depType]
}

/*
// detectExternalServiceType determines the application type for external services (unused - kept for compatibility)
func (t *TraceServiceMapBuilder) detectExternalServiceType(serviceName, protocol, depType string) string {
	serviceName = strings.ToLower(serviceName)
	protocol = strings.ToUpper(protocol)

	// Primary detection: Pattern matching on service name
	// Order matters: more specific patterns first
	specificPatterns := []struct {
		pattern string
		appType string
	}{
		// Most specific hosts and APIs first
		{"api.github.com", "github-api"},
		{"github.com", "github-api"},

		// Database services (specific names first)
		{"postgresql", "postgres"},
		{"postgres", "postgres"},
		{"elasticsearch", "elasticsearch"},
		{"mongodb", "mongodb"},
		{"clickhouse", "clickhouse"},
		{"cassandra", "cassandra"},
		{"elastic", "elasticsearch"},
		{"redis", "redis"},
		{"mysql", "mysql"},
		{"mongo", "mongodb"},

		// Generic patterns (after specific ones)
		{"github", "github-api"},
		{"internal", "internal-service"},
		{"local", "local-service"},
	}

	for _, sp := range specificPatterns {
		if strings.Contains(serviceName, sp.pattern) {
			return sp.appType
		}
	}

	// Fallback: Protocol-based detection
	switch protocol {
	case "REDIS":
		return "redis"
	case "GRPC":
		return "grpc-service"
	case "HTTP":
		// Generic HTTP external service
		if strings.Contains(serviceName, "api") {
			return "external-api"
		}
		return "http-service"
	}

	// Final fallback: Default external service type
	return "external-service"
}

// buildExternalServiceDownstreams creates downstream links for external services
func (t *TraceServiceMapBuilder) buildExternalServiceDownstreams(dependencyMap map[string]*ServiceDependency, serviceStats map[string]*serviceMetrics, externalServiceName string, durationMinutes float64, earliestTime, latestTime time.Time) []DownstreamLink {
	var downstreams []DownstreamLink

	// Find all dependencies where this external service is the target
	for _, dep := range dependencyMap {
		// Extract service name from target if it has a prefix
		targetName := dep.Target
		if strings.Contains(targetName, ":Service:") {
			parts := strings.Split(targetName, ":Service:")
			if len(parts) > 1 {
				targetName = parts[1]
			}
		}

		if targetName == externalServiceName {
			// This service depends on our external service
			sourceName := dep.Source
			sourceNamespace := ""

			// Look up the source service's namespace
			if sourceStats, exists := serviceStats[sourceName]; exists {
				sourceNamespace = sourceStats.Namespace
			}

			// Calculate metrics
			dep.AvgDuration = dep.TotalDuration / float64(dep.CallCount) / t.config.NanosecondsToMilliseconds
			dep.ErrorRate = float64(dep.ErrorCount) / float64(dep.CallCount) * 100
			reqPerMin := float64(dep.CallCount) / durationMinutes

			downstreamLink := DownstreamLink{
				Id: ServiceApplicationId{
					Name:      sourceName,
					Kind:      "Service",
					Namespace: sourceNamespace,
				},
				Status:       t.getLinkStatusCode(dep.ErrorRate),
				Stats:        []string{fmt.Sprintf("%.1f req/min", reqPerMin), fmt.Sprintf("%.1fms", dep.AvgDuration)},
				Weight:       float64(dep.CallCount),
				Latency:      dep.AvgDuration,
				RequestCount: float64(dep.CallCount),
				FailureCount: float64(dep.ErrorCount),
				Protocol:     dep.Protocol,
				DrillDown:    t.createLinkDrillDown(dep, earliestTime, latestTime),
			}

			downstreams = append(downstreams, downstreamLink)
		}
	}

	return downstreams
}

// buildExternalServiceLabels creates labels for external services
func (t *TraceServiceMapBuilder) buildExternalServiceLabels(extService *ExternalServiceInfo) map[string]string {
	labels := make(map[string]string)

	// External service name as identifier
	labels["external_service"] = extService.Name

	if extService.Protocol != "" && extService.Protocol != "Unknown" {
		labels["protocol"] = extService.Protocol
	}

	return labels
}
*/

// detectDependencyFromCustomAttributes checks for common custom caller patterns
// This is customer-agnostic and works with any custom attribute structure
func (t *TraceServiceMapBuilder) detectDependencyFromCustomAttributes(span TraceSpan, attrs *SpanAttributes) (caller string, found bool) {
	// Priority 1: Check common caller attribute patterns (customer-agnostic)
	// These patterns cover various naming conventions used across different customers
	callerPatterns := []string{
		"custom.p44.caller",       // P44's pattern (from P44 custom attributes)
		"custom.caller",           // Generic pattern
		"custom.source.service",   // Alternative pattern
		"custom.upstream.service", // Alternative pattern
		"caller",                  // Direct attribute (no custom prefix)
		"source.service",          // OTel-style
		"upstream.service",        // Alternative OTel-style
	}

	for _, pattern := range callerPatterns {
		if val, ok := span.SpanAttributes[pattern]; ok && val != "" {
			slog.Debug("Found explicit caller from custom attributes",
				"pattern", pattern,
				"caller", val,
				"target_service", attrs.ServiceName,
				"span_id", span.SpanID,
				"trace_id", span.TraceID)
			return val, true
		}
	}

	return "", false
}

// Utility methods for ServiceMap
func (sm *ServiceMap) ToJSON() ([]byte, error) {
	return json.MarshalIndent(sm, "", "  ")
}

func (sm *ServiceMap) GetTopServices(limit int) []ServiceApplication {
	if limit <= 0 || limit > len(sm.Applications) {
		return sm.Applications
	}
	return sm.Applications[:limit]
}

func (sm *ServiceMap) GetServiceByName(name string) *ServiceApplication {
	for _, app := range sm.Applications {
		if app.Id.Name == name {
			return &app
		}
	}
	return nil
}

// filterToUpstreamServices filters the service map to only include the target service
// and all services that call it (upstreams), transitively
func filterToUpstreamServices(serviceMap *ServiceMap, targetService string) *ServiceMap {
	if serviceMap == nil || len(serviceMap.Applications) == 0 {
		return serviceMap
	}

	// Build a map for fast lookup
	appMap := make(map[string]*ServiceApplication)
	for i := range serviceMap.Applications {
		app := &serviceMap.Applications[i]
		appMap[app.Id.Name] = app
	}

	// Check if target service exists
	if _, exists := appMap[targetService]; !exists {
		// Log sample services to help debug
		serviceNames := make([]string, 0, len(appMap))
		externalServices := make([]string, 0)
		for name, app := range appMap {
			serviceNames = append(serviceNames, name)
			if app.Id.Kind == "ExternalService" {
				externalServices = append(externalServices, name)
			}
		}
		sampleSize := 10
		if len(serviceNames) < sampleSize {
			sampleSize = len(serviceNames)
		}
		slog.Warn("Target service not found in service map",
			"target_service", targetService,
			"total_services", len(serviceMap.Applications),
			"external_services", externalServices,
			"sample_services", serviceNames[:sampleSize])
		return &ServiceMap{
			Applications: []ServiceApplication{},
			GeneratedAt:  serviceMap.GeneratedAt,
		}
	}

	// Start with target service and traverse upstreams recursively
	included := make(map[string]bool)
	toProcess := []string{targetService}
	included[targetService] = true

	// BFS traversal to find all upstream services (services that call the target, transitively)
	// Since "Downstream" = services calling me, we traverse Downstreams backwards
	for len(toProcess) > 0 {
		currentService := toProcess[0]
		toProcess = toProcess[1:]

		currentApp, exists := appMap[currentService]
		if !exists {
			continue
		}

		// Traverse Downstreams (services that call this service)
		for _, downstream := range currentApp.Downstreams {
			callerName := downstream.Id.Name
			if callerName != "" && !included[callerName] {
				included[callerName] = true
				toProcess = append(toProcess, callerName)
			}
		}
	}

	// Build filtered service map with only included services
	filteredApps := make([]ServiceApplication, 0, len(included))
	for _, app := range serviceMap.Applications {
		if included[app.Id.Name] {
			filteredApps = append(filteredApps, app)
		}
	}

	slog.Info("filterToUpstreamServices completed",
		"target_service", targetService,
		"original_count", len(serviceMap.Applications),
		"filtered_count", len(filteredApps),
		"upstream_services", len(filteredApps)-1)

	return &ServiceMap{
		Applications: filteredApps,
		GeneratedAt:  serviceMap.GeneratedAt,
		K8sMetadata:  serviceMap.K8sMetadata, // Preserve K8s metadata when filtering
	}
}

// ExtractServiceNameFromUpstreamId extracts the service name from upstream ID
// Format: ":Service:name" or ":ExternalService:name"
func ExtractServiceNameFromUpstreamId(upstreamId string) string {
	parts := strings.Split(upstreamId, ":")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

// ParseUpstreamId parses upstream ID format and returns name and kind
// Format: "namespace:Kind:name" (e.g., ":Service:my-service" or "default:ExternalService:redis")
func ParseUpstreamId(id string) (name, kind string) {
	parts := strings.Split(id, ":")
	if len(parts) >= 3 {
		// Format: namespace:Kind:name
		return parts[2], parts[1]
	}
	if len(parts) == 2 {
		// Format: Kind:name (no namespace)
		return parts[1], parts[0]
	}
	// Single value, assume it's just the name
	return id, "Service"
}

// extractK8sMetadataFromSpans extracts K8s infrastructure metadata from the spans
func (t *TraceServiceMapBuilder) extractK8sMetadataFromSpans() *K8sInfrastructureMetadata {
	if len(t.spans) == 0 {
		return nil
	}

	metadata := &K8sInfrastructureMetadata{
		Clusters:   make(map[string]*K8sClusterInfo),
		Namespaces: make(map[string]*K8sNamespaceInfo),
		Pods:       make(map[string]*K8sPodInfo),
		Nodes:      make(map[string]*K8sNodeInfo),
	}

	for _, span := range t.spans {
		// Parse span attributes to extract K8s information
		var clusterName, namespace, podName, nodeName, environment, serviceName string

		if span.SpanAttributes != nil {
			for key, value := range span.SpanAttributes {
				switch key {
				case "k8s.cluster.name", "k8s_cluster":
					clusterName = value
				case "k8s.namespace.name", "k8s.namespace":
					namespace = value
				case "k8s.pod.name", "k8s.pod":
					podName = value
				case "k8s.node.name", "k8s.node":
					nodeName = value
				case "deployment.environment", "environment":
					environment = value
				case "service.name":
					serviceName = value
				}
			}
		}

		// Also check workload namespace as fallback
		if namespace == "" {
			namespace = span.WorkloadNamespace
		}

		// Add cluster if found
		if clusterName != "" {
			key := clusterName
			if _, exists := metadata.Clusters[key]; !exists {
				metadata.Clusters[key] = &K8sClusterInfo{
					Name:        clusterName,
					Environment: environment,
				}
			}
		}

		// Add namespace if found
		if namespace != "" {
			key := fmt.Sprintf("%s:%s", clusterName, namespace)
			if _, exists := metadata.Namespaces[key]; !exists {
				metadata.Namespaces[key] = &K8sNamespaceInfo{
					Name:        namespace,
					Cluster:     clusterName,
					Environment: environment,
				}
			}
		}

		// Add pod if found
		if podName != "" {
			key := fmt.Sprintf("%s:%s:%s", clusterName, namespace, podName)
			if _, exists := metadata.Pods[key]; !exists {
				metadata.Pods[key] = &K8sPodInfo{
					Name:        podName,
					Namespace:   namespace,
					Node:        nodeName,
					ServiceName: serviceName,
					Environment: environment,
				}
			}
		}

		// Add node if found
		if nodeName != "" {
			key := fmt.Sprintf("%s:%s", clusterName, nodeName)
			if _, exists := metadata.Nodes[key]; !exists {
				metadata.Nodes[key] = &K8sNodeInfo{
					Name:        nodeName,
					Cluster:     clusterName,
					Environment: environment,
				}
			}
		}
	}

	// Return nil if no K8s metadata was found
	if len(metadata.Clusters) == 0 && len(metadata.Namespaces) == 0 &&
		len(metadata.Pods) == 0 && len(metadata.Nodes) == 0 {
		return nil
	}

	slog.Info("Extracted K8s metadata from spans",
		"clusters", len(metadata.Clusters),
		"namespaces", len(metadata.Namespaces),
		"pods", len(metadata.Pods),
		"nodes", len(metadata.Nodes))

	return metadata
}
