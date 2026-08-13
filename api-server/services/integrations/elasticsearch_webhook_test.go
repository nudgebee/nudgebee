package integrations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/event"
)

// canonicalKibanaPayload mirrors the body the documented Kibana Webhook
// connector template produces for the "stderr log spike by container" ES query
// rule, grouped by kubernetes.container_name.
func canonicalKibanaPayload() map[string]any {
	return map[string]any{
		"rule": map[string]any{
			"id":   "baf85522-3bcb-41a7-bd3c-72942707a6fc",
			"name": "Sample: stderr log spike by container",
			"type": ".es-query",
			"url":  "https://kibana.example/app/management/insightsAndAlerting/triggersActions/rule/baf85522",
		},
		"alert": map[string]any{
			"id":           "kubewatch",
			"action_group": "query matched",
		},
		"status":     "firing",
		"severity":   "high",
		"title":      "stderr spike on kubewatch",
		"message":    "container 'kubewatch' logged 2568 stderr lines in the last 5m",
		"value":      "2568",
		"link":       "https://kibana.example/app/discover#/?_a=abc",
		"conditions": "Number of matching documents is greater than 100",
		"timestamp":  "2026-08-13T11:49:24Z",
		"tags":       "sample,logs,nudgebee",
		"labels": map[string]any{
			"namespace": "nudgebee",
			"pod":       "kubewatch-6d9f7c5b8-x2kqp",
			"cluster":   "example-cluster",
		},
	}
}

func TestMapElasticsearchAlertToEvent_Firing(t *testing.T) {
	got, err := mapElasticsearchAlertToEvent("acct-1", canonicalKibanaPayload())
	require.NoError(t, err)

	assert.Equal(t, "acct-1", got.AccountId)
	// Fingerprint must include the alert instance so grouped rules don't collapse
	// onto a single event.
	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc:kubewatch", got.EventId)
	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc:kubewatch", got.Investigation.Fingerprint)
	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc", got.Investigation.RuleId)
	assert.Equal(t, "Sample: stderr log spike by container", got.Investigation.RuleName)
	assert.Equal(t, "Sample: stderr log spike by container", got.EventType)
	assert.Equal(t, "stderr spike on kubewatch", got.EventTitle)
	assert.Equal(t, "container 'kubewatch' logged 2568 stderr lines in the last 5m", got.EventDescription)
	assert.Equal(t, "https://kibana.example/app/discover#/?_a=abc", got.EventUrl)
	assert.Equal(t, string(event.EventStatusFiring), got.EventStatus)
	assert.Equal(t, string(event.EventPriorityHigh), got.EventPriority)
	assert.Equal(t, "elasticsearch", got.Investigation.RuleType)

	assert.Equal(t, "pod", got.EventSubjectKind)
	assert.Equal(t, "kubewatch-6d9f7c5b8-x2kqp", got.EventSubjectName)
	assert.Equal(t, "nudgebee", got.EventSubjectNamespace)

	assert.Equal(t, time.Date(2026, 8, 13, 11, 49, 24, 0, time.UTC), got.EventCreatedAt.UTC())
	// A firing alert is still open — no end time.
	assert.True(t, got.EventEndsAt.IsZero())

	assert.Equal(t, []string{"sample", "logs", "nudgebee"}, got.EventTags)

	assert.Equal(t, "Sample: stderr log spike by container", got.Investigation.Labels["alertname"])
	assert.Equal(t, "kubewatch", got.Investigation.Labels["alert_id"])
	assert.Equal(t, ".es-query", got.Investigation.Labels["rule_type"])
	assert.Equal(t, "2568", got.Investigation.Labels["value"])
	assert.Equal(t, "example-cluster", got.Investigation.Labels["cluster"])
	// Carried for the event_rules row: the rule URL and the human-readable
	// predicate that becomes the rule expression.
	assert.Equal(t, "Number of matching documents is greater than 100", got.Investigation.Labels["conditions"])
	assert.Equal(t,
		"https://kibana.example/app/management/insightsAndAlerting/triggersActions/rule/baf85522",
		got.Investigation.Labels["rule_url"])
}

func TestMapElasticsearchAlertToEvent_Recovered(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["status"] = "resolved"

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)

	// routeWebhookEvent switches on EventStatus to decide resolve-vs-investigate.
	assert.Equal(t, string(event.EventStatusResolved), got.EventStatus)
	assert.Equal(t, event.EventStatusResolved, got.Investigation.Status)
	assert.False(t, got.EventEndsAt.IsZero())
}

// Kibana's recovery action group is the only status signal when the operator
// forgets to template a literal `status` into the recovery action body.
func TestMapElasticsearchAlertToEvent_StatusFallsBackToActionGroup(t *testing.T) {
	payload := canonicalKibanaPayload()
	delete(payload, "status")
	payload["alert"] = map[string]any{"id": "kubewatch", "action_group": "recovered"}

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)
	assert.Equal(t, string(event.EventStatusResolved), got.EventStatus)
}

// A hand-rolled Watcher body sends flat keys rather than nested objects.
func TestMapElasticsearchAlertToEvent_FlatAliases(t *testing.T) {
	got, err := mapElasticsearchAlertToEvent("acct-1", map[string]any{
		"rule_id":   "watch-disk-full",
		"rule_name": "Disk full",
		"alert_id":  "node-3",
		"severity":  "critical",
		"tags":      []any{"infra", "disk"},
	})
	require.NoError(t, err)

	assert.Equal(t, "watch-disk-full", got.Investigation.RuleId)
	assert.Equal(t, "watch-disk-full:node-3", got.EventId)
	assert.Equal(t, "Disk full", got.EventTitle)
	assert.Equal(t, string(event.EventPriorityHigh), got.EventPriority)
	assert.Equal(t, []string{"infra", "disk"}, got.EventTags)
	// No timestamp in the payload — the framework substitutes time.Now() for a
	// zero EventCreatedAt, so it must stay zero here rather than be invented.
	assert.True(t, got.EventCreatedAt.IsZero())
}

// The rule name is the fallback identity when no rule id is templated.
func TestMapElasticsearchAlertToEvent_RuleNameAsIdentity(t *testing.T) {
	got, err := mapElasticsearchAlertToEvent("acct-1", map[string]any{
		"rule": map[string]any{"name": "Disk full"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Disk full", got.Investigation.RuleId)
	assert.Equal(t, "Disk full", got.EventId)
}

// investigateWebhookEvent hard-requires a non-empty RuleId, so an
// unidentifiable payload must fail loudly instead of producing a mis-keyed event.
func TestMapElasticsearchAlertToEvent_MissingIdentity(t *testing.T) {
	_, err := mapElasticsearchAlertToEvent("acct-1", map[string]any{
		"status":  "firing",
		"message": "something happened",
	})
	require.Error(t, err)
}

// event_rules.severity is FK-constrained to event_rule_severity, which holds
// only 'critical' and 'warning'. Anything else fails the CreateEventRule insert
// with 23503, so every EventPriority must narrow to one of the two.
func TestEventRuleSeverity_OnlyEmitsAllowedValues(t *testing.T) {
	allowed := map[string]bool{"critical": true, "warning": true}

	for _, priority := range []event.EventPriority{
		event.EventPriorityHigh,
		event.EventPriorityMedium,
		event.EventPriorityLow,
		event.EventPriorityInfo,
		event.EventPriorityDebug,
		"", // unset — the parser defaults to low, but guard the zero value too
	} {
		got := eventRuleSeverity(priority)
		assert.True(t, allowed[got], "priority %q produced %q, which event_rule_severity does not contain", priority, got)
	}

	assert.Equal(t, "critical", eventRuleSeverity(event.EventPriorityHigh))
	assert.Equal(t, "warning", eventRuleSeverity(event.EventPriorityMedium))
}

// event_rules.alert_type is FK-constrained to 'log' / 'metric'; "" is safe
// because CreateEventRule defaults an empty value to 'metric'.
func TestElasticsearchAlertType(t *testing.T) {
	assert.Equal(t, "log", elasticsearchAlertType(".es-query"))
	assert.Equal(t, "", elasticsearchAlertType(".index-threshold"))
	assert.Equal(t, "", elasticsearchAlertType(""))
}

func TestExtractElasticsearchSubject_ECSDottedKeys(t *testing.T) {
	kind, name := extractElasticsearchSubject(map[string]string{
		"kubernetes.pod.name": "checkout-7d9f",
	})
	assert.Equal(t, "pod", kind)
	assert.Equal(t, "checkout-7d9f", name)

	kind, name = extractElasticsearchSubject(map[string]string{
		"kubernetes.deployment": "checkout",
	})
	assert.Equal(t, "deployment", kind)
	assert.Equal(t, "checkout", name)

	kind, name = extractElasticsearchSubject(map[string]string{"cluster": "dev"})
	assert.Equal(t, "", kind)
	assert.Equal(t, "", name)
}
