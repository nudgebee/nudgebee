package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/encoding/protojson"
)

// getGCPAlertPolicies fetches all alert policies from GCP Cloud Monitoring
func getGCPAlertPolicies(ctx providers.CloudProviderContext, account providers.Account) (providers.ListEventRules, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ListEventRules{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := monitoring.NewAlertPolicyClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ListEventRules{}, fmt.Errorf("failed to create alert policy client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close alert policy client", "error", cerr)
		}
	}()

	var eventRules []providers.EventRule

	req := &monitoringpb.ListAlertPoliciesRequest{
		Name: fmt.Sprintf("projects/%s", session.ProjectId),
	}

	it := client.ListAlertPolicies(ctx.GetContext(), req)
	for {
		policy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping alert policies — API disabled or permission denied", "error", err, "projectId", session.ProjectId)
				break
			}
			ctx.GetLogger().Error("failed to list alert policies", "error", err, "projectId", session.ProjectId)
			return providers.ListEventRules{}, fmt.Errorf("failed to list alert policies: %w", err)
		}

		// Skip disabled policies
		if policy.GetEnabled() != nil && !policy.GetEnabled().GetValue() {
			ctx.GetLogger().Debug("skipping disabled alert policy", "policyName", policy.GetName())
			continue
		}

		// Extract policy details
		policyName := policy.GetDisplayName()
		if policyName == "" {
			policyName = extractResourceNameFromPath(policy.GetName())
		}

		// Serialize policy to JSON for storage in Expr field
		exprJson, err := protojson.Marshal(policy)
		if err != nil {
			ctx.GetLogger().Error("failed to marshal alert policy", "error", err, "policyName", policyName)
			continue
		}

		// Extract service name and metric from conditions
		serviceName := "Cloud Monitoring"
		metricType := ""
		resourceType := ""
		if len(policy.Conditions) > 0 {
			condition := policy.Conditions[0]
			if threshold := condition.GetConditionThreshold(); threshold != nil {
				filter := threshold.GetFilter()
				// Extract metric type from filter (e.g., metric.type="compute.googleapis.com/instance/cpu/utilization")
				if metricStart := strings.Index(filter, "metric.type"); metricStart != -1 {
					metricPart := filter[metricStart:]
					if startQuote := strings.Index(metricPart, "\""); startQuote != -1 {
						metricPart = metricPart[startQuote+1:]
						if endQuote := strings.Index(metricPart, "\""); endQuote != -1 {
							metricType = metricPart[:endQuote]
							serviceName = extractServiceNameFromMetric(metricType)
						}
					}
				}

				// Extract resource type from filter (e.g., resource.type="gce_instance")
				if resourceStart := strings.Index(filter, "resource.type"); resourceStart != -1 {
					resourcePart := filter[resourceStart:]
					if startQuote := strings.Index(resourcePart, "\""); startQuote != -1 {
						resourcePart = resourcePart[startQuote+1:]
						if endQuote := strings.Index(resourcePart, "\""); endQuote != -1 {
							resourceType = resourcePart[:endQuote]
						}
					}
				}
			}
		}

		// Extract duration from condition
		var duration time.Duration
		if len(policy.Conditions) > 0 {
			condition := policy.Conditions[0]
			if threshold := condition.GetConditionThreshold(); threshold != nil {
				if dur := threshold.GetDuration(); dur != nil {
					duration = dur.AsDuration()
				}
			}
		}

		// Determine severity from policy metadata or default to critical
		severity := providers.EventDefinitionSeverityCritical
		if policy.GetSeverity() != monitoringpb.AlertPolicy_SEVERITY_UNSPECIFIED {
			severity = gcpSeverityToEventSeverity(policy.GetSeverity())
		}

		eventRules = append(eventRules, providers.EventRule{
			Name:        policyName,
			Description: policy.GetDocumentation().GetContent(),
			Summary:     fmt.Sprintf("GCP Alert Policy '%s' for %s", policyName, serviceName),
			Expr:        string(exprJson),
			Source:      "GCP_Metric_Alert",
			Severity:    severity,
			Category:    serviceName,
			Duration:    duration,
			Labels: map[string]string{
				"gcp_project":       session.ProjectId,
				"gcp_policy_name":   policyName,
				"gcp_policy_id":     policy.GetName(),
				"gcp_service_name":  serviceName,
				"gcp_metric_type":   metricType,
				"gcp_resource_type": resourceType,
			},
		})

		ctx.GetLogger().Debug("added alert policy rule", "policyName", policyName, "serviceName", serviceName)
	}

	ctx.GetLogger().Info("fetched GCP alert policies", "count", len(eventRules), "projectId", session.ProjectId)
	return providers.ListEventRules{Items: eventRules}, nil
}

// getGCPAlertIncidents fetches alert incidents by querying Cloud Logging for alert notifications
// GCP doesn't provide a simple "list firing alerts" API, so we query Cloud Logging entries
// that are generated when alerts fire. This requires:
// 1. Alert policies must have notification channels configured
// 2. Cloud Logging must be enabled (default in GCP)
// 3. Service account needs roles/logging.viewer permission
func getGCPAlertIncidents(ctx providers.CloudProviderContext, account providers.Account, query providers.ListEventRequest) (providers.ListEventResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ListEventResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	// Set default time range if not provided
	endTime := time.Now()
	if query.EndDate != nil {
		endTime = *query.EndDate
	}

	startTime := endTime.Add(-24 * time.Hour) // Default: last 24 hours
	if query.StartDate != nil {
		startTime = *query.StartDate
	}

	ctx.GetLogger().Info("querying GCP alert incidents via Cloud Logging",
		"projectId", session.ProjectId,
		"startTime", startTime.Format(time.RFC3339),
		"endTime", endTime.Format(time.RFC3339))

	// Query Cloud Logging for alert notifications
	// We look for two types of log entries:
	// 1. Alert policy state change logs (when alerts fire or resolve)
	// 2. Notification channel delivery logs

	// Build the query filter for Cloud Logging
	// GCP creates logs when alerts fire and notifications are sent:
	// 1. Alert policy state change logs (when alerts fire or resolve)
	// 2. Notification channel delivery logs
	// 3. Incident data in jsonPayload
	// Keep the filter targeted to avoid drowning real incidents in unrelated monitoring logs
	logFilter := `
		(
			protoPayload.serviceName="monitoring.googleapis.com"
		)
		OR (
			jsonPayload.incident.policy_name!=""
		)
		OR (
			resource.type="alerting_policy"
		)
	`

	// Use the existing Cloud Logging query function
	logsQuery := providers.QueryLogsRequest{
		StartTime:   &startTime,
		EndTime:     &endTime,
		QueryString: logFilter,
	}

	limit := int64(1000)
	logsQuery.Limit = &limit

	logsResponse, err := queryGcloudLogs(ctx, account, logsQuery)
	if err != nil {
		ctx.GetLogger().Warn("failed to query cloud logs for alert incidents", "error", err)
		// Don't fail - just return empty. Logs might not be available or permissions missing
		return providers.ListEventResponse{
			Items:   []providers.Event{},
			Summary: []providers.EventSummary{},
		}, nil
	}

	var events []providers.Event
	seenIncidents := make(map[string]bool) // Deduplicate by incident ID

	// Parse log entries to extract alert incidents
	for _, logResult := range logsResponse.Results {
		// Each log result has fields as a map
		incident := extractIncidentFromLogEntry(ctx, logResult, session.ProjectId)
		if incident != nil {
			// Deduplicate by incident ID
			if seenIncidents[incident.EventId] {
				continue
			}
			seenIncidents[incident.EventId] = true
			events = append(events, *incident)
		}
	}

	ctx.GetLogger().Info("fetched GCP alert incidents from Cloud Logging",
		"count", len(events),
		"projectId", session.ProjectId,
		"logEntriesScanned", len(logsResponse.Results))

	return providers.ListEventResponse{
		Items:   events,
		Summary: []providers.EventSummary{},
	}, nil
}

// extractIncidentFromLogEntry extracts alert incident information from a Cloud Logging entry
func extractIncidentFromLogEntry(ctx providers.CloudProviderContext, logEntry providers.LogMessage, projectID string) *providers.Event {
	// Parse the log message (usually JSON format)
	var logData map[string]interface{}
	if err := common.UnmarshalJson([]byte(logEntry.Message), &logData); err != nil {
		// Not JSON or parse error - skip this entry
		ctx.GetLogger().Debug("skipping non-JSON log entry", "error", err)
		return nil
	}

	// Try to extract incident data from jsonPayload.incident or top-level incident
	var policyName, policyID, summary, state, resourceID, resourceType, region string
	var conditionName string
	severity := providers.EventSeverityHigh

	// Check for incident in jsonPayload
	var incident map[string]interface{}
	if jsonPayload, ok := logData["jsonPayload"].(map[string]interface{}); ok {
		if inc, ok := jsonPayload["incident"].(map[string]interface{}); ok {
			incident = inc
		}
	}

	// Check for incident at top level
	if incident == nil {
		if inc, ok := logData["incident"].(map[string]interface{}); ok {
			incident = inc
		}
	}

	// Extract incident fields
	if incident != nil {
		if name, ok := incident["policy_name"].(string); ok {
			policyName = name
			policyID = name
		}
		if sum, ok := incident["summary"].(string); ok {
			summary = sum
		}
		if st, ok := incident["state"].(string); ok {
			state = st
		}
		if cond, ok := incident["condition_name"].(string); ok {
			conditionName = cond
		}

		// Extract resource information
		if resource, ok := incident["resource"].(map[string]interface{}); ok {
			if id, ok := resource["resource_id"].(string); ok {
				resourceID = id
			}
			if typ, ok := resource["type"].(string); ok {
				resourceType = typ
			}
			if labels, ok := resource["labels"].(map[string]interface{}); ok {
				if reg, ok := labels["region"].(string); ok {
					region = reg
				} else if zone, ok := labels["zone"].(string); ok {
					// Extract region from zone
					if idx := strings.LastIndex(zone, "-"); idx > 0 {
						region = zone[:idx]
					}
				}
			}
		}
	}

	// Check for protoPayload (audit logs)
	if protoPayload, ok := logData["protoPayload"].(map[string]interface{}); ok {
		// Extract policy name from resource name
		if resourceName, ok := protoPayload["resourceName"].(string); ok {
			if policyName == "" {
				policyName = extractResourceNameFromPath(resourceName)
			}
			policyID = resourceName
		}

		// Extract request/response for more details
		if request, ok := protoPayload["request"].(map[string]interface{}); ok {
			if alertPolicy, ok := request["alertPolicy"].(map[string]interface{}); ok {
				if displayName, ok := alertPolicy["displayName"].(string); ok {
					policyName = displayName
				}
			}
		}
	}

	// Also check for alert policy name in labels
	for _, label := range logEntry.Labels {
		if label.Label == "alert_policy" || label.Label == "policy_name" {
			if policyName == "" {
				policyName = label.Value
			}
		}
	}

	// Skip if no policy information found
	if policyName == "" && policyID == "" {
		return nil
	}

	// Determine event status based on state
	eventStatus := providers.EventStatusFiring
	if state == "closed" || state == "resolved" {
		eventStatus = providers.EventStatusResolved
	}

	// Extract service name from resource type or policy name
	serviceName := "Cloud Monitoring"
	if resourceType != "" {
		serviceName = mapResourceTypeToServiceName(resourceType)
	}

	// Convert timestamp from milliseconds to time.Time
	timestamp := time.Unix(0, logEntry.Timestamp*int64(time.Millisecond))

	// Build event ID from policy and timestamp
	eventID := fmt.Sprintf("%s/%s", policyID, timestamp.Format(time.RFC3339))

	// Build description
	description := summary
	if description == "" {
		description = fmt.Sprintf("Alert policy '%s' triggered", policyName)
		if conditionName != "" {
			description += fmt.Sprintf(" - Condition: %s", conditionName)
		}
	}

	// Build labels
	labels := map[string]string{
		"gcp_project":       projectID,
		"gcp_policy_name":   policyName,
		"gcp_policy_id":     policyID,
		"gcp_service_name":  serviceName,
		"gcp_resource_type": resourceType,
		"gcp_state":         state,
	}

	if conditionName != "" {
		labels["gcp_condition_name"] = conditionName
	}

	return &providers.Event{
		Title:               fmt.Sprintf("%s: %s", serviceName, policyName),
		EventName:           policyName,
		Description:         description,
		Date:                timestamp,
		EventSource:         "GCP_Metric_Alert",
		EventId:             eventID,
		EventStatus:         eventStatus,
		EventSeverity:       severity,
		ResourceType:        resourceType,
		ResourceId:          resourceID,
		ResourceRegion:      region,
		ResourceServiceName: serviceName,
		Raw:                 logData,
		Labels:              labels,
	}
}

// mapResourceTypeToServiceName maps GCP monitored resource types to service names.
// This is the canonical mapping used by both v3 alerts API and Pub/Sub event paths.
// Service names must match gcloudServiceMap keys (case-insensitive) for metric/resource queries to work.
func mapResourceTypeToServiceName(resourceType string) string {
	typeToService := map[string]string{
		"gce_instance":         ServiceNameCompute,
		"cloudsql_database":    ServiceNameSQL,
		"cloud_run_revision":   "Cloud Run",
		"k8s_cluster":          "Kubernetes Engine",
		"k8s_node":             "Kubernetes Engine",
		"k8s_pod":              "Kubernetes Engine",
		"k8s_container":        "Kubernetes Engine",
		"gke_cluster":          "Kubernetes Engine",
		"gke_nodepool":         "Kubernetes Engine",
		"gcs_bucket":           "Cloud Storage",
		"cloud_function":       "Cloud Functions",
		"https_lb_rule":        ServiceNameLoadBalancing,
		"tcp_ssl_proxy_rule":   ServiceNameLoadBalancing,
		"pubsub_topic":         "Cloud Pub/Sub",
		"pubsub_subscription":  "Cloud Pub/Sub",
		"bigquery_dataset":     "BigQuery",
		"bigquery_table":       "BigQuery",
		"vpc_access_connector": "Networking",
	}

	if service, ok := typeToService[resourceType]; ok {
		return service
	}
	return "Cloud Monitoring"
}

// Helper functions

// extractServiceNameFromMetric extracts service name from GCP metric type
// e.g., "compute.googleapis.com/instance/cpu/utilization" -> "Compute Engine"
func extractServiceNameFromMetric(metricType string) string {
	if metricType == "" {
		return "Cloud Monitoring"
	}

	parts := strings.Split(metricType, "/")
	if len(parts) == 0 {
		return "Cloud Monitoring"
	}

	namespace := parts[0]
	switch {
	case strings.Contains(namespace, "compute.googleapis.com"):
		return ServiceNameCompute
	case strings.Contains(namespace, "cloudsql.googleapis.com"):
		return ServiceNameSQL
	case strings.Contains(namespace, "storage.googleapis.com"):
		return "Cloud Storage"
	case strings.Contains(namespace, "run.googleapis.com"):
		return "Cloud Run"
	case strings.Contains(namespace, "container.googleapis.com"):
		return "Kubernetes Engine"
	case strings.Contains(namespace, "cloudfunctions.googleapis.com"):
		return "Cloud Functions"
	case strings.Contains(namespace, "loadbalancing.googleapis.com"):
		return ServiceNameLoadBalancing
	case strings.Contains(namespace, "pubsub.googleapis.com"):
		return "Cloud Pub/Sub"
	case strings.Contains(namespace, "bigquery.googleapis.com"):
		return "BigQuery"
	default:
		return "Cloud Monitoring"
	}
}

// extractResourceNameFromPath extracts the last part of a GCP resource path
// e.g., "projects/my-project/alertPolicies/12345" -> "12345"
func extractResourceNameFromPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

// extractRegionFromFilter extracts region from GCP monitoring filter
// e.g., 'resource.labels.zone="us-central1-a"' -> "us-central1"
func extractRegionFromFilter(filter string) string {
	// Try zone first (compute instances)
	if idx := strings.Index(filter, "resource.labels.zone"); idx != -1 {
		remaining := filter[idx+len("resource.labels.zone"):]
		if startQuote := strings.Index(remaining, "\""); startQuote != -1 {
			remaining = remaining[startQuote+1:]
			if endQuote := strings.Index(remaining, "\""); endQuote != -1 {
				zone := remaining[:endQuote]
				// Extract region from zone (e.g., "us-central1-a" -> "us-central1")
				if lastHyphen := strings.LastIndex(zone, "-"); lastHyphen > 0 {
					return zone[:lastHyphen]
				}
				return zone
			}
		}
	}

	// Try region label
	if idx := strings.Index(filter, "resource.labels.region"); idx != -1 {
		remaining := filter[idx+len("resource.labels.region"):]
		if startQuote := strings.Index(remaining, "\""); startQuote != -1 {
			remaining = remaining[startQuote+1:]
			if endQuote := strings.Index(remaining, "\""); endQuote != -1 {
				return remaining[:endQuote]
			}
		}
	}

	// Try location label
	if idx := strings.Index(filter, "resource.labels.location"); idx != -1 {
		remaining := filter[idx+len("resource.labels.location"):]
		if startQuote := strings.Index(remaining, "\""); startQuote != -1 {
			remaining = remaining[startQuote+1:]
			if endQuote := strings.Index(remaining, "\""); endQuote != -1 {
				return remaining[:endQuote]
			}
		}
	}

	return ""
}

// gcpSeverityToEventSeverity converts GCP alert policy severity to event definition severity
func gcpSeverityToEventSeverity(severity monitoringpb.AlertPolicy_Severity) providers.EventDefinitionSeverity {
	switch severity {
	case monitoringpb.AlertPolicy_CRITICAL, monitoringpb.AlertPolicy_ERROR:
		return providers.EventDefinitionSeverityCritical
	case monitoringpb.AlertPolicy_WARNING:
		return providers.EventDefinitionSeverityWarning
	default:
		return providers.EventDefinitionSeverityCritical
	}
}

// fetchAlertPoliciesOnce fetches all enabled alert policies for an account once
// This avoids making N identical API calls when processing multiple services
func fetchAlertPoliciesOnce(ctx providers.CloudProviderContext, account providers.Account) ([]*monitoringpb.AlertPolicy, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Error("failed to get gcloud session for alert policies", "error", err)
		return nil, err
	}

	client, err := monitoring.NewAlertPolicyClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		ctx.GetLogger().Error("failed to create alert policy client", "error", err)
		return nil, err
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close alert policy client", "error", cerr)
		}
	}()

	var allPolicies []*monitoringpb.AlertPolicy
	req := &monitoringpb.ListAlertPoliciesRequest{
		Name: fmt.Sprintf("projects/%s", session.ProjectId),
	}

	it := client.ListAlertPolicies(ctx.GetContext(), req)
	for {
		policy, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			ctx.GetLogger().Warn("failed to list alert policies", "error", err)
			return nil, err
		}

		// Only include enabled policies
		if policy.GetEnabled() != nil && policy.GetEnabled().GetValue() {
			allPolicies = append(allPolicies, policy)
		}
	}

	ctx.GetLogger().Info("fetched alert policies for account", "policyCount", len(allPolicies), "projectId", session.ProjectId)
	return allPolicies, nil
}

// attachAlertPoliciesToResourcesWithCache attaches pre-fetched alert policies to matching resources
// This is the cached version that accepts policies as a parameter to avoid repeated API calls
func attachAlertPoliciesToResourcesWithCache(ctx providers.CloudProviderContext, resources []providers.Resource, allPolicies []*monitoringpb.AlertPolicy) error {
	if len(resources) == 0 || len(allPolicies) == 0 {
		return nil
	}

	ctx.GetLogger().Info("attaching alert policies to resources", "policyCount", len(allPolicies), "resourceCount", len(resources))

	// Attach relevant policies to each resource
	attachedCount := 0
	for i := range resources {
		resource := &resources[i]
		var matchingPolicies []interface{}

		for _, policy := range allPolicies {
			if policyMatchesResource(policy, resource) {
				// Convert policy to map for storage
				policyJson, err := protojson.Marshal(policy)
				if err != nil {
					ctx.GetLogger().Warn("failed to marshal policy", "error", err, "policyName", policy.GetDisplayName())
					continue
				}
				var policyMap map[string]interface{}
				if err := common.UnmarshalJson(policyJson, &policyMap); err != nil {
					ctx.GetLogger().Warn("failed to unmarshal policy to map", "error", err)
					continue
				}
				matchingPolicies = append(matchingPolicies, policyMap)
			}
		}

		if len(matchingPolicies) > 0 {
			if resource.Meta == nil {
				resource.Meta = make(map[string]interface{})
			}
			resource.Meta["AlertPolicies"] = matchingPolicies
			attachedCount++
			ctx.GetLogger().Debug("attached alert policies to resource", "resourceId", resource.Id, "resourceName", resource.Name, "policyCount", len(matchingPolicies))
		}
	}

	ctx.GetLogger().Info("finished attaching alert policies", "resourcesWithPolicies", attachedCount, "totalResources", len(resources))
	return nil
}

// policyMatchesResource checks if an alert policy applies to a specific resource
func policyMatchesResource(policy *monitoringpb.AlertPolicy, resource *providers.Resource) bool {
	if len(policy.Conditions) == 0 {
		return false
	}

	// Check each condition to see if it targets this resource
	for _, condition := range policy.Conditions {
		var filter string

		// Handle different condition types
		if threshold := condition.GetConditionThreshold(); threshold != nil {
			filter = threshold.GetFilter()
		} else if absent := condition.GetConditionAbsent(); absent != nil {
			// ConditionAbsent: metric absence alerts
			filter = absent.GetFilter()
		} else if matchedLog := condition.GetConditionMatchedLog(); matchedLog != nil {
			// ConditionMatchedLog: log-based alerts
			filter = matchedLog.GetFilter()
		}

		if filter == "" {
			continue
		}

		// Extract resource type from filter
		expectedResourceType := extractResourceTypeFromPolicy(filter)
		if expectedResourceType != "" && !matchesResourceType(expectedResourceType, resource.Type) {
			continue
		}

		// Check if filter mentions this specific resource
		if resourceMatchesFilter(resource, filter) {
			return true
		}

		// If filter doesn't specify a specific resource, it applies to all resources of that type
		if expectedResourceType != "" && matchesResourceType(expectedResourceType, resource.Type) {
			// Check if filter has specific resource constraints
			hasSpecificResourceFilter := strings.Contains(filter, "instance_id") ||
				strings.Contains(filter, "database_id") ||
				strings.Contains(filter, "service_name") ||
				strings.Contains(filter, "cluster_name") ||
				strings.Contains(filter, "function_name") ||
				strings.Contains(filter, "bucket_name")

			// If no specific resource is mentioned, policy applies to all resources of this type
			if !hasSpecificResourceFilter {
				return true
			}
		}
	}

	return false
}

// extractResourceTypeFromPolicy extracts GCP resource type from monitoring filter
func extractResourceTypeFromPolicy(filter string) string {
	if idx := strings.Index(filter, "resource.type"); idx != -1 {
		remaining := filter[idx+len("resource.type"):]
		if startQuote := strings.Index(remaining, "\""); startQuote != -1 {
			remaining = remaining[startQuote+1:]
			if endQuote := strings.Index(remaining, "\""); endQuote != -1 {
				return remaining[:endQuote]
			}
		}
	}
	return ""
}

// matchesResourceType checks if resource type matches the policy's expected type
func matchesResourceType(policyResourceType, resourceType string) bool {
	// Direct match
	if policyResourceType == resourceType {
		return true
	}

	// Map GCP resource types to our resource types
	typeMapping := map[string]string{
		"gce_instance":       "compute.googleapis.com/Instance",
		"cloudsql_database":  "sqladmin.googleapis.com/Instance",
		"cloud_run_revision": "run.googleapis.com/Service",
		"k8s_cluster":        "container.googleapis.com/Cluster",
		"gcs_bucket":         "storage.googleapis.com/Bucket",
		"cloud_function":     "cloudfunctions.googleapis.com/CloudFunction",
		"https_lb_rule":      "compute.googleapis.com/ForwardingRule",
		"pubsub_topic":       "pubsub.googleapis.com/Topic",
		"bigquery_dataset":   "bigquery.googleapis.com/Dataset",
	}

	if mapped, ok := typeMapping[policyResourceType]; ok {
		return mapped == resourceType
	}

	return false
}

// resourceMatchesFilter checks if a resource matches the filter constraints
func resourceMatchesFilter(resource *providers.Resource, filter string) bool {
	// Extract resource ID from resource
	resourceId := resource.Id
	resourceName := resource.Name

	// Check if filter contains this resource's ID or name
	patterns := []string{
		fmt.Sprintf("instance_id=\"%s\"", resourceId),
		fmt.Sprintf("instance_id=\"%s\"", resourceName),
		fmt.Sprintf("database_id=\"%s\"", resourceId),
		fmt.Sprintf("database_id=\"%s\"", resourceName),
		fmt.Sprintf("service_name=\"%s\"", resourceName),
		fmt.Sprintf("cluster_name=\"%s\"", resourceName),
		fmt.Sprintf("function_name=\"%s\"", resourceName),
		fmt.Sprintf("bucket_name=\"%s\"", resourceName),
	}

	for _, pattern := range patterns {
		if strings.Contains(filter, pattern) {
			return true
		}
	}

	// Check region match if filter has region constraint
	resourceRegion := resource.Region
	if resourceRegion != "" {
		regionPatterns := []string{
			fmt.Sprintf("region=\"%s\"", resourceRegion),
			fmt.Sprintf("location=\"%s\"", resourceRegion),
		}
		for _, pattern := range regionPatterns {
			if strings.Contains(filter, pattern) {
				return true
			}
		}

		// Check zone match (extract region from zone)
		if strings.Contains(filter, "zone=\"") {
			zoneRegion := extractRegionFromFilter(filter)
			if zoneRegion != "" && zoneRegion == resourceRegion {
				return true
			}
		}
	}

	return false
}
