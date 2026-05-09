package account

import (
	"net/http"
	"net/http/httptest"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestStoreEventForAccount(t *testing.T) {
	// Set up a mock server to act as the services-server/hasura endpoint
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/hasura/event", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	server := httptest.NewServer(router)
	defer server.Close()

	// Override the configuration directly to point to the mock server for this test
	originalUrl := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = server.URL
	defer func() { config.Config.ServiceEndpoint = originalUrl }()

	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreEvents(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.NoError(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreEventRulesForAccount(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreEventRules(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestGetEventsInternal(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, _, err := getEventsInternal(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListEventRequest{})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}
func TestStoreMultipleEvents_DuplicateFiltering(t *testing.T) {
	// 1. Setup: DB, context, and mock server for InvestigateEvent
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	dbms, err := common.GetDatabaseManager(common.Metastore)
	assert.NoError(t, err)
	assert.NotNil(t, dbms)

	// This will track how many times InvestigateEvent is called
	investigateCallCount := 0
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/hasura/event", func(c *gin.Context) {
		investigateCallCount++
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	server := httptest.NewServer(router)
	defer server.Close()

	originalUrl := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = server.URL
	defer func() { config.Config.ServiceEndpoint = originalUrl }()

	// Mock data
	testAccountID := os.Getenv("TEST_ACCOUNT")
	testTenantID := os.Getenv("TEST_TENANT")
	originatingAccount := providers.Account{AccountNumber: "864186153326", AccountName: "test-account"}
	testEvent := providers.Event{
		EventId:     "test-duplicate-event-fingerprint-123",
		EventName:   "TestEvent",
		Title:       "Test Duplicate Event",
		Date:        time.Now(),
		EventSource: "AWS_CloudWatch_Alarm",
		EventStatus: providers.EventStatusFiring,
	}

	// Cleanup function to delete the test event from the DB
	cleanup := func() {
		_, err := dbms.Exec("DELETE FROM events WHERE fingerprint = $1 AND cloud_account_id = $2", testEvent.EventId, testAccountID)
		if err != nil {
			t.Logf("Failed to clean up test event: %v", err)
		}
	}
	defer cleanup()
	cleanup() // Clean before test starts as well

	// 2. First call: Process the event for the first time
	err = storeMultipleEventsInDB(ctx, dbms, originatingAccount, testAccountID, []providers.Event{testEvent})
	assert.NoError(t, err)
	assert.Equal(t, 1, investigateCallCount, "InvestigateEvent should be called once for the new event")

	// 3. Simulate the event being active in the DB
	// In a real scenario, InvestigateEvent would do this. We'll do it manually for the test.
	_, err = dbms.Exec(`
    INSERT INTO events (
        id, created_at, updated_at, tenant, cloud_account_id, fingerprint,
        finding_id, status, aggregation_key, source, priority, title, finding_type,
        subject_name, cluster, evidences
    )
    VALUES (gen_random_uuid(), NOW(), NOW(), $1, $2, $3, $3, 'FIRING', $4, $5, 'HIGH', $6, 'issue', 'test-subject', 'test-cluster', '{}'::jsonb)
`, testTenantID, testAccountID, testEvent.EventId, testEvent.EventName, testEvent.EventSource, testEvent.Title)
	assert.NoError(t, err, "Failed to insert mock active event into DB")

	// 4. Second call: Process the same event again
	investigateCallCount = 0 // Reset counter
	err = storeMultipleEventsInDB(ctx, dbms, originatingAccount, testAccountID, []providers.Event{testEvent})
	assert.NoError(t, err)

	// 5. Verification: Ensure the event was filtered and not processed again
	assert.Equal(t, 0, investigateCallCount, "InvestigateEvent should NOT be called for an existing active event")

	// Final cleanup
	cleanup()
}

func TestStoreMultipleEvents_ResolvedUpdatesExisting(t *testing.T) {
	// Setup: DB, context, and mock server
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	dbms, err := common.GetDatabaseManager(common.Metastore)
	assert.NoError(t, err)
	assert.NotNil(t, dbms)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/hasura/event", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	server := httptest.NewServer(router)
	defer server.Close()

	originalUrl := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = server.URL
	defer func() { config.Config.ServiceEndpoint = originalUrl }()

	testAccountID := os.Getenv("TEST_ACCOUNT")
	testTenantID := os.Getenv("TEST_TENANT")
	originatingAccount := providers.Account{AccountNumber: "864186153326", AccountName: "test-account"}
	fingerprint := "test-resolve-event-fingerprint-456"
	var insertedEventID string

	// Cleanup — deferred and also run upfront to clear any previous test remnants.
	// event_history cleanup is scoped by event_id (captured after INSERT) to avoid
	// deleting unrelated history rows for the same account.
	cleanupEvents := func() {
		_, _ = dbms.Exec("DELETE FROM events WHERE fingerprint = $1 AND cloud_account_id = $2", fingerprint, testAccountID)
	}
	cleanupHistory := func() {
		if insertedEventID != "" {
			_, _ = dbms.Exec("DELETE FROM event_history WHERE event_id = $1", insertedEventID)
		}
	}
	defer func() {
		cleanupHistory()
		cleanupEvents()
	}()
	cleanupEvents()

	// 1. Insert a FIRING event directly into DB, capturing its ID for scoped cleanup
	err = dbms.QueryAndScan(&insertedEventID,
		`INSERT INTO events (
			id, created_at, updated_at, tenant, cloud_account_id, fingerprint,
			finding_id, status, aggregation_key, source, priority, title, finding_type,
			subject_name, cluster, evidences
		)
		VALUES (gen_random_uuid(), NOW(), NOW(), $1, $2, $3,
			$3 || '-1234567890', 'FIRING', 'TestAlarm', 'AWS_CloudWatch_Alarm', 'HIGH',
			'Test Alarm (OK -> ALARM)', 'issue', 'test-resource', 'test-cluster', '{}'::jsonb)
		RETURNING id::text`,
		testTenantID, testAccountID, fingerprint)
	assert.NoError(t, err, "Failed to insert FIRING event")

	// 2. Send a RESOLVED event with the same fingerprint but different finding_id
	resolvedEvent := providers.Event{
		EventId:     fingerprint,
		EventName:   "TestAlarm",
		Title:       "Test Alarm (ALARM -> OK)",
		Description: "Threshold Crossed: value dropped below threshold",
		Date:        time.Now(),
		EventSource: "AWS_CloudWatch_Alarm",
		EventStatus: providers.EventStatusResolved,
		Labels: map[string]string{
			"aws_event_status": "RESOLVED",
		},
	}

	err = storeMultipleEventsInDB(ctx, dbms, originatingAccount, testAccountID, []providers.Event{resolvedEvent})
	assert.NoError(t, err)

	// 3. Verify the existing event was updated to RESOLVED (no new row created)
	type eventRow struct {
		Status   string `db:"status"`
		Priority string `db:"priority"`
	}
	var rows []eventRow
	err = dbms.QueryAndScan(&rows,
		`SELECT status, priority FROM events WHERE fingerprint = $1 AND cloud_account_id = $2`,
		fingerprint, testAccountID)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(rows), "Should still be exactly 1 event row, not 2")
	assert.Equal(t, "RESOLVED", rows[0].Status, "Event should be updated to RESOLVED")
	assert.Equal(t, "INFO", rows[0].Priority, "Priority should be downgraded to INFO")

	// 4. Verify event_history was created with resolution metadata, scoped by event_id
	type historyRow struct {
		ChangeType   string `db:"change_type"`
		ChangeReason string `db:"change_reason"`
	}
	var historyRows []historyRow
	err = dbms.QueryAndScan(&historyRows,
		`SELECT change_type, change_reason FROM event_history
		 WHERE event_id = $1 AND change_reason = 'cloud_alarm_resolved'`,
		insertedEventID)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(historyRows), "Should have exactly 1 event_history entry for this event")
	assert.Equal(t, "status", historyRows[0].ChangeType)
}

func TestStoreEventRulesGCloud(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreEventRules(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}
