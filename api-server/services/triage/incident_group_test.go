package triage

import (
	"context"
	"fmt"
	"os"
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

func TestDecideSameSubjectAttach_NewGroupElectsEarliest(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	members := []groupCandidate{
		gc("crashloop", "KubePodCrashLooping", now.Add(-5*time.Minute)),
		gc("oom", "KubeContainerOOMKilled", now.Add(-8*time.Minute)),
	}

	leader, offset, ok := decideSameSubjectAttach(seed, members, nil, nil, nil, nil)
	require.True(t, ok)
	assert.Equal(t, "oom", leader, "earliest member leads")
	assert.Equal(t, 8*time.Minute, offset)
}

func TestDecideSameSubjectAttach_RefiresHoldGroupOpen(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	// The OOM chain started 80 minutes ago but re-fired 10 minutes ago: the
	// chain leader's start is outside the attach window, its last re-fire is
	// not. The re-fire keeps the group open; the link still goes to the chain
	// leader (only leaders carry links).
	oom := gc("oom", "KubeContainerOOMKilled", now.Add(-80*time.Minute))
	oom.LastSeen = now.Add(-10 * time.Minute)
	members := []groupCandidate{oom}

	leader, offset, ok := decideSameSubjectAttach(seed, members, nil, nil, nil, nil)
	require.True(t, ok, "a re-firing member holds the attach window open")
	assert.Equal(t, "oom", leader)
	assert.Equal(t, 80*time.Minute, offset, "offset measures from the leader's start, not the re-fire")

	// Same shape but the last re-fire is also stale: group closed.
	oom.LastSeen = now.Add(-IncidentAttachWindow - time.Minute)
	_, _, ok = decideSameSubjectAttach(seed, []groupCandidate{oom}, nil, nil, nil, nil)
	assert.False(t, ok)
}

func TestDecideSameSubjectAttach_AttachWindowExpired(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	members := []groupCandidate{
		gc("oom", "KubeContainerOOMKilled", now.Add(-IncidentAttachWindow-time.Minute)),
	}

	_, _, ok := decideSameSubjectAttach(seed, members, nil, nil, nil, nil)
	assert.False(t, ok, "a member quiet for longer than the attach window does not hold the group open")
}

func TestDecideSameSubjectAttach_FollowsLiveLeader(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	// crashloop is already a child of the OOM leader, which itself started 30
	// minutes ago (older than any current member, still inside the cap).
	members := []groupCandidate{
		gc("crashloop", "KubePodCrashLooping", now.Add(-5*time.Minute)),
	}
	edges := map[string]string{"crashloop": "oom-leader"}
	leaderStarts := map[string]time.Time{"oom-leader": now.Add(-30 * time.Minute)}

	leader, offset, ok := decideSameSubjectAttach(seed, members, edges, leaderStarts, nil, nil)
	require.True(t, ok)
	assert.Equal(t, "oom-leader", leader, "attach goes to the group's leader, never a child — the star stays one hop")
	assert.Equal(t, 30*time.Minute, offset)
}

// TestDecideSameSubjectAttach_OldLeaderStillLiveWhileMembersFire is the reported
// case, reduced.
//
// The two alerts on i-0dcee3621b8456783 opened their chains 19 hours apart on
// 4 and 5 September and were both still firing on the 8th. A group is live while
// its members are firing, not for a fixed period after its leader started — the
// previous rule closed a group 90 minutes after the leader's first event, which
// would reject every long-running incident, which is most of them.
func TestDecideSameSubjectAttach_OldLeaderStillLiveWhileMembersFire(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)

	// A member that fired 5 minutes ago, belonging to a group whose leader first
	// fired days back. The group is live: something in it is still firing.
	members := []groupCandidate{
		gc("child", "KubePodCrashLooping", now.Add(-5*time.Minute)),
	}
	edges := map[string]string{"child": "old-leader"}
	leaderStarts := map[string]time.Time{"old-leader": now.Add(-72 * time.Hour)}

	leader, _, ok := decideSameSubjectAttach(seed, members, edges, leaderStarts, nil, nil)
	require.True(t, ok, "a firing member keeps its group live however old the leader is")
	assert.Equal(t, "old-leader", leader, "the seed joins the existing group rather than starting a rival")

	// An unlinked member alongside it does not start a competing group: the
	// existing group still wins, so one incident keeps one leader.
	members = append(members, gc("fresh", "KubeContainerOOMKilled", now.Add(-3*time.Minute)))
	leader, _, ok = decideSameSubjectAttach(seed, members, edges, leaderStarts, nil, nil)
	require.True(t, ok)
	assert.Equal(t, "old-leader", leader, "an existing group anchors the incident as it grows")
}

// TestDecideSameSubjectAttach_ChronicJoinsButNeverLeads pins the membership rule
// that replaced the old "chronic never groups" one.
//
// Gating membership on the firing rate was measured against the Rackspace tenant
// and left the reported machines ungrouped: payment (17 and 11 firings/week) and
// inventory (15 and 11) have no non-chronic alert at all, so nothing could ever
// found a group on them, and order qualified only because one counter sat at 9
// against a threshold of 10. Whether an incident was visible came down to a noise
// counter versus an arbitrary constant. Membership is now decided by what is
// firing; the rate only decides who leads.
func TestDecideSameSubjectAttach_ChronicJoinsButNeverLeads(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	chronic := map[string]bool{"FlappingLatency": true}

	// A chronic member alone still forms a group and leads it: the incident is
	// real, it just ranks low. This is the case that used to return no group.
	members := []groupCandidate{
		gc("flap", "FlappingLatency", now.Add(-2*time.Minute)),
	}
	leader, _, ok := decideSameSubjectAttach(seed, members, nil, nil, chronic, nil)
	require.True(t, ok, "a chronic pair must still form a group")
	assert.Equal(t, "flap", leader)

	// With a non-chronic member present, the non-chronic one leads even though
	// the chronic one started earlier — a flapper never becomes the headline.
	members = append(members, gc("oom", "KubeContainerOOMKilled", now.Add(-1*time.Minute)))
	leader, _, ok = decideSameSubjectAttach(seed, members, nil, nil, chronic, nil)
	require.True(t, ok)
	assert.Equal(t, "oom", leader, "non-chronic outranks chronic regardless of start order")

	// A chronic firing now DOES hold the group open, because membership is
	// liveness-based: something is still firing on this subject.
	members = []groupCandidate{
		gc("flap", "FlappingLatency", now.Add(-2*time.Minute)),
		gc("oom", "KubeContainerOOMKilled", now.Add(-IncidentAttachWindow-5*time.Minute)),
	}
	leader, _, ok = decideSameSubjectAttach(seed, members, nil, nil, chronic, nil)
	require.True(t, ok, "a recent chronic firing keeps the subject live")
	assert.Equal(t, "oom", leader, "the stale non-chronic member still outranks the chronic one")
}

func TestDecideSameSubjectAttach_DeterministicTieBreak(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	ts := now.Add(-4 * time.Minute)
	members := []groupCandidate{
		gc("bbb", "KubePodCrashLooping", ts),
		gc("aaa", "KubeContainerOOMKilled", ts),
	}

	leader, _, ok := decideSameSubjectAttach(seed, members, nil, nil, nil, nil)
	require.True(t, ok)
	assert.Equal(t, "aaa", leader, "equal starts break by ID so concurrent processors agree")
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
	assert.Contains(t, reason, "calls edge")

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

// TestPoolConnectedMembers_GroupsTheWholeConnectedSet is the a-b-c case, built
// from the topology actually stored on the Rackspace scenario-lab account:
// payment and inventory both call order, and all three call database.
//
// The rule this replaces joined the single most recently active neighbour, so
// whether three connected alerting services ended up in one incident was luck.
// It also followed one hop only, which is not enough here: payment reaches
// database directly, but reaches inventory only through order.
func TestPoolConnectedMembers_GroupsTheWholeConnectedSet(t *testing.T) {
	const (
		order     = "aws:ComputeInstance:order"
		payment   = "aws:ComputeInstance:payment"
		inventory = "aws:ComputeInstance:inventory"
		database  = "aws:ComputeInstance:database"
		unrelated = "aws:ComputeInstance:billing"
	)
	graph := &DependencyGraph{
		Nodes: map[string]*ServiceNode{
			order: {}, payment: {}, inventory: {}, database: {}, unrelated: {},
		},
		Edges: map[string][]string{
			payment:   {order, database},
			inventory: {order, database},
			order:     {database},
		},
		ReverseEdges: map[string][]string{
			order:    {payment, inventory},
			database: {payment, inventory, order},
		},
	}
	now := time.Date(2026, 9, 8, 5, 56, 0, 0, time.UTC)
	cand := func(id, subject, svc string) connectedCandidate {
		return connectedCandidate{
			candidate:  gc(id, "svc-down", now.Add(-2*time.Minute)),
			subjectKey: "amazonec2|" + subject,
			serviceKey: svc,
			subject:    subject,
		}
	}
	cands := []connectedCandidate{
		cand("payment-chain", "i-payment", payment),
		cand("inventory-chain", "i-inventory", inventory),
		cand("database-chain", "i-database", database),
		cand("unrelated-chain", "i-billing", unrelated),
	}

	members, hops, subjects, capped, _ := poolConnectedMembers(graph, order, "amazonec2|i-order", cands)

	assert.False(t, capped)
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	assert.ElementsMatch(t, []string{"payment-chain", "inventory-chain", "database-chain"}, ids,
		"every connected alerting service joins, not just the most recent one")
	assert.Len(t, subjects, 3)
	// The subject each key maps to is carried through, not parsed back out of the
	// key: SubjectKey is not reversible (its datastore form is "db|ns|series"),
	// and these values are what the chronic rate query matches on.
	assert.ElementsMatch(t, []string{"i-payment", "i-inventory", "i-database"},
		[]string{subjects["amazonec2|i-payment"], subjects["amazonec2|i-inventory"], subjects["amazonec2|i-database"]})
	assert.NotContains(t, ids, "unrelated-chain", "a service with no path to the seed stays out")
	for _, id := range ids {
		assert.LessOrEqual(t, hops[id], maxIncidentHops)
	}
}

// TestPoolConnectedMembers_StopsAtTheSubjectCap proves the cap stops adding
// rather than truncating a group that already exists, so one runaway fan-out
// cannot turn an incident into an estate-wide blob.
func TestPoolConnectedMembers_StopsAtTheSubjectCap(t *testing.T) {
	const seedSvc = "aws:ComputeInstance:hub"
	graph := &DependencyGraph{
		Nodes: map[string]*ServiceNode{seedSvc: {}},
		Edges: map[string][]string{seedSvc: {}},
	}
	now := time.Date(2026, 9, 8, 5, 56, 0, 0, time.UTC)
	cands := make([]connectedCandidate, 0, incidentGroupSubjectCap+5)
	for i := 0; i < incidentGroupSubjectCap+5; i++ {
		svc := fmt.Sprintf("aws:ComputeInstance:n%d", i)
		graph.Nodes[svc] = &ServiceNode{}
		graph.Edges[seedSvc] = append(graph.Edges[seedSvc], svc)
		cands = append(cands, connectedCandidate{
			candidate:  gc(fmt.Sprintf("chain-%d", i), "svc-down", now.Add(-time.Minute)),
			subjectKey: fmt.Sprintf("amazonec2|i-%d", i),
			serviceKey: svc,
			subject:    fmt.Sprintf("i-%d", i),
		})
	}

	_, _, subjects, capped, _ := poolConnectedMembers(graph, seedSvc, "amazonec2|i-seed", cands)

	assert.True(t, capped, "the cap must be reported, not applied silently")
	assert.Len(t, subjects, incidentGroupSubjectCap)
}

// TestDecideSameSubjectAttach_CauseLeadsOverEarliest is the reason the election
// takes a dependency signal at all.
//
// The ordinary shape of an outage puts the symptom first in time: a load
// balancer reports 5xx the moment its backend stops answering, while the health
// check that notices the backend is down needs a couple of evaluation periods to
// agree. Ranked on start time the 5xx becomes the headline and "the service is
// down" is filed underneath it — the group is right and the story is backwards.
//
// Measured on the case this came from: alb-5xx opened 11:38, the backend alarm
// 11:51, and the balancer depends on the backend.
func TestDecideSameSubjectAttach_CauseLeadsOverEarliest(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "unhealthy-hosts", now)
	alb := gc("alb-5xx", "alb-5xx", now.Add(-22*time.Minute))
	backend := gc("backend-down", "service-down", now.Add(-9*time.Minute))

	// The balancer depends on the backend, so one member depends on "backend-down".
	dependedOnBy := map[string]int{"backend-down": 1}

	leader, _, ok := decideSameSubjectAttach(seed, []groupCandidate{alb, backend}, nil, nil, nil, dependedOnBy)
	require.True(t, ok)
	assert.Equal(t, "backend-down", leader,
		"the member others depend on leads, even though the load balancer alarmed 13 minutes earlier")
}

// With nothing structural to separate members — the common same-subject case,
// where every alert is on one machine and the graph says nothing — the election
// must fall back to exactly what it did before.
func TestDecideSameSubjectAttach_TimingStillBreaksStructuralTies(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	members := []groupCandidate{
		gc("crashloop", "KubePodCrashLooping", now.Add(-5*time.Minute)),
		gc("oom", "KubeContainerOOMKilled", now.Add(-8*time.Minute)),
	}

	for _, tt := range []struct {
		name         string
		dependedOnBy map[string]int
	}{
		{"no signal at all", nil},
		{"every member scores the same", map[string]int{"crashloop": 2, "oom": 2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			leader, _, ok := decideSameSubjectAttach(seed, members, nil, nil, nil, tt.dependedOnBy)
			require.True(t, ok)
			assert.Equal(t, "oom", leader, "earliest member still leads when nothing depends on anything")
		})
	}
}

// A flapper must not take the headline just because things point at it — the
// chronic check runs before the causal one, and that order is the point.
func TestDecideSameSubjectAttach_ChronicOutranksCause(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "unhealthy-hosts", now)
	noisy := gc("noisy", "flapping-alert", now.Add(-30*time.Minute))
	quiet := gc("quiet", "real-alert", now.Add(-2*time.Minute))

	leader, _, ok := decideSameSubjectAttach(
		seed,
		[]groupCandidate{noisy, quiet},
		nil, nil,
		map[string]bool{"flapping-alert": true},
		map[string]int{"noisy": 5}, // everything depends on it, and it still must not lead
	)
	require.True(t, ok)
	assert.Equal(t, "quiet", leader, "a chronic member never leads, however central it looks")
}

// TestCandidatesAreAlertsNotChains documents the shape the window query now
// returns. A group is the alerts firing together in a window; anchoring links on
// firings rather than on each chain's first event is what gives an incident a
// beginning and an end.
//
// Before, a link pointed at the event an alert FIRST ever produced, so a group
// never ended: any member firing held it open while the alert that named it
// could have stopped days earlier. Measured on the Rackspace tenant over 14
// days — 1,337 of 1,445 links (93%) named a headline quiet for an average of
// 38.8 hours, one group had 17 members, one headline was 158 days old, and none
// of it was visible in a time-scoped view because the events predated the window.
func TestCandidatesAreAlertsNotChains(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	// Two firings of one alert inside the window are one candidate, and the
	// candidate carries the LATEST firing — the incident is about what is
	// happening now, not when the alert first appeared.
	seed := gc("seed-firing", "alb-5xx", now)
	backend := gc("backend-firing-latest", "service-down", now.Add(-2*time.Minute))
	backend.LastSeen = now.Add(-1 * time.Minute)

	leader, offset, ok := decideSameSubjectAttach(
		seed, []groupCandidate{backend}, nil, nil, nil, map[string]int{"backend-firing-latest": 1})
	require.True(t, ok)
	assert.Equal(t, "backend-firing-latest", leader,
		"the headline is a firing from this window, elected on who others depend on")
	assert.Equal(t, 2*time.Minute, offset)
}

// A burst that has gone quiet must not hold a group open: once its firings fall
// outside the window they are not candidates, so the next burst forms its own
// incident rather than inheriting an old headline.
func TestQuietBurstDoesNotHoldTheGroupOpen(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	seed := gc("new-firing", "alb-5xx", now)
	stale := gc("old-firing", "service-down", now.Add(-2*IncidentAttachWindow))
	stale.LastSeen = now.Add(-2 * IncidentAttachWindow)

	_, _, ok := decideSameSubjectAttach(seed, []groupCandidate{stale}, nil, nil, nil, nil)
	assert.False(t, ok, "a member last seen two windows ago must not keep an incident open")
}
