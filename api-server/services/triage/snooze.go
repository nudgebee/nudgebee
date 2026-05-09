package triage

import (
	"encoding/json"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/google/uuid"
)

// expiredSnoozeEvent holds the returned columns from the bulk unsnooze UPDATE.
type expiredSnoozeEvent struct {
	ID             string `db:"id"`
	CloudAccountID string `db:"cloud_account_id"`
	TenantID       string `db:"tenant"`
}

// ProcessExpiredSnoozes transitions events whose snooze has expired back to OPEN.
// Called by the "Snooze Expiry Check" cron job.
func ProcessExpiredSnoozes(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	db := dbms.Db

	// Bulk-update all expired snoozed events in one statement
	var unsnoozed []expiredSnoozeEvent
	err = db.Select(&unsnoozed, `
		UPDATE events
		SET nb_status = 'OPEN',
		    nb_status_changed_at = NOW(),
		    nb_status_changed_by = NULL,
		    snoozed_until = NULL,
		    updated_at = NOW()
		WHERE nb_status = 'SNOOZED'
		  AND snoozed_until IS NOT NULL
		  AND snoozed_until < NOW()
		RETURNING id, cloud_account_id, tenant
	`)
	if err != nil {
		ctx.GetLogger().Error("snooze_expiry: failed to update expired snoozes", "error", err)
		return err
	}

	if len(unsnoozed) == 0 {
		return nil
	}

	ctx.GetLogger().Info("snooze_expiry: transitioned expired snoozed events to OPEN", "count", len(unsnoozed))

	// Log each status change to event_history
	oldValue, _ := json.Marshal(map[string]string{"nb_status": NBStatusSnoozed})
	newValue, _ := json.Marshal(map[string]string{"nb_status": NBStatusOpen})

	for _, evt := range unsnoozed {
		historyID := uuid.New().String()
		_, histErr := db.Exec(`
			INSERT INTO event_history (
				id, event_id, cloud_account_id, tenant_id,
				change_type, old_value, new_value, change_reason
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8
			)
		`, historyID, evt.ID, evt.CloudAccountID, evt.TenantID,
			"status", string(oldValue), string(newValue), "snooze_expired")
		if histErr != nil {
			ctx.GetLogger().Warn("snooze_expiry: failed to log history", "event_id", evt.ID, "error", histErr)
		}
	}

	return nil
}
