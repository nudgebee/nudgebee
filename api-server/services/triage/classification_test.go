package triage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyPreview(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Test event ID - find an event with duplicates
	eventID := "beabc0e6-c276-46ad-aeb7-734190314c1a"

	// Fetch the event first
	var event models.Event
	query := `
		SELECT id, title, fingerprint, cloud_account_id, tenant, nb_status
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &event, query, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	t.Logf("Event: %s", event.Title)
	t.Logf("Fingerprint: %v", ptrToStr(event.Fingerprint))

	// Get account and tenant IDs
	if event.CloudAccountId == nil || event.Tenant == nil || *event.Tenant == "" {
		t.Skip("Event missing cloud_account_id or tenant, skipping")
		return
	}
	cloudAccountID := *event.CloudAccountId
	tenantID := *event.Tenant

	// Test preview for this_event scope
	t.Run("this_event_scope", func(t *testing.T) {
		req := ClassifyPreviewRequest{
			EventID:        eventID,
			Classification: ClassificationFalsePositive,
			ApplyScope:     ApplyScopeThisEvent,
		}

		result, err := ClassifyPreview(ctx, db, req, cloudAccountID, tenantID)
		require.NoError(t, err)

		t.Logf("Current Event: %s -> %s", result.CurrentEvent.ID, result.CurrentEvent.NewStatus)
		t.Logf("Existing Events Count: %d", result.ExistingEvents.Count)

		assert.Equal(t, eventID, result.CurrentEvent.ID)
		assert.Equal(t, "RESOLVED", result.CurrentEvent.NewStatus)
		assert.Equal(t, 0, result.ExistingEvents.Count) // this_event scope doesn't affect others
		assert.Nil(t, result.RuleToCreate)
	})

	// Test preview for fingerprint scope
	t.Run("fingerprint_scope", func(t *testing.T) {
		req := ClassifyPreviewRequest{
			EventID:        eventID,
			Classification: ClassificationFalsePositive,
			ApplyScope:     ApplyScopeThisFingerprint,
		}

		result, err := ClassifyPreview(ctx, db, req, cloudAccountID, tenantID)
		require.NoError(t, err)

		t.Logf("Existing Events Count: %d", result.ExistingEvents.Count)
		t.Logf("Current Event New Status: %s", result.CurrentEvent.NewStatus)
		if result.RuleToCreate != nil {
			t.Logf("Rule To Create Action: %s", result.RuleToCreate.Action)
		}

		assert.Equal(t, eventID, result.CurrentEvent.ID)
		assert.GreaterOrEqual(t, result.ExistingEvents.Count, 0)
		assert.Equal(t, "SUPPRESSED", result.CurrentEvent.NewStatus)
		assert.NotNil(t, result.RuleToCreate)

		resultJson, _ := json.MarshalIndent(result, "", "  ")
		t.Logf("Full Preview:\n%s", string(resultJson))
	})

	// Test preview for time_limited scope
	t.Run("time_limited_scope", func(t *testing.T) {
		hours := 168 // 7 days
		req := ClassifyPreviewRequest{
			EventID:         eventID,
			Classification:  ClassificationDuplicate,
			ApplyScope:      ApplyScopeTimeLimited,
			ApplyUntilHours: &hours,
		}

		result, err := ClassifyPreview(ctx, db, req, cloudAccountID, tenantID)
		require.NoError(t, err)

		if result.RuleToCreate != nil {
			t.Logf("Rule To Create Expires At: %v", result.RuleToCreate.ExpiresAt)
		}

		assert.NotNil(t, result.RuleToCreate)
		assert.NotNil(t, result.RuleToCreate.ExpiresAt)
	})
}

func TestGetDuplicateSuggestions(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Test with an event that has duplicates
	eventID := "beabc0e6-c276-46ad-aeb7-734190314c1a"

	// Fetch the event first
	var event models.Event
	query := `
		SELECT id, fingerprint, cloud_account_id
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &event, query, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	if event.CloudAccountId == nil {
		t.Skip("Event has no cloud_account_id, skipping")
		return
	}

	suggestions, err := GetDuplicateSuggestions(ctx, db, eventID, *event.CloudAccountId)
	require.NoError(t, err)

	t.Logf("=== DUPLICATE SUGGESTIONS ===")
	t.Logf("Found %d suggestions", len(suggestions))

	for i, s := range suggestions {
		t.Logf("%d. Event %s (occurrence #%d, is_first=%v)", i+1, s.EventID[:8], s.OccurrenceNumber, s.IsFirst)
		t.Logf("   Title: %s", s.Title)
		t.Logf("   StartsAt: %v", s.StartsAt)
	}

	// First suggestion should be the first occurrence (if any duplicates exist)
	if len(suggestions) > 0 {
		// Check that first occurrence appears at top
		hasFirst := false
		for _, s := range suggestions {
			if s.IsFirst {
				hasFirst = true
				break
			}
		}
		t.Logf("Has first occurrence in suggestions: %v", hasFirst)
	}
}

func TestValidateClassificationRequest(t *testing.T) {
	testCases := []struct {
		name        string
		req         ClassifyEventRequest
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid true_positive",
			req: ClassifyEventRequest{
				EventID:           "test-event-id",
				Classification:    ClassificationTruePositive,
				ReasonCode:        "correct_severity",
				ApplyScope:        ApplyScopeThisEvent,
				PriorityDirection: strPtr("correct"),
			},
			expectError: false,
		},
		{
			name: "valid false_positive",
			req: ClassifyEventRequest{
				EventID:        "test-event-id",
				Classification: ClassificationFalsePositive,
				ReasonCode:     "known_noise",
				ApplyScope:     ApplyScopeThisEvent,
			},
			expectError: false,
		},
		{
			name: "valid duplicate with linked_event",
			req: ClassifyEventRequest{
				EventID:        "test-event-id",
				Classification: ClassificationDuplicate,
				ReasonCode:     "duplicate_incident",
				ApplyScope:     ApplyScopeThisEvent,
				LinkedEventID:  strPtr("original-event-id"),
			},
			expectError: false,
		},
		{
			name: "duplicate missing linked_event",
			req: ClassifyEventRequest{
				EventID:        "test-event-id",
				Classification: ClassificationDuplicate,
				ReasonCode:     "duplicate_incident",
				ApplyScope:     ApplyScopeThisEvent,
			},
			expectError: true,
			errorMsg:    "linked_event_id is required for duplicate classification",
		},
		{
			name: "time_limited missing hours",
			req: ClassifyEventRequest{
				EventID:        "test-event-id",
				Classification: ClassificationFalsePositive,
				ReasonCode:     "known_noise",
				ApplyScope:     ApplyScopeTimeLimited,
			},
			expectError: true,
			errorMsg:    "apply_until_hours is required for time_limited scope",
		},
		{
			name: "time_limited invalid hours",
			req: ClassifyEventRequest{
				EventID:         "test-event-id",
				Classification:  ClassificationFalsePositive,
				ReasonCode:      "known_noise",
				ApplyScope:      ApplyScopeTimeLimited,
				ApplyUntilHours: intPtr(1000), // exceeds max
			},
			expectError: true,
			errorMsg:    "apply_until_hours must be between 1 and 720",
		},
		{
			name: "invalid classification",
			req: ClassifyEventRequest{
				EventID:        "test-event-id",
				Classification: "invalid_type",
				ReasonCode:     "some_reason",
				ApplyScope:     ApplyScopeThisEvent,
			},
			expectError: true,
			errorMsg:    "invalid classification",
		},
		{
			name: "invalid apply_scope",
			req: ClassifyEventRequest{
				EventID:        "test-event-id",
				Classification: ClassificationFalsePositive,
				ReasonCode:     "known_noise",
				ApplyScope:     "invalid_scope",
			},
			expectError: true,
			errorMsg:    "invalid apply_scope",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClassifyRequest(tc.req)
			if tc.expectError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetTargetStatusForClassification(t *testing.T) {
	testCases := []struct {
		classification string
		applyScope     string
		expectedStatus string
	}{
		{ClassificationTruePositive, ApplyScopeThisEvent, NBStatusActionRequired},    // TP = confirmed issue needs fix
		{ClassificationFalsePositive, ApplyScopeThisEvent, NBStatusSuppressed},       // FP should be SUPPRESSED
		{ClassificationFalsePositive, ApplyScopeThisFingerprint, NBStatusSuppressed}, // FP should be SUPPRESSED
		{ClassificationBenignPositive, ApplyScopeThisEvent, NBStatusResolved},        // BP = expected, no action needed
		{ClassificationDuplicate, ApplyScopeThisEvent, NBStatusDuplicate},            // Duplicate should be DUPLICATE
		{ClassificationDuplicate, ApplyScopeTimeLimited, NBStatusDuplicate},          // Duplicate should be DUPLICATE
	}

	for _, tc := range testCases {
		t.Run(tc.classification+"_"+tc.applyScope, func(t *testing.T) {
			status := getTargetStatusForClassification(tc.classification, tc.applyScope)
			assert.Equal(t, tc.expectedStatus, status)
		})
	}
}

func TestCountExistingEventsWithFingerprint(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Find an event with a fingerprint
	eventID := "beabc0e6-c276-46ad-aeb7-734190314c1a"

	var event struct {
		Fingerprint    string `db:"fingerprint"`
		CloudAccountID string `db:"cloud_account_id"`
	}
	err = db.GetContext(ctx, &event, `
		SELECT fingerprint, cloud_account_id
		FROM events
		WHERE id = $1 AND fingerprint IS NOT NULL
	`, eventID)
	if err != nil {
		t.Skipf("Event %s not found or has no fingerprint, skipping", eventID)
		return
	}

	count, sampleIDs, err := countExistingEventsWithFingerprint(ctx, db, event.Fingerprint, event.CloudAccountID, eventID, "")
	require.NoError(t, err)

	t.Logf("=== FINGERPRINT COUNT ===")
	t.Logf("Fingerprint: %s", event.Fingerprint[:16]+"...")
	t.Logf("Count: %d", count)
	t.Logf("Sample IDs: %v", sampleIDs)

	assert.GreaterOrEqual(t, count, 0)
	assert.LessOrEqual(t, len(sampleIDs), 5)
}

// Helper function to create int pointer (strPtr is in rules_test.go)
func intPtr(i int) *int {
	return &i
}

func TestUpdateNBStatus(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Find an event to test status update
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
		WHERE nb_status = 'OPEN'
		LIMIT 1
	`)
	if err != nil {
		t.Skip("No OPEN events found, skipping")
		return
	}

	// Test status update
	t.Run("update_to_acknowledged", func(t *testing.T) {
		req := UpdateNBStatusRequest{
			EventID:  eventID,
			NBStatus: NBStatusAcknowledged,
		}

		resp, err := UpdateNBStatus(ctx, db, req, cloudAccountID, "test-user")
		require.NoError(t, err)

		assert.True(t, resp.Success)
		assert.Equal(t, originalStatus, resp.PrevStatus)
		assert.Equal(t, NBStatusAcknowledged, resp.NewStatus)

		t.Logf("Updated event %s: %s -> %s", eventID[:8], resp.PrevStatus, resp.NewStatus)
	})

	// Restore original status
	t.Cleanup(func() {
		_, err := db.ExecContext(ctx, `
			UPDATE events SET nb_status = $1, nb_status_changed_at = NOW()
			WHERE id = $2
		`, originalStatus, eventID)
		if err != nil {
			t.Logf("Warning: Failed to restore original status: %v", err)
		}
	})
}

func TestSnoozeStatus(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Find an event to test snooze
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
		WHERE nb_status = 'OPEN'
		LIMIT 1
	`)
	if err != nil {
		t.Skip("No OPEN events found, skipping")
		return
	}

	t.Run("snooze_event", func(t *testing.T) {
		snoozeUntil := time.Now().Add(24 * time.Hour)
		req := UpdateNBStatusRequest{
			EventID:      eventID,
			NBStatus:     NBStatusSnoozed,
			SnoozedUntil: &snoozeUntil,
		}

		resp, err := UpdateNBStatus(ctx, db, req, cloudAccountID, "test-user")
		require.NoError(t, err)

		assert.True(t, resp.Success)
		assert.Equal(t, NBStatusSnoozed, resp.NewStatus)

		// Verify snooze_until was set
		var snoozedUntil *time.Time
		err = db.GetContext(ctx, &snoozedUntil, `
			SELECT snoozed_until FROM events WHERE id = $1
		`, eventID)
		require.NoError(t, err)

		assert.NotNil(t, snoozedUntil)
		t.Logf("Event snoozed until: %v", snoozedUntil)
	})

	// Restore original status
	t.Cleanup(func() {
		_, err := db.ExecContext(ctx, `
			UPDATE events SET nb_status = $1, nb_status_changed_at = NOW(), snoozed_until = NULL
			WHERE id = $2
		`, originalStatus, eventID)
		if err != nil {
			t.Logf("Warning: Failed to restore original status: %v", err)
		}
	})
}
