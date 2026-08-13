package integrations

import (
	"fmt"
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
	core.RegisterIntegration(ElasticsearchWebhook{})
}

// ElasticsearchWebhook ingests alerts from Kibana Alerting (Stack Management →
// Rules) delivered through a Kibana **Webhook** connector, and from any
// Elasticsearch Watcher `webhook` action shaped the same way.
//
// It is deliberately distinct from the `ES` datasource integration in
// elasticsearch.go (which *queries* Elasticsearch for logs/metrics/traces) and
// from ElasticsearchAgent in elasticsearch_agent.go. This one only *receives*
// alerts.
//
// Unlike Datadog / Grafana / PagerDuty, Kibana has no fixed outbound alert
// schema — a Webhook connector POSTs whatever mustache-templated body the
// operator configures. So this parser defines a canonical body (documented
// below) and reads it defensively: every field except the rule identity is
// optional, and flat aliases are accepted so a hand-rolled Watcher template
// does not have to nest.
//
// Canonical connector body (Kibana → Connectors → Webhook → Body):
//
//	{
//	  "rule":   { "id": "{{rule.id}}", "name": "{{rule.name}}",
//	              "type": "{{rule.type}}", "url": "{{rule.url}}" },
//	  "alert":  { "id": "{{alert.id}}", "action_group": "{{alert.actionGroup}}" },
//	  "status": "firing",
//	  "severity": "high",
//	  "title": "{{rule.name}}",
//	  "message": "{{context.message}}",
//	  "value": "{{context.value}}",
//	  "link": "{{context.link}}",
//	  "conditions": "{{context.conditions}}",
//	  "timestamp": "{{date}}",
//	  "labels": { "namespace": "prod", "pod": "checkout-7d9f" }
//	}
//
// `status` is templated per action group by the operator — "firing" on the
// rule's "Query matched" action and "resolved" on its "Recovered" action —
// because Kibana exposes recovery as a separate action, not as a field the
// firing payload can carry.
type ElasticsearchWebhook struct{}

const IntegrationElasticsearchWebhook = "elasticsearch_webhook"

func (m ElasticsearchWebhook) Name() string {
	return IntegrationElasticsearchWebhook
}

func (m ElasticsearchWebhook) Category() core.IntegrationCategory {
	return core.IntegrationCategoryIncidentWebhook
}

func (m ElasticsearchWebhook) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Elasticsearch/Kibana Webhook",
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

func (m ElasticsearchWebhook) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m ElasticsearchWebhook) MergeEventWebhooks(sc *security.RequestContext, previous core.EventIncomingWebhook, new core.EventIncomingWebhook) (core.EventIncomingWebhook, error) {
	return new, nil
}

func (m ElasticsearchWebhook) ProcessEventWebook(sc *security.RequestContext, settings []core.IntegrationConfigValue, accountId, webhookPayloadString string) ([]core.EventIncomingWebhook, error) {
	payload := map[string]any{}
	if err := common.UnmarshalJson([]byte(webhookPayloadString), &payload); err != nil {
		return []core.EventIncomingWebhook{}, err
	}

	parsed, err := mapElasticsearchAlertToEvent(accountId, payload)
	if err != nil {
		return []core.EventIncomingWebhook{}, err
	}

	// Upsert an event rule so the alert shows up in the rule management UI and
	// the workflow Event-Trigger catalog, consistent with the other webhook
	// integrations. Skipped on recovery — the rule already exists from the
	// firing delivery. Async so a slow write never delays the 200.
	if parsed.Investigation.Status != event.EventStatusResolved {
		ruleName := parsed.Investigation.RuleName
		severityLabel := eventRuleSeverity(parsed.Investigation.Severity)
		description := parsed.EventDescription
		labels := parsed.Investigation.Labels
		// Kibana renders `{{context.conditions}}` as a human-readable predicate
		// ("Number of matching documents is greater than 100"). That is the
		// closest thing to a rule expression the alert payload carries — the
		// observed value is per-firing, not a property of the rule.
		expr := labels["conditions"]
		alertType := elasticsearchAlertType(labels["rule_type"])
		// Stored so a rule row can be traced back to (and later reconciled
		// with) the Kibana rule it came from. MetricProvider/Source use the
		// keys the alertrule registry resolves on (getAlertRuleSource: ES/user).
		providerConfig := map[string]any{
			"kibana_rule_id":   parsed.Investigation.RuleId,
			"kibana_rule_type": labels["rule_type"],
		}
		if v := labels["rule_url"]; v != "" {
			providerConfig["kibana_rule_url"] = v
		}
		if len(parsed.EventTags) > 0 {
			providerConfig["kibana_rule_tags"] = parsed.EventTags
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					sc.GetLogger().Error("elasticsearch_webhook: panic in CreateEventRule goroutine", "panic", r)
				}
			}()
			eventReq := eventrule.EventConfig{
				Annotations: struct {
					Description string `json:"description"`
					Summary     string `json:"summary"`
					Runbook     string `json:"runbook"`
				}{
					Description: description,
					Summary:     ruleName,
					Runbook:     "",
				},
				Expr: expr,
				Labels: struct {
					Severity string `json:"severity"`
				}{Severity: severityLabel},
				Alert:                ruleName,
				Duration:             "0",
				AccountID:            accountId,
				Source:               IntegrationElasticsearchWebhook,
				Category:             "alert",
				Severity:             severityLabel,
				Enabled:              true,
				TriggerParams:        []map[string]any{},
				ActionParams:         []map[string]any{},
				AlertType:            alertType,
				MetricProvider:       "ES",
				MetricProviderSource: "user",
				ProviderConfig:       providerConfig,
			}
			if _, err := eventrule.CreateEventRule(sc, eventReq); err != nil {
				sc.GetLogger().Error("elasticsearch_webhook: CreateEventRule failed", "error", err, "alert", ruleName)
			}
		}()
	}

	return []core.EventIncomingWebhook{*parsed}, nil
}

// mapElasticsearchAlertToEvent maps one Kibana alert delivery to an
// EventIncomingWebhook. Single source of truth for the mapping so the live
// webhook path and the replay path produce identical, fingerprint-keyed events.
func mapElasticsearchAlertToEvent(accountId string, payload map[string]any) (*core.EventIncomingWebhook, error) {
	// Rule identity. investigateWebhookEvent hard-requires a non-empty RuleId,
	// and the fingerprint is derived from it, so an unidentifiable payload is a
	// hard error rather than a silently mis-keyed event.
	ruleId := firstNonEmpty(
		nestedString(payload, "rule", "id"),
		stringValue(payload["rule_id"]),
	)
	ruleName := firstNonEmpty(
		nestedString(payload, "rule", "name"),
		stringValue(payload["rule_name"]),
	)
	if ruleId == "" {
		ruleId = ruleName
	}
	if ruleId == "" {
		return nil, fmt.Errorf("elasticsearch webhook: payload carries no rule id or rule name")
	}
	if ruleName == "" {
		ruleName = ruleId
	}

	// Alert instance. Kibana emits one delivery per alert instance when the rule
	// groups by a term (e.g. one per container), so the instance id has to be
	// part of the fingerprint — otherwise every group collapses onto one event
	// and a recovery for one group would close all of them.
	alertId := firstNonEmpty(
		nestedString(payload, "alert", "id"),
		stringValue(payload["alert_id"]),
	)
	fingerprint := ruleId
	if alertId != "" {
		fingerprint = ruleId + ":" + alertId
	}

	status := mapElasticsearchStatus(firstNonEmpty(
		stringValue(payload["status"]),
		nestedString(payload, "alert", "action_group"),
	))
	severity := mapElasticsearchSeverity(stringValue(payload["severity"]))

	eventUrl := firstNonEmpty(
		stringValue(payload["link"]),
		nestedString(payload, "rule", "url"),
	)
	message := firstNonEmpty(
		stringValue(payload["message"]),
		nestedString(payload, "context", "message"),
	)
	title := firstNonEmpty(stringValue(payload["title"]), ruleName)

	labels := mapStringAnyToStringString(payload["labels"])
	if labels == nil {
		labels = map[string]string{}
	}
	// Carry the Kibana-side identifiers as labels so they are searchable and
	// usable as account_mapping / webhook_label_mapping inputs.
	putIfNotEmpty(labels, "alertname", ruleName)
	putIfNotEmpty(labels, "rule_id", ruleId)
	putIfNotEmpty(labels, "alert_id", alertId)
	putIfNotEmpty(labels, "rule_type", nestedString(payload, "rule", "type"))
	putIfNotEmpty(labels, "rule_url", nestedString(payload, "rule", "url"))
	putIfNotEmpty(labels, "value", firstNonEmpty(
		stringValue(payload["value"]),
		nestedString(payload, "context", "value"),
	))
	// `{{context.conditions}}` — the rule's predicate in words. Kept as a label
	// so it can also serve as the event_rules expression.
	putIfNotEmpty(labels, "conditions", firstNonEmpty(
		stringValue(payload["conditions"]),
		nestedString(payload, "context", "conditions"),
	))

	subjectKind, subjectName := extractElasticsearchSubject(labels)
	namespace := firstNonEmpty(labels["namespace"], labels["kubernetes.namespace"])

	eventCreatedAt := parseElasticsearchTime(firstNonEmpty(
		stringValue(payload["timestamp"]),
		stringValue(payload["date"]),
	))
	// Only a recovery delivery bounds the alert; a firing one is still open.
	var eventEndsAt time.Time
	if status == event.EventStatusResolved {
		eventEndsAt = eventCreatedAt
	}

	return &core.EventIncomingWebhook{
		AccountId:             accountId,
		WebhookId:             uuid.NewString(),
		EventType:             ruleName,
		EventId:               fingerprint,
		EventUrl:              eventUrl,
		EventStatus:           string(status),
		EventPriority:         string(severity),
		EventCreatedAt:        eventCreatedAt, // zero → framework substitutes time.Now()
		EventEndsAt:           eventEndsAt,
		EventTitle:            title,
		EventDescription:      message,
		EventTags:             parseElasticsearchTags(payload["tags"]),
		EventSubjectKind:      subjectKind,
		EventSubjectName:      subjectName,
		EventSubjectNamespace: namespace,
		Investigation: core.EventIncomingWebhookInvestigation{
			RuleName:    ruleName,
			Labels:      labels,
			Annotations: map[string]string{},
			RuleType:    "elasticsearch",
			RuleId:      ruleId,
			Fingerprint: fingerprint,
			Status:      status,
			Severity:    severity,
			SourceUrl:   eventUrl,
		},
	}, nil
}

// extractElasticsearchSubject picks the K8s workload subject from alert labels.
// Accepts both the short label keys an operator would hand-write and the ECS
// dotted keys that Elastic Agent puts on the underlying documents.
// Priority: pod > deployment > statefulset > daemonset > replicaset > job > cronjob.
func extractElasticsearchSubject(labels map[string]string) (kind, name string) {
	for _, k := range []string{"pod", "deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob"} {
		if v := labels[k]; v != "" {
			return k, v
		}
		if v := labels["kubernetes."+k]; v != "" {
			return k, v
		}
	}
	if v := labels["kubernetes.pod.name"]; v != "" {
		return "pod", v
	}
	return "", ""
}

// elasticsearchAlertType maps a Kibana rule type onto event_rules.alert_type,
// which is FK-constrained to 'log' / 'metric'. The ES query rule counts
// documents, so it is log-shaped; anything else (and an absent rule type)
// returns "" and inherits the 'metric' default applied by CreateEventRule.
func elasticsearchAlertType(kibanaRuleType string) string {
	if kibanaRuleType == ".es-query" {
		return "log"
	}
	return ""
}

// eventRuleSeverity narrows an EventPriority to the two values event_rules
// accepts. event_rules.severity is FK-constrained to event_rule_severity, whose
// only rows are 'critical' and 'warning', so passing the lower-cased priority
// ("high"/"medium"/"low") fails the insert with 23503.
func eventRuleSeverity(priority event.EventPriority) string {
	if priority == event.EventPriorityHigh {
		return "critical"
	}
	return "warning"
}

// mapElasticsearchStatus maps the operator-templated status (or the Kibana
// action group it falls back to) onto EventStatus. Kibana's recovery action
// group is literally "recovered"; the default firing group for the ES query
// rule is "query matched".
func mapElasticsearchStatus(status string) event.EventStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "resolved", "recovered", "ok", "closed":
		return event.EventStatusResolved
	default:
		return event.EventStatusFiring
	}
}

// mapElasticsearchSeverity maps a severity string to EventPriority. Kibana
// rules carry no native severity, so this reads whatever the operator templated
// into the body and defaults to low when absent.
func mapElasticsearchSeverity(severity string) event.EventPriority {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high", "error", "p1", "p2":
		return event.EventPriorityHigh
	case "warning", "warn", "medium", "p3":
		return event.EventPriorityMedium
	case "low", "minor", "p4", "p5":
		return event.EventPriorityLow
	case "info", "informational", "none":
		return event.EventPriorityInfo
	case "debug":
		return event.EventPriorityDebug
	default:
		return event.EventPriorityLow
	}
}

// parseElasticsearchTime parses Kibana's `{{date}}` (ISO-8601 / RFC3339).
// Returns the zero time on anything unparseable — the framework substitutes
// time.Now() for a zero EventCreatedAt.
func parseElasticsearchTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	return time.Time{}
}

// parseElasticsearchTags accepts either a JSON array of strings or the
// comma-separated string Kibana produces when `{{rule.tags}}` is rendered into
// a string field.
func parseElasticsearchTags(raw any) []string {
	switch v := raw.(type) {
	case []any:
		tags := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(stringValue(item)); s != "" {
				tags = append(tags, s)
			}
		}
		return tags
	case string:
		var tags []string
		for _, part := range strings.Split(v, ",") {
			if s := strings.TrimSpace(part); s != "" {
				tags = append(tags, s)
			}
		}
		return tags
	}
	return nil
}

// nestedString reads payload[outer][inner] as a string, tolerating a missing or
// wrongly-typed outer object.
func nestedString(payload map[string]any, outer, inner string) string {
	obj, ok := payload[outer].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(obj[inner])
}

// stringValue renders a JSON scalar as a string. Kibana templates every value
// as a string, but a hand-rolled Watcher body may send a real number or bool.
func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

func putIfNotEmpty(labels map[string]string, key, value string) {
	if value != "" && labels[key] == "" {
		labels[key] = value
	}
}
