package triage

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// HistoricalStats represents 30-day historical metrics for a fingerprint
type HistoricalStats struct {
	TotalEvents      int        `json:"total_events"`
	FiringCount      int        `json:"firing_count"`
	ResolvedCount    int        `json:"resolved_count"`
	ClosedCount      int        `json:"closed_count"`
	ResolutionRate   float64    `json:"resolution_rate"`    // resolved / total
	ClosureRate      float64    `json:"closure_rate"`       // closed / total
	NoiseLevel       float64    `json:"noise_level"`        // (firing - resolved) / total
	AvgDurationHours float64    `json:"avg_duration_hours"` // Average time to resolution
	FirstSeenAt      *time.Time `json:"first_seen_at"`
	LastSeenAt       *time.Time `json:"last_seen_at"`
}

// HourlyBucket represents event counts for a single hour
type HourlyBucket struct {
	Hour          time.Time `json:"hour"`
	FiringCount   int       `json:"firing_count"`
	ResolvedCount int       `json:"resolved_count"`
	ClosedCount   int       `json:"closed_count"`
}

// ComputeHistoricalStats calculates 30-day historical metrics for a fingerprint
func ComputeHistoricalStats(ctx context.Context, db *sqlx.DB, fingerprint string, accountId string) (*HistoricalStats, error) {
	query := `
		SELECT
			COUNT(*) as total_events,
			COUNT(*) FILTER (WHERE status = 'firing') as firing_count,
			COUNT(*) FILTER (WHERE status = 'resolved') as resolved_count,
			COUNT(*) FILTER (WHERE status = 'closed') as closed_count,
			MIN(starts_at) as first_seen_at,
			MAX(starts_at) as last_seen_at,
			AVG(EXTRACT(EPOCH FROM (COALESCE(ends_at, NOW()) - starts_at)) / 3600) as avg_duration_hours
		FROM events
		WHERE fingerprint = $1
		  AND cloud_account_id = $2
		  AND starts_at >= NOW() - INTERVAL '30 days'
	`

	var result struct {
		TotalEvents      int        `db:"total_events"`
		FiringCount      int        `db:"firing_count"`
		ResolvedCount    int        `db:"resolved_count"`
		ClosedCount      int        `db:"closed_count"`
		FirstSeenAt      *time.Time `db:"first_seen_at"`
		LastSeenAt       *time.Time `db:"last_seen_at"`
		AvgDurationHours *float64   `db:"avg_duration_hours"`
	}

	err := db.GetContext(ctx, &result, query, fingerprint, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to compute historical stats: %w", err)
	}

	// Handle case where no events found
	if result.TotalEvents == 0 {
		return &HistoricalStats{
			TotalEvents:      0,
			FiringCount:      0,
			ResolvedCount:    0,
			ClosedCount:      0,
			ResolutionRate:   0,
			ClosureRate:      0,
			NoiseLevel:       0,
			AvgDurationHours: 0,
		}, nil
	}

	stats := &HistoricalStats{
		TotalEvents:   result.TotalEvents,
		FiringCount:   result.FiringCount,
		ResolvedCount: result.ResolvedCount,
		ClosedCount:   result.ClosedCount,
		FirstSeenAt:   result.FirstSeenAt,
		LastSeenAt:    result.LastSeenAt,
	}

	// Calculate rates
	total := float64(result.TotalEvents)
	stats.ResolutionRate = float64(result.ResolvedCount) / total
	stats.ClosureRate = float64(result.ClosedCount) / total
	stats.NoiseLevel = float64(result.FiringCount-result.ResolvedCount) / total

	// Handle nil avg duration
	if result.AvgDurationHours != nil {
		stats.AvgDurationHours = *result.AvgDurationHours
	}

	return stats, nil
}

// ComputeHourlyTrend calculates 24-hour event trend grouped by hour
func ComputeHourlyTrend(ctx context.Context, db *sqlx.DB, fingerprint string, accountId string) ([]HourlyBucket, error) {
	query := `
		SELECT
			date_trunc('hour', starts_at) as hour,
			COUNT(*) FILTER (WHERE status = 'firing') as firing_count,
			COUNT(*) FILTER (WHERE status = 'resolved') as resolved_count,
			COUNT(*) FILTER (WHERE status = 'closed') as closed_count
		FROM events
		WHERE fingerprint = $1
		  AND cloud_account_id = $2
		  AND starts_at >= NOW() - INTERVAL '24 hours'
		GROUP BY date_trunc('hour', starts_at)
		ORDER BY hour ASC
	`

	var buckets []struct {
		Hour          time.Time `db:"hour"`
		FiringCount   int       `db:"firing_count"`
		ResolvedCount int       `db:"resolved_count"`
		ClosedCount   int       `db:"closed_count"`
	}

	err := db.SelectContext(ctx, &buckets, query, fingerprint, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to compute hourly trend: %w", err)
	}

	// Convert to HourlyBucket slice
	result := make([]HourlyBucket, len(buckets))
	for i, bucket := range buckets {
		result[i] = HourlyBucket{
			Hour:          bucket.Hour,
			FiringCount:   bucket.FiringCount,
			ResolvedCount: bucket.ResolvedCount,
			ClosedCount:   bucket.ClosedCount,
		}
	}

	return result, nil
}

// GetDuplicateChain retrieves all duplicate events for a given event with tenant isolation
func GetDuplicateChain(ctx context.Context, db *sqlx.DB, eventId string, tenantID string) ([]DuplicateEvent, error) {
	// Get all events with the same first_event_id using a subquery
	query := `
		SELECT
			ed.event_id,
			ed.first_event_id,
			ed.occurrence_number,
			ed.created_at,
			e.starts_at as event_starts_at,
			e.status as event_state
		FROM event_duplicates ed
		JOIN events e ON e.id = ed.event_id
		WHERE ed.tenant_id = $2 AND ed.first_event_id = (
			SELECT first_event_id
			FROM event_duplicates
			WHERE event_id = $1 AND tenant_id = $2
		)
		ORDER BY ed.occurrence_number ASC
	`

	var duplicates []struct {
		EventID          string    `db:"event_id"`
		FirstEventID     string    `db:"first_event_id"`
		OccurrenceNumber int       `db:"occurrence_number"`
		CreatedAt        time.Time `db:"created_at"`
		EventStartsAt    time.Time `db:"event_starts_at"`
		EventState       string    `db:"event_state"`
	}

	err := db.SelectContext(ctx, &duplicates, query, eventId, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get duplicate chain: %w", err)
	}

	// Convert to DuplicateEvent slice
	result := make([]DuplicateEvent, len(duplicates))
	for i, dup := range duplicates {
		result[i] = DuplicateEvent{
			EventID:          dup.EventID,
			FirstEventID:     dup.FirstEventID,
			OccurrenceNumber: dup.OccurrenceNumber,
			CreatedAt:        dup.CreatedAt,
			EventStartsAt:    dup.EventStartsAt,
			EventState:       dup.EventState,
		}
	}

	return result, nil
}

// DuplicateEvent represents a single event in the duplicate chain
type DuplicateEvent struct {
	EventID          string    `json:"event_id"`
	FirstEventID     string    `json:"first_event_id"`
	OccurrenceNumber int       `json:"occurrence_number"`
	CreatedAt        time.Time `json:"created_at"`
	EventStartsAt    time.Time `json:"event_starts_at"`
	EventState       string    `json:"event_state"`
}

// GetCorrelatedEvents retrieves all events correlated with the given event
// Uses fingerprint matching to handle deduplication
// Enforces tenant isolation for security
func GetCorrelatedEvents(ctx context.Context, db *sqlx.DB, eventId string, tenantID string) ([]CorrelatedEvent, error) {
	// Find correlations using fingerprint matching (handles deduplication)
	// Returns the event with correlated fingerprint that's CLOSEST in time to the input event
	// Optimized: Split OR conditions into UNION for better index usage (13x faster)
	query := `
		WITH input_event AS (
			SELECT fingerprint, cloud_account_id, starts_at, tenant
			FROM events
			WHERE id = $1 AND tenant = $2
		),
		-- Find correlations where input event's fingerprint matches event_id side
		correlations_as_event AS (
			SELECT
				ec.event_id,
				ec.related_event_id,
				ec.correlation_type,
				ec.correlation_score,
				ec.correlation_reason,
				ec.time_offset_minutes,
				ec.dependency_distance,
				ec.created_at,
				e_related.fingerprint as correlated_fingerprint
			FROM event_correlations ec
			JOIN events e_event ON e_event.id = ec.event_id
			JOIN events e_related ON e_related.id = ec.related_event_id
			CROSS JOIN input_event ie
			WHERE e_event.fingerprint = ie.fingerprint
			  AND e_event.tenant = ie.tenant
			  AND e_related.tenant = ie.tenant
		),
		-- Find correlations where input event's fingerprint matches related_event_id side
		correlations_as_related AS (
			SELECT
				ec.event_id,
				ec.related_event_id,
				ec.correlation_type,
				ec.correlation_score,
				ec.correlation_reason,
				ec.time_offset_minutes,
				ec.dependency_distance,
				ec.created_at,
				e_event.fingerprint as correlated_fingerprint
			FROM event_correlations ec
			JOIN events e_event ON e_event.id = ec.event_id
			JOIN events e_related ON e_related.id = ec.related_event_id
			CROSS JOIN input_event ie
			WHERE e_related.fingerprint = ie.fingerprint
			  AND e_event.tenant = ie.tenant
			  AND e_related.tenant = ie.tenant
		),
		-- Combine all correlations
		all_correlations AS (
			SELECT * FROM correlations_as_event
			UNION ALL
			SELECT * FROM correlations_as_related
		)
		SELECT DISTINCT ON (e_correlated.fingerprint)
			ac.event_id,
			ac.related_event_id,
			ac.correlation_type,
			ac.correlation_score,
			ac.correlation_reason,
			ac.time_offset_minutes,
			ac.dependency_distance,
			ac.created_at,
			e_correlated.title as correlated_title,
			e_correlated.fingerprint as correlated_fingerprint,
			e_correlated.starts_at as correlated_starts_at,
			e_correlated.status as correlated_state,
			e_correlated.finding_type as correlated_finding_type,
			e_correlated.subject_name,
			e_correlated.subject_namespace,
			e_correlated.subject_owner,
			e_correlated.subject_owner_kind
		FROM all_correlations ac
		JOIN events e_correlated ON e_correlated.fingerprint = ac.correlated_fingerprint
		CROSS JOIN input_event ie
		WHERE e_correlated.cloud_account_id = ie.cloud_account_id
		  AND e_correlated.tenant = ie.tenant
		  AND e_correlated.id != $1
		ORDER BY e_correlated.fingerprint, ABS(EXTRACT(EPOCH FROM (e_correlated.starts_at - ie.starts_at))) ASC
	`

	var correlations []struct {
		EventID               string    `db:"event_id"`
		RelatedEventID        string    `db:"related_event_id"`
		CorrelationType       string    `db:"correlation_type"`
		CorrelationScore      float64   `db:"correlation_score"`
		CorrelationReason     string    `db:"correlation_reason"`
		TimeOffsetMinutes     int       `db:"time_offset_minutes"`
		DependencyDistance    int       `db:"dependency_distance"`
		CreatedAt             time.Time `db:"created_at"`
		CorrelatedTitle       string    `db:"correlated_title"`
		CorrelatedFingerprint string    `db:"correlated_fingerprint"`
		CorrelatedStartsAt    time.Time `db:"correlated_starts_at"`
		CorrelatedState       string    `db:"correlated_state"`
		CorrelatedFindingType *string   `db:"correlated_finding_type"`
		SubjectName           *string   `db:"subject_name"`
		SubjectNamespace      *string   `db:"subject_namespace"`
		SubjectOwner          *string   `db:"subject_owner"`
		SubjectOwnerKind      *string   `db:"subject_owner_kind"`
	}

	err := db.SelectContext(ctx, &correlations, query, eventId, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get correlated events: %w", err)
	}

	// Convert to CorrelatedEvent slice
	result := make([]CorrelatedEvent, len(correlations))
	for i, corr := range correlations {
		result[i] = CorrelatedEvent{
			CorrelatedEventID:     corr.RelatedEventID,
			CorrelationType:       corr.CorrelationType,
			CorrelationScore:      corr.CorrelationScore,
			CorrelationReason:     corr.CorrelationReason,
			TimeOffsetMinutes:     corr.TimeOffsetMinutes,
			DependencyDistance:    corr.DependencyDistance,
			CorrelatedTitle:       corr.CorrelatedTitle,
			CorrelatedFingerprint: corr.CorrelatedFingerprint,
			CorrelatedStartsAt:    corr.CorrelatedStartsAt,
			CorrelatedState:       corr.CorrelatedState,
			CorrelatedFindingType: corr.CorrelatedFindingType,
			SubjectName:           corr.SubjectName,
			SubjectNamespace:      corr.SubjectNamespace,
			SubjectOwner:          corr.SubjectOwner,
			SubjectOwnerKind:      corr.SubjectOwnerKind,
		}
	}

	return result, nil
}

// CorrelatedEvent represents a correlated event with metadata
type CorrelatedEvent struct {
	CorrelatedEventID     string    `json:"correlated_event_id"`
	CorrelationType       string    `json:"correlation_type"`
	CorrelationScore      float64   `json:"correlation_score"`
	CorrelationReason     string    `json:"correlation_reason"`
	TimeOffsetMinutes     int       `json:"time_offset_minutes"`
	DependencyDistance    int       `json:"dependency_distance"`
	CorrelatedTitle       string    `json:"correlated_title"`
	CorrelatedFingerprint string    `json:"correlated_fingerprint"`
	CorrelatedStartsAt    time.Time `json:"correlated_starts_at"`
	CorrelatedState       string    `json:"correlated_state"`
	CorrelatedFindingType *string   `json:"correlated_finding_type"`
	SubjectName           *string   `json:"subject_name"`
	SubjectNamespace      *string   `json:"subject_namespace"`
	SubjectOwner          *string   `json:"subject_owner"`
	SubjectOwnerKind      *string   `json:"subject_owner_kind"`
}
