package triage

import (
	"context"
	"nudgebee/services/internal/database"
	"os"
	"strconv"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEvent(id string, startsAt time.Time, opts ...func(*models.Event)) *models.Event {
	e := &models.Event{
		Id:       id,
		StartsAt: &startsAt,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func withNamespace(ns string) func(*models.Event) {
	return func(e *models.Event) { e.SubjectNamespace = strPtr(ns) }
}

func withServiceKey(sk string) func(*models.Event) {
	return func(e *models.Event) { e.ServiceKey = strPtr(sk) }
}

func withCloudResourceId(id string) func(*models.Event) {
	return func(e *models.Event) { e.CloudResourceId = strPtr(id) }
}

func withSubjectName(name string) func(*models.Event) {
	return func(e *models.Event) { e.SubjectName = strPtr(name) }
}

func withSubjectOwner(owner, kind string) func(*models.Event) {
	return func(e *models.Event) {
		e.SubjectOwner = strPtr(owner)
		e.SubjectOwnerKind = strPtr(kind)
	}
}

func TestGetServiceKeyFromEvent_UsesDBServiceKey(t *testing.T) {
	// When service_key is set in DB, it should be used directly
	e := makeEvent("1", time.Now(), withServiceKey("arn:aws:rds:us-east-1:123:db:main"))
	assert.Equal(t, "arn:aws:rds:us-east-1:123:db:main", getServiceKeyFromEvent(e))
}

func TestGetServiceKeyFromEvent_FallsBackToK8sFields(t *testing.T) {
	// When service_key is empty, fall back to K8s fields
	e := makeEvent("1", time.Now(),
		withNamespace("nudgebee"),
		withSubjectOwner("services-server", "Deployment"),
	)
	assert.Equal(t, "nudgebee:Deployment:services-server", getServiceKeyFromEvent(e))
}

func TestGetServiceKeyFromEvent_EmptyServiceKeyFallsBackToSubjectName(t *testing.T) {
	// PagerDuty/anomaly events: no service_key, no subject_owner
	e := makeEvent("1", time.Now(),
		withServiceKey(""),
		withNamespace("nudgebee"),
		withSubjectName("hasura"),
	)
	assert.Equal(t, "nudgebee:Deployment:hasura", getServiceKeyFromEvent(e))
}

func TestGetServiceKeyFromEvent_EmptyReturnsEmpty(t *testing.T) {
	// No service_key, no subject fields -> empty
	e := makeEvent("1", time.Now())
	assert.Equal(t, "", getServiceKeyFromEvent(e))
}

// TestGetServiceKeyFromEvent_EmptyStringPointers pins production behaviour.
// sqlx scans an empty TEXT column into a non-nil *string pointing at "", so
// pagerduty / anomaly / slo events arrive at the correlation engine with
// `SubjectOwner`, `SubjectOwnerKind`, and often `ServiceKey` as non-nil
// pointers to empty strings. The previous implementation used `!= nil` only,
// which caused:
//   - `kind` default "Deployment" silently overwritten with ""
//   - `SubjectOwner == ""` satisfies the outer `if`, blocking the
//     `SubjectName` fallback in the inner `else if`
// → fallback returned "" for every pagerduty event, which in turn short-
// circuited the dependency-scoring block in calculateCorrelationScore
// (`if service1Key != "" && service2Key != ""`). No ServiceMap correlations
// fired for any pagerduty-sourced demo event (verified on live data,
// account a2a30b02-0f67-42e5-a2ab-c658230fd798).
func TestGetServiceKeyFromEvent_EmptyStringPointers(t *testing.T) {
	emptyStr := ""
	demoNs := "demo"
	adName := "ad"
	now := time.Now()

	e := &models.Event{
		Id:               "1",
		StartsAt:         &now,
		ServiceKey:       &emptyStr, // non-nil ptr to ""
		SubjectNamespace: &demoNs,
		SubjectOwner:     &emptyStr, // non-nil ptr to "" — the production trap
		SubjectOwnerKind: &emptyStr, // non-nil ptr to "" — the production trap
		SubjectName:      &adName,
	}

	got := getServiceKeyFromEvent(e)
	assert.Equal(t, "demo:Deployment:ad", got,
		"empty-string SubjectOwner must not block the SubjectName fallback; "+
			"empty-string SubjectOwnerKind must not overwrite the Deployment default")
}

// TestGetServiceKeyFromEvent_EmptyStringOwnerOnly guards the edge case where
// SubjectOwner is the empty-string trap but SubjectOwnerKind is properly
// populated — the fallback must still reach SubjectName.
func TestGetServiceKeyFromEvent_EmptyStringOwnerOnly(t *testing.T) {
	emptyStr := ""
	ns := "demo"
	kind := "StatefulSet"
	name := "cart"
	now := time.Now()

	e := &models.Event{
		Id:               "1",
		StartsAt:         &now,
		SubjectNamespace: &ns,
		SubjectOwner:     &emptyStr,
		SubjectOwnerKind: &kind,
		SubjectName:      &name,
	}

	assert.Equal(t, "demo:StatefulSet:cart", getServiceKeyFromEvent(e))
}

func TestCorrelation_SameCloudResource_DifferentAlerts(t *testing.T) {
	// GCP: test-pg-high-cpu and test-pg-slow-queries on same Cloud SQL instance, 3 min apart
	// This is the case that was MISSED before (score 0.40 < 0.50 threshold)
	now := time.Now()
	e1 := makeEvent("1", now,
		withServiceKey("arn:gcp:cloud sql:us-central:nudgebee-dev:cloudsql_database:beehive-test-pg"),
		withCloudResourceId("777ea88f-f060-47d0-94d6-ea36fab34db6"),
		withNamespace("cloudsql_database"),
	)
	e2 := makeEvent("2", now.Add(3*time.Minute),
		withServiceKey("arn:gcp:cloud sql:us-central:nudgebee-dev:cloudsql_database:beehive-test-pg"),
		withCloudResourceId("777ea88f-f060-47d0-94d6-ea36fab34db6"),
		withNamespace("cloudsql_database"),
	)

	result := calculateCorrelationScore(e1, e2, nil, nil)

	assert.True(t, result.IsCorrelated, "Events on same cloud resource should correlate")
	// Score: time(0.15) + cloud_resource(0.25) + namespace(0.10) + service(0.15) = 0.65
	assert.InDelta(t, 0.65, result.CorrelationScore, 0.01)
	assert.Equal(t, "same_resource", result.CorrelationType)
}

func TestCorrelation_SameCloudResource_DifferentServiceKeys(t *testing.T) {
	// AWS: Same RDS instance but different service_key formats from EventBridge vs CloudWatch
	now := time.Now()
	e1 := makeEvent("1", now,
		withServiceKey("arn:aws:rds:us-east-1:740395098545:db-instance:main"),
		withCloudResourceId("64d4de6c-e42d-46c5-83ba-a75fc47082d0"),
		withNamespace("AmazonRDS"),
	)
	e2 := makeEvent("2", now.Add(1*time.Minute),
		withServiceKey("arn:aws:rds:us-east-1:740395098545::main"),
		withCloudResourceId("64d4de6c-e42d-46c5-83ba-a75fc47082d0"),
		withNamespace("AmazonRDS"),
	)

	result := calculateCorrelationScore(e1, e2, nil, nil)

	assert.True(t, result.IsCorrelated, "Events with same cloud_resource_id should correlate even with different service_keys")
	// Score: time(0.30) + cloud_resource(0.25) + namespace(0.10) = 0.65
	// (no same_service because service_keys differ)
	assert.InDelta(t, 0.65, result.CorrelationScore, 0.01)
	assert.Equal(t, "same_resource", result.CorrelationType)
}

func TestCorrelation_SameServiceKey_NoCloudResource(t *testing.T) {
	// K8s: Same service, within 2 minutes — same as before
	now := time.Now()
	e1 := makeEvent("1", now,
		withServiceKey("nudgebee/Deployment/services-server"),
		withNamespace("nudgebee"),
	)
	e2 := makeEvent("2", now.Add(1*time.Minute),
		withServiceKey("nudgebee/Deployment/services-server"),
		withNamespace("nudgebee"),
	)

	result := calculateCorrelationScore(e1, e2, nil, nil)

	assert.True(t, result.IsCorrelated)
	// Score: time(0.30) + namespace(0.10) + service(0.15) = 0.55
	assert.InDelta(t, 0.55, result.CorrelationScore, 0.01)
	assert.Equal(t, "same_service", result.CorrelationType)
}

func TestCorrelation_DifferentResource_DifferentService_BelowThreshold(t *testing.T) {
	// Two unrelated cloud events in same account, 5 min apart — should NOT correlate
	now := time.Now()
	e1 := makeEvent("1", now,
		withServiceKey("arn:aws:ec2:us-east-1:123::i-abc"),
		withCloudResourceId("resource-1"),
		withNamespace("AmazonEC2"),
	)
	e2 := makeEvent("2", now.Add(4*time.Minute),
		withServiceKey("arn:aws:rds:us-east-1:123:db:main"),
		withCloudResourceId("resource-2"),
		withNamespace("AmazonRDS"),
	)

	result := calculateCorrelationScore(e1, e2, nil, nil)

	// Score: time(0.15) only = 0.15 — well below threshold
	assert.False(t, result.IsCorrelated, "Unrelated events should not correlate")
	assert.Less(t, result.CorrelationScore, CorrelationScoreThreshold)
}

func TestCorrelation_TooFarApart(t *testing.T) {
	now := time.Now()
	e1 := makeEvent("1", now,
		withServiceKey("arn:gcp:cloud sql:test:beehive-test-pg"),
		withCloudResourceId("same-resource"),
	)
	e2 := makeEvent("2", now.Add(15*time.Minute),
		withServiceKey("arn:gcp:cloud sql:test:beehive-test-pg"),
		withCloudResourceId("same-resource"),
	)

	result := calculateCorrelationScore(e1, e2, nil, nil)

	assert.False(t, result.IsCorrelated, "Events >10min apart should never correlate")
}

func TestCorrelation_SelfCorrelation(t *testing.T) {
	now := time.Now()
	e := makeEvent("1", now, withServiceKey("arn:aws:rds:main"))

	result := calculateCorrelationScore(e, e, nil, nil)

	assert.False(t, result.IsCorrelated)
}

func TestCorrelation_WithServiceMap(t *testing.T) {
	// K8s events with service map — existing behavior should be preserved
	now := time.Now()
	e1 := makeEvent("1", now,
		withServiceKey("nudgebee:Deployment:frontend"),
		withNamespace("nudgebee"),
	)
	e2 := makeEvent("2", now.Add(1*time.Minute),
		withServiceKey("nudgebee:Deployment:backend"),
		withNamespace("nudgebee"),
	)

	// Build a simple service map: frontend -> backend
	graph := &DependencyGraph{
		Nodes:        make(map[string]*ServiceNode),
		Edges:        map[string][]string{"nudgebee:Deployment:frontend": {"nudgebee:Deployment:backend"}},
		ReverseEdges: map[string][]string{"nudgebee:Deployment:backend": {"nudgebee:Deployment:frontend"}},
	}

	result := calculateCorrelationScore(e1, e2, graph, nil)

	assert.True(t, result.IsCorrelated)
	// Score: time(0.30) + dependency(0.40, direct) + causality(0.15) + namespace(0.10) = 0.95
	assert.InDelta(t, 0.95, result.CorrelationScore, 0.01)
	assert.Equal(t, "likely_root_cause", result.CorrelationType)
}

// TestCorrelation_TraceCoParticipant_NoServiceMap pins the core use case:
// two events that share a failing trace but have no ServiceMap/knowledge_graph
// attached (e.g. a flagd configuration_change event paired with a downstream
// alert). The trace-anchored fallback fires because the triaged event's trace
// lists flagd as a participant.
func TestCorrelation_TraceCoParticipant_NoServiceMap(t *testing.T) {
	now := time.Now()
	flagdChange := makeEvent("flagd-1", now,
		withServiceKey("demo/Deployment/flagd"),
		withNamespace("demo"),
		withSubjectName("flagd"),
	)
	fraudAlert := makeEvent("fraud-1", now.Add(1*time.Minute),
		withNamespace("demo"),
		withSubjectName("fraud-detection"),
	)
	// Triaged event (fraudAlert) has trace evidence naming flagd as a participant.
	traceServices := map[string]bool{
		"fraud-detection": true,
		"flagd":           true,
		"cart":            true,
	}

	result := calculateCorrelationScore(flagdChange, fraudAlert, nil, traceServices)

	assert.True(t, result.IsCorrelated)
	assert.Equal(t, 1, result.DependencyDistance)
	// Score: time(0.30, <=2min) + trace co-participant(0.40) + causality(0.15) + namespace(0.10) = 0.95
	assert.InDelta(t, 0.95, result.CorrelationScore, 0.01)
	// likely_root_cause upgrade triggers (upstream_dependency + dep=1 + e1<e2 + score>0.80)
	assert.Equal(t, "likely_root_cause", result.CorrelationType)
	assert.Contains(t, result.CorrelationReason, "trace co-participant")
}

// TestCorrelation_TraceCoParticipant_DoesNotOverrideGraph guards against the
// trace signal double-counting when ServiceMap already found a direct
// dependency. The fallback must only fire when ServiceMap returned no path.
func TestCorrelation_TraceCoParticipant_DoesNotOverrideGraph(t *testing.T) {
	now := time.Now()
	e1 := makeEvent("1", now,
		withServiceKey("nudgebee:Deployment:frontend"),
		withNamespace("nudgebee"),
		withSubjectName("frontend"),
	)
	e2 := makeEvent("2", now.Add(1*time.Minute),
		withServiceKey("nudgebee:Deployment:backend"),
		withNamespace("nudgebee"),
		withSubjectName("backend"),
	)
	graph := &DependencyGraph{
		Nodes:        make(map[string]*ServiceNode),
		Edges:        map[string][]string{"nudgebee:Deployment:frontend": {"nudgebee:Deployment:backend"}},
		ReverseEdges: map[string][]string{"nudgebee:Deployment:backend": {"nudgebee:Deployment:frontend"}},
	}
	traceServices := map[string]bool{"frontend": true, "backend": true}

	result := calculateCorrelationScore(e1, e2, graph, traceServices)

	// ServiceMap already awarded 0.40 (direct dep) + 0.15 (causal). Trace
	// signal must NOT add another 0.40+0.15 on top. Score stays at 0.95, not 1.50.
	assert.True(t, result.IsCorrelated)
	assert.InDelta(t, 0.95, result.CorrelationScore, 0.01)
	assert.NotContains(t, result.CorrelationReason, "trace co-participant")
}

// TestCorrelation_TraceCoParticipant_OnlyFiresWhenNameMatches guards against
// false positives. traceServices is extracted from the triaged event (event2)
// by the caller, so the triaged event's own service is naturally in the set.
// The check must be on event1 (the candidate) — otherwise every candidate in
// the window would appear as a co-participant. This test pairs a candidate
// whose subject is NOT in the trace with a triaged event whose subject IS in
// the trace, and asserts no trace bonus fires.
func TestCorrelation_TraceCoParticipant_OnlyFiresWhenNameMatches(t *testing.T) {
	now := time.Now()
	// e1 is the candidate. Its subject is NOT in the trace set.
	e1 := makeEvent("1", now,
		withNamespace("demo"),
		withSubjectName("unrelated-service"),
	)
	// e2 is the triaged event. Its subject IS in the set (trivially — own trace),
	// which is exactly the tautological case that would false-positive if the
	// check were on event2.
	e2 := makeEvent("2", now.Add(1*time.Minute),
		withNamespace("demo"),
		withSubjectName("fraud-detection"),
	)
	traceServices := map[string]bool{
		"fraud-detection": true,
		"cart":            true,
	}

	result := calculateCorrelationScore(e1, e2, nil, traceServices)

	// e1.SubjectName="unrelated-service" not in traceServices — no bonus.
	// Score: time(0.30) + namespace(0.10) = 0.40 → below 0.50 threshold.
	assert.False(t, result.IsCorrelated)
	assert.NotContains(t, result.CorrelationReason, "trace co-participant")
}

// TestExtractTraceServiceSet verifies the parser correctly reads stringified
// span-array evidence and collects participating service names. Builds a real
// evidence JSON matching the trace auto-action's output shape.
func TestExtractTraceServiceSet(t *testing.T) {
	// Build the stringified span payload that lives inside evidence.data.
	// Shape: { "data": [ {span...}, ... ] }
	spansJSON := `{"data":[
		{"service_name":"checkout","span_kind":"SPAN_KIND_CLIENT"},
		{"service_name":"cart","span_kind":"SPAN_KIND_SERVER"},
		{"workload_name":"flagd","span_kind":"SPAN_KIND_SERVER"},
		{"service_name":"checkout"},
		{"span_kind":"SPAN_KIND_INTERNAL"}
	]}`
	evidencesJSON := `[
		{"type":"diff"},
		{
			"type":"json",
			"format":"json",
			"metadata":{"query":{"mode":"error_plus_expansion","workload_name":"checkout"}},
			"data":` + strconv.Quote(spansJSON) + `
		}
	]`

	var ev models.Json
	require.NoError(t, ev.Scan([]byte(evidencesJSON)))
	event := &models.Event{Id: "e1", Evidences: &ev}

	got := extractTraceServiceSet(event)
	require.NotNil(t, got)
	assert.True(t, got["checkout"], "service_name=checkout must be collected")
	assert.True(t, got["cart"], "service_name=cart must be collected")
	assert.True(t, got["flagd"], "workload_name=flagd (fallback) must be collected")
	assert.Len(t, got, 3, "distinct service names only; duplicates / spans without a name are dropped")
}

// TestExtractTraceServiceSet_NoTraceEvidence returns nil when the event's
// evidences contain no trace entry (only config-diff, or empty). This is the
// common case for configuration_change events.
func TestExtractTraceServiceSet_NoTraceEvidence(t *testing.T) {
	evidencesJSON := `[{"type":"diff"}]`
	var ev models.Json
	require.NoError(t, ev.Scan([]byte(evidencesJSON)))
	event := &models.Event{Id: "e1", Evidences: &ev}

	assert.Nil(t, extractTraceServiceSet(event))
}

// TestExtractTraceServiceSet_IgnoresNonTraceQueryModes rejects evidence entries
// whose metadata.query.mode is set to something other than the trace
// auto-action's known modes.
func TestExtractTraceServiceSet_IgnoresNonTraceQueryModes(t *testing.T) {
	evidencesJSON := `[
		{
			"type":"json",
			"metadata":{"query":{"mode":"some_other_mode"}},
			"data":"{\"data\":[{\"service_name\":\"x\"}]}"
		}
	]`
	var ev models.Json
	require.NoError(t, ev.Scan([]byte(evidencesJSON)))
	event := &models.Event{Id: "e1", Evidences: &ev}

	assert.Nil(t, extractTraceServiceSet(event))
}

// TestCorrelation_TraceCoParticipant_Live pulls a real checkout error event
// from the local dev DB (it carries rich trace evidence with the full request
// path) and confirms that the trace-anchored fallback correctly identifies
// participating services and would correlate with downstream events whose
// subject appears in that trace — without needing a ServiceMap.
//
// Call-argument convention (matches processor.go):
//
//	calculateCorrelationScore(triaged, candidate, serviceMap, traceServices)
//
// The triaged event is the one running correlation; traceServices is extracted
// from its own evidence. Candidate's subject is checked against that set.
//
// Gated on TEST_LIVE_CORRELATION=1 so CI doesn't require a DB. Run with:
//
//	cd api-server/services
//	TEST_LIVE_CORRELATION=1 go test -v -run TestCorrelation_TraceCoParticipant_Live ./triage/
func TestCorrelation_TraceCoParticipant_Live(t *testing.T) {
	if os.Getenv("TEST_LIVE_CORRELATION") != "1" {
		t.Skip("set TEST_LIVE_CORRELATION=1 to run (requires local Metastore connection)")
	}

	// Event IDs from the dev account (a2a30b02-0f67-42e5-a2ab-c658230fd798).
	// The checkout event fired during a cartFailure scenario and carries
	// error_plus_expansion trace evidence — Phase 1 found 4 error spans and
	// the expansion surfaces the full request path (checkout → cart → flagd
	// → ...). The flagd event is a diff-only configuration_change.
	const (
		checkoutAlertID = "f6792f8c-73b9-456f-9a7f-c7486041f4f5"
		flagdChangeID   = "8e1318a1-53be-422e-9c4a-6dbce492e4b6"
	)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "failed to connect to Metastore — check .env")

	loadEvent := func(id string) *models.Event {
		var ev models.Event
		query := `SELECT id, title, finding_type, source,
			subject_type, subject_name, subject_namespace, subject_owner, subject_owner_kind,
			service_key, cloud_resource_id, fingerprint, starts_at, ends_at, status,
			cloud_account_id, tenant, created_at, evidences
			FROM events WHERE id = $1`
		require.NoError(t, dbms.Db.GetContext(context.Background(), &ev, query, id),
			"failed to load event %s", id)
		return &ev
	}

	checkout := loadEvent(checkoutAlertID)
	flagd := loadEvent(flagdChangeID)

	t.Logf("checkout event (triaged): subject=%s ns=%s starts_at=%v",
		*checkout.SubjectName, *checkout.SubjectNamespace, checkout.StartsAt)
	t.Logf("flagd event:              subject=%s ns=%s starts_at=%v finding_type=%s",
		*flagd.SubjectName, *flagd.SubjectNamespace, flagd.StartsAt, *flagd.FindingType)

	// 1. Extract the trace service set from the checkout event's real evidence.
	traceSvcs := extractTraceServiceSet(checkout)
	require.NotNil(t, traceSvcs, "checkout event should have trace evidence")
	t.Logf("checkout trace service set (%d): %v", len(traceSvcs), traceSvcs)
	assert.Contains(t, traceSvcs, "flagd",
		"checkout's error-expansion trace should include flagd as a participant")

	// 2. Confirm flagd's event has NO trace evidence (diff-only).
	assert.Nil(t, extractTraceServiceSet(flagd),
		"flagd configuration_change should carry only diff evidence, no trace")

	// 3. Simulate the correlation that would fire if flagd (candidate from
	//    past window) and checkout (triaged, has trace evidence) were being
	//    correlated. Original flagd event is ~8min after checkout — shift it
	//    to 2min before checkout to match the realistic "flag changed, then
	//    cart started failing" scenario.
	shifted := checkout.StartsAt.Add(-2 * time.Minute)
	candidate := *flagd
	candidate.StartsAt = &shifted

	// Argument order matches processor.go after the fix: (candidate, triaged).

	// Pre-fix verification: without trace signal, score is time+namespace only.
	beforeFix := calculateCorrelationScore(&candidate, checkout, nil, nil)
	t.Logf("WITHOUT trace signal:  correlated=%v score=%.2f type=%s",
		beforeFix.IsCorrelated, beforeFix.CorrelationScore, beforeFix.CorrelationType)

	// With the trace signal, flagd (candidate) appears in checkout's (triaged)
	// trace set → correlate.
	withTrace := calculateCorrelationScore(&candidate, checkout, nil, traceSvcs)
	t.Logf("WITH trace signal:     correlated=%v score=%.2f type=%s reason=%s",
		withTrace.IsCorrelated, withTrace.CorrelationScore, withTrace.CorrelationType, withTrace.CorrelationReason)

	assert.True(t, withTrace.IsCorrelated,
		"trace co-participant signal should correlate flagd→checkout at distance 1")
	assert.Equal(t, 1, withTrace.DependencyDistance)
	assert.Contains(t, withTrace.CorrelationReason, "trace co-participant")
	assert.Greater(t, withTrace.CorrelationScore, 0.50)
}
