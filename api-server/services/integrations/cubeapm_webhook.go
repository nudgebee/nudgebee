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
	"strings"
	"time"

	"github.com/google/uuid"
)

func init() {
	core.RegisterIntegration(CubeAPMWebhook{})
}

const IntegrationCubeAPMWebhook = "cubeapm_webhook"

type CubeAPMWebhook struct{}

func (m CubeAPMWebhook) Name() string {
	return IntegrationCubeAPMWebhook
}

func (m CubeAPMWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m CubeAPMWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Description: "Add this endpoint as a Webhook notification channel in CubeAPM. Leave the " +
			"payload template unset — CubeAPM's default body is already Prometheus " +
			"Alertmanager-compatible, which is what this integration parses.",
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of CubeAPM Webhook",
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

func (m CubeAPMWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m CubeAPMWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// cubeAPMWebhookRuleType is stamped on every parsed alert's Investigation. It
// names the rule ENGINE, not the vendor: CubeAPM's alerting is Prometheus rule
// evaluation, and the downstream evidence builders branch on this value to decide
// how to render a rule's condition.
const cubeAPMWebhookRuleType = "prometheus"

// ProcessEventWebook parses a CubeAPM alert notification.
//
// CubeAPM's default webhook body is Prometheus Alertmanager-compatible, so the
// alert envelope is parsed exactly as prometheus_alertmanager_webhook parses it.
// What CubeAPM adds on top is four fields that Alertmanager has no equivalent for
// — `cubeImageURL` and `cubeSampleLog` per alert, and `groupKeyHash` /
// `incidentTime` at the top level — and those are where this handler earns its
// separate existence: the chart image and sample log become evidence blocks, which
// is the difference between an investigation that shows what fired and one that
// only names it.
func (m CubeAPMWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := map[string]any{}
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("cubeapm_webhook: failed to parse payload: %w", err)
	}

	alertsRaw, present := payload["alerts"]
	if !present {
		return nil, fmt.Errorf("cubeapm_webhook: payload has no 'alerts' array — check that the " +
			"notification channel uses CubeAPM's default (Alertmanager-compatible) body")
	}
	alerts, ok := alertsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("cubeapm_webhook: 'alerts' is not an array")
	}
	// A delivery that carries an empty alert list is a well-formed no-op — a
	// resolved group flushing, typically. Recording it as a failure would make
	// ingestion read as broken; ErrEventNotSupported records it as skipped.
	if len(alerts) == 0 {
		return nil, core.ErrEventNotSupported
	}

	externalURL, _ := payload["externalURL"].(string)
	groupKeyHash, _ := payload["groupKeyHash"].(string)
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

		// Group-level labels and annotations carry the context CubeAPM factored
		// out of the individual alerts (the cluster, the alert group's runbook).
		// Merged non-clobbering so a per-alert value always wins.
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
		if groupKeyHash != "" {
			labels["groupKeyHash"] = groupKeyHash
		}

		// Alertmanager carries runbook_url as an annotation, but the downstream
		// alert-rule evidence card reads Labels. Surface it there (non-clobber) so
		// the runbook link renders — the same bridge the Alertmanager and
		// PagerDuty parsers make.
		if rb := annotations["runbook_url"]; rb != "" && labels["runbook_url"] == "" {
			labels["runbook_url"] = rb
		}

		startsAt := parseCubeAPMWebhookTime(alert["startsAt"], "startsAt")
		endsAt := parseCubeAPMWebhookTime(alert["endsAt"], "endsAt")

		fingerprint, _ := alert["fingerprint"].(string)
		generatorURL, _ := alert["generatorURL"].(string)
		status, _ := alert["status"].(string)
		imageURL := cubeAPMWebhookString(alert["cubeImageURL"])
		sampleLog := cubeAPMWebhookString(alert["cubeSampleLog"])

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

		// A pod's name is ephemeral, so when the alert also names a service, that
		// service is the stable owner to attribute the event to.
		var ownerKind, ownerName string
		if subjectKind == "pod" && labels["service"] != "" && labels["service"] != subjectName {
			ownerKind = "service"
			ownerName = labels["service"]
		}

		alertName := labels["alertname"]
		if alertName == "" {
			// Without an alert name there is no event type to group by and no rule
			// to register; the group key is the only stable identifier left.
			alertName = groupKeyHash
		}
		if alertName == "" {
			alertName = "CubeAPM Alert"
		}

		severity := labels["severity"]
		if severity == "" {
			severity = "warning"
		}

		registerCubeAPMEventRule(sc, accountId, alertName, severity, annotations)

		title := annotations["summary"]
		if title == "" {
			title = alertName
		}

		evidences := []event.EventEvidence{buildCubeAPMAlertEvidence(alertName, status, labels, annotations)}
		if ev := buildCubeAPMSampleLogEvidence(sampleLog); ev != nil {
			evidences = append(evidences, *ev)
		}
		if ev := buildCubeAPMImageEvidence(imageURL); ev != nil {
			evidences = append(evidences, *ev)
		}

		// The generator URL points at the CubeAPM query that fired; it is the most
		// useful "open in vendor" target, with the install's own base URL as the
		// fallback for rules that carry none.
		eventURL := generatorURL
		if eventURL == "" {
			eventURL = externalURL
		}

		results = append(results, core.EventIncomingWebhook{
			AccountId:             accountId,
			WebhookId:             uuid.NewString(),
			EventType:             alertName,
			EventId:               fingerprint,
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
				RuleType:    cubeAPMWebhookRuleType,
				RuleId:      alertName,
				Fingerprint: fingerprint,
				Status:      mapGrafanaStatus(status),
				Severity:    mapGrafanaSeverity(severity),
				SourceUrl:   eventURL,
				Evidences:   evidences,
			},
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("cubeapm_webhook: no valid alerts found in payload")
	}
	return results, nil
}

// parseCubeAPMWebhookTime reads an RFC3339 timestamp, logging rather than failing
// on a malformed one — a bad timestamp should not discard an otherwise usable
// alert. CubeAPM renders these with Go's MarshalText, so a never-set endsAt
// arrives as the zero time rather than being omitted.
func parseCubeAPMWebhookTime(raw any, field string) time.Time {
	value, _ := raw.(string)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		slog.Warn("cubeapm webhook: failed to parse timestamp", "field", field, "value", value, "error", err)
		return time.Time{}
	}
	if parsed.IsZero() || parsed.Year() <= 1 {
		return time.Time{}
	}
	return parsed
}

// cubeAPMWebhookString reads a template-rendered field, treating a template that
// resolved to nothing as absent. CubeAPM renders `{{ .CubeImageURL }}` as an empty
// string when the alert has no chart, and Go templates print a nil pointer as
// "<no value>" — both mean "not present", and neither should become evidence.
func cubeAPMWebhookString(raw any) string {
	value, _ := raw.(string)
	value = strings.TrimSpace(value)
	if value == "" || value == "<no value>" || value == "<nil>" {
		return ""
	}
	return value
}

// cubeAPMWebhookTags picks the few labels worth showing as event tags, in a fixed
// order and deduplicated by value so a cluster and namespace sharing a name do not
// render twice.
func cubeAPMWebhookTags(labels map[string]string) []string {
	var tags []string
	seen := map[string]bool{}
	for _, k := range []string{"cluster", "namespace", "service", "job", "env"} {
		if v := labels[k]; v != "" && !seen[v] {
			tags = append(tags, v)
			seen[v] = true
		}
	}
	return tags
}

// registerCubeAPMEventRule upserts the event rule this alert belongs to, off the
// request path.
//
// The context is detached from the request so completing the HTTP response does
// not cancel the rule creation mid-write, and the goroutine recovers so a panic
// inside CreateEventRule takes down one registration rather than the service.
func registerCubeAPMEventRule(sc *security.RequestContext, accountId, alertName, severity string, annotations map[string]string) {
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
				asc.GetLogger().Error("cubeapm webhook: panic in CreateEventRule goroutine", "recover", r, "alert", alertName)
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
			Source:        IntegrationCubeAPMWebhook,
			Category:      "alert",
			Severity:      severity,
			Enabled:       true,
			TriggerParams: []map[string]interface{}{},
			ActionParams:  []map[string]interface{}{},
		}
		if _, err := eventrule.CreateEventRule(asc, eventReq); err != nil {
			asc.GetLogger().Error("cubeapm webhook: CreateEventRule failed", "error", err, "alert", alertName)
		}
	}(detachedSc)
}

// buildCubeAPMAlertEvidence renders the alert's identity and firing context as a
// table, so the investigation shows what fired without the reader scanning the raw
// label dump.
func buildCubeAPMAlertEvidence(alertName, status string, labels, annotations map[string]string) event.EventEvidence {
	rows := [][]any{{"Alert", alertName}}
	appendRow := func(label, value string) {
		if value != "" {
			rows = append(rows, []any{label, value})
		}
	}
	appendRow("Status", status)
	appendRow("Severity", labels["severity"])
	appendRow("Environment", labels["env"])
	appendRow("Cluster", labels["cluster"])
	appendRow("Namespace", labels["namespace"])
	appendRow("Service", labels["service"])
	appendRow("Summary", annotations["summary"])
	appendRow("Description", annotations["description"])
	appendRow("Query", labels["generatorURL"])

	return event.EventEvidence{
		Type:    "table",
		Insight: []event.EventEvidenceInsight{},
		Data: map[string]any{
			"column_renderers": map[string]any{},
			"headers":          []string{"field", "value"},
			"rows":             rows,
			"table_name":       "*CubeAPM alert*",
		},
		AdditionalInfo: cubeAPMEvidenceAdditionalInfo("cubeapm_alert", "CubeAPM Alert"),
	}
}

// buildCubeAPMSampleLogEvidence surfaces `cubeSampleLog` — the matching log line
// CubeAPM attaches to a log-based alert — as its own block. Returns nil when the
// alert is metric-based and carries none.
func buildCubeAPMSampleLogEvidence(sampleLog string) *event.EventEvidence {
	if sampleLog == "" {
		return nil
	}
	return &event.EventEvidence{
		Type:    "markdown",
		Insight: []event.EventEvidenceInsight{},
		Data: map[string]any{
			"name": "CubeAPM Sample Log",
			"data": "```\n" + sampleLog + "\n```",
		},
		AdditionalInfo: cubeAPMEvidenceAdditionalInfo("cubeapm_sample_log", "CubeAPM Sample Log"),
	}
}

// buildCubeAPMImageEvidence surfaces `cubeImageURL` — the rendered chart of the
// series that breached — as a markdown image. Non-http values are rejected rather
// than embedded: the field is template-rendered and an unvalidated value would put
// an arbitrary URI into markdown shown to every viewer of the investigation.
func buildCubeAPMImageEvidence(imageURL string) *event.EventEvidence {
	if imageURL == "" {
		return nil
	}
	if !strings.HasPrefix(imageURL, "http://") && !strings.HasPrefix(imageURL, "https://") {
		return nil
	}
	// A closing paren would terminate the markdown image early and let the rest of
	// the value render as text; percent-encoding it keeps the link intact.
	safeURL := strings.ReplaceAll(imageURL, ")", "%29")

	return &event.EventEvidence{
		Type:    "markdown",
		Insight: []event.EventEvidenceInsight{},
		Data: map[string]any{
			"name": "CubeAPM Alert Chart",
			"data": fmt.Sprintf("![CubeAPM alert chart](%s)", safeURL),
		},
		AdditionalInfo: cubeAPMEvidenceAdditionalInfo("cubeapm_alert_chart", "CubeAPM Alert Chart"),
	}
}

func cubeAPMEvidenceAdditionalInfo(actionName, actionTitle string) map[string]any {
	return map[string]any{
		"action_name":            actionName,
		"actual_action_name":     actionName,
		"action_title":           actionTitle,
		"conditional_expression": "",
	}
}
