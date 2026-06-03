package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/internal/testenv"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoring(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Test event ID
	eventID := testenv.RequireEnv(t, "TEST_EVENT_ID")["TEST_EVENT_ID"]

	// Fetch the event from database
	var event models.Event
	query := `
		SELECT id, title, priority, finding_type, source,
		       subject_name, subject_namespace, subject_owner, subject_owner_kind,
		       cloud_account_id, cluster, evidences, labels
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &event, query, eventID)
	require.NoError(t, err, "Failed to fetch event")

	t.Logf("Event: %s", event.Title)
	t.Logf("Priority: %v", event.Priority)
	t.Logf("Subject Owner: %v", event.SubjectOwner)

	// Compute the score
	result, err := ComputeScore(ctx, db, &event)
	require.NoError(t, err, "Failed to compute score")

	// Print the results
	t.Logf("=== SCORING RESULT ===")
	t.Logf("Score: %d", result.Score)
	t.Logf("Priority: %s", result.Priority)
	t.Logf("Confidence: %.2f", result.Confidence)

	resultJson, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("Full Result:\n%s", string(resultJson))

	// Print factors
	factorsJSON, _ := json.MarshalIndent(result.Factors, "", "  ")
	t.Logf("Factors:\n%s", string(factorsJSON))

	// Basic assertions
	assert.GreaterOrEqual(t, result.Score, 0)
	assert.LessOrEqual(t, result.Score, 100)
	assert.Contains(t, []string{"P0", "P1", "P2", "P3"}, result.Priority)
	assert.GreaterOrEqual(t, result.Confidence, 0.0)
	assert.LessOrEqual(t, result.Confidence, 1.0)

	// Verify factors are populated
	assert.NotNil(t, result.Factors["base_severity"])
	assert.NotNil(t, result.Factors["env_multiplier"])
}

func TestScoringMultipleEvents(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	// Test with multiple event types
	ids := testenv.RequireEnv(t, "TEST_WORKFLOW_EVENT_ID", "TEST_EVENT_ID")
	testCases := []struct {
		name    string
		eventID string
	}{
		{"workflow-server event", ids["TEST_WORKFLOW_EVENT_ID"]},
		{"service_map event", ids["TEST_EVENT_ID"]},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var event models.Event
			query := `
				SELECT id, title, priority, finding_type, source,
				       subject_name, subject_namespace, subject_owner, subject_owner_kind,
				       cloud_account_id, cluster, evidences, labels
				FROM events
				WHERE id = $1
			`
			err := db.GetContext(ctx, &event, query, tc.eventID)
			if err != nil {
				t.Skipf("Event %s not found, skipping", tc.eventID)
				return
			}

			result, err := ComputeScore(ctx, db, &event)
			require.NoError(t, err)

			t.Logf("Event: %s", event.Title)
			t.Logf("Subject Owner: %v", ptrToStr(event.SubjectOwner))
			t.Logf("Score: %d, Priority: %s", result.Score, result.Priority)

			assert.GreaterOrEqual(t, result.Score, 0)
			assert.LessOrEqual(t, result.Score, 100)
		})
	}
}

// Helper function to safely convert pointer to string
func ptrToStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestComputeAndUpdateScore tests the full flow of computing and updating a score
func TestComputeAndUpdateScore(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	eventID := testenv.RequireEnv(t, "TEST_WORKFLOW_EVENT_ID")["TEST_WORKFLOW_EVENT_ID"]

	// Fetch the event
	var event models.Event
	query := `
		SELECT id, title, priority, finding_type, source,
		       subject_name, subject_namespace, subject_owner, subject_owner_kind,
		       cloud_account_id, cluster, evidences, labels
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &event, query, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	// Compute the score
	result, err := ComputeScore(ctx, db, &event)
	require.NoError(t, err)

	// Print detailed breakdown
	fmt.Println("=== SCORE BREAKDOWN ===")
	fmt.Printf("Event ID: %s\n", event.Id)
	fmt.Printf("Title: %s\n", event.Title)
	fmt.Printf("Priority (original): %v\n", ptrToStr(event.Priority))
	fmt.Printf("Subject Owner: %v\n", ptrToStr(event.SubjectOwner))
	fmt.Printf("Namespace: %v\n", ptrToStr(event.SubjectNamespace))
	fmt.Println()
	fmt.Printf("Base Severity: %v\n", result.Factors["base_severity"])
	fmt.Printf("Env Multiplier: %v\n", result.Factors["env_multiplier"])
	fmt.Printf("Raw Score: %v\n", result.Factors["raw_score"])
	fmt.Printf("Duplicate Penalty: %v\n", result.Factors["duplicate_penalty"])
	fmt.Printf("Correlation Adj: %v\n", result.Factors["correlation_adjustment"])
	fmt.Printf("Finding Type Adj: %v\n", result.Factors["finding_type_adjustment"])
	fmt.Printf("Evidence Bonus: %v\n", result.Factors["evidence_bonus"])
	fmt.Printf("Total Adjustments: %v\n", result.Factors["total_adjustments"])
	fmt.Println()
	fmt.Printf("FINAL SCORE: %d\n", result.Score)
	fmt.Printf("PRIORITY: %s\n", result.Priority)
	fmt.Printf("CONFIDENCE: %.2f\n", result.Confidence)

	// Optionally update the event (commented out to avoid modifying data)
	// err = UpdateEventScore(ctx, db, eventID, result)
	// require.NoError(t, err)
}

// TestProcessEventIntegration tests the full triage flow including
// duplicate detection, correlation detection, and scoring
func TestProcessEventIntegration(t *testing.T) {
	ctx := context.Background()

	// Get database connection
	testenv.RequireMetastore(t)
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	require.NoError(t, err, "Failed to get database manager")

	db := dbManager.Db

	eventID := testenv.RequireEnv(t, "TEST_EVENT_ID")["TEST_EVENT_ID"]

	// Fetch the full event from database
	var event models.Event
	query := `
		SELECT id, title, priority, finding_type, source, fingerprint,
		       subject_name, subject_namespace, subject_owner, subject_owner_kind,
		       cloud_account_id, cluster, evidences, labels, tenant, starts_at
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &event, query, eventID)
	if err != nil {
		t.Skipf("Event %s not found, skipping", eventID)
		return
	}

	t.Logf("=== EVENT INFO ===")
	t.Logf("Event ID: %s", event.Id)
	t.Logf("Title: %s", event.Title)
	t.Logf("Source Priority: %v", ptrToStr(event.Priority))
	t.Logf("Subject Owner: %v", ptrToStr(event.SubjectOwner))
	t.Logf("Namespace: %v", ptrToStr(event.SubjectNamespace))
	t.Logf("Fingerprint: %v", ptrToStr(event.Fingerprint))

	// Run the full triage process
	t.Logf("\n=== RUNNING TRIAGE ===")
	err = ProcessEvent(ctx, db, &event)
	require.NoError(t, err, "ProcessEvent failed")

	// Verify the score was saved
	var savedEvent struct {
		ComputedScore    *int     `db:"computed_score"`
		ComputedPriority *string  `db:"computed_priority"`
		ScoreFactors     *string  `db:"score_factors"`
		ScoreConfidence  *float64 `db:"score_confidence"`
	}

	verifyQuery := `
		SELECT computed_score, computed_priority, score_factors, score_confidence
		FROM events
		WHERE id = $1
	`
	err = db.GetContext(ctx, &savedEvent, verifyQuery, eventID)
	require.NoError(t, err, "Failed to fetch saved score")

	t.Logf("\n=== SAVED RESULTS ===")
	if savedEvent.ComputedScore != nil {
		t.Logf("Computed Score: %d", *savedEvent.ComputedScore)
	} else {
		t.Logf("Computed Score: <nil>")
	}
	if savedEvent.ComputedPriority != nil {
		t.Logf("Computed Priority: %s", *savedEvent.ComputedPriority)
	} else {
		t.Logf("Computed Priority: <nil>")
	}
	if savedEvent.ScoreConfidence != nil {
		t.Logf("Score Confidence: %.2f", *savedEvent.ScoreConfidence)
	}
	if savedEvent.ScoreFactors != nil {
		t.Logf("Score Factors: %s", *savedEvent.ScoreFactors)
	}

	// Assertions
	assert.NotNil(t, savedEvent.ComputedScore, "computed_score should be set")
	assert.NotNil(t, savedEvent.ComputedPriority, "computed_priority should be set")

	if savedEvent.ComputedScore != nil {
		assert.GreaterOrEqual(t, *savedEvent.ComputedScore, 0)
		assert.LessOrEqual(t, *savedEvent.ComputedScore, 100)
	}

	if savedEvent.ComputedPriority != nil {
		assert.Contains(t, []string{"P0", "P1", "P2", "P3"}, *savedEvent.ComputedPriority)
	}

	// Check duplicate record
	var duplicateRecord struct {
		OccurrenceNumber int    `db:"occurrence_number"`
		FirstEventID     string `db:"first_event_id"`
	}
	dupQuery := `
		SELECT occurrence_number, first_event_id
		FROM event_duplicates
		WHERE event_id = $1
	`
	err = db.GetContext(ctx, &duplicateRecord, dupQuery, eventID)
	if err == nil {
		t.Logf("\n=== DUPLICATE INFO ===")
		t.Logf("Occurrence Number: %d", duplicateRecord.OccurrenceNumber)
		t.Logf("First Event ID: %s", duplicateRecord.FirstEventID)
	}

	// Check correlation records
	var correlationCount int
	corrQuery := `SELECT COUNT(*) FROM event_correlations WHERE event_id = $1`
	err = db.GetContext(ctx, &correlationCount, corrQuery, eventID)
	if err == nil {
		t.Logf("\n=== CORRELATION INFO ===")
		t.Logf("Correlated Events: %d", correlationCount)
	}
}
