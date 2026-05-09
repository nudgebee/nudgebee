package event

import (
	"errors"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"time"
)

// ProcessSeverityEscalation is no longer auto-registered as a standalone processor.
// Severity recalculation now runs inside processTriage (before ComputeScore) to guarantee
// the triage scorer sees the escalated priority. This function is kept for backward
// compatibility but should not be registered via RegisterEventProcessor.
func ProcessSeverityEscalation(ctx *security.RequestContext, eventData map[string]any) error {

	// Extract required fields
	eventID, ok := eventData["id"].(string)
	if !ok {
		return nil // Skip if no ID
	}

	currentSeverity, ok := eventData["priority"].(string)
	if !ok || currentSeverity == "" {
		currentSeverity = "MEDIUM" // Default
	}

	aggregationKey, ok := eventData["aggregation_key"].(string)
	if !ok || aggregationKey == "" {
		return nil // No aggregation key, skip
	}

	fingerprint, ok := eventData["fingerprint"].(string)
	if !ok || fingerprint == "" {
		return nil // No fingerprint, skip
	}

	cloudAccountID, ok := eventData["cloud_account_id"].(string)
	if !ok {
		return nil
	}

	var startsAt time.Time
	if eventData["starts_at"] != nil {
		if t, ok := eventData["starts_at"].(time.Time); ok {
			startsAt = t
		} else if s, ok := eventData["starts_at"].(string); ok {
			var err error
			startsAt, err = time.Parse(time.RFC3339, s)
			if err != nil {
				return nil
			}
		}
	}

	if startsAt.IsZero() {
		startsAt = time.Now()
	}

	// Get database connection
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Calculate if escalation needed
	result, err := RecalculateSeverity(
		ctx.GetContext(),
		dbm.Db,
		currentSeverity,
		aggregationKey,
		fingerprint,
		cloudAccountID,
		startsAt,
	)
	if err != nil {
		ctx.GetLogger().Error("Failed to recalculate severity", "error", err, "event_id", eventID)
		return err
	}

	// If escalated, insert history first, then update the event
	if result.WasEscalated {
		// Extract tenant_id for history record
		tenantID, ok := eventData["tenant"].(string)
		if !ok {
			ctx.GetLogger().Error("Missing tenant in event data for history", "event_id", eventID)
			return errors.New("missing tenant ID for event history")
		}

		// Insert history with rich escalation metadata before UPDATE
		// This captures the full context (thresholds, durations, etc.)
		err = InsertEventHistory(
			ctx.GetContext(),
			dbm.Db,
			eventID,
			tenantID,
			cloudAccountID,
			"priority",
			currentSeverity,    // old value
			result.NewSeverity, // new value
			result.Reason,      // "duration_threshold" or "duration_since_first"
			result.Metadata,    // threshold_hours, actual_hours, aggregation_key, etc.
		)
		if err != nil {
			ctx.GetLogger().Error("Failed to insert event history", "error", err, "event_id", eventID)
			// Don't fail - history is not critical, continue with UPDATE
		}

		// Then UPDATE the event (triggers webhook, but deterministic ID prevents duplicate)
		updateQuery := `
			UPDATE events
			SET priority = $1, updated_at = now()
			WHERE id = $2
		`

		_, err = dbm.Db.ExecContext(ctx.GetContext(), updateQuery, result.NewSeverity, eventID)
		if err != nil {
			ctx.GetLogger().Error("Failed to update event priority", "error", err, "event_id", eventID)
			return err
		}

		ctx.GetLogger().Info("Event severity escalated",
			"event_id", eventID,
			"old_severity", currentSeverity,
			"new_severity", result.NewSeverity,
			"reason", result.Reason,
		)
	}

	return nil
}
