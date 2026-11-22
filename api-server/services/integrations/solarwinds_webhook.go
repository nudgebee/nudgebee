package integrations

import (
	"fmt"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(SolarWindsWebhook{})
}

const IntegrationSolarWindsWebhook = "solarwinds_webhook"

type SolarWindsWebhook struct{}

func (m SolarWindsWebhook) Name() string {
	return IntegrationSolarWindsWebhook
}

func (m SolarWindsWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

// WebhookLabelHelp describes the labels the extractSolarWinds* functions build. Update it with the parser.
func (m SolarWindsWebhook) WebhookLabelHelp() core.WebhookLabelHelp {
	return core.WebhookLabelHelp{
		Summary: "A fixed set of fields from the alert.",
		Labels: []core.WebhookLabelSource{
			{Label: "severity", Source: "severity"},
			{Label: "priority", Source: "priority"},
			{Label: "alert_type", Source: "type"},
			{Label: "condition", Source: "condition"},
			{Label: "entity_type", Source: "the event's own entity .type"},
			{Label: "entity_id", Source: "the event's own entity .entityId"},
			{Label: "entity_names", Source: "the event's own entity .displayName"},
			// alert_definition_id is set too, but it is a UUID — nothing anyone would
			// route an account on, and the form only has room for the common labels.
			{Label: "host_name", Source: "events[0].hostName (log alerts)"},
		},
		Notes: []string{
			"An alert that fires on several entities at once becomes one event per entity, so each event's entity labels describe that entity alone.",
			"The alert name is the title, not a label.",
		},
	}
}

func (m SolarWindsWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of SolarWinds Webhook",
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

func (m SolarWindsWebhook) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m SolarWindsWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

// ---------------------------------------------------------------------------
// SolarWinds Observability webhook payload structs
// ---------------------------------------------------------------------------

// SolarWindsWebhookEntityValue is a single metric value reported by an entity.
type SolarWindsWebhookEntityValue struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

// SolarWindsWebhookEntity is a single K8s/infrastructure entity affected by the alert.
// The fields present depend on the payload type:
//   - entity-metric-raw: Values, ActiveAlertURL, Timestamp, TriggerId
//   - alert-cleared-entity-raw: ClearedAt, TriggeredAt, ActiveDurationInSeconds, Reason
type SolarWindsWebhookEntity struct {
	Type           string                         `json:"type"`
	DisplayName    string                         `json:"displayName"`
	EntityId       string                         `json:"entityId"`
	Values         []SolarWindsWebhookEntityValue `json:"values"`
	ActiveAlertURL string                         `json:"activeAlertUrl"`
	// alert-cleared-entity-raw fields
	ClearedAt               string `json:"clearedAt"`
	TriggeredAt             string `json:"triggeredAt"`
	ActiveDurationInSeconds int64  `json:"activeDurationInSeconds"`
	Reason                  string `json:"reason"`
	TriggerID               string `json:"triggerId"`
}

// SolarWindsWebhookMetric is a single metric value reported by a standalone-metric-raw alert.
type SolarWindsWebhookMetric struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

// SolarWindsAffectedEvaluation represents one evaluation instance in a standalone-metric-raw alert.
type SolarWindsAffectedEvaluation struct {
	Values    []SolarWindsWebhookEntityValue `json:"values"`
	Tags      interface{}                    `json:"tags"`
	TriggerID string                         `json:"triggerId"`
}

// SolarWindsLogEvent is a single log record included in a generic-event-raw alert.
type SolarWindsLogEvent struct {
	Message         string `json:"message"`
	Timestamp       string `json:"timestamp"`
	HostName        string `json:"hostName"`
	ApplicationName string `json:"applicationName"`
	EventURL        string `json:"eventUrl"`
	ID              string `json:"id"`
	SourceID        string `json:"sourceId"`
	Severity        string `json:"severity"`
}

// SolarWindsWebhookPayload is the payload SolarWinds Observability sends to a webhook.
// Supports entity-metric-raw, standalone-metric-raw, alert-cleared-entity-raw, and generic-event-raw.
type SolarWindsWebhookPayload struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Severity  string `json:"severity"`
	Priority  string `json:"priority"`
	DetailURL string `json:"detailUrl"`
	Condition string `json:"condition"`
	// entity-metric-raw / alert-cleared-entity-raw fields
	Entities []SolarWindsWebhookEntity `json:"entities"`
	// standalone-metric-raw fields
	Description         string                         `json:"description"`
	Runbook             *string                        `json:"runbook"`
	AlertDefinitionID   string                         `json:"alertDefinitionId"`
	Metrics             []SolarWindsWebhookMetric      `json:"metrics"`
	Timestamp           string                         `json:"timestamp"`
	AffectedEvaluations []SolarWindsAffectedEvaluation `json:"affectedEvaluations"`
	// generic-event-raw fields
	LogViewerURL  string               `json:"logViewerUrl"`
	RecordCount   int                  `json:"recordCount"`
	Threshold     float64              `json:"threshold"`
	TimespanStart string               `json:"timespanStart"`
	TimespanEnd   string               `json:"timespanEnd"`
	Query         string               `json:"query"`
	Events        []SolarWindsLogEvent `json:"events"`
	Frequency     string               `json:"frequency"`
	MinTimeAt     string               `json:"minTimeAt"`
	// shared
	TriggerID string `json:"triggerId"`
}

// ---------------------------------------------------------------------------
// ProcessEventWebook — main integration entry point
// ---------------------------------------------------------------------------

func (m SolarWindsWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	var payload SolarWindsWebhookPayload
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return nil, fmt.Errorf("solarwinds_webhook: failed to unmarshal payload: %w", err)
	}

	// Reject payloads that are not recognised SWO alert notifications. An empty/unrelated
	// JSON object would unmarshal successfully but produce a synthetic alert with no real
	// data. ErrEventNotSupported causes the framework to mark it processed silently.
	switch payload.Type {
	case "entity-metric-raw", "standalone-metric-raw", "alert-cleared-entity-raw", "generic-event-raw":
		// supported
	default:
		return nil, core.ErrEventNotSupported
	}
	if strings.TrimSpace(payload.Name) == "" && strings.TrimSpace(payload.TriggerID) == "" {
		return nil, core.ErrEventNotSupported
	}

	// Trim whitespace — SWO alert names can have trailing spaces.
	title := strings.TrimSpace(payload.Name)
	if title == "" {
		title = "SolarWinds Alert"
	}

	// Severity mapping — normalise to upper-case for case-insensitive comparison.
	// For cleared alerts SWO sends severity="CLEAR"; use priority for the real level.
	severityStr := strings.ToUpper(payload.Severity)
	if severityStr == "" || severityStr == "CLEAR" {
		severityStr = strings.ToUpper(payload.Priority)
	}
	priority := mapSolarWindsSeverity(severityStr)

	// Status: alert-cleared-entity-raw → resolved; all others → firing.
	isCleared := payload.Type == "alert-cleared-entity-raw"
	status := event.EventStatusFiring
	if isCleared {
		status = event.EventStatusResolved
	}

	// SWO's own description is the operator-authored text from the alert definition
	// (the same sentence its notification emails show). Prefer it over a sentence
	// rebuilt from the title and condition.
	description := strings.TrimSpace(payload.Description)
	if description == "" {
		description = solarWindsFallbackDescription(payload, title)
	}

	// assembleEvent turns one subject's worth of data into a webhook event, sharing
	// everything the payload decides once (title, status, severity, evidence).
	assembleEvent := func(parts solarWindsEventParts) core.EventIncomingWebhook {
		labels := parts.labels
		if labels == nil {
			labels = map[string]string{}
		}
		// Title-as-subject events must skip the central workload-match step. The
		// alert name is not a K8s workload name and ILIKE would produce false
		// positives. The label is consumed by core.MatchWorkloadAndEnrich.
		if !parts.hasEntitySubject {
			labels["nb_skip_workload_match"] = "true"
		}
		// The fingerprint is derived, not taken from the payload, so it is stable
		// across firings rather than unique per delivery. Registering an event_rules
		// row per fingerprint would therefore add a row named after a hash on top of
		// the title-named rule this parser already creates below.
		labels[event.LabelUnstableAggregationKey] = "true"

		return core.EventIncomingWebhook{
			// The per-instance trigger id belongs here, not in the fingerprint: it
			// identifies this delivery, which is exactly what WebhookId is for.
			WebhookId: parts.triggerID,
			// Transitional: events opened before this parser moved to a derived
			// fingerprint were stored under the raw trigger id, so a resolve has to
			// close those too or an alert that fired before the change and cleared
			// after it would stay open forever. The NUL-byte fallback the old code
			// used for a payload without a trigger id never persisted at all, so
			// there is nothing else to match. Remove once no open solarwinds_webhook
			// event carries a bare trigger-id fingerprint.
			LegacyFingerprint: parts.triggerID,
			EventType:         "solarwinds_alert",
			EventId:           parts.fingerprint,
			EventUrl:          parts.eventURL,
			EventStatus:       string(status),
			EventPriority:     string(priority),
			EventTitle:        title,
			EventDescription:  description,
			Investigation: core.EventIncomingWebhookInvestigation{
				RuleName:    title,
				RuleId:      parts.fingerprint,
				Fingerprint: parts.fingerprint,
				Status:      event.EventStatus(status),
				Severity:    priority,
				SourceUrl:   parts.eventURL,
				Labels:      labels,
				Evidences:   []event.EventEvidence{buildSolarWindsAlertEvidence(payload, severityStr, parts.triggerID)},
			},
			EventSubjectName:  parts.subjectName,
			EventSubjectKind:  parts.subjectKind,
			PodNameCandidates: parts.podCandidates,
			AccountId:         accountId,
			EventCreatedAt:    parts.createdAt, // zero → framework substitutes time.Now()
			EventEndsAt:       parts.endsAt,    // non-zero only for cleared events
		}
	}

	var webhookEvents []core.EventIncomingWebhook

	switch payload.Type {
	case "standalone-metric-raw":
		// No entity subjects; use the title as a display name only.
		webhookEvents = append(webhookEvents, assembleEvent(solarWindsEventParts{
			fingerprint: solarWindsFingerprint(payload, solarWindsConditionKey(payload)),
			triggerID:   payload.TriggerID,
			eventURL:    payload.DetailURL,
			subjectName: title,
			labels:      extractSolarWindsStandaloneMetricInfo(payload),
		}))
	case "generic-event-raw":
		// Log-based — no entity subject. Use applicationName from the first log
		// event when available; it helps correlate with a K8s workload.
		parts := solarWindsEventParts{
			fingerprint: solarWindsFingerprint(payload, solarWindsConditionKey(payload)),
			triggerID:   payload.TriggerID,
			eventURL:    payload.DetailURL,
			subjectName: title,
			labels:      extractSolarWindsGenericEventInfo(payload),
		}
		if len(payload.Events) > 0 && payload.Events[0].ApplicationName != "" {
			parts.subjectName = payload.Events[0].ApplicationName
			parts.subjectKind = "deployment"
			parts.hasEntitySubject = true
		}
		webhookEvents = append(webhookEvents, assembleEvent(parts))
	default:
		// entity-metric-raw and alert-cleared-entity-raw both carry an entities array.
		// One event per entity: SWO reports several breaching entities in a single
		// firing delivery but clears them one delivery at a time, so a single event
		// could only ever name one of them and the rest would be invisible.
		for index, entity := range payload.Entities {
			webhookEvents = append(webhookEvents, assembleEvent(solarWindsEntityParts(payload, entity, title, isCleared, index)))
		}
		if len(webhookEvents) == 0 {
			// Well-formed but entity-less. Use the alert title as the display subject,
			// never as a workload-lookup candidate — the alert name is not a K8s
			// workload name and an ILIKE match would produce false positives.
			webhookEvents = append(webhookEvents, assembleEvent(solarWindsEventParts{
				fingerprint: solarWindsFingerprint(payload, solarWindsConditionKey(payload)),
				triggerID:   payload.TriggerID,
				eventURL:    payload.DetailURL,
				subjectName: title,
				labels:      extractSolarWindsBaseLabels(payload),
			}))
		}
	}

	// Upsert an event rule so this alert is visible in the rule management UI,
	// consistent with the Datadog webhook flow. Run async to avoid blocking the response.
	// Skip for cleared events — the rule was already created when the alert first fired.
	if !isCleared {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					sc.GetLogger().Error("solarwinds_webhook: panic in CreateEventRule goroutine", "panic", r)
				}
			}()
			eventReq := buildSolarWindsEventConfig(payload, title, description, severityStr, accountId)
			if _, err := eventrule.CreateEventRule(sc, eventReq); err != nil {
				sc.GetLogger().Error("solarwinds_webhook: CreateEventRule failed", "error", err)
			}
		}()
	}

	return webhookEvents, nil
}

// buildSolarWindsEventConfig assembles the event_rules upsert for one delivery, so
// the alert's name is selectable in the rule-management UI. Pure, so the shape can
// be asserted without running the goroutine that sends it.
func buildSolarWindsEventConfig(payload SolarWindsWebhookPayload, title, description, severityStr, accountId string) eventrule.EventConfig {
	severityLabel := strings.ToLower(severityStr)
	if severityLabel == "" {
		severityLabel = "warning"
	}
	runbook := ""
	if payload.Runbook != nil {
		runbook = *payload.Runbook
	}
	expr := solarWindsConditionKey(payload)

	return eventrule.EventConfig{
		Annotations: struct {
			Description string `json:"description"`
			Summary     string `json:"summary"`
			Runbook     string `json:"runbook"`
		}{
			Description: description,
			Summary:     title,
			Runbook:     runbook,
		},
		Expr: expr,
		Labels: struct {
			Severity string `json:"severity"`
		}{Severity: severityLabel},
		Alert:     title,
		Duration:  "0",
		AccountID: accountId,
		Source:    IntegrationSolarWindsWebhook,
		Category:  "alert",
		Severity:  severityLabel,
		Enabled:   true,
		// The alert's own id in SolarWinds, which every delivery carries on both the
		// firing and the cleared payload. Recording it is the only way a SWO rule is
		// ever identified: SolarWinds has no API that lists alerts, so an alert
		// Nudgebee has not seen fire cannot be looked up at all.
		ExternalRuleID: payload.AlertDefinitionID,
		TriggerParams:  []map[string]any{},
		ActionParams:   []map[string]any{},
	}
}

// solarWindsEventParts is the per-subject data the payload families compute
// differently before ProcessEventWebook's shared assembly step.
type solarWindsEventParts struct {
	fingerprint      string
	triggerID        string
	eventURL         string
	subjectName      string
	subjectKind      string
	hasEntitySubject bool
	podCandidates    []string
	labels           map[string]string
	createdAt        time.Time
	endsAt           time.Time
}

// solarWindsConditionKey is the payload's condition, or the log query for the
// log-based type that has no condition.
func solarWindsConditionKey(payload SolarWindsWebhookPayload) string {
	if payload.Condition != "" {
		return payload.Condition
	}
	return payload.Query
}

// solarWindsFingerprint builds the stable identity of one alerting subject.
//
// It deliberately does NOT use triggerId. That id is per alert *instance*, so
// keying on it puts every firing of the same recurring alert in its own
// occurrence chain and fabricates correlated incidents — the case
// core.CanonicalFingerprint was written for. It is also absent from the
// top-level payload whenever an alert fires on more than one entity, which used
// to send this parser down a fallback that embedded a NUL byte; Postgres
// rejects NUL in text, so the event silently failed to persist and its webhook
// row sat at processing_status='processing' forever.
//
// alertDefinitionId is the SWO alert's own UUID and is present on both the
// firing and the cleared delivery, so a clear resolves the event its firing
// opened. The trimmed name is only a fallback for a payload without one.
// The name is trimmed because SWO sends names with trailing spaces: an untrimmed
// key would split one alert's occurrence chain on the whitespace alone.
//
// A payload carrying neither an id nor a name is still accepted when it has a
// trigger id, so the chain continues through the condition and finally the type,
// which the switch above guarantees is non-empty. Without that, two unnamed
// alerts on one entity would both key on "" and merge into a single chain.
func solarWindsFingerprint(payload SolarWindsWebhookPayload, subjectKey string) string {
	definitionKey := payload.AlertDefinitionID
	if definitionKey == "" {
		definitionKey = strings.TrimSpace(payload.Name)
	}
	if definitionKey == "" {
		definitionKey = solarWindsConditionKey(payload)
	}
	if definitionKey == "" {
		definitionKey = payload.Type
	}
	return core.CanonicalFingerprint(IntegrationSolarWindsWebhook, definitionKey, subjectKey)
}

// solarWindsFallbackDescription rebuilds a description for a payload that
// carries none of its own.
func solarWindsFallbackDescription(payload SolarWindsWebhookPayload, title string) string {
	kind, detail := "alert", payload.Condition
	if payload.Type == "generic-event-raw" {
		kind, detail = "log alert", payload.Query
	}
	if detail == "" {
		return fmt.Sprintf("SolarWinds %s '%s'", kind, title)
	}
	return fmt.Sprintf("SolarWinds %s '%s': %s", kind, title, detail)
}

// solarWindsEntityParts builds the per-event data for one entity of an
// entity-metric-raw or alert-cleared-entity-raw payload.
func solarWindsEntityParts(payload SolarWindsWebhookPayload, entity SolarWindsWebhookEntity, title string, isCleared bool, index int) solarWindsEventParts {
	// Prefer the entity id for identity — a display name can be reused across
	// namespaces, and SWO sends the id on both the firing and the cleared delivery.
	subjectKey := entity.EntityId
	if subjectKey == "" {
		subjectKey = entity.DisplayName
	}
	if subjectKey == "" {
		// An entity with neither an id nor a name has no stable identity, so no
		// stable fingerprint exists for it. Key on its position so that two such
		// entities in one delivery do not collapse into a single occurrence chain;
		// the trade-off is a key that moves if SWO ever reorders them.
		subjectKey = fmt.Sprintf("#%d", index)
	}

	// The per-entity trigger id is the one SWO always sends; the top-level one is
	// absent on multi-entity deliveries.
	triggerID := entity.TriggerID
	if triggerID == "" {
		triggerID = payload.TriggerID
	}

	eventURL := entity.ActiveAlertURL
	if eventURL == "" {
		eventURL = payload.DetailURL
	}

	parts := solarWindsEventParts{
		fingerprint: solarWindsFingerprint(payload, subjectKey),
		triggerID:   triggerID,
		eventURL:    eventURL,
		subjectName: entity.DisplayName,
		subjectKind: mapSolarWindsEntityKind(entity.Type),
		// Only a named entity is a safe workload-lookup candidate.
		hasEntitySubject: entity.DisplayName != "",
		labels:           extractSolarWindsEntityLabels(payload, entity),
	}

	// A pod subject needs resolving to its owning workload: k8s_workloads stores
	// owners, never pod instances, so the raw pod name never matches and the event
	// would keep no namespace, cloud_resource_id or service_key.
	if parts.subjectKind == "pod" && entity.DisplayName != "" {
		parts.podCandidates = []string{entity.DisplayName}
	}

	if parts.subjectName == "" {
		// An entity with a Type but no DisplayName would otherwise leave a stale
		// kind (e.g. "pod") paired with the alert title as the subject name.
		parts.subjectName = title
		parts.subjectKind = ""
	}

	if isCleared {
		if entity.TriggeredAt != "" {
			if t, err := time.Parse(time.RFC3339, entity.TriggeredAt); err == nil {
				parts.createdAt = t
			}
		}
		if entity.ClearedAt != "" {
			if t, err := time.Parse(time.RFC3339, entity.ClearedAt); err == nil {
				parts.endsAt = t
			}
		}
	}

	return parts
}

// extractSolarWindsBaseLabels builds the labels every entity-bearing payload
// shares, independent of which entity the event is about.
func extractSolarWindsBaseLabels(payload SolarWindsWebhookPayload) map[string]string {
	labels := make(map[string]string)
	labels["severity"] = payload.Severity
	labels["priority"] = payload.Priority
	if payload.Condition != "" {
		labels["condition"] = payload.Condition
	}
	if payload.Type != "" {
		labels["alert_type"] = payload.Type
	}
	if payload.AlertDefinitionID != "" {
		labels["alert_definition_id"] = payload.AlertDefinitionID
	}
	return labels
}

// extractSolarWindsEntityLabels builds the labels for the event about one entity.
func extractSolarWindsEntityLabels(payload SolarWindsWebhookPayload, entity SolarWindsWebhookEntity) map[string]string {
	labels := extractSolarWindsBaseLabels(payload)
	labels["entity_type"] = entity.Type
	if entity.EntityId != "" {
		labels["entity_id"] = entity.EntityId
	}
	applyEntityLabels([]SolarWindsWebhookEntity{entity}, labels)
	return labels
}

// extractSolarWindsStandaloneMetricInfo builds the labels map for a standalone-metric-raw alert.
// These payloads have no entities array; metric values are in the top-level metrics field.
func extractSolarWindsStandaloneMetricInfo(payload SolarWindsWebhookPayload) map[string]string {
	labels := make(map[string]string)
	labels["severity"] = payload.Severity
	labels["priority"] = payload.Priority
	if payload.Condition != "" {
		labels["condition"] = payload.Condition
	}
	if payload.AlertDefinitionID != "" {
		labels["alert_definition_id"] = payload.AlertDefinitionID
	}
	labels["alert_type"] = payload.Type
	for _, m := range payload.Metrics {
		if m.Name == "" {
			continue
		}
		labels["metric_"+m.Name] = fmt.Sprintf("%g", m.Value)
		labels["metric_"+m.Name+"_threshold"] = fmt.Sprintf("%g", m.Threshold)
	}
	return labels
}

// extractSolarWindsGenericEventInfo builds the labels map for a generic-event-raw (log) alert.
// These payloads carry a log query, record count, and a sample of matching log events.
func extractSolarWindsGenericEventInfo(payload SolarWindsWebhookPayload) map[string]string {
	labels := make(map[string]string)
	labels["severity"] = payload.Severity
	labels["priority"] = payload.Priority
	if payload.Query != "" {
		labels["log_query"] = payload.Query
	}
	if payload.AlertDefinitionID != "" {
		labels["alert_definition_id"] = payload.AlertDefinitionID
	}
	labels["alert_type"] = payload.Type
	if payload.RecordCount > 0 {
		labels["record_count"] = fmt.Sprintf("%d", payload.RecordCount)
	}
	if payload.Frequency != "" {
		labels["frequency"] = payload.Frequency
	}
	if len(payload.Events) > 0 {
		first := payload.Events[0]
		if first.ApplicationName != "" {
			labels["application_name"] = first.ApplicationName
		}
		if first.HostName != "" {
			labels["host_name"] = first.HostName
		}
	}
	return labels
}

// applyEntityLabels adds entity_names and metric_<entity>_<metric> labels for the
// given entities. Since an alert produces one event per entity this is called with
// a single entity; the DisplayName stays in the metric key so the label names an
// account mapping was built on do not change.
func applyEntityLabels(entities []SolarWindsWebhookEntity, labels map[string]string) {
	var entityNames []string
	for _, e := range entities {
		if e.DisplayName != "" {
			entityNames = append(entityNames, e.DisplayName)
		}
		for _, v := range e.Values {
			if v.Name == "" {
				continue
			}
			key := "metric_" + v.Name
			if e.DisplayName != "" {
				key = "metric_" + e.DisplayName + "_" + v.Name
			}
			labels[key] = fmt.Sprintf("%g", v.Value)
		}
	}
	if len(entityNames) > 0 {
		labels["entity_names"] = strings.Join(entityNames, ",")
	}
}

// buildSolarWindsAlertEvidence constructs the structured evidence block for a SWO
// alert. triggerID is the id of the entity this event is about — the top-level one
// is absent whenever an alert fires on more than one entity.
func buildSolarWindsAlertEvidence(payload SolarWindsWebhookPayload, severityStr, triggerID string) event.EventEvidence {
	conditionInsight := payload.Condition
	if conditionInsight == "" {
		conditionInsight = "(no condition)"
	}
	return event.EventEvidence{
		Type: "json",
		Data: map[string]any{
			"name": "SolarWinds Alert Details",
			"data": payload,
		},
		Insight: []event.EventEvidenceInsight{
			{
				Message:  fmt.Sprintf("TriggerID: %s, Condition: %s", triggerID, conditionInsight),
				Severity: strings.ToLower(severityStr),
			},
		},
		AdditionalInfo: map[string]any{
			"action_name":            "solarwinds_alert_details",
			"actual_action_name":     "solarwinds_alert_details",
			"action_title":           "SolarWinds Alert Details",
			"conditional_expression": "",
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mapSolarWindsSeverity converts an upper-cased SWO severity/priority string to
// an internal EventPriority. Unknown values default to Medium (not Low) because
// SWO only fires alerts for meaningful threshold breaches.
func mapSolarWindsSeverity(severity string) event.EventPriority {
	switch severity {
	case "CRITICAL":
		return event.EventPriorityHigh
	case "HIGH":
		return event.EventPriorityHigh
	case "WARNING", "MEDIUM":
		return event.EventPriorityMedium
	case "LOW":
		return event.EventPriorityLow
	case "INFO":
		return event.EventPriorityInfo
	default:
		return event.EventPriorityMedium
	}
}

// mapSolarWindsEntityKind maps a SolarWinds entity type to a lower-case K8s kind string.
// Entity types are verified against the SolarWinds Observability REST API (/v1/entities).
func mapSolarWindsEntityKind(entityType string) string {
	switch entityType {
	case "KubernetesCluster":
		return "cluster"
	case "KubernetesDeployment":
		return "deployment"
	case "KubernetesStatefulSet":
		return "statefulset"
	case "KubernetesReplicaSet":
		return "replicaset"
	case "KubernetesJob":
		return "job"
	case "KubernetesPod", "KubernetesPodInstance":
		return "pod"
	case "KubernetesNode":
		return "node"
	case "KubernetesContainer":
		return "container"
	default:
		return strings.ToLower(entityType)
	}
}
