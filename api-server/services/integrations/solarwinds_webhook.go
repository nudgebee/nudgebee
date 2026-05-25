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

func (m SolarWindsWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of SolarWinds Webhook",
				Default:     "",
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			"token": {
				Type:    core.ToolSchemaTypeString,
				Default: "",
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

	// Fingerprint: prefer triggerId (unique per alert instance); fall back to a
	// deterministic key so we never hard-fail on a well-formed but ID-less payload.
	// generic-event-raw alerts use query as the condition equivalent.
	fingerprint := payload.TriggerID
	if fingerprint == "" {
		conditionKey := payload.Condition
		if conditionKey == "" {
			conditionKey = payload.Query
		}
		fingerprint = title + "\x00" + conditionKey
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

	var subjectName, subjectKind string
	var hasEntitySubject bool
	var labels map[string]string
	var description string
	var eventCreatedAt, eventEndsAt time.Time

	switch payload.Type {
	case "standalone-metric-raw":
		labels = extractSolarWindsStandaloneMetricInfo(payload)
		// standalone-metric-raw has no entity subjects; use title as display name only.
		subjectName = title
		description = payload.Description
		if description == "" {
			description = fmt.Sprintf("SolarWinds alert '%s'", title)
			if payload.Condition != "" {
				description = fmt.Sprintf("SolarWinds alert '%s': %s", title, payload.Condition)
			}
		}
	case "generic-event-raw":
		labels = extractSolarWindsGenericEventInfo(payload)
		// generic-event-raw is log-based — no entity subject, use applicationName from first
		// log event as subject when available (helps correlate with a K8s workload).
		subjectName = title
		if len(payload.Events) > 0 && payload.Events[0].ApplicationName != "" {
			subjectName = payload.Events[0].ApplicationName
			subjectKind = "deployment"
			hasEntitySubject = true
		}
		description = fmt.Sprintf("SolarWinds log alert '%s'", title)
		if payload.Query != "" {
			description = fmt.Sprintf("SolarWinds log alert '%s': %s", title, payload.Query)
		}
	default:
		// entity-metric-raw and alert-cleared-entity-raw both carry an entities array.
		subjectName, subjectKind, hasEntitySubject, labels = extractSolarWindsEntityInfo(payload)
		if subjectName == "" {
			// Use alert title as display subject only when no entity name is available.
			// Do NOT use it as a workload-lookup candidate — the alert name is not a K8s
			// workload name and an ILIKE match would produce false positives.
			// Also clear subjectKind: an entity may have a Type but no DisplayName, leaving
			// a stale kind (e.g. "pod") paired with the alert title as the subject name.
			subjectName = title
			subjectKind = ""
		}
		description = fmt.Sprintf("SolarWinds alert '%s'", title)
		if payload.Condition != "" {
			description = fmt.Sprintf("SolarWinds alert '%s': %s", title, payload.Condition)
		}

		// For cleared alerts extract timestamps from the first entity.
		if isCleared && len(payload.Entities) > 0 {
			first := payload.Entities[0]
			if first.TriggeredAt != "" {
				if t, err := time.Parse(time.RFC3339, first.TriggeredAt); err == nil {
					eventCreatedAt = t
				}
			}
			if first.ClearedAt != "" {
				if t, err := time.Parse(time.RFC3339, first.ClearedAt); err == nil {
					eventEndsAt = t
				}
			}
		}
	}

	// Title-as-subject events must skip the central workload-match step. The
	// alert name is not a K8s workload name and ILIKE would produce false
	// positives. The label is consumed by core.MatchWorkloadAndEnrich.
	if !hasEntitySubject {
		if labels == nil {
			labels = map[string]string{}
		}
		labels["nb_skip_workload_match"] = "true"
	}

	investigation := core.EventIncomingWebhookInvestigation{
		RuleName:    title,
		RuleId:      fingerprint,
		Fingerprint: fingerprint,
		Status:      event.EventStatus(status),
		Severity:    priority,
		SourceUrl:   payload.DetailURL,
		Labels:      labels,
		Evidences:   []event.EventEvidence{buildSolarWindsAlertEvidence(payload, severityStr)},
	}

	webhookEvent := core.EventIncomingWebhook{
		WebhookId:        payload.TriggerID,
		EventType:        "solarwinds_alert",
		EventId:          fingerprint,
		EventUrl:         payload.DetailURL,
		EventStatus:      string(status),
		EventPriority:    string(priority),
		EventTitle:       title,
		EventDescription: description,
		Investigation:    investigation,
		EventSubjectName: subjectName,
		EventSubjectKind: subjectKind,
		AccountId:        accountId,
		EventCreatedAt:   eventCreatedAt, // zero → framework substitutes time.Now()
		EventEndsAt:      eventEndsAt,    // non-zero only for cleared events
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
			severityLabel := strings.ToLower(severityStr)
			if severityLabel == "" {
				severityLabel = "warning"
			}
			runbook := ""
			if payload.Runbook != nil {
				runbook = *payload.Runbook
			}
			expr := payload.Condition
			if expr == "" {
				expr = payload.Query
			}
			eventReq := eventrule.EventConfig{
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
				Alert:         title,
				Duration:      "0",
				AccountID:     accountId,
				Source:        "solarwinds_webhook",
				Category:      "alert",
				Severity:      severityLabel,
				Enabled:       true,
				TriggerParams: []map[string]any{},
				ActionParams:  []map[string]any{},
			}
			if _, err := eventrule.CreateEventRule(sc, eventReq); err != nil {
				sc.GetLogger().Error("solarwinds_webhook: CreateEventRule failed", "error", err)
			}
		}()
	}

	return []core.EventIncomingWebhook{webhookEvent}, nil
}

// extractSolarWindsEntityInfo builds the labels map and returns the primary subject
// name/kind from the entities array. hasEntitySubject is true only when the first
// entity has a non-empty DisplayName (used to guard K8s workload lookup).
func extractSolarWindsEntityInfo(payload SolarWindsWebhookPayload) (subjectName, subjectKind string, hasEntitySubject bool, labels map[string]string) {
	labels = make(map[string]string)
	labels["severity"] = payload.Severity
	labels["priority"] = payload.Priority
	if payload.Condition != "" {
		labels["condition"] = payload.Condition
	}
	if payload.Type != "" {
		labels["alert_type"] = payload.Type
	}

	if len(payload.Entities) == 0 {
		return
	}

	first := payload.Entities[0]
	subjectName = first.DisplayName
	subjectKind = mapSolarWindsEntityKind(first.Type)
	hasEntitySubject = first.DisplayName != ""
	labels["entity_type"] = first.Type
	if first.EntityId != "" {
		labels["entity_id"] = first.EntityId
	}

	applyEntityLabels(payload.Entities, labels)
	return
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

// applyEntityLabels adds entity_names and metric_<entity>_<metric> labels for all entities.
// Metric keys are prefixed with the entity DisplayName to prevent collisions when multiple
// entities report the same metric name (e.g. k8s.cluster.spec.memory.requests).
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

// buildSolarWindsAlertEvidence constructs the structured evidence block for a SWO alert.
func buildSolarWindsAlertEvidence(payload SolarWindsWebhookPayload, severityStr string) event.EventEvidence {
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
				Message:  fmt.Sprintf("TriggerID: %s, Condition: %s", payload.TriggerID, conditionInsight),
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
// an internal EventPriortiy. Unknown values default to Medium (not Low) because
// SWO only fires alerts for meaningful threshold breaches.
func mapSolarWindsSeverity(severity string) event.EventPriortiy {
	switch severity {
	case "CRITICAL":
		return event.EventPriortiyHigh
	case "HIGH":
		return event.EventPriortiyHigh
	case "WARNING", "MEDIUM":
		return event.EventPriortiyMedium
	case "LOW":
		return event.EventPriortiyLow
	case "INFO":
		return event.EventPriortiyInfo
	default:
		return event.EventPriortiyMedium
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
