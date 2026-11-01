package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/config"
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

// GCP Cloud Monitoring webhook payload — only the fields read here. The collector parses
// the full payload. See: https://cloud.google.com/monitoring/support/notification-options#webhooks

type GCPMonitoringWebhookPayload struct {
	Incident GCPMonitoringIncident `json:"incident"`
	Version  string                `json:"version"`
}

type GCPMonitoringIncident struct {
	IncidentID     string                `json:"incident_id"`
	URL            string                `json:"url"`
	StartedAt      int64                 `json:"started_at"`
	State          string                `json:"state"` // "open" or "closed"
	Resource       GCPMonitoringResource `json:"resource"`
	Metric         GCPMonitoringMetric   `json:"metric"`
	ThresholdValue string                `json:"threshold_value"`
}

type GCPMonitoringResource struct {
	Type string `json:"type"`
}

type GCPMonitoringMetric struct {
	Type string `json:"type"`
}

// gcpNormalizedEvent is the event row the polling path would store for the incident, as
// returned by the collector's normalize_gcp_monitoring_webhook endpoint.
type gcpNormalizedEvent struct {
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	AggregationKey   string            `json:"aggregation_key"`
	SubjectName      string            `json:"subject_name"`
	SubjectType      string            `json:"subject_type"`
	SubjectNamespace string            `json:"subject_namespace"`
	ServiceKey       string            `json:"service_key"`
	Fingerprint      string            `json:"fingerprint"`
	FindingId        string            `json:"finding_id"`
	CloudResourceId  string            `json:"cloud_resource_id"`
	Priority         string            `json:"priority"`
	Status           string            `json:"status"`
	Labels           map[string]string `json:"labels"`
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
				Priority:    100,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"token": {
				Type:     core.ToolSchemaTypeString,
				Priority: 70,
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

// ProcessEventWebook takes the incident's identity — subject, fingerprint, finding_id,
// service key, labels — from the collector, which derives it with the same code it polls
// incidents with. This package used to normalize the payload itself; that copy drifted
// from polling several times, last by naming a Cloud SQL instance "sample-db" instead
// of "project:sample-db", which left the traces enricher with no spans (#34377).
func (m GCPMonitoringWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var payload GCPMonitoringWebhookPayload
	err := common.UnmarshalJson([]byte(webhookPayloadString), &payload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GCP Monitoring payload: %w", err)
	}
	// The evidence keeps the whole delivery, not just the fields read here.
	var rawPayload map[string]any
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &rawPayload); err != nil {
		return nil, fmt.Errorf("failed to parse GCP Monitoring payload: %w", err)
	}

	inc := payload.Incident
	if strings.TrimSpace(inc.IncidentID) == "" {
		return nil, fmt.Errorf("invalid GCP Monitoring payload: incident_id is missing or empty")
	}
	if accountId == "" {
		return nil, fmt.Errorf("GCP Monitoring webhook for incident %s has no cloud account", inc.IncidentID)
	}

	normalized, err := normalizeGCPIncident(sc, accountId, webhookPayloadString)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize GCP Monitoring incident %s: %w", inc.IncidentID, err)
	}

	webhook := buildGCPMonitoringWebhookEvent(inc, rawPayload, normalized)

	// Async create event rule (same pattern as Azure Monitor)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sc.GetLogger().Error("Panic recovered in GCP Monitoring event rule creation goroutine",
					"panic", r, "incidentId", inc.IncidentID, "policyName", normalized.AggregationKey)
			}
		}()

		// Build query from metric info
		alertQuery := ""
		if inc.Metric.Type != "" {
			if inc.ThresholdValue != "" {
				alertQuery = fmt.Sprintf("%s > %s", inc.Metric.Type, inc.ThresholdValue)
			} else {
				alertQuery = fmt.Sprintf("gcp_monitoring{metric_type=\"%s\"}", inc.Metric.Type)
			}
		} else {
			alertQuery = fmt.Sprintf("gcp_monitoring_alert{policy_name=\"%s\"}", normalized.AggregationKey)
		}

		alertSeverity := "warning"
		if normalized.Priority == string(event.EventPriorityHigh) {
			alertSeverity = "critical"
		}

		eventReq := eventrule.EventConfig{
			Annotations: struct {
				Description string `json:"description"`
				Summary     string `json:"summary"`
				Runbook     string `json:"runbook"`
			}{
				Description: normalized.Description,
				Summary:     normalized.Title,
			},
			Expr: alertQuery,
			Labels: struct {
				Severity string `json:"severity"`
			}{Severity: alertSeverity},
			Alert:         normalized.AggregationKey,
			Duration:      "0",
			AccountID:     accountId,
			Source:        "gcp_monitoring_webhook",
			Category:      "alert",
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

// buildGCPMonitoringWebhookEvent fills the webhook event from the collector's normalized
// row; the raw delivery is kept as evidence.
func buildGCPMonitoringWebhookEvent(inc GCPMonitoringIncident, payload map[string]any, n gcpNormalizedEvent) core.EventIncomingWebhook {
	var eventTime time.Time
	if inc.StartedAt > 0 {
		eventTime = time.Unix(inc.StartedAt, 0).UTC()
	} else {
		eventTime = time.Now()
	}

	// routeWebhookEvent resolves on "resolved" and investigates on "triggered".
	eventStatus := "triggered"
	investigationStatus := event.EventStatusFiring
	if n.Status == string(event.EventStatusResolved) {
		eventStatus = "resolved"
		investigationStatus = event.EventStatusResolved
	}

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
		RuleName: n.AggregationKey,
		Labels:   n.Labels,
		Annotations: map[string]string{
			"description": n.Description,
			"summary":     n.Title,
		},
		RuleType:    "gcp_monitoring_alert",
		RuleId:      n.AggregationKey,
		Fingerprint: n.Fingerprint,
		Status:      investigationStatus,
		Severity:    event.EventPriority(n.Priority),
		SourceUrl:   inc.URL,
		Evidences:   evidences,
	}

	return core.EventIncomingWebhook{
		WebhookId:             n.FindingId,
		EventType:             "gcp_monitoring_alert",
		EventId:               n.FindingId,
		EventUrl:              inc.URL,
		EventStatus:           eventStatus,
		EventPriority:         n.Priority,
		EventCreatedAt:        eventTime,
		EventTitle:            n.Title,
		EventDescription:      n.Description,
		EventTags:             []string{"gcp", "monitoring", inc.Resource.Type, inc.State},
		Investigation:         investigation,
		EventSubjectKind:      n.SubjectType,
		EventSubjectNamespace: n.SubjectNamespace,
		EventSubjectName:      n.SubjectName,
		ServiceKey:            n.ServiceKey,
		CloudResourceId:       n.CloudResourceId,
	}
}

// normalizeGCPIncident asks the collector for the event row polling would store for this
// incident. A collector outage fails the delivery; the incident still arrives by polling
// on accounts whose credentials can list alerts.
func normalizeGCPIncident(sc *security.RequestContext, accountId, webhookPayload string) (gcpNormalizedEvent, error) {
	// The tenant-admin context is nil when its account lookup failed.
	if sc.GetSecurityContext() == nil {
		return gcpNormalizedEvent{}, fmt.Errorf("no tenant context")
	}
	collectorURL := fmt.Sprintf("%s/v1/cloud/normalize_gcp_monitoring_webhook?account_id=%s",
		config.Config.CloudCollectorServerUrl, url.QueryEscape(accountId))
	httpReq, err := http.NewRequestWithContext(sc.GetContext(), http.MethodPost, collectorURL, bytes.NewReader([]byte(webhookPayload)))
	if err != nil {
		return gcpNormalizedEvent{}, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(config.Config.CloudCollectorServerTokenHeader, config.Config.CloudCollectorServerToken)
	httpReq.Header.Set("x-tenant-id", sc.GetSecurityContext().GetTenantId())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return gcpNormalizedEvent{}, fmt.Errorf("failed to call collector-server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return gcpNormalizedEvent{}, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return gcpNormalizedEvent{}, fmt.Errorf("collector-server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResponse struct {
		Data   gcpNormalizedEvent `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return gcpNormalizedEvent{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(apiResponse.Errors) > 0 {
		return gcpNormalizedEvent{}, fmt.Errorf("%s", apiResponse.Errors[0].Message)
	}
	if apiResponse.Data.FindingId == "" {
		return gcpNormalizedEvent{}, fmt.Errorf("collector-server returned no finding_id")
	}
	return apiResponse.Data, nil
}

func (m GCPMonitoringWebhook) TestConnection(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId string) (bool, error) {
	return true, nil
}
