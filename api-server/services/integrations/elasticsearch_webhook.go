package integrations

import (
	"fmt"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"regexp"
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
//	  "alert":  { "id": "{{alert.id}}", "uuid": "{{alert.uuid}}",
//	              "action_group": "{{alert.actionGroup}}" },
//	  "status": "firing",
//	  "severity": "high",
//	  "title": "{{rule.name}}",
//	  "message": "{{context.message}}",
//	  "value": "{{context.value}}",
//	  "link": "{{context.link}}",
//	  "conditions": "{{context.conditions}}",
//	  "timestamp": "{{date}}",
//	  "labels": { "namespace": "prod" }
//	}
//
// `labels` is optional. `{{alert.id}}` is the rule's group value — or, for a rule
// grouped by several fields, the values joined by a comma — and each value is
// tried as a pod and as a workload name. An explicit workload label (`pod`,
// `deployment`, …) still wins when it holds a single name.
//
// `{{alert.uuid}}` is optional and names one firing: Kibana (8.8 and later) issues
// a new one each time the alert becomes active and repeats it on every run until
// the alert recovers. With it, an alert that fires again after it recovered gets
// an event of its own; without it every firing shares the alert's one event.
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

// WebhookLabelHelp describes the labels mapElasticsearchAlertToEvent builds. Update it with the parser.
func (m ElasticsearchWebhook) WebhookLabelHelp() core.WebhookLabelHelp {
	return core.WebhookLabelHelp{
		Summary: "Keys under labels in the connector body become labels under their own name. A few rule and alert fields are added under fixed names.",
		Labels: []core.WebhookLabelSource{
			{Label: "<key under labels>", Source: "labels.<key>"},
			{Label: "alertname", Source: "rule.name"},
			{Label: "rule_id", Source: "rule.id"},
			{Label: "rule_type", Source: "rule.type"},
			{Label: "alert_id", Source: "alert.id"},
			{Label: "alert_uuid", Source: "alert.uuid"},
			{Label: "value", Source: "value"},
			{Label: "conditions", Source: "conditions"},
		},
		Notes: []string{
			"Only flat text values under labels are kept. A nested object, number or boolean there is dropped.",
			"status, severity, title, message and tags are not labels.",
		},
	}
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
			"kibana_rule_id":   labels["rule_id"],
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
	// One firing of that alert. Any non-empty value is taken as sent, so a
	// hand-rolled Watcher body can supply its own; a Kibana older than 8.8 renders
	// `{{alert.uuid}}` as "", which leaves the delivery on the alert's one event.
	// The payload carries no time the firing began ({{date}} is the delivery's), so
	// the id itself is the key rather than core.FiringKeyFromStart.
	firingKey := firstNonEmpty(
		nestedString(payload, "alert", "uuid"),
		stringValue(payload["alert_uuid"]),
	)

	status := mapElasticsearchStatus(firstNonEmpty(
		stringValue(payload["status"]),
		nestedString(payload, "alert", "action_group"),
	))
	severity := mapElasticsearchSeverity(stringValue(payload["severity"]))

	// Kibana renders `{{context.link}}` as a path and `{{rule.url}}` as "" unless
	// server.publicBaseUrl is set. A path would be opened against this server, so
	// only an absolute link is kept.
	eventUrl := firstAbsoluteURL(
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
	// A firing that arrives while the alert's event is still open is folded into
	// that event (core.firingFindingIDFor), so this label is where a recovery
	// Nudgebee never received shows: it no longer matches the event's finding_id.
	putIfNotEmpty(labels, "alert_uuid", firingKey)
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

	subjectKey, subjectKind, subjectName := extractElasticsearchSubject(labels)
	namespace := firstNonEmpty(labels["namespace"], labels["kubernetes.namespace"])
	// The payload often cannot name the workload outright. A rule grouped by
	// container carries only the container name, which equals the workload name for
	// the app container but not for a sidecar (istio-proxy, filebeat, …). A rule
	// grouped by several fields carries its group values joined by a comma, whether
	// in `{{alert.id}}` or in whichever label the operator templated it into. So
	// every such value is offered as a *candidate* rather than assigned:
	// core.MatchWorkloadAndEnrich promotes one only on an exact k8s_workloads match,
	// and core.ResolveSubjectFromPodCandidates only on an exact pod name.
	var names []string
	for strings.Contains(subjectName, ",") {
		// A group id, not a name. Left in the label it would be stored as the pod
		// the alert named; it stays available as alert_id. The next subject label,
		// if any, is then considered in its place.
		names = append(names, splitKibanaGroup(subjectName)...)
		delete(labels, subjectKey)
		subjectKey, subjectKind, subjectName = extractElasticsearchSubject(labels)
	}
	names = append(names, elasticsearchWorkloadCandidates(labels)...)
	names = append(names, splitKibanaGroup(alertId)...)
	candidates := elasticsearchCandidateNames(names, subjectName)
	if len(candidates) > 0 {
		labels[core.WorkloadCandidatesLabel] = strings.Join(candidates, ",")
	}
	// An explicit non-pod workload label is the operator naming the subject; a pod
	// found among the candidates must not replace it.
	var podCandidates []string
	switch subjectKind {
	case "pod":
		podCandidates = append([]string{subjectName}, candidates...)
	case "":
		podCandidates = candidates
	}

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
		FiringKey:             firingKey,
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
		PodNameCandidates:     podCandidates,
		Investigation: core.EventIncomingWebhookInvestigation{
			RuleName:    ruleName,
			Labels:      labels,
			Annotations: map[string]string{},
			RuleType:    "elasticsearch",
			// RuleId becomes the event's aggregation_key, which is what the rule
			// column shows and what event_rules.alert is matched on — so it is the
			// rule name, as for the other name-keyed sources. The Kibana id stays
			// in labels["rule_id"] and in the fingerprint.
			RuleId:      ruleName,
			Fingerprint: fingerprint,
			Status:      status,
			Severity:    severity,
			SourceUrl:   eventUrl,
		},
	}, nil
}

// extractElasticsearchSubject picks the K8s workload subject from alert labels,
// and reports which label key held it.
// Accepts both the short label keys an operator would hand-write and the ECS
// dotted keys that Elastic Agent puts on the underlying documents.
// Priority: pod > deployment > statefulset > daemonset > replicaset > job > cronjob.
func extractElasticsearchSubject(labels map[string]string) (key, kind, name string) {
	for _, k := range []string{"pod", "deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob"} {
		if v := labels[k]; v != "" {
			return k, k, v
		}
		if v := labels["kubernetes."+k]; v != "" {
			return "kubernetes." + k, k, v
		}
	}
	if v := labels["kubernetes.pod.name"]; v != "" {
		return "kubernetes.pod.name", "pod", v
	}
	return "", "", ""
}

// elasticsearchWorkloadCandidates returns the container names an alert carries,
// deduplicated and in label-key priority order. Accepts the short key an operator
// would hand-write plus the ECS dotted key Elastic Agent puts on container_logs
// documents. A value that is a group id contributes each of its parts.
func elasticsearchWorkloadCandidates(labels map[string]string) []string {
	candidates := []string{}
	seen := map[string]bool{}
	for _, k := range []string{"container", "container.name", "kubernetes.container", "kubernetes.container.name"} {
		for _, v := range splitKibanaGroup(labels[k]) {
			if seen[v] {
				continue
			}
			seen[v] = true
			candidates = append(candidates, v)
		}
	}
	return candidates
}

// splitKibanaGroup splits a Kibana group id into its values. A rule grouped by
// one field renders `{{alert.id}}` as that field's value; grouped by several, as
// the values joined by a comma.
func splitKibanaGroup(groupId string) []string {
	var values []string
	for _, v := range strings.Split(groupId, ",") {
		if v = strings.TrimSpace(v); v != "" {
			values = append(values, v)
		}
	}
	return values
}

// kubernetesNameRegex is the shape of a Kubernetes object name (RFC 1123
// subdomain). It keeps values that cannot be a pod or workload — the alert id of
// an ungrouped rule is the literal "query matched" — out of the inventory lookups.
var kubernetesNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// elasticsearchCandidateNames narrows names to distinct values that could be a
// Kubernetes object name, in order, leaving out the subject already chosen.
func elasticsearchCandidateNames(names []string, subject string) []string {
	candidates := []string{}
	seen := map[string]bool{subject: true}
	for _, n := range names {
		if seen[n] || len(n) > 253 || !kubernetesNameRegex.MatchString(n) {
			continue
		}
		seen[n] = true
		candidates = append(candidates, n)
	}
	return candidates
}

// firstAbsoluteURL returns the first value that is an absolute http(s) URL.
func firstAbsoluteURL(values ...string) string {
	for _, v := range values {
		u, err := url.Parse(v)
		if err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
			return v
		}
	}
	return ""
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
