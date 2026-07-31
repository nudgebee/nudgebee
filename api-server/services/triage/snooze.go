package triage

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"

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

	// Batch-insert history records (same pattern as bulk_operations.go)
	oldValue, _ := json.Marshal(map[string]string{"nb_status": NBStatusSnoozed})
	newValue, _ := json.Marshal(map[string]string{"nb_status": NBStatusOpen})
	oldStr, newStr := string(oldValue), string(newValue)

	const batchSize = 100
	for i := 0; i < len(unsnoozed); i += batchSize {
		end := i + batchSize
		if end > len(unsnoozed) {
			end = len(unsnoozed)
		}
		batch := unsnoozed[i:end]

		var sb strings.Builder
		sb.Grow(150 + len(batch)*60)
		sb.WriteString(`INSERT INTO event_history (id, event_id, cloud_account_id, tenant_id, change_type, old_value, new_value, change_reason) VALUES `)
		args := make([]interface{}, 0, len(batch)*8)
		for j, evt := range batch {
			if j > 0 {
				sb.WriteString(", ")
			}
			base := j * 8
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8)
			args = append(args, uuid.New().String(), evt.ID, evt.CloudAccountID, evt.TenantID,
				"status", oldStr, newStr, "snooze_expired")
		}

		if _, err := db.Exec(sb.String(), args...); err != nil {
			ctx.GetLogger().Warn("snooze_expiry: failed to batch-insert history",
				"error", err, "batch_size", len(batch))
		}
	}

	return nil
}
