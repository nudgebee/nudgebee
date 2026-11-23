package integrations

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Entity and node names here are synthetic. The payload *shapes* are taken from
// live SolarWinds deliveries: a firing delivery carries every breaching entity
// at once and omits the top-level triggerId, while a clear arrives as its own
// single-entity delivery that repeats the entity's id.
const (
	swTestTenant  = "11111111-1111-1111-1111-111111111111"
	swTestUser    = "22222222-2222-2222-2222-222222222222"
	swTestAccount = "33333333-3333-3333-3333-333333333333"
)

func swTestContext() *security.RequestContext {
	return security.NewRequestContextForUserTenant(swTestUser, swTestTenant, nil, nil, nil)
}

func swProcess(t *testing.T, payload string) []core.EventIncomingWebhook {
	t.Helper()
	events, err := SolarWindsWebhook{}.ProcessEventWebook(swTestContext(), nil, swTestAccount, payload)
	require.NoError(t, err)
	return events
}

// A multi-entity firing has no top-level triggerId at all. The parser used to
// fall back to a key built with a literal NUL byte, which Postgres rejects in a
// text column: the event never persisted and its webhook row stayed at
// processing_status='processing' with no error recorded anywhere.
const swMultiEntityFiring = `{
  "type": "entity-metric-raw",
  "name": "K8s node memory saturated",
  "severity": "CRITICAL",
  "priority": "CRITICAL",
  "description": "A node has averaged over 85% memory for 10 minutes.",
  "condition": "k8s.node.memory.utilization > 85",
  "alertDefinitionId": "aaaaaaaa-0000-0000-0000-000000000001",
  "detailUrl": "https://example.invalid/alerts/settings",
  "runbook": null,
  "triggerId": null,
  "entities": [
    {"type":"KubernetesNode","displayName":"node-a","entityId":"e-100",
     "triggerId":"t-a","activeAlertUrl":"https://example.invalid/active/a",
     "values":[{"name":"k8s.node.memory.utilization","value":91.4}]},
    {"type":"KubernetesNode","displayName":"node-b","entityId":"e-200",
     "triggerId":"t-b","activeAlertUrl":"https://example.invalid/active/b",
     "values":[{"name":"k8s.node.memory.utilization","value":88.2}]}
  ]
}`

func TestSolarWindsWebhook_MultiEntityFiringWithoutTopLevelTriggerId(t *testing.T) {
	events := swProcess(t, swMultiEntityFiring)

	// One event per entity: SWO reports them together but clears them separately,
	// so a single event could only ever name one of the two.
	require.Len(t, events, 2)

	subjects := []string{events[0].EventSubjectName, events[1].EventSubjectName}
	assert.ElementsMatch(t, []string{"node-a", "node-b"}, subjects)

	for _, e := range events {
		// The regression this file exists for: a NUL byte here means Postgres
		// rejects the write, and the same UPDATE carries processing_status.
		assert.NotContains(t, e.EventId, "\x00", "fingerprint must not contain a NUL byte")
		assert.NotContains(t, e.Investigation.Fingerprint, "\x00")
		assert.NotEmpty(t, e.EventId)
		assert.Equal(t, e.EventId, e.Investigation.Fingerprint)

		assert.Equal(t, string(event.EventStatusFiring), e.EventStatus)
		assert.Equal(t, "node", e.EventSubjectKind)
		assert.Equal(t, "K8s node memory saturated", e.EventTitle)
		// SWO's own description, not one rebuilt from title + condition.
		assert.Equal(t, "A node has averaged over 85% memory for 10 minutes.", e.EventDescription)
	}

	// Distinct entities must not share an occurrence chain.
	assert.NotEqual(t, events[0].EventId, events[1].EventId)

	// The per-entity trigger id is what SWO always sends; it belongs on the
	// delivery, not in the fingerprint.
	assert.ElementsMatch(t, []string{"t-a", "t-b"}, []string{events[0].WebhookId, events[1].WebhookId})
}

func TestSolarWindsWebhook_PerEntityLabelsDescribeOnlyThatEntity(t *testing.T) {
	events := swProcess(t, swMultiEntityFiring)
	require.Len(t, events, 2)

	bySubject := map[string]core.EventIncomingWebhook{}
	for _, e := range events {
		bySubject[e.EventSubjectName] = e
	}

	a := bySubject["node-a"].Investigation.Labels
	assert.Equal(t, "node-a", a["entity_names"])
	assert.Equal(t, "e-100", a["entity_id"])
	assert.Equal(t, "KubernetesNode", a["entity_type"])
	assert.Equal(t, "91.4", a["metric_node-a_k8s.node.memory.utilization"])
	// node-b's reading must not leak onto node-a's event.
	assert.NotContains(t, a, "metric_node-b_k8s.node.memory.utilization")
	assert.Equal(t, "aaaaaaaa-0000-0000-0000-000000000001", a["alert_definition_id"])

	b := bySubject["node-b"].Investigation.Labels
	assert.Equal(t, "node-b", b["entity_names"])
	assert.Equal(t, "88.2", b["metric_node-b_k8s.node.memory.utilization"])
}

// A clear arrives per entity and repeats that entity's id, so its fingerprint
// has to match the one the firing opened or the event stays open forever.
const swSingleEntityClear = `{
  "type": "alert-cleared-entity-raw",
  "name": "K8s node memory saturated",
  "severity": "CLEAR",
  "priority": "CRITICAL",
  "description": "A node has averaged over 85% memory for 10 minutes.",
  "condition": "k8s.node.memory.utilization > 85",
  "alertDefinitionId": "aaaaaaaa-0000-0000-0000-000000000001",
  "detailUrl": "https://example.invalid/alerts/settings",
  "triggerId": "t-a",
  "entities": [
    {"type":"KubernetesNode","displayName":"node-a","entityId":"e-100","triggerId":"t-a",
     "triggeredAt":"2026-10-12T19:04:36Z","clearedAt":"2026-10-12T19:20:00Z",
     "activeDurationInSeconds":924,"reason":"Metric returned below threshold"}
  ]
}`

func TestSolarWindsWebhook_ClearResolvesTheFiringItMatches(t *testing.T) {
	firing := swProcess(t, swMultiEntityFiring)
	require.Len(t, firing, 2)
	var nodeAFiring core.EventIncomingWebhook
	for _, e := range firing {
		if e.EventSubjectName == "node-a" {
			nodeAFiring = e
		}
	}
	require.NotEmpty(t, nodeAFiring.EventId)

	cleared := swProcess(t, swSingleEntityClear)
	require.Len(t, cleared, 1)

	// Same alert definition + same entity ⇒ same fingerprint, so the clear lands
	// on the open event instead of opening a second one.
	assert.Equal(t, nodeAFiring.EventId, cleared[0].EventId)

	assert.Equal(t, string(event.EventStatusResolved), cleared[0].EventStatus)
	// severity "CLEAR" must fall back to priority for the real level.
	assert.Equal(t, string(event.EventPriorityHigh), cleared[0].EventPriority)
	assert.False(t, cleared[0].EventCreatedAt.IsZero(), "triggeredAt should populate EventCreatedAt")
	assert.False(t, cleared[0].EventEndsAt.IsZero(), "clearedAt should populate EventEndsAt")
}

// Events opened before this parser moved to a derived fingerprint were stored
// under the raw trigger id. A resolve has to close those as well, or an alert
// that fired before the change and cleared after it stays open forever.
func TestSolarWindsWebhook_ClearAlsoClosesTheOldFingerprint(t *testing.T) {
	cleared := swProcess(t, swSingleEntityClear)
	require.Len(t, cleared, 1)

	// The old scheme's value for a single-entity delivery, which is the only
	// shape that ever persisted under it.
	assert.Equal(t, "t-a", cleared[0].LegacyFingerprint)
	assert.NotEqual(t, cleared[0].LegacyFingerprint, cleared[0].Investigation.Fingerprint,
		"a legacy fingerprint equal to the current one would make the extra close a no-op")
}

// Two entities carrying neither an id nor a name have no stable identity. They
// must still get distinct fingerprints, or they collapse into one occurrence
// chain and one of the two becomes invisible.
func TestSolarWindsWebhook_AnonymousEntitiesDoNotShareAFingerprint(t *testing.T) {
	events := swProcess(t, `{
	  "type": "entity-metric-raw",
	  "name": "K8s node memory saturated",
	  "severity": "CRITICAL",
	  "priority": "CRITICAL",
	  "condition": "k8s.node.memory.utilization > 85",
	  "alertDefinitionId": "aaaaaaaa-0000-0000-0000-000000000001",
	  "triggerId": null,
	  "entities": [
	    {"type":"KubernetesNode","values":[{"name":"k8s.node.memory.utilization","value":91.4}]},
	    {"type":"KubernetesNode","values":[{"name":"k8s.node.memory.utilization","value":88.2}]}
	  ]
	}`)

	require.Len(t, events, 2)
	assert.NotEqual(t, events[0].EventId, events[1].EventId)
	for _, e := range events {
		assert.NotContains(t, e.EventId, "\x00")
		// No name means no safe workload-lookup candidate.
		assert.Equal(t, "K8s node memory saturated", e.EventSubjectName)
		assert.Empty(t, e.EventSubjectKind)
		assert.Equal(t, "true", e.Investigation.Labels["nb_skip_workload_match"])
	}
}

// A payload with neither an alertDefinitionId nor a name is accepted when it
// carries a trigger id. Two such alerts on the same entity must not key on the
// empty string and merge into one occurrence chain.
func TestSolarWindsWebhook_UnnamedAlertsDoNotShareAFingerprint(t *testing.T) {
	unnamed := func(condition string) string {
		return `{
		  "type": "entity-metric-raw",
		  "name": "",
		  "severity": "CRITICAL",
		  "priority": "CRITICAL",
		  "condition": "` + condition + `",
		  "triggerId": "t-unnamed",
		  "entities": [{"type":"KubernetesNode","displayName":"node-f","entityId":"e-700","triggerId":"t-unnamed"}]
		}`
	}

	cpu := swProcess(t, unnamed("k8s.node.cpu.utilization > 85"))
	memory := swProcess(t, unnamed("k8s.node.memory.utilization > 85"))
	require.Len(t, cpu, 1)
	require.Len(t, memory, 1)

	assert.NotEqual(t, cpu[0].EventId, memory[0].EventId,
		"two unnamed alerts on one entity must not collapse into one chain")
	for _, e := range append(cpu, memory...) {
		assert.NotEmpty(t, e.EventId)
		assert.NotContains(t, e.EventId, "\x00")
		// With no name the title falls back, and the subject follows it.
		assert.Equal(t, "SolarWinds Alert", e.EventTitle)
	}
}

// SWO sends alert names with trailing spaces. The fingerprint has to trim, or
// one alert's chain splits on the whitespace alone.
func TestSolarWindsWebhook_FingerprintIgnoresSurroundingWhitespace(t *testing.T) {
	// No alertDefinitionId, so the name is what the fingerprint keys on.
	namedOnly := func(name string) string {
		return `{
		  "type": "entity-metric-raw",
		  "name": "` + name + `",
		  "severity": "CRITICAL",
		  "priority": "CRITICAL",
		  "condition": "k8s.node.cpu.utilization > 85",
		  "triggerId": "t-ws",
		  "entities": [{"type":"KubernetesNode","displayName":"node-g","entityId":"e-800","triggerId":"t-ws"}]
		}`
	}

	plain := swProcess(t, namedOnly("K8s node CPU high"))
	padded := swProcess(t, namedOnly("  K8s node CPU high  "))
	require.Len(t, plain, 1)
	require.Len(t, padded, 1)

	// Same alert, so padding on the name must not change its identity.
	assert.Equal(t, plain[0].EventId, padded[0].EventId)
	assert.Equal(t, "K8s node CPU high", padded[0].EventTitle)
}

// A firing that repeats after a clear carries a fresh triggerId. Keying on that
// id would start a new occurrence chain and fabricate a correlated incident, so
// the fingerprint must ignore it.
func TestSolarWindsWebhook_FingerprintIsStableAcrossFirings(t *testing.T) {
	first := swProcess(t, swMultiEntityFiring)
	refired := swProcess(t, strings.ReplaceAll(swMultiEntityFiring, `"t-a"`, `"t-a-second-instance"`))
	require.Len(t, first, 2)
	require.Len(t, refired, 2)

	assert.ElementsMatch(t,
		[]string{first[0].EventId, first[1].EventId},
		[]string{refired[0].EventId, refired[1].EventId},
		"a new trigger id must not change the fingerprint")
}

const swPodFiring = `{
  "type": "entity-metric-raw",
  "name": "K8s pod restarting repeatedly",
  "severity": "CRITICAL",
  "priority": "CRITICAL",
  "condition": "composite.k8s.pod.container.status.restarts.increase >= 3",
  "alertDefinitionId": "bbbbbbbb-0000-0000-0000-000000000002",
  "triggerId": "t-pod",
  "entities": [
    {"type":"KubernetesPod","displayName":"checkout-54d6f745c5-9j599","entityId":"e-300","triggerId":"t-pod",
     "values":[{"name":"composite.k8s.pod.container.status.restarts.increase","value":3}]}
  ]
}`

// k8s_workloads stores owning workloads, never pod instances, so a raw pod name
// matches nothing and the event keeps no namespace, cloud_resource_id or
// service_key. PodNameCandidates routes it through the pod→owner resolver.
func TestSolarWindsWebhook_PodEntityOffersPodNameCandidate(t *testing.T) {
	events := swProcess(t, swPodFiring)
	require.Len(t, events, 1)

	assert.Equal(t, "pod", events[0].EventSubjectKind)
	assert.Equal(t, []string{"checkout-54d6f745c5-9j599"}, events[0].PodNameCandidates)
	// A named entity is a legitimate lookup subject, so the opt-out must be absent.
	assert.NotContains(t, events[0].Investigation.Labels, "nb_skip_workload_match")
}

// This parser registers its own event_rules row named after the alert title. The
// derived fingerprint would otherwise register a second row named after a hash.
func TestSolarWindsWebhook_MarksAggregationKeyUnstable(t *testing.T) {
	for name, payload := range map[string]string{
		"entity alert": swPodFiring,
		"multi entity": swMultiEntityFiring,
	} {
		t.Run(name, func(t *testing.T) {
			for _, e := range swProcess(t, payload) {
				assert.Equal(t, "true", e.Investigation.Labels[event.LabelUnstableAggregationKey])
			}
		})
	}
}

// An entity-less payload is still well-formed; it must produce one event whose
// subject is the title and which opts out of the workload lookup.
func TestSolarWindsWebhook_EntitylessPayloadFallsBackToTitle(t *testing.T) {
	events := swProcess(t, `{
	  "type": "entity-metric-raw",
	  "name": "K8s cluster lost a node",
	  "severity": "CRITICAL",
	  "priority": "CRITICAL",
	  "condition": "k8s.cluster.nodes.ready < 17",
	  "alertDefinitionId": "cccccccc-0000-0000-0000-000000000003",
	  "triggerId": null,
	  "entities": []
	}`)

	require.Len(t, events, 1)
	assert.Equal(t, "K8s cluster lost a node", events[0].EventSubjectName)
	assert.Empty(t, events[0].EventSubjectKind)
	assert.NotContains(t, events[0].EventId, "\x00")
	assert.Equal(t, "true", events[0].Investigation.Labels["nb_skip_workload_match"])
}

func TestSolarWindsWebhook_StandaloneMetricKeepsItsOwnDescription(t *testing.T) {
	events := swProcess(t, `{
	  "type": "standalone-metric-raw",
	  "name": "Node CPU high",
	  "severity": "WARNING",
	  "priority": "WARNING",
	  "description": "Node CPU utilization above 85 percent for 10 minutes.",
	  "condition": "k8s.node.cpu.utilization > 85",
	  "alertDefinitionId": "dddddddd-0000-0000-0000-000000000004",
	  "triggerId": null,
	  "metrics": [{"name":"k8s.node.cpu.utilization","value":91.4,"threshold":85}]
	}`)

	require.Len(t, events, 1)
	assert.Equal(t, "Node CPU utilization above 85 percent for 10 minutes.", events[0].EventDescription)
	assert.Equal(t, string(event.EventPriorityMedium), events[0].EventPriority)
	assert.NotContains(t, events[0].EventId, "\x00")
	assert.Equal(t, "91.4", events[0].Investigation.Labels["metric_k8s.node.cpu.utilization"])
}

// Without a description SWO's own wording is unavailable, so the parser rebuilds
// one from the title and condition.
func TestSolarWindsWebhook_FallsBackToARebuiltDescription(t *testing.T) {
	events := swProcess(t, `{
	  "type": "entity-metric-raw",
	  "name": "K8s node NotReady",
	  "severity": "CRITICAL",
	  "priority": "CRITICAL",
	  "condition": "k8s.node.status.condition.ready < 1",
	  "alertDefinitionId": "eeeeeeee-0000-0000-0000-000000000005",
	  "triggerId": "t-nr",
	  "entities": [{"type":"KubernetesNode","displayName":"node-c","entityId":"e-400","triggerId":"t-nr"}]
	}`)

	require.Len(t, events, 1)
	assert.Equal(t,
		"SolarWinds alert 'K8s node NotReady': k8s.node.status.condition.ready < 1",
		events[0].EventDescription)
}

// Live deliveries carry fields this parser has no use for. They must unmarshal
// harmlessly rather than shift the fields it does read — this fixture mirrors a
// real multi-entity delivery field for field, including the duplicated top-level
// "id" and the per-entity relationships/entityTags/alertEvaluationId/entityUrl.
const swRealShapedFiring = `{
  "type": "entity-metric-raw",
  "name": "ZZ CPU check",
  "severity": "CRITICAL",
  "priority": "CRITICAL",
  "detailUrl": "https://example.invalid/alerts/settings/alert/ffffffff-0000-0000-0000-000000000006/overview",
  "runbook": null,
  "description": "Expected to fire immediately.",
  "condition": "k8s.node.cpu.utilization > 2",
  "alertDefinitionId": "ffffffff-0000-0000-0000-000000000006",
  "id": "ffffffff-0000-0000-0000-000000000006",
  "triggerId": null,
  "entities": [
    {"type":"KubernetesNode","displayName":"node-d","entityId":"e-500",
     "values":[{"name":"k8s.node.cpu.utilization","value":21.57}],
     "relationships":[],"timestamp":"2026-10-12T18:57:55Z","tags":null,"entityTags":[],
     "alertEvaluationId":113795280,"triggerId":"t-d",
     "activeAlertUrl":"https://example.invalid/active/d",
     "entityUrl":"https://example.invalid/entities/redirect/e-500"},
    {"type":"KubernetesNode","displayName":"node-e","entityId":"e-600",
     "values":[{"name":"k8s.node.cpu.utilization","value":17.16}],
     "relationships":[],"timestamp":"2026-10-12T18:57:55Z","tags":null,"entityTags":[],
     "alertEvaluationId":113795272,"triggerId":"t-e",
     "activeAlertUrl":"https://example.invalid/active/e",
     "entityUrl":"https://example.invalid/entities/redirect/e-600"}
  ]
}`

func TestSolarWindsWebhook_ToleratesTheFullLivePayloadShape(t *testing.T) {
	events := swProcess(t, swRealShapedFiring)
	require.Len(t, events, 2)

	bySubject := map[string]core.EventIncomingWebhook{}
	for _, e := range events {
		assert.NotContains(t, e.EventId, "\x00")
		assert.Equal(t, "Expected to fire immediately.", e.EventDescription)
		bySubject[e.EventSubjectName] = e
	}

	d, ok := bySubject["node-d"]
	require.True(t, ok)
	assert.Equal(t, "t-d", d.WebhookId)
	assert.Equal(t, "21.57", d.Investigation.Labels["metric_node-d_k8s.node.cpu.utilization"])
	// The per-entity active-alert link is more useful than the definition page.
	assert.Equal(t, "https://example.invalid/active/d", d.EventUrl)

	e, ok := bySubject["node-e"]
	require.True(t, ok)
	assert.Equal(t, "t-e", e.WebhookId)
	assert.NotEqual(t, d.EventId, e.EventId)

	// Evidence must name this entity's trigger id; the top-level one is absent here,
	// which would otherwise render as "TriggerID: , Condition: ...".
	require.Len(t, d.Investigation.Evidences, 1)
	require.Len(t, d.Investigation.Evidences[0].Insight, 1)
	assert.Contains(t, d.Investigation.Evidences[0].Insight[0].Message, "TriggerID: t-d")
}

func TestSolarWindsWebhook_RejectsUnrecognisedPayloads(t *testing.T) {
	for name, payload := range map[string]string{
		"unrelated json":      `{"hello":"world"}`,
		"swo test button":     `{"Message":"Test Notification, created date: 2026-10-12T19:18:08.766Z"}`,
		"known type, no name": `{"type":"entity-metric-raw","name":"","triggerId":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			events, err := SolarWindsWebhook{}.ProcessEventWebook(swTestContext(), nil, swTestAccount, payload)
			assert.ErrorIs(t, err, core.ErrEventNotSupported)
			assert.Empty(t, events)
		})
	}
}

// The alert's id in SolarWinds is the only handle Nudgebee ever gets on the rule:
// SolarWinds has no API that lists alerts, so an alert it has not seen fire cannot
// be looked up. Every delivery carries it, so every delivery must record it.
func TestSolarWindsWebhook_EventConfigCarriesTheUpstreamAlertId(t *testing.T) {
	var payload SolarWindsWebhookPayload
	require.NoError(t, json.Unmarshal([]byte(swMultiEntityFiring), &payload))

	cfg := buildSolarWindsEventConfig(payload, "K8s node memory saturated",
		"A node has averaged over 85% memory for 10 minutes.", "CRITICAL", swTestAccount)

	assert.Equal(t, "aaaaaaaa-0000-0000-0000-000000000001", cfg.ExternalRuleID)
	assert.Equal(t, IntegrationSolarWindsWebhook, cfg.Source)
	assert.Equal(t, "K8s node memory saturated", cfg.Alert)
	assert.Equal(t, "critical", cfg.Severity)
	assert.Equal(t, "k8s.node.memory.utilization > 85", cfg.Expr)
}

// A payload with no alertDefinitionId must leave the field empty rather than
// inventing one; upsertEventRule's COALESCE then keeps whatever is already stored.
func TestSolarWindsWebhook_EventConfigLeavesTheIdEmptyWhenAbsent(t *testing.T) {
	var payload SolarWindsWebhookPayload
	require.NoError(t, json.Unmarshal([]byte(swPodFiring), &payload))
	payload.AlertDefinitionID = ""

	cfg := buildSolarWindsEventConfig(payload, "K8s pod restarting repeatedly", "d", "CRITICAL", swTestAccount)
	assert.Empty(t, cfg.ExternalRuleID)
}

// Severity is lower-cased for the rule row, and an absent one must not produce a
// blank severity the rule tables cannot render.
func TestSolarWindsWebhook_EventConfigSeverityFallsBackToWarning(t *testing.T) {
	var payload SolarWindsWebhookPayload
	require.NoError(t, json.Unmarshal([]byte(swPodFiring), &payload))

	cfg := buildSolarWindsEventConfig(payload, "t", "d", "", swTestAccount)
	assert.Equal(t, "warning", cfg.Severity)
	assert.Equal(t, "warning", cfg.Labels.Severity)
}
