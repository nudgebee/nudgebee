package triage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// BackfillOptions configures the backfill operation
type BackfillOptions struct {
	CloudAccountID string     // Required: process events for this account
	StartTime      *time.Time // Optional: process events from this time
	EndTime        *time.Time // Optional: process events until this time
	Fingerprint    *string    // Optional: process only events with this fingerprint
	FirstEventID   *string    // Optional: process only events in the duplicate chain of this first event
	BatchSize      int        // Number of events to process in each batch (default: 100)
	DryRun         bool       // If true, only log what would be done without making changes
}

// BackfillResult contains statistics about the backfill operation
type BackfillResult struct {
	TotalEvents         int
	DuplicatesDetected  int
	CorrelationsCreated int
	Errors              int
	Duration            time.Duration
}

// BackfillTriage processes existing events to populate triage data
// This should be run to backfill historical events or fix inconsistent data
func BackfillTriage(ctx context.Context, db *sqlx.DB, opts BackfillOptions) (*BackfillResult, error) {
	startTime := time.Now()
	result := &BackfillResult{}

	// Validate options
	if opts.CloudAccountID == "" {
		return nil, fmt.Errorf("cloud_account_id is required")
	}

	if opts.BatchSize == 0 {
		opts.BatchSize = 100
	}

	slog.InfoContext(ctx, "Starting triage backfill",
		"cloud_account_id", opts.CloudAccountID,
		"start_time", opts.StartTime,
		"end_time", opts.EndTime,
		"fingerprint", opts.Fingerprint,
		"batch_size", opts.BatchSize,
		"dry_run", opts.DryRun,
	)

	// Build query to fetch events
	query := `
		SELECT
			id, fingerprint, starts_at, ends_at, status,
			cloud_account_id, tenant,
			subject_name, subject_namespace, subject_owner, subject_owner_kind,
			evidences, created_at
		FROM events
		WHERE cloud_account_id = $1
	`
	args := []interface{}{opts.CloudAccountID}
	argIndex := 2

	if opts.StartTime != nil {
		query += fmt.Sprintf(" AND starts_at >= $%d", argIndex)
		args = append(args, *opts.StartTime)
		argIndex++
	}

	if opts.EndTime != nil {
		query += fmt.Sprintf(" AND starts_at <= $%d", argIndex)
		args = append(args, *opts.EndTime)
		argIndex++
	}

	if opts.FirstEventID != nil {
		// If FirstEventID is provided, only fetch that single event
		query += fmt.Sprintf(" AND id = $%d", argIndex)
		args = append(args, *opts.FirstEventID)
	} else if opts.Fingerprint != nil {
		query += fmt.Sprintf(" AND fingerprint = $%d", argIndex)
		args = append(args, *opts.Fingerprint)
	}

	// IMPORTANT: Process events in chronological order to build correct duplicate chains
	// Use created_at as secondary sort for deterministic ordering when starts_at is identical
	query += " ORDER BY starts_at ASC, created_at ASC"

	// Fetch all events
	var events []models.Event
	err := db.SelectContext(ctx, &events, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}

	result.TotalEvents = len(events)
	slog.InfoContext(ctx, "Fetched events for backfill",
		"count", result.TotalEvents,
	)

	if opts.DryRun {
		slog.InfoContext(ctx, "DRY RUN: Would process events", "count", result.TotalEvents)
		result.Duration = time.Since(startTime)
		return result, nil
	}

	// Clear existing triage data for these events (optional - can be toggled)
	if err := clearTriageData(ctx, db, events); err != nil {
		slog.WarnContext(ctx, "Failed to clear existing triage data", "error", err)
	}

	// Process events
	var batchResult *BackfillResult
	if opts.FirstEventID != nil && len(events) == 1 {
		// Single event mode: query-based duplicate detection
		batchResult = processSingleEvent(ctx, db, &events[0])
	} else {
		// Batch mode: in-memory chain building
		batchResult = processBatch(ctx, db, events)
	}

	result.DuplicatesDetected = batchResult.DuplicatesDetected
	result.CorrelationsCreated = batchResult.CorrelationsCreated
	result.Errors = batchResult.Errors

	result.Duration = time.Since(startTime)

	slog.InfoContext(ctx, "Triage backfill completed",
		"total_events", result.TotalEvents,
		"duplicates_detected", result.DuplicatesDetected,
		"correlations_created", result.CorrelationsCreated,
		"errors", result.Errors,
		"duration_seconds", result.Duration.Seconds(),
	)

	return result, nil
}

// clearTriageData removes existing triage data for the given events
func clearTriageData(ctx context.Context, db *sqlx.DB, events []models.Event) error {
	if len(events) == 0 {
		return nil
	}

	// Collect event IDs
	eventIDs := make([]string, len(events))
	for i, e := range events {
		eventIDs[i] = e.Id
	}

	// Delete existing duplicates
	query := `DELETE FROM event_duplicates WHERE event_id = ANY($1)`
	_, err := db.ExecContext(ctx, query, pq.Array(eventIDs))
	if err != nil {
		return fmt.Errorf("failed to delete duplicates: %w", err)
	}

	// Delete existing correlations
	query = `DELETE FROM event_correlations WHERE event_id = ANY($1) OR related_event_id = ANY($1)`
	_, err = db.ExecContext(ctx, query, pq.Array(eventIDs))
	if err != nil {
		return fmt.Errorf("failed to delete correlations: %w", err)
	}

	slog.InfoContext(ctx, "Cleared existing triage data", "events", len(events))
	return nil
}

// DeduplicateCorrelations removes duplicate correlations where the same event correlates
// multiple times to events with the same fingerprint. Keeps only one correlation per
// (event_id, fingerprint) pair - specifically the one with the highest correlation score.
func DeduplicateCorrelations(ctx context.Context, db *sqlx.DB, cloudAccountID string) error {
	slog.InfoContext(ctx, "Starting correlation deduplication", "cloud_account_id", cloudAccountID)

	// Delete duplicate correlations, keeping only the one with highest score per (event_id, fingerprint) pair
	// If scores are equal, keep the one with earliest starts_at (first occurrence)
	query := `
		DELETE FROM event_correlations ec
		USING events e
		WHERE ec.related_event_id = e.id
		AND ec.cloud_account_id = $1
		AND EXISTS (
			SELECT 1
			FROM event_correlations ec2
			JOIN events e2 ON ec2.related_event_id = e2.id
			WHERE ec2.event_id = ec.event_id
			AND ec2.cloud_account_id = ec.cloud_account_id
			AND e2.fingerprint = e.fingerprint
			AND (
				ec2.correlation_score > ec.correlation_score
				OR (
					ec2.correlation_score = ec.correlation_score
					AND e2.starts_at < e.starts_at
				)
			)
		)
	`

	result, err := db.ExecContext(ctx, query, cloudAccountID)
	if err != nil {
		return fmt.Errorf("failed to deduplicate correlations: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	slog.InfoContext(ctx, "Correlation deduplication completed",
		"cloud_account_id", cloudAccountID,
		"removed_duplicates", rowsAffected,
	)

	return nil
}

// processSingleEvent processes a single event using query-based duplicate detection.
// This version is more correct and efficient, using a single query with window functions
// to gather all necessary duplicate chain information.
func processSingleEvent(ctx context.Context, db *sqlx.DB, event *models.Event) *BackfillResult {
	result := &BackfillResult{
		TotalEvents: 1,
	}

	if event.Fingerprint == nil {
		slog.WarnContext(ctx, "Event has no fingerprint, skipping", "event_id", event.Id)
		return result
	}

	fingerprint := *event.Fingerprint

	// Efficiently find duplicate information using window functions
	var dupInfo struct {
		OccurrenceNumber    int       `db:"occurrence_number"`
		FirstEventID        string    `db:"first_event_id"`
		FirstEventStartsAt  time.Time `db:"first_event_starts_at"`
		AbsoluteFirstSeenAt time.Time `db:"absolute_first_seen_at"`
	}

	query := `
		WITH ranked_events AS (
			SELECT
				id,
				starts_at,
				created_at,
				ROW_NUMBER() OVER w AS occurrence_number,
				FIRST_VALUE(id) OVER w AS first_event_id,
				FIRST_VALUE(starts_at) OVER w AS first_event_starts_at,
				FIRST_VALUE(created_at) OVER w AS absolute_first_seen_at
			FROM events
			WHERE fingerprint = $1 AND cloud_account_id = $2
			WINDOW w AS (ORDER BY starts_at ASC, created_at ASC)
		)
		SELECT
			occurrence_number,
			first_event_id,
			first_event_starts_at,
			absolute_first_seen_at
		FROM ranked_events
		WHERE id = $3
	`
	err := db.GetContext(ctx, &dupInfo, query, fingerprint, event.CloudAccountId, event.Id)

	if err != nil {
		slog.ErrorContext(ctx, "Failed to get duplicate info",
			"error", err,
			"event_id", event.Id,
			"fingerprint", fingerprint,
		)
		result.Errors++
		return result
	}

	// Calculate time since first occurrence
	var timeSinceFirst int
	if event.StartsAt != nil && !dupInfo.FirstEventStartsAt.IsZero() {
		timeSinceFirst = int(event.StartsAt.Sub(dupInfo.FirstEventStartsAt).Seconds())
	}

	// Insert into event_duplicates
	insertQuery := `
		INSERT INTO event_duplicates (
			event_id, fingerprint, cloud_account_id, tenant_id,
			first_event_id, occurrence_number, time_since_first_seconds,
			absolute_first_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_id, cloud_account_id) DO UPDATE SET
			fingerprint = EXCLUDED.fingerprint,
			first_event_id = EXCLUDED.first_event_id,
			occurrence_number = EXCLUDED.occurrence_number,
			time_since_first_seconds = EXCLUDED.time_since_first_seconds,
			absolute_first_seen_at = EXCLUDED.absolute_first_seen_at
	`

	_, err = db.ExecContext(ctx, insertQuery,
		event.Id,
		fingerprint,
		event.CloudAccountId,
		event.Tenant,
		dupInfo.FirstEventID,
		dupInfo.OccurrenceNumber,
		timeSinceFirst,
		dupInfo.AbsoluteFirstSeenAt,
	)

	if err != nil {
		slog.ErrorContext(ctx, "Failed to insert duplicate entry",
			"error", err,
			"event_id", event.Id,
		)
		result.Errors++
		return result
	}

	result.DuplicatesDetected++
	slog.InfoContext(ctx, "Processed single event duplicate",
		"event_id", event.Id,
		"first_event_id", dupInfo.FirstEventID,
		"occurrence_number", dupInfo.OccurrenceNumber,
	)

	// Process correlations
	err = detectAndRecordCorrelations(ctx, db, event)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to detect correlations",
			"error", err,
			"event_id", event.Id,
		)
		result.Errors++
	} else {
		// TODO: This is a simplified count. For accurate stats,
		// detectAndRecordCorrelations should return the number of correlations created.
		result.CorrelationsCreated++
	}

	return result
}

// processBatch processes a batch of events, building duplicate chains in memory
func processBatch(ctx context.Context, db *sqlx.DB, events []models.Event) *BackfillResult {
	result := &BackfillResult{}

	// Build duplicate chains in memory by fingerprint
	// Map: fingerprint -> list of events in chronological order
	fingerprintChains := make(map[string][]*models.Event)

	for i := range events {
		event := &events[i]
		if event.Fingerprint == nil {
			continue
		}

		fingerprint := *event.Fingerprint
		fingerprintChains[fingerprint] = append(fingerprintChains[fingerprint], event)
	}

	// Process each fingerprint chain
	for fingerprint, chain := range fingerprintChains {
		if len(chain) == 0 {
			continue
		}

		// Events are already in chronological order from the query
		firstEvent := chain[0]
		firstEventID := firstEvent.Id

		slog.InfoContext(ctx, "Processing fingerprint chain",
			"fingerprint", fingerprint,
			"chain_length", len(chain),
			"first_event_id", firstEventID,
			"first_event_time", firstEvent.StartsAt,
		)

		occurrenceNumber := 0

		for _, event := range chain {
			// Skip events with nil StartsAt
			if event.StartsAt == nil {
				slog.WarnContext(ctx, "Event has nil StartsAt, skipping",
					"event_id", event.Id,
				)
				continue
			}

			occurrenceNumber++

			// Calculate time since first occurrence
			var timeSinceFirst int
			if firstEvent.StartsAt != nil {
				timeSinceFirst = int((*event.StartsAt).Sub(*firstEvent.StartsAt).Seconds())

				// If starts_at is identical, use created_at for time difference
				if timeSinceFirst == 0 && event.CreatedAt != nil && firstEvent.CreatedAt != nil {
					timeSinceFirst = int((*event.CreatedAt).Sub(*firstEvent.CreatedAt).Seconds())
				}
			}

			// Insert into database
			insertQuery := `
				INSERT INTO event_duplicates (
					event_id, fingerprint, cloud_account_id, tenant_id,
					first_event_id, occurrence_number, time_since_first_seconds,
					absolute_first_seen_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				ON CONFLICT (event_id, cloud_account_id) DO UPDATE SET
					first_event_id = EXCLUDED.first_event_id,
					occurrence_number = EXCLUDED.occurrence_number,
					time_since_first_seconds = EXCLUDED.time_since_first_seconds,
					absolute_first_seen_at = EXCLUDED.absolute_first_seen_at
			`

			_, err := db.ExecContext(ctx, insertQuery,
				event.Id,
				event.Fingerprint,
				event.CloudAccountId,
				event.Tenant,
				firstEventID,
				occurrenceNumber,
				timeSinceFirst,
				firstEvent.CreatedAt,
			)

			if err != nil {
				slog.ErrorContext(ctx, "Failed to insert duplicate",
					"error", err,
					"event_id", event.Id,
				)
				result.Errors++
			} else {
				result.DuplicatesDetected++
				slog.InfoContext(ctx, "Recorded duplicate",
					"event_id", event.Id,
					"fingerprint", fingerprint,
					"occurrence_number", occurrenceNumber,
					"first_event_id", firstEventID,
				)
			}

			// Process correlation detection for this event
			if err := detectAndRecordCorrelations(ctx, db, event); err != nil {
				slog.ErrorContext(ctx, "Failed to detect correlations",
					"error", err,
					"event_id", event.Id,
				)
				result.Errors++
			}
		}
	}

	return result
}

// BackfillByFingerprint is a convenience function to backfill all events with a specific fingerprint
func BackfillByFingerprint(ctx context.Context, db *sqlx.DB, cloudAccountID, fingerprint string) (*BackfillResult, error) {
	return BackfillTriage(ctx, db, BackfillOptions{
		CloudAccountID: cloudAccountID,
		Fingerprint:    &fingerprint,
		BatchSize:      100,
		DryRun:         false,
	})
}

// BackfillTimeRange is a convenience function to backfill all events in a time range
func BackfillTimeRange(ctx context.Context, db *sqlx.DB, cloudAccountID string, start, end time.Time) (*BackfillResult, error) {
	return BackfillTriage(ctx, db, BackfillOptions{
		CloudAccountID: cloudAccountID,
		StartTime:      &start,
		EndTime:        &end,
		BatchSize:      100,
		DryRun:         false,
	})
}
