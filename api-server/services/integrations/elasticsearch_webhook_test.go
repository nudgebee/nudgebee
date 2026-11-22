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
	// RuleId is what the event is keyed and shown by, so it is the rule name; the
	// Kibana id stays in the fingerprint above and in the rule_id label.
	assert.Equal(t, "Sample: stderr log spike by container", got.Investigation.RuleId)
	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc", got.Investigation.Labels["rule_id"])
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

	assert.Equal(t, "Disk full", got.Investigation.RuleId)
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
	key, kind, name := extractElasticsearchSubject(map[string]string{
		"kubernetes.pod.name": "checkout-7d9f",
	})
	assert.Equal(t, "kubernetes.pod.name", key)
	assert.Equal(t, "pod", kind)
	assert.Equal(t, "checkout-7d9f", name)

	key, kind, name = extractElasticsearchSubject(map[string]string{
		"kubernetes.deployment": "checkout",
	})
	assert.Equal(t, "kubernetes.deployment", key)
	assert.Equal(t, "deployment", kind)
	assert.Equal(t, "checkout", name)

	key, kind, name = extractElasticsearchSubject(map[string]string{"cluster": "dev"})
	assert.Equal(t, "", key)
	assert.Equal(t, "", kind)
	assert.Equal(t, "", name)
}

// A rule grouped by container carries no workload label, so the container name
// is offered to core.MatchWorkloadAndEnrich as a candidate rather than being
// assigned to the subject outright.
func TestMapElasticsearchAlertToEvent_ContainerBecomesWorkloadCandidate(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["alert"] = map[string]any{"id": "ecr-proxy-server", "action_group": "query matched"}
	payload["labels"] = map[string]any{
		"container": "ecr-proxy-server",
		"cluster":   "dev",
	}

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)

	// The subject stays empty — only an exact k8s_workloads match may set it.
	assert.Equal(t, "", got.EventSubjectKind)
	assert.Equal(t, "", got.EventSubjectName)
	assert.Equal(t, "ecr-proxy-server", got.Investigation.Labels["nb_workload_candidates"])
	assert.Equal(t, []string{"ecr-proxy-server"}, got.PodNameCandidates)
}

// A pod label names the subject, and the group value is still offered: when the
// pod is not in the inventory, the container name can still match the workload.
func TestMapElasticsearchAlertToEvent_CandidatesAlongsideAPodSubject(t *testing.T) {
	got, err := mapElasticsearchAlertToEvent("acct-1", canonicalKibanaPayload())
	require.NoError(t, err)

	assert.Equal(t, "kubewatch-6d9f7c5b8-x2kqp", got.EventSubjectName)
	assert.Equal(t, "kubewatch", got.Investigation.Labels["nb_workload_candidates"])
	assert.Equal(t, []string{"kubewatch-6d9f7c5b8-x2kqp", "kubewatch"}, got.PodNameCandidates)
}

// A rule grouped by two fields renders {{alert.id}} as "value1,value2". Templated
// into a subject label it is not a name: it must not become the subject, and it
// must not stay in the pod label, where it would be stored as the alert's pod.
func TestMapElasticsearchAlertToEvent_TwoFieldGroupIdInSubjectLabel(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["alert"] = map[string]any{"id": "checkout,checkout-54d6f745c5-9j599", "action_group": "query matched"}
	payload["labels"] = map[string]any{"pod": "checkout,checkout-54d6f745c5-9j599"}

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)

	assert.Equal(t, "", got.EventSubjectKind)
	assert.Equal(t, "", got.EventSubjectName)
	_, hasPod := got.Investigation.Labels["pod"]
	assert.False(t, hasPod, "the group id must not be left in the pod label")
	assert.Equal(t, "checkout,checkout-54d6f745c5-9j599", got.Investigation.Labels["alert_id"])
	assert.Equal(t, "checkout,checkout-54d6f745c5-9j599", got.Investigation.Labels["nb_workload_candidates"])
	assert.Equal(t, []string{"checkout", "checkout-54d6f745c5-9j599"}, got.PodNameCandidates)
	// The fingerprint keeps the whole group id: one event per group.
	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc:checkout,checkout-54d6f745c5-9j599", got.EventId)
}

// The same group id templated under two subject keys must leave neither behind.
func TestMapElasticsearchAlertToEvent_GroupIdUnderSeveralSubjectLabels(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["alert"] = map[string]any{"id": "checkout,checkout-54d6f745c5-9j599", "action_group": "query matched"}
	payload["labels"] = map[string]any{
		"pod":                 "checkout,checkout-54d6f745c5-9j599",
		"kubernetes.pod.name": "checkout,checkout-54d6f745c5-9j599",
	}

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)

	_, hasPod := got.Investigation.Labels["pod"]
	_, hasECSPod := got.Investigation.Labels["kubernetes.pod.name"]
	assert.False(t, hasPod)
	assert.False(t, hasECSPod)
	assert.Equal(t, "", got.EventSubjectName)
	assert.Equal(t, []string{"checkout", "checkout-54d6f745c5-9j599"}, got.PodNameCandidates)
}

// The documented body needs no labels at all: the group values in {{alert.id}}
// are the candidates.
func TestMapElasticsearchAlertToEvent_GroupIdWithoutLabels(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["alert"] = map[string]any{"id": "checkout,checkout-54d6f745c5-9j599", "action_group": "query matched"}
	delete(payload, "labels")

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)

	assert.Equal(t, "", got.EventSubjectName)
	assert.Equal(t, "checkout,checkout-54d6f745c5-9j599", got.Investigation.Labels["nb_workload_candidates"])
	assert.Equal(t, []string{"checkout", "checkout-54d6f745c5-9j599"}, got.PodNameCandidates)
}

// An ungrouped rule's alert id is the literal "query matched" — not a name, so it
// is never looked up.
func TestMapElasticsearchAlertToEvent_UngroupedRuleOffersNoCandidates(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["alert"] = map[string]any{"id": "query matched", "action_group": "query matched"}
	delete(payload, "labels")

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)

	assert.Equal(t, "", got.Investigation.Labels["nb_workload_candidates"])
	assert.Empty(t, got.PodNameCandidates)
}

// An explicit workload label is the operator naming the subject. A pod found
// among the group values must not replace it.
func TestMapElasticsearchAlertToEvent_ExplicitWorkloadLabelKeepsPodsOut(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["alert"] = map[string]any{"id": "checkout-54d6f745c5-9j599", "action_group": "query matched"}
	payload["labels"] = map[string]any{"deployment": "checkout"}

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)

	assert.Equal(t, "deployment", got.EventSubjectKind)
	assert.Equal(t, "checkout", got.EventSubjectName)
	assert.Empty(t, got.PodNameCandidates)
}

// Kibana renders {{context.link}} as a path, and {{rule.url}} as "", unless
// server.publicBaseUrl is set. A path stored as the alert URL opens a page on
// this server, so only an absolute link is kept.
func TestMapElasticsearchAlertToEvent_OnlyAnAbsoluteLinkIsKept(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["link"] = "/app/r?l=DISCOVER_APP_LOCATOR&v=8.19.11"
	payload["rule"] = map[string]any{"id": "rule-1", "name": "Rule one", "url": ""}

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)
	assert.Equal(t, "", got.EventUrl)
	assert.Equal(t, "", got.Investigation.SourceUrl)

	// A relative link falls back to an absolute rule URL.
	payload["rule"] = map[string]any{"id": "rule-1", "name": "Rule one", "url": "https://kibana.example/app/rule/rule-1"}
	got, err = mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)
	assert.Equal(t, "https://kibana.example/app/rule/rule-1", got.EventUrl)
}

func TestElasticsearchWorkloadCandidates(t *testing.T) {
	// ECS dotted key, as Elastic Agent writes it on container_logs documents.
	assert.Equal(t, []string{"ecr-proxy-server"}, elasticsearchWorkloadCandidates(map[string]string{
		"container.name": "ecr-proxy-server",
	}))

	// Same value under several keys collapses to one candidate.
	assert.Equal(t, []string{"app"}, elasticsearchWorkloadCandidates(map[string]string{
		"container":                 "app",
		"kubernetes.container.name": "app",
	}))

	// Short key wins the ordering when the values genuinely differ.
	assert.Equal(t, []string{"app", "istio-proxy"}, elasticsearchWorkloadCandidates(map[string]string{
		"container":      "app",
		"container.name": "istio-proxy",
	}))

	// A group id templated into the container label contributes each value.
	assert.Equal(t, []string{"app", "app-54d6f745c5-9j599"}, elasticsearchWorkloadCandidates(map[string]string{
		"container": "app,app-54d6f745c5-9j599",
	}))

	assert.Empty(t, elasticsearchWorkloadCandidates(map[string]string{"cluster": "dev"}))
}

// {{alert.uuid}} names one firing. It becomes the FiringKey and a label, and never
// part of the alert's own identity, which resolve and replay key on.
func TestMapElasticsearchAlertToEvent_AlertUuidIsTheFiringKey(t *testing.T) {
	payload := canonicalKibanaPayload()
	payload["alert"] = map[string]any{
		"id":           "kubewatch",
		"uuid":         " 4b3c68a5-4d6c-4866-a877-207b4fa717d9 ",
		"action_group": "query matched",
	}
	// The nested field is the documented one and wins over the flat alias.
	payload["alert_uuid"] = "flat-alias-value"

	got, err := mapElasticsearchAlertToEvent("acct-1", payload)
	require.NoError(t, err)
	assert.Equal(t, "4b3c68a5-4d6c-4866-a877-207b4fa717d9", got.FiringKey)
	assert.Equal(t, "4b3c68a5-4d6c-4866-a877-207b4fa717d9", got.Investigation.Labels["alert_uuid"])
	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc:kubewatch", got.EventId)
	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc:kubewatch", got.Investigation.Fingerprint)
}

func TestMapElasticsearchAlertToEvent_NoFiringKeyWithoutAlertUuid(t *testing.T) {
	cases := map[string]map[string]any{
		// The template from before the field was documented.
		"field absent": {"id": "kubewatch", "action_group": "query matched"},
		// Kibana older than 8.8 has no such variable and renders it empty.
		"rendered empty": {"id": "kubewatch", "uuid": "", "action_group": "query matched"},
		"only spaces":    {"id": "kubewatch", "uuid": "  ", "action_group": "query matched"},
	}
	for name, alert := range cases {
		t.Run(name, func(t *testing.T) {
			payload := canonicalKibanaPayload()
			payload["alert"] = alert

			got, err := mapElasticsearchAlertToEvent("acct-1", payload)
			require.NoError(t, err)
			assert.Empty(t, got.FiringKey)
			assert.NotContains(t, got.Investigation.Labels, "alert_uuid")
			assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc:kubewatch", got.EventId)
		})
	}
}

// A hand-rolled Watcher body has no {{alert.uuid}}; whatever it sends to name a
// firing is taken as sent, flat like its other fields.
func TestMapElasticsearchAlertToEvent_FlatAlertUuidAlias(t *testing.T) {
	got, err := mapElasticsearchAlertToEvent("acct-1", map[string]any{
		"rule_id":    "watch-disk-full",
		"alert_id":   "node-3",
		"alert_uuid": "watch-disk-full_2026-08-13T11:49:24Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "watch-disk-full_2026-08-13T11:49:24Z", got.FiringKey)
	assert.Equal(t, "watch-disk-full:node-3", got.EventId)
}
