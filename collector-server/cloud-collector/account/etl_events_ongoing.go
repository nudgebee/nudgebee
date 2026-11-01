package account

import (
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"time"

	"github.com/lib/pq"
)

const (
	// ongoingListingLag keeps a firing that started just before the listing out of
	// its reach: a push stores a firing within seconds, and the listing may not
	// show the new state yet.
	ongoingListingLag = 2 * time.Minute
	// sameFiringTolerance is how far a stored firing's start may sit before the
	// listed start of the current firing and still be that firing. A polled row
	// and a pushed row both start at the time the alarm changed state, cut to the
	// second. The tolerance is for rows a push stored before it read that time
	// from the event: those start at the time the event was sent, which was the
	// same second or the next.
	sameFiringTolerance = 2 * time.Second
	// ongoingCoverageMaxAge is how long a poll's listing of a scope lets a pushed
	// firing from that scope be stored open: six polls at the ten-minute cadence.
	// Past it nothing is known to be able to end the row, so the push is stored
	// as a moment again.
	ongoingCoverageMaxAge = time.Hour

	// Keys of the events job status that record what the last poll listed.
	ongoingListedAtKey = "ongoing_listed_at"
	ongoingScopesKey   = "ongoing_scopes"

	// resolveTimeLayout is how a resolve time is bound. events.starts_at and
	// ends_at are timestamps without a time zone holding UTC, so the value is
	// sent as a UTC wall clock and never as a timestamptz, whose conversion would
	// follow the session time zone.
	resolveTimeLayout = "2006-01-02 15:04:05.999999"
)

// openFiring is a stored firing that has not ended.
type openFiring struct {
	ID          string    `db:"id"`
	Fingerprint string    `db:"fingerprint"`
	StartsAt    time.Time `db:"starts_at"`
}

// endedFiring is an open firing a poll found to be over, and when it ended.
type endedFiring struct {
	openFiring
	EndedAt time.Time
	Reason  string
}

const (
	endedNotListed  = "no longer on at the poll"
	endedFiredAgain = "fired again since"
)

// endedFirings picks the open firings a poll's snapshot shows to be over.
//
// A firing is left alone unless the snapshot can speak for it: its scope must
// have been listed completely, and it must have started long enough before the
// listing to be in it. Of the rest, one whose condition is no longer on has
// ended at the poll, and one whose condition is on but began a later firing has
// ended by the time that firing began: the condition went off and came back
// between two polls.
func endedFirings(open []openFiring, snapshot providers.OngoingSnapshot) []endedFiring {
	if snapshot.Identify == nil {
		return nil
	}
	listed := make(map[string]bool, len(snapshot.Scopes))
	for _, scope := range snapshot.Scopes {
		listed[scope] = true
	}
	var ended []endedFiring
	for _, firing := range open {
		scope, key, ok := snapshot.Identify(firing.Fingerprint)
		if !ok || !listed[scope] {
			continue
		}
		if firing.StartsAt.After(snapshot.ListedAt.Add(-ongoingListingLag)) {
			continue
		}
		since, on := snapshot.Since[key]
		switch {
		case !on:
			ended = append(ended, endedFiring{openFiring: firing, EndedAt: snapshot.ListedAt, Reason: endedNotListed})
		case since.IsZero():
			// On, but the source did not say since when: any open firing may be
			// the current one.
		case firing.StartsAt.Before(since.Add(-sameFiringTolerance)):
			ended = append(ended, endedFiring{openFiring: firing, EndedAt: since, Reason: endedFiredAgain})
		}
	}
	return ended
}

// keepOngoingWhereListed clears the ongoing mark of every event whose scope is
// not one a poll has listed, so it is stored as a moment. An event stays open
// only where a poll can end it.
func keepOngoingWhereListed(events []providers.Event, listed map[string]bool) {
	for i := range events {
		if events[i].OngoingScope != "" && !listed[events[i].OngoingScope] {
			events[i].OngoingScope = ""
		}
	}
}

// batchClosableSources are the sources whose stored events the batch-wide close
// in StoreEvents may close for being absent from the batch.
//
// Sources a snapshot covers are left out. Their firings are ended by
// endFiringsNoLongerOn, which knows which scopes were listed. The batch-wide
// close does not: it would close a firing in a region that failed to list, with
// no end time, and a row closed while its alarm still breaches is never stored
// again because its finding_id already exists.
func batchClosableSources(events providers.ListEventResponse) []string {
	covered := map[string]bool{}
	if events.Ongoing != nil {
		for _, source := range events.Ongoing.Sources {
			covered[source] = true
		}
	}
	seen := map[string]bool{}
	var sources []string
	for _, event := range events.Items {
		if covered[event.EventSource] || seen[event.EventSource] {
			continue
		}
		seen[event.EventSource] = true
		sources = append(sources, event.EventSource)
	}
	return sources
}

// listedScopes are the scopes a snapshot listed completely; none for a poll
// that returned no snapshot.
func listedScopes(snapshot *providers.OngoingSnapshot) map[string]bool {
	if snapshot == nil {
		return nil
	}
	listed := make(map[string]bool, len(snapshot.Scopes))
	for _, scope := range snapshot.Scopes {
		listed[scope] = true
	}
	return listed
}

// ongoingCoverageStatus is what a poll records about its listing in the events
// job status, for the push path to read.
func ongoingCoverageStatus(snapshot *providers.OngoingSnapshot) map[string]any {
	if snapshot == nil {
		return nil
	}
	return map[string]any{
		ongoingListedAtKey: snapshot.ListedAt.UTC().Format(time.RFC3339),
		ongoingScopesKey:   snapshot.Scopes,
	}
}

// listedScopesFromStatus reads back, from an account's job status, the scopes
// its last poll listed. It returns none when that listing is older than
// ongoingCoverageMaxAge or the last poll recorded no listing: the events status
// is rewritten whole on every poll, so a failed poll erases it.
func listedScopesFromStatus(jobStatus map[string]any, now time.Time) map[string]bool {
	eventsStatus, _ := jobStatus["events"].(map[string]any)
	at, _ := eventsStatus[ongoingListedAtKey].(string)
	listedAt, err := time.Parse(time.RFC3339, at)
	if err != nil || now.Sub(listedAt) > ongoingCoverageMaxAge {
		return nil
	}
	scopes, _ := eventsStatus[ongoingScopesKey].([]any)
	listed := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if s, ok := scope.(string); ok {
			listed[s] = true
		}
	}
	return listed
}

// endFiringsNoLongerOn resolves the account's open firings that the poll's
// snapshot shows to be over.
func endFiringsNoLongerOn(ctx *security.RequestContext, dbms *common.DatabaseManager, accountID string, snapshot providers.OngoingSnapshot) error {
	if len(snapshot.Scopes) == 0 {
		return nil
	}
	var open []openFiring
	err := dbms.QueryAndScan(&open,
		`SELECT id::text, fingerprint, starts_at FROM events
		 WHERE cloud_account_id = $1 AND status = 'FIRING' AND source = ANY($2)
		 AND fingerprint LIKE $3 AND starts_at IS NOT NULL`,
		accountID, pq.Array(snapshot.Sources), snapshot.FingerprintLike)
	if err != nil {
		return err
	}
	ended := endedFirings(open, snapshot)
	if len(ended) == 0 {
		return nil
	}

	ids := make([]string, 0, len(ended))
	ats := make([]string, 0, len(ended))
	byID := make(map[string]endedFiring, len(ended))
	for _, firing := range ended {
		ids = append(ids, firing.ID)
		ats = append(ats, firing.EndedAt.UTC().Format(resolveTimeLayout))
		byID[firing.ID] = firing
	}
	var rows []resolvedRow
	err = dbms.QueryAndScan(&rows,
		`UPDATE events e SET status = 'RESOLVED', updated_at = r.at, ends_at = r.at
		 FROM unnest($2::uuid[], $3::timestamp[]) AS r(id, at)
		 WHERE e.id = r.id AND e.cloud_account_id = $1
		 AND e.status NOT IN ('CLOSED', 'RESOLVED')
		 RETURNING e.id::text, e.tenant::text, e.cloud_account_id::text, e.fingerprint`,
		accountID, pq.Array(ids), pq.Array(ats))
	if err != nil {
		return err
	}
	ctx.GetLogger().Info("resolved firings that are no longer on", "accountId", accountID, "open", len(open), "resolved", len(rows))

	announceResolved(ctx, dbms, rows, func(row resolvedRow) map[string]any {
		firing := byID[row.ID]
		return map[string]any{
			"resolved_date":   firing.EndedAt.UTC().Format(time.RFC3339),
			"resolved_reason": firing.Reason,
		}
	})
	return nil
}
