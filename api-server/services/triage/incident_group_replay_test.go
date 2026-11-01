package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// incident_group_replay_test.go replays recorded real incidents through the
// grouping the inbox uses (attachSameSubjectIncident) and checks the outcome a
// person expects: one incident, headed by the thing the others depend on.
//
// Each fixture in testdata/grouping_replay is one real run: the alerts, when each
// started, when each arrived and when each ended. The same run is replayed several
// ways — as recorded, with triage delayed the way the enrichment queue delays it,
// with triage order shuffled, and through one ingest path instead of two — because
// the answer must not depend on any of those.

type replayFixture struct {
	Name      string `json:"name"`
	Story     string `json:"story"`
	Namespace string `json:"namespace"`
	// Shape is "cloud" for a run of cloud alarms that have nothing to do with one
	// another. Such a run has no single expected incident; what it must not do is
	// file an alert under another subject's incident unless the two started
	// together (see lateCross).
	Shape string `json:"shape,omitempty"`
	// Maps is the service map stored on each alert, keyed by the alert's subject:
	// [caller, callee] pairs exactly as recorded. Each is the neighbourhood of its
	// own subject, so they differ — the seed only ever sees its own.
	Maps   map[string][][2]string `json:"maps"`
	Expect struct {
		HeadSubject string `json:"head_subject"`
		// TimingWindowS is, for a cloud run, how far from an incident's earliest
		// alert a member on another subject may have started.
		TimingWindowS int `json:"timing_window_s,omitempty"`
		// MinLinked is, for a cloud run, how many alerts must end up filed under
		// another at the least — the ones that share a subject and start minutes
		// apart. Without it a run in which nothing grouped at all would pass.
		MinLinked int `json:"min_linked,omitempty"`
	} `json:"expect"`
	Events []replayEvent `json:"events"`
}

type replayEvent struct {
	ID          string `json:"id"`
	Alert       string `json:"alert"`
	Kind        string `json:"kind"` // alert | change
	Path        string `json:"path"` // agent | webhook | cluster
	SubjectType string `json:"subject_type"`
	// SubjectNamespace is set on cloud alarms only: the provider service the
	// alarm's resource belongs to, which is what a cloud alarm stores there.
	SubjectNamespace string `json:"subject_namespace,omitempty"`
	Subject          string `json:"subject"`
	Fingerprint      string `json:"fingerprint"`
	StartsS          int    `json:"starts_s"`
	ArrivesS         int    `json:"arrives_s"`
	EndsS            *int   `json:"ends_s,omitempty"`
	// Placed is false for an alert whose labels name no workload: its subject is
	// the alert name and it has no namespace. Such an alert is not required to
	// join the incident.
	Placed     *bool `json:"placed,omitempty"`
	ServiceMap bool  `json:"service_map,omitempty"`
}

func (e replayEvent) placed() bool { return e.Placed == nil || *e.Placed }

// replayVariant is one way of replaying a run.
type replayVariant struct {
	name string
	// triageDelay is how long after arrival an event is grouped. The enrichment
	// queue held the recorded burst for about three minutes.
	triageDelay time.Duration
	// shuffleSeed, when non-zero, shuffles the triage order of events whose triage
	// falls in the same shuffleBucket.
	shuffleSeed   int64
	shuffleBucket time.Duration
	// dropPath removes one ingest path's copy of every alert that arrived on two.
	dropPath string
}

var replayVariants = []replayVariant{
	{name: "as recorded"},
	{name: "triage 3 min late", triageDelay: 3 * time.Minute},
	{name: "triage 3 min late, order shuffled (1)", triageDelay: 3 * time.Minute, shuffleSeed: 1, shuffleBucket: time.Minute},
	{name: "triage 3 min late, order shuffled (2)", triageDelay: 3 * time.Minute, shuffleSeed: 2, shuffleBucket: time.Minute},
	{name: "one path only (webhook)", dropPath: "agent"},
	{name: "one path only (webhook), order shuffled", triageDelay: 3 * time.Minute, shuffleSeed: 3, shuffleBucket: time.Minute, dropPath: "agent"},
}

type replayOutcome struct {
	required     int
	groups       int
	largest      int
	headSubject  string
	headAlert    string
	twoParents   int
	chains       int
	unplacedIn   int
	unplacedSeen int
	// outside lists the required events that did not end up in the largest group.
	outside []string
	// After every event is triaged a second time (a re-score does that):
	// links that moved or went away, rows left with two heads, and rows whose
	// answer to scoring ("symptom of another alert") disagrees with their link.
	retriageMoved      int
	retriageTwoParents int
	retriageWrongChild int
	// crossSubject counts the alerts filed under an incident headed by another
	// subject. lateCross lists the ones that did not start with that incident —
	// further than the fixture's timing window from its earliest alert — and
	// lateCrossAgain counts them once more after everything is triaged again.
	crossSubject   int
	lateCross      []string
	lateCrossAgain int
	// linked counts the alerts filed under another, on any evidence.
	linked int
	// aloneBesideTiming counts the alerts left in no group although an alert on
	// their own subject started inside the attach window of them — because that
	// neighbour sits in another subject's incident, on timing, and nothing is
	// placed through it. Reported, not asserted: it is what that rule costs.
	aloneBesideTiming int
}

func (o replayOutcome) ok(f replayFixture) bool {
	stable := o.twoParents == 0 && o.chains == 0 &&
		o.retriageMoved == 0 && o.retriageTwoParents == 0 && o.retriageWrongChild == 0
	if f.Shape == replayShapeCloud {
		return stable && len(o.lateCross) == 0 && o.lateCrossAgain == 0 && o.linked >= f.Expect.MinLinked
	}
	return stable && o.groups == 1 && o.headSubject == f.Expect.HeadSubject
}

const replayShapeCloud = "cloud"

func loadReplayFixtures(t *testing.T) []replayFixture {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "grouping_replay", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	sort.Strings(files)
	out := make([]replayFixture, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var fx replayFixture
		require.NoError(t, json.Unmarshal(raw, &fx), f)
		out = append(out, fx)
	}
	return out
}

// replayServiceMap renders one subject's recorded service map as the evidence
// card the grouping reads it from.
func replayServiceMap(ns string, calls [][2]string) string {
	names := map[string]bool{}
	for _, c := range calls {
		names[c[0]], names[c[1]] = true, true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	nodes := make([]map[string]any, 0, len(sorted))
	for _, n := range sorted {
		kind := "Deployment"
		if n == "postgres" || n == "rabbitmq" {
			kind = "StatefulSet"
		}
		nodes = append(nodes, map[string]any{
			"id": n, "node_type": "Workload",
			"properties": map[string]any{"kind": kind, "name": n, "namespace": ns},
		})
	}
	edges := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		edges = append(edges, map[string]any{"relationship_type": "CALLS", "source_node_id": c[0], "dest_node_id": c[1]})
	}
	raw, _ := json.Marshal([]map[string]any{{"type": "knowledge_graph", "nodes": nodes, "edges": edges}})
	return string(raw)
}

type replayAction struct {
	at    time.Duration
	order int // insert < resolve < triage at the same instant
	ev    replayEvent
	kind  string
}

// TestGroupingReplay_RealRuns is the definition of done for incident grouping: it
// fails while any way of replaying a recorded run gives more than one incident,
// the wrong headline, or links that are not a one-level star.
func TestGroupingReplay_RealRuns(t *testing.T) {
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

	// Grouping takes a per-account lock for the length of its transaction, and a
	// replay is one long transaction. Give each replay its own account — any
	// account of the tenant that has no events — so they do not queue behind each
	// other; replays that have to share one take turns.
	var accounts []string
	require.NoError(t, dbConn.SelectContext(ctx, &accounts, `
		SELECT ca.id::text FROM cloud_accounts ca
		WHERE ca.tenant::text = $1
		  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.cloud_account_id = ca.id)
		ORDER BY ca.id LIMIT 24`, tenant))
	if len(accounts) == 0 {
		accounts = []string{account}
	}
	turns := make([]sync.Mutex, len(accounts))

	only := os.Getenv("GROUPING_REPLAY_ONLY")

	// Every replay runs in its own transaction and namespace, so they run side by
	// side: from a laptop each query costs a network round trip and a serial pass
	// takes most of an hour.
	t.Run("replays", func(t *testing.T) {
		for fi, fx := range loadReplayFixtures(t) {
			for vi, v := range replayVariants {
				label := fmt.Sprintf("%s / %s", fx.Name, v.name)
				if only != "" && !strings.Contains(label, only) {
					continue
				}
				if fx.Shape == replayShapeCloud && v.dropPath != "" {
					continue // one ingest path only: these would repeat the variants above
				}
				fx, v, salt := fx, v, fi*100+vi
				slot := (fi*len(replayVariants) + vi) % len(accounts)
				t.Run(label, func(t *testing.T) {
					t.Parallel()
					turns[slot].Lock()
					defer turns[slot].Unlock()
					out := replayRun(t, ctx, dbConn, tenant, accounts[slot], fx, v, salt)
					summary := fmt.Sprintf("groups=%d largest=%d/%d head=%s (%s) two_parents=%d chains=%d unplaced_joined=%d/%d again: moved=%d two_parents=%d wrong_child=%d",
						out.groups, out.largest, out.required, out.headSubject, out.headAlert,
						out.twoParents, out.chains, out.unplacedIn, out.unplacedSeen,
						out.retriageMoved, out.retriageTwoParents, out.retriageWrongChild)
					if fx.Shape == replayShapeCloud {
						summary = fmt.Sprintf("linked=%d (at least %d) under_another_subject=%d of which_not_started_together=%d (again: %d) alone_beside_a_neighbour_filed_on_timing=%d two_parents=%d chains=%d again: moved=%d two_parents=%d wrong_child=%d",
							out.linked, fx.Expect.MinLinked, out.crossSubject, len(out.lateCross), out.lateCrossAgain, out.aloneBesideTiming,
							out.twoParents, out.chains,
							out.retriageMoved, out.retriageTwoParents, out.retriageWrongChild)
						if !out.ok(fx) {
							t.Errorf("REPLAY FAIL %-58s %s\n      filed under another subject's incident without having started with it:\n        %s", label, summary, strings.Join(out.lateCross, "\n        "))
							return
						}
						t.Logf("REPLAY ok   %-58s %s", label, summary)
						return
					}
					if !out.ok(fx) {
						t.Errorf("REPLAY FAIL %-58s %s\n      outside the main group:\n        %s", label, summary, strings.Join(out.outside, "\n        "))
						return
					}
					t.Logf("REPLAY ok   %-58s %s", label, summary)
				})
			}
		}
	})
}

func replayRun(t *testing.T, ctx context.Context, dbConn *sqlx.DB, tenant, account string, fx replayFixture, v replayVariant, salt int) replayOutcome {
	t.Helper()
	tx, err := dbConn.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	ns := fmt.Sprintf("%s-replay-%d", fx.Namespace, salt)
	anchor := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC).Add(time.Duration(salt) * 24 * time.Hour)
	serviceMaps := map[string]string{}
	for subject, calls := range fx.Maps {
		serviceMaps[subject] = replayServiceMap(ns, calls)
	}
	if fx.Shape == replayShapeCloud {
		for _, e := range fx.Events {
			if e.ServiceMap {
				serviceMaps[e.Subject] = replayCloudCard(e.Subject)
			}
		}
	}

	// One path only: drop the other path's copy of an alert that arrived on both.
	events := fx.Events
	if v.dropPath != "" {
		paths := map[string]map[string]bool{}
		for _, e := range fx.Events {
			k := e.Fingerprint + "|" + fmt.Sprint(e.StartsS)
			if paths[k] == nil {
				paths[k] = map[string]bool{}
			}
			paths[k][e.Path] = true
		}
		events = nil
		for _, e := range fx.Events {
			k := e.Fingerprint + "|" + fmt.Sprint(e.StartsS)
			if e.Path == v.dropPath && len(paths[k]) > 1 {
				continue
			}
			events = append(events, e)
		}
	}

	actions := make([]replayAction, 0, len(events)*3)
	for _, e := range events {
		arrive := time.Duration(e.ArrivesS) * time.Second
		actions = append(actions, replayAction{at: arrive, order: 0, ev: e, kind: "insert"})
		if e.EndsS != nil && *e.EndsS > e.StartsS {
			actions = append(actions, replayAction{at: time.Duration(*e.EndsS) * time.Second, order: 1, ev: e, kind: "resolve"})
		}
		actions = append(actions, replayAction{at: arrive + v.triageDelay, order: 2, ev: e, kind: "triage"})
	}
	if v.shuffleSeed != 0 {
		// Events triaged in the same bucket are triaged in a random order, at the
		// end of the bucket — what a burst released from a queue looks like.
		rng := rand.New(rand.NewSource(v.shuffleSeed))
		for i := range actions {
			if actions[i].kind == "triage" {
				bucket := actions[i].at / v.shuffleBucket
				actions[i].at = (bucket+1)*v.shuffleBucket - time.Duration(rng.Intn(1000))*time.Millisecond
			}
		}
	}
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].at != actions[j].at {
			return actions[i].at < actions[j].at
		}
		return actions[i].order < actions[j].order
	})

	ids := map[string]string{}
	built := map[string]*models.Event{}
	for _, a := range actions {
		e := a.ev
		switch a.kind {
		case "insert":
			ev := insertReplayEvent(t, ctx, tx, tenant, account, ns, anchor, serviceMaps[e.Subject], e)
			ids[e.ID] = ev.Id
			built[e.ID] = ev
		case "resolve":
			_, err := tx.ExecContext(ctx, `UPDATE events SET status = 'CLOSED', ends_at = $2 WHERE id = $1`,
				ids[e.ID], anchor.Add(time.Duration(*e.EndsS)*time.Second))
			require.NoError(t, err)
		case "triage":
			_, err := attachSameSubjectIncident(ctx, tx, built[e.ID])
			require.NoError(t, err, "%s %s", e.ID, e.Alert)
		}
	}

	// Read the links back and follow each event to its root.
	all := make([]string, 0, len(ids))
	byUUID := map[string]replayEvent{}
	for _, e := range events {
		all = append(all, ids[e.ID])
		byUUID[ids[e.ID]] = e
	}
	type linkRow struct {
		Child  string `db:"event_id"`
		Leader string `db:"related_event_id"`
	}
	readLinks := func() map[string]map[string]bool {
		var links []linkRow
		require.NoError(t, tx.SelectContext(ctx, &links, `
			SELECT event_id, related_event_id FROM event_correlations
			WHERE correlation_type = $1 AND cloud_account_id = $2 AND event_id = ANY($3)`,
			SameIncidentCorrelationType, account, pq.Array(all)))
		parents := map[string]map[string]bool{}
		for _, l := range links {
			if parents[l.Child] == nil {
				parents[l.Child] = map[string]bool{}
			}
			parents[l.Child][l.Leader] = true
		}
		return parents
	}
	parents := readLinks()
	parentOf := func(id string) string {
		best := ""
		for p := range parents[id] {
			if best == "" || p < best {
				best = p
			}
		}
		return best
	}
	rootOf := func(id string) string {
		seen := map[string]bool{}
		for !seen[id] {
			seen[id] = true
			p := parentOf(id)
			if p == "" {
				return id
			}
			id = p
		}
		return id
	}

	var out replayOutcome
	groupSize := map[string]int{}
	for _, e := range events {
		id := ids[e.ID]
		if len(parents[id]) > 1 {
			out.twoParents++
		}
		if p := parentOf(id); p != "" && parentOf(p) != "" {
			out.chains++
		}
		if !e.placed() {
			out.unplacedSeen++
			if r := rootOf(id); r != id {
				if re, known := byUUID[r]; known && re.placed() {
					out.unplacedIn++
				}
			}
			continue
		}
		out.required++
		groupSize[rootOf(id)]++
	}
	out.groups = len(groupSize)
	head := ""
	for r, n := range groupSize {
		if n > out.largest || (n == out.largest && r < head) {
			out.largest, head = n, r
		}
	}
	if he, known := byUUID[head]; known {
		out.headSubject, out.headAlert = he.Subject, he.Alert
	}
	for _, e := range events {
		if !e.placed() || rootOf(ids[e.ID]) == head {
			continue
		}
		lead := "alone"
		if r := rootOf(ids[e.ID]); r != ids[e.ID] {
			if re, known := byUUID[r]; known {
				lead = "under " + re.Alert + " on " + re.Subject
			} else {
				lead = "under an event outside the run"
			}
		}
		out.outside = append(out.outside, fmt.Sprintf("%s %s on %s (%s, starts %ds) %s", e.ID, e.Alert, e.Subject, e.Path, e.StartsS, lead))
	}

	// A cloud run: who ended up under another subject's incident, and whether they
	// started with it. Nothing but the clock connects two subjects in such a run,
	// so every one of these links was made on timing — or by a later firing
	// following an earlier one that was.
	window := time.Duration(fx.Expect.TimingWindowS) * time.Second
	crossSubject := func(parentOf func(string) string) (int, []string) {
		root := func(id string) string {
			seen := map[string]bool{}
			for !seen[id] {
				seen[id] = true
				p := parentOf(id)
				if p == "" {
					return id
				}
				id = p
			}
			return id
		}
		earliest := map[string]int{}
		for _, e := range events {
			r := root(ids[e.ID])
			if first, ok := earliest[r]; !ok || e.StartsS < first {
				earliest[r] = e.StartsS
			}
		}
		n, late := 0, []string(nil)
		for _, e := range events {
			r := root(ids[e.ID])
			re, known := byUUID[r]
			if r == ids[e.ID] || !known || re.Subject == e.Subject {
				continue
			}
			n++
			gap := time.Duration(e.StartsS-earliest[r]) * time.Second
			if gap > window || gap < -window {
				late = append(late, fmt.Sprintf("%s %s (starts %ds) under %s, whose incident began at %ds — %s apart",
					e.ID, e.Alert, e.StartsS, re.Alert, earliest[r], gap.Round(time.Second)))
			}
		}
		return n, late
	}
	if fx.Shape == replayShapeCloud {
		out.crossSubject, out.lateCross = crossSubject(parentOf)
		isHead := map[string]bool{}
		for _, e := range events {
			if p := parentOf(ids[e.ID]); p != "" {
				out.linked++
				isHead[p] = true
			}
		}
		for _, e := range events {
			if parentOf(ids[e.ID]) != "" || isHead[ids[e.ID]] {
				continue
			}
			for _, o := range events {
				gap := time.Duration(o.StartsS-e.StartsS) * time.Second
				if o.Subject != e.Subject || o.ID == e.ID || gap > IncidentAttachWindow || gap < -IncidentAttachWindow {
					continue
				}
				if r := rootOf(ids[o.ID]); r != ids[o.ID] && byUUID[r].Subject != o.Subject {
					out.aloneBesideTiming++
					break
				}
			}
		}
	}

	// Triage runs again on stored rows. A second pass over every event must leave
	// every link where it is, and must tell scoring "symptom" for exactly the rows
	// filed under another alert — never for the head or a further row of it.
	saidChild := map[string]bool{}
	for _, e := range events {
		isChild, err := attachSameSubjectIncident(ctx, tx, built[e.ID])
		require.NoError(t, err, "again: %s %s", e.ID, e.Alert)
		saidChild[ids[e.ID]] = isChild
	}
	after := readLinks()
	for child, heads := range parents {
		for head := range heads {
			if !after[child][head] {
				out.retriageMoved++
			}
		}
	}
	for _, e := range events {
		id := ids[e.ID]
		if len(after[id]) > 1 {
			out.retriageTwoParents++
		}
		isSymptom := false
		for head := range after[id] {
			if he, known := byUUID[head]; !known || he.Fingerprint != e.Fingerprint {
				isSymptom = true
			}
		}
		if saidChild[id] != isSymptom {
			out.retriageWrongChild++
		}
	}
	if fx.Shape == replayShapeCloud {
		_, lateAgain := crossSubject(func(id string) string {
			best := ""
			for p := range after[id] {
				if best == "" || p < best {
					best = p
				}
			}
			return best
		})
		out.lateCrossAgain = len(lateAgain)
	}
	return out
}

// replayCloudCard is the stored map of a cloud resource the graph knows and has
// no dependency edges for: the alarm is placed, with nothing connected to it.
func replayCloudCard(subject string) string {
	raw, _ := json.Marshal([]map[string]any{{
		"type": "knowledge_graph",
		"nodes": []map[string]any{{
			"id": subject, "node_type": "ComputeInstance",
			"properties": map[string]any{
				"name": subject, "resource_id": subject,
				"arn": "arn:aws:ec2:us-east-1:000000000000:instance/" + subject,
			},
		}},
		"edges": []map[string]any{},
	}})
	return string(raw)
}

func insertReplayEvent(t *testing.T, ctx context.Context, tx *sqlx.Tx, tenant, account, ns string, anchor time.Time, serviceMap string, e replayEvent) *models.Event {
	t.Helper()
	id := uuid.NewString()
	starts := anchor.Add(time.Duration(e.StartsS) * time.Second)
	arrives := anchor.Add(time.Duration(e.ArrivesS) * time.Second)

	source, findingType := "prometheus", "issue"
	subjectNS, subjectType, ownerKind, serviceKey := ns, e.SubjectType, "", ""
	kind := "Deployment"
	if strings.EqualFold(e.SubjectType, "statefulset") {
		kind = "StatefulSet"
	}
	switch e.Path {
	case "webhook":
		source = "prometheus_alertmanager_webhook"
		ownerKind = strings.ToLower(kind)
		serviceKey = fmt.Sprintf("%s:%s:%s", ns, kind, e.Subject)
	case "cluster":
		source = "kubernetes_api_server"
		ownerKind = kind
		serviceKey = ns + "/" + e.Subject
	default:
		ownerKind = kind
		serviceKey = ns + "/" + e.Subject
	}
	if e.Kind == "change" {
		findingType = "configuration_change"
	}
	owner := e.Subject
	if !e.placed() {
		subjectNS, subjectType, ownerKind, owner = "", "", "", ""
		serviceKey = "/" + e.Subject
	}
	// A cloud alarm as the cloud collector stores it: the provider service in the
	// namespace, no owner, a resource identifier for the service key, and — today
	// — closed on arrival with no duration.
	status, endsAt := "FIRING", (*time.Time)(nil)
	if e.Path == "cloud" {
		source = "AWS_CloudWatch_Alarm"
		subjectNS, subjectType, ownerKind, owner = e.SubjectNamespace, e.SubjectType, "", ""
		serviceKey = fmt.Sprintf("arn:aws:replay:us-east-1:000000000000:%s:%s", e.SubjectType, e.Subject)
		if e.EndsS != nil && *e.EndsS <= e.StartsS {
			status, endsAt = "CLOSED", &starts
		}
	}
	evidence := "[]"
	if e.ServiceMap && serviceMap != "" {
		evidence = serviceMap
	}
	// Each run gets its own fingerprints: the fixture's are only unique within it.
	fingerprint := ns + "-" + e.Fingerprint

	_, err := tx.ExecContext(ctx, `
		INSERT INTO events (id, tenant, cloud_account_id, aggregation_key, subject_type,
			subject_namespace, subject_name, subject_owner, subject_owner_kind, service_key,
			fingerprint, finding_id, finding_type, priority, cluster, starts_at, created_at,
			status, source, title, evidences, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'HIGH', 'replay-cluster',
			$14, $15, $18, $16, $4, $17::jsonb, $19)`,
		id, tenant, account, e.Alert, subjectType, subjectNS, e.Subject, owner, ownerKind, serviceKey,
		fingerprint, "fid-"+id, findingType, starts, arrives, source, evidence, status, endsAt)
	require.NoError(t, err, "%s %s", e.ID, e.Alert)

	ev := &models.Event{
		Id:               id,
		Tenant:           &tenant,
		CloudAccountId:   &account,
		Source:           &source,
		AggregationKey:   strPtr(e.Alert),
		FindingType:      &findingType,
		SubjectType:      &subjectType,
		SubjectNamespace: &subjectNS,
		SubjectName:      strPtr(e.Subject),
		SubjectOwner:     &owner,
		SubjectOwnerKind: &ownerKind,
		ServiceKey:       &serviceKey,
		Fingerprint:      &fingerprint,
		StartsAt:         &starts,
		CreatedAt:        &arrives,
		Status:           &status,
		EndsAt:           endsAt,
	}
	if evidence != "[]" {
		var j models.Json
		require.NoError(t, j.Scan([]uint8(evidence)))
		ev.Evidences = &j
	}
	return ev
}

// Two alerts of one incident triaged at the same moment must not both decide on
// the state before either wrote. The second waits for the first's transaction.
func TestIncidentGroupLock_SecondTriageWaitsForTheFirst(t *testing.T) {
	if os.Getenv("TEST_LIVE_CORRELATION") != "1" {
		t.Skip("set TEST_LIVE_CORRELATION=1 to run (requires APP_DATABASE_URL)")
	}
	dbURL := os.Getenv("APP_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set APP_DATABASE_URL to run")
	}
	dbConn, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer func() { _ = dbConn.Close() }()
	ctx := context.Background()
	account := "replay-lock-" + uuid.NewString()

	first, err := dbConn.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = first.Rollback() }()
	require.NoError(t, lockIncidentGrouping(ctx, first, account))

	second, err := dbConn.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = second.Rollback() }()
	got := make(chan error, 1)
	go func() { got <- lockIncidentGrouping(ctx, second, account) }()

	select {
	case err := <-got:
		t.Fatalf("the second triage got the account lock while the first still held it (err=%v)", err)
	case <-time.After(3 * time.Second):
	}

	other, err := dbConn.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = other.Rollback() }()
	require.NoError(t, lockIncidentGrouping(ctx, other, account+"-another-account"), "another account is not held up")

	require.NoError(t, first.Rollback())
	select {
	case err := <-got:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("the second triage never got the lock after the first finished")
	}
}
