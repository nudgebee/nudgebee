package api

import (
	"context"
	"log/slog"
	"nudgebee/services/event"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"nudgebee/services/triage"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// lots of actions are in different modules so calling them from internal modules like events causes issues like action not found, so this is best place to call any refresh event or processing
// we cannot have all the actions in single package, because of cyclic dependencies, requires some work to sort-out
func TestRefreshEvent(t *testing.T) {
	m := testenv.RequireEnv(t, testenv.Tenant, "TEST_EVENT_ID")
	tenant := m[testenv.Tenant]
	eventId := m["TEST_EVENT_ID"]
	err := event.RefreshInvestigation(security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil), eventId)
	assert.Nil(t, err)
}

func TestProcessEventTriage(t *testing.T) {
	m := testenv.RequireEnv(t, testenv.Tenant, "TEST_EVENT_ID")
	tenant := m[testenv.Tenant]
	eventId := m["TEST_EVENT_ID"]

	// Get the event
	ctx := security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil)
	ev, err := event.GetEvent(ctx, eventId)
	assert.Nil(t, err)
	assert.NotEmpty(t, ev.Id, "Event not found")

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	assert.Nil(t, err)

	// Check for other events in time window before processing
	type EventCount struct {
		Count int `db:"count"`
	}
	var count EventCount
	timeWindowQuery := `
		SELECT COUNT(*) as count
		FROM events
		WHERE cloud_account_id = $1
		  AND fingerprint != $2
		  AND starts_at >= $3
		  AND starts_at <= $4
	`
	startWindow := ev.StartsAt.Add(-10 * time.Minute)
	endWindow := ev.StartsAt.Add(10 * time.Minute)
	err = dbms.Db.Get(&count, timeWindowQuery, *ev.CloudAccountId, *ev.Fingerprint, startWindow, endWindow)
	assert.Nil(t, err)
	t.Logf("Events in 10-minute window (different fingerprint): %d", count.Count)

	// Process triage
	err = triage.ProcessEvent(context.Background(), dbms.Db, &ev)
	assert.Nil(t, err)

	// Get tenant ID for tenant isolation
	tenantID := ctx.GetSecurityContext().GetTenantId()

	// Verify results
	duplicates, err := triage.GetDuplicateChain(context.Background(), dbms.Db, eventId, tenantID)
	assert.Nil(t, err)
	t.Logf("Event triage completed - Duplicate chain length: %d", len(duplicates))

	correlations, err := triage.GetCorrelatedEvents(context.Background(), dbms.Db, eventId, tenantID)
	assert.Nil(t, err)
	t.Logf("Event triage completed - Correlated events: %d", len(correlations))
}

func TestBackfillTriage(t *testing.T) {
	m := testenv.RequireEnv(t, testenv.Tenant, testenv.Account, "TEST_FINGERPRINT")
	tenant := m[testenv.Tenant]
	cloudAccountID := m[testenv.Account]
	fingerprint := m["TEST_FINGERPRINT"]

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	assert.Nil(t, err)

	// Check how many events exist with this fingerprint before backfill
	type EventCount struct {
		Count int `db:"count"`
	}
	var beforeCount EventCount
	countQuery := `
		SELECT COUNT(*) as count
		FROM events
		WHERE cloud_account_id = $1
		  AND fingerprint = $2
	`
	err = dbms.Db.Get(&beforeCount, countQuery, cloudAccountID, fingerprint)
	assert.Nil(t, err)
	t.Logf("Events with fingerprint before backfill: %d", beforeCount.Count)

	// Create security context and get tenant ID for tenant isolation
	secCtx := security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil)
	tenantID := secCtx.GetSecurityContext().GetTenantId()

	// Run backfill
	ctx := context.Background()
	opts := triage.BackfillOptions{
		CloudAccountID: cloudAccountID,
		Fingerprint:    &fingerprint,
		BatchSize:      100,
		DryRun:         false,
	}

	result, err := triage.BackfillTriage(ctx, dbms.Db, opts)
	assert.Nil(t, err)
	assert.NotNil(t, result)

	t.Logf("Backfill completed:")
	t.Logf("  Total events: %d", result.TotalEvents)
	t.Logf("  Duplicates detected: %d", result.DuplicatesDetected)
	t.Logf("  Correlations created: %d", result.CorrelationsCreated)
	t.Logf("  Errors: %d", result.Errors)
	t.Logf("  Duration: %v", result.Duration)

	// Verify that we processed the expected number of events
	assert.Equal(t, beforeCount.Count, result.TotalEvents, "Should process all events with fingerprint")
	assert.Equal(t, 0, result.Errors, "Should have no errors")

	// Pick one event to verify duplicate chain
	var eventID string
	eventQuery := `
		SELECT id
		FROM events
		WHERE cloud_account_id = $1
		  AND fingerprint = $2
		ORDER BY starts_at ASC
		LIMIT 1
	`
	err = dbms.Db.Get(&eventID, eventQuery, cloudAccountID, fingerprint)
	assert.Nil(t, err)

	// Get duplicate chain for the first event
	duplicates, err := triage.GetDuplicateChain(ctx, dbms.Db, eventID, tenantID)
	assert.Nil(t, err)
	t.Logf("Duplicate chain for first event (%s): %d occurrences", eventID, len(duplicates))

	// Verify we have a complete chain
	assert.Greater(t, len(duplicates), 1, "Should have multiple duplicates in chain")

	// Verify occurrence numbers are sequential
	for i, dup := range duplicates {
		expectedOccurrence := i + 1
		assert.Equal(t, expectedOccurrence, dup.OccurrenceNumber,
			"Occurrence %d should be numbered correctly", i)
	}

	// Verify all duplicates have the same first_event_id
	if len(duplicates) > 0 {
		firstEventID := duplicates[0].FirstEventID
		for _, dup := range duplicates {
			assert.Equal(t, firstEventID, dup.FirstEventID,
				"All duplicates should point to same first event")
		}
	}

	// Get historical stats to verify analytics work after backfill
	stats, err := triage.ComputeHistoricalStats(ctx, dbms.Db, fingerprint, cloudAccountID)
	assert.Nil(t, err)
	assert.NotNil(t, stats)
	t.Logf("Historical stats:")
	t.Logf("  Total events: %d", stats.TotalEvents)
	t.Logf("  Firing count: %d", stats.FiringCount)
	t.Logf("  Resolved count: %d", stats.ResolvedCount)
	t.Logf("  Resolution rate: %.2f%%", stats.ResolutionRate*100)
}

func TestDeduplicateCorrelations(t *testing.T) {
	m := testenv.RequireEnv(t, testenv.Tenant, "TEST_ACCOUNT_ID")
	tenant := m[testenv.Tenant]
	testAccountID := m["TEST_ACCOUNT_ID"]

	ctx := security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil)

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	assert.Nil(t, err, "Failed to get database manager")

	// First, check how many duplicate correlations exist
	var beforeStats struct {
		TotalCorrelations     int `db:"total_correlations"`
		UniquePairs           int `db:"unique_pairs"`
		EventsWithDuplicates  int `db:"events_with_duplicates"`
		MaxDuplicatesPerEvent int `db:"max_duplicates_per_event"`
	}

	query := `
		SELECT
			COUNT(*) as total_correlations,
			COUNT(DISTINCT (event_id, related_fingerprint)) as unique_pairs,
			COUNT(DISTINCT event_id) FILTER (WHERE dup_count > 1) as events_with_duplicates,
			MAX(dup_count) as max_duplicates_per_event
		FROM (
			SELECT
				ec.event_id,
				e.fingerprint as related_fingerprint,
				COUNT(*) as dup_count
			FROM event_correlations ec
			JOIN events e ON e.id = ec.related_event_id
			WHERE ec.cloud_account_id = $1
			GROUP BY ec.event_id, e.fingerprint
		) sub
	`
	err = dbms.Db.GetContext(ctx.GetContext(), &beforeStats, query, testAccountID)
	assert.Nil(t, err, "Failed to get before stats")

	t.Logf("Before deduplication:")
	t.Logf("  Total correlations: %d", beforeStats.TotalCorrelations)
	t.Logf("  Unique (event_id, fingerprint) pairs: %d", beforeStats.UniquePairs)
	t.Logf("  Events with duplicate correlations: %d", beforeStats.EventsWithDuplicates)
	t.Logf("  Max duplicates for single event: %d", beforeStats.MaxDuplicatesPerEvent)

	// Run deduplication
	err = triage.DeduplicateCorrelations(ctx.GetContext(), dbms.Db, testAccountID)
	assert.Nil(t, err, "Deduplication failed")

	// Check results after deduplication
	var afterStats struct {
		TotalCorrelations     int `db:"total_correlations"`
		UniquePairs           int `db:"unique_pairs"`
		EventsWithDuplicates  int `db:"events_with_duplicates"`
		MaxDuplicatesPerEvent int `db:"max_duplicates_per_event"`
	}

	err = dbms.Db.GetContext(ctx.GetContext(), &afterStats, query, testAccountID)
	assert.Nil(t, err, "Failed to get after stats")

	t.Logf("After deduplication:")
	t.Logf("  Total correlations: %d", afterStats.TotalCorrelations)
	t.Logf("  Unique (event_id, fingerprint) pairs: %d", afterStats.UniquePairs)
	t.Logf("  Events with duplicate correlations: %d", afterStats.EventsWithDuplicates)
	t.Logf("  Max duplicates for single event: %d", afterStats.MaxDuplicatesPerEvent)
	t.Logf("  Removed: %d correlations", beforeStats.TotalCorrelations-afterStats.TotalCorrelations)

	// Verify no more duplicates exist
	assert.Equal(t, 0, afterStats.EventsWithDuplicates, "Should have no events with duplicate correlations")
	assert.Equal(t, afterStats.TotalCorrelations, afterStats.UniquePairs, "Total correlations should equal unique pairs")
}

func TestReplayWebhookEvent(t *testing.T) {
	tenant := testenv.RequireTenantID(t)
	testenv.RequireMetastore(t)
	ctxt := security.NewRequestContextForTenantAdmin(tenant, nil, nil, nil)
	err := core.ReplayWebhookEvent(ctxt, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Error("Expected error for empty webhook_event_id, got nil")
	}
	expectedError := "integrations: webhook_event_id is required"
	if err.Error() != expectedError {
		t.Errorf("Expected error %q, got %q", expectedError, err.Error())
	}
}
