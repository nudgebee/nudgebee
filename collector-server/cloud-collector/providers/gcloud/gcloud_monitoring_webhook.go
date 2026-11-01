package gcloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nudgebee/collector/cloud/providers"
)

// GCP Cloud Monitoring pushes an incident to a webhook notification channel in its own
// shape (https://cloud.google.com/monitoring/support/notification-options#webhooks), not
// the v3 Alert shape polling reads. ConvertWebhookIncident maps it onto Alert and hands it
// to convertAlertToEvent, so a pushed incident gets exactly the identity (subject,
// fingerprint, finding_id, service key, labels) the same incident gets when polled.
//
// api-server used to normalize these payloads itself. That second copy drifted from
// polling repeatedly — the last time it named a Cloud SQL instance by its display name
// instead of its project:instance id, and the traces enricher found no spans for it
// (#34377). Keep identity logic in convertAlertToEvent only.

type webhookPayload struct {
	Incident webhookIncident `json:"incident"`
}

type webhookIncident struct {
	IncidentID       string             `json:"incident_id"`
	ScopingProjectID string             `json:"scoping_project_id"`
	URL              string             `json:"url"`
	StartedAt        int64              `json:"started_at"`
	EndedAt          int64              `json:"ended_at"`
	State            string             `json:"state"` // "open" or "closed"
	Summary          string             `json:"summary"`
	ObservedValue    string             `json:"observed_value"`
	ThresholdValue   string             `json:"threshold_value"`
	Resource         *MonitoredResource `json:"resource"`
	Metric           *webhookMetric     `json:"metric"`
	PolicyName       string             `json:"policy_name"`
	Severity         string             `json:"severity"` // "Critical" / "Error" / "Warning", may be empty
	PolicyUserLabels map[string]string  `json:"policy_user_labels"`
	ConditionName    string             `json:"condition_name"`
	Condition        struct {
		Name string `json:"name"` // projects/{project}/alertPolicies/{policy}/conditions/{condition}
	} `json:"condition"`
}

type webhookMetric struct {
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels"`
}

// ConvertWebhookIncident converts a GCP Monitoring webhook body into the Event polling
// would produce for the same incident.
func ConvertWebhookIncident(ctx providers.CloudProviderContext, account providers.Account, body []byte) (providers.Event, error) {
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return providers.Event{}, fmt.Errorf("invalid GCP Monitoring webhook payload: %w", err)
	}
	inc := payload.Incident
	if strings.TrimSpace(inc.IncidentID) == "" {
		return providers.Event{}, fmt.Errorf("invalid GCP Monitoring webhook payload: incident_id is missing")
	}

	alert := webhookIncidentToAlert(inc, account.AccountNumber)

	// Polling resolves a GCE instance's name from its numeric id through the Compute API
	// when the metric labels don't carry it. Build the client only for that case, so every
	// other webhook costs no token exchange.
	var httpClient *http.Client
	if needsInstanceNameLookup(alert) {
		session, err := getGcloudSessionFromAccount(ctx, account)
		if err == nil {
			httpClient, err = createHTTPClientFromOpts(ctx, session)
		}
		if err != nil {
			ctx.GetLogger().Warn("gcp webhook: instance name lookup unavailable, using instance id", "error", err)
		}
	}

	event := convertAlertToEvent(ctx, httpClient, alert, &account)
	addWebhookDeliveryContext(&event, inc)
	return event, nil
}

// webhookIncidentToAlert maps the webhook shape onto the v3 Alert fields
// convertAlertToEvent reads.
func webhookIncidentToAlert(inc webhookIncident, accountNumber string) *Alert {
	// The v3 API names an incident under the project it was listed from — the scoping
	// project that owns the policy — which is the project the webhook reports as
	// scoping_project_id. The resource's own project differs under a multi-project
	// metrics scope, so it must not be used here or finding_id stops matching polling.
	project := inc.ScopingProjectID
	if project == "" {
		project = accountNumber
	}

	// The webhook carries the policy only through its condition's resource name.
	policyPath := inc.Condition.Name
	if idx := strings.Index(policyPath, "/conditions/"); idx != -1 {
		policyPath = policyPath[:idx]
	}

	alert := &Alert{
		Name:     fmt.Sprintf("projects/%s/alerts/%s", project, inc.IncidentID),
		State:    AlertStateOpen,
		Resource: inc.Resource,
		Policy: &PolicySnapshot{
			Name:        policyPath,
			DisplayName: inc.PolicyName,
			// The API spells severity CRITICAL/ERROR/WARNING; the webhook title-cases it.
			Severity:   strings.ToUpper(inc.Severity),
			UserLabels: inc.PolicyUserLabels,
		},
	}
	if strings.EqualFold(inc.State, "closed") {
		alert.State = AlertStateClosed
	}
	if inc.StartedAt > 0 {
		alert.OpenTime = time.Unix(inc.StartedAt, 0).UTC().Format(time.RFC3339)
	}
	if inc.EndedAt > 0 {
		alert.CloseTime = time.Unix(inc.EndedAt, 0).UTC().Format(time.RFC3339)
	}
	if inc.Metric != nil && inc.Metric.Type != "" {
		alert.Metric = &Metric{Type: inc.Metric.Type, Labels: inc.Metric.Labels}
	} else {
		// A policy without a metric is a log-match policy; the API reports those with a
		// log block, which is what makes polling label them gcp_alert_type=log.
		alert.Log = &LogMetadata{}
	}
	return alert
}

func needsInstanceNameLookup(alert *Alert) bool {
	if alert.Resource == nil || alert.Resource.Type != "gce_instance" || alert.Resource.Labels["instance_id"] == "" {
		return false
	}
	return alert.Metric == nil || alert.Metric.Labels["instance_name"] == ""
}

// addWebhookDeliveryContext keeps what the webhook reports but the v3 Alert does not:
// GCP's one-line summary of the breach and the values behind it. None of it feeds the
// event's identity.
func addWebhookDeliveryContext(event *providers.Event, inc webhookIncident) {
	if inc.Summary != "" {
		event.Description += "\n" + inc.Summary
	}
	if event.Labels == nil {
		event.Labels = map[string]string{}
	}
	for key, value := range map[string]string{
		"gcp_console_url": inc.URL,
		"observed_value":  inc.ObservedValue,
		"threshold_value": inc.ThresholdValue,
		"condition_name":  inc.ConditionName,
	} {
		if value != "" {
			event.Labels[key] = value
		}
	}
}
