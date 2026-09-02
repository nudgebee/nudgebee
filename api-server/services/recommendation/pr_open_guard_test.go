package recommendation

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two SQL predicates below are what stop a resolution row becoming a zombie:
// counted as a recommendation's open pull request while no code path can ever
// release it again. On dev this pinned llm-server and relay-server to pull
// requests that had already merged, and neither raised another for a month.

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
	require.NoError(t, err)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	// Restore the handle rather than leaving a closed one behind: the manager is
	// package-global, so a later test that does not swap it first would otherwise
	// get "sql: database is closed".
	previous := manager.Db
	t.Cleanup(func() {
		_ = db.Close()
		manager.Db = previous
	})
	manager.Db = sqlx.NewDb(db, "postgres")
	return mock
}

// TestFindOpenPRResolutionOnlyConsidersInProgressRows pins the status filter.
//
// The reconciler in account/adapter/pr_lifecycle.go selects on
// status = 'InProgress' everywhere. If this query is willing to consider a row
// the reconciler is not, a row that lands in the gap blocks its recommendation
// forever: nothing advances it to a terminal state, and the guard keeps handing
// it back instead of raising a pull request.
func TestFindOpenPRResolutionOnlyConsidersInProgressRows(t *testing.T) {
	mock := mockMetastore(t)

	mock.ExpectQuery("FROM recommendation_resolution").
		WithArgs("rec-1", string(models.RecommendationResolutionTypePullRequest),
			string(models.RecommendationResolutionStatusInProgress)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := findOpenPRResolution("rec-1")

	require.NoError(t, err)
	assert.Nil(t, got, "no open row means no open PR")
	assert.NoError(t, mock.ExpectationsWereMet(),
		"the open-PR guard must narrow to InProgress, matching the reconciler")
}

type stubAdapterContext struct{}

func (stubAdapterContext) GetContext() context.Context                   { return context.Background() }
func (stubAdapterContext) GetLogger() *slog.Logger                       { return slog.Default() }
func (stubAdapterContext) GetSecurityContext() *security.SecurityContext { return nil }

// TestRecordValueRefreshLeavesTerminalPullRequestsAlone pins the terminal guard.
//
// A merge can land while the code agent is still running. Without the guard this
// write stamps pr_lifecycle_state='created' over 'merged' while leaving
// status='Success' — and that pair is unrecoverable, because the reconciler only
// looks at InProgress rows. Every other writer of pr_lifecycle_state guards on
// the state it replaces; this one has to as well.
func TestRecordValueRefreshLeavesTerminalPullRequestsAlone(t *testing.T) {
	mock := mockMetastore(t)

	mock.ExpectExec("pr_lifecycle_state NOT IN").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := recordValueRefresh(stubAdapterContext{},
		&models.RecommendationResolution{Id: "res-1", TypeReferenceId: "https://github.com/o/r/pull/1"},
		map[string]any{"cpu": "100m"}, "cpu 80m → 100m")

	require.NoError(t, err, "a PR that went terminal mid-refresh is not an error")
	assert.NoError(t, mock.ExpectationsWereMet(),
		"the refresh write must refuse to resurrect a merged/closed pull request")
}
