package integrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
)

func init() {
	core.RegisterIntegration(OrionWebhook{})
}

// OrionWebhook receives SolarWinds Orion (SolarWinds Observability Self-Hosted)
// alerts. Orion posts them from an alert action ("Send a GET or POST Request to a
// Web Server") with a body the admin pastes once: flat facts, each an Orion macro.
// Identity is computed here from ids Orion keeps stable, never from display names:
// a node's Caption, a volume's "C:\" or an interface's "Gi0/1" repeat across objects.
//
// Verified on Orion 2026.4.0 with real trigger and reset deliveries:
//   - alert_def_id (${N=Alerting;M=AlertDefID}) is the alert definition's GUID.
//   - object_url (${N=SwisEntity;M=DetailsUrl}) carries NetObject=N:4 — the object's
//     own id (N: node, V: volume, I: interface), the same on trigger and reset.
//   - alert_active_id (${N=Alerting;M=AlertActiveID}) names one firing and is the
//     same on its trigger and its reset; it is empty when an action is run with Test.
//   - ip (${N=SwisEntity;M=IP_Address}) is read when the action runs, so a reset can
//     carry a different address than its trigger: it is a label, never identity.
type OrionWebhook struct{}

const IntegrationOrionWebhook = "orion_webhook"

func (m OrionWebhook) Name() string {
	return IntegrationOrionWebhook
}

func (m OrionWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

// WebhookLabelHelp describes the labels mapOrionAlertToEvent builds. Update it with the parser.
func (m OrionWebhook) WebhookLabelHelp() core.WebhookLabelHelp {
	return core.WebhookLabelHelp{
		Summary: "Each body field below becomes a label under a fixed name.",
		Labels: []core.WebhookLabelSource{
			{Label: "ip", Source: "ip"},
			{Label: "object_type", Source: "object_type"},
			{Label: "orion_object", Source: "object"},
			{Label: "orion_netobject", Source: "the NetObject value in object_url"},
			{Label: "orion_alert_def_id", Source: "alert_def_id"},
			{Label: "orion_alert_active_id", Source: "alert_active_id"},
			{Label: "orion_severity", Source: "severity"},
			{Label: "orion_identity", Source: "set to caption_fallback when object_url has no NetObject"},
		},
		Notes: []string{
			"A value Orion could not resolve arrives as the literal ${...} macro and is treated as missing.",
			"status, alert_name, message and url are not labels; source_tool is always SolarWinds Platform.",
		},
	}
}

func (m OrionWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of SolarWinds Platform (Orion) Webhook",
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

func (m OrionWebhook) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m OrionWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// SkipResolvedReplay: replaying a reset would re-investigate it as a firing.
func (m OrionWebhook) SkipResolvedReplay() bool {
	return true
}

// ValidateWebhookPayload is the synchronous check run before a delivery is
// stored, so a body pasted with a missing field gets a 400 naming it.
func (m OrionWebhook) ValidateWebhookPayload(webhookPayload string) error {
	if _, verr := parseOrionPayload(webhookPayload); verr != nil {
		return verr
	}
	return nil
}

type orionPayload struct {
	Status        string `json:"status"`
	AlertDefId    string `json:"alert_def_id"`
	AlertActiveId string `json:"alert_active_id"`
	AlertName     string `json:"alert_name"`
	Object        string `json:"object"`
	ObjectType    string `json:"object_type"`
	ObjectUrl     string `json:"object_url"`
	Ip            string `json:"ip"`
	Severity      string `json:"severity"`
	Message       string `json:"message"`
	Url           string `json:"url"`

	netObject string
}

// orionValue treats a macro Orion could not resolve as missing: it arrives as the
// literal "${N=...}" text (seen for ${N=SwisEntity;M=Uri}), or empty under Test.
func orionValue(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "${") {
		return ""
	}
	return v
}

// orionNetObject returns the NetObject query value of an Orion details URL, e.g.
// "N:4" from https://host/Orion/NetPerfMon/NodeDetails.aspx?NetObject=N:4.
func orionNetObject(detailsUrl string) string {
	u, err := url.Parse(detailsUrl)
	if err != nil {
		return ""
	}
	for k, v := range u.Query() {
		if strings.EqualFold(k, "NetObject") && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

func parseOrionPayload(payload string) (orionPayload, *core.WebhookValidationError) {
	verr := &core.WebhookValidationError{}
	add := func(field, reason string) {
		verr.Items = append(verr.Items, core.WebhookValidationItemError{Index: -1, Field: field, Reason: reason})
	}

	trimmed := bytes.TrimSpace([]byte(payload))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		add("payload", "must be a JSON object")
		return orionPayload{}, verr
	}
	var p orionPayload
	if err := json.Unmarshal(trimmed, &p); err != nil {
		add("payload", "invalid JSON: "+err.Error())
		return orionPayload{}, verr
	}
	for _, f := range []*string{&p.Status, &p.AlertDefId, &p.AlertActiveId, &p.AlertName, &p.Object,
		&p.ObjectType, &p.ObjectUrl, &p.Ip, &p.Severity, &p.Message, &p.Url} {
		*f = orionValue(*f)
	}
	p.Status = strings.ToLower(p.Status)
	p.netObject = orionNetObject(p.ObjectUrl)

	if p.Status != "firing" && p.Status != "resolved" {
		add("status", `must be "firing" (trigger action) or "resolved" (reset action)`)
	}
	if p.AlertDefId == "" {
		add("alert_def_id", "is required: ${N=Alerting;M=AlertDefID}")
	}
	if p.AlertName == "" {
		add("alert_name", "is required: ${N=Alerting;M=AlertName}")
	}
	if p.netObject == "" && p.Object == "" {
		add("object_url", "is required (or object): ${N=SwisEntity;M=DetailsUrl}")
	}
	// The url becomes the alert's link in the UI, so only plain web links pass.
	if p.Url != "" {
		if u, err := url.Parse(p.Url); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("url", "must be an http or https URL")
		}
	}
	if len(verr.Items) > 0 {
		return orionPayload{}, verr
	}
	return p, nil
}

// mapOrionSeverity maps Orion's severity words onto event priorities.
func mapOrionSeverity(severity string) event.EventPriority {
	switch strings.ToLower(severity) {
	case "critical", "serious":
		return event.EventPriorityHigh
	case "warning":
		return event.EventPriorityMedium
	case "notice":
		return event.EventPriorityLow
	case "informational", "info":
		return event.EventPriorityInfo
	default:
		return event.EventPriorityMedium
	}
}

// orionFingerprint names one alert on one object, scoped to the integration so two
// Orion servers (whose NetObject ids both start at N:1) never share an event.
// It is the EventId as well: a reset closes the alert's open row by it, so closing
// never depends on matching a timestamp.
func orionFingerprint(integrationId string, p orionPayload) (fingerprint string, captionFallback bool) {
	object := p.netObject
	if object == "" {
		object = "caption:" + p.Object
		captionFallback = true
	}
	return "ow:" + integrationId + ":" + p.AlertDefId + "|" + object, captionFallback
}

func mapOrionAlertToEvent(integrationId, accountId string, p orionPayload) core.EventIncomingWebhook {
	status := event.EventStatusFiring
	if p.Status == "resolved" {
		status = event.EventStatusResolved
	}
	priority := mapOrionSeverity(p.Severity)
	fingerprint, captionFallback := orionFingerprint(integrationId, p)

	title := p.AlertName
	if p.Object != "" {
		title = p.AlertName + " on " + p.Object
	}

	labels := map[string]string{"source_tool": "SolarWinds Platform"}
	putIfNotEmpty(labels, "ip", p.Ip)
	putIfNotEmpty(labels, "object_type", p.ObjectType)
	putIfNotEmpty(labels, "orion_object", p.Object)
	putIfNotEmpty(labels, "orion_netobject", p.netObject)
	putIfNotEmpty(labels, "orion_alert_def_id", p.AlertDefId)
	putIfNotEmpty(labels, "orion_alert_active_id", p.AlertActiveId)
	putIfNotEmpty(labels, "orion_severity", p.Severity)
	if captionFallback {
		labels["orion_identity"] = "caption_fallback"
	}

	webhookEvent := core.EventIncomingWebhook{
		AccountId: accountId,
		WebhookId: "ow-" + fingerprint + "-" + p.AlertActiveId + "-" + p.Status,
		EventType: IntegrationOrionWebhook,
		EventId:   fingerprint,
		// One firing. Empty under Orion's Test button, which leaves every Test
		// delivery on the alert's one row.
		FiringKey:        p.AlertActiveId,
		EventUrl:         p.Url,
		EventStatus:      string(status),
		EventPriority:    string(priority),
		EventTitle:       title,
		EventDescription: p.Message,
		// Zero → the framework uses the delivery time. Orion's trigger-time macro is
		// server-local text with no zone, so it is not used.
		Investigation: core.EventIncomingWebhookInvestigation{
			// RuleName/RuleId become alertname and aggregation_key, which event_rules.alert
			// is matched on: the alert's name, as CreateEventRule writes it below.
			RuleName:    p.AlertName,
			RuleId:      p.AlertName,
			RuleType:    IntegrationOrionWebhook,
			Fingerprint: fingerprint,
			Status:      status,
			Severity:    priority,
			SourceUrl:   p.Url,
			Labels:      labels,
			Annotations: map[string]string{},
		},
	}

	if p.Object != "" {
		webhookEvent.EventSubjectName = p.Object
		webhookEvent.EventSubjectKind = "other"
		if strings.EqualFold(p.ObjectType, "Node") {
			webhookEvent.EventSubjectKind = "host"
		}
		// Orion named the object: keep it verbatim, and skip the K8s workload match,
		// which would otherwise try a node's Caption as a pod name.
		labels["nb_skip_workload_match"] = "true"
	}
	return webhookEvent
}

// orionEventRule is the event_rules upsert for a firing, so the Orion alert appears
// in rule management, muting and triage. Alert is the alert's name — the value
// aggregation_key carries — so native-rule registration finds this row instead of
// minting a second one. Pure, so the shape can be asserted without the goroutine.
func orionEventRule(accountId string, p orionPayload, priority event.EventPriority) eventrule.EventConfig {
	severity := eventRuleSeverity(priority)
	return eventrule.EventConfig{
		Annotations: struct {
			Description string `json:"description"`
			Summary     string `json:"summary"`
			Runbook     string `json:"runbook"`
		}{
			Description: p.Message,
			Summary:     p.AlertName,
		},
		Labels: struct {
			Severity string `json:"severity"`
		}{Severity: severity},
		Alert:     p.AlertName,
		Duration:  "0",
		AccountID: accountId,
		Source:    IntegrationOrionWebhook,
		Category:  "alert",
		Severity:  severity,
		Enabled:   true,
		// The alert definition's GUID in Orion (Orion.AlertConfigurations.AlertRefID).
		ExternalRuleID: p.AlertDefId,
		TriggerParams:  []map[string]any{},
		ActionParams:   []map[string]any{},
	}
}

func (m OrionWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	integrationId, _ := core.GetSettingValue(settings, core.SyntheticIntegrationIdKey)
	if integrationId == "" {
		return nil, errors.New("orion_webhook: integration id missing from settings")
	}
	p, verr := parseOrionPayload(webhookPayloadString)
	if verr != nil {
		return nil, verr
	}
	webhookEvent := mapOrionAlertToEvent(integrationId, accountId, p)

	// Skipped on reset: the rule already exists from the trigger. Async so a slow
	// write never delays the response.
	if p.Status == "firing" {
		rule := orionEventRule(accountId, p, mapOrionSeverity(p.Severity))
		go func() {
			defer func() {
				if r := recover(); r != nil {
					sc.GetLogger().Error("orion_webhook: panic in CreateEventRule goroutine", "panic", r)
				}
			}()
			if _, err := eventrule.CreateEventRule(sc, rule); err != nil {
				sc.GetLogger().Error("orion_webhook: CreateEventRule failed", "error", err, "alert", p.AlertName)
			}
		}()
	}
	return []core.EventIncomingWebhook{webhookEvent}, nil
}
