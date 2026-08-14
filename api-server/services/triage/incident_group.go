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
	IncidentAttachWindow = 15 * time.Minute
	// IncidentAbsorptionCap bounds a group's total age: alerts starting more
	// than this after the leader open a new group instead. Keeps one leader
	// from absorbing a whole day of a subject's history.
	IncidentAbsorptionCap = 90 * time.Minute

	incidentGroupingEnvFlag = "INCIDENT_GROUPING_ENABLED"
	// incidentCandidateLimit bounds the window fetch; one subject+namespace
	// rarely has more than a handful of distinct fingerprints in 90 minutes.
	incidentCandidateLimit = 200
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
//   - members: chain-first events sharing the seed's SubjectKey, started within
//     [seed - IncidentAbsorptionCap, seed).
//   - edges: existing same_incident links among the members (child -> leader;
//     the leader itself may be older than the member window).
//   - leaderStarts: starts_at for every distinct edge target.
//   - chronicPairs: the subject's aggregation_keys whose trailing rate is
//     chronic; they neither lead nor re-arm the attach timer.
func decideSameSubjectAttach(
	seed groupCandidate,
	members []groupCandidate,
	edges map[string]string,
	leaderStarts map[string]time.Time,
	chronicPairs map[string]bool,
) (string, time.Duration, bool) {
	capStart := seed.StartsAt.Add(-IncidentAbsorptionCap)

	// Split the leaders referenced by existing links into live (inside the
	// absorption cap) and stale (their group is over; members bound to them
	// are history, not attachable activity).
	liveLeaders := map[string]bool{}
	staleLeaders := map[string]bool{}
	for _, m := range members {
		l, linked := edges[m.ID]
		if !linked {
			continue
		}
		if ls, known := leaderStarts[l]; known && !ls.Before(capStart) {
			liveLeaders[l] = true
		} else {
			staleLeaders[l] = true
		}
	}

	// Activity that can hold a group open or found a new one: non-chronic
	// members not bound to a capped-out group.
	active := make([]groupCandidate, 0, len(members))
	for _, m := range members {
		if chronicPairs[m.AggregationKey] {
			continue
		}
		if l, linked := edges[m.ID]; linked && staleLeaders[l] {
			continue
		}
		active = append(active, m)
	}
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

	// No group yet: the earliest active member becomes the leader.
	sort.Slice(active, func(i, j int) bool {
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
func attachSameSubjectIncident(ctx context.Context, db sqlx.ExtContext, event *models.Event) error {
	if event == nil || event.Tenant == nil || *event.Tenant == "" ||
		event.CloudAccountId == nil || *event.CloudAccountId == "" ||
		event.StartsAt == nil || event.AggregationKey == nil || *event.AggregationKey == "" {
		return nil // not groupable
	}
	identity := eventAlertIdentity(event)
	// Derived signals (SLO violations, anomaly detections) neither lead nor
	// join groups — same reasoning as the assembly's isDerivedSignal: they are
	// statistical echoes of concrete failures, and replay showed an
	// SLOViolation leading a crashloop group it did not cause.
	if isDerivedSignal(identity) {
		return nil
	}
	seedKey := SubjectKey(identity)
	ns, roughSubj := chronicSubjectIdentity(event)
	if roughSubj == "" || strings.HasSuffix(seedKey, "|") {
		return nil // no subject identity — cluster-scoped alerts never group here
	}
	start := *event.StartsAt

	// One grouped query gives every pair's trailing rate on this subject: the
	// seed's own chronic gate plus the chronic flags for candidate pairs. Uses
	// the same raw owner-else-name identity as LoadChronicStats (hash-suffixed
	// ownerless pods undercount, which errs toward "not chronic" — safe).
	type pairRate struct {
		AggregationKey string `db:"aggregation_key"`
		Weekly         int    `db:"weekly"`
		LastHour       int    `db:"last_hour"`
	}
	var rates []pairRate
	err := sqlx.SelectContext(ctx, db, &rates, `
		SELECT aggregation_key,
		       count(*) AS weekly,
		       count(*) FILTER (WHERE starts_at >= $5) AS last_hour
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = $2
		  AND lower(coalesce(nullif(btrim(subject_owner), ''), btrim(subject_name))) = $3
		  AND lower(coalesce(btrim(subject_namespace), '')) = $4
		  AND starts_at >= $6 AND starts_at < $7
		  AND id != $8
		  AND aggregation_key IS NOT NULL
		GROUP BY aggregation_key`,
		*event.Tenant, *event.CloudAccountId, roughSubj, ns,
		start.Add(-time.Hour), start.Add(-ChronicLookback), start, event.Id,
	)
	if err != nil {
		return fmt.Errorf("failed to load subject pair rates: %w", err)
	}
	chronicPairs := make(map[string]bool, len(rates))
	var seedStats ChronicStats
	seedLastHour := 0
	for _, r := range rates {
		stats := ChronicStats{WeeklyCount: r.Weekly}
		if stats.Chronic() {
			chronicPairs[r.AggregationKey] = true
		}
		if r.AggregationKey == *event.AggregationKey {
			seedStats = stats
			seedLastHour = r.LastHour
		}
	}
	// Chronic seeds neither declare nor extend — unless bursting far past
	// their own baseline (+1 counts the seed firing itself).
	if seedStats.Chronic() && !seedStats.IsBursting(seedLastHour+1) {
		return nil
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
	// One row per fingerprint: the EARLIEST occurrence (the chain leader — the
	// row that can carry group links) plus the fingerprint's newest start in
	// the window (last_seen), so re-fires keep the attach timer alive.
	var rows []candidateRow
	err = sqlx.SelectContext(ctx, db, &rows, `
		SELECT DISTINCT ON (fingerprint)
		       id, subject_type, subject_name, subject_namespace, subject_owner,
		       aggregation_key, starts_at,
		       max(starts_at) OVER (PARTITION BY fingerprint) AS last_seen
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = $2
		  AND lower(coalesce(btrim(subject_namespace), '')) = $3
		  AND starts_at >= $4 AND starts_at < $5
		  AND id != $6
		  AND fingerprint IS DISTINCT FROM $7
		  AND lower(coalesce(finding_type, '')) NOT IN ('slo', 'anomaly')
		ORDER BY fingerprint, starts_at ASC
		LIMIT `+fmt.Sprint(incidentCandidateLimit),
		*event.Tenant, *event.CloudAccountId, ns,
		start.Add(-IncidentAbsorptionCap), start, event.Id, event.Fingerprint,
	)
	if err != nil {
		return fmt.Errorf("failed to load group candidates: %w", err)
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
	if len(members) == 0 {
		return nil // first alert on this subject — implicit group of one
	}

	memberIDs := make([]string, len(members))
	for i, m := range members {
		memberIDs[i] = m.ID
	}
	type edgeRow struct {
		EventID        string `db:"event_id"`
		RelatedEventID string `db:"related_event_id"`
	}
	var edgeRows []edgeRow
	err = sqlx.SelectContext(ctx, db, &edgeRows, `
		SELECT event_id, related_event_id
		FROM event_correlations
		WHERE correlation_type = $1
		  AND cloud_account_id = $2
		  AND event_id = ANY($3)`,
		SameIncidentCorrelationType, *event.CloudAccountId, pq.Array(memberIDs),
	)
	if err != nil {
		return fmt.Errorf("failed to load existing group links: %w", err)
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
			return fmt.Errorf("failed to load group leader starts: %w", err)
		}
		for _, lr := range lrs {
			leaderStarts[lr.ID] = lr.StartsAt
		}
	}

	seed := groupCandidate{ID: event.Id, AggregationKey: *event.AggregationKey, StartsAt: start}
	leaderID, offset, ok := decideSameSubjectAttach(seed, members, edges, leaderStarts, chronicPairs)
	if !ok {
		return nil
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO event_correlations (
			event_id, related_event_id, cloud_account_id, tenant_id,
			correlation_type, correlation_score, correlation_reason,
			time_offset_minutes, dependency_distance
		) VALUES ($1, $2, $3, $4, $5, 1.0, $6, $7, 0)
		ON CONFLICT DO NOTHING`,
		event.Id, leaderID, *event.CloudAccountId, event.Tenant,
		SameIncidentCorrelationType,
		fmt.Sprintf("same subject (%s) within incident attach window", seedKey),
		int(offset.Minutes()),
	)
	if err != nil {
		return fmt.Errorf("failed to insert same_incident link: %w", err)
	}

	slog.InfoContext(ctx, "Attached event to same-subject incident group",
		"event_id", event.Id,
		"leader_event_id", leaderID,
		"subject_key", seedKey,
		"offset_minutes", int(offset.Minutes()),
	)
	return nil
}
