package triage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// ProcessEvent performs immediate triage on a new event
// This includes triage rules check, deduplication detection, and correlation with related events
func ProcessEvent(ctx context.Context, db *sqlx.DB, event *models.Event) error {
	startTime := time.Now()

	// Step 1: Deduplication detection FIRST (target: < 10ms)
	// This must run before CheckTriageRules so that the occurrence number is available
	// for occurrence-based rules (like the system auto-duplicate rule)
	if err := detectAndRecordDuplicate(ctx, db, event); err != nil {
		slog.ErrorContext(ctx, "Failed to detect duplicate", "error", err, "event_id", event.Id)
		// Continue processing - don't fail entire triage on duplicate detection error
	}

	// Step 2: Check triage rules (after deduplication so occurrence_number is available)
	ruleResult, err := CheckTriageRules(ctx, db, event)
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
	if err := detectAndRecordCorrelations(ctx, db, event); err != nil {
		slog.ErrorContext(ctx, "Failed to detect correlations", "error", err, "event_id", event.Id)
		// Continue processing - don't fail entire triage on correlation error
	}

	// Step 4: Compute and save score (target: < 50ms)
	// Apply any score adjustments from rules
	result, err := ComputeScore(ctx, db, event)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to compute score", "error", err, "event_id", event.Id)
		// Continue processing - don't fail entire triage on scoring error
	} else {
		// Apply score adjustment from rules if present
		if ruleResult != nil && ruleResult.ScoreAdjustment != nil {
			result.Score = clamp(result.Score+ruleResult.ScoreAdjustment.Adjustment, 0, 100)
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

// detectAndRecordDuplicate checks if this event is a duplicate and records it
// Creates chains per fingerprint. If the chain's first event is RESOLVED, starts a new chain
// while keeping a reference to the previous chain via previous_event_id.
func detectAndRecordDuplicate(ctx context.Context, db *sqlx.DB, event *models.Event) error {
	// Check for required fields
	if event.Fingerprint == nil || event.CloudAccountId == nil || event.StartsAt == nil {
		return fmt.Errorf("event missing required fields (fingerprint, cloud_account_id, or starts_at)")
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

	err := db.GetContext(ctx, &chainInfo, chainQuery,
		*event.Fingerprint,
		*event.CloudAccountId,
		event.Id,
	)

	if err != nil {
		// sql.ErrNoRows means this is the first event with this fingerprint
		if err.Error() == "sql: no rows in result set" {
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
				return fmt.Errorf("failed to insert first occurrence: %w", err)
			}

			slog.InfoContext(ctx, "First occurrence of fingerprint",
				"event_id", event.Id,
				"fingerprint", *event.Fingerprint,
			)
			return nil
		}
		return fmt.Errorf("failed to query duplicate chain: %w", err)
	}

	// Check if the first event in the chain is RESOLVED
	// If so, start a NEW chain (occurrence=1) but keep reference to old chain
	if chainInfo.FirstEventNBStatus == NBStatusResolved {
		// Start a new chain - this is a recurrence after resolution
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
			return fmt.Errorf("failed to insert new chain after resolved: %w", err)
		}

		slog.InfoContext(ctx, "Recurrence after resolved - starting new chain",
			"event_id", event.Id,
			"previous_chain_first_event_id", chainInfo.FirstEventID,
			"fingerprint", *event.Fingerprint,
		)
		return nil
	}

	// This is a duplicate event - continue the existing chain
	occurrenceNumber := chainInfo.OccurrenceNumber + 1
	timeSinceFirst := int(event.StartsAt.Sub(chainInfo.FirstEventStartsAt).Seconds())
	timeSincePrevious := int(event.StartsAt.Sub(chainInfo.LatestEventStartsAt).Seconds())

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
		return fmt.Errorf("failed to insert duplicate record: %w", err)
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

	return nil
}

// detectAndRecordCorrelations finds and records correlated events
func detectAndRecordCorrelations(ctx context.Context, db *sqlx.DB, event *models.Event) error {
	// Check for required fields
	if event.CloudAccountId == nil || event.Fingerprint == nil || event.StartsAt == nil {
		return fmt.Errorf("event missing required fields (cloud_account_id, fingerprint, or starts_at)")
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
	err = db.SelectContext(ctx, &recentEvents, query,
		*event.CloudAccountId,
		*event.Fingerprint,
		startWindow,
		endWindow,
		MaxRecentEventsToCheck,
	)

	if err != nil {
		return fmt.Errorf("failed to query recent events: %w", err)
	}

	slog.DebugContext(ctx, "Checking correlations",
		"event_id", event.Id,
		"recent_events_count", len(recentEvents),
		"has_service_map", serviceMap != nil,
		"trace_service_count", len(traceServices),
	)

	correlationCount := 0

	// Calculate correlation score for each recent event
	for i := range recentEvents {
		recentEvent := &recentEvents[i]

		// Argument order follows calculateCorrelationScore's docstring:
		// event1 is the candidate (pulled from the past window), event2 is
		// the triaged event. This lets the existing causality bonus
		// (event2.After(event1)) correctly fire when the candidate is older
		// — the common case during live triage.
		result := calculateCorrelationScore(recentEvent, event, serviceMap, traceServices)

		if result.IsCorrelated {
			// Insert bidirectional correlation
			err := insertCorrelation(ctx, db, event, recentEvent, &result)
			if err != nil {
				slog.ErrorContext(ctx, "Failed to insert correlation",
					"event_id_1", event.Id,
					"event_id_2", recentEvent.Id,
					"error", err,
				)
				continue
			}

			correlationCount++

			slog.InfoContext(ctx, "Correlation detected",
				"event_id", event.Id,
				"correlated_event_id", recentEvent.Id,
				"correlation_type", result.CorrelationType,
				"correlation_score", result.CorrelationScore,
				"correlation_reason", result.CorrelationReason,
			)
		}
	}

	slog.InfoContext(ctx, "Correlation detection completed",
		"event_id", event.Id,
		"correlations_found", correlationCount,
	)

	return nil
}

// insertCorrelation inserts a bidirectional correlation record
func insertCorrelation(ctx context.Context, db *sqlx.DB, event1, event2 *models.Event, result *CorrelationResult) error {
	// Check if correlation already exists between these fingerprints
	var existingCount int
	checkQuery := `
		SELECT COUNT(*)
		FROM event_correlations ec
		JOIN events e1 ON e1.id = ec.event_id
		JOIN events e2 ON e2.id = ec.related_event_id
		WHERE e1.fingerprint = $1
		  AND e2.fingerprint = $2
		  AND ec.cloud_account_id = $3
	`
	err := db.GetContext(ctx, &existingCount, checkQuery,
		*event1.Fingerprint, *event2.Fingerprint, *event1.CloudAccountId)
	if err != nil {
		return fmt.Errorf("failed to check existing correlation: %w", err)
	}

	if existingCount > 0 {
		// Correlation already exists for this fingerprint pair, skip
		slog.DebugContext(ctx, "Correlation already exists for fingerprint pair, skipping",
			"fingerprint1", *event1.Fingerprint,
			"fingerprint2", *event2.Fingerprint,
		)
		return nil
	}

	insertQuery := `
		INSERT INTO event_correlations (
			event_id, related_event_id, cloud_account_id, tenant_id,
			correlation_type, correlation_score, correlation_reason,
			time_offset_minutes, dependency_distance
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (related_event_id, event_id, cloud_account_id) DO NOTHING
	`

	// Insert both directions for efficient querying
	// Direction 1: event1 -> event2
	_, err = db.ExecContext(ctx, insertQuery,
		event1.Id,
		event2.Id,
		*event1.CloudAccountId,
		event1.Tenant,
		result.CorrelationType,
		result.CorrelationScore,
		result.CorrelationReason,
		result.TimeOffsetMinutes,
		result.DependencyDistance,
	)
	if err != nil {
		return fmt.Errorf("failed to insert correlation (1->2): %w", err)
	}

	// Direction 2: event2 -> event1
	_, err = db.ExecContext(ctx, insertQuery,
		event2.Id,
		event1.Id,
		*event1.CloudAccountId,
		event1.Tenant,
		result.CorrelationType,
		result.CorrelationScore,
		result.CorrelationReason,
		-result.TimeOffsetMinutes, // Negative offset for reverse direction
		result.DependencyDistance,
	)
	if err != nil {
		return fmt.Errorf("failed to insert correlation (2->1): %w", err)
	}

	return nil
}
