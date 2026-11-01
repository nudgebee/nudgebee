package integrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(EventWebhook{})
}

// IntegrationEventWebhook lets any tool that can post JSON send alerts
// straight in, using the documented contract below instead of a per-tool
// integration. Not to be confused with workflow_webhook (generic_webhook.go),
// which triggers automations.
//
// Identity follows the Zenduty model: `id` names one firing (→ finding_id) and
// `fingerprint` names the alert across firings (→ fingerprint), so a re-fire
// after a resolve becomes a new event linked to the old one in history.
const IntegrationEventWebhook = "event_webhook"

const (
	eventWebhookMaxBatch    = 100
	eventWebhookMaxIdLength = 256
	eventWebhookEventType   = "event_webhook"
)

// eventWebhookSeverities maps the contract's severities onto event priorities,
// the same way core.normalizeSeverity does for other webhooks.
var eventWebhookSeverities = map[string]event.EventPriority{
	"critical": event.EventPriorityHigh,
	"high":     event.EventPriorityHigh,
	"medium":   event.EventPriorityMedium,
	"low":      event.EventPriorityLow,
	"info":     event.EventPriorityLow,
}

type EventWebhook struct{}

func (m EventWebhook) Name() string {
	return IntegrationEventWebhook
}

func (m EventWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m EventWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Event Webhook",
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

func (m EventWebhook) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m EventWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// SkipResolvedReplay: replaying a resolve would re-investigate it as a firing.
func (m EventWebhook) SkipResolvedReplay() bool {
	return true
}

// ValidateWebhookPayload is the synchronous check run before a delivery is
// stored. It is the same parse ProcessEventWebook runs, so the two can't drift.
func (m EventWebhook) ValidateWebhookPayload(webhookPayload string) error {
	if _, verr := parseEventWebhookPayload(webhookPayload); verr != nil {
		return verr
	}
	return nil
}

type eventWebhookSubject struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Namespace string `json:"namespace"`
}

type eventWebhookItem struct {
	Id          string               `json:"id"`
	Fingerprint string               `json:"fingerprint"`
	Status      string               `json:"status"`
	Title       string               `json:"title"`
	RuleName    string               `json:"rule_name"`
	Severity    string               `json:"severity"`
	Description string               `json:"description"`
	StartsAt    string               `json:"starts_at"`
	EndsAt      string               `json:"ends_at"`
	Subject     *eventWebhookSubject `json:"subject"`
	SourceTool  string               `json:"source_tool"`
	Url         string               `json:"url"`
	Labels      map[string]string    `json:"labels"`

	startsAt time.Time
	endsAt   time.Time
}

// parseEventWebhookPayload parses and validates a delivery: a single alert
// object, or {"events": [...]}. Any invalid item rejects the whole delivery.
func parseEventWebhookPayload(payload string) ([]eventWebhookItem, *core.WebhookValidationError) {
	fail := func(index int, field, reason string) *core.WebhookValidationError {
		return &core.WebhookValidationError{Items: []core.WebhookValidationItemError{{Index: index, Field: field, Reason: reason}}}
	}

	trimmed := bytes.TrimSpace([]byte(payload))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fail(-1, "payload", "must be a JSON object: one alert, or {\"events\": [...]}")
	}
	var envelope struct {
		Events *[]json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, fail(-1, "payload", "invalid JSON: "+err.Error())
	}
	raws := []json.RawMessage{trimmed}
	if envelope.Events != nil {
		raws = *envelope.Events
		if len(raws) == 0 {
			return nil, fail(-1, "events", "must contain at least one alert")
		}
		if len(raws) > eventWebhookMaxBatch {
			return nil, fail(-1, "events", fmt.Sprintf("at most %d alerts per request", eventWebhookMaxBatch))
		}
	}

	verr := &core.WebhookValidationError{}
	items := make([]eventWebhookItem, 0, len(raws))
	for i, raw := range raws {
		var item eventWebhookItem
		if err := json.Unmarshal(raw, &item); err != nil {
			verr.Items = append(verr.Items, core.WebhookValidationItemError{Index: i, Field: "payload", Reason: "invalid alert object: " + err.Error()})
			continue
		}
		verr.Items = append(verr.Items, validateEventWebhookItem(i, &item)...)
		items = append(items, item)
	}
	if len(verr.Items) > 0 {
		return nil, verr
	}
	return items, nil
}

func validateEventWebhookItem(index int, item *eventWebhookItem) []core.WebhookValidationItemError {
	var errs []core.WebhookValidationItemError
	add := func(field, reason string) {
		errs = append(errs, core.WebhookValidationItemError{Index: index, Field: field, Reason: reason})
	}

	// id, fingerprint and rule_name become stored identities (finding_id,
	// fingerprint, aggregation key), so they get the strictest checks.
	for _, f := range []struct{ name, value string }{
		{"id", item.Id}, {"fingerprint", item.Fingerprint}, {"rule_name", item.RuleName},
	} {
		if reason := checkEventWebhookIdentity(f.value); reason != "" {
			add(f.name, reason)
		}
	}
	if strings.TrimSpace(item.Title) == "" {
		add("title", "is required")
	}

	item.Status = strings.ToLower(strings.TrimSpace(item.Status))
	if item.Status != "firing" && item.Status != "resolved" {
		add("status", `must be "firing" or "resolved"`)
	}

	item.Severity = strings.ToLower(strings.TrimSpace(item.Severity))
	if item.Severity == "" {
		item.Severity = "medium"
	}
	if _, ok := eventWebhookSeverities[item.Severity]; !ok {
		add("severity", "must be one of critical, high, medium, low, info")
	}

	var err error
	if item.StartsAt != "" {
		if item.startsAt, err = time.Parse(time.RFC3339, item.StartsAt); err != nil {
			add("starts_at", "must be RFC 3339 with a timezone, e.g. 2026-09-29T10:00:00Z")
		}
	}
	if item.EndsAt != "" {
		if item.endsAt, err = time.Parse(time.RFC3339, item.EndsAt); err != nil {
			add("ends_at", "must be RFC 3339 with a timezone, e.g. 2026-09-29T10:00:00Z")
		}
	}
	if !item.startsAt.IsZero() && !item.endsAt.IsZero() && item.endsAt.Before(item.startsAt) {
		add("ends_at", "must not be before starts_at")
	}

	// The url becomes the alert's link in the UI, so only plain web links pass:
	// a javascript: or data: URL must never be rendered as a clickable link.
	if item.Url != "" {
		if u, err := url.Parse(item.Url); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("url", "must be an http or https URL")
		}
	}

	for k := range item.Labels {
		if strings.HasPrefix(k, "nb_") {
			add("labels."+k, `keys starting with "nb_" are reserved`)
		}
	}
	return errs
}

// checkEventWebhookIdentity returns why an identity value is invalid, or "".
func checkEventWebhookIdentity(v string) string {
	if strings.TrimSpace(v) == "" {
		return "is required"
	}
	if len(v) > eventWebhookMaxIdLength {
		return fmt.Sprintf("must be at most %d characters", eventWebhookMaxIdLength)
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return "must not contain control characters"
		}
	}
	return ""
}

// eventWebhookEventId and eventWebhookFingerprint scope sender-chosen values to
// this integration, in separate namespaces: resolveEventQuery matches
// `fingerprint = $3 OR finding_id = $3`, so an id must never equal a fingerprint
// even when a sender uses the same string for both.
func eventWebhookEventId(integrationId, id string) string {
	return "ew:" + integrationId + ":id:" + id
}

func eventWebhookFingerprint(integrationId, fingerprint string) string {
	return "ew:" + integrationId + ":fp:" + fingerprint
}

func (m EventWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	integrationId, _ := core.GetSettingValue(settings, core.SyntheticIntegrationIdKey)
	if integrationId == "" {
		// Without it every id would be "ew::…" and never match earlier events.
		return nil, errors.New("event_webhook: integration id missing from settings")
	}

	items, verr := parseEventWebhookPayload(webhookPayloadString)
	if verr != nil {
		return nil, verr
	}

	events := make([]core.EventIncomingWebhook, 0, len(items))
	for _, item := range items {
		events = append(events, buildEventWebhookEvent(integrationId, accountId, item))
	}
	return events, nil
}

func buildEventWebhookEvent(integrationId, accountId string, item eventWebhookItem) core.EventIncomingWebhook {
	status := event.EventStatusFiring
	if item.Status == "resolved" {
		status = event.EventStatusResolved
	}
	priority := eventWebhookSeverities[item.Severity]
	fingerprint := eventWebhookFingerprint(integrationId, item.Fingerprint)

	labels := make(map[string]string, len(item.Labels)+3)
	for k, v := range item.Labels {
		labels[k] = v
	}
	if item.SourceTool != "" {
		labels["source_tool"] = item.SourceTool
	}
	// Group by alert, not by firing: convertWebhookEventToEvent would otherwise
	// default pattern_hash to the per-firing EventId.
	labels["pattern_hash"] = fingerprint

	webhookEvent := core.EventIncomingWebhook{
		WebhookId:        fmt.Sprintf("ew-%s-%s", item.Id, item.Status),
		EventType:        eventWebhookEventType,
		EventId:          eventWebhookEventId(integrationId, item.Id),
		EventUrl:         item.Url,
		EventStatus:      string(status),
		EventPriority:    string(priority),
		EventTitle:       item.Title,
		EventDescription: item.Description,
		EventCreatedAt:   item.startsAt, // zero → framework substitutes time.Now()
		EventEndsAt:      item.endsAt,
		AccountId:        accountId,
		Investigation: core.EventIncomingWebhookInvestigation{
			RuleName:    item.RuleName,
			RuleId:      item.RuleName,
			RuleType:    eventWebhookEventType,
			Fingerprint: fingerprint,
			Status:      status,
			Severity:    priority,
			SourceUrl:   item.Url,
			Labels:      labels,
		},
	}

	if item.Subject != nil && strings.TrimSpace(item.Subject.Name) != "" {
		webhookEvent.EventSubjectName = item.Subject.Name
		webhookEvent.EventSubjectKind = item.Subject.Type
		webhookEvent.EventSubjectNamespace = item.Subject.Namespace
		// The sender named the subject: keep it verbatim. Skipping the workload
		// match also skips the AI extractor, since the name stays non-empty.
		labels["nb_skip_workload_match"] = "true"
	}
	return webhookEvent
}
