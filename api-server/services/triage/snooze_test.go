package triage

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessExpiredSnoozes(t *testing.T) {
	ctx := context.Background()

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")
	db := dbManager.Db

	// Find an OPEN event to use as test subject
	var eventID string
	var cloudAccountID string
	var originalStatus string

	err = db.GetContext(ctx, &struct {
		ID             *string `db:"id"`
		CloudAccountID *string `db:"cloud_account_id"`
		NBStatus       *string `db:"nb_status"`
	}{&eventID, &cloudAccountID, &originalStatus}, `
		SELECT id, cloud_account_id, nb_status
		FROM events
		WHERE nb_status = 'OPEN' AND cloud_account_id IS NOT NULL AND tenant IS NOT NULL
		LIMIT 1
	`)
	if err != nil {
		t.Skip("No OPEN events found, skipping")
		return
	}

	// Restore original state after test
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `
			UPDATE events SET nb_status = $1, nb_status_changed_at = NOW(), snoozed_until = NULL
			WHERE id = $2
		`, originalStatus, eventID)
	})

	t.Run("expired_snooze_transitions_to_open", func(t *testing.T) {
		// Set event to SNOOZED with an already-expired snoozed_until
		expiredTime := time.Now().Add(-1 * time.Hour)
		_, err := db.ExecContext(ctx, `
			UPDATE events SET nb_status = 'SNOOZED', snoozed_until = $1, nb_status_changed_at = NOW()
			WHERE id = $2
		`, expiredTime, eventID)
		require.NoError(t, err)

		// Run the processor
		reqCtx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
		err = ProcessExpiredSnoozes(reqCtx)
		require.NoError(t, err)

		// Verify event was transitioned back to OPEN
		var newStatus string
		var snoozedUntil *time.Time
		err = db.GetContext(ctx, &struct {
			NBStatus     *string     `db:"nb_status"`
			SnoozedUntil **time.Time `db:"snoozed_until"`
		}{&newStatus, &snoozedUntil}, `
			SELECT nb_status, snoozed_until FROM events WHERE id = $1
		`, eventID)
		require.NoError(t, err)

		assert.Equal(t, NBStatusOpen, newStatus)
		assert.Nil(t, snoozedUntil)
		t.Logf("Event %s transitioned from SNOOZED -> OPEN after snooze expired", eventID[:8])
	})

	t.Run("future_snooze_remains_snoozed", func(t *testing.T) {
		// Set event to SNOOZED with a future snoozed_until
		futureTime := time.Now().Add(24 * time.Hour)
		_, err := db.ExecContext(ctx, `
			UPDATE events SET nb_status = 'SNOOZED', snoozed_until = $1, nb_status_changed_at = NOW()
			WHERE id = $2
		`, futureTime, eventID)
		require.NoError(t, err)

		// Run the processor
		reqCtx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
		err = ProcessExpiredSnoozes(reqCtx)
		require.NoError(t, err)

		// Verify event remains SNOOZED
		var status string
		err = db.GetContext(ctx, &status, `
			SELECT nb_status FROM events WHERE id = $1
		`, eventID)
		require.NoError(t, err)

		assert.Equal(t, NBStatusSnoozed, status)
		t.Logf("Event %s correctly remained SNOOZED (snooze not yet expired)", eventID[:8])
	})
}
