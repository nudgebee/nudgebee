package integrations

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
)

func init() {
	core.RegisterIntegration(SigNozWebhook{})
}

const IntegrationSigNozWebhook = "signoz_webhook"

// signozWebhookRuleType names the alert engine on every parsed alert. It is
// SigNoz's own rule evaluator, not Prometheus: a SigNoz log or trace alert's
// query is not PromQL and must not be treated as one downstream.
const signozWebhookRuleType = "signoz"

// SigNozWebhook ingests SigNoz alert notifications sent by a Webhook
// notification channel. Alert rules that Nudgebee creates in SigNoz are routed
// to the channels whose URL carries this integration's token.
type SigNozWebhook struct{}

func (m SigNozWebhook) Name() string {
	return IntegrationSigNozWebhook
}

func (m SigNozWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m SigNozWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Description: "Add this endpoint as a Webhook notification channel in SigNoz (Settings → Alert Channels → " +
			"New Channel → Webhook). Alert rules Nudgebee creates in SigNoz notify the channels that use this URL.",
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of SigNoz Webhook",
				Default:     "",
				Priority:    100,
			},
			core.AccountId: {
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

func (m SigNozWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m SigNozWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// ProcessEventWebook parses a SigNoz alert notification. SigNoz's webhook body
// is Prometheus Alertmanager's (version "4"); what SigNoz adds is the ruleId and
// ruleSource labels, and group-by labels that keep their OTel dotted names
// (k8s.namespace.name, k8s.pod.name, k8s.deployment.name), which the shared
// subject extractors read.
func (m SigNozWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := map[string]any{}
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("signoz_webhook: failed to parse payload: %w", err)
	}
	alertsRaw, present := payload["alerts"]
	if !present {
		return nil, fmt.Errorf("signoz_webhook: payload has no 'alerts' array — check that the SigNoz channel type is Webhook")
	}
	alerts, ok := alertsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("signoz_webhook: 'alerts' is not an array")
	}
	if len(alerts) == 0 {
		return nil, core.ErrEventNotSupported
	}

	externalURL, _ := payload["externalURL"].(string)
	commonLabels := mapStringAnyToStringString(payload["commonLabels"])
	commonAnnotations := mapStringAnyToStringString(payload["commonAnnotations"])

	var results []core.EventIncomingWebhook
	for _, alertRaw := range alerts {
		alert, ok := alertRaw.(map[string]any)
		if !ok {
			continue
		}
		labels := mapStringAnyToStringString(alert["labels"])
		annotations := mapStringAnyToStringString(alert["annotations"])
		for k, v := range commonLabels {
			if labels[k] == "" {
				labels[k] = v
			}
		}
		for k, v := range commonAnnotations {
			if annotations[k] == "" {
				annotations[k] = v
			}
		}
		if externalURL != "" {
			labels["externalURL"] = externalURL
		}
		if rb := annotations["runbook_url"]; rb != "" && labels["runbook_url"] == "" {
			labels["runbook_url"] = rb
		}

		startsAt := parseCubeAPMWebhookTime(alert["startsAt"], "startsAt")
		endsAt := parseCubeAPMWebhookTime(alert["endsAt"], "endsAt")
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
		// A pod name is not a service; leave service unset rather than fill it
		// with an ephemeral name.
		if subjectName != "" && subjectKind != "pod" && labels["service"] == "" {
			labels["service"] = subjectName
		}
		var ownerKind, ownerName string
		if subjectKind == "pod" && labels["service"] != "" && labels["service"] != subjectName {
			ownerKind = "service"
			ownerName = labels["service"]
		}

		alertName := labels["alertname"]
		if alertName == "" {
			alertName = "SigNoz Alert"
		}
		severity := labels["severity"]
		if severity == "" {
			severity = "warning"
		}

		signozRegisterEventRule(sc, accountId, alertName, severity, labels["ruleId"], annotations)

		title := annotations["summary"]
		if title == "" {
			title = alertName
		}
		// The rule page in SigNoz is the "open in vendor" target; ruleSource and
		// generatorURL both point at it.
		eventURL := firstNonEmptyString(labels["ruleSource"], generatorURL, externalURL)

		results = append(results, core.EventIncomingWebhook{
			AccountId:             accountId,
			WebhookId:             uuid.NewString(),
			EventType:             alertName,
			EventId:               fingerprint,
			FiringKey:             core.FiringKeyFromStart(fingerprint, startsAt),
			EventUrl:              eventURL,
			EventStatus:           string(mapGrafanaStatus(status)),
			EventPriority:         string(mapGrafanaSeverity(severity)),
			EventCreatedAt:        startsAt,
			EventEndsAt:           endsAt,
			EventTitle:            title,
			EventDescription:      annotations["description"],
			EventTags:             cubeAPMWebhookTags(labels),
			EventSubjectKind:      subjectKind,
			EventSubjectName:      subjectName,
			EventSubjectNamespace: namespace,
			EventSubjectOwner:     ownerName,
			EventSubjectOwnerKind: ownerKind,
			Investigation: core.EventIncomingWebhookInvestigation{
				RuleName:    alertName,
				Labels:      labels,
				Annotations: annotations,
				RuleType:    signozWebhookRuleType,
				// The alert name, as for every Alertmanager-family source: it is what
				// auto-analysis rules match on and what stays stable when the rule is
				// recreated. The SigNoz rule id stays on the labels and the rule row.
				RuleId:      alertName,
				Fingerprint: fingerprint,
				Status:      mapGrafanaStatus(status),
				Severity:    mapGrafanaSeverity(severity),
				SourceUrl:   eventURL,
				Evidences:   []event.EventEvidence{buildSigNozAlertEvidence(alertName, status, labels, annotations)},
			},
		})
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("signoz_webhook: no valid alerts found in payload")
	}
	return results, nil
}

// signozRegisterEventRule is a variable so tests can parse a payload without a
// database behind the rule upsert.
var signozRegisterEventRule = registerSigNozEventRule

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// registerSigNozEventRule upserts the event rule this alert belongs to, off the
// request path, carrying SigNoz's rule id so a later edit in Nudgebee writes to
// the rule that fired. The context is detached from the request, so finishing
// the response does not cancel the write, and bounded, so a stuck write cannot
// hold the goroutine forever.
//
// On this release the rule row cannot carry SigNoz's rule id from a firing
// (EventConfig has no ExternalRuleID); provider sync fills it in.
func registerSigNozEventRule(sc *security.RequestContext, accountId, alertName, severity, _ string, annotations map[string]string) {
	if sc == nil {
		return
	}
	logger := sc.GetLogger()
	if logger == nil {
		logger = slog.Default()
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("signoz webhook: panic in CreateEventRule goroutine", "recover", r, "stack", string(debug.Stack()), "alert", alertName)
			}
		}()

		ctx, cancel := context.WithTimeout(context.WithoutCancel(sc.GetContext()), signozRuleRegistrationTimeout)
		defer cancel()
		asc := security.NewRequestContext(ctx, sc.GetSecurityContext(), logger, sc.GetTracer(), sc.GetMeter())

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
			Source:        IntegrationSigNozWebhook,
			Category:      "alert",
			Severity:      severity,
			Enabled:       true,
			TriggerParams: []map[string]interface{}{},
			ActionParams:  []map[string]interface{}{},
		}
		if _, err := eventrule.CreateEventRule(asc, eventReq); err != nil {
			logger.Error("signoz webhook: CreateEventRule failed", "error", err, "alert", alertName)
		}
	}()
}

const signozRuleRegistrationTimeout = 30 * time.Second

// buildSigNozAlertEvidence renders the alert's identity and firing context as a
// table.
func buildSigNozAlertEvidence(alertName, status string, labels, annotations map[string]string) event.EventEvidence {
	rows := [][]any{{"Alert", alertName}}
	appendRow := func(label, value string) {
		if value != "" {
			rows = append(rows, []any{label, value})
		}
	}
	appendRow("Status", status)
	appendRow("Severity", labels["severity"])
	appendRow("Cluster", firstNonEmptyString(labels["cluster"], labels["k8s.cluster.name"]))
	appendRow("Namespace", labels["namespace"])
	appendRow("Workload", labels["service"])
	appendRow("Pod", labels["k8s.pod.name"])
	appendRow("Summary", annotations["summary"])
	appendRow("Description", annotations["description"])
	appendRow("Related logs", annotations["related_logs"])
	appendRow("Related traces", annotations["related_traces"])
	appendRow("Rule", labels["ruleSource"])

	return event.EventEvidence{
		Type:    "table",
		Insight: []event.EventEvidenceInsight{},
		Data: map[string]any{
			"column_renderers": map[string]any{},
			"headers":          []string{"field", "value"},
			"rows":             rows,
			"table_name":       "*SigNoz alert*",
		},
		AdditionalInfo: map[string]any{
			"action_name":            "signoz_alert",
			"actual_action_name":     "signoz_alert",
			"action_title":           "SigNoz Alert",
			"conditional_expression": "",
		},
	}
}
