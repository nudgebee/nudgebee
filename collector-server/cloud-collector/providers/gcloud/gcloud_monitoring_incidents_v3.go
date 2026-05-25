package gcloud

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nudgebee/collector/cloud/providers"

	"cloud.google.com/go/auth/credentials"
	"cloud.google.com/go/auth/oauth2adapt"
	"golang.org/x/oauth2"
)

// GCP v3 Alert API structures (based on https://docs.cloud.google.com/monitoring/api/ref_v3/rest/v3/projects.alerts)

// AlertState represents the state of an alert
type AlertState string

const (
	AlertStateUnspecified AlertState = "STATE_UNSPECIFIED"
	AlertStateOpen        AlertState = "OPEN"
	AlertStateClosed      AlertState = "CLOSED"
)

// Alert represents a firing or closed incident in GCP
// This is the actual incident object that GCP creates when an alert policy fires
type Alert struct {
	Name      string                 `json:"name"`      // projects/{project}/alerts/{alert_id}
	State     AlertState             `json:"state"`     // OPEN or CLOSED
	OpenTime  string                 `json:"openTime"`  // RFC3339 timestamp
	CloseTime string                 `json:"closeTime"` // RFC3339 timestamp (if closed)
	Resource  *MonitoredResource     `json:"resource"`  // Resource that triggered the alert
	Metric    *Metric                `json:"metric"`    // Metric information
	Log       *LogMetadata           `json:"log"`       // Log-based alert metadata
	Policy    *PolicySnapshot        `json:"policy"`    // Snapshot of the alert policy
	Metadata  map[string]interface{} `json:"metadata"`  // Additional metadata
}

// MonitoredResource represents a GCP resource
type MonitoredResource struct {
	Type   string            `json:"type"`   // e.g., "gce_instance"
	Labels map[string]string `json:"labels"` // Resource labels (project_id, zone, instance_id, etc.)
}

// Metric represents metric information
type Metric struct {
	Type   string            `json:"type"`   // Metric type (e.g., "compute.googleapis.com/instance/cpu/utilization")
	Labels map[string]string `json:"labels"` // Metric labels
}

// LogMetadata represents log-based alert metadata
type LogMetadata struct {
	ResourceContainer string `json:"resourceContainer"` // Resource container
}

// PolicySnapshot represents the alert policy that generated this alert
type PolicySnapshot struct {
	Name        string            `json:"name"`        // Policy name
	DisplayName string            `json:"displayName"` // Policy display name
	Severity    string            `json:"severity"`    // CRITICAL, ERROR, WARNING
	UserLabels  map[string]string `json:"userLabels"`  // User-defined labels
}

// ListAlertsResponse is the response from the alerts.list API
type ListAlertsResponse struct {
	Alerts        []*Alert `json:"alerts"`
	NextPageToken string   `json:"nextPageToken"`
}

// getGCPIncidentsV3 fetches firing incidents directly from GCP v3 alerts API
// This is the CORRECT way - GCP already evaluates policies and creates incidents
// We just query them directly, similar to AWS CloudWatch GetAlarmHistory
func getGCPIncidentsV3(ctx providers.CloudProviderContext, account *providers.Account, query providers.ListEventRequest) (providers.ListEventResponse, error) {
	logger := ctx.GetLogger()
	session, err := getGcloudSessionFromAccount(ctx, *account)
	if err != nil {
		return providers.ListEventResponse{}, fmt.Errorf("failed to get GCP session: %w", err)
	}

	projectID := session.ProjectId

	// Build API endpoint
	baseURL := fmt.Sprintf("https://monitoring.googleapis.com/v3/projects/%s/alerts", projectID)

	// Build filter for open incidents within time range
	var filters []string
	if query.StartDate != nil {
		startTimeStr := query.StartDate.Format(time.RFC3339)
		// Fetch incidents that opened since the last run OR are still open.
		// No upper bound needed — we want everything from StartDate to now.
		filters = append(filters, fmt.Sprintf("open_time>=\"%s\" OR state=\"OPEN\"", startTimeStr))
	} else {
		// No StartDate (initial load) — fetch open incidents only.
		// This avoids fetching the entire incident history on first run.
		filters = append(filters, "state=\"OPEN\"")
	}

	// Create HTTP client using session credentials
	httpClient, err := createHTTPClientFromOpts(ctx, session)
	if err != nil {
		return providers.ListEventResponse{}, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Fetch all alerts with pagination
	var allAlerts []*Alert
	pageToken := ""

	for {
		// Construct URL with filters and pagination
		params := url.Values{}
		if len(filters) > 0 {
			params.Add("filter", filters[0])
		}
		if pageToken != "" {
			params.Add("pageToken", pageToken)
		}

		apiURL := baseURL
		if len(params) > 0 {
			apiURL = fmt.Sprintf("%s?%s", baseURL, params.Encode())
		}

		logger.Debug("Fetching GCP incidents via v3 API", "url", apiURL, "projectID", projectID)

		// Make HTTP request
		req, err := http.NewRequestWithContext(ctx.GetContext(), "GET", apiURL, nil)
		if err != nil {
			return providers.ListEventResponse{}, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return providers.ListEventResponse{}, fmt.Errorf("failed to call alerts API: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("failed to close response body", "error", cerr)
		}

		if err != nil {
			return providers.ListEventResponse{}, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return providers.ListEventResponse{}, fmt.Errorf("alerts API returned status %d: %s", resp.StatusCode, string(body))
		}

		// Parse response
		var alertsResp ListAlertsResponse
		if err := json.Unmarshal(body, &alertsResp); err != nil {
			return providers.ListEventResponse{}, fmt.Errorf("failed to parse alerts response: %w", err)
		}

		allAlerts = append(allAlerts, alertsResp.Alerts...)

		// Check for more pages
		if alertsResp.NextPageToken == "" {
			break
		}
		pageToken = alertsResp.NextPageToken
	}

	logger.Info("Fetched GCP incidents", "count", len(allAlerts), "projectID", projectID)

	// Convert alerts to Event objects
	// Note: The API filter already handles state filtering, so we trust the API response
	var events []providers.Event
	for _, alert := range allAlerts {
		event := convertAlertToEvent(ctx, httpClient, alert, account)
		events = append(events, event)
	}

	return providers.ListEventResponse{
		Items:   events,
		Summary: []providers.EventSummary{},
	}, nil
}

// convertAlertToEvent converts a GCP Alert to our Event structure
// The httpClient is used to look up instance names from instance IDs (like GCP Console does)
func convertAlertToEvent(ctx providers.CloudProviderContext, httpClient *http.Client, alert *Alert, account *providers.Account) providers.Event {
	eventTime := parseRFC3339Time(alert.OpenTime)
	if eventTime == nil {
		now := time.Now()
		eventTime = &now
	}

	// Determine severity
	var severity providers.EventSeverity
	if alert.Policy != nil && alert.Policy.Severity != "" {
		switch alert.Policy.Severity {
		case "CRITICAL":
			severity = providers.EventSeverityHigh
		case "ERROR":
			severity = providers.EventSeverityHigh
		case "WARNING":
			severity = providers.EventSeverityMedium
		default:
			severity = providers.EventSeverityHigh
		}
	} else {
		severity = providers.EventSeverityHigh
	}

	// Determine event status
	var eventStatus providers.EventStatus
	switch alert.State {
	case AlertStateOpen:
		eventStatus = providers.EventStatusFiring
	case AlertStateClosed:
		eventStatus = providers.EventStatusResolved
	default:
		eventStatus = providers.EventStatusFiring
	}

	// Extract resource information
	var resourceType, resourceID, resourceRegion string
	var instanceID string // Keep numeric ID for API lookup
	if alert.Resource != nil {
		resourceType = alert.Resource.Type

		// For GCE instances, prefer instance_name (human-readable) over instance_id (numeric)
		// GCP's Resource labels only contain instance_id (numeric like "3346138238029787252")
		// But Metric labels often contain instance_name (human-readable like "my-vm-1")
		// This matches what GCP Console displays to users
		if alert.Metric != nil {
			resourceID = alert.Metric.Labels["instance_name"]
		}

		// Store instance_id for potential API lookup
		instanceID = alert.Resource.Labels["instance_id"]

		// Extract zone for API lookup (needed before we potentially do the lookup)
		var zone string
		if z, ok := alert.Resource.Labels["zone"]; ok {
			zone = z
			parts := strings.Split(z, "-")
			if len(parts) >= 3 {
				resourceRegion = strings.Join(parts[:len(parts)-1], "-")
			}
		}
		if resourceRegion == "" {
			resourceRegion = alert.Resource.Labels["region"]
		}
		if resourceRegion == "" {
			resourceRegion = alert.Resource.Labels["location"]
		}

		// If we don't have instance_name from metric labels but have instance_id,
		// try to look it up via Compute Engine API (like GCP Console does)
		if resourceID == "" && instanceID != "" && resourceType == "gce_instance" {
			projectID := alert.Resource.Labels["project_id"]
			if projectID == "" && account != nil {
				projectID = account.AccountNumber
			}
			// Try API lookup
			if lookedUpName := lookupInstanceName(ctx, httpClient, projectID, zone, instanceID); lookedUpName != "" {
				resourceID = lookedUpName
			}
		}

		// Fall back to resource labels if we still don't have a name
		if resourceID == "" {
			resourceID = instanceID // Use instance_id as fallback
		}
		if resourceID == "" {
			resourceID = alert.Resource.Labels["resource_id"]
		}
		if resourceID == "" {
			if dbID := alert.Resource.Labels["database_id"]; dbID != "" {
				// database_id is in format "project_id:instance_name" (e.g., "myproject:mydb").
				// Keep the full value — GCP Cloud Monitoring filters require
				// resource.labels.database_id in this exact format.
				resourceID = dbID
			}
		}
		if resourceID == "" {
			resourceID = alert.Resource.Labels["pod_name"]
		}
		if resourceID == "" {
			resourceID = alert.Resource.Labels["container_name"]
		}
		if resourceID == "" {
			resourceID = alert.Resource.Labels["function_name"]
		}
	}

	// Ensure we always have a resource type and ID (required for DB storage)
	// Try to derive resource type from metric type if not available from resource
	if resourceType == "" {
		resourceType = deriveResourceTypeFromMetric(alert)
	}
	if resourceType == "" {
		resourceType = "unknown"
	}
	if resourceID == "" {
		// Fall back to alert incident ID if no resource ID available
		resourceID = extractResourceNameFromPath(alert.Name)
	}

	// Map resource type to service name
	serviceName := mapResourceTypeToServiceName(resourceType)

	// Build labels
	labels := map[string]string{
		"gcp_incident_id":   extractResourceNameFromPath(alert.Name), // Short incident ID (e.g., "12345")
		"gcp_incident_name": alert.Name,                              // Full incident path (e.g., "projects/.../alerts/12345")
		"gcp_state":         string(alert.State),
	}

	// Add project ID from resource labels or account
	if alert.Resource != nil {
		if projectID, ok := alert.Resource.Labels["project_id"]; ok {
			labels["gcp_project_id"] = projectID
		}
	}
	if _, ok := labels["gcp_project_id"]; !ok && account != nil {
		labels["gcp_project_id"] = account.AccountNumber
	}

	if alert.Policy != nil {
		labels["gcp_policy_name"] = alert.Policy.DisplayName
		labels["gcp_policy_id"] = alert.Policy.Name

		// Add user labels from policy
		for k, v := range alert.Policy.UserLabels {
			labels[fmt.Sprintf("policy_%s", k)] = v
		}
	}

	if alert.Resource != nil {
		for k, v := range alert.Resource.Labels {
			labels[fmt.Sprintf("resource_%s", k)] = v
		}
	}

	// Determine alert type for downstream evidence actions
	if alert.Metric != nil {
		labels["gcp_alert_type"] = "metric"
	} else if alert.Log != nil {
		labels["gcp_alert_type"] = "log"
		if alert.Log.ResourceContainer != "" {
			labels["gcp_log_resource_container"] = alert.Log.ResourceContainer
		}
	} else {
		labels["gcp_alert_type"] = "unknown"
	}

	// Add metric labels (includes instance_name for GCE instances)
	if alert.Metric != nil {
		labels["gcp_metric_type"] = alert.Metric.Type
		labels["gcp_event_metric_type"] = alert.Metric.Type // For actions.go CanAutoExecute check

		// Derive metric name from metric type (e.g., "cloudsql.googleapis.com/database/cpu/utilization" → full type used as name)
		// GCP metric queries accept the full metric type as the metric identifier
		labels["gcp_event_metric_name"] = alert.Metric.Type

		for k, v := range alert.Metric.Labels {
			labels[fmt.Sprintf("metric_%s", k)] = v
		}
	}

	// Add labels required by actions.go for investigation tasks
	// These labels enable automatic task execution when investigating GCP events
	labels["gcp_region"] = resourceRegion            // Required for cloudMetricsAction, cloudResourceAction, cloudCliAction
	labels["gcp_event_instance"] = resourceID        // Required for cloudMetricsAction, cloudResourceAction
	labels["gcp_event_resource_type"] = resourceType // Required for cloudCliAction
	labels["gcp_service_name"] = serviceName         // Required for cloudResourceAction, used in cloudMetricsAction
	labels["gcp_account"] = account.AccountNumber    // Used in cloudMetricsAction AutoExecute

	// Add time range for metric queries — narrows the time window around the incident
	if alert.OpenTime != "" {
		labels["gcp_event_start_time"] = alert.OpenTime
	}
	if alert.CloseTime != "" {
		labels["gcp_event_end_time"] = alert.CloseTime
	}

	// Add zone information for CLI commands
	if alert.Resource != nil && alert.Resource.Labels != nil {
		if zone, ok := alert.Resource.Labels["zone"]; ok {
			labels["gcp_zone"] = zone // Used in buildGCloudDescribeCommand
		}
	}

	// Build raw event data
	rawEvent := map[string]any{
		"alert_name": alert.Name,
		"state":      alert.State,
		"open_time":  alert.OpenTime,
		"close_time": alert.CloseTime,
		"resource":   alert.Resource,
		"metric":     alert.Metric,
		"policy":     alert.Policy,
		"metadata":   alert.Metadata,
	}

	// Build stable EventId (fingerprint) for deduplication
	// GCP's alert.Name (e.g., "projects/project/alerts/12345") is unique per INCIDENT,
	// meaning each time an alert fires, it gets a new ID.
	// For proper fingerprinting (like AWS AlarmArn), we need a stable identifier
	// that represents "this policy firing on this resource".
	//
	// Format: {policy_name}:{resource_type}:{resource_id}
	// This allows:
	// - Same policy on different resources = different fingerprints
	// - Same policy on same resource at different times = same fingerprint
	// - finding_id = fingerprint + timestamp (computed in etl_events.go)
	eventID := buildStableEventID(alert, resourceType, resourceID)

	event := providers.Event{
		Title:               getAlertTitle(alert),
		EventName:           getEventName(alert),
		Description:         getAlertDescription(alert),
		Date:                *eventTime,
		EventSource:         "GCP_Metric_Alert",
		EventId:             eventID,
		FindingId:           alert.Name, // Source-native incident path (e.g., "projects/.../alerts/12345")
		EventStatus:         eventStatus,
		EventSeverity:       severity,
		ResourceType:        resourceType,
		ResourceId:          resourceID,
		ResourceRegion:      resourceRegion,
		ResourceServiceName: serviceName,
		Raw:                 rawEvent,
		Labels:              labels,
	}

	return event
}

// getEventName returns the event name for the alert
// This is used as aggregation_key in the DB, so it must never be empty
func getEventName(alert *Alert) string {
	if alert.Policy != nil && alert.Policy.DisplayName != "" {
		return alert.Policy.DisplayName
	}
	if alert.Policy != nil && alert.Policy.Name != "" {
		return extractResourceNameFromPath(alert.Policy.Name)
	}
	// Fall back to alert name if no policy info
	if alert.Name != "" {
		return extractResourceNameFromPath(alert.Name)
	}
	return "GCP Alert"
}

// buildStableEventID creates a stable fingerprint for GCP alerts
// This is used for deduplication - same policy + resource = same fingerprint
// Different from alert.Name which is unique per incident (changes each time alert fires)
//
// Format: {policy_name}:{resource_type}:{resource_id}
// Example: "projects/nudgebee-dev/alertPolicies/67890:gce_instance:1234567890"
//
// This is analogous to AWS AlarmArn which is stable across firings
func buildStableEventID(alert *Alert, resourceType, resourceID string) string {
	var policyName string
	if alert.Policy != nil {
		policyName = alert.Policy.Name
	}

	// If we don't have policy name, fall back to alert name (incident-specific)
	if policyName == "" {
		return alert.Name
	}

	// Build stable fingerprint: policy + resource type + resource ID
	// This ensures:
	// - Same policy on VM-A and VM-B = different fingerprints
	// - Same policy on VM-A at time T1 and T2 = same fingerprint
	if resourceType != "" && resourceID != "" {
		return fmt.Sprintf("%s:%s:%s", policyName, resourceType, resourceID)
	}

	// If no resource info, just use policy name
	// (this handles log-based alerts that may not have specific resources)
	return policyName
}

// getAlertTitle generates a human-readable title for the alert
func getAlertTitle(alert *Alert) string {
	if alert.Policy != nil && alert.Policy.DisplayName != "" {
		if alert.Resource != nil {
			return fmt.Sprintf("%s: %s", alert.Resource.Type, alert.Policy.DisplayName)
		}
		return alert.Policy.DisplayName
	}

	if alert.Resource != nil {
		return fmt.Sprintf("Alert on %s", alert.Resource.Type)
	}

	return "GCP Alert"
}

// getAlertDescription generates a description for the alert
func getAlertDescription(alert *Alert) string {
	if alert.Policy == nil {
		return "Alert triggered"
	}

	desc := fmt.Sprintf("Alert policy '%s' is currently firing.", alert.Policy.DisplayName)

	// Add resource information
	if alert.Resource != nil {
		if instanceID, ok := alert.Resource.Labels["instance_id"]; ok {
			desc += fmt.Sprintf("\nResource: %s (ID: %s)", alert.Resource.Type, instanceID)
		} else {
			desc += fmt.Sprintf("\nResource: %s", alert.Resource.Type)
		}
	}

	// Add metric information
	if alert.Metric != nil {
		desc += fmt.Sprintf("\nMetric: %s", alert.Metric.Type)
	}

	return desc
}

// deriveResourceTypeFromMetric attempts to derive the resource type from the metric type
// when the resource.type is not directly available.
//
// GCP metric types follow the pattern: {service}.googleapis.com/{resource}/{metric}
// Examples:
//   - compute.googleapis.com/instance/cpu/utilization -> gce_instance
//   - cloudsql.googleapis.com/database/cpu/utilization -> cloudsql_database
//   - storage.googleapis.com/storage/object_count -> gcs_bucket
//   - run.googleapis.com/request_count -> cloud_run_revision
//   - cloudfunctions.googleapis.com/function/execution_count -> cloud_function
//   - pubsub.googleapis.com/topic/send_message_operation_count -> pubsub_topic
//   - container.googleapis.com/container/cpu/usage_time -> k8s_container
func deriveResourceTypeFromMetric(alert *Alert) string {
	if alert.Metric == nil || alert.Metric.Type == "" {
		return ""
	}

	metricType := alert.Metric.Type

	// Map metric prefixes to resource types
	metricToResourceType := map[string]string{
		"compute.googleapis.com/instance/":        "gce_instance",
		"compute.googleapis.com/disk/":            "gce_disk",
		"cloudsql.googleapis.com/database/":       "cloudsql_database",
		"storage.googleapis.com/storage/":         "gcs_bucket",
		"run.googleapis.com/":                     "cloud_run_revision",
		"cloudfunctions.googleapis.com/function/": "cloud_function",
		"pubsub.googleapis.com/topic/":            "pubsub_topic",
		"pubsub.googleapis.com/subscription/":     "pubsub_subscription",
		"container.googleapis.com/container/":     "k8s_container",
		"container.googleapis.com/node/":          "k8s_node",
		"container.googleapis.com/pod/":           "k8s_pod",
		"appengine.googleapis.com/":               "gae_app",
		"bigquery.googleapis.com/":                "bigquery_dataset",
		"spanner.googleapis.com/":                 "spanner_instance",
		"dataflow.googleapis.com/":                "dataflow_job",
		"loadbalancing.googleapis.com/":           "https_lb_rule",
		"logging.googleapis.com/":                 "logging_sink",
		"monitoring.googleapis.com/uptime_check/": "uptime_url",
		"redis.googleapis.com/":                   "redis_instance",
		"memcache.googleapis.com/":                "memcache_instance",
		"firestore.googleapis.com/":               "firestore_database",
		"bigtable.googleapis.com/":                "bigtable_table",
		"composer.googleapis.com/":                "cloud_composer_environment",
		"dataproc.googleapis.com/":                "cloud_dataproc_cluster",
		"aiplatform.googleapis.com/":              "aiplatform_endpoint",
		"vpcaccess.googleapis.com/":               "vpc_access_connector",
		"networkservices.googleapis.com/":         "network_services",
		"certificatemanager.googleapis.com/":      "certificate_map",
	}

	// Check each prefix
	for prefix, resourceType := range metricToResourceType {
		if strings.HasPrefix(metricType, prefix) {
			return resourceType
		}
	}

	// If no exact match, try to extract service name from metric type
	// Format: {service}.googleapis.com/...
	if strings.Contains(metricType, ".googleapis.com/") {
		parts := strings.SplitN(metricType, ".googleapis.com/", 2)
		if len(parts) > 0 && parts[0] != "" {
			// Return the service name as a fallback (e.g., "compute", "cloudsql")
			return parts[0]
		}
	}

	return ""
}

// parseRFC3339Time parses an RFC3339 timestamp string
func parseRFC3339Time(timeStr string) *time.Time {
	if timeStr == "" {
		return nil
	}

	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return nil
	}

	return &t
}

// lookupInstanceName queries the GCP Compute Engine API to get the instance name from instance ID.
// This mirrors what GCP Console does - it uses the instance_id (numeric) to look up the
// human-readable instance_name.
//
// Parameters:
//   - httpClient: authenticated HTTP client
//   - projectID: GCP project ID
//   - zone: zone where the instance resides (e.g., "us-central1-a")
//   - instanceID: numeric instance ID (e.g., "3346138238029787252")
//
// Returns the instance name (e.g., "my-vm-1") or empty string if not found.
func lookupInstanceName(ctx providers.CloudProviderContext, httpClient *http.Client, projectID, zone, instanceID string) string {
	if httpClient == nil || projectID == "" || zone == "" || instanceID == "" {
		return ""
	}

	// Use the Compute Engine API to list instances and find by ID
	// GET https://compute.googleapis.com/compute/v1/projects/{project}/zones/{zone}/instances?filter=id={instanceID}
	apiURL := fmt.Sprintf("https://compute.googleapis.com/compute/v1/projects/%s/zones/%s/instances",
		url.PathEscape(projectID),
		url.PathEscape(zone))

	req, err := http.NewRequestWithContext(ctx.GetContext(), "GET", apiURL, nil)
	if err != nil {
		ctx.GetLogger().Debug("failed to create compute API request", "error", err)
		return ""
	}

	// Add filter to find instance by ID
	q := req.URL.Query()
	q.Set("filter", fmt.Sprintf("id=%s", instanceID))
	req.URL.RawQuery = q.Encode()

	resp, err := httpClient.Do(req)
	if err != nil {
		ctx.GetLogger().Debug("failed to query compute API", "error", err)
		return ""
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			ctx.GetLogger().Debug("failed to close response body", "error", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		ctx.GetLogger().Debug("compute API returned non-200 status", "status", resp.StatusCode)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.GetLogger().Debug("failed to read compute API response", "error", err)
		return ""
	}

	// Parse response to extract instance name
	var result struct {
		Items []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		ctx.GetLogger().Debug("failed to parse compute API response", "error", err)
		return ""
	}

	// Return the first matching instance name
	for _, item := range result.Items {
		if item.ID == instanceID {
			return item.Name
		}
	}

	return ""
}

// createHTTPClientFromOpts creates an authenticated HTTP client from session credentials
func createHTTPClientFromOpts(ctx providers.CloudProviderContext, session gcloudAuthSession) (*http.Client, error) {
	// Use the credentials from the session to create an OAuth2 HTTP client
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		CredentialsJSON: []byte(session.AccountCred),
		Scopes: []string{
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/monitoring.read",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create credentials: %w", err)
	}

	// Create HTTP client with OAuth2 transport
	tokenSource := oauth2adapt.TokenSourceFromTokenProvider(creds)
	return oauth2.NewClient(ctx.GetContext(), tokenSource), nil
}
