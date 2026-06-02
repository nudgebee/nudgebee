package integrations

import (
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/google/uuid"
)

func init() {
	core.RegisterIntegration(GrafanaWebhook{})
}

type GrafanaWebhook struct{}

const IntegrationGrafanaWebhook = "grafana_webhook"

func (m GrafanaWebhook) Name() string {
	return IntegrationGrafanaWebhook
}

func (m GrafanaWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m GrafanaWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Grafana Webhook",
				Default:     "",
				Priority:    100,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"token": {
				Type:     core.ToolSchemaTypeString,
				Default:  "",
				Priority: 70,
			},
		},
	}
}

func (m GrafanaWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m GrafanaWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

func (m GrafanaWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := map[string]any{}
	err := common.UnmarshalJson([]byte(webhookPayloadString), &payload)
	if err != nil {
		return []core.EventIncomingWebhook{}, err
	}

	// Grafana sends alerts in the same format as Prometheus Alertmanager
	alertsRaw, ok := payload["alerts"].([]any)
	if !ok || len(alertsRaw) == 0 {
		return []core.EventIncomingWebhook{}, fmt.Errorf("no alerts found in payload")
	}

	var results []core.EventIncomingWebhook

	for _, alertRaw := range alertsRaw {
		alert, ok := alertRaw.(map[string]any)
		if !ok {
			continue
		}

		labels := mapStringAnyToStringString(alert["labels"])
		annotations := mapStringAnyToStringString(alert["annotations"])

		startsAtStr, ok := alert["startsAt"].(string)
		if !ok {
			slog.Warn("grafana webhook: missing or invalid 'startsAt' field", "alert", alert)
		}
		startsAt, err := time.Parse(time.RFC3339, startsAtStr)
		if err != nil && startsAtStr != "" {
			slog.Warn("grafana webhook: failed to parse 'startsAt'", "value", startsAtStr, "error", err)
		}

		endsAtStr, ok := alert["endsAt"].(string)
		if !ok {
			slog.Warn("grafana webhook: missing or invalid 'endsAt' field", "alert", alert)
		}
		endsAt, err := time.Parse(time.RFC3339, endsAtStr)
		if err != nil && endsAtStr != "" {
			slog.Warn("grafana webhook: failed to parse 'endsAt'", "value", endsAtStr, "error", err)
		}

		fingerprint, ok := alert["fingerprint"].(string)
		if !ok {
			slog.Warn("grafana webhook: missing or invalid 'fingerprint' field")
		}
		generatorURL, _ := alert["generatorURL"].(string)
		dashboardURL, _ := alert["dashboardURL"].(string)
		silenceURL, _ := alert["silenceURL"].(string)
		panelURL, _ := alert["panelURL"].(string)
		if silenceURL != "" {
			labels["silenceURL"] = silenceURL
		}
		if generatorURL != "" {
			labels["generatorURL"] = generatorURL
		}
		if dashboardURL != "" {
			labels["dashboardURL"] = dashboardURL
		}
		if panelURL != "" {
			labels["panelURL"] = panelURL
		}
		status, ok := alert["status"].(string)
		if !ok {
			slog.Warn("grafana webhook: missing or invalid 'status' field")
		}

		// Use generatorURL, fall back to dashboardURL or panelURL
		eventURL := generatorURL
		if eventURL == "" {
			eventURL = dashboardURL
		}
		if eventURL == "" {
			eventURL = panelURL
		}

		// Build title from annotations or labels
		title := annotations["summary"]
		if title == "" {
			title = labels["alertname"]
		}

		// Build tags from relevant labels
		var tags []string
		if v := labels["grafana_folder"]; v != "" {
			tags = append(tags, v)
		}
		if v := labels["namespace"]; v != "" {
			tags = append(tags, v)
		}
		if v := labels["cluster"]; v != "" {
			tags = append(tags, v)
		}

		subjectKind, subjectName := extractGrafanaSubject(labels)
		if subjectName != "" && labels["service"] == "" {
			labels["service"] = subjectName
		}

		alertName := labels["alertname"]
		severity := labels["severity"]
		if severity == "" {
			severity = "warning"
		}
		valueString, _ := alert["valueString"].(string)
		go func() {
			eventReq := eventrule.EventConfig{
				Annotations: struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
					Runbook     string `json:"runbook"`
				}{
					Description: annotations["description"],
					Summary:     annotations["summary"],
					Runbook:     annotations["runbook_url"],
				},
				Expr: valueString,
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: severity},
				Alert:         alertName,
				AccountID:     accountId,
				Source:        IntegrationGrafanaWebhook,
				Category:      "alert",
				Severity:      severity,
				Enabled:       true,
				TriggerParams: []map[string]interface{}{},
				ActionParams:  []map[string]interface{}{},
			}
			if _, err := eventrule.CreateEventRule(sc, eventReq); err != nil {
				sc.GetLogger().Error("grafana webhook: CreateEventRule failed", "error", err, "alert", alertName)
			}
		}()

		parsedPayload := core.EventIncomingWebhook{
			AccountId:             accountId,
			WebhookId:             uuid.NewString(),
			EventType:             labels["alertname"],
			EventId:               fingerprint,
			EventUrl:              eventURL,
			EventStatus:           string(mapGrafanaStatus(status)),
			EventPriority:         string(mapGrafanaSeverity(labels["severity"])),
			EventCreatedAt:        startsAt,
			EventEndsAt:           endsAt,
			EventTitle:            title,
			EventDescription:      annotations["description"],
			EventTags:             tags,
			EventSubjectKind:      subjectKind,
			EventSubjectName:      subjectName,
			EventSubjectNamespace: labels["namespace"],
			Investigation: core.EventIncomingWebhookInvestigation{
				RuleName:    labels["alertname"],
				Labels:      labels,
				Annotations: annotations,
				RuleType:    "grafana",
				RuleId:      labels["alertname"],
				Fingerprint: fingerprint,
				Status:      mapGrafanaStatus(status),
				Severity:    mapGrafanaSeverity(labels["severity"]),
				SourceUrl:   eventURL,
			},
		}

		results = append(results, parsedPayload)
	}

	if len(results) == 0 {
		return []core.EventIncomingWebhook{}, fmt.Errorf("no valid alerts found in payload")
	}

	return results, nil
}

// extractGrafanaSubject picks the K8s workload subject from alert labels.
// Priority: pod > deployment > statefulset > daemonset > replicaset > job > cronjob.
func extractGrafanaSubject(labels map[string]string) (kind, name string) {
	for _, k := range []string{"pod", "deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob"} {
		if v := labels[k]; v != "" {
			return k, v
		}
	}
	return "", ""
}

// mapGrafanaStatus maps Grafana/Prometheus lowercase status to EventStatus constants.
func mapGrafanaStatus(status string) event.EventStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "firing":
		return event.EventStatusFiring
	case "resolved":
		return event.EventStatusResolved
	case "closed":
		return event.EventStatusClosed
	default:
		return event.EventStatusFiring
	}
}

// mapGrafanaSeverity maps Grafana/Prometheus lowercase severity to EventPriortiy constants.
func mapGrafanaSeverity(severity string) event.EventPriortiy {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return event.EventPriortiyHigh
	case "high":
		return event.EventPriortiyHigh
	case "warning", "warn", "medium":
		return event.EventPriortiyMedium
	case "low":
		return event.EventPriortiyLow
	case "info", "informational", "none":
		return event.EventPriortiyInfo
	case "debug":
		return event.EventPriortiyDebug
	default:
		return event.EventPriortiyLow
	}
}
