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
