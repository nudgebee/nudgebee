package recommendation

import (
	"database/sql/driver"
	"errors"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recommendationResolutionColumns mirrors the column list selected by
// ListRecommendationResolutions.
var recommendationResolutionColumns = []string{
	"id", "created_at", "updated_at", "recommendation_id", "type", "data", "status",
	"type_reference_id", "resolver_type", "resolver_id", "status_message",
	"pr_iteration_count", "pr_lifecycle_state", "last_pr_check_at",
}

// TestListRecommendationResolutions is a regression test for two defects in
// ListRecommendationResolutions: the result set was leaked on the scan-error
// early-return path, and a mid-iteration driver error was swallowed (the loop
// never checked rows.Err()). The latter is especially important here because
// this query backs the duplicate-resolution safety check — a silently
// truncated result could miss an in-progress row and allow a duplicate
// resolution to be created.
func TestListRecommendationResolutions(t *testing.T) {
	manager := &database.DatabaseManager{}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})

	newMock := func(t *testing.T) sqlmock.Sqlmock {
		t.Helper()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		manager.Db = sqlx.NewDb(db, "postgres")
		return mock
	}

	call := func() ([]models.RecommendationResolution, error) {
		return ListRecommendationResolutions(nil, "rec-1", "PullRequest", models.RecommendationResolutionResolverType(""), "resolver-1")
	}

	t.Run("closes the result set when row scanning fails", func(t *testing.T) {
		mock := newMock(t)
		rows := sqlmock.NewRows([]string{"unexpected_column"}).AddRow("x")
		mock.ExpectQuery("FROM recommendation_resolution").
			WillReturnRows(rows).
			RowsWillBeClosed()

		_, err := call()

		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("surfaces a mid-iteration error instead of truncating", func(t *testing.T) {
		mock := newMock(t)
		boom := errors.New("connection reset mid-stream")
		rows := sqlmock.NewRows(recommendationResolutionColumns).
			AddRow(make([]driver.Value, len(recommendationResolutionColumns))...).
			RowError(0, boom)
		mock.ExpectQuery("FROM recommendation_resolution").
			WillReturnRows(rows).
			RowsWillBeClosed()

		resolutions, err := call()

		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.Empty(t, resolutions)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
