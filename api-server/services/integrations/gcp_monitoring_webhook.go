package integrations

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(GCPMonitoringWebhook{})
}

type GCPMonitoringWebhook struct{}

// GCP Cloud Monitoring webhook payload structs
// See: https://cloud.google.com/monitoring/support/notification-options#webhooks

type GCPMonitoringWebhookPayload struct {
	Incident GCPMonitoringIncident `json:"incident"`
	Version  string                `json:"version"`
}

type GCPMonitoringIncident struct {
	IncidentID              string                 `json:"incident_id"`
	ScopingProjectID        string                 `json:"scoping_project_id"`
	URL                     string                 `json:"url"`
	StartedAt               int64                  `json:"started_at"`
	EndedAt                 int64                  `json:"ended_at"`
	State                   string                 `json:"state"` // "open" or "closed"
	Summary                 string                 `json:"summary"`
	ObservedValue           string                 `json:"observed_value"`
	Resource                GCPMonitoringResource  `json:"resource"`
	ResourceTypeDisplayName string                 `json:"resource_type_display_name"`
	ResourceID              string                 `json:"resource_id"`
	ResourceDisplayName     string                 `json:"resource_display_name"`
	Metric                  GCPMonitoringMetric    `json:"metric"`
	Metadata                GCPMonitoringMetadata  `json:"metadata"`
	PolicyName              string                 `json:"policy_name"`
	PolicyUserLabels        map[string]string      `json:"policy_user_labels"`
	ConditionName           string                 `json:"condition_name"`
	Condition               GCPMonitoringCondition `json:"condition"`
	Documentation           GCPMonitoringDoc       `json:"documentation"`
	ThresholdValue          string                 `json:"threshold_value"`
}

type GCPMonitoringResource struct {
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels"`
}

type GCPMonitoringMetric struct {
	Type        string            `json:"type"`
	DisplayName string            `json:"displayName"`
	Labels      map[string]string `json:"labels"`
}

type GCPMonitoringMetadata struct {
	SystemLabels map[string]string `json:"system_labels"`
	UserLabels   map[string]string `json:"user_labels"`
}

type GCPMonitoringCondition struct {
	Name               string      `json:"name"`
	DisplayName        string      `json:"displayName"`
	ConditionThreshold interface{} `json:"conditionThreshold,omitempty"`
}

type GCPMonitoringDoc struct {
	Content  string `json:"content"`
	MimeType string `json:"mime_type"`
}

const IntegrationGCPMonitoringWebhook = "gcp_monitoring_webhook"

func (m GCPMonitoringWebhook) Name() string {
	return IntegrationGCPMonitoringWebhook
}

func (m GCPMonitoringWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m GCPMonitoringWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of GCP Monitoring Webhook",
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				AutoGenerateFunc: "listAccounts",
			},
			"token": {
				Type: core.ToolSchemaTypeString,
			},
		},
	}
}

func (m GCPMonitoringWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m GCPMonitoringWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

func (m GCPMonitoringWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var payload GCPMonitoringWebhookPayload
	err := common.UnmarshalJson([]byte(webhookPayloadString), &payload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GCP Monitoring payload: %w", err)
	}

	inc := payload.Incident
	if strings.TrimSpace(inc.IncidentID) == "" {
		return nil, fmt.Errorf("invalid GCP Monitoring payload: incident_id is missing or empty")
	}

	// Extract fields
	policyName := inc.PolicyName
	if strings.TrimSpace(policyName) == "" {
		policyName = "GCP Monitoring Alert"
	}

	conditionName := inc.ConditionName
	projectID := inc.Resource.Labels["project_id"]
	if projectID == "" {
		projectID = inc.ScopingProjectID
	}

	// Parse event time
	var eventTime time.Time
	if inc.StartedAt > 0 {
		eventTime = time.Unix(inc.StartedAt, 0).UTC()
	} else {
		eventTime = time.Now()
	}

	// Map state to event status
	eventStatus := "triggered"
	var investigationStatus event.EventStatus
	if strings.ToLower(inc.State) == "closed" {
		eventStatus = "resolved"
		investigationStatus = event.EventStatusResolved
	} else {
		investigationStatus = event.EventStatusFiring
	}

	// Map severity from policy user labels, default to HIGH
	var eventPriority event.EventPriortiy
	if sev, ok := inc.PolicyUserLabels["severity"]; ok {
		switch strings.ToLower(sev) {
		case "critical":
			eventPriority = event.EventPriortiyHigh
		case "high":
			eventPriority = event.EventPriortiyHigh
		case "medium", "warning":
			eventPriority = event.EventPriortiyMedium
		case "low", "info":
			eventPriority = event.EventPriortiyLow
		default:
			eventPriority = event.EventPriortiyHigh
		}
	} else {
		eventPriority = event.EventPriortiyHigh
	}

	// Determine subject name from resource (needed for fingerprint and labels).
	// Resolution order matches polling in gcloud_monitoring_incidents_v3.go.
	subjectName := inc.ResourceDisplayName
	if subjectName == "" {
		subjectName = inc.Resource.Labels["instance_id"]
	}
	if subjectName == "" {
		subjectName = inc.Resource.Labels["resource_id"]
	}
	if subjectName == "" {
		// database_id kept as-is (e.g., "project:instance") to match polling behavior
		subjectName = inc.Resource.Labels["database_id"]
	}
	if subjectName == "" {
		subjectName = inc.Resource.Labels["pod_name"]
	}
	if subjectName == "" {
		subjectName = inc.Resource.Labels["container_name"]
	}
	if subjectName == "" {
		subjectName = inc.Resource.Labels["function_name"]
	}

	// Extract region from zone (matches polling logic in gcloud_monitoring_incidents_v3.go)
	var region string
	if zone, ok := inc.Resource.Labels["zone"]; ok {
		parts := strings.Split(zone, "-")
		if len(parts) >= 3 {
			region = strings.Join(parts[:len(parts)-1], "-")
		}
	}
	if region == "" {
		region = inc.Resource.Labels["region"]
	}
	if region == "" {
		region = inc.Resource.Labels["location"]
	}

	serviceName := gcpResourceTypeToServiceName(inc.Resource.Type)

	// Build title — match polling format: "{resource_type}: {policy_name}"
	var title string
	if inc.Resource.Type != "" {
		title = fmt.Sprintf("%s: %s", inc.Resource.Type, policyName)
	} else {
		title = policyName
	}

	// Build markdown description
	var desc strings.Builder
	if inc.Summary != "" {
		fmt.Fprintf(&desc, "## Summary\n%s\n\n", inc.Summary)
	}

	desc.WriteString("## Alert Details\n")
	fmt.Fprintf(&desc, "- **Policy:** %s\n", policyName)
	if conditionName != "" {
		fmt.Fprintf(&desc, "- **Condition:** %s\n", conditionName)
	}
	fmt.Fprintf(&desc, "- **State:** %s\n", inc.State)
	if projectID != "" {
		fmt.Fprintf(&desc, "- **Project:** %s\n", projectID)
	}

	// Metric info
	if inc.Metric.Type != "" {
		desc.WriteString("\n## Metric Information\n")
		metricDisplayName := inc.Metric.DisplayName
		if metricDisplayName == "" {
			metricDisplayName = inc.Metric.Type
		}
		fmt.Fprintf(&desc, "- **Metric:** %s\n", metricDisplayName)
		if inc.ObservedValue != "" && inc.ThresholdValue != "" {
			fmt.Fprintf(&desc, "- **Observed Value:** %s\n", inc.ObservedValue)
			fmt.Fprintf(&desc, "- **Threshold:** %s\n", inc.ThresholdValue)
		}
	}

	// Resource info
	if inc.Resource.Type != "" {
		desc.WriteString("\n## Resource\n")
		resourceDisplay := inc.ResourceDisplayName
		if resourceDisplay == "" {
			resourceDisplay = inc.Resource.Type
		}
		fmt.Fprintf(&desc, "- **Type:** %s\n", inc.Resource.Type)
		fmt.Fprintf(&desc, "- **Name:** %s\n", resourceDisplay)
		if zone, ok := inc.Resource.Labels["zone"]; ok {
			fmt.Fprintf(&desc, "- **Zone:** %s\n", zone)
		}
	}

	if inc.URL != "" {
		fmt.Fprintf(&desc, "\n[View in GCP Console](%s)\n", inc.URL)
	}

	message := desc.String()

	// Build labels
	labels := map[string]string{
		"integration":    "gcp_monitoring",
		"incident_id":    inc.IncidentID,
		"policy_name":    policyName,
		"condition_name": conditionName,
		"state":          inc.State,
		"alertname":      policyName,
	}

	if projectID != "" {
		labels["project_id"] = projectID
	}
	if inc.Resource.Type != "" {
		labels["resource_type"] = inc.Resource.Type
	}
	if inc.ResourceDisplayName != "" {
		labels["resource_display_name"] = inc.ResourceDisplayName
	}
	if inc.ResourceTypeDisplayName != "" {
		labels["resource_type_display_name"] = inc.ResourceTypeDisplayName
	}
	if inc.Metric.Type != "" {
		labels["metric_type"] = inc.Metric.Type
	}
	if inc.Metric.DisplayName != "" {
		labels["metric_display_name"] = inc.Metric.DisplayName
	}
	if inc.ObservedValue != "" {
		labels["observed_value"] = inc.ObservedValue
	}
	if inc.ThresholdValue != "" {
		labels["threshold_value"] = inc.ThresholdValue
	}
	if inc.URL != "" {
		labels["gcp_console_url"] = inc.URL
	}

	// Add resource labels
	for k, v := range inc.Resource.Labels {
		labels["resource_"+k] = v
	}

	// Add policy user labels
	for k, v := range inc.PolicyUserLabels {
		labels["policy_"+k] = v
	}

	// Serialize condition for labels
	if inc.Condition.Name != "" {
		labels["condition_resource_name"] = inc.Condition.Name
	}

	// Compatibility labels for auto-execution of enrichment actions (cloud_resource,
	// cloud_metrics, cloud_logs). Matches the label names set by the polling path in
	// gcloud_monitoring_incidents_v3.go. Same pattern as Azure PR #27020.
	labels["gcp_region"] = region
	labels["gcp_event_instance"] = subjectName
	labels["gcp_service_name"] = serviceName
	labels["gcp_account"] = projectID
	labels["gcp_event_resource_type"] = inc.Resource.Type
	if zone, ok := inc.Resource.Labels["zone"]; ok {
		labels["gcp_zone"] = zone
	}
	if inc.Metric.Type != "" {
		labels["gcp_alert_type"] = "metric"
		labels["gcp_metric_type"] = inc.Metric.Type
		labels["gcp_event_metric_type"] = inc.Metric.Type
		labels["gcp_event_metric_name"] = inc.Metric.Type
	} else {
		labels["gcp_alert_type"] = "log"
	}

	// Fingerprint: match polling's buildStableEventID() format — {policy_path}:{resource_type}:{resource_id}.
	// Extract policy path from condition name by stripping the /conditions/... suffix.
	policyPath := inc.Condition.Name
	if idx := strings.Index(policyPath, "/conditions/"); idx != -1 {
		policyPath = policyPath[:idx]
	}
	fingerprint := policyPath
	if policyPath != "" && inc.Resource.Type != "" && subjectName != "" {
		fingerprint = fmt.Sprintf("%s:%s:%s", policyPath, inc.Resource.Type, subjectName)
	}
	if fingerprint == "" {
		fingerprint = inc.IncidentID
	}

	// EventId (finding_id): use the GCP API's native incident path format so the
	// polling path (which uses alert.Name) produces the same finding_id for dedup.
	eventId := fmt.Sprintf("projects/%s/alerts/%s", projectID, inc.IncidentID)

	// Build service_key matching cloud-collector's buildExternalResourceId format
	serviceKey := buildGCPServiceKey(projectID, region, serviceName, inc.Resource.Type, subjectName)

	// Build raw payload evidence
	evidences := []event.EventEvidence{
		{
			Type: "json",
			Data: map[string]any{
				"name": "GCP Monitoring Event",
				"data": payload,
			},
			Insight: []event.EventEvidenceInsight{
				{
					Message:  fmt.Sprintf("GCP Monitoring Incident: %s", inc.IncidentID),
					Severity: "info",
				},
			},
			AdditionalInfo: map[string]any{
				"action_name":        "gcp_monitoring_event",
				"actual_action_name": "gcp_monitoring_event",
				"action_title":       "GCP Monitoring Event",
			},
		},
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName: policyName,
		Labels:   labels,
		Annotations: map[string]string{
			"description": message,
			"summary":     title,
		},
		RuleType:    "gcp_monitoring_alert",
		RuleId:      policyName,
		Fingerprint: fingerprint,
		Status:      investigationStatus,
		Severity:    eventPriority,
		SourceUrl:   inc.URL,
		Evidences:   evidences,
	}

	webhook := core.EventIncomingWebhook{
		WebhookId:             eventId,
		EventType:             "gcp_monitoring_alert",
		EventId:               eventId,
		EventUrl:              inc.URL,
		EventStatus:           eventStatus,
		EventPriority:         string(eventPriority),
		EventCreatedAt:        eventTime,
		EventTitle:            title,
		EventDescription:      message,
		EventTags:             []string{"gcp", "monitoring", inc.Resource.Type, inc.State},
		Investigation:         investigation,
		EventSubjectNamespace: inc.Resource.Type,
		EventSubjectName:      subjectName,
		ServiceKey:            serviceKey,
	}

	// Async create event rule (same pattern as Azure Monitor)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sc.GetLogger().Error("Panic recovered in GCP Monitoring event rule creation goroutine",
					"panic", r, "incidentId", inc.IncidentID, "policyName", policyName)
			}
		}()

		alertName := policyName
		alertQuery := ""
		alertMessage := message
		alertCategory := "alert"

		// Build query from metric info
		if inc.Metric.Type != "" {
			if inc.ThresholdValue != "" {
				alertQuery = fmt.Sprintf("%s > %s", inc.Metric.Type, inc.ThresholdValue)
			} else {
				alertQuery = fmt.Sprintf("gcp_monitoring{metric_type=\"%s\"}", inc.Metric.Type)
			}
		} else {
			alertQuery = fmt.Sprintf("gcp_monitoring_alert{policy_name=\"%s\"}", policyName)
		}

		alertSeverity := "warning"
		if eventPriority == event.EventPriortiyHigh {
			alertSeverity = "critical"
		}

		conditionJSON, _ := json.Marshal(inc.Condition)
		_ = conditionJSON

		eventReq := eventrule.EventConfig{
			Annotations: struct {
				Description string `json:"description"`
				Summary     string `json:"summary"`
				Runbook     string `json:"runbook"`
			}{
				Description: alertMessage,
				Summary:     title,
			},
			Expr: alertQuery,
			Labels: struct {
				Severity string `json:"severity"`
			}{Severity: alertSeverity},
			Alert:         alertName,
			Duration:      "0",
			AccountID:     accountId,
			Source:        "gcp_monitoring_webhook",
			Category:      alertCategory,
			Severity:      alertSeverity,
			Enabled:       true,
			TriggerParams: []map[string]interface{}{},
			ActionParams:  []map[string]interface{}{},
		}
		_, err := eventrule.CreateEventRule(sc, eventReq)
		if err != nil {
			sc.GetLogger().Error("CreateEventRule failed for GCP Monitoring webhook", "error", err, "incidentId", inc.IncidentID)
		}
	}()

	return []core.EventIncomingWebhook{webhook}, nil
}

func (m GCPMonitoringWebhook) TestConnection(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId string) (bool, error) {
	return true, nil
}

// gcpResourceTypeToServiceName maps GCP monitored resource types to human-readable service names.
// Mirrors mapResourceTypeToServiceName in cloud-collector's gcloud_monitoring_alerts_common.go.
func gcpResourceTypeToServiceName(resourceType string) string {
	m := map[string]string{
		"gce_instance":         "Compute Engine",
		"cloudsql_database":    "Cloud SQL",
		"cloud_run_revision":   "Cloud Run",
		"k8s_cluster":          "Kubernetes Engine",
		"k8s_node":             "Kubernetes Engine",
		"k8s_pod":              "Kubernetes Engine",
		"k8s_container":        "Kubernetes Engine",
		"gke_cluster":          "Kubernetes Engine",
		"gke_nodepool":         "Kubernetes Engine",
		"gcs_bucket":           "Cloud Storage",
		"cloud_function":       "Cloud Functions",
		"https_lb_rule":        "Cloud Load Balancing",
		"tcp_ssl_proxy_rule":   "Cloud Load Balancing",
		"pubsub_topic":         "Cloud Pub/Sub",
		"pubsub_subscription":  "Cloud Pub/Sub",
		"bigquery_dataset":     "BigQuery",
		"bigquery_table":       "BigQuery",
		"vpc_access_connector": "Networking",
	}
	if s, ok := m[resourceType]; ok {
		return s
	}
	return "Cloud Monitoring"
}

// buildGCPServiceKey builds an ARN-style service key matching the format produced by
// cloud-collector's buildExternalResourceId (account/common.go).
// Format: arn:gcp:{service}:{region}:{project}:{resource_type}:{resource_id}
func buildGCPServiceKey(projectID, region, serviceName, resourceType, resourceID string) string {
	if resourceType == "" || resourceID == "" {
		return ""
	}
	svc := strings.ReplaceAll(strings.ToLower(serviceName), " ", "-")
	if region == "" {
		region = "global"
	}
	region = strings.ToLower(region)
	resourceID = strings.ToLower(resourceID)
	resourceID = strings.ReplaceAll(resourceID, " ", "-")
	resourceType = strings.ToLower(resourceType)
	resourceType = strings.ReplaceAll(resourceType, " ", "-")
	return "arn:gcp:" + svc + ":" + region + ":" + projectID + ":" + resourceType + ":" + resourceID
}
