package event

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

type EscalationResult struct {
	NewSeverity  string
	WasEscalated bool
	Reason       string
	Metadata     map[string]interface{}
}

// RecalculateSeverity recalculates event severity based on duration thresholds
func RecalculateSeverity(
	ctx context.Context,
	db *sqlx.DB,
	currentSeverity string,
	aggregationKey string,
	fingerprint string,
	cloudAccountID string,
	startsAt time.Time,
) (*EscalationResult, error) {

	result := &EscalationResult{
		NewSeverity:  currentSeverity,
		WasEscalated: false,
	}

	// If already HIGH, don't escalate further (HIGH is max severity)
	if currentSeverity == "HIGH" {
		return result, nil
	}

	// Get threshold configuration for this aggregation key
	threshold := getThresholdForKey(aggregationKey)
	if threshold == nil {
		return result, nil // No threshold defined
	}

	// Calculate hours since current event
	hoursSinceCurrent := time.Since(startsAt).Hours()

	// Check against thresholds (both critical and high escalate to HIGH)
	if threshold.CriticalThresholdHours != nil &&
		hoursSinceCurrent >= *threshold.CriticalThresholdHours {
		result.NewSeverity = "HIGH"
		result.WasEscalated = true
		result.Reason = "duration_threshold"
		result.Metadata = map[string]interface{}{
			"threshold_hours": *threshold.CriticalThresholdHours,
			"actual_hours":    hoursSinceCurrent,
			"aggregation_key": aggregationKey,
			"threshold_level": "critical",
		}
		return result, nil
	}

	if threshold.HighThresholdHours != nil &&
		hoursSinceCurrent >= *threshold.HighThresholdHours {
		result.NewSeverity = "HIGH"
		result.WasEscalated = true
		result.Reason = "duration_threshold"
		result.Metadata = map[string]interface{}{
			"threshold_hours": *threshold.HighThresholdHours,
			"actual_hours":    hoursSinceCurrent,
			"aggregation_key": aggregationKey,
			"threshold_level": "high",
		}
		return result, nil
	}

	// Not yet met threshold, check first occurrence
	var firstEventStartsAt time.Time
	query := `
		SELECT e.starts_at
		FROM events e
		WHERE e.fingerprint = $1
		  AND e.cloud_account_id = $2
		ORDER BY e.starts_at ASC, e.created_at ASC
		LIMIT 1
	`

	err := db.GetContext(ctx, &firstEventStartsAt, query, fingerprint, cloudAccountID)
	if err != nil {
		// No first event found, use current
		return result, nil
	}

	// Calculate hours since first occurrence
	hoursSinceFirst := time.Since(firstEventStartsAt).Hours()

	// Re-check thresholds against first occurrence
	if threshold.CriticalThresholdHours != nil &&
		hoursSinceFirst >= *threshold.CriticalThresholdHours {
		result.NewSeverity = "HIGH"
		result.WasEscalated = true
		result.Reason = "duration_since_first"
		result.Metadata = map[string]interface{}{
			"threshold_hours": *threshold.CriticalThresholdHours,
			"actual_hours":    hoursSinceFirst,
			"first_event_at":  firstEventStartsAt,
			"aggregation_key": aggregationKey,
			"threshold_level": "critical",
		}
		return result, nil
	}

	if threshold.HighThresholdHours != nil &&
		hoursSinceFirst >= *threshold.HighThresholdHours {
		result.NewSeverity = "HIGH"
		result.WasEscalated = true
		result.Reason = "duration_since_first"
		result.Metadata = map[string]interface{}{
			"threshold_hours": *threshold.HighThresholdHours,
			"actual_hours":    hoursSinceFirst,
			"first_event_at":  firstEventStartsAt,
			"aggregation_key": aggregationKey,
			"threshold_level": "high",
		}
		return result, nil
	}

	return result, nil
}
