package triage

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"

	"github.com/jmoiron/sqlx"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gc(id, aggKey string, startsAt time.Time) groupCandidate {
	return groupCandidate{ID: id, AggregationKey: aggKey, StartsAt: startsAt}
}

func TestAttachSameSubjectIncident_DerivedSignalsNeverGroup(t *testing.T) {
	// SLO violations and anomaly detections are statistical echoes, not
	// concrete failures — they neither lead nor join groups (mirrors the
	// assembly's isDerivedSignal rule). attachSameSubjectIncident must bail
	// before touching the DB, so a nil db is the proof.
	slo := &models.Event{
		Id:               "slo-1",
		Tenant:           strPtr("t"),
		CloudAccountId:   strPtr("a"),
		AggregationKey:   strPtr("SLOViolation"),
		FindingType:      strPtr("SLO"),
		SubjectNamespace: strPtr("ns"),
		SubjectOwner:     strPtr("web"),
		StartsAt:         &time.Time{},
	}
	attached, err := attachSameSubjectIncident(context.Background(), nil, slo)
	assert.NoError(t, err)
	assert.False(t, attached)

	slo.FindingType = strPtr("Anomaly")
	attached, err = attachSameSubjectIncident(context.Background(), nil, slo)
	assert.NoError(t, err)
	assert.False(t, attached)
}

func TestIncidentGroupingEnabled(t *testing.T) {
	// On by default; only an explicit false/0 kills it.
	t.Setenv(incidentGroupingEnvFlag, "")
	assert.True(t, incidentGroupingEnabled())
	t.Setenv(incidentGroupingEnvFlag, "true")
	assert.True(t, incidentGroupingEnabled())
	t.Setenv(incidentGroupingEnvFlag, "false")
	assert.False(t, incidentGroupingEnabled())
	t.Setenv(incidentGroupingEnvFlag, "0")
	assert.False(t, incidentGroupingEnabled())
}

func TestEventAlertIdentity_NilSafe(t *testing.T) {
	id := eventAlertIdentity(&models.Event{Id: "x"})
	assert.Equal(t, "x", id.ID)
	assert.Empty(t, id.SubjectName)
	assert.Empty(t, id.SubjectOwner)
	assert.True(t, len(SubjectKey(id)) > 0 && SubjectKey(id)[len(SubjectKey(id))-1] == '|',
		"no subject identity yields a trailing-| key, which attach rejects")
}

// TestAttachSameSubjectIncident_E2E replays the OOM-pod story (OOMKilled ->
// CrashLoopBackOff -> NotReady on one workload) through the real SQL inside an
// always-rolled-back transaction (the TestLoadChronicStats_E2E pattern) and
// asserts the star shape: both later alerts link straight to the OOM leader.
func TestAttachSameSubjectIncident_E2E(t *testing.T) {
	if os.Getenv("TEST_LIVE_CORRELATION") != "1" {
		t.Skip("set TEST_LIVE_CORRELATION=1 to run (requires APP_DATABASE_URL + TEST_ACCOUNT_ID)")
	}

	env := testenv.RequireEnv(t, "TEST_ACCOUNT_ID")
	account := env["TEST_ACCOUNT_ID"]
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set APP_DATABASE_URL to run")
	}
	dbConn, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer func() { _ = dbConn.Close() }()
	ctx := context.Background()

	var tenant string
	require.NoError(t, dbConn.GetContext(ctx, &tenant,
		`SELECT tenant::text FROM cloud_accounts WHERE id = $1`, account))

	tx, err := dbConn.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	const ns = "ns-e2e-incident-group"
	anchor := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)

	mkEvent := func(aggKey string, startsAt time.Time) *models.Event {
		id := uuid.NewString()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, tenant, cloud_account_id, aggregation_key,
				subject_namespace, subject_name, subject_owner, fingerprint, finding_id,
				finding_type, priority, cluster, starts_at, created_at, source, title, evidences)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $10, 'issue', 'HIGH', 'e2e-cluster', $9, $9, 'kubernetes_api_server', $4, '[]'::jsonb)`,
			id, tenant, account, aggKey, ns, "checkout-7d9f8b6c5d-x2vk4", "checkout", "fp-"+id, startsAt, "fid-"+id)
		require.NoError(t, err)
		return &models.Event{
			Id:               id,
			Tenant:           &tenant,
			CloudAccountId:   &account,
			AggregationKey:   strPtr(aggKey),
			SubjectNamespace: strPtr(ns),
			SubjectName:      strPtr("checkout-7d9f8b6c5d-x2vk4"),
			SubjectOwner:     strPtr("checkout"),
			Fingerprint:      strPtr("fp-" + id),
			StartsAt:         &startsAt,
		}
	}

	leaderLinkOf := func(eventID string) (string, bool) {
		var leader string
		err := tx.GetContext(ctx, &leader, `
			SELECT related_event_id FROM event_correlations
			WHERE event_id = $1 AND correlation_type = $2`, eventID, SameIncidentCorrelationType)
		if err != nil {
			return "", false
		}
		return leader, true
	}

	oom := mkEvent("KubeContainerOOMKilled", anchor)
	oomChild, err := attachSameSubjectIncident(ctx, tx, oom)
	require.NoError(t, err)
	assert.False(t, oomChild, "first alert on the subject is an implicit group of one — not a child")
	_, linked := leaderLinkOf(oom.Id)
	assert.False(t, linked, "first alert on the subject is an implicit group of one — no link row")

	crash := mkEvent("KubePodCrashLooping", anchor.Add(3*time.Minute))
	crashChild, err := attachSameSubjectIncident(ctx, tx, crash)
	require.NoError(t, err)
	assert.True(t, crashChild, "attached under the leader — scored as a child")
	leader, linked := leaderLinkOf(crash.Id)
	require.True(t, linked)
	assert.Equal(t, oom.Id, leader)

	notReady := mkEvent("KubePodNotReady", anchor.Add(6*time.Minute))
	notReadyChild, err := attachSameSubjectIncident(ctx, tx, notReady)
	require.NoError(t, err)
	assert.True(t, notReadyChild, "attached under the leader — scored as a child")
	leader, linked = leaderLinkOf(notReady.Id)
	require.True(t, linked)
	assert.Equal(t, oom.Id, leader, "third alert links to the leader, not to the second alert — star, not chain")

	// A late alert after the attach window opens a new group instead.
	late := mkEvent("KubeDeploymentReplicasMismatch", anchor.Add(6*time.Minute).Add(IncidentAttachWindow+time.Minute))
	lateChild, err := attachSameSubjectIncident(ctx, tx, late)
	require.NoError(t, err)
	assert.False(t, lateChild, "past the attach window it opens its own group — not a child")
	_, linked = leaderLinkOf(late.Id)
	assert.False(t, linked, "quiet gap past the attach window ends the group")
}

// TestAttachTopologyIncident_E2E replays the cross-service story through the
// real SQL inside an always-rolled-back transaction: checkout's alert opens a
// group; payments' alert carries a stored service map with a CALLS edge to
// checkout and must join checkout's group with a topology reason.
func TestAttachTopologyIncident_E2E(t *testing.T) {
	if os.Getenv("TEST_LIVE_CORRELATION") != "1" {
		t.Skip("set TEST_LIVE_CORRELATION=1 to run (requires APP_DATABASE_URL + TEST_ACCOUNT_ID)")
	}

	env := testenv.RequireEnv(t, "TEST_ACCOUNT_ID")
	account := env["TEST_ACCOUNT_ID"]
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set APP_DATABASE_URL to run")
	}
	dbConn, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer func() { _ = dbConn.Close() }()
	ctx := context.Background()

	var tenant string
	require.NoError(t, dbConn.GetContext(ctx, &tenant,
		`SELECT tenant::text FROM cloud_accounts WHERE id = $1`, account))

	tx, err := dbConn.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	const ns = "ns-e2e-topology"
	anchor := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)

	kgEvidence := `[{"type": "knowledge_graph", "nodes": [
	  {"id": "n1", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "checkout", "namespace": "` + ns + `"}},
	  {"id": "n2", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "payments", "namespace": "` + ns + `"}}
	], "edges": [
	  {"relationship_type": "CALLS", "source_node_id": "n2", "dest_node_id": "n1"}
	]}]`

	mkEvent := func(owner, aggKey string, startsAt time.Time, evidenceJSON string) *models.Event {
		id := uuid.NewString()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, tenant, cloud_account_id, aggregation_key,
				subject_namespace, subject_name, subject_owner, fingerprint, finding_id,
				finding_type, priority, cluster, starts_at, created_at, source, title, evidences)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $11, 'issue', 'HIGH', 'e2e-cluster', $9, $9, 'kubernetes_api_server', $4, coalesce(nullif($10,'')::jsonb, '[]'::jsonb))`,
			id, tenant, account, aggKey, ns, owner+"-abc12", owner, "fp-"+id, startsAt, evidenceJSON, "fid-"+id)
		require.NoError(t, err)
		ev := &models.Event{
			Id:               id,
			Tenant:           &tenant,
			CloudAccountId:   &account,
			AggregationKey:   strPtr(aggKey),
			SubjectNamespace: strPtr(ns),
			SubjectName:      strPtr(owner + "-abc12"),
			SubjectOwner:     strPtr(owner),
			Fingerprint:      strPtr("fp-" + id),
			StartsAt:         &startsAt,
		}
		if evidenceJSON != "" {
			var j models.Json
			require.NoError(t, j.Scan([]uint8(evidenceJSON)))
			ev.Evidences = &j
		}
		return ev
	}

	// checkout opens its group (lone leader — no link row yet).
	checkoutErr := mkEvent("checkout", "HighErrorRate", anchor, "")
	checkoutErrChild, err := attachSameSubjectIncident(ctx, tx, checkoutErr)
	require.NoError(t, err)
	assert.False(t, checkoutErrChild, "first alert opens the group — not a child")

	// payments alerts 4 minutes later; its stored map says payments CALLS
	// checkout — it must join checkout's group via topology.
	paymentsErr := mkEvent("payments", "HighLatency", anchor.Add(4*time.Minute), kgEvidence)
	paymentsErrChild, err := attachSameSubjectIncident(ctx, tx, paymentsErr)
	require.NoError(t, err)
	assert.True(t, paymentsErrChild, "joined checkout's group by topology — scored as a child")

	var leader, reason string
	err = tx.QueryRowContext(ctx, `
		SELECT related_event_id, correlation_reason FROM event_correlations
		WHERE event_id = $1 AND correlation_type = $2`, paymentsErr.Id, SameIncidentCorrelationType).
		Scan(&leader, &reason)
	require.NoError(t, err, "payments must have topology-attached")
	assert.Equal(t, checkoutErr.Id, leader)
	// The reason has said "N hop(s) away" since the connected-set pools were
	// merged; "calls edge" was the wording of the single-hop rule that replaced.
	// The assertion kept passing review because this E2E only runs with
	// TEST_LIVE_CORRELATION=1.
	assert.Contains(t, reason, "hop(s) away")

	// A later checkout alert still resolves the same star (transitivity via
	// the edge-following leader resolution).
	checkoutCrash := mkEvent("checkout", "CrashLoopBackOff", anchor.Add(6*time.Minute), "")
	checkoutCrashChild, err := attachSameSubjectIncident(ctx, tx, checkoutCrash)
	require.NoError(t, err)
	assert.True(t, checkoutCrashChild, "same-subject attach — scored as a child")
	err = tx.QueryRowContext(ctx, `
		SELECT related_event_id FROM event_correlations
		WHERE event_id = $1 AND correlation_type = $2`, checkoutCrash.Id, SameIncidentCorrelationType).
		Scan(&leader)
	require.NoError(t, err)
	assert.Equal(t, checkoutErr.Id, leader, "same-subject attach joins the existing cross-service star")

	// No stored map on the seed and no same-subject group: no attach.
	//
	// The unlocated co-timing path is switched off for this assertion because it
	// would otherwise answer it: inventory has no topology position, checkout's
	// group is the only one open, and they share a namespace — which is exactly
	// when attachUnlocatedIncident attaches. That path has its own test; this one
	// is about topology evidence, so it must not depend on co-timing either way.
	t.Setenv(unlocatedGroupingEnvFlag, "false")
	lonely := mkEvent("inventory", "DiskPressure", anchor.Add(5*time.Minute), "")
	lonelyChild, err := attachSameSubjectIncident(ctx, tx, lonely)
	require.NoError(t, err)
	assert.False(t, lonelyChild, "no edge to the group — not a child")
	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT count(*) FROM event_correlations
		WHERE event_id = $1 AND correlation_type = $2`, lonely.Id, SameIncidentCorrelationType).Scan(&n))
	assert.Equal(t, 0, n, "no map, no same-subject members — stays lone")
}

// TestAttachUnlocatedIncident_Scoped_E2E pins the one thing that keeps the
// co-timing path from merging an account: an alert the graph cannot place joins
// an open incident only inside its own scope — its namespace for a Kubernetes
// alert, AWS as a whole for an AWS alarm.
//
// Without the namespace bound this path was the single largest producer of
// wrong links — measured on prod over 7 days, 11,633 of its 15,632 links crossed
// a namespace, and one ImagePullBackOff group had collected 1,854 members across
// 29 namespaces because a chronic leader kept it open indefinitely.
func TestAttachUnlocatedIncident_Scoped_E2E(t *testing.T) {
	if os.Getenv("TEST_LIVE_CORRELATION") != "1" {
		t.Skip("set TEST_LIVE_CORRELATION=1 to run (requires APP_DATABASE_URL + TEST_ACCOUNT_ID)")
	}

	env := testenv.RequireEnv(t, "TEST_ACCOUNT_ID")
	account := env["TEST_ACCOUNT_ID"]
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set APP_DATABASE_URL to run")
	}
	dbConn, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer func() { _ = dbConn.Close() }()
	ctx := context.Background()

	var tenant string
	require.NoError(t, dbConn.GetContext(ctx, &tenant,
		`SELECT tenant::text FROM cloud_accounts WHERE id = $1`, account))

	tx, err := dbConn.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	const incidentNS = "ns-e2e-unlocated-incident"
	const otherNS = "ns-e2e-unlocated-other"
	anchor := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)

	// No evidences on any of these: every seed here reaches the unlocated path,
	// which is the path under test.
	mkSourced := func(source, ns, owner, aggKey string, startsAt time.Time) *models.Event {
		id := uuid.NewString()
		_, err := tx.ExecContext(ctx, `
			INSERT INTO events (id, tenant, cloud_account_id, aggregation_key,
				subject_namespace, subject_name, subject_owner, fingerprint, finding_id,
				finding_type, priority, cluster, starts_at, created_at, source, title, evidences)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $10, 'issue', 'HIGH', 'e2e-cluster', $9, $9, $11, $4, '[]'::jsonb)`,
			id, tenant, account, aggKey, ns, owner+"-abc12", owner, "fp-"+id, startsAt, "fid-"+id, source)
		require.NoError(t, err)
		return &models.Event{
			Id:               id,
			Tenant:           &tenant,
			CloudAccountId:   &account,
			Source:           strPtr(source),
			AggregationKey:   strPtr(aggKey),
			SubjectNamespace: strPtr(ns),
			SubjectName:      strPtr(owner + "-abc12"),
			SubjectOwner:     strPtr(owner),
			Fingerprint:      strPtr("fp-" + id),
			StartsAt:         &startsAt,
		}
	}
	mkEvent := func(ns, owner, aggKey string, startsAt time.Time) *models.Event {
		return mkSourced("kubernetes_api_server", ns, owner, aggKey, startsAt)
	}
	leaderLinkOf := func(eventID string) (string, bool) {
		var leader string
		if err := tx.GetContext(ctx, &leader, `
			SELECT related_event_id FROM event_correlations
			WHERE event_id = $1 AND correlation_type = $2`, eventID, SameIncidentCorrelationType); err != nil {
			return "", false
		}
		return leader, true
	}

	// An incident is open in incidentNS: two alerts on one subject, so the group
	// exists and its leader lives in that namespace.
	oom := mkEvent(incidentNS, "checkout", "KubeContainerOOMKilled", anchor)
	_, err = attachSameSubjectIncident(ctx, tx, oom)
	require.NoError(t, err)
	crash := mkEvent(incidentNS, "checkout", "KubePodCrashLooping", anchor.Add(2*time.Minute))
	crashChild, err := attachSameSubjectIncident(ctx, tx, crash)
	require.NoError(t, err)
	require.True(t, crashChild, "precondition: an incident is open in incidentNS")

	// An unplaceable alert in ANOTHER namespace must not join it. This is the
	// regression: co-timing alone used to be enough.
	foreign := mkEvent(otherNS, "billing", "DiskPressure", anchor.Add(4*time.Minute))
	foreignChild, err := attachSameSubjectIncident(ctx, tx, foreign)
	require.NoError(t, err)
	assert.False(t, foreignChild, "another namespace's incident is not this alert's incident")
	_, linked := leaderLinkOf(foreign.Id)
	assert.False(t, linked, "no link row across namespaces on co-timing alone")

	// The same alert inside the incident's own namespace still joins — the gate
	// narrows this path, it does not disable it.
	local := mkEvent(incidentNS, "inventory", "DiskPressure", anchor.Add(5*time.Minute))
	localChild, err := attachSameSubjectIncident(ctx, tx, local)
	require.NoError(t, err)
	assert.True(t, localChild, "same namespace, the one incident that began with it — still attaches")
	leader, linked := leaderLinkOf(local.Id)
	require.True(t, linked)
	assert.Equal(t, oom.Id, leader, "joins the open incident's leader")

	// AWS: an incident led by an EC2 alarm is open in the same window as the
	// Kubernetes one above.
	ec2CPU := mkSourced("AWS_CloudWatch_Alarm", "AmazonEC2", "i-0e2e0000000000001", "ec2-cpu-high", anchor.Add(time.Minute))
	_, err = attachSameSubjectIncident(ctx, tx, ec2CPU)
	require.NoError(t, err)
	ec2Status := mkSourced("AWS_CloudWatch_Alarm", "AmazonEC2", "i-0e2e0000000000001", "ec2-status-check", anchor.Add(3*time.Minute))
	ec2StatusChild, err := attachSameSubjectIncident(ctx, tx, ec2Status)
	require.NoError(t, err)
	require.True(t, ec2StatusChild, "precondition: an AWS incident is open")

	// An ELB alarm the graph cannot place joins it across AWS services on timing
	// — and is not confused by the Kubernetes incident open at the same time.
	elb := mkSourced("AWS_EventBridge", "AWSELB", "app/e2e-lb/1a2b3c", "alb-target-5xx", anchor.Add(6*time.Minute))
	elbChild, err := attachSameSubjectIncident(ctx, tx, elb)
	require.NoError(t, err)
	assert.True(t, elbChild, "AWS alarms group across AWS services on timing")
	leader, linked = leaderLinkOf(elb.Id)
	require.True(t, linked)
	assert.Equal(t, ec2CPU.Id, leader, "joins the AWS incident, not the Kubernetes one")

	// A Kubernetes alert never lands in the AWS incident, even in a namespace
	// that has nothing else open.
	stray := mkEvent("ns-e2e-unlocated-quiet", "cart", "DiskPressure", anchor.Add(7*time.Minute))
	strayChild, err := attachSameSubjectIncident(ctx, tx, stray)
	require.NoError(t, err)
	assert.False(t, strayChild, "the AWS incident is not a Kubernetes alert's incident")

	// Co-timing is the fallback for alerts the graph cannot place. The two seeds
	// below sit in incidentNS, where exactly one incident is open, so the old rule
	// would have attached both.
	mkWith := func(owner, aggKey, findingType, evidence string, startsAt time.Time) *models.Event {
		ev := mkEvent(incidentNS, owner, aggKey, startsAt)
		_, err := tx.ExecContext(ctx,
			`UPDATE events SET finding_type = $2, evidences = $3::jsonb WHERE id = $1`,
			ev.Id, findingType, evidence)
		require.NoError(t, err)
		ev.FindingType = strPtr(findingType)
		var j models.Json
		require.NoError(t, j.Scan([]uint8(evidence)))
		ev.Evidences = &j
		return ev
	}

	// Placed by its own stored map, with nothing connected alerting: no group.
	placedMap := `[{"type": "knowledge_graph", "nodes": [
	  {"id": "n1", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "search", "namespace": "` + incidentNS + `"}},
	  {"id": "n2", "node_type": "Workload", "properties": {"kind": "Deployment", "name": "search-db", "namespace": "` + incidentNS + `"}}
	], "edges": [
	  {"relationship_type": "CALLS", "source_node_id": "n1", "dest_node_id": "n2"}
	]}]`
	placed := mkWith("search", "HighLatency", "issue", placedMap, anchor.Add(8*time.Minute))
	placed.SubjectType = strPtr("deployment")
	placed.SubjectOwnerKind = strPtr("Deployment")
	placedChild, err := attachSameSubjectIncident(ctx, tx, placed)
	require.NoError(t, err)
	assert.False(t, placedChild, "the graph placed it and nothing connected is alerting — timing must not group it")

	// A deploy event carries no map but is a Kubernetes object the graph knows.
	deploy := mkWith("relay", "ConfigurationChange/deployment", "configuration_change", "[]", anchor.Add(9*time.Minute))
	deployChild, err := attachSameSubjectIncident(ctx, tx, deploy)
	require.NoError(t, err)
	assert.False(t, deployChild, "a configuration change never groups on timing alone")

	// Timing is evidence about how an incident BEGAN. The cases below are hours
	// apart: the AWS scope is the whole account, so two of them inside one window
	// would be each other's "second incident".
	const aws = "AWS_CloudWatch_Alarm"
	attach := func(ev *models.Event) bool {
		t.Helper()
		child, err := attachSameSubjectIncident(ctx, tx, ev)
		require.NoError(t, err)
		return child
	}
	// incident opens one: two alarms on one host, a minute apart, both stored
	// before either is triaged — the way one poll delivers them. Triaged one at a
	// time, the first would be an alarm on its own and would itself join an
	// incident that began a minute earlier.
	incident := func(host string, at time.Time) *models.Event {
		t.Helper()
		head := mkSourced(aws, "AmazonEC2", host, host+"-cpu-high", at)
		second := mkSourced(aws, "AmazonEC2", host, host+"-status-check", at.Add(time.Minute))
		attach(head)
		require.True(t, attach(second), "precondition: an incident is open on %s", host)
		return head
	}
	lone := func(name string, at time.Time) *models.Event {
		return mkSourced(aws, "AWSELB", name, name+"-5xx", at)
	}

	// An alarm that started two minutes into a cloud incident joins it; one that
	// started six minutes in does not — although the first joiner fired four
	// minutes before it, which used to keep the incident open for it.
	t1 := anchor.Add(2 * time.Hour)
	headA := incident("i-0e2e00000000000a1", t1)
	early := lone("app/e2e-early/1", t1.Add(2*time.Minute))
	require.True(t, attach(early), "started two minutes after the incident began")
	leader, linked = leaderLinkOf(early.Id)
	require.True(t, linked)
	assert.Equal(t, headA.Id, leader)
	late := lone("app/e2e-late/1", t1.Add(6*time.Minute))
	assert.False(t, attach(late), "started six minutes after the incident began: not part of how it began")
	_, linked = leaderLinkOf(late.Id)
	assert.False(t, linked, "no link row for an alarm that did not start with the incident")

	// Nothing is placed through an alert that is there on timing. A second alarm
	// on the early joiner's own resource is judged on its own start: a minute
	// later it started with the incident too and joins; twelve minutes in it did
	// not, though it shares a subject with an alert that is in the group.
	sibling := mkSourced(aws, "AWSELB", "app/e2e-early/1", "app/e2e-early/1-latency", t1.Add(3*time.Minute))
	require.True(t, attach(sibling), "same resource as the early joiner, and itself three minutes into the incident")
	leader, linked = leaderLinkOf(sibling.Id)
	require.True(t, linked)
	assert.Equal(t, headA.Id, leader)
	lateSibling := mkSourced(aws, "AWSELB", "app/e2e-early/1", "app/e2e-early/1-rejected", t1.Add(12*time.Minute))
	assert.False(t, attach(lateSibling), "same resource as the early joiner, but twelve minutes into the incident")
	_, linked = leaderLinkOf(lateSibling.Id)
	assert.False(t, linked)

	// The alarm that started FIRST and is triaged last joins too: which of them
	// is triaged first must not decide it.
	t2 := anchor.Add(4 * time.Hour)
	before := lone("app/e2e-before/1", t2)
	headB := incident("i-0e2e00000000000b1", t2.Add(4*time.Minute))
	require.True(t, attach(before), "started four minutes before the incident's first alert")
	leader, linked = leaderLinkOf(before.Id)
	require.True(t, linked)
	assert.Equal(t, headB.Id, leader)

	// An incident that began twenty minutes ago and is still collecting its own
	// host's alarms neither takes a new alarm nor stops it joining the incident
	// that has just begun. It used to do the second: two open incidents, decline.
	t3 := anchor.Add(6 * time.Hour)
	headC := mkSourced(aws, "AmazonEC2", "i-0e2e00000000000c1", "c1-cpu-high", t3)
	attach(headC)
	require.True(t, attach(mkSourced(aws, "AmazonEC2", "i-0e2e00000000000c1", "c1-status-check", t3.Add(10*time.Minute))))
	require.True(t, attach(mkSourced(aws, "AmazonEC2", "i-0e2e00000000000c1", "c1-disk-low", t3.Add(20*time.Minute))),
		"precondition: the old incident has a member from a minute ago")
	headD := incident("i-0e2e00000000000d1", t3.Add(19*time.Minute))
	fresh := lone("app/e2e-fresh/1", t3.Add(21*time.Minute))
	require.True(t, attach(fresh), "one incident began with it; the older one is not a candidate")
	leader, linked = leaderLinkOf(fresh.Id)
	require.True(t, linked)
	assert.Equal(t, headD.Id, leader, "joins the incident that began with it, not the old one")
	assert.NotEqual(t, headC.Id, leader)

	// Two incidents began with it: timing cannot say which.
	t4 := anchor.Add(8 * time.Hour)
	incident("i-0e2e00000000000e1", t4)
	incident("i-0e2e00000000000f1", t4.Add(time.Minute))
	torn := lone("app/e2e-torn/1", t4.Add(3*time.Minute))
	assert.False(t, attach(torn), "two incidents began in the window — decline")

	// A link made on timing places that firing only. The same alarm firing again
	// ten minutes later — same fingerprint, new start, as a cloud alarm's next
	// firing is — did not start with the incident, and must not follow its
	// earlier firing in while the host's own alarms keep the incident alive.
	t5 := anchor.Add(10 * time.Hour)
	incident("i-0e2e0000000000a11", t5)
	repeater := func(at time.Time) *models.Event {
		ev := lone("app/e2e-repeater/1", at)
		_, err := tx.ExecContext(ctx, `UPDATE events SET fingerprint = $2 WHERE id = $1`, ev.Id, "fp-e2e-repeater")
		require.NoError(t, err)
		ev.Fingerprint = strPtr("fp-e2e-repeater")
		return ev
	}
	first := repeater(t5.Add(2 * time.Minute))
	require.True(t, attach(first), "precondition: the first firing joined on timing")
	// A second stored row of that SAME firing — a re-delivery — is the same
	// firing, and goes where it went, on the same evidence: its link is a timing
	// link too, so it cannot place the next firing either.
	copyOfFirst := repeater(t5.Add(2 * time.Minute))
	assert.True(t, attach(copyOfFirst), "another row of the firing that joined follows it")
	var copyReason string
	require.NoError(t, tx.GetContext(ctx, &copyReason, `
		SELECT correlation_reason FROM event_correlations
		WHERE event_id = $1 AND correlation_type = $2`, copyOfFirst.Id, SameIncidentCorrelationType))
	assert.True(t, strings.HasPrefix(copyReason, timingOnlyReasonPrefix), "the copy's link says timing, as the firing's does: %q", copyReason)
	assert.Contains(t, copyReason, "within 5 minutes")
	require.True(t, attach(mkSourced(aws, "AmazonEC2", "i-0e2e0000000000a11", "a11-disk-low", t5.Add(10*time.Minute))),
		"precondition: the host's own alarms keep the incident alive")
	second := repeater(t5.Add(12 * time.Minute))
	assert.False(t, attach(second), "a later firing does not follow a firing that was filed on timing")
	_, linked = leaderLinkOf(second.Id)
	assert.False(t, linked)

	// Kubernetes keeps the fifteen-minute window, measured from the incident's
	// first alert rather than from its latest member.
	const anchoredNS = "ns-e2e-unlocated-anchored"
	t6 := anchor.Add(12 * time.Hour)
	headK := mkEvent(anchoredNS, "checkout", "KubeContainerOOMKilled", t6)
	attach(headK)
	require.True(t, attach(mkEvent(anchoredNS, "checkout", "KubePodCrashLooping", t6.Add(2*time.Minute))))
	require.True(t, attach(mkEvent(anchoredNS, "checkout", "KubePodNotReady", t6.Add(12*time.Minute))),
		"precondition: the incident has a member from a few minutes ago")
	inside := mkEvent(anchoredNS, "inventory", "DiskPressure", t6.Add(14*time.Minute))
	require.True(t, attach(inside), "fourteen minutes after the incident began: inside the namespace window")
	leader, linked = leaderLinkOf(inside.Id)
	require.True(t, linked)
	assert.Equal(t, headK.Id, leader)
	outside := mkEvent(anchoredNS, "billing", "DiskPressure", t6.Add(16*time.Minute))
	assert.False(t, attach(outside), "sixteen minutes after it began — a member four minutes ago does not renew it")
}

func TestGroupLinks_PlacingFirings(t *testing.T) {
	links := groupLinks{
		parent: map[string]string{"by-subject": "head", "by-timing": "head"},
		leads:  map[string]bool{"head": true},
		timing: map[string]bool{"by-subject": false, "by-timing": true},
	}
	assert.True(t, links.filedOnTiming([]string{"unfiled", "by-timing", "by-subject"}), "the row that places the alert is the first filed one")
	assert.False(t, links.filedOnTiming([]string{"by-subject", "by-timing"}))
	assert.False(t, links.filedOnTiming([]string{"unfiled"}), "an alert in no group is not there on timing")
	assert.False(t, links.filedOnTiming([]string{"head"}), "nor is a head")
	assert.Equal(t, []string{"unfiled", "by-subject"}, links.placingFirings([]string{"unfiled", "by-timing", "by-subject"}),
		"a firing filed on timing alone does not place the alert's next firing; order is kept")
	assert.Equal(t, []string{"a", "b"}, groupLinks{}.placingFirings([]string{"a", "b"}),
		"nothing filed on timing: every firing counts")
}

func TestSubjectKey_OwnerHashStripped(t *testing.T) {
	// Collectors disagree on the owner form for one workload: some report the
	// Deployment ("postgres"), some the ReplicaSet ("postgres-78d9cffd68").
	// Both must key to the same subject or same-incident attach misses
	// (observed live: a Pods-Restarting alert 6 minutes inside an open window
	// stayed unlinked because its owner carried the RS name).
	deployment := AlertIdentity{SubjectNamespace: "namespace-104a", SubjectOwner: "postgres"}
	replicaSet := AlertIdentity{SubjectNamespace: "namespace-104a", SubjectOwner: "postgres-78d9cffd68"}
	assert.Equal(t, SubjectKey(deployment), SubjectKey(replicaSet))
}
