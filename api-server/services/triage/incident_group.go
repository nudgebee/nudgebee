package triage

import (
	"context"
	"database/sql"
	"errors"
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
// leader is the group's earliest non-chronic alert; every child points straight
// at the leader (a star, never child -> child), so resolving a group is one hop
// — the same shape event_duplicates uses per-fingerprint via first_event_id,
// applied across fingerprints on one subject.
//
// Membership is evidence-based, not scored: same SubjectKey, inside a rolling
// attach window, non-chronic. Re-fires never attach — only dedup-chain leaders
// (occurrence 1) participate, so a group member represents its whole chain.
// Chronic pairs (>= ChronicWeeklyThreshold firings/week, chronic.go) neither
// lead nor extend a group: a flapper must not become an immortal leader
// vacuuming up everything on its subject.
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
	// incidentCandidateLimit bounds the window fetch; one subject+namespace
	// rarely has more than a handful of distinct fingerprints in 90 minutes.
	incidentCandidateLimit = 200
	// maxIncidentHops is how far a connected service can sit from the one that
	// started the group. One hop misses a calling c through b, which is the shape
	// of the reported case (payment and inventory both call order, and all three
	// call database). More than two swallows an estate: node degree on the
	// Rackspace tenant is median 1 but one node has 1,047 edges.
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

// groupCandidate is the projection of a window event the attach decision needs.
type groupCandidate struct {
	ID             string
	AggregationKey string
	// StartsAt is the fingerprint's earliest start in the window — the chain
	// leader's, which is the row that can carry group links.
	StartsAt time.Time
	// LastSeen is the fingerprint's newest start in the window: re-fires keep
	// a group's attach timer alive even though only chain leaders join.
	LastSeen time.Time
}

// decideSameSubjectAttach picks the leader the seed should attach to, or
// reports there is none. Pure — all I/O happens in the caller.
//
//   - members: chains sharing the seed's SubjectKey, or connected to it, that
//     FIRED within [seed - IncidentAttachWindow, seed). Membership is liveness,
//     not arrival time: a group is live while something in it is still firing.
//   - edges: existing same_incident links among the members (child -> leader;
//     the leader itself may be older than the member window).
//   - leaderStarts: starts_at for every distinct edge target.
//   - chronicPairs: the subject's aggregation_keys whose trailing rate is
//     chronic. They do NOT decide membership — a chronic alert firing alongside
//     others on one subject is part of that incident and an operator wants to see
//     it. They only lose the leader election, so a flapper never becomes the
//     headline. Measured on the Rackspace tenant: gating membership on the rate
//     left two of three reported machines unable to form a group at all, because
//     every alert on them was chronic, and the third qualified only because one
//     counter sat at 9 against a threshold of 10.
func decideSameSubjectAttach(
	seed groupCandidate,
	members []groupCandidate,
	edges map[string]string,
	leaderStarts map[string]time.Time,
	chronicPairs map[string]bool,
	dependedOnBy map[string]int,
) (string, time.Duration, bool) {
	// Every member reaching here fired inside the attach window, so every group
	// they belong to is live by construction. There is no staleness test any more:
	// a group ends when everything in it goes quiet, which this query expresses by
	// simply not returning it. The old test asked whether the LEADER started
	// recently, which would have rejected exactly the case this change exists for
	// — the reported chains lead from events several days old and are still firing.
	liveLeaders := map[string]bool{}
	for _, m := range members {
		if l, linked := edges[m.ID]; linked {
			liveLeaders[l] = true
		}
	}

	// Chronic members count as activity — see the doc comment; they are excluded
	// from LEADING below, not from belonging.
	active := make([]groupCandidate, 0, len(members))
	active = append(active, members...)
	if len(active) == 0 {
		return "", 0, false
	}

	// The attach timer runs on last activity, not first: a member's re-fires
	// (LastSeen) hold the group open even though only chain leaders join.
	newest := time.Time{}
	for _, m := range active {
		seen := m.LastSeen
		if seen.IsZero() {
			seen = m.StartsAt
		}
		if seen.After(newest) {
			newest = seen
		}
	}
	if seed.StartsAt.Sub(newest) > IncidentAttachWindow {
		return "", 0, false
	}

	// An existing live group wins; with several (shouldn't happen, but links
	// written concurrently can race), the earliest-started leader is the
	// deterministic choice.
	if len(liveLeaders) > 0 {
		var leader string
		for l := range liveLeaders {
			if leader == "" ||
				leaderStarts[l].Before(leaderStarts[leader]) ||
				(leaderStarts[l].Equal(leaderStarts[leader]) && l < leader) {
				leader = l
			}
		}
		return leader, seed.StartsAt.Sub(leaderStarts[leader]), true
	}

	// No group yet: elect a leader.
	//
	// A non-chronic member always beats a chronic one, so a flapper never becomes
	// the headline of an incident it merely accompanies. Within that class the
	// member the most others DEPEND ON leads: an alert on a thing its neighbours
	// need is the cause, and the alerts on the things that need it are the
	// symptoms. Timing cannot express that and gets it backwards on the ordinary
	// shape of an outage — a load balancer reports errors before a health check
	// notices the backend behind it is down, so earliest-start makes the 5xx the
	// headline and files "the service is down" underneath it.
	//
	// Start time only breaks a tie between members nothing distinguishes
	// structurally (the common same-subject case, where every member scores 0),
	// and the id breaks that in turn so the choice is deterministic under
	// concurrent attaches. When every member is chronic one of them still leads —
	// the group exists, it just ranks low.
	sort.Slice(active, func(i, j int) bool {
		ci, cj := chronicPairs[active[i].AggregationKey], chronicPairs[active[j].AggregationKey]
		if ci != cj {
			return !ci
		}
		if di, dj := dependedOnBy[active[i].ID], dependedOnBy[active[j].ID]; di != dj {
			return di > dj
		}
		if !active[i].StartsAt.Equal(active[j].StartsAt) {
			return active[i].StartsAt.Before(active[j].StartsAt)
		}
		return active[i].ID < active[j].ID
	})
	leader := active[0]
	return leader.ID, seed.StartsAt.Sub(leader.StartsAt), true
}

// attachSameSubjectIncident links a just-triaged chain-leader event to its
// subject's open group, if one exists. Failures are returned for logging but
// must never fail triage — grouping is additive.
//
// Reports whether the event attached as a group CHILD. Scoring consumes that
// directly rather than re-reading event_correlations: the caller writes the
// link here, in Step 3b, and scores in Step 4, so a second query would only
// re-read what this function just decided — and, because the legacy pairwise
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
	ns, roughSubj := chronicSubjectIdentity(event)
	if roughSubj == "" || strings.HasSuffix(seedKey, "|") {
		return false, nil // no subject identity — cluster-scoped alerts never group here
	}
	start := *event.StartsAt

	// Group links hang off the chain's FIRST event, so one alert contributes one
	// member however many times it fires. Resolving it here is also the gate that
	// replaces the old occurrence==1 check: no chain row means we do not know this
	// alert's identity, and guessing would let every copy of one alert look like a
	// separate member.
	var seedChainID string
	err := sqlx.GetContext(ctx, db, &seedChainID,
		`SELECT first_event_id FROM event_duplicates WHERE event_id = $1 AND cloud_account_id = $2`,
		event.Id, *event.CloudAccountId)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil // no chain yet — nothing to hang a link off
	case err != nil:
		// Anything else is an operational failure. Returning it gets it logged by
		// the caller; treating it as "no chain" would disable grouping silently
		// for as long as the database was unhappy.
		return false, fmt.Errorf("failed to resolve seed chain: %w", err)
	case seedChainID == "":
		return false, nil
	}

	// One grouped query gives every pair's trailing rate on this subject: the
	// seed's own chronic gate plus the chronic flags for candidate pairs. Uses
	// the same raw owner-else-name identity as LoadChronicStats (hash-suffixed
	// ownerless pods undercount, which errs toward "not chronic" — safe).
	type pairRate struct {
		AggregationKey string `db:"aggregation_key"`
		Weekly         int    `db:"weekly"`
	}
	var rates []pairRate
	err = sqlx.SelectContext(ctx, db, &rates, `
		SELECT aggregation_key,
		       count(*) AS weekly
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = $2
		  AND lower(coalesce(nullif(btrim(subject_owner), ''), btrim(subject_name))) = $3
		  AND lower(coalesce(btrim(subject_namespace), '')) = $4
		  AND starts_at >= $5 AND starts_at < $6
		  AND id != $7
		  AND aggregation_key IS NOT NULL
		GROUP BY aggregation_key`,
		*event.Tenant, *event.CloudAccountId, roughSubj, ns,
		start.Add(-ChronicLookback), start, event.Id,
	)
	if err != nil {
		return false, fmt.Errorf("failed to load subject pair rates: %w", err)
	}
	// The rates are now only used to decide who LEADS, never who belongs. The
	// seed's own rate is therefore no longer a gate: a chronic alert firing
	// alongside others on its subject joins their group and simply loses the
	// leader election. The trailing-hour burst escape went with the gate, since
	// there is nothing left for it to escape.
	chronicPairs := make(map[string]bool, len(rates))
	for _, r := range rates {
		if (ChronicStats{WeeklyCount: r.Weekly}).Chronic() {
			chronicPairs[r.AggregationKey] = true
		}
	}

	// Chain-first candidates on the subject's namespace inside the absorption
	// cap; SubjectKey matching happens in Go so hash-stripped ownerless pod
	// names still meet their siblings.
	type candidateRow struct {
		ID               string    `db:"id"`
		SubjectType      *string   `db:"subject_type"`
		SubjectName      *string   `db:"subject_name"`
		SubjectNamespace *string   `db:"subject_namespace"`
		SubjectOwner     *string   `db:"subject_owner"`
		AggregationKey   *string   `db:"aggregation_key"`
		StartsAt         time.Time `db:"starts_at"`
		LastSeen         time.Time `db:"last_seen"`
	}
	// Candidates are alerts that FIRED inside the attach window, not alerts whose
	// chain started inside it. That is the whole point of the change: on the
	// Rackspace tenant the two chains in the reported case opened 19 hours apart
	// and have been firing every few minutes ever since, so a start-time search
	// could never pair them however wide the window got. Widening was measured
	// and rejected — 15 minutes to 24 hours multiplies candidate pairs 96x, and
	// the median gap between two chain starts on one subject is 4.1 days.
	//
	// Each row is a firing; `id` is its chain's FIRST event, because that is the
	// row group links hang off (one link per alert, not one per firing). The
	// window is now IncidentAttachWindow rather than the 90-minute absorption cap
	// it replaced, so this scans LESS than it used to.
	var rows []candidateRow
	err = sqlx.SelectContext(ctx, db, &rows, `
		SELECT DISTINCT ON (d.first_event_id)
		       d.first_event_id AS id, e.subject_type, e.subject_name, e.subject_namespace, e.subject_owner,
		       e.aggregation_key, le.starts_at AS starts_at,
		       max(e.starts_at) OVER (PARTITION BY d.first_event_id) AS last_seen
		FROM events e
		JOIN event_duplicates d ON d.event_id = e.id AND d.cloud_account_id = e.cloud_account_id
		JOIN events le ON le.id = d.first_event_id
		WHERE e.tenant = $1
		  AND e.cloud_account_id = $2
		  AND lower(coalesce(btrim(e.subject_namespace), '')) = $3
		  AND e.starts_at >= $4 AND e.starts_at < $5
		  AND d.first_event_id != $6
		  AND e.fingerprint IS DISTINCT FROM $7
		  AND lower(coalesce(e.finding_type, '')) NOT IN ('slo', 'anomaly')
		ORDER BY d.first_event_id, e.starts_at DESC
		LIMIT `+fmt.Sprint(incidentCandidateLimit),
		*event.Tenant, *event.CloudAccountId, ns,
		start.Add(-IncidentAttachWindow), start, seedChainID, event.Fingerprint,
	)
	if err != nil {
		return false, fmt.Errorf("failed to load group candidates: %w", err)
	}

	deref := func(p *string) string {
		if p != nil {
			return *p
		}
		return ""
	}
	members := make([]groupCandidate, 0, len(rows))
	memberStarts := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		key := SubjectKey(AlertIdentity{
			ID:               r.ID,
			SubjectType:      deref(r.SubjectType),
			SubjectName:      deref(r.SubjectName),
			SubjectNamespace: deref(r.SubjectNamespace),
			SubjectOwner:     deref(r.SubjectOwner),
		})
		if key != seedKey {
			continue
		}
		members = append(members, groupCandidate{
			ID:             r.ID,
			AggregationKey: deref(r.AggregationKey),
			StartsAt:       r.StartsAt,
			LastSeen:       r.LastSeen,
		})
		memberStarts[r.ID] = r.StartsAt
	}
	// ID is the chain, StartsAt is THIS firing: the link is written for the chain,
	// but "is anything else firing right now" is asked about the firing in hand.
	seed := groupCandidate{ID: seedChainID, AggregationKey: *event.AggregationKey, StartsAt: start}
	// No early return when the seed's own subject has nothing: an alert that is
	// alone on its machine can still belong to a connected service's incident,
	// which the merge below covers.

	// Merge the seed's own subject with every connected subject that is alerting
	// in the same window, then elect ONE leader over the union. Trying the two in
	// sequence meant a machine whose own alerts grouped never looked at its
	// neighbours at all.
	pool, err := collectConnectedMembers(ctx, db, event, seed, seedKey, start)
	if err != nil {
		// Connected-set evidence is additive: losing it must not cost the group
		// the seed can already form on its own subject.
		slog.WarnContext(ctx, "Failed to collect connected members", "error", err, "event_id", event.Id)
	}
	members = append(members, pool.members...)
	for id, ts := range pool.memberStarts {
		memberStarts[id] = ts
	}
	for k := range pool.chronicPairs {
		chronicPairs[k] = true
	}
	if len(members) == 0 {
		return false, nil
	}

	leaderID, offset, ok, err := resolveOpenGroupLeader(ctx, db, event, seed, members, memberStarts, chronicPairs, pool.dependedOnBy)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	reason := fmt.Sprintf("same subject (%s) within incident attach window", seedKey)
	if h := pool.hops[leaderID]; h > 0 {
		reason = fmt.Sprintf("%s is %d hop(s) away, both firing within the incident attach window", seedKey, h)
	}
	if err := insertGroupLink(ctx, db, event, seedChainID, leaderID, offset, pool.hops[leaderID], reason); err != nil {
		return false, err
	}
	return true, nil
}

// resolveOpenGroupLeader loads the members' existing group links, resolves
// leader start times, and runs the attach decision. Shared by the same-subject
// path and the topology path — both attach into a subject-keyed star.
func resolveOpenGroupLeader(
	ctx context.Context,
	db sqlx.ExtContext,
	event *models.Event,
	seed groupCandidate,
	members []groupCandidate,
	memberStarts map[string]time.Time,
	chronicPairs map[string]bool,
	dependedOnBy map[string]int,
) (string, time.Duration, bool, error) {
	memberIDs := make([]string, len(members))
	for i, m := range members {
		memberIDs[i] = m.ID
	}
	type edgeRow struct {
		EventID        string `db:"event_id"`
		RelatedEventID string `db:"related_event_id"`
	}
	var edgeRows []edgeRow
	err := sqlx.SelectContext(ctx, db, &edgeRows, `
		SELECT event_id, related_event_id
		FROM event_correlations
		WHERE correlation_type = $1
		  AND cloud_account_id = $2
		  AND event_id = ANY($3)`,
		SameIncidentCorrelationType, *event.CloudAccountId, pq.Array(memberIDs),
	)
	if err != nil {
		return "", 0, false, fmt.Errorf("failed to load existing group links: %w", err)
	}
	edges := make(map[string]string, len(edgeRows))
	leaderStarts := make(map[string]time.Time)
	missing := map[string]bool{}
	for _, e := range edgeRows {
		edges[e.EventID] = e.RelatedEventID
		if ts, ok := memberStarts[e.RelatedEventID]; ok {
			leaderStarts[e.RelatedEventID] = ts
		} else {
			missing[e.RelatedEventID] = true
		}
	}
	missingLeaders := make([]string, 0, len(missing))
	for id := range missing {
		missingLeaders = append(missingLeaders, id)
	}
	if len(missingLeaders) > 0 {
		type leaderRow struct {
			ID       string    `db:"id"`
			StartsAt time.Time `db:"starts_at"`
		}
		var lrs []leaderRow
		err = sqlx.SelectContext(ctx, db, &lrs,
			`SELECT id, starts_at FROM events WHERE id = ANY($1)`,
			pq.Array(missingLeaders),
		)
		if err != nil {
			return "", 0, false, fmt.Errorf("failed to load group leader starts: %w", err)
		}
		for _, lr := range lrs {
			leaderStarts[lr.ID] = lr.StartsAt
		}
	}

	leaderID, offset, ok := decideSameSubjectAttach(seed, members, edges, leaderStarts, chronicPairs, dependedOnBy)
	return leaderID, offset, ok, nil
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

// ownerElseName mirrors the SQL identity the chronic rate query groups on:
// lower(coalesce(nullif(btrim(subject_owner), ”), btrim(subject_name))). Trims
// before the emptiness test so a whitespace-only owner falls back to the name.
func ownerElseName(owner, name string) string {
	if s := strings.ToLower(strings.TrimSpace(owner)); s != "" {
		return s
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// connectedCandidate is one alerting chain, with the two identities the connected-set
// decision needs: which machine it is on, and which service node it maps to.
type connectedCandidate struct {
	candidate  groupCandidate
	subjectKey string
	serviceKey string
	// subject is the owner-else-name identity the chronic rate query matches on,
	// carried here rather than parsed back out of subjectKey. SubjectKey is not
	// reversible: its datastore form is "db|namespace|series", so splitting on a
	// separator yields the series list rather than a subject.
	subject string
}

// poolConnectedMembers picks every candidate whose service sits within
// maxIncidentHops of the seed's, in either direction, and returns them as one
// pool. Pure — all I/O happens in the caller.
//
// This replaces "pick the single most recently active neighbour". If a, b and c
// are connected and all three are alerting, they are one incident; the old rule
// joined one of them, so whether all three ended up together was luck. The
// reported case needs two hops, not one: payment and inventory both call order,
// and all three call database.
//
// Reports the distinct subjects pooled and whether the subject cap stopped it
// adding more, so the caller can say so rather than silently truncating.
func poolConnectedMembers(
	graph *DependencyGraph,
	seedSvcKey, seedKey string,
	cands []connectedCandidate,
) ([]groupCandidate, map[string]int, map[string]string, bool, map[string]int) {
	members := make([]groupCandidate, 0, len(cands))
	hopsByMember := make(map[string]int, len(cands))
	dependedOnBy := make(map[string]int, len(cands))
	subjects := map[string]string{}
	capped := false

	for _, c := range cands {
		if c.subjectKey == seedKey || strings.HasSuffix(c.subjectKey, "|") || c.serviceKey == "" {
			continue
		}
		hops := graph.getDependencyDistance(seedSvcKey, c.serviceKey)
		if hops <= 0 || hops > maxIncidentHops {
			rev := graph.getDependencyDistance(c.serviceKey, seedSvcKey)
			if rev <= 0 || rev > maxIncidentHops {
				continue
			}
			hops = rev
		}
		if _, seen := subjects[c.subjectKey]; !seen {
			if len(subjects) >= incidentGroupSubjectCap {
				capped = true
				continue
			}
			subjects[c.subjectKey] = c.subject
		}
		members = append(members, c.candidate)
		hopsByMember[c.candidate.ID] = hops
	}

	// Who depends on whom, among the members that made it in. getDependencyDistance
	// walks the "depends on" direction, so a positive distance from A to B means A
	// needs B — and B is the better candidate for the headline. Counted rather
	// than treated as a flag so the member the most others need wins, which is
	// what a shared backend looks like from the alerts around it.
	//
	// The seed is scored too: it is a member like any other, and on the case this
	// exists for it is the one the rest depend on.
	keys := make(map[string]string, len(cands)+1)
	keys[seedKey] = seedSvcKey
	for _, c := range cands {
		if _, kept := hopsByMember[c.candidate.ID]; kept {
			keys[c.candidate.ID] = c.serviceKey
		}
	}
	for id, svcKey := range keys {
		for otherID, otherKey := range keys {
			if id == otherID || otherKey == "" || svcKey == "" {
				continue
			}
			if d := graph.getDependencyDistance(otherKey, svcKey); d > 0 && d <= maxIncidentHops {
				dependedOnBy[id]++
			}
		}
	}

	return members, hopsByMember, subjects, capped, dependedOnBy
}

// collectConnectedMembers gathers the alerts on OTHER subjects that are connected
// to the seed's and firing in the same window, so the caller can elect one leader
// over the same-subject and connected pools together.
//
// It used to attach on its own, and only when the same-subject path had already
// declined. That ordering meant the reported case could never reach it: order's
// own two alerts group, the function returns, and payment, inventory and database
// are never considered — the connected-set rule could not fire on the case it was
// written for. The pools are now merged before a leader is chosen.
//
// Original note: join a NEIGHBOR subject's open group
// when the seed's own stored service map places the two subjects one CALLS hop
// apart. Evidence is stored-only (the map embedded in the event at enrichment
// time — topology as it was when the alert fired); precision rests on three
// gates: direct edge only (d=1), the seed's own chronic gate (already applied
// by the caller), and the neighbor group's members' chronic gate inside the
// shared attach decision. Events without a stored map (cloud alerts, plain
// webhooks) never topology-attach — correct, not a bug.
type connectedPool struct {
	members      []groupCandidate
	memberStarts map[string]time.Time
	hops         map[string]int
	chronicPairs map[string]bool
	// dependedOnBy counts how many other members of this pool depend on each
	// member. It is the only causal signal the group has: everything else the
	// election looks at is timing, and timing gets the direction wrong exactly
	// when it matters. A load balancer starts erroring before the operator's
	// health check notices the backend is down, so earliest-start crowns the
	// symptom and files the cause underneath it.
	dependedOnBy map[string]int
}

func collectConnectedMembers(
	ctx context.Context,
	db sqlx.ExtContext,
	event *models.Event,
	seed groupCandidate,
	seedKey string,
	start time.Time,
) (connectedPool, error) {
	var empty connectedPool
	if !topologyGroupingEnabled() {
		return empty, nil
	}
	graph, err := parseServiceMapFromEvent(event)
	if err != nil || graph == nil {
		return empty, nil // no stored topology — nothing to reason with
	}
	seedSvcKey := getServiceKeyFromEvent(event)
	if seedSvcKey == "" {
		return empty, nil
	}

	// Alerts that FIRED inside the attach window, account-wide, one row per
	// chain. Same shape as the same-subject query: a cross-service story is made
	// of alerts happening now, not of chains that happened to open together.
	type candidateRow struct {
		ID               string    `db:"id"`
		SubjectType      *string   `db:"subject_type"`
		SubjectName      *string   `db:"subject_name"`
		SubjectNamespace *string   `db:"subject_namespace"`
		SubjectOwner     *string   `db:"subject_owner"`
		SubjectOwnerKind *string   `db:"subject_owner_kind"`
		ServiceKey       *string   `db:"service_key"`
		AggregationKey   *string   `db:"aggregation_key"`
		StartsAt         time.Time `db:"starts_at"`
		LastSeen         time.Time `db:"last_seen"`
	}
	var rows []candidateRow
	err = sqlx.SelectContext(ctx, db, &rows, `
		SELECT DISTINCT ON (d.first_event_id)
		       d.first_event_id AS id, e.subject_type, e.subject_name, e.subject_namespace,
		       e.subject_owner, e.subject_owner_kind, e.service_key, e.aggregation_key,
		       le.starts_at AS starts_at,
		       max(e.starts_at) OVER (PARTITION BY d.first_event_id) AS last_seen
		FROM events e
		JOIN event_duplicates d ON d.event_id = e.id AND d.cloud_account_id = e.cloud_account_id
		JOIN events le ON le.id = d.first_event_id
		WHERE e.tenant = $1
		  AND e.cloud_account_id = $2
		  AND e.starts_at >= $3 AND e.starts_at < $4
		  AND d.first_event_id != $5
		  AND e.fingerprint IS DISTINCT FROM $6
		  AND lower(coalesce(e.finding_type, '')) NOT IN ('slo', 'anomaly')
		ORDER BY d.first_event_id, e.starts_at DESC
		LIMIT `+fmt.Sprint(incidentCandidateLimit),
		*event.Tenant, *event.CloudAccountId,
		start.Add(-IncidentAttachWindow), start, seed.ID, event.Fingerprint,
	)
	if err != nil {
		return empty, fmt.Errorf("failed to load topology candidates: %w", err)
	}

	deref := func(p *string) string {
		if p != nil {
			return *p
		}
		return ""
	}

	cands := make([]connectedCandidate, 0, len(rows))
	for _, r := range rows {
		cands = append(cands, connectedCandidate{
			candidate: groupCandidate{
				ID:             r.ID,
				AggregationKey: deref(r.AggregationKey),
				StartsAt:       r.StartsAt,
				LastSeen:       r.LastSeen,
			},
			subjectKey: SubjectKey(AlertIdentity{
				ID:               r.ID,
				SubjectType:      deref(r.SubjectType),
				SubjectName:      deref(r.SubjectName),
				SubjectNamespace: deref(r.SubjectNamespace),
				SubjectOwner:     deref(r.SubjectOwner),
			}),
			serviceKey: getServiceKeyFromEvent(&models.Event{
				ServiceKey:       r.ServiceKey,
				SubjectName:      r.SubjectName,
				SubjectNamespace: r.SubjectNamespace,
				SubjectOwner:     r.SubjectOwner,
				SubjectOwnerKind: r.SubjectOwnerKind,
				SubjectType:      r.SubjectType,
			}),
			subject: ownerElseName(deref(r.SubjectOwner), deref(r.SubjectName)),
		})
	}
	members, hopsByMember, subjects, capped, dependedOnBy := poolConnectedMembers(graph, seedSvcKey, seedKey, cands)
	memberStarts := make(map[string]time.Time, len(members))
	for _, m := range members {
		memberStarts[m.ID] = m.StartsAt
	}
	if len(members) == 0 {
		return empty, nil
	}
	if capped {
		slog.InfoContext(ctx, "Incident group hit the subject cap; not adding more",
			"event_id", event.Id, "cap", incidentGroupSubjectCap, "seed_subject", seedKey)
	}

	// Chronic rates across every pooled subject, so the leader election prefers a
	// non-chronic alert over a flapper wherever it sits in the connected set.
	subjectList := make([]string, 0, len(subjects))
	for _, subj := range subjects {
		if subj != "" {
			subjectList = append(subjectList, subj)
		}
	}
	chronicPairs := map[string]bool{}
	if len(subjectList) > 0 {
		type pairRate struct {
			AggregationKey string `db:"aggregation_key"`
			Weekly         int    `db:"weekly"`
		}
		var rates []pairRate
		err = sqlx.SelectContext(ctx, db, &rates, `
			SELECT aggregation_key, count(*) AS weekly
			FROM events
			WHERE tenant = $1
			  AND cloud_account_id = $2
			  AND lower(coalesce(nullif(btrim(subject_owner), ''), btrim(subject_name))) = ANY($3)
			  AND starts_at >= $4 AND starts_at < $5
			  AND aggregation_key IS NOT NULL
			GROUP BY aggregation_key`,
			*event.Tenant, *event.CloudAccountId, pq.Array(subjectList),
			start.Add(-ChronicLookback), start,
		)
		if err != nil {
			return empty, fmt.Errorf("failed to load neighbor pair rates: %w", err)
		}
		for _, r := range rates {
			if (ChronicStats{WeeklyCount: r.Weekly}).Chronic() {
				chronicPairs[r.AggregationKey] = true
			}
		}
	}

	return connectedPool{
		members:      members,
		memberStarts: memberStarts,
		hops:         hopsByMember,
		dependedOnBy: dependedOnBy,
		chronicPairs: chronicPairs,
	}, nil
}
