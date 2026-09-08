package triage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// Write-time chronic classification (incident-declaration prerequisite 0c,
// docs/incident-declaration-plan.md).
//
// A (subject, alert-type) pair that fires all week is background noise: on the
// dev cluster, 8% of pairs produce 68.5% of all events. The incident engine
// (Phase 3) must be able to ask "is this alert chronic?" at write time so such
// alerts never declare incidents and never keep one's attach timer alive. The
// view-time assembly has its own chronic fold (incident_tiers.go Rate); this is
// the write-time primitive over the events table.
//
// The threshold is a dev-derived starting point, re-derived per account during
// the shadow phase — see the plan's parameters table.

const (
	// ChronicWeeklyThreshold: firings per trailing week at/above which a
	// subject+alert-type pair classifies as chronic.
	ChronicWeeklyThreshold = 10
	// ChronicLookback is the trailing window the firing count is measured over.
	ChronicLookback = 7 * 24 * time.Hour
)

// ChronicStats is a subject+alert-type pair's trailing firing history.
type ChronicStats struct {
	// WeeklyCount is the number of firings in the trailing ChronicLookback.
	WeeklyCount int
}

// Chronic reports whether the pair's trailing rate classifies it as background
// noise. A chronic pair does not become the LEADER of an incident group — a
// flapper must not be the headline — but it still belongs to one when it fires
// alongside others, and it still surfaces as context.
//
// It used to gate membership as well, with a burst escape for pairs firing far
// past their own baseline. Both were removed: measured on the Rackspace tenant,
// the gate left machines whose every alert was chronic unable to form a group at
// all, and the escape's floor of three firings in an hour was never reached by
// the alerts it would have had to rescue, which average under two.
func (s ChronicStats) Chronic() bool {
	return s.WeeklyCount >= ChronicWeeklyThreshold
}

// chronicSubjectIdentity mirrors the SQL-side expression in LoadChronicStats:
// namespace + (owner else name), lowered. It deliberately does NOT apply
// WorkloadName hash-stripping — the stored rows can't be normalized inside an
// indexed SQL predicate, so both sides use the raw owner-else-name form. Pod
// rows without an owner therefore fragment across hash-suffixed names and
// undercount; the error direction is safe (undercount -> less chronic -> the
// alert stays eligible to declare).
func chronicSubjectIdentity(ev *models.Event) (ns, subject string) {
	if ev.SubjectNamespace != nil {
		ns = strings.ToLower(strings.TrimSpace(*ev.SubjectNamespace))
	}
	// Trim BEFORE the emptiness check so a whitespace-only owner still falls
	// back to the name.
	if ev.SubjectOwner != nil {
		subject = strings.ToLower(strings.TrimSpace(*ev.SubjectOwner))
	}
	if subject == "" && ev.SubjectName != nil {
		subject = strings.ToLower(strings.TrimSpace(*ev.SubjectName))
	}
	return ns, subject
}

// LoadChronicStats counts the event's subject+aggregation_key firings over the
// trailing week, anchored at the event's own start (so replays classify with
// the history that existed at the time, not today's). The event being
// classified is excluded by id — a pair's 10th firing is judged against the 9
// before it.
func LoadChronicStats(ctx context.Context, db sqlx.ExtContext, ev *models.Event) (ChronicStats, error) {
	if ev == nil || ev.Tenant == nil || *ev.Tenant == "" ||
		ev.CloudAccountId == nil || *ev.CloudAccountId == "" ||
		ev.AggregationKey == nil || *ev.AggregationKey == "" ||
		ev.StartsAt == nil {
		return ChronicStats{}, fmt.Errorf("event missing required fields for chronic classification (tenant, cloud_account_id, aggregation_key, starts_at)")
	}
	ns, subject := chronicSubjectIdentity(ev)
	if subject == "" {
		return ChronicStats{}, fmt.Errorf("event has no subject identity (owner or name)")
	}

	query := `
		SELECT count(*)
		FROM events
		WHERE tenant = $1
		  AND cloud_account_id = $2
		  AND aggregation_key = $3
		  AND lower(coalesce(nullif(btrim(subject_owner), ''), btrim(subject_name))) = $4
		  AND lower(coalesce(btrim(subject_namespace), '')) = $5
		  AND starts_at >= $6
		  AND starts_at < $7
		  AND id != $8`

	var count int
	err := sqlx.GetContext(ctx, db, &count, query,
		*ev.Tenant, *ev.CloudAccountId, *ev.AggregationKey,
		subject, ns,
		ev.StartsAt.Add(-ChronicLookback), *ev.StartsAt,
		ev.Id,
	)
	if err != nil {
		return ChronicStats{}, fmt.Errorf("failed to count trailing firings: %w", err)
	}
	return ChronicStats{WeeklyCount: count}, nil
}
