package notification

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
)

func TestTriggerNotificationForRecommendationResolution_SQLInjection(t *testing.T) {
	// Create mock database
	mockDB, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer func() {
		_ = mockDB.Close()
	}()

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")

	// Register the mock database hook
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlxDB}, nil
	})

	// Setup context and logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

	// Define recommendation with potentially malicious ID
	recID := "rec-123"
	accountID := "acc-123' OR '1'='1" // Malicious input
	recommendation := models.Recommendation{
		Id:             recID,
		CloudAccountId: accountID,
		Category:       "TestCategory",
		RuleName:       "TestRule",
		Status:         models.RecommendationStatusOpen,
		TenantId:       "tenant-1",
	}

	resolution := models.RecommendationResolution{
		ResolverType:  models.RecommendationResolutionResolverTypeUser,
		Type:          models.RecommendationResolutionTypePullRequest,
		Status:        models.RecommendationResolutionStatusSuccess,
		StatusMessage: nil,
	}

	// Expect the parameterized query
	// The key expectation is that the query uses $1 and passes accountID as an argument
	// instead of embedding it directly in the query string.
	// sqlmock expects prepared statements by default for parameterized queries.
	mock.ExpectQuery(`select ca.account_name from cloud_accounts ca where id=\$1`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"account_name"}).AddRow("Test Account"))

	// Execute the function
	err = TriggerNotificationForRecommendationResolution(ctx, recommendation, resolution)

	if err != nil {
		t.Logf("Error from TriggerNotificationForRecommendationResolution: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Expectations were not met: %v", err)
	}
}

// mockMetastore points the metastore at a fresh sqlmock for one test.
//
// The manager is memoised by database.GetDatabaseManager on first resolution, so
// registering a new hook per test would not take effect. Resolve whichever
// manager the package ended up with and swap its handle instead.
func mockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{}, nil
	})
	manager, err := database.GetDatabaseManager(database.Metastore)
	assert.NoError(t, err)

	db, mock, err := sqlmock.New()
	assert.NoError(t, err)

	previous := manager.Db
	t.Cleanup(func() {
		_ = db.Close()
		manager.Db = previous
	})
	manager.Db = sqlx.NewDb(db, "postgres")
	return mock
}

// A resolve path may downgrade priority in the same UPDATE that resolves the
// row (the cloud alarm path sets it to INFO), so ProcessEventResolved must not
// gate on priority — it has to reach the nb_status lookup regardless.
func TestProcessEventResolved_NonHighPriorityReachesSuppressionLookup(t *testing.T) {
	mock := mockMetastore(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

	// SUPPRESSED short-circuits before the publish, so the test needs no broker
	// while still proving the priority gate no longer returns early.
	mock.ExpectQuery(`SELECT nb_status FROM events WHERE id = \$1`).
		WithArgs("event-1").
		WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow("SUPPRESSED"))

	err := ProcessEventResolved(ctx, map[string]any{
		"id":       "event-1",
		"priority": "INFO",
		"source":   "AWS_CloudWatch_Alarm",
		"tenant":   "tenant-1",
	})
	assert.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Expectations were not met: %v", err)
	}
}

// A triage "drop" rule hides an event completely, a stronger action than
// "suppress", so a DROPPED event must stay silent on resolve too.
func TestProcessEventResolved_DroppedEventIsNotNotified(t *testing.T) {
	mock := mockMetastore(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

	mock.ExpectQuery(`SELECT nb_status FROM events WHERE id = \$1`).
		WithArgs("event-1").
		WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow("DROPPED"))

	err := ProcessEventResolved(ctx, map[string]any{
		"id":       "event-1",
		"priority": "HIGH",
		"source":   "AWS_CloudWatch_Alarm",
		"tenant":   "tenant-1",
	})
	// No broker is configured in tests, so reaching the publish returns an
	// error: NoError proves the DROPPED gate returned before publishing.
	assert.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Expectations were not met: %v", err)
	}
}

// capturePublished records the messages bound for notifications-server instead
// of sending them, so a test can read one without a broker.
func capturePublished(t *testing.T) *[]map[string]any {
	t.Helper()
	var sent []map[string]any
	orig := publishNotification
	publishNotification = func(_ *security.RequestContext, message map[string]any) error {
		sent = append(sent, message)
		return nil
	}
	t.Cleanup(func() { publishNotification = orig })
	return &sent
}

// A finding that had cleared and fired again is published as finding_returned,
// carrying the event row the same way a resolve does. ProcessEventResolved runs
// through the same code and must keep its own type.
func TestProcessEventReturnedAndResolved_PublishTheirOwnType(t *testing.T) {
	for _, tc := range []struct {
		wantType string
		process  func(*security.RequestContext, map[string]any) error
	}{
		{wantType: "finding_returned", process: ProcessEventReturned},
		{wantType: "finding_resolved", process: ProcessEventResolved},
	} {
		t.Run(tc.wantType, func(t *testing.T) {
			mock := mockMetastore(t)
			sent := capturePublished(t)

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

			mock.ExpectQuery(`SELECT nb_status FROM events WHERE id = \$1`).
				WithArgs("event-1").
				WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow("OPEN"))

			finding := map[string]any{
				"id":       "event-1",
				"priority": "HIGH",
				"source":   "anomaly",
				"status":   "FIRING",
				"tenant":   "tenant-1",
			}
			assert.NoError(t, tc.process(ctx, finding))

			assert.Equal(t, []map[string]any{{
				"kind":       "notification",
				"type":       tc.wantType,
				"source":     "anomaly",
				"tenant_id":  "tenant-1",
				"parameters": map[string]any{"finding": finding},
			}}, *sent)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("Expectations were not met: %v", err)
			}
		})
	}
}

// A finding triage suppressed or dropped stays silent when it returns, the same
// as when it resolves.
func TestProcessEventReturned_SilencedFindingIsNotNotified(t *testing.T) {
	for _, nbStatus := range []string{"SUPPRESSED", "DROPPED"} {
		t.Run(nbStatus, func(t *testing.T) {
			mock := mockMetastore(t)
			sent := capturePublished(t)

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

			mock.ExpectQuery(`SELECT nb_status FROM events WHERE id = \$1`).
				WithArgs("event-1").
				WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow(nbStatus))

			err := ProcessEventReturned(ctx, map[string]any{
				"id":       "event-1",
				"priority": "HIGH",
				"source":   "anomaly",
				"status":   "FIRING",
				"tenant":   "tenant-1",
			})
			assert.NoError(t, err)

			assert.Empty(t, *sent)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("Expectations were not met: %v", err)
			}
		})
	}
}

// The message is consumed after the upsert. An anomaly that cleared again in
// between is no longer firing, so nothing is announced and nothing is read.
func TestProcessEventReturned_EventThatClearedAgainIsNotNotified(t *testing.T) {
	for _, status := range []string{"RESOLVED", "CLOSED", ""} {
		t.Run("status "+status, func(t *testing.T) {
			mock := mockMetastore(t)
			sent := capturePublished(t)

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

			err := ProcessEventReturned(ctx, map[string]any{
				"id":       "event-1",
				"priority": "HIGH",
				"source":   "anomaly",
				"status":   status,
				"tenant":   "tenant-1",
			})
			assert.NoError(t, err)

			assert.Empty(t, *sent)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("Expectations were not met: %v", err)
			}
		})
	}
}
