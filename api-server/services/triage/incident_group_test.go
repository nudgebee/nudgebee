package triage

import (
	"context"
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

	leader, offset, ok := decideSameSubjectAttach(seed, members, nil, nil, nil)
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

	leader, offset, ok := decideSameSubjectAttach(seed, members, nil, nil, nil)
	require.True(t, ok, "a re-firing member holds the attach window open")
	assert.Equal(t, "oom", leader)
	assert.Equal(t, 80*time.Minute, offset, "offset measures from the leader's start, not the re-fire")

	// Same shape but the last re-fire is also stale: group closed.
	oom.LastSeen = now.Add(-IncidentAttachWindow - time.Minute)
	_, _, ok = decideSameSubjectAttach(seed, []groupCandidate{oom}, nil, nil, nil)
	assert.False(t, ok)
}

func TestDecideSameSubjectAttach_AttachWindowExpired(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	members := []groupCandidate{
		gc("oom", "KubeContainerOOMKilled", now.Add(-IncidentAttachWindow-time.Minute)),
	}

	_, _, ok := decideSameSubjectAttach(seed, members, nil, nil, nil)
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

	leader, offset, ok := decideSameSubjectAttach(seed, members, edges, leaderStarts, nil)
	require.True(t, ok)
	assert.Equal(t, "oom-leader", leader, "attach goes to the group's leader, never a child — the star stays one hop")
	assert.Equal(t, 30*time.Minute, offset)
}

func TestDecideSameSubjectAttach_CappedGroupDoesNotAbsorb(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	// The only recent activity is a child of a leader that started beyond the
	// absorption cap: that group is over, so the seed does not attach.
	members := []groupCandidate{
		gc("child", "KubePodCrashLooping", now.Add(-5*time.Minute)),
	}
	edges := map[string]string{"child": "stale-leader"}
	leaderStarts := map[string]time.Time{"stale-leader": now.Add(-IncidentAbsorptionCap - time.Minute)}

	_, _, ok := decideSameSubjectAttach(seed, members, edges, leaderStarts, nil)
	assert.False(t, ok)

	// A fresh unlinked member alongside the capped group founds a new one.
	members = append(members, gc("fresh", "KubeContainerOOMKilled", now.Add(-3*time.Minute)))
	leader, _, ok := decideSameSubjectAttach(seed, members, edges, leaderStarts, nil)
	require.True(t, ok)
	assert.Equal(t, "fresh", leader)
}

func TestDecideSameSubjectAttach_ChronicNeverLeadsNorExtends(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	chronic := map[string]bool{"FlappingLatency": true}

	// A chronic flapper is the only member: no group.
	members := []groupCandidate{
		gc("flap", "FlappingLatency", now.Add(-2*time.Minute)),
	}
	_, _, ok := decideSameSubjectAttach(seed, members, nil, nil, chronic)
	assert.False(t, ok, "a chronic pair must not found a group")

	// Chronic member is earlier than a real one: the real one leads.
	members = append(members, gc("oom", "KubeContainerOOMKilled", now.Add(-1*time.Minute)))
	leader, _, ok := decideSameSubjectAttach(seed, members, nil, nil, chronic)
	require.True(t, ok)
	assert.Equal(t, "oom", leader)

	// A recent chronic firing must not re-arm the timer for a stale real member.
	members = []groupCandidate{
		gc("flap", "FlappingLatency", now.Add(-2*time.Minute)),
		gc("oom", "KubeContainerOOMKilled", now.Add(-IncidentAttachWindow-5*time.Minute)),
	}
	_, _, ok = decideSameSubjectAttach(seed, members, nil, nil, chronic)
	assert.False(t, ok, "chronic firings do not hold a group open")
}

func TestDecideSameSubjectAttach_DeterministicTieBreak(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	seed := gc("seed", "KubePodNotReady", now)
	ts := now.Add(-4 * time.Minute)
	members := []groupCandidate{
		gc("bbb", "KubePodCrashLooping", ts),
		gc("aaa", "KubeContainerOOMKilled", ts),
	}

	leader, _, ok := decideSameSubjectAttach(seed, members, nil, nil, nil)
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
	assert.NoError(t, attachSameSubjectIncident(context.Background(), nil, slo))

	slo.FindingType = strPtr("Anomaly")
	assert.NoError(t, attachSameSubjectIncident(context.Background(), nil, slo))
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
	require.NoError(t, attachSameSubjectIncident(ctx, tx, oom))
	_, linked := leaderLinkOf(oom.Id)
	assert.False(t, linked, "first alert on the subject is an implicit group of one — no link row")

	crash := mkEvent("KubePodCrashLooping", anchor.Add(3*time.Minute))
	require.NoError(t, attachSameSubjectIncident(ctx, tx, crash))
	leader, linked := leaderLinkOf(crash.Id)
	require.True(t, linked)
	assert.Equal(t, oom.Id, leader)

	notReady := mkEvent("KubePodNotReady", anchor.Add(6*time.Minute))
	require.NoError(t, attachSameSubjectIncident(ctx, tx, notReady))
	leader, linked = leaderLinkOf(notReady.Id)
	require.True(t, linked)
	assert.Equal(t, oom.Id, leader, "third alert links to the leader, not to the second alert — star, not chain")

	// A late alert after the attach window opens a new group instead.
	late := mkEvent("KubeDeploymentReplicasMismatch", anchor.Add(6*time.Minute).Add(IncidentAttachWindow+time.Minute))
	require.NoError(t, attachSameSubjectIncident(ctx, tx, late))
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
	require.NoError(t, attachSameSubjectIncident(ctx, tx, checkoutErr))

	// payments alerts 4 minutes later; its stored map says payments CALLS
	// checkout — it must join checkout's group via topology.
	paymentsErr := mkEvent("payments", "HighLatency", anchor.Add(4*time.Minute), kgEvidence)
	require.NoError(t, attachSameSubjectIncident(ctx, tx, paymentsErr))

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
	require.NoError(t, attachSameSubjectIncident(ctx, tx, checkoutCrash))
	err = tx.QueryRowContext(ctx, `
		SELECT related_event_id FROM event_correlations
		WHERE event_id = $1 AND correlation_type = $2`, checkoutCrash.Id, SameIncidentCorrelationType).
		Scan(&leader)
	require.NoError(t, err)
	assert.Equal(t, checkoutErr.Id, leader, "same-subject attach joins the existing cross-service star")

	// No stored map on the seed and no same-subject group: no attach.
	lonely := mkEvent("inventory", "DiskPressure", anchor.Add(5*time.Minute), "")
	require.NoError(t, attachSameSubjectIncident(ctx, tx, lonely))
	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT count(*) FROM event_correlations
		WHERE event_id = $1 AND correlation_type = $2`, lonely.Id, SameIncidentCorrelationType).Scan(&n))
	assert.Equal(t, 0, n, "no map, no same-subject members — stays lone")
}
