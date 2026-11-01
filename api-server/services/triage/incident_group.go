package triage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// incident_group.go is slice 1 of same-subject incident grouping (epic #34655):
// when one subject produces several distinct alert types in a short burst (a pod
// OOMKilled, then CrashLoopBackOff, then NotReady), link them into one group by
// writing a `same_incident` row (child -> leader) to event_correlations. The
// leader is elected from the group (decideGroup in incident_group_decide.go);
// every child points straight at it (a star, never child -> child), so resolving
// a group is one hop.
//
// A group is the alerts firing together in one window — nothing more. Links join
// the FIRINGS, so an incident has a beginning and an end without needing a record
// of its own: a fresh burst links fresh events, an older burst's links fall
// outside the window, and the headline is always something that fired in it.
//
// Links used to hang off each alert's first-ever event instead, which made a
// group a permanent association rather than an incident. It never ended: any
// member firing held it open while the alert that named it could have stopped
// days ago. Measured on an anonymized production tenant over 14 days — 1,337 of 1,445 links
// (93%) named a headline whose alert had not fired in the preceding 15 minutes,
// average 38.8 hours quiet; one group had collected 17 members, one headline was
// 158 days old, and none of it was visible in a time-scoped view because the
// events predated the window being looked at.
//
// Membership is evidence-based, not scored: same SubjectKey (or connected to it),
// inside a rolling attach window. One row per ALERT, carrying its latest firing,
// so an alert firing ten times is one member. Chronic pairs
// (>= ChronicWeeklyThreshold firings/week, chronic.go) still join — they only
// lose the leader election, so a flapper never becomes the headline.
//
// The decision is made over the SET of alerts active around the one being
// triaged, collapsed to one member per alert, under a per-account lock. It used
// to be made per stored row, looking only at rows that started strictly earlier,
// and that made the answer depend on arrival order: three alerts from one rule
// group starting in the same second never saw each other, a cause arriving after
// its symptoms was filed under one of them, an alert stored twice by two ingest
// paths could split a group, and a database that had been down for half an hour
// was no longer a candidate when its callers finally alerted. Replaying recorded
// incidents in shuffled order (incident_group_replay_test.go) is the gate.
//
// On by default; INCIDENT_GROUPING_ENABLED=false is the kill switch. The
// promotion train (main -> test -> prod) gives dev/test a validation window
// with real traffic before any customer account writes links.

const (
	// SameIncidentCorrelationType marks child->leader group links in
	// event_correlations. Readers must filter on it — the legacy heuristic's
	// scored rows share the table.
	SameIncidentCorrelationType = "same_incident"
	// IncidentAttachWindow is the rolling quiet-time bound: a new alert joins
	// only while the group's newest member started less than this ago
	// (re-armed by each join).
	IncidentAttachWindow    = 15 * time.Minute
	incidentGroupingEnvFlag = "INCIDENT_GROUPING_ENABLED"
	// topologyGroupingEnvFlag kills only the cross-service (stored-map) attach
	// path; same-subject grouping keeps its own switch above.
	topologyGroupingEnvFlag = "INCIDENT_TOPOLOGY_GROUPING"
	// unlocatedGroupingEnvFlag kills the co-timing attach for alerts that have
	// no position in the graph at all. Defaults on like the other two, so the
	// narrowness has to come from the rule rather than from the switch: it fires
	// only when nothing else matched, only into an existing incident in the
	// seed's own scope (namespace, or AWS as a whole) that began with the seed,
	// and only when exactly one such incident exists. This is the flag to pull
	// first if grouping ever looks too eager.
	unlocatedGroupingEnvFlag = "INCIDENT_UNLOCATED_GROUPING"
	// timingOnlyReasonPrefix starts the reason on every link written on timing
	// alone. The path is counted and audited by it, and a firing that was linked
	// this way is told apart by it (groupLinks.timing).
	timingOnlyReasonPrefix = "no topology position; sole "
	// timingOnlyCloudWindow is how far from an incident's earliest alert a cloud
	// alarm may have started and still join it on timing alone. A cloud account
	// is the widest scope this path has — any alarm of the account against any
	// alarm-led incident in it — so it gets the tightest bound. Reviewing stored
	// timing-only links on cloud accounts, the ones a person would call right
	// started within a few minutes of their incident's earliest alert, while the
	// wrong ones were spread evenly over the quarter hour. Namespace and node
	// scopes keep IncidentAttachWindow: there, many links between five and
	// fifteen minutes looked right — typically one alert type failing on sibling
	// workloads a few minutes apart.
	timingOnlyCloudWindow = 5 * time.Minute
	// leaderTakeoverEnvFlag kills only the replacement of an existing head: with
	// it off the arriving alert is always filed under the head already there.
	// Links already moved stay moved.
	leaderTakeoverEnvFlag = "INCIDENT_LEADER_TAKEOVER"
	// leaderTakeoverReasonPrefix starts the reason on the link a replaced head
	// gets to its replacement, so the set can be counted or audited, and so the
	// co-timing path does not count a replaced head as an open incident.
	leaderTakeoverReasonPrefix = "dependency leads:"
	// maxLeaderFollow bounds how far a link is followed to its root. Every attach
	// now runs under the account lock and writes one-level stars; this only
	// matters for chains written before that, and the bound keeps a cycle among
	// them from looping.
	maxLeaderFollow = 3
	// incidentStillFiringLookback is how long before the attach window an alert
	// on something the seed depends on may have started and still count, as long
	// as it is still firing. Same length as the incident panel's cause lead-in
	// (CauseLeadInSeconds). Measured on two weeks of real alerts that started more
	// than 15 minutes into a still-firing alert on a service they call: nearly
	// all of the ones that followed within six hours came in the first hour, a
	// few more in the second, and the majority came later still — standing
	// conditions, not incidents. CauseLeadInSeconds is negative (seconds before
	// the seed), so this is a positive two hours.
	incidentStillFiringLookback = -CauseLeadInSeconds * time.Second
	// incidentRowsPerAlert bounds the rows read for one alert around a seed, and
	// incidentActiveRowLimit the rows read in all. Rows only matter for finding an
	// alert's existing link, and every row gets its own when it is triaged, so the
	// newest few of an alert are enough.
	incidentRowsPerAlert   = 20
	incidentActiveRowLimit = 2000
	// incidentCandidateLimit bounds the window fetch; one subject+namespace
	// rarely has more than a handful of distinct fingerprints in 90 minutes.
	incidentCandidateLimit = 200
	// maxIncidentHops is how far a connected service can sit from the one that
	// started the group. One hop misses a calling c through b, which is the shape
	// of the reported case (payment and inventory both call order, and all three
	// call database). More than two swallows an estate: node degree on the
	// anonymized production tenant is median 1 but one node has 1,047 edges.
	maxIncidentHops = 2
	// incidentGroupSubjectCap bounds how many machines one incident can span.
	// Across 14 days the number of subjects alerting at once in a 15-minute window
	// averages 5, with 25 at the 99th percentile and 29 at worst — for the WHOLE
	// account, not one connected set. Past this it is a fleet-wide event and wants
	// different treatment, so grouping stops adding rather than grows.
	incidentGroupSubjectCap = 20
)

// incidentGroupingEnabled defaults to true; only an explicit "false"/"0"
// disables (kill switch — a default-off flag nobody flips would leave the
// feature dead in config, the fate the threshold-suggestion audit measured).
func incidentGroupingEnabled() bool {
	v := strings.TrimSpace(os.Getenv(incidentGroupingEnvFlag))
	return !strings.EqualFold(v, "false") && v != "0"
}

// eventAlertIdentity projects a stored event onto the identity the tiering and
// grouping rules key on (nil-safe pointer derefs).
func eventAlertIdentity(ev *models.Event) AlertIdentity {
	deref := func(p *string) string {
		if p != nil {
			return *p
		}
		return ""
	}
	return AlertIdentity{
		ID:               ev.Id,
		SubjectType:      deref(ev.SubjectType),
		SubjectName:      deref(ev.SubjectName),
		SubjectNamespace: deref(ev.SubjectNamespace),
		SubjectOwner:     deref(ev.SubjectOwner),
		AggregationKey:   deref(ev.AggregationKey),
		FindingType:      deref(ev.FindingType),
	}
}

// leaderTakeoverEnabled defaults to true; only an explicit "false"/"0" disables,
// matching the other grouping switches.
func leaderTakeoverEnabled() bool {
	v := strings.TrimSpace(os.Getenv(leaderTakeoverEnvFlag))
	return !strings.EqualFold(v, "false") && v != "0"
}

// groupCandidate is one alert as the node-memory axis reports it.
type groupCandidate struct {
	ID             string
	AggregationKey string
	// Fingerprint is the alert's identity across its firings and ingest paths.
	Fingerprint string
	// StartsAt is the firing this candidate represents — the alert's most recent
	// one inside the window.
	StartsAt time.Time
	// LastSeen is the alert's newest start in the window.
	LastSeen time.Time
}

// activeRow is one stored event around the seed.
type activeRow struct {
	ID               string     `db:"id"`
	Fingerprint      *string    `db:"fingerprint"`
	AggregationKey   *string    `db:"aggregation_key"`
	SubjectType      *string    `db:"subject_type"`
	SubjectName      *string    `db:"subject_name"`
	SubjectNamespace *string    `db:"subject_namespace"`
	SubjectOwner     *string    `db:"subject_owner"`
	SubjectOwnerKind *string    `db:"subject_owner_kind"`
	ServiceKey       *string    `db:"service_key"`
	StartsAt         time.Time  `db:"starts_at"`
	CreatedAt        time.Time  `db:"created_at"`
	Status           string     `db:"status"`
	EndsAt           *time.Time `db:"ends_at"`
}

// activeRowsWhere selects the rows the seed can be grouped with.
//
// The first arm is the attach window on BOTH sides of the seed's start: an alert
// that started in the same second, or a little later but is already stored
// because the seed's own triage was delayed, is as much part of the incident as
// one that started before. The second arm reaches further back for alerts that
// were still firing when the seed started; relateMembers decides which of those
// count. "Still firing" is judged at the seed's start, not at triage time: a
// seed triaged minutes late must see the alert that was firing when it began,
// even if that alert has resolved since.
//
// $1 tenant, $2 account, $3 seed id, $4 window start, $5 window end,
// $6 still-firing lookback start, $7 seed start.
const activeRowsWhere = `
	e.tenant = $1
	AND e.cloud_account_id = $2
	AND e.id != $3
	AND lower(coalesce(e.finding_type, '')) NOT IN ('slo', 'anomaly')
	AND ((e.starts_at >= $4 AND e.starts_at <= $5)
	  OR (e.starts_at >= $6 AND e.starts_at < $4
	      AND ((e.status = 'FIRING' AND e.ends_at IS NULL) OR e.ends_at >= $7)))`

// loadActiveRows reads the rows around the seed, newest start first and earliest
// stored first within a start — the order collapseActiveRows needs.
//
// The cap is per alert first, then overall: an alert re-notifying every minute
// must not crowd the others out. When the overall cap does bite it keeps the
// still-firing alerts from before the window — the likeliest causes, and few —
// and then the rows that started closest to the seed.
func loadActiveRows(ctx context.Context, db sqlx.ExtContext, event *models.Event, start time.Time) ([]activeRow, error) {
	var rows []activeRow
	err := sqlx.SelectContext(ctx, db, &rows, `
		SELECT id, fingerprint, aggregation_key, subject_type, subject_name,
		       subject_namespace, subject_owner, subject_owner_kind, service_key,
		       starts_at, created_at, status, ends_at
		FROM (
			SELECT e.id, e.fingerprint, e.aggregation_key, e.subject_type, e.subject_name,
			       e.subject_namespace, e.subject_owner, e.subject_owner_kind, e.service_key,
			       e.starts_at, coalesce(e.created_at, e.starts_at) AS created_at,
			       coalesce(e.status, '') AS status, e.ends_at,
			       row_number() OVER (PARTITION BY coalesce(nullif(e.fingerprint, ''), e.id::text)
			                          ORDER BY e.starts_at DESC, e.created_at ASC, e.id ASC) AS rn
			FROM events e
			WHERE `+activeRowsWhere+`
		) a
		WHERE rn <= `+fmt.Sprint(incidentRowsPerAlert)+`
		ORDER BY (starts_at < $4) DESC, abs(extract(epoch FROM starts_at - $7)) ASC
		LIMIT `+fmt.Sprint(incidentActiveRowLimit),
		*event.Tenant, *event.CloudAccountId, event.Id,
		start.Add(-IncidentAttachWindow), start.Add(IncidentAttachWindow),
		start.Add(-incidentStillFiringLookback), start)
	if err != nil {
		return nil, fmt.Errorf("failed to load alerts around the event: %w", err)
	}
	if len(rows) == incidentActiveRowLimit {
		slog.InfoContext(ctx, "Incident grouping hit the active-row limit; rows furthest from the event's start were not considered",
			"event_id", event.Id, "limit", incidentActiveRowLimit)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].StartsAt.Equal(rows[j].StartsAt) {
			return rows[i].StartsAt.After(rows[j].StartsAt)
		}
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}

// attachSameSubjectIncident places a just-triaged event in its incident, if it
// has one. Failures are returned for logging but must never fail triage —
// grouping is additive.
//
// Reports whether the event was filed as a CHILD of another alert. Scoring
// consumes that directly rather than re-reading event_correlations: the caller
// writes the link here, in Step 3b, and scores in Step 4, so a second query would
// only re-read what this function just decided — and, because the legacy pairwise
// rows share the table, would sometimes read the wrong row back.
func attachSameSubjectIncident(ctx context.Context, db sqlx.ExtContext, event *models.Event) (bool, error) {
	if event == nil || event.Tenant == nil || *event.Tenant == "" ||
		event.CloudAccountId == nil || *event.CloudAccountId == "" ||
		event.StartsAt == nil || event.AggregationKey == nil || *event.AggregationKey == "" {
		return false, nil // not groupable
	}
	identity := eventAlertIdentity(event)
	// Derived signals (SLO violations, anomaly detections) neither lead nor
	// join groups — same reasoning as the assembly's isDerivedSignal: they are
	// statistical echoes of concrete failures, and replay showed an
	// SLOViolation leading a crashloop group it did not cause.
	if isDerivedSignal(identity) {
		return false, nil
	}
	seedKey := SubjectKey(identity)
	_, roughSubj := chronicSubjectIdentity(event)
	if roughSubj == "" || strings.HasSuffix(seedKey, "|") {
		return false, nil // no subject identity — cluster-scoped alerts never group here
	}
	start := *event.StartsAt

	// A row that is already filed stays filed. Triage runs again on stored rows
	// (a re-score), and a related alert's triage may have filed this one first.
	placed, isChild, err := seedAlreadyFiled(ctx, db, event)
	if err != nil || placed {
		return isChild, err
	}

	// Everything that does not depend on who is linked to whom is read without
	// the lock: most alerts have nothing around them, and an account in a storm
	// of unrelated alerts must not be serialised on it. A row stored after this
	// read is not lost — its own triage will find this seed.
	rows, err := loadActiveRows(ctx, db, event, start)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	g := prepareGrouping(ctx, event, seedKey, start, rows)
	if len(g.in.Related) == 0 && len(g.own.rows) == 0 && len(g.own.others) == 0 && !g.nodeAxis && !g.timingFallback {
		return false, nil // nothing to pair it with, and co-timing may not
	}
	if len(g.in.Related) > 0 {
		if g.in.Noisy, err = loadNoisyAlerts(ctx, db, event, g.in, start); err != nil {
			return false, err
		}
	}

	// The decision reads what other alerts' triage has written and writes what
	// theirs will read, so two alerts of one incident triaged together must not
	// both decide on the state before either wrote. Reading the links, deciding
	// and writing run in one transaction holding the account's lock.
	err = inGroupTx(ctx, db, func(tx *sqlx.Tx) error {
		if err := lockIncidentGrouping(ctx, tx, *event.CloudAccountId); err != nil {
			return err
		}
		child, err := decideAndLink(ctx, tx, event, g, start)
		isChild = child
		return err
	})
	if err != nil {
		return false, err
	}
	return isChild, nil
}

// ownPlacement reads, from the existing links, where the seed's alert already
// stands. seedRow is the row being triaged, otherRows the other rows of the same
// alert around it, and sameAlert answers for a head row that is neither — an
// earlier firing from before the window, which a chain of firings each less than
// a window apart still leads. Pure.
//
//   - seedFiled: the seed row itself already has a head. Nothing more is written;
//     seedIsChild says whether that head is another alert.
//   - ownHead: another row of the alert is filed under this head, which is not a
//     row of the alert.
//   - ownLeads: this row of the alert heads a group — the seed row when it does,
//     or the row another row of the alert is filed under.
func ownPlacement(links groupLinks, seedRow string, otherRows []string, sameAlert func(row string) bool) (ownHead, ownLeads string, seedFiled, seedIsChild bool) {
	isOwn := func(row string) bool {
		if row == seedRow {
			return true
		}
		for _, r := range otherRows {
			if r == row {
				return true
			}
		}
		return sameAlert != nil && sameAlert(row)
	}
	if _, filed := links.parent[seedRow]; filed {
		return "", "", true, !isOwn(links.root(seedRow))
	}
	ownHead, ownLeads = links.groupOf(append([]string{seedRow}, otherRows...))
	if ownHead != "" && isOwn(ownHead) {
		// Filed under a row of its own alert: that alert heads the group.
		ownHead, ownLeads = "", ownHead
	}
	return ownHead, ownLeads, false, false
}

// seedAlreadyFiled reports whether the seed row already has a head, and whether
// that makes it a child: a row filed under another row of its own alert is one
// more row of the head, not a symptom.
func seedAlreadyFiled(ctx context.Context, db sqlx.ExtContext, event *models.Event) (filed, isChild bool, err error) {
	var heads []string
	err = sqlx.SelectContext(ctx, db, &heads, `
		SELECT coalesce(h.fingerprint, '')
		FROM event_correlations c
		JOIN events h ON h.id = c.related_event_id
		WHERE c.correlation_type = $1
		  AND c.cloud_account_id = $2
		  AND c.event_id = $3
		ORDER BY c.related_event_id
		LIMIT 1`,
		SameIncidentCorrelationType, *event.CloudAccountId, event.Id)
	if err != nil {
		return false, false, fmt.Errorf("failed to check whether the event is already grouped: %w", err)
	}
	if len(heads) == 0 {
		return false, false, nil
	}
	seedFP := deref(event.Fingerprint)
	return true, seedFP == "" || heads[0] != seedFP, nil
}

// lockIncidentGrouping serialises grouping decisions for one account until the
// transaction ends. The key is the account, not a namespace: the service map
// connects subjects across namespaces, so two alerts of one incident can sit in
// different ones. hashtextextended, not hashtext: bigint, so the lock uses the
// full 64-bit keyspace (same as the mute lock in mute.go).
func lockIncidentGrouping(ctx context.Context, tx *sqlx.Tx, accountID string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 34655))`,
		accountID+"|incident-group"); err != nil {
		return fmt.Errorf("failed to lock incident grouping for the account: %w", err)
	}
	return nil
}

// grouping is what is known about the seed before any link is read.
type grouping struct {
	in      decideInput
	own     ownRows
	members []*alertMember
	located bool
	// nodeAxis: the seed is a node memory failure, so the node axis may relate it
	// to alerts neither its subject nor its service map reaches.
	nodeAxis bool
	// timingFallback: the graph cannot place the seed, so co-timing may.
	timingFallback bool
	seedKey        string
	// rowStarts is the start of every row read around the seed, and rowAlerts
	// the alert each belongs to.
	rowStarts map[string]time.Time
	rowAlerts map[string]string
	// seedCreated is when the seed row was stored.
	seedCreated time.Time
}

// prepareGrouping turns the rows around the seed into the alert-level picture
// the decision needs, short of the links. No I/O.
func prepareGrouping(ctx context.Context, event *models.Event, seedKey string, start time.Time, rows []activeRow) grouping {
	ns, roughSubj := chronicSubjectIdentity(event)
	seed := alertMember{
		Fingerprint:    alertKey(deref(event.Fingerprint), event.Id),
		RepRow:         event.Id,
		Rows:           []string{event.Id},
		AggregationKey: *event.AggregationKey,
		SubjectKey:     seedKey,
		ServiceKey:     getServiceKeyFromEvent(event),
		Subject:        roughSubj,
		Namespace:      ns,
		StartsAt:       start,
		Recent:         true,
		Open:           true,
	}
	g := grouping{
		seedKey: seedKey, seedCreated: start,
		rowStarts: make(map[string]time.Time, len(rows)+1),
		rowAlerts: make(map[string]string, len(rows)+1),
	}
	if event.CreatedAt != nil {
		g.seedCreated = *event.CreatedAt
	}
	g.rowStarts[event.Id], g.rowAlerts[event.Id] = start, seed.Fingerprint
	for _, r := range rows {
		g.rowStarts[r.ID], g.rowAlerts[r.ID] = r.StartsAt, alertKey(deref(r.Fingerprint), r.ID)
	}
	g.own, g.members = collapseActiveRows(rows, start, seed.Fingerprint)

	graph, _ := parseServiceMapFromEvent(event)
	// Located is a fact about the seed, decided before the kill switch: turning
	// topology grouping off must not turn every placed alert into an unplaced one
	// and hand it to co-timing instead.
	g.located = graph != nil && seed.ServiceKey != ""
	if !g.located || !topologyGroupingEnabled() {
		graph = nil
	}
	related, capped := relateMembers(graph, seed, isConfigurationChange(event), g.members)
	if capped {
		slog.InfoContext(ctx, "Incident group hit the subject cap; not adding more",
			"event_id", event.Id, "cap", incidentGroupSubjectCap, "seed_subject", seedKey)
	}
	_, _, g.nodeAxis = nodeMemoryFailure(nodeFailure{
		AggregationKey: deref(event.AggregationKey),
		SubjectType:    deref(event.SubjectType),
		SubjectName:    deref(event.SubjectName),
		SubjectNode:    deref(event.SubjectNode),
		Evidences:      event.Evidences,
	})
	g.nodeAxis = g.nodeAxis && nodeGroupingEnabled()
	g.timingFallback = timingFallbackAllowed(event, g.located)
	g.in = decideInput{
		Seed:            seed,
		SeedKey:         seedKey,
		Related:         related,
		DependedOnBy:    countDependents(graph, seed, related),
		TakeoverEnabled: leaderTakeoverEnabled(),
	}
	return g
}

// decideAndLink reads the links of every alert involved, decides, and writes the
// result. Runs under the account lock.
func decideAndLink(ctx context.Context, tx *sqlx.Tx, event *models.Event, g grouping, start time.Time) (bool, error) {
	in := g.in
	// The node axis: memory failures on the seed's node. It reads which group
	// each of them is in, so it runs under the lock. An alert already related
	// through its subject or the service map keeps that, stronger, relation.
	if g.nodeAxis {
		alertRows := make(map[string][]string, len(g.members))
		for _, m := range g.members {
			alertRows[m.Fingerprint] = m.Rows
		}
		nodes, err := collectNodeMembers(ctx, tx, event,
			groupCandidate{ID: event.Id, AggregationKey: in.Seed.AggregationKey, Fingerprint: deref(event.Fingerprint), StartsAt: start},
			g.seedKey, start, alertRows)
		if err != nil {
			// Additive: losing it must not cost the seed the group it can form without it.
			slog.WarnContext(ctx, "Failed to collect node members", "error", err, "event_id", event.Id)
		}
		before := len(in.Related)
		in.Related = mergeNodeMembers(in.Related, g.members, nodes, in.Seed, g.seedKey, in.DependedOnBy)
		in.Node = nodes.node
		if len(in.Related) > before {
			// The firing rates were read before the lock, without these.
			if in.Noisy, err = loadNoisyAlerts(ctx, tx, event, in, start); err != nil {
				return false, err
			}
		}
	}

	// Existing links of every alert involved, by any of its rows — the seed row
	// included: a related alert's triage may have filed it since the check before
	// the lock, and a re-scored head is triaged again with its group under it.
	ownAll := append(append([]string{event.Id}, g.own.rows...), g.own.others...)
	lookup := append([]string{}, ownAll...)
	for _, m := range in.Related {
		lookup = append(lookup, m.Rows...)
	}
	links, err := loadGroupLinks(ctx, tx, *event.CloudAccountId, lookup)
	if err != nil {
		return false, err
	}
	// A related alert that is in its group only on timing is not evidence for
	// the seed (groupLinks.timing). It is left out; the seed is placed by what
	// remains, or goes through the timing test itself.
	related := make([]relatedMember, 0, len(in.Related))
	for _, m := range in.Related {
		if !links.filedOnTiming(m.Rows) {
			related = append(related, m)
		}
	}
	in.Related = related
	// The head an own row is filed under may be an earlier firing of the same
	// alert from before the window; which alert that row belongs to decides
	// whether the seed is its symptom or one more row of it.
	var ownRoots []string
	for _, r := range ownAll {
		if _, filed := links.parent[r]; filed {
			ownRoots = append(ownRoots, links.root(r))
		}
	}
	if _, _, err := loadRows(ctx, tx, ownRoots, g.rowStarts, g.rowAlerts); err != nil {
		return false, err
	}
	sameAlert := func(row string) bool { return g.rowAlerts[row] == in.Seed.Fingerprint }
	var seedFiled, seedIsChild bool
	in.OwnHead, in.OwnLeads, seedFiled, seedIsChild = ownPlacement(links, event.Id, g.own.rows, sameAlert)
	if seedFiled {
		return seedIsChild, nil
	}
	// The seed follows another row of its own firing. If that row was filed on
	// timing alone, so is this one: it is the same firing on the same evidence,
	// and its link must say so, or this row would place the alert's next firing
	// where the first row may not (groupLinks.timing).
	followsTiming := false
	if in.OwnHead != "" {
		for _, r := range g.own.rows {
			if _, filed := links.parent[r]; filed {
				followsTiming = links.timing[r]
				break
			}
		}
	}
	if in.OwnHead == "" && in.OwnLeads == "" {
		placing := links.placingFirings(g.own.others)
		if groups := links.groupsOf(placing); len(groups) > 0 {
			live, err := loadLiveGroups(ctx, tx, event, groups, start)
			if err != nil {
				return false, err
			}
			in.OwnHead, in.OwnLeads = earlierFiringPlacement(links, placing, live, sameAlert)
		}
	}
	in.OwnRep = g.own.repBefore(g.seedCreated, event.Id)
	if in.OwnHead == "" && in.OwnLeads == "" {
		// Like every other alert in the set, the seed's is ranked from, and headed
		// by, its first firing in the window — whichever firing is triaged first.
		if rep, repStart := firstFreeFiring(links, g.own.others, g.rowStarts, start); rep != "" {
			in.OwnRep, in.Seed.StartsAt = rep, repStart
		}
	}
	for i := range in.Related {
		head, leads := links.groupOf(in.Related[i].Rows)
		if head == "" {
			head = leads
		}
		in.Related[i].GroupHead = head
	}
	if len(in.Related) == 0 && in.OwnHead == "" && in.OwnLeads == "" {
		// Nothing the graph or the subject could pair this alert with. Co-timing
		// is the evidence of last resort, so it is only consulted when the graph
		// genuinely has nothing to say — see timingFallbackAllowed. Under the
		// lock, so the incident it joins cannot change heads while it is read.
		if !g.timingFallback {
			return false, nil
		}
		leaderID, offset, reason, ok, err := attachUnlocatedIncident(ctx, tx, event, start)
		if err != nil || !ok {
			return false, err
		}
		if err := insertGroupLink(ctx, tx, event, event.Id, leaderID, offset, 0, reason); err != nil {
			return false, err
		}
		return true, nil
	}
	heads := []string{in.OwnHead, in.OwnLeads, in.OwnRep}
	for _, m := range in.Related {
		heads = append(heads, m.GroupHead)
	}
	if in.HeadStarts, in.HeadAlerts, err = loadRows(ctx, tx, heads, g.rowStarts, g.rowAlerts); err != nil {
		return false, err
	}

	d := decideGroup(in)
	if followsTiming && d.Outcome == outcomeJoin && d.HeadRow == in.OwnHead {
		d.Reason = timingOnlyReason(unlocatedScopeFor(event))
	}
	slog.InfoContext(ctx, "Incident group decision",
		"event_id", event.Id,
		"outcome", d.Outcome.String(),
		"head_event_id", d.HeadRow,
		"replaced_head_event_id", d.MoveFrom,
		"related_alerts", len(in.Related),
		"brought_along", len(d.Bring),
		"seed_noisy", in.Noisy[in.Seed.Fingerprint],
		"seed_depended_on_by", in.DependedOnBy[in.Seed.Fingerprint],
		"why", d.Why,
	)

	offset := time.Duration(0)
	if !d.HeadStart.IsZero() {
		offset = start.Sub(d.HeadStart)
	}
	head, headStart := d.HeadRow, d.HeadStart
	switch d.Outcome {
	case outcomeJoin, outcomeLead:
		if head != "" && head != event.Id {
			if err := insertGroupLink(ctx, tx, event, event.Id, head, offset, d.DepDistance, d.Reason); err != nil {
				return false, err
			}
		} else {
			head, headStart = event.Id, start
		}
	case outcomeTakeOver:
		// The old head becomes a child that started before its head, hence the
		// negated offset.
		if err := moveGroup(ctx, tx, event, d.MoveFrom, d.Reason, -offset, d.DepDistance); err != nil {
			return false, err
		}
		head, headStart = event.Id, start
	default:
		return false, nil
	}
	for _, b := range d.Bring {
		bringOffset := time.Duration(0)
		if !headStart.IsZero() {
			bringOffset = b.Starts.Sub(headStart)
		}
		if err := insertGroupLink(ctx, tx, event, b.Row, head, bringOffset, 0, b.Reason); err != nil {
			return false, err
		}
	}
	return d.SeedIsChild && head != event.Id, nil
}

func (o groupOutcome) String() string {
	switch o {
	case outcomeJoin:
		return "join"
	case outcomeLead:
		return "lead"
	case outcomeTakeOver:
		return "take_over"
	}
	return "none"
}

// alertKey is an alert's identity: its fingerprint, or its own row when it has
// none.
func alertKey(fingerprint, rowID string) string {
	if fingerprint != "" {
		return fingerprint
	}
	return "row:" + rowID
}

// ownRows is the seed's own alert as its other stored rows show it.
type ownRows struct {
	// rows is the other rows of the seed's own firing: the same alert with the
	// same start, from a second ingest path or re-notified.
	rows    []string
	created map[string]time.Time
	// others is the rows of the alert's other firings inside the attach window,
	// newest first.
	others []string
}

// repBefore returns the row that stands for the firing when it leads: the
// earliest stored, if that is not the seed itself. Two rows of one firing — from
// two ingest paths, say — then agree on which of them carries the lead,
// whichever is triaged first.
func (o ownRows) repBefore(seedCreated time.Time, seedID string) string {
	rep := ""
	for _, id := range o.rows {
		c := o.created[id]
		if c.After(seedCreated) || (c.Equal(seedCreated) && id > seedID) {
			continue
		}
		if rep == "" || c.Before(o.created[rep]) || (c.Equal(o.created[rep]) && id < rep) {
			rep = id
		}
	}
	return rep
}

// collapseActiveRows turns stored rows into alerts: one member per fingerprint,
// carrying its current firing. The other rows of the seed's own firing are
// returned separately. Rows must arrive newest start first, then earliest
// stored. Pure.
func collapseActiveRows(rows []activeRow, start time.Time, seedFingerprint string) (ownRows, []*alertMember) {
	own := ownRows{created: map[string]time.Time{}}
	current := map[string]time.Time{} // start of each alert's newest firing
	byKey := map[string]*alertMember{}
	var members []*alertMember
	for _, r := range rows {
		key := alertKey(deref(r.Fingerprint), r.ID)
		if key == seedFingerprint {
			// Another row of the SAME firing is the seed itself. Another firing
			// inside the window is kept apart: on its own it does not place the
			// seed (see earlierFiringPlacement).
			switch {
			case r.StartsAt.Equal(start):
				own.rows = append(own.rows, r.ID)
				own.created[r.ID] = r.CreatedAt
			case !r.StartsAt.Before(start.Add(-IncidentAttachWindow)):
				own.others = append(own.others, r.ID)
			}
			continue
		}
		inWindow := !r.StartsAt.Before(start.Add(-IncidentAttachWindow))
		m, seen := byKey[key]
		if !seen {
			// First row of the key: newest start, earliest stored — the current
			// firing's representative.
			current[key] = r.StartsAt
			m = &alertMember{
				Fingerprint:    key,
				RepRow:         r.ID,
				AggregationKey: deref(r.AggregationKey),
				SubjectKey: SubjectKey(AlertIdentity{
					ID:               r.ID,
					SubjectType:      deref(r.SubjectType),
					SubjectName:      deref(r.SubjectName),
					SubjectNamespace: deref(r.SubjectNamespace),
					SubjectOwner:     deref(r.SubjectOwner),
				}),
				ServiceKey: getServiceKeyFromEvent(&models.Event{
					ServiceKey:       r.ServiceKey,
					SubjectName:      r.SubjectName,
					SubjectNamespace: r.SubjectNamespace,
					SubjectOwner:     r.SubjectOwner,
					SubjectOwnerKind: r.SubjectOwnerKind,
					SubjectType:      r.SubjectType,
				}),
				Subject:   ownerElseName(deref(r.SubjectOwner), deref(r.SubjectName)),
				Namespace: strings.ToLower(strings.TrimSpace(deref(r.SubjectNamespace))),
				StartsAt:  r.StartsAt,
				Recent:    inWindow,
			}
			byKey[key] = m
			members = append(members, m)
		}
		sameFiring := r.StartsAt.Equal(current[key])
		if sameFiring || inWindow {
			m.Rows = append(m.Rows, r.ID)
		}
		if inWindow && r.StartsAt.Before(m.StartsAt) {
			// An earlier firing inside the window: the alert has been part of this
			// burst since then, and that firing's first row stands for it — so the
			// same row heads whichever of its firings a related alert sees first.
			m.RepRow, m.StartsAt = r.ID, r.StartsAt
		}
		if sameFiring &&
			((r.Status == "FIRING" && r.EndsAt == nil) || (r.EndsAt != nil && !r.EndsAt.Before(start))) {
			m.Open = true
		}
	}
	return own, members
}

// relateMembers keeps the active alerts the seed has evidence for. Pure.
//
//   - Same subject, started inside the attach window. (One exception is applied
//     later, once the links are read: an alert that is in its group only on
//     timing does not carry a neighbour on its own subject in with it — see
//     groupLinks.timing.)
//   - On a service within maxIncidentHops of the seed's in either direction,
//     started inside the attach window. If a, b and c are connected and all three
//     are alerting, they are one incident.
//   - Started before the window, still firing, on a service the seed DEPENDS on.
//     Direction is the point: a caller alerting half an hour into its database's
//     outage belongs to that outage, while an alert that has been standing on
//     some caller says nothing about what the service it calls does next. There
//     is no such arm for the seed's own subject — with no direction to go on, a
//     standing alert would collect everything on its workload.
//   - The exception is a configuration change: a change made to an object while
//     an alert on that object is still firing belongs to that incident (it is
//     usually the fix). A change is a point event, never a standing one, and it
//     carries no service map, so no other rule can place it.
//
// graph is nil when the seed has no stored service map or topology grouping is
// off; only the same-subject rule applies then. Reports whether the subject cap
// stopped it adding more.
func relateMembers(graph *DependencyGraph, seed alertMember, seedIsChange bool, members []*alertMember) ([]relatedMember, bool) {
	related := make([]relatedMember, 0, len(members))
	subjects := map[string]bool{}
	capped := false
	for _, m := range members {
		if m.SubjectKey == seed.SubjectKey {
			if m.Recent || (seedIsChange && m.Open) {
				related = append(related, relatedMember{alertMember: *m, Relation: relSameSubject})
			}
			continue
		}
		if graph == nil || seed.ServiceKey == "" || m.ServiceKey == "" || strings.HasSuffix(m.SubjectKey, "|") {
			continue
		}
		rel, hops := relation(0), 0
		switch {
		case m.Recent:
			if h := graph.getDependencyDistance(seed.ServiceKey, m.ServiceKey); h > 0 && h <= maxIncidentHops {
				rel, hops = relConnected, h
			}
		case m.Open:
			if h := graph.dependsOnDistance(seed.ServiceKey, m.ServiceKey, maxIncidentHops); h > 0 {
				rel, hops = relDependency, h
			}
		}
		if rel == 0 {
			continue
		}
		if !subjects[m.SubjectKey] {
			if len(subjects) >= incidentGroupSubjectCap {
				capped = true
				continue
			}
			subjects[m.SubjectKey] = true
		}
		related = append(related, relatedMember{alertMember: *m, Relation: rel, Hops: hops})
	}
	return related, capped
}

// countDependents counts, for the seed and each related alert, how many of the
// others depend on it — the only causal signal the group has. dependsOnDistance
// follows dependencies in one direction, so a database is depended on by the
// services calling it and not the reverse. An alert on the seed's own subject
// sits on the same service and gets the seed's count. Keyed by fingerprint. Pure.
func countDependents(graph *DependencyGraph, seed alertMember, related []relatedMember) map[string]int {
	out := make(map[string]int, len(related)+1)
	if graph == nil || seed.ServiceKey == "" {
		return out
	}
	type participant struct{ fp, svc string }
	ps := []participant{{seed.Fingerprint, seed.ServiceKey}}
	for _, m := range related {
		if m.Relation != relSameSubject && m.ServiceKey != "" {
			ps = append(ps, participant{m.Fingerprint, m.ServiceKey})
		}
	}
	for _, x := range ps {
		for _, y := range ps {
			if x.fp != y.fp && graph.dependsOnDistance(y.svc, x.svc, maxIncidentHops) > 0 {
				out[x.fp]++
			}
		}
	}
	for _, m := range related {
		if m.Relation == relSameSubject {
			out[m.Fingerprint] = out[seed.Fingerprint]
		}
	}
	return out
}

// mergeNodeMembers adds the node axis's members to the related set and its
// counts to dependedOnBy. A node that ran out of memory is what the pods killed
// on it depend on.
func mergeNodeMembers(related []relatedMember, members []*alertMember, nodes nodePool, seed alertMember, seedKey string, dependedOnBy map[string]int) []relatedMember {
	if len(nodes.members) == 0 {
		return related
	}
	have := map[string]bool{}
	for _, m := range related {
		have[m.Fingerprint] = true
	}
	known := map[string]*alertMember{}
	for _, m := range members {
		known[m.Fingerprint] = m
	}
	rowKey := map[string]string{}
	for _, c := range nodes.members {
		key := alertKey(c.Fingerprint, c.ID)
		rowKey[c.ID] = key
		if have[key] {
			continue
		}
		have[key] = true
		am := alertMember{Fingerprint: key, RepRow: c.ID, Rows: []string{c.ID}, AggregationKey: c.AggregationKey, StartsAt: c.StartsAt, Recent: true}
		if k, ok := known[key]; ok {
			am = *k
		}
		related = append(related, relatedMember{alertMember: am, Relation: relNode})
	}
	for id, n := range nodes.dependedOnBy {
		switch {
		case id == seedKey:
			dependedOnBy[seed.Fingerprint] += n
		case rowKey[id] != "":
			dependedOnBy[rowKey[id]] += n
		}
	}
	return related
}

// groupLinks is the existing same_incident links around a set of rows.
type groupLinks struct {
	parent map[string]string // child row -> leader row
	leads  map[string]bool   // rows that have children
	// timing is the child rows filed on timing alone. Such a link says that
	// firing started with the incident, and nothing else: not that the alert's
	// next firing belongs there, and not that another alert on the same subject
	// does. So nothing is placed THROUGH it — not a later firing of the same
	// alert (placingFirings), not a related alert (filedOnTiming). Whatever else
	// belongs to the incident on timing has to pass the timing test itself.
	// Otherwise an alarm that fails every ten minutes, once in, follows itself
	// into the group for as long as anything else in it keeps firing, and an
	// alert that started a quarter of an hour into an incident is filed under it
	// because a neighbour of its own happened to start with it.
	timing map[string]bool
}

// filedOnTiming reports whether the alert these rows belong to is in its group
// on timing alone: the row that places it (the first filed, as in groupOf) has
// a timing link.
func (l groupLinks) filedOnTiming(rows []string) bool {
	for _, r := range rows {
		if _, ok := l.parent[r]; ok {
			return l.timing[r]
		}
	}
	return false
}

// placingFirings returns, of an alert's other firings, the ones whose place can
// place a new firing of it: all but those filed on timing alone. Order kept.
func (l groupLinks) placingFirings(others []string) []string {
	if len(l.timing) == 0 {
		return others
	}
	kept := make([]string, 0, len(others))
	for _, r := range others {
		if !l.timing[r] {
			kept = append(kept, r)
		}
	}
	return kept
}

// groupOf reports the group an alert is in, from any of its rows: the head row
// it is filed under (followed to the root), and the own row that heads a group.
// At most one is set; being filed wins.
func (l groupLinks) groupOf(rows []string) (filedUnder, leadsRow string) {
	for _, r := range rows {
		if _, ok := l.parent[r]; ok {
			return l.root(r), ""
		}
	}
	for _, r := range rows {
		if l.leads[r] {
			return "", r
		}
	}
	return "", ""
}

// groupsOf lists the head rows of the groups the given rows are in, as members
// or as heads, each once, in the rows' order.
func (l groupLinks) groupsOf(rows []string) []string {
	var heads []string
	seen := map[string]bool{}
	for _, r := range rows {
		head := ""
		if _, filed := l.parent[r]; filed {
			head = l.root(r)
		} else if l.leads[r] {
			head = r
		}
		if head != "" && !seen[head] {
			seen[head] = true
			heads = append(heads, head)
		}
	}
	return heads
}

// earlierFiringPlacement reads where an earlier firing of the seed's alert,
// inside the attach window, leaves the seed. That firing's group counts only
// while it is live — another alert in it has fired inside the window (live, by
// head row). Then the seed belongs where its alert already is: under that
// group's head, or as one more row of the head when the head is the alert
// itself. others is newest firing first; the first live group wins. Pure.
//
// Without the liveness test an alert that fires every ten minutes would file
// each firing under the one before and keep a single incident open for as long
// as it flaps. With no placement at all, a second change to an object — or a
// replaced head firing again — would count as new to its own incident, stand
// outside it, or take the lead back.
func earlierFiringPlacement(links groupLinks, others []string, live map[string]bool, sameAlert func(row string) bool) (ownHead, ownLeads string) {
	for _, head := range links.groupsOf(others) {
		if !live[head] {
			continue
		}
		if sameAlert != nil && sameAlert(head) {
			return "", head
		}
		for _, r := range others {
			if r == head {
				return "", head
			}
		}
		return head, ""
	}
	return "", ""
}

// firstFreeFiring returns the earliest firing of the seed's alert that started
// inside the window before the seed's own and is in no group, and its start; ""
// when there is none. others is newest start first, earliest stored first within
// a start. A firing already in a group is skipped: if that group were still
// open the seed would have been placed in it, and linking to a row filed
// elsewhere would make a chain. Pure.
func firstFreeFiring(links groupLinks, others []string, starts map[string]time.Time, seedStart time.Time) (string, time.Time) {
	rep, repStart := "", time.Time{}
	for _, r := range others {
		st := starts[r]
		if !st.Before(seedStart) {
			continue
		}
		if _, filed := links.parent[r]; filed || links.leads[r] {
			continue
		}
		if rep == "" || st.Before(repStart) {
			rep, repStart = r, st
		}
	}
	return rep, repStart
}

// loadLiveGroups reports which of the given groups (by head row) another alert
// than the seed's is keeping open: the head itself, or a member, belongs to a
// different alert and started inside the attach window around the seed.
func loadLiveGroups(ctx context.Context, tx *sqlx.Tx, event *models.Event, heads []string, start time.Time) (map[string]bool, error) {
	live := map[string]bool{}
	var got []string
	if err := sqlx.SelectContext(ctx, tx, &got, `
		SELECT g.head::text
		FROM unnest($1::uuid[]) AS g(head)
		WHERE EXISTS (SELECT 1 FROM events h
		              WHERE h.id = g.head
		                AND h.fingerprint IS DISTINCT FROM $2
		                AND h.starts_at >= $3 AND h.starts_at <= $4)
		   OR EXISTS (SELECT 1
		              FROM event_correlations c
		              JOIN events m ON m.id = c.event_id
		              WHERE c.correlation_type = $5
		                AND c.cloud_account_id = $6
		                AND c.related_event_id = g.head
		                AND m.fingerprint IS DISTINCT FROM $2
		                AND m.starts_at >= $3 AND m.starts_at <= $4)`,
		pq.Array(heads), event.Fingerprint,
		start.Add(-IncidentAttachWindow), start.Add(IncidentAttachWindow),
		SameIncidentCorrelationType, *event.CloudAccountId); err != nil {
		return nil, fmt.Errorf("failed to check which of the alert's earlier groups are still open: %w", err)
	}
	for _, h := range got {
		live[h] = true
	}
	return live, nil
}

func (l groupLinks) root(row string) string {
	for i := 0; i < maxLeaderFollow+1; i++ {
		p, ok := l.parent[row]
		if !ok || p == row {
			break
		}
		row = p
	}
	return row
}

// loadGroupLinks reads the links the given rows are part of, as children or as
// leaders, and follows leaders that are themselves children so groupOf can
// answer with a root. A child with two parents (links written before the lock
// existed) resolves to the lower id, as the inbox query does.
func loadGroupLinks(ctx context.Context, tx *sqlx.Tx, accountID string, rows []string) (groupLinks, error) {
	links := groupLinks{parent: map[string]string{}, leads: map[string]bool{}, timing: map[string]bool{}}
	type linkRow struct {
		Child  string `db:"event_id"`
		Leader string `db:"related_event_id"`
		Reason string `db:"correlation_reason"`
	}
	add := func(got []linkRow) []string {
		var leaders []string
		for _, g := range got {
			if cur, ok := links.parent[g.Child]; !ok || g.Leader < cur {
				links.parent[g.Child] = g.Leader
				links.timing[g.Child] = strings.HasPrefix(g.Reason, timingOnlyReasonPrefix)
			}
			links.leads[g.Leader] = true
			leaders = append(leaders, g.Leader)
		}
		return leaders
	}
	if len(rows) == 0 {
		return links, nil
	}
	var got []linkRow
	if err := sqlx.SelectContext(ctx, tx, &got, `
		SELECT event_id, related_event_id, coalesce(correlation_reason, '') AS correlation_reason
		FROM event_correlations
		WHERE correlation_type = $1
		  AND cloud_account_id = $2
		  AND (event_id = ANY($3) OR related_event_id = ANY($3))`,
		SameIncidentCorrelationType, accountID, pq.Array(rows)); err != nil {
		return links, fmt.Errorf("failed to load existing group links: %w", err)
	}
	asked := map[string]bool{}
	for _, r := range rows {
		asked[r] = true
	}
	pending := add(got)
	for round := 0; round < maxLeaderFollow; round++ {
		var next []string
		for _, l := range pending {
			if !asked[l] {
				asked[l] = true
				next = append(next, l)
			}
		}
		if len(next) == 0 {
			break
		}
		got = nil
		if err := sqlx.SelectContext(ctx, tx, &got, `
			SELECT event_id, related_event_id, coalesce(correlation_reason, '') AS correlation_reason
			FROM event_correlations
			WHERE correlation_type = $1
			  AND cloud_account_id = $2
			  AND event_id = ANY($3)`,
			SameIncidentCorrelationType, accountID, pq.Array(next)); err != nil {
			return links, fmt.Errorf("failed to follow group links to their root: %w", err)
		}
		pending = add(got)
	}
	return links, nil
}

// loadRows returns, for each of the given rows, its start and the alert it
// belongs to. Rows read around the seed are answered from knownStarts /
// knownAlerts; a row from before the window is looked up and remembered there.
func loadRows(ctx context.Context, tx *sqlx.Tx, want []string, knownStarts map[string]time.Time, knownAlerts map[string]string) (map[string]time.Time, map[string]string, error) {
	starts, alerts := map[string]time.Time{}, map[string]string{}
	var missing []string
	for _, h := range want {
		if h == "" {
			continue
		}
		if ts, ok := knownStarts[h]; ok {
			starts[h], alerts[h] = ts, knownAlerts[h]
		} else if _, asked := starts[h]; !asked {
			starts[h] = time.Time{}
			missing = append(missing, h)
		}
	}
	if len(missing) == 0 {
		return starts, alerts, nil
	}
	type headRow struct {
		ID          string    `db:"id"`
		StartsAt    time.Time `db:"starts_at"`
		Fingerprint string    `db:"fingerprint"`
	}
	var got []headRow
	if err := sqlx.SelectContext(ctx, tx, &got,
		`SELECT id, starts_at, coalesce(fingerprint, '') AS fingerprint FROM events WHERE id = ANY($1)`,
		pq.Array(missing)); err != nil {
		return nil, nil, fmt.Errorf("failed to load group head rows: %w", err)
	}
	for _, g := range got {
		starts[g.ID], alerts[g.ID] = g.StartsAt, alertKey(g.Fingerprint, g.ID)
		knownStarts[g.ID], knownAlerts[g.ID] = starts[g.ID], alerts[g.ID]
	}
	return starts, alerts, nil
}

// loadNoisyAlerts marks, by fingerprint, the alerts whose subject+alert-type pair
// has fired chronicFiringThreshold or more times in the trailing week.
//
// It counts FIRINGS, not stored rows. A re-notification of an alert that is still
// firing is another row with the same fingerprint and start, so counting rows made
// one long outage look like a flapper: a database down for ten hours and
// re-notified hourly crossed the threshold on its own and could not head the next
// incident it caused.
func loadNoisyAlerts(ctx context.Context, db sqlx.ExtContext, event *models.Event, in decideInput, start time.Time) (map[string]bool, error) {
	noisy := map[string]bool{}
	pair := func(m alertMember) string { return m.Namespace + "|" + m.Subject + "|" + m.AggregationKey }
	subjects := map[string]bool{}
	all := []alertMember{in.Seed}
	for _, m := range in.Related {
		all = append(all, m.alertMember)
	}
	for _, m := range all {
		if m.Subject != "" {
			subjects[m.Subject] = true
		}
	}
	if len(subjects) == 0 {
		return noisy, nil
	}
	list := make([]string, 0, len(subjects))
	for s := range subjects {
		list = append(list, s)
	}
	type rate struct {
		Namespace      string `db:"ns"`
		Subject        string `db:"subj"`
		AggregationKey string `db:"aggregation_key"`
		Firings        int    `db:"firings"`
	}
	var rates []rate
	if err := sqlx.SelectContext(ctx, db, &rates, `
		SELECT lower(coalesce(btrim(subject_namespace), '')) AS ns,
		       lower(coalesce(nullif(btrim(subject_owner), ''), btrim(subject_name))) AS subj,
		       aggregation_key,
		       count(DISTINCT (fingerprint, starts_at)) AS firings
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = $2
		  AND lower(coalesce(nullif(btrim(subject_owner), ''), btrim(subject_name))) = ANY($3)
		  AND starts_at >= $4 AND starts_at < $5
		  AND aggregation_key IS NOT NULL
		GROUP BY 1, 2, 3`,
		*event.Tenant, *event.CloudAccountId, pq.Array(list),
		start.Add(-ChronicLookback), start); err != nil {
		return nil, fmt.Errorf("failed to load firing rates: %w", err)
	}
	firings := make(map[string]int, len(rates))
	for _, r := range rates {
		firings[r.Namespace+"|"+r.Subject+"|"+r.AggregationKey] = r.Firings
	}
	for _, m := range all {
		if firings[pair(m)] >= ChronicWeeklyThreshold {
			noisy[m.Fingerprint] = true
		}
	}
	return noisy, nil
}

// moveGroup makes the seed the head of the group headed by oldHead: every child
// of that head is repointed at the seed, and the old head becomes one more child.
//
// The whole star moves. A partial move would leave children under a head that is
// itself a child, and every reader assumes one hop. Moved links keep their
// reason: it says why the child is in the group, which has not changed, and the
// node axis reads its own prefix back out of it. Their offset is shifted to the
// new head's start; their dependency distance is left as recorded against the
// old one, since the distance to the new head is not known for a child the
// seed's own map does not reach. Runs under the account lock.
func moveGroup(ctx context.Context, tx *sqlx.Tx, event *models.Event, oldHead, reason string, offset time.Duration, depDistance int) error {
	account := *event.CloudAccountId
	res, err := tx.ExecContext(ctx, `
		UPDATE event_correlations c
		SET related_event_id = $4,
		    time_offset_minutes = c.time_offset_minutes + $5
		WHERE c.correlation_type = $1 AND c.cloud_account_id = $2
		  AND c.related_event_id = $3
		  AND c.event_id <> $4
		  AND NOT EXISTS (SELECT 1
		                  FROM event_correlations d
		                  WHERE d.correlation_type = $1 AND d.cloud_account_id = $2
		                    AND d.related_event_id = $4 AND d.event_id = c.event_id)`,
		SameIncidentCorrelationType, account, oldHead, event.Id, int(offset.Minutes()),
	)
	if err != nil {
		return fmt.Errorf("failed to move incident group to its new head: %w", err)
	}
	moved, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM event_correlations
		WHERE correlation_type = $1 AND cloud_account_id = $2 AND related_event_id = $3`,
		SameIncidentCorrelationType, account, oldHead,
	); err != nil {
		return fmt.Errorf("failed to clear the replaced incident head's links: %w", err)
	}
	if err := insertGroupLink(ctx, tx, event, oldHead, event.Id, offset, depDistance, reason); err != nil {
		return err
	}
	slog.InfoContext(ctx, "Incident group leader replaced",
		"event_id", event.Id,
		"replaced_leader_event_id", oldHead,
		"children_moved", moved,
		"reason", reason,
	)
	return nil
}

// inGroupTx runs fn in a transaction: a new one when db is the pool, the caller's
// own when db already is one (the E2E tests run inside a rolled-back transaction).
func inGroupTx(ctx context.Context, db sqlx.ExtContext, fn func(tx *sqlx.Tx) error) error {
	switch d := db.(type) {
	case *sqlx.Tx:
		return fn(d)
	case *sqlx.DB:
		tx, err := d.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin incident group transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	return fmt.Errorf("incident group transaction: unsupported database handle %T", db)
}

// insertGroupLink writes one child -> leader star edge.
func insertGroupLink(ctx context.Context, db sqlx.ExtContext, event *models.Event, childID, leaderID string, offset time.Duration, depDistance int, reason string) error {
	if childID == leaderID {
		return nil // a chain never links to itself
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO event_correlations (
			event_id, related_event_id, cloud_account_id, tenant_id,
			correlation_type, correlation_score, correlation_reason,
			time_offset_minutes, dependency_distance
		) VALUES ($1, $2, $3, $4, $5, 1.0, $6, $7, $8)
		ON CONFLICT DO NOTHING`,
		childID, leaderID, *event.CloudAccountId, event.Tenant,
		SameIncidentCorrelationType, reason, int(offset.Minutes()), depDistance,
	)
	if err != nil {
		return fmt.Errorf("failed to insert same_incident link: %w", err)
	}
	slog.InfoContext(ctx, "Attached event to incident group",
		"event_id", event.Id,
		"chain_event_id", childID,
		"leader_event_id", leaderID,
		"reason", reason,
		"offset_minutes", int(offset.Minutes()),
	)
	return nil
}

// topologyGroupingEnabled defaults to true; only an explicit "false"/"0"
// disables — the cross-service kill switch, independent of the same-subject
// one so either evidence family can be turned off alone.
func topologyGroupingEnabled() bool {
	v := strings.TrimSpace(os.Getenv(topologyGroupingEnvFlag))
	return !strings.EqualFold(v, "false") && v != "0"
}

// unlocatedGroupingEnabled defaults to true; only an explicit "false"/"0"
// disables, matching the other two grouping switches — a default-off flag
// nobody flips leaves the path dead in config.
func unlocatedGroupingEnabled() bool {
	v := strings.TrimSpace(os.Getenv(unlocatedGroupingEnvFlag))
	return !strings.EqualFold(v, "false") && v != "0"
}

// attachUnlocatedIncident is the last resort for an alert that the graph cannot
// place at all: a CloudWatch alarm on a custom namespace dimensioned by a
// logical service ("Service=nginx") resolves to no node, gets no service-map
// evidence, and is therefore invisible to both the same-subject and the
// connected-set pools. It is not that such an alert belongs to nothing — the one
// that prompted this ran on a host whose load balancer and target groups were
// alarming in the same two minutes — it is that we have no edge to prove it.
//
// So this groups on co-timing. That is the weakest claim this file makes, and it
// runs by default, so the narrowness lives in the rule: it refuses every case
// where co-timing is not decisive.
//
//   - It runs only when the seed reached this point with no members of its own,
//     and only for an alert the graph could not place (timingFallbackAllowed).
//   - It joins an EXISTING incident; it never starts one. A group founded on an
//     alert we cannot locate would have nothing holding it together.
//   - The incident must be in the seed's scope (see unlocatedScopeFor). For a
//     Kubernetes alert that is its own namespace. Co-timing says nothing about
//     which namespace a story belongs to, and without this the path merged
//     whatever else the account happened to be alerting about: measured on prod
//     over 7 days, 11,633 of its 15,632 links (74%) crossed a namespace, one
//     ImagePullBackOff group reaching 1,854 members across 29 namespaces.
//     Cross-namespace grouping is still reachable — through a service-map edge,
//     in relateMembers, where there is evidence for it.
//   - The incident must have STARTED with the seed: its earliest alert — the
//     head or anything filed under it — started within the scope's window of the
//     seed, before or after. "Fired together" is evidence about how an incident
//     began. It used to be enough that any member had fired in the last fifteen
//     minutes, which every new member renewed: one alarm failing every ten
//     minutes kept a group open for hours, the group stayed the only one open,
//     and every unplaced alert in the account was filed under it, whatever
//     resource it was on. An incident that began earlier is not a candidate at
//     all, so it neither attracts the seed nor stops it joining one that has
//     just begun.
//   - Exactly one such incident in that scope, or it declines. Two incidents
//     means co-timing does not identify which one, and picking either would
//     attach the alert to a story it may have no part in — the same reason
//     resourceDimensionIndex drops a dimension its namespaces disagree about.
//
// A link written here places the firing it was written for and no other: see
// groupLinks.timing.
//
// Returns the leader to attach to, the reason to record, and whether to attach.
func attachUnlocatedIncident(ctx context.Context, db sqlx.ExtContext, event *models.Event, start time.Time) (string, time.Duration, string, bool, error) {
	// The account guard is redundant against today's only caller, which rejects
	// an event without one before it gets this far. Repeated anyway because the
	// query below is account-scoped and nothing else bounds it: reached with an
	// empty account it would scan every correlation in the table and answer
	// about someone else's incidents.
	if !unlocatedGroupingEnabled() || event == nil ||
		event.CloudAccountId == nil || *event.CloudAccountId == "" {
		return "", 0, "", false, nil
	}

	// Leaders of the groups an alert that started near the seed belongs to, as a
	// member or as the head, kept when the whole group is that young: the head
	// started no earlier than the window, and nothing filed under it did either.
	// The near alert is what bounds the read — a group is only ever looked at
	// through the few alerts of the account that started around the seed, never
	// through its whole history. Restricted to the seed's own account: co-timing
	// across accounts is not even weak evidence, it is coincidence.
	//
	// The scope is matched on the LEADER. The leader is the story this alert
	// would be filed under, and it is the row the operator reads.
	//
	// LIMIT 2 is the whole question. The rule below attaches on exactly one
	// incident and declines otherwise, so "two" and "two hundred" are the same
	// answer and there is no reason to fetch the difference.
	//
	// A leader that has been replaced (moveGroup) is not an incident of its own,
	// even if an attach that raced the takeover left an alert filed under it.
	// Counting it would make one incident look like two and decline every
	// unlocated alert until the window closed.
	scope := unlocatedScopeFor(event)
	type leaderRow struct {
		ID       string    `db:"id"`
		StartsAt time.Time `db:"starts_at"`
	}
	var leaders []leaderRow
	err := sqlx.SelectContext(ctx, db, &leaders, `
		WITH near AS (
			SELECT n.id
			FROM events n
			WHERE n.cloud_account_id = $2
			  AND n.starts_at >= $3
			  AND n.starts_at <= $4
		), heads AS (
			SELECT ec.related_event_id AS id
			FROM event_correlations ec
			JOIN near ON near.id = ec.event_id
			WHERE ec.correlation_type = $1
			  AND ec.cloud_account_id = $2
			UNION
			SELECT ec.related_event_id
			FROM event_correlations ec
			JOIN near ON near.id = ec.related_event_id
			WHERE ec.correlation_type = $1
			  AND ec.cloud_account_id = $2
		)
		SELECT l.id, l.starts_at
		FROM heads h
		JOIN events l ON l.id = h.id
		WHERE l.starts_at >= $3
		  AND `+scope.leaderPredicate+`
		  AND NOT EXISTS (SELECT 1
		                  FROM event_correlations r
		                  WHERE r.correlation_type = $1
		                    AND r.cloud_account_id = $2
		                    AND r.event_id = l.id
		                    AND r.correlation_reason LIKE $6)
		  AND NOT EXISTS (SELECT 1
		                  FROM event_correlations o
		                  JOIN events om ON om.id = o.event_id
		                  WHERE o.correlation_type = $1
		                    AND o.cloud_account_id = $2
		                    AND o.related_event_id = l.id
		                    AND om.starts_at < $3)
		LIMIT 2`,
		SameIncidentCorrelationType, *event.CloudAccountId,
		start.Add(-scope.window), start.Add(scope.window), scope.arg, leaderTakeoverReasonPrefix+"%",
	)
	if err != nil {
		return "", 0, "", false, fmt.Errorf("failed to load young incidents for unlocated attach: %w", err)
	}

	if len(leaders) != 1 {
		// 0: nothing began with the seed in this scope. >1: co-timing cannot
		// choose between them.
		if len(leaders) > 1 {
			slog.InfoContext(ctx, "Unlocated alert not grouped: several incidents began in the window",
				"event_id", event.Id, "scope", scope.label, "incidents", len(leaders))
		}
		return "", 0, "", false, nil
	}

	return leaders[0].ID, start.Sub(leaders[0].StartsAt), timingOnlyReason(scope), true, nil
}

// timingOnlyReason is the reason recorded on a link made on timing alone.
func timingOnlyReason(scope unlocatedScope) string {
	return fmt.Sprintf("%s%s incident that began within %d minutes of this alert",
		timingOnlyReasonPrefix, scope.label, int(scope.window.Minutes()))
}

// timingFallbackAllowed decides whether an alert that found no subject or
// topology partner may be grouped on co-timing at all.
//
// Co-timing is the fallback for an alert the graph cannot place, not for one it
// placed and found nothing alerting next to. When the knowledge graph knows the
// subject, its edges are the evidence: no alerting neighbour means no group.
// Measured on dev over 7 days, 60% of co-timing links came from seeds that had
// a stored service map and simply had quiet neighbours.
//
//   - Configuration changes never use it. They are not enriched, so they never
//     carry a map, but they are always Kubernetes objects the graph knows. A
//     rollout redeploys a whole namespace at once and every change event would
//     otherwise join whatever incident is open there — 22 of 23 co-timing
//     members of one dev group were deploy events. Changes are surfaced on the
//     incident timeline instead; a change on the same workload or a connected
//     one still groups through the subject and topology paths.
//   - AWS alarms always may. The graph often cannot connect them even when it
//     places them, and firing together in one account is the evidence left.
//   - Everything else may only when it has no position in the graph.
func timingFallbackAllowed(event *models.Event, located bool) bool {
	if isConfigurationChange(event) {
		return false
	}
	if isAWSAlarmSource(event.Source) {
		return true
	}
	return !located
}

func isConfigurationChange(event *models.Event) bool {
	return event.FindingType != nil && strings.EqualFold(strings.TrimSpace(*event.FindingType), "configuration_change")
}

// awsAlarmSources are the event sources that carry AWS alarms. Same set
// observability/eventrule_actions_logs.go treats as AWS.
var awsAlarmSources = []string{"AWS_CloudWatch_Alarm", "AWS_EventBridge"}

func isAWSAlarmSource(source *string) bool {
	if source == nil {
		return false
	}
	for _, s := range awsAlarmSources {
		if *source == s {
			return true
		}
	}
	return false
}

// unlocatedScope is the boundary co-timing may not cross: a SQL predicate on the
// leader row `l`, bound to $5, a label for logs and link reasons, and how far
// from an incident's earliest alert the seed may have started. The wider the
// boundary, the shorter that is.
type unlocatedScope struct {
	leaderPredicate string
	arg             any
	label           string
	window          time.Duration
}

// unlocatedScopeFor picks the boundary for an alert the graph could not place.
//
// A Kubernetes alert stays inside its namespace — the one boundary it always
// carries even when the graph cannot place it.
//
// An AWS alarm stays inside AWS, but may cross AWS services. Its namespace field
// holds the service code (AmazonEC2, AWSELB, AmazonRDS), so a namespace match
// would keep an ELB alarm away from the instances behind it — which is the case
// this path exists for: AWS alarms routinely have no graph position (a
// CloudWatch alarm on a custom namespace carries no instance id), and firing
// together in one account is the only evidence available. It never joins a
// Kubernetes-led incident; nothing ties the two but the clock.
//
// Keyed on source, not on the shape of the namespace: "amazon-cloudwatch" is a
// real Kubernetes namespace. Azure and GCP alarms are not included — Azure VM
// heartbeat alerts fan out across dozens of VMs at once on one tenant, and
// widening them wants its own measurement — so they keep the namespace rule,
// which for them means one provider service.
func unlocatedScopeFor(event *models.Event) unlocatedScope {
	if isAWSAlarmSource(event.Source) {
		return unlocatedScope{
			leaderPredicate: "l.source = ANY($5)",
			arg:             pq.Array(awsAlarmSources),
			label:           "AWS",
			window:          timingOnlyCloudWindow,
		}
	}
	// A node alert has no namespace, so the namespace predicate below would put
	// every node alert in the cluster into one shared "" bucket and co-time-attach
	// them to each other. The node is the boundary that alert actually carries.
	if node := strings.TrimSpace(deref(event.SubjectNode)); node != "" && strings.EqualFold(strings.TrimSpace(deref(event.SubjectType)), "node") {
		return unlocatedScope{
			leaderPredicate: "lower(coalesce(btrim(l.subject_node), '')) = $5",
			arg:             strings.ToLower(node),
			label:           fmt.Sprintf("node %q", node),
			window:          IncidentAttachWindow,
		}
	}
	ns, _ := chronicSubjectIdentity(event)
	return unlocatedScope{
		leaderPredicate: "lower(coalesce(btrim(l.subject_namespace), '')) = $5",
		arg:             ns,
		label:           fmt.Sprintf("namespace %q", ns),
		window:          IncidentAttachWindow,
	}
}

// ownerElseName mirrors the SQL identity the chronic rate query groups on:
// lower(coalesce(nullif(btrim(subject_owner), ”), btrim(subject_name))). Trims
// before the emptiness test so a whitespace-only owner falls back to the name.
func ownerElseName(owner, name string) string {
	if s := strings.ToLower(strings.TrimSpace(owner)); s != "" {
		return s
	}
	return strings.ToLower(strings.TrimSpace(name))
}
