package integrations

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"time"

	"github.com/google/uuid"
)

func init() {
	core.RegisterIntegration(PrometheusAlertManagerWebhook{})
}

type PrometheusAlertManagerWebhook struct {
}

const IntegrationPrometheusAlertManagerWebhook = "prometheus_alertmanager_webhook"

func (m PrometheusAlertManagerWebhook) Name() string {
	return IntegrationPrometheusAlertManagerWebhook
}

func (m PrometheusAlertManagerWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m PrometheusAlertManagerWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Prometheus Alertmanager Webhook",
				Default:          "",
				AutoGenerateFunc: "",
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			"token": {
				Type:             core.ToolSchemaTypeString,
				Default:          "",
				AutoGenerateFunc: "",
			},
		},
	}
}

func (m PrometheusAlertManagerWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m PrometheusAlertManagerWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

func (m PrometheusAlertManagerWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := map[string]any{}
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return []core.EventIncomingWebhook{}, err
	}

	alertsRaw, ok := payload["alerts"].([]any)
	if !ok || len(alertsRaw) == 0 {
		return []core.EventIncomingWebhook{}, fmt.Errorf("no alerts found in payload")
	}

	externalURL, _ := payload["externalURL"].(string)

	var results []core.EventIncomingWebhook

	for _, alertRaw := range alertsRaw {
		alert, ok := alertRaw.(map[string]any)
		if !ok {
			continue
		}

		labels := mapStringAnyToStringString(alert["labels"])
		if externalURL != "" {
			labels["externalURL"] = externalURL
		}
		annotations := mapStringAnyToStringString(alert["annotations"])

		startsAtStr, _ := alert["startsAt"].(string)
		startsAt, err := time.Parse(time.RFC3339, startsAtStr)
		if err != nil && startsAtStr != "" {
			slog.Warn("prometheus alertmanager webhook: failed to parse 'startsAt'", "value", startsAtStr, "error", err)
		}

		endsAtStr, _ := alert["endsAt"].(string)
		endsAt, err := time.Parse(time.RFC3339, endsAtStr)
		if err != nil && endsAtStr != "" {
			slog.Warn("prometheus alertmanager webhook: failed to parse 'endsAt'", "value", endsAtStr, "error", err)
		}

		fingerprint, _ := alert["fingerprint"].(string)
		generatorURL, _ := alert["generatorURL"].(string)
		status, _ := alert["status"].(string)

		if generatorURL != "" {
			labels["generatorURL"] = generatorURL
		}

		subjectKind, subjectName := extractPromSubject(labels)
		namespace := extractPromNamespace(labels)
		if namespace != "" && labels["namespace"] == "" {
			labels["namespace"] = namespace
		}
		if subjectName != "" && labels["service"] == "" {
			labels["service"] = subjectName
		}

		// If subject is the pod and we have a service label, use it as the
		// stable owner since pod names are ephemeral.
		var ownerKind, ownerName string
		if subjectKind == "pod" && labels["service"] != "" && labels["service"] != subjectName {
			ownerKind = "service"
			ownerName = labels["service"]
		}

		alertName := labels["alertname"]
		severity := labels["severity"]
		if severity == "" {
			severity = "warning"
		}
		// Detach from the request context so request completion does not
		// cancel the rule creation, and protect the service from a panic
		// inside CreateEventRule via recover.
		detachedSc := security.NewRequestContext(
			context.WithoutCancel(sc.GetContext()),
			sc.GetSecurityContext(),
			sc.GetLogger(),
			sc.GetTracer(),
			sc.GetMeter(),
		)
		go func(asc *security.RequestContext) {
			defer func() {
				if r := recover(); r != nil {
					asc.GetLogger().Error("prometheus alertmanager webhook: panic in CreateEventRule goroutine", "recover", r, "alert", alertName)
				}
			}()
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
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: severity},
				Alert:         alertName,
				AccountID:     accountId,
				Source:        IntegrationPrometheusAlertManagerWebhook,
				Category:      "alert",
				Severity:      severity,
				Enabled:       true,
				TriggerParams: []map[string]interface{}{},
				ActionParams:  []map[string]interface{}{},
			}
			if _, err := eventrule.CreateEventRule(asc, eventReq); err != nil {
				asc.GetLogger().Error("prometheus alertmanager webhook: CreateEventRule failed", "error", err, "alert", alertName)
			}
		}(detachedSc)

		title := annotations["summary"]
		if title == "" {
			title = alertName
		}

		var tags []string
		seen := map[string]bool{}
		for _, k := range []string{"cluster", "namespace", "service", "job"} {
			if v := labels[k]; v != "" && !seen[v] {
				tags = append(tags, v)
				seen[v] = true
			}
		}

		parsedPayload := core.EventIncomingWebhook{
			AccountId:             accountId,
			WebhookId:             uuid.NewString(),
			EventType:             alertName,
			EventId:               fingerprint,
			EventUrl:              generatorURL,
			EventStatus:           string(mapGrafanaStatus(status)),
			EventPriority:         string(mapGrafanaSeverity(labels["severity"])),
			EventCreatedAt:        startsAt,
			EventEndsAt:           endsAt,
			EventTitle:            title,
			EventDescription:      annotations["description"],
			EventTags:             tags,
			EventSubjectKind:      subjectKind,
			EventSubjectName:      subjectName,
			EventSubjectNamespace: namespace,
			EventSubjectOwner:     ownerName,
			EventSubjectOwnerKind: ownerKind,
			Investigation: core.EventIncomingWebhookInvestigation{
				RuleName:    alertName,
				Labels:      labels,
				Annotations: annotations,
				RuleType:    "prometheus",
				RuleId:      alertName,
				Fingerprint: fingerprint,
				Status:      mapGrafanaStatus(status),
				Severity:    mapGrafanaSeverity(labels["severity"]),
				SourceUrl:   generatorURL,
			},
		}

		results = append(results, parsedPayload)
	}

	if len(results) == 0 {
		return []core.EventIncomingWebhook{}, fmt.Errorf("no valid alerts found in payload")
	}

	return results, nil
}

// extractPromSubject picks the K8s subject from Prometheus alert labels.
// Priority: K8s controllers > service-mesh workload > service > pod > instance.
// Controllers preferred over pod because pod names are ephemeral. Service
// preferred over pod when no controller label is present since prometheus
// usually labels alerts with the K8s Service name. The `job` label is
// intentionally NOT a controller fallback — in Prometheus it almost always
// refers to the scrape job, not a K8s Job; use `kube_job` for that.
// `destination_workload_name` / `workload` come from service-mesh telemetry
// (Istio, Linkerd) where the K8s controller kind is not in the label set.
func extractPromSubject(labels map[string]string) (kind, name string) {
	for _, k := range []string{"deployment", "statefulset", "daemonset", "replicaset", "cronjob", "kube_job"} {
		if v := labels[k]; v != "" {
			kind = k
			if k == "kube_job" {
				kind = "job"
			}
			return kind, v
		}
	}
	for _, k := range []string{"destination_workload_name", "workload", "source_workload_name"} {
		if v := labels[k]; v != "" {
			return "workload", v
		}
	}
	if v := labels["service"]; v != "" {
		return "service", v
	}
	if v := labels["pod"]; v != "" {
		return "pod", v
	}
	if v := labels["instance"]; v != "" {
		return "instance", v
	}
	return "", ""
}

// extractPromNamespace picks the namespace from Prometheus alert labels,
// falling through service-mesh telemetry conventions when the canonical
// `namespace` label is absent.
func extractPromNamespace(labels map[string]string) string {
	for _, k := range []string{"namespace", "destination_workload_namespace", "workload_namespace", "source_workload_namespace", "kubernetes_namespace"} {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return ""
}

func mapStringAnyToStringString(input any) map[string]string {
	result := map[string]string{}
	if input == nil {
		return result
	}
	if casted, ok := input.(map[string]any); ok {
		for k, v := range casted {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
	}
	return result
}
