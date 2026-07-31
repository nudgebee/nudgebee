package storage

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"nudgebee/runbook/internal/model"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for #34943. A pull request that is open on the provider but
// whose resolution row has been flipped to Failed — by the stale sweep, or by a
// transient provider auth error — used to fall through the optimizer's "is
// anything in flight?" check. The run then executed, was handed back the
// resolution it had already recorded, and aborted on a duplicate key before
// writing any status. The task stayed in Scheduled, which reads as "waiting its
// turn", so it held that workload back from every later run, permanently.

func newMockDao(t *testing.T) (*OptimizerDao, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	return &OptimizerDao{db: sqlx.NewDb(mockDB, "sqlmock")}, mock, func() { _ = mockDB.Close() }
}

// nearNow matches a time argument within tolerance of an expected instant.
type nearNow struct {
	expected  time.Time
	tolerance time.Duration
}

func (n nearNow) Match(v driver.Value) bool {
	got, ok := v.(time.Time)
	if !ok {
		return false
	}
	delta := got.Sub(n.expected)
	if delta < 0 {
		delta = -delta
	}
	return delta <= n.tolerance
}

// TestGetActiveResolutions_TreatsForeignOpenPRAsActive covers a pull request the
// auto optimize does not own: status Failed, but a real PR URL and a lifecycle
// the api-server's open-PR guard still counts as open. The optimizer must return
// it, so the run skips rather than executing into the duplicate that caused the
// wedge (#34943). We do not rewrite a pull request a person raised by hand, so
// for these there is nothing to do but wait.
func TestGetActiveResolutions_TreatsForeignOpenPRAsActive(t *testing.T) {
	dao, mock, cleanup := newMockDao(t)
	defer cleanup()

	recID := uuid.New()
	prURL := "https://github.com/acme/infra/pull/860"

	rows := sqlmock.NewRows([]string{
		"id", "recommendation_id", "type", "data", "status", "type_reference_id",
		"resolver_type", "resolver_id", "created_at", "updated_at", "status_message",
		"pr_iteration_count", "pr_lifecycle_state", "last_pr_check_at",
	}).AddRow(
		uuid.New(), recID, "PullRequest", nil, "Failed", prURL,
		"User", uuid.New(), time.Now().UTC(), nil, "stale sweep",
		5, "stale", nil,
	)

	// Match the WHERE-clause arm specifically, not the column list — the query
	// must filter on pr_lifecycle_state, not just select it. Keying off status
	// alone is the divergence from the api-server guard that caused the bug.
	mock.ExpectQuery(regexp.QuoteMeta(`pr_lifecycle_state <> ALL`)).WillReturnRows(rows)

	got, err := dao.GetActiveResolutionsForRecommendations(context.Background(), []uuid.UUID{recID})
	require.NoError(t, err)

	require.Len(t, got[recID], 1, "an open PR whose row says Failed must still count as active")
	assert.Equal(t, prURL, got[recID][0].TypeReferenceID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetActiveResolutions_QueryCarriesGuardVocabulary pins the arguments so
// neither arm can be quietly narrowed: the lifecycle arm back to a status-only
// check (#34943), or the resolver exclusion away entirely (#34959).
func TestGetActiveResolutions_QueryCarriesGuardVocabulary(t *testing.T) {
	dao, mock, cleanup := newMockDao(t)
	defer cleanup()

	recID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(`pr_lifecycle_state <> ALL`)).
		WithArgs(
			sqlmock.AnyArg(), // recommendation ids
			string(model.RecommendationResolutionStatusInProgress),
			string(model.RecommendationResolutionTypePullRequest),
			sqlmock.AnyArg(), // terminal lifecycle states
			model.RecommendationResolutionResolverTypeAutoOptimize,
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "recommendation_id", "type", "data", "status", "type_reference_id",
			"resolver_type", "resolver_id", "created_at", "updated_at", "status_message",
			"pr_iteration_count", "pr_lifecycle_state", "last_pr_check_at",
		}))

	_, err := dao.GetActiveResolutionsForRecommendations(context.Background(), []uuid.UUID{recID})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, []string{"merged", "closed", "unresolvable"}, model.PRLifecycleTerminalStates,
		"these mirror the api-server open-PR guard; changing one side without the other reopens #34943")
}

// TestGetActiveResolutions_ExcludesOwnOpenPRAndInFlightCreation pins the two
// halves of the #34959 change in the SQL itself:
//
//   - an open pull request the auto optimize raised does NOT block, so the run
//     can recompute values and let the api-server guard decide whether to refresh
//     that pull request in place;
//   - a resolution still InProgress with no pull request URL DOES block, because
//     creation is genuinely in flight and there is nothing to compare yet.
func TestGetActiveResolutions_ExcludesOwnOpenPRAndInFlightCreation(t *testing.T) {
	dao, mock, cleanup := newMockDao(t)
	defer cleanup()

	recID := uuid.New()

	// sqlmock cannot evaluate SQL, so the contract is asserted on the predicate
	// itself: both clauses must be present, in this order. Dropping either one
	// silently reverts a behaviour — the first would re-block a refreshable pull
	// request, the second would let a run act while creation is still in flight.
	bothClauses := regexp.QuoteMeta(`type_reference_id NOT LIKE 'http%'`) +
		`(?s).*` + regexp.QuoteMeta(`resolver_type <> `)

	mock.ExpectQuery(bothClauses).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "recommendation_id", "type", "data", "status", "type_reference_id",
			"resolver_type", "resolver_id", "created_at", "updated_at", "status_message",
			"pr_iteration_count", "pr_lifecycle_state", "last_pr_check_at",
		}))

	_, err := dao.GetActiveResolutionsForRecommendations(context.Background(), []uuid.UUID{recID})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetActiveTasks_BoundsScheduledByAge covers the second half: a task left in
// Scheduled by a run that died must stop blocking its recommendation once it is
// older than the staleness window.
func TestGetActiveTasks_BoundsScheduledByAge(t *testing.T) {
	dao, mock, cleanup := newMockDao(t)
	defer cleanup()

	recID := uuid.New()
	expectedCutoff := time.Now().UTC().Add(-model.ScheduledTaskStaleAfter)

	mock.ExpectQuery(regexp.QuoteMeta(`created_at >`)).
		WithArgs(
			sqlmock.AnyArg(), // recommendation ids
			string(model.AutopilotTaskStatusScheduled),
			nearNow{expected: expectedCutoff, tolerance: time.Minute},
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "auto_pilot_id", "tenant_id", "account_id", "recommendation_id", "status",
			"created_at", "updated_at", "meta", "attributes", "resource_filter", "scheduled_time",
			"name", "reason", "error", "command", "task_id", "skipped_by",
		}))

	got, err := dao.GetActiveTasksForRecommendations(context.Background(), []uuid.UUID{recID})
	require.NoError(t, err)
	assert.Empty(t, got, "a Scheduled task older than the window must not block the recommendation")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestMarkAutoOptimizeTaskTerminal_OnlyTouchesStatusColumns guards the fallback
// that runs when the normal save fails. It must not write task_id — that column
// is what the failing save was tripping over — and must not clobber an outcome
// that did land.
func TestMarkAutoOptimizeTaskTerminal_OnlyTouchesStatusColumns(t *testing.T) {
	dao, mock, cleanup := newMockDao(t)
	defer cleanup()

	taskID := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE auto_pilot_task`)).
		WithArgs(
			string(model.AutopilotTaskStatusFailed),
			"boom",
			sqlmock.AnyArg(), // updated_at
			taskID,
			string(model.AutopilotTaskStatusScheduled), // only overwrite a still-Scheduled row
			string(model.AutopilotTaskStatusFailed),    // error column set only for a failure
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, dao.MarkAutoOptimizeTaskTerminal(context.Background(), taskID,
		string(model.AutopilotTaskStatusFailed), "boom"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestMarkAutoOptimizeTaskTerminal_PreservesASuccessfulOutcome is the case that
// matters most in practice: the save this backstops trips most easily on a task
// that succeeded and produced a resolution id. Recording Failed there would
// report a success as a failure.
func TestMarkAutoOptimizeTaskTerminal_PreservesASuccessfulOutcome(t *testing.T) {
	dao, mock, cleanup := newMockDao(t)
	defer cleanup()

	taskID := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE auto_pilot_task`)).
		WithArgs(
			string(model.AutopilotTaskStatusComplete), // the outcome the run reached
			"could not record outcome",
			sqlmock.AnyArg(),
			taskID,
			string(model.AutopilotTaskStatusScheduled),
			string(model.AutopilotTaskStatusFailed),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, dao.MarkAutoOptimizeTaskTerminal(context.Background(), taskID,
		string(model.AutopilotTaskStatusComplete), "could not record outcome"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestMarkAutoOptimizeTaskTerminal_CoercesNonTerminalStatus — leaving the row
// Scheduled is the one outcome this function exists to prevent.
func TestMarkAutoOptimizeTaskTerminal_CoercesNonTerminalStatus(t *testing.T) {
	for _, given := range []string{"", string(model.AutopilotTaskStatusScheduled)} {
		dao, mock, cleanup := newMockDao(t)

		taskID := uuid.New()
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE auto_pilot_task`)).
			WithArgs(
				string(model.AutopilotTaskStatusFailed), // coerced
				"no outcome",
				sqlmock.AnyArg(),
				taskID,
				string(model.AutopilotTaskStatusScheduled),
				string(model.AutopilotTaskStatusFailed),
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, dao.MarkAutoOptimizeTaskTerminal(context.Background(), taskID, given, "no outcome"))
		require.NoError(t, mock.ExpectationsWereMet(), "given status %q", given)
		cleanup()
	}
}
