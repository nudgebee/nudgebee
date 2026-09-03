package integrations

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(IncidentIOWebhook{})
}

const IntegrationIncidentIOWebhook = "incidentio_webhook"

// incident.io webhook event types this integration models. Names verified
// against the published OpenAPI spec (docs.incident.io/openapi/webhooks.json),
// which declares 34 event types in total.
//
// Only incidents are ingested. incident.io alerts (public_alert.alert_created_v1
// and friends) are relays of alerts that originate in Prometheus / Grafana /
// Datadog — all of which NudgeBee already ingests directly from the source.
// Subscribing to incident.io's copy as well would produce two events for one
// condition, with different fingerprints (different ID space), so nothing would
// dedupe them. Everything not listed here returns core.ErrEventNotSupported and
// records as `skipped` rather than `failed`.
const (
	incidentIOEventIncidentCreated       = "public_incident.incident_created_v2"
	incidentIOEventIncidentUpdated       = "public_incident.incident_updated_v2"
	incidentIOEventIncidentStatusUpdated = "public_incident.incident_status_updated_v2"
)

// incident.io status categories. All categories except `live` and `learning` are
// managed by incident.io and cannot be renamed or reconfigured; the display names
// of those two are tenant-configurable but the category values are not.
const (
	incidentIOCategoryTriage   = "triage"
	incidentIOCategoryLive     = "live"
	incidentIOCategoryPaused   = "paused"
	incidentIOCategoryLearning = "learning"
	incidentIOCategoryClosed   = "closed"
	incidentIOCategoryDeclined = "declined"
	incidentIOCategoryMerged   = "merged"
	incidentIOCategoryCanceled = "canceled"
)

// IncidentIOStatus is an incident.io incident status. Statuses are
// tenant-configurable, so Category — not Name — is the stable thing to map on.
type IncidentIOStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Rank     int    `json:"rank"`
}

// IncidentIOSeverity is an incident.io severity. Both the names and how many
// exist are tenant-configurable (the defaults are Minor / Major / Critical),
// so mapping is by name first and Rank only as a fallback.
type IncidentIOSeverity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Rank int    `json:"rank"`
}

// IncidentIONamedRef covers the several incident.io sub-objects NudgeBee only
// reads an id and a name from (incident_type, custom_field, role).
type IncidentIONamedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// IncidentIOUser is an incident.io user reference (role assignees).
type IncidentIOUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// IncidentIORoleAssignment binds a user to an incident role (lead, comms, ...).
type IncidentIORoleAssignment struct {
	Role     IncidentIORole  `json:"role"`
	Assignee *IncidentIOUser `json:"assignee,omitempty"`
}

// IncidentIORole is an incident role definition. ShortForm is the compact handle
// ("lead") and is preferred over Name ("Incident Lead") as a label key.
type IncidentIORole struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ShortForm string `json:"short_form"`
}

// IncidentIOCustomFieldOption is one option of a select-typed custom field.
type IncidentIOCustomFieldOption struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// IncidentIOCatalogEntry is a catalog-typed custom field value.
type IncidentIOCatalogEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// IncidentIOCustomFieldValue is one value of a custom field entry. Exactly one
// of the value_* fields is populated, depending on the field's type.
type IncidentIOCustomFieldValue struct {
	ValueText         string                       `json:"value_text,omitempty"`
	ValueLink         string                       `json:"value_link,omitempty"`
	ValueNumeric      string                       `json:"value_numeric,omitempty"`
	ValueOption       *IncidentIOCustomFieldOption `json:"value_option,omitempty"`
	ValueCatalogEntry *IncidentIOCatalogEntry      `json:"value_catalog_entry,omitempty"`
}

// IncidentIOCustomFieldEntry is one custom field and its values on an incident.
type IncidentIOCustomFieldEntry struct {
	CustomField IncidentIONamedRef           `json:"custom_field"`
	Values      []IncidentIOCustomFieldValue `json:"values"`
}

// IncidentIOIncident is the incident object embedded in every incident webhook.
type IncidentIOIncident struct {
	ID                      string                       `json:"id"`
	Reference               string                       `json:"reference"`
	Name                    string                       `json:"name"`
	Summary                 string                       `json:"summary"`
	Permalink               string                       `json:"permalink"`
	Visibility              string                       `json:"visibility"`
	Mode                    string                       `json:"mode"`
	CreatedAt               string                       `json:"created_at"`
	UpdatedAt               string                       `json:"updated_at"`
	MostRecentUpdateMessage string                       `json:"most_recent_update_message"`
	SlackChannelURL         string                       `json:"slack_channel_url"`
	SlackChannelName        string                       `json:"slack_channel_name"`
	Severity                *IncidentIOSeverity          `json:"severity,omitempty"`
	IncidentStatus          *IncidentIOStatus            `json:"incident_status,omitempty"`
	IncidentType            *IncidentIONamedRef          `json:"incident_type,omitempty"`
	CustomFieldEntries      []IncidentIOCustomFieldEntry `json:"custom_field_entries,omitempty"`
	IncidentRoleAssignments []IncidentIORoleAssignment   `json:"incident_role_assignments,omitempty"`
}

// IncidentIOStatusUpdatedPayload is the body of public_incident.incident_status_updated_v2.
// Unlike the created/updated events — whose body IS the incident — this one wraps
// the incident alongside the transition that triggered it.
type IncidentIOStatusUpdatedPayload struct {
	Incident       IncidentIOIncident `json:"incident"`
	Message        string             `json:"message"`
	NewStatus      *IncidentIOStatus  `json:"new_status,omitempty"`
	PreviousStatus *IncidentIOStatus  `json:"previous_status,omitempty"`
}

type IncidentIOWebhook struct{}

func (w IncidentIOWebhook) Name() string {
	return IntegrationIncidentIOWebhook
}

func (w IncidentIOWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (w IncidentIOWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of incident.io Webhook",
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

func (w IncidentIOWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (w IncidentIOWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// ProcessEventWebook parses an incident.io incident webhook into a NudgeBee event.
//
// incident.io keys the payload by the event type string itself, so the body is
//
//	{"event_type": "public_incident.incident_created_v2",
//	 "public_incident.incident_created_v2": { ...incident... }}
//
// which means the event type has to be read before the body can be unmarshalled
// into a concrete type — hence the two-pass parse.
func (w IncidentIOWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var envelope map[string]json.RawMessage
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &envelope); err != nil {
		return nil, fmt.Errorf("incidentio_webhook: failed to unmarshal payload: %w", err)
	}

	rawEventType, ok := envelope["event_type"]
	if !ok {
		return nil, fmt.Errorf("incidentio_webhook: invalid payload, event_type not found")
	}
	var eventType string
	if err := common.UnmarshalJson(rawEventType, &eventType); err != nil {
		return nil, fmt.Errorf("incidentio_webhook: failed to unmarshal event_type: %w", err)
	}

	switch eventType {
	case incidentIOEventIncidentCreated, incidentIOEventIncidentUpdated, incidentIOEventIncidentStatusUpdated:
	default:
		// Alerts, escalations, actions, follow-ups, schedules and status pages all
		// arrive on the same subscription. They are not failures, just events this
		// integration does not model.
		return nil, core.ErrEventNotSupported
	}

	body, ok := envelope[eventType]
	if !ok {
		return nil, fmt.Errorf("incidentio_webhook: invalid payload, body %q not found", eventType)
	}

	var incident IncidentIOIncident
	var newStatus, previousStatus *IncidentIOStatus

	if eventType == incidentIOEventIncidentStatusUpdated {
		var payload IncidentIOStatusUpdatedPayload
		if err := common.UnmarshalJson(body, &payload); err != nil {
			return nil, fmt.Errorf("incidentio_webhook: failed to unmarshal status update body: %w", err)
		}
		incident = payload.Incident
		newStatus = payload.NewStatus
		previousStatus = payload.PreviousStatus
	} else {
		if err := common.UnmarshalJson(body, &incident); err != nil {
			return nil, fmt.Errorf("incidentio_webhook: failed to unmarshal incident body: %w", err)
		}
	}

	if incident.ID == "" {
		return nil, fmt.Errorf("incidentio_webhook: invalid payload, incident id not found")
	}

	// Resolve the status this delivery reports. incident_status_updated_v2 carries
	// the transition explicitly; the other two only carry the incident's current
	// status.
	status := newStatus
	if status == nil {
		status = incident.IncidentStatus
	}
	category := ""
	if status != nil {
		category = strings.ToLower(strings.TrimSpace(status.Category))
	}
	eventStatus := incidentIOEventStatusForCategory(category)

	// Storm control. incident.io fires incident_updated_v2 on EVERY field change —
	// a summary edit, a custom field, a role assignment, a Slack channel rename —
	// and an actively managed incident easily produces dozens per hour. Re-emitting
	// a firing event for each would re-enter investigation (and LLM analysis) over
	// and over for one incident.
	//
	// So updated_v2 is treated as a resolution-only safety net: it is emitted only
	// when the incident has reached a terminal category, which covers the case
	// where a status_updated_v2 delivery was lost and the incident would otherwise
	// stay firing in NudgeBee forever. Every non-terminal edit is skipped.
	//
	// status_updated_v2 is likewise skipped when the transition does not change how
	// NudgeBee sees the incident — Investigating -> Fixing are both `live`, so they
	// both mean "still firing" and there is nothing to report.
	switch eventType {
	case incidentIOEventIncidentUpdated:
		if eventStatus != event.EventStatusResolved {
			return nil, core.ErrEventNotSupported
		}
	case incidentIOEventIncidentStatusUpdated:
		if previousStatus != nil {
			prevCategory := strings.ToLower(strings.TrimSpace(previousStatus.Category))
			if incidentIOEventStatusForCategory(prevCategory) == eventStatus {
				return nil, core.ErrEventNotSupported
			}
		}
	}

	severity := incidentIOEventPriority(incident.Severity)

	eventURL := incident.Permalink
	if eventURL == "" {
		eventURL = incident.SlackChannelURL
	}

	createdAt := parseIncidentIOTime(incident.CreatedAt)
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	title := incident.Name
	if title == "" {
		title = incident.Reference
	}

	description := incident.Summary
	if description == "" {
		description = incident.MostRecentUpdateMessage
	}

	labels := incidentIOLabels(incident, status)

	// incident.io incidents raised from an alert route usually carry the upstream
	// Alertmanager-style firing text in the summary, which is where the real
	// alertname / namespace / pod / severity labels live. parseFiringLabels is
	// shared with PagerDuty and ZenDuty (defined in pagerduty_webhook.go, same
	// package). Merged non-clobbering so structural incident.io metadata always
	// wins over anything scraped out of free text.
	for k, v := range parseFiringLabels(incident.Summary) {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName: title,
		RuleType: "incidentio_incident",
		RuleId:   incident.ID,
		// The incident id is stable for the whole lifecycle, so the firing event
		// and the later resolved event share a fingerprint and chain onto the same
		// occurrence. The human reference (INC-123) is deliberately not part of it:
		// it is display-only and not guaranteed stable across a merge.
		Fingerprint: core.CanonicalFingerprint("incidentio", incident.ID),
		Status:      eventStatus,
		Severity:    severity,
		SourceUrl:   eventURL,
		Labels:      labels,
	}

	webhookEvent := core.EventIncomingWebhook{
		WebhookId:        fmt.Sprintf("incidentio-%s-%d", incident.ID, time.Now().Unix()),
		EventType:        "incident",
		EventId:          incident.ID,
		EventUrl:         eventURL,
		EventStatus:      string(eventStatus),
		EventPriority:    string(severity),
		EventCreatedAt:   createdAt,
		EventTitle:       title,
		EventDescription: description,
		EventTags:        []string{},
		Investigation:    investigation,
		AccountId:        accountId,
	}

	// Only assign a subject when one was actually found — an empty assignment
	// clobbers the later label-based resolution in resolveSubjectFromLabels.
	if ns := labels["namespace"]; ns != "" {
		webhookEvent.EventSubjectNamespace = ns
	}
	if pod := labels["pod"]; pod != "" {
		webhookEvent.EventSubjectName = pod
	} else if svc := labels["service_name"]; svc != "" {
		webhookEvent.EventSubjectName = svc
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}

// incidentIOEventStatusForCategory maps an incident.io status category onto a
// NudgeBee event status.
//
// `learning` (the post-incident / debrief phase) counts as resolved: impact is
// mitigated by the time an incident enters it, and the remaining work is the
// review. `paused` counts as firing — work has stopped but impact has not been
// declared over. An unrecognised or absent category is treated as firing so a
// new incident.io category can never silently swallow a live incident.
func incidentIOEventStatusForCategory(category string) event.EventStatus {
	switch category {
	case incidentIOCategoryClosed, incidentIOCategoryLearning,
		incidentIOCategoryDeclined, incidentIOCategoryMerged, incidentIOCategoryCanceled:
		return event.EventStatusResolved
	case incidentIOCategoryTriage, incidentIOCategoryLive, incidentIOCategoryPaused:
		return event.EventStatusFiring
	default:
		return event.EventStatusFiring
	}
}

// incidentIOEventPriority maps an incident.io severity onto a NudgeBee priority.
//
// Severity names and how many exist are both tenant-configurable (the shipped
// defaults are Minor / Major / Critical), so the name is matched first and Rank
// is only a fallback for tenants who renamed them to something unrecognised.
// Rank is relative within a tenant, not absolute, which is exactly why it is the
// fallback and not the primary signal.
func incidentIOEventPriority(severity *IncidentIOSeverity) event.EventPriority {
	if severity == nil {
		// An incident with no severity set is still a declared incident.
		return event.EventPriorityMedium
	}

	switch strings.ToLower(strings.TrimSpace(severity.Name)) {
	case "critical", "crit", "sev0", "sev1", "p0", "p1", "urgent", "emergency":
		return event.EventPriorityHigh
	case "major", "high", "sev2", "p2":
		return event.EventPriorityHigh
	case "moderate", "medium", "sev3", "p3":
		return event.EventPriorityMedium
	case "minor", "low", "sev4", "p4":
		return event.EventPriorityLow
	case "trivial", "info", "informational", "none", "sev5", "p5":
		return event.EventPriorityInfo
	}

	switch {
	case severity.Rank >= 3:
		return event.EventPriorityHigh
	case severity.Rank == 2:
		return event.EventPriorityMedium
	case severity.Rank == 1:
		return event.EventPriorityLow
	default:
		return event.EventPriorityMedium
	}
}

// incidentIOLabels builds the investigation labels for an incident.
//
// incident.io's own structural metadata is written under an `nb_incidentio_`
// prefix so it stays available for evidence and audit without participating in
// subject resolution (event/service.go walks bare label keys such as
// service_name and namespace when looking for a subject). Custom fields are
// deliberately NOT prefixed: they are operator-defined, so a tenant with a
// `namespace` or `cluster` custom field means it to resolve.
func incidentIOLabels(incident IncidentIOIncident, status *IncidentIOStatus) map[string]string {
	labels := map[string]string{}

	if incident.Reference != "" {
		labels["nb_incidentio_reference"] = incident.Reference
	}
	if status != nil {
		if status.Name != "" {
			labels["nb_incidentio_status"] = status.Name
		}
		if status.Category != "" {
			labels["nb_incidentio_status_category"] = status.Category
		}
	}
	if incident.Severity != nil && incident.Severity.Name != "" {
		labels["nb_incidentio_severity"] = incident.Severity.Name
	}
	if incident.IncidentType != nil && incident.IncidentType.Name != "" {
		labels["nb_incidentio_incident_type"] = incident.IncidentType.Name
	}
	if incident.Visibility != "" {
		labels["nb_incidentio_visibility"] = incident.Visibility
	}
	if incident.Mode != "" {
		labels["nb_incidentio_mode"] = incident.Mode
	}
	if incident.SlackChannelName != "" {
		labels["nb_incidentio_slack_channel"] = incident.SlackChannelName
	}

	for _, assignment := range incident.IncidentRoleAssignments {
		if assignment.Assignee == nil || assignment.Assignee.Name == "" {
			continue
		}
		key := assignment.Role.ShortForm
		if key == "" {
			key = assignment.Role.Name
		}
		if key == "" {
			continue
		}
		labels["nb_incidentio_role_"+incidentIOLabelKey(key)] = assignment.Assignee.Name
	}

	// Custom fields are written last but must not clobber anything already in the
	// map. Field names are operator-controlled, so a field literally named "Nb
	// Incidentio Reference" normalises onto a key written above and would silently
	// replace the audit-trail metadata this function promises to preserve. First
	// writer wins, which also makes two custom fields normalising to the same key
	// deterministic rather than map-order dependent.
	for _, entry := range incident.CustomFieldEntries {
		key := incidentIOLabelKey(entry.CustomField.Name)
		if key == "" {
			continue
		}
		if _, exists := labels[key]; exists {
			continue
		}
		if value := incidentIOCustomFieldValue(entry.Values); value != "" {
			labels[key] = value
		}
	}

	return labels
}

// incidentIOCustomFieldValue renders a custom field's values as a single label
// value, joining multi-select fields with a comma.
func incidentIOCustomFieldValue(values []IncidentIOCustomFieldValue) string {
	rendered := make([]string, 0, len(values))
	for _, v := range values {
		switch {
		case v.ValueOption != nil && v.ValueOption.Value != "":
			rendered = append(rendered, v.ValueOption.Value)
		case v.ValueCatalogEntry != nil && v.ValueCatalogEntry.Name != "":
			rendered = append(rendered, v.ValueCatalogEntry.Name)
		case v.ValueText != "":
			rendered = append(rendered, v.ValueText)
		case v.ValueLink != "":
			rendered = append(rendered, v.ValueLink)
		case v.ValueNumeric != "":
			rendered = append(rendered, v.ValueNumeric)
		}
	}
	return strings.Join(rendered, ",")
}

// incidentIOLabelKey normalises a human-facing incident.io name ("Affected
// Team") into a label key ("affected_team").
func incidentIOLabelKey(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}

// parseIncidentIOTime parses an incident.io timestamp. The API documents
// RFC3339 with fractional seconds; the plain-second form is accepted too so a
// truncated timestamp does not cost us the event's real creation time.
func parseIncidentIOTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}
