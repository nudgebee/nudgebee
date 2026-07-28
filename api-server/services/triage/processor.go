package triage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// ProcessEvent performs immediate triage on a new event
// This includes triage rules check, deduplication detection, and correlation with related events
func ProcessEvent(ctx context.Context, db *sqlx.DB, event *models.Event) error {
	startTime := time.Now()

	// Step 1: Deduplication detection FIRST (target: < 10ms)
	// This must run before CheckTriageRules so that the occurrence number is available
	// for occurrence-based rules (like the system auto-duplicate rule)
	occurrence, err := detectAndRecordDuplicate(ctx, db, event)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to detect duplicate", "error", err, "event_id", event.Id)
		// Continue processing - don't fail entire triage on duplicate detection error
		occurrence = 1
	}

	// Step 2: Check triage rules (after deduplication so occurrence_number is available)
	ruleResult, err := CheckTriageRules(ctx, db, event, occurrence)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to check triage rules", "error", err, "event_id", event.Id)
		// Continue processing - don't fail on rule check error
	}

	// Apply rule actions if a rule matched
	if ruleResult != nil {
		if err := ApplyTriageRuleActions(ctx, db, event, ruleResult); err != nil {
			slog.ErrorContext(ctx, "Failed to apply rule actions", "error", err, "event_id", event.Id)
		}

		// If dropped by rule, skip remaining processing (event stays in DB with nb_status=DROPPED)
		if ruleResult.Suppression != nil && ruleResult.Suppression.Action == ActionDrop {
			slog.InfoContext(ctx, "Event dropped by triage rule",
				"event_id", event.Id,
				"rule_id", ruleResult.RuleID,
			)
			return nil
		}
	}

	// Step 3: Correlation detection (target: < 150ms)
	corrType, corrScore, err := detectAndRecordCorrelations(ctx, db, event)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to detect correlations", "error", err, "event_id", event.Id)
		// Continue processing - don't fail entire triage on correlation error
	}

	// Step 4: Compute and save score (target: < 50ms)
	// Apply any score adjustments from rules
	result, err := ComputeScore(ctx, db, event, occurrence, corrType, corrScore)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to compute score", "error", err, "event_id", event.Id)
		// Continue processing - don't fail entire triage on scoring error
	} else {
		// Apply score adjustment from rules if present. A human override (per-event correction
		// or class priority_pin) is ABSOLUTE — additive scoring rules must NOT move it, or a
		// broad +N/-N rule would silently undo the human's pin. ResolveScore already chose the
		// final number; respect it.
		if ruleResult != nil && ruleResult.ScoreAdjustment != nil && !IsHumanAuthority(result) {
			result.Score = clamp(result.Score+ruleResult.ScoreAdjustment.Adjustment, 0, 100)
			// Re-assert the LLM verdict's priority band: an additive rule must not push the score
			// past the ceiling the model chose (a dev disk alert capped at P1 must not land at P0).
			// No-op for legacy-scored events (no band in factors).
			result.Score = clampToBand(result.Score, result.Factors)
			result.Priority = scoreToPriority(result.Score)
			slog.InfoContext(ctx, "Applied score adjustment from rule",
				"event_id", event.Id,
				"adjustment", ruleResult.ScoreAdjustment.Adjustment,
				"new_score", result.Score,
			)
		}

		if err := UpdateEventScore(ctx, db, event.Id, result); err != nil {
			slog.ErrorContext(ctx, "Failed to update event score", "error", err, "event_id", event.Id)
		}
	}

	duration := time.Since(startTime)
	slog.InfoContext(ctx, "Event triage completed",
		"event_id", event.Id,
		"duration_ms", duration.Milliseconds(),
	)

	// Log warning if processing took too long
	if duration > 200*time.Millisecond {
		slog.WarnContext(ctx, "Event triage exceeded target duration",
			"event_id", event.Id,
			"duration_ms", duration.Milliseconds(),
			"target_ms", 200,
		)
	}

	return nil
}

// DefaultDedupWindow bounds how long a fingerprint's occurrence chain stays open. A
// matching fingerprint arriving after this much silence opens a NEW chain instead of
// extending the old one, so it is triaged as a fresh incident rather than auto-classified
// DUPLICATE of an event that may be months old.
//
// 24h is taken from the live chain data: every chain that had grown past 30 days contained
// at least one gap longer than 24h, so this bound breaks all of them, while only ~2.7% of
// existing duplicate links have a gap that wide.
const DefaultDedupWindow = 24 * time.Hour

// dedupWindowBySource overrides DefaultDedupWindow for sources whose natural firing cadence
// does not fit the default — a detector that samples on a long interval needs a wider
// window, a chatty source may want a narrower one. Keyed by events.source; empty means every
// source uses the default.
//
// Written once here and read-only thereafter, which is what keeps the per-event lookup
// lock-free. Anything that needs to populate it at runtime (config reload, per-tenant
// overrides from the DB) must add synchronisation first — and tests must not mutate it,
// which is why the lookup below is split so they can pass their own table.
var dedupWindowBySource = map[string]time.Duration{}

// dedupWindowForSource resolves the dedup window for an event's source, falling back to
// DefaultDedupWindow for an absent or unlisted source.
func dedupWindowForSource(source *string) time.Duration {
	return dedupWindowFrom(dedupWindowBySource, source)
}

// dedupWindowFrom is the lookup itself, taking the override table as a parameter so tests can
// exercise a per-source override without writing to the package-level map.
func dedupWindowFrom(windows map[string]time.Duration, source *string) time.Duration {
	if source == nil {
		return DefaultDedupWindow
	}
	if window, ok := windows[*source]; ok {
		return window
	}
	return DefaultDedupWindow
}

// detectAndRecordDuplicate checks if this event is a duplicate and records it
// Creates chains per fingerprint. If the chain's first event is RESOLVED, or the chain has
// been silent for longer than the source's dedup window, starts a new chain while keeping a
// reference to the previous chain via previous_event_id.
// db is sqlx.ExtContext (satisfied by both *sqlx.DB and *sqlx.Tx) so the full
// path can run inside a rolled-back transaction in integration tests.
func detectAndRecordDuplicate(ctx context.Context, db sqlx.ExtContext, event *models.Event) (int, error) {
	// Check for required fields
	if event.Fingerprint == nil || event.CloudAccountId == nil || event.StartsAt == nil || event.Tenant == nil || *event.Tenant == "" {
		return 0, fmt.Errorf("event missing required fields (fingerprint, cloud_account_id, starts_at, or tenant)")
	}

	// Idempotency guard: if this event already has a chain row, it was processed
	// before (rescore-on-mint, redelivered post-process message) — return its stored
	// occurrence. Without this, the chain query below sees the chain members added
	// AFTER this event, computes latest+1 for an event that is actually the chain's
	// first, and the occurrence>1 auto-duplicate rule then marks the chain's ORIGINAL
	// as a duplicate of itself, leaving the whole chain DUPLICATE with no open event.
	var storedOccurrence int
	err := sqlx.GetContext(ctx, db, &storedOccurrence,
		`SELECT occurrence_number FROM event_duplicates WHERE event_id = $1 AND cloud_account_id = $2`,
		event.Id, *event.CloudAccountId)
	if err == nil {
		return storedOccurrence, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("failed to query existing occurrence: %w", err)
	}

	// Query event_duplicates to find existing chain for this fingerprint
	// Also fetch the first event's nb_status to check if we should start a new chain
	chainQuery := `
		SELECT
			ed.first_event_id,
			ed.event_id as latest_event_id,
			ed.occurrence_number,
			ed.absolute_first_seen_at,
			first_e.starts_at as first_event_starts_at,
			latest_e.starts_at as latest_event_starts_at,
			COALESCE(first_e.nb_status, 'OPEN') as first_event_nb_status
		FROM event_duplicates ed
		JOIN events first_e ON ed.first_event_id = first_e.id
		JOIN events latest_e ON ed.event_id = latest_e.id
		WHERE ed.fingerprint = $1
		  AND ed.cloud_account_id = $2
		  AND ed.event_id != $3
		ORDER BY ed.occurrence_number DESC
		LIMIT 1
	`

	var chainInfo struct {
		FirstEventID        string     `db:"first_event_id"`
		LatestEventID       string     `db:"latest_event_id"`
		OccurrenceNumber    int        `db:"occurrence_number"`
		AbsoluteFirstSeenAt *time.Time `db:"absolute_first_seen_at"`
		FirstEventStartsAt  time.Time  `db:"first_event_starts_at"`
		LatestEventStartsAt time.Time  `db:"latest_event_starts_at"`
		FirstEventNBStatus  string     `db:"first_event_nb_status"`
	}

	err = sqlx.GetContext(ctx, db, &chainInfo, chainQuery,
		*event.Fingerprint,
		*event.CloudAccountId,
		event.Id,
	)

	if err != nil {
		// sql.ErrNoRows means this is the first event with this fingerprint.
		// Use errors.Is, not a string match — a wrapped error (or a driver whose
		// message differs) would otherwise be misclassified and break the
		// first-occurrence path.
		if errors.Is(err, sql.ErrNoRows) {
			// This is the first occurrence, insert with occurrence_number = 1
			insertQuery := `
				INSERT INTO event_duplicates (
					event_id, fingerprint, cloud_account_id, tenant_id,
					first_event_id, previous_event_id, occurrence_number,
					time_since_first_seconds, time_since_previous_seconds,
					absolute_first_seen_at
				) VALUES ($1, $2, $3, $4, $5, $6, 1, 0, 0, $7)
				ON CONFLICT (event_id, cloud_account_id) DO NOTHING
			`
			_, err := db.ExecContext(ctx, insertQuery,
				event.Id,
				*event.Fingerprint,
				*event.CloudAccountId,
				event.Tenant,
				event.Id,        // first_event_id (self-reference for first occurrence)
				event.Id,        // previous_event_id (self-reference for first occurrence)
				event.CreatedAt, // absolute_first_seen_at
			)
			if err != nil {
				return 0, fmt.Errorf("failed to insert first occurrence: %w", err)
			}

			slog.InfoContext(ctx, "First occurrence of fingerprint",
				"event_id", event.Id,
				"fingerprint", *event.Fingerprint,
			)
			return 1, nil
		}
		return 0, fmt.Errorf("failed to query duplicate chain: %w", err)
	}

	// Gap between the chain's latest occurrence and this one, measured start to start.
	timeSincePrevious := int(event.StartsAt.Sub(chainInfo.LatestEventStartsAt).Seconds())

	// Start a NEW chain (occurrence=1), keeping a reference to the old chain via
	// previous_event_id, when the old chain is no longer live. Two triggers:
	//   - the chain's first event is RESOLVED: a recurrence after a fix.
	//   - the gap to the previous occurrence exceeds the source's dedup window: a
	//     fingerprint firing again a day later is a new incident, not the 300th duplicate
	//     of one from three months ago. Without this the chain is effectively immortal,
	//     since the RESOLVED check almost never fires (nb_status rarely reaches RESOLVED).
	// Deliberately NOT "the previous occurrence has closed" on its own: sources here clear
	// alerts within minutes, so that alone would turn one flapping alert into hundreds of
	// separate incidents. The window is what separates flapping from genuine recurrence.
	//
	// The gap is measured from the previous occurrence's START, not its ends_at, even
	// though ends_at would more precisely describe how long the fingerprint was quiet.
	// ends_at is not trustworthy across sources: on `anomaly` its median implies a 0.5h
	// alert but p95 implies 76 DAYS, with a third of rows claiming durations over a week.
	// Those far-future ends sit past the next occurrence's start, so an ends_at-based gap
	// is permanently negative and the chain never breaks — measured on live data it left
	// 83 chains running over 30 days versus 1 here, i.e. it failed toward exactly the
	// over-suppression this change exists to fix. Starting from the previous start instead
	// can split one genuinely long-running alert into two chains, which surfaces the event
	// as OPEN rather than hiding it as a duplicate: the safe direction to be wrong in.
	//
	// A negative gap means out-of-order delivery — this event predates the chain's latest,
	// so it can never trip the window and the existing chaining behaviour is unchanged.
	dedupWindow := dedupWindowForSource(event.Source)
	chainWentStale := timeSincePrevious > int(dedupWindow.Seconds())

	if chainInfo.FirstEventNBStatus == NBStatusResolved || chainWentStale {
		insertQuery := `
			INSERT INTO event_duplicates (
				event_id, fingerprint, cloud_account_id, tenant_id,
				first_event_id, previous_event_id, occurrence_number,
				time_since_first_seconds, time_since_previous_seconds,
				absolute_first_seen_at
			) VALUES ($1, $2, $3, $4, $5, $6, 1, 0, 0, $7)
			ON CONFLICT (event_id, cloud_account_id) DO NOTHING
		`
		_, err := db.ExecContext(ctx, insertQuery,
			event.Id,
			*event.Fingerprint,
			*event.CloudAccountId,
			event.Tenant,
			event.Id,                      // first_event_id = self (new chain)
			chainInfo.FirstEventID,        // previous_event_id = old chain's first event (keeps reference)
			chainInfo.AbsoluteFirstSeenAt, // absolute_first_seen_at (carry from old chain, never reset)
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert new chain: %w", err)
		}

		reason := "resolved"
		if chainWentStale {
			reason = "dedup_window_elapsed"
		}
		slog.InfoContext(ctx, "Recurrence - starting new chain",
			"event_id", event.Id,
			"reason", reason,
			"previous_chain_first_event_id", chainInfo.FirstEventID,
			"time_since_previous_seconds", timeSincePrevious,
			"dedup_window_seconds", int(dedupWindow.Seconds()),
			"fingerprint", *event.Fingerprint,
		)
		return 1, nil
	}

	// This is a duplicate event - continue the existing chain
	occurrenceNumber := chainInfo.OccurrenceNumber + 1
	timeSinceFirst := int(event.StartsAt.Sub(chainInfo.FirstEventStartsAt).Seconds())

	insertQuery := `
		INSERT INTO event_duplicates (
			event_id, fingerprint, cloud_account_id, tenant_id,
			first_event_id, previous_event_id, occurrence_number,
			time_since_first_seconds, time_since_previous_seconds,
			absolute_first_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (event_id, cloud_account_id) DO NOTHING
	`

	_, err = db.ExecContext(ctx, insertQuery,
		event.Id,
		*event.Fingerprint,
		*event.CloudAccountId,
		event.Tenant,
		chainInfo.FirstEventID,  // Always use the TRUE first event
		chainInfo.LatestEventID, // previous_event_id (most recent in chain)
		occurrenceNumber,
		timeSinceFirst,
		timeSincePrevious,
		chainInfo.AbsoluteFirstSeenAt, // absolute_first_seen_at (carry from chain)
	)

	if err != nil {
		return 0, fmt.Errorf("failed to insert duplicate record: %w", err)
	}

	slog.InfoContext(ctx, "Duplicate event detected",
		"event_id", event.Id,
		"first_event_id", chainInfo.FirstEventID,
		"previous_event_id", chainInfo.LatestEventID,
		"occurrence_number", occurrenceNumber,
		"time_since_first_seconds", timeSinceFirst,
		"time_since_previous_seconds", timeSincePrevious,
		"fingerprint", *event.Fingerprint,
	)

	return occurrenceNumber, nil
}

// detectAndRecordCorrelations finds and records correlated events.
// db is sqlx.ExtContext (satisfied by both *sqlx.DB and *sqlx.Tx) so the full
// path can run inside a rolled-back transaction in integration tests.
func detectAndRecordCorrelations(ctx context.Context, db sqlx.ExtContext, event *models.Event) (string, float64, error) {
	// Check for required fields
	if event.CloudAccountId == nil || event.Fingerprint == nil || event.StartsAt == nil || event.Tenant == nil || *event.Tenant == "" {
		return "", 0, fmt.Errorf("event missing required fields (cloud_account_id, fingerprint, starts_at, or tenant)")
	}

	// Parse service map from event evidences
	serviceMap, err := parseServiceMapFromEvent(event)
	if err != nil {
		slog.DebugContext(ctx, "No service map available for correlation",
			"event_id", event.Id,
			"error", err,
		)
		// Continue with temporal correlation only (no service map needed)
		serviceMap = nil
	}

	// Parse trace-participant service set from trace evidence. Used as a
	// per-request causality fallback when ServiceMap can't produce a direct
	// dependency — services that participated in the triaged event's failing
	// trace are by definition related to it, which is a stronger signal than
	// an aggregate graph edge.
	traceServices := extractTraceServiceSet(event)

	// Query recent events to check for correlation (exclude same fingerprint - those are duplicates)
	// Use DISTINCT ON to get only one representative event per fingerprint to avoid correlating with all duplicates
	query := `
		SELECT DISTINCT ON (fingerprint)
			id, fingerprint, starts_at, ends_at, status,
			subject_name, subject_namespace, subject_owner, subject_owner_kind,
			service_key, cloud_resource_id
		FROM events
		WHERE cloud_account_id = $1
		  AND fingerprint != $2
		  AND starts_at >= $3
		  AND starts_at <= $4
		ORDER BY fingerprint, starts_at DESC
		LIMIT $5
	`

	// Time window: 10 minutes before and after current event
	startWindow := event.StartsAt.Add(-MaxTimeWindowMinutes * time.Minute)
	endWindow := event.StartsAt.Add(MaxTimeWindowMinutes * time.Minute)

	var recentEvents []models.Event
	err = sqlx.SelectContext(ctx, db, &recentEvents, query,
		*event.CloudAccountId,
		*event.Fingerprint,
		startWindow,
		endWindow,
		MaxRecentEventsToCheck,
	)

	if err != nil {
		return "", 0, fmt.Errorf("failed to query recent events: %w", err)
	}

	slog.DebugContext(ctx, "Checking correlations",
		"event_id", event.Id,
		"recent_events_count", len(recentEvents),
		"has_service_map", serviceMap != nil,
		"trace_service_count", len(traceServices),
	)

	highestScore := 0.0
	highestType := ""

	// First pass: score every candidate, track the strongest correlation (used by
	// ComputeScore), and collect the correlated ones. The existence check and the
	// insert are then batched (one query each) rather than run per candidate — the
	// old per-candidate COUNT(*) + 2 INSERT was an N+1 that dominated triage
	// latency (avg ~22 correlations/event, p95 ~103).
	var correlated []correlatedCandidate
	for i := range recentEvents {
		recentEvent := &recentEvents[i]

		// Argument order follows calculateCorrelationScore's docstring:
		// event1 is the candidate (pulled from the past window), event2 is
		// the triaged event. This lets the existing causality bonus
		// (event2.After(event1)) correctly fire when the candidate is older
		// — the common case during live triage.
		result := calculateCorrelationScore(recentEvent, event, serviceMap, traceServices)
		if !result.IsCorrelated {
			continue
		}
		if result.CorrelationScore > highestScore {
			highestScore = result.CorrelationScore
			highestType = result.CorrelationType
		}
		correlated = append(correlated, correlatedCandidate{event: recentEvent, result: result})
	}

	correlationCount := 0
	if len(correlated) > 0 {
		// Batch the per-pair dedup that insertCorrelation used to do per candidate:
		// drop candidates whose fingerprint pair already has a correlation from a
		// prior triage run. recentEvents is already DISTINCT ON (fingerprint), so
		// candidate fingerprints are unique within this run.
		candidateFps := make([]string, 0, len(correlated))
		for _, c := range correlated {
			if c.event.Fingerprint != nil {
				candidateFps = append(candidateFps, *c.event.Fingerprint)
			}
		}

		existing, err := existingCorrelatedFingerprints(ctx, db, *event.Fingerprint, candidateFps, *event.CloudAccountId)
		if err != nil {
			return "", 0, fmt.Errorf("failed to batch-check existing correlations: %w", err)
		}

		toInsert := filterNewCorrelations(correlated, existing)
		if len(toInsert) > 0 {
			if err := batchInsertCorrelations(ctx, db, event, toInsert); err != nil {
				return "", 0, fmt.Errorf("failed to batch-insert correlations: %w", err)
			}
			correlationCount = len(toInsert)
		}
	}

	slog.InfoContext(ctx, "Correlation detection completed",
		"event_id", event.Id,
		"correlations_found", correlationCount,
	)

	return highestType, highestScore, nil
}

// correlatedCandidate pairs a recent event that correlated with the triaged
// event with its computed correlation result, so the dedup check and insert can
// be batched after the scoring pass.
type correlatedCandidate struct {
	event  *models.Event
	result CorrelationResult
}

// existingCorrelatedFingerprints returns the subset of candidateFingerprints
// that already have a correlation with the triaged event's fingerprint in this
// account. This replaces the per-candidate COUNT(*) existence check with a
// single ANY() lookup, preserving the original per-pair dedup semantic
// (correlations are kept at fingerprint-pair granularity, not event-pair).
func existingCorrelatedFingerprints(ctx context.Context, db sqlx.ExtContext, triagedFingerprint string, candidateFingerprints []string, cloudAccountID string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(candidateFingerprints) == 0 {
		return out, nil
	}

	// Mirrors the old check's direction: e1 = triaged (event_id side),
	// e2 = candidate (related_event_id side).
	query := `
		SELECT DISTINCT e2.fingerprint
		FROM event_correlations ec
		JOIN events e1 ON e1.id = ec.event_id
		JOIN events e2 ON e2.id = ec.related_event_id
		WHERE e1.fingerprint = $1
		  AND e2.fingerprint = ANY($2)
		  AND ec.cloud_account_id = $3
	`

	var fps []string
	if err := sqlx.SelectContext(ctx, db, &fps, query, triagedFingerprint, pq.Array(candidateFingerprints), cloudAccountID); err != nil {
		return nil, err
	}
	for _, fp := range fps {
		out[fp] = true
	}
	return out, nil
}

// filterNewCorrelations drops candidates whose fingerprint pair already has a
// correlation (present in existing), preserving the original per-pair dedup.
// Candidates with a nil fingerprint are kept (they can't be in existing).
func filterNewCorrelations(correlated []correlatedCandidate, existing map[string]bool) []correlatedCandidate {
	out := make([]correlatedCandidate, 0, len(correlated))
	for _, c := range correlated {
		if c.event.Fingerprint != nil && existing[*c.event.Fingerprint] {
			continue // fingerprint pair already correlated
		}
		out = append(out, c)
	}
	return out
}

// correlationInsertCols is the number of columns per event_correlations row,
// used to number placeholders in the batched multi-row INSERT.
const correlationInsertCols = 9

// buildCorrelationInsert builds the multi-row INSERT statement and its argument
// list for all candidates. Each candidate contributes two rows (both
// directions), mirroring exactly what the old per-candidate insertCorrelation
// wrote: direction 1 (triaged -> candidate) with the forward offset, direction 2
// (candidate -> triaged) with the negated offset. Kept pure (no DB) so the
// placeholder numbering and per-direction argument binding are unit-testable —
// that is where a batched-insert bug would hide. Returns ("", nil) for no
// candidates.
func buildCorrelationInsert(triaged *models.Event, candidates []correlatedCandidate) (string, []interface{}) {
	if len(candidates) == 0 {
		return "", nil
	}

	valueClauses := make([]string, 0, len(candidates)*2)
	args := make([]interface{}, 0, len(candidates)*2*correlationInsertCols)

	appendRow := func(eventID, relatedID string, timeOffset, depDist int, res *CorrelationResult) {
		base := len(args)
		ph := make([]string, correlationInsertCols)
		for j := 0; j < correlationInsertCols; j++ {
			ph[j] = fmt.Sprintf("$%d", base+j+1)
		}
		valueClauses = append(valueClauses, "("+strings.Join(ph, ", ")+")")
		args = append(args,
			eventID, relatedID, *triaged.CloudAccountId, triaged.Tenant,
			res.CorrelationType, res.CorrelationScore, res.CorrelationReason,
			timeOffset, depDist,
		)
	}

	for i := range candidates {
		c := &candidates[i]
		// Direction 1: triaged -> candidate.
		appendRow(triaged.Id, c.event.Id, c.result.TimeOffsetMinutes, c.result.DependencyDistance, &c.result)
		// Direction 2: candidate -> triaged (negated offset), for efficient reverse queries.
		appendRow(c.event.Id, triaged.Id, -c.result.TimeOffsetMinutes, c.result.DependencyDistance, &c.result)
	}

	query := `
		INSERT INTO event_correlations (
			event_id, related_event_id, cloud_account_id, tenant_id,
			correlation_type, correlation_score, correlation_reason,
			time_offset_minutes, dependency_distance
		) VALUES ` + strings.Join(valueClauses, ", ") + `
		ON CONFLICT (related_event_id, event_id, cloud_account_id) DO NOTHING`

	return query, args
}

// batchInsertCorrelations inserts bidirectional correlation rows for every
// candidate in a single multi-row INSERT, replacing the per-candidate pair of
// INSERTs. ON CONFLICT DO NOTHING keeps it idempotent across concurrent triage
// runs (same guarantee the single-row insert had).
func batchInsertCorrelations(ctx context.Context, db sqlx.ExtContext, triaged *models.Event, candidates []correlatedCandidate) error {
	query, args := buildCorrelationInsert(triaged, candidates)
	if query == "" {
		return nil
	}
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to batch-insert %d correlations: %w", len(candidates), err)
	}
	return nil
}
