package queue

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/event/lifecycle"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emittedCall struct {
	phase lifecycle.Phase
	event map[string]any
	extra map[string]any
}

// withStubbedSeams swaps the loadEventMapFn / emitLifecycleFn seams for the
// duration of the test and restores them afterward. The emit stub records the
// (phase, event, extra) it was called with.
func withStubbedSeams(t *testing.T, loadErr error) *[]emittedCall {
	t.Helper()
	origLoad := loadEventMapFn
	origEmit := emitLifecycleFn
	t.Cleanup(func() {
		loadEventMapFn = origLoad
		emitLifecycleFn = origEmit
	})

	calls := &[]emittedCall{}
	loadEventMapFn = func(_ context.Context, eventID string, logger *slog.Logger) (*security.RequestContext, map[string]any, error) {
		if loadErr != nil {
			return nil, nil, loadErr
		}
		return nil, map[string]any{"id": eventID}, nil
	}
	emitLifecycleFn = func(ctx *security.RequestContext, phase lifecycle.Phase, event map[string]any, extra map[string]any) {
		*calls = append(*calls, emittedCall{phase: phase, event: event, extra: extra})
	}
	return calls
}

func TestProcessInvestigationCompleted_MalformedJSON(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted(context.Background(), []byte("{not json"))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "no phase must be emitted on malformed payload")
}

func TestProcessInvestigationCompleted_MissingIDs(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted(context.Background(), []byte(`{"status":"COMPLETED"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls)
}

func TestProcessInvestigationCompleted_NonTerminalSkipped(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"e-nonterminal","account_id":"a1","status":"IN_PROGRESS"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "non-terminal status must not emit a phase")
}

func TestProcessInvestigationCompleted_CompletedEmitsCompletedPhase(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"7d3f1c52-0a6b-4a1e-9c57-1f6f0f3b2a01","account_id":"a1","status":"COMPLETED","summary":"the summary","investigation":"the rca","log_summary":"ls","log_analysis":"la"}`))
	require.NoError(t, err)
	require.Len(t, *calls, 1)
	c := (*calls)[0]
	assert.Equal(t, lifecycle.PhaseInvestigationCompleted, c.phase)
	assert.Equal(t, "COMPLETED", c.extra["analysis_status"])
	assert.Equal(t, "the summary", c.extra["analysis_summary"])
	assert.Equal(t, "the rca", c.extra["analysis_investigation"])
	assert.Equal(t, "ls", c.extra["analysis_log_summary"])
	assert.Equal(t, "la", c.extra["analysis_log_analysis"])
}

func TestProcessInvestigationCompleted_FailedEmitsFailedPhase(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"7d3f1c52-0a6b-4a1e-9c57-1f6f0f3b2a02","account_id":"a1","status":"FAILED","status_reason":"boom"}`))
	require.NoError(t, err)
	require.Len(t, *calls, 1, "FAILED must still emit (investigation.failed) so the event isn't dropped")
	c := (*calls)[0]
	assert.Equal(t, lifecycle.PhaseInvestigationFailed, c.phase)
	assert.Equal(t, "FAILED", c.extra["analysis_status"])
	assert.Equal(t, "boom", c.extra["analysis_status_reason"])
	assert.Empty(t, c.extra["analysis_summary"])
}

func TestProcessInvestigationCompleted_DedupRunsOnce(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	payload := []byte(`{"event_id":"7d3f1c52-0a6b-4a1e-9c57-1f6f0f3b2a03","account_id":"a1","status":"COMPLETED","summary":"s"}`)
	require.NoError(t, processInvestigationCompleted(context.Background(), payload))
	require.NoError(t, processInvestigationCompleted(context.Background(), payload))
	assert.Len(t, *calls, 1, "duplicate completion envelopes must emit at most once per event")
}

func TestProcessInvestigationCompleted_TokenBearingDropped(t *testing.T) {
	calls := withStubbedSeams(t, nil)
	// A token-bearing envelope belongs to runbook-server's consumer; api-server
	// must drop it so it doesn't emit a phase for events it never deferred (a
	// runbook investigation bypasses api-server's lifecycle).
	err := processInvestigationCompleted(context.Background(), []byte(`{"task_token":"dGhpcy1pcy1hLXRva2Vu","event_id":"e-token","account_id":"a1","status":"COMPLETED","summary":"s"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "token-bearing envelopes must not emit a lifecycle phase")
}

func TestProcessInvestigationCompleted_NotFoundAcks(t *testing.T) {
	// A missing event is permanent — the message must be ACK'd (no error
	// returned) so it isn't requeued forever, and no phase is emitted.
	calls := withStubbedSeams(t, sql.ErrNoRows)
	err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"7d3f1c52-0a6b-4a1e-9c57-1f6f0f3b2a04","account_id":"a1","status":"COMPLETED"}`))
	assert.NoError(t, err)
	assert.Empty(t, *calls, "no phase must be emitted when the event does not exist")
}

func TestProcessInvestigationCompleted_TransientLoadErrorRequeues(t *testing.T) {
	// A transient DB / network error must be returned so RabbitMQ requeues and
	// the lifecycle phase still fires once the event loads — not silently dropped.
	calls := withStubbedSeams(t, assert.AnError)
	err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"7d3f1c52-0a6b-4a1e-9c57-1f6f0f3b2a05","account_id":"a1","status":"COMPLETED"}`))
	assert.Error(t, err, "a transient load error must requeue (return an error)")
	assert.Empty(t, *calls, "no phase must be emitted when the event can't be loaded")
}

func TestProcessInvestigationCompleted_NonUUIDEventIDAcks(t *testing.T) {
	// events.id is a uuid column: Postgres rejects any other value on every
	// delivery, so the lookup can never succeed. The envelope must be ACK'd
	// before the query is issued.
	for _, eventID := range []string{"not-a-uuid", "12345", "   "} {
		t.Run(eventID, func(t *testing.T) {
			calls := withStubbedSeams(t, nil)
			loads := 0
			loadEventMapFn = func(context.Context, string, *slog.Logger) (*security.RequestContext, map[string]any, error) {
				loads++
				return nil, nil, errors.New("pq: invalid input syntax for type uuid")
			}
			err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"`+eventID+`","account_id":"a1","status":"COMPLETED"}`))
			assert.NoError(t, err, "a non-UUID event_id must be ACK'd, not requeued")
			assert.Zero(t, loads, "the event must not be queried")
			assert.Empty(t, *calls)
		})
	}
}

// mockMetastore backs event.GetEvent with sqlmock so the real loadEventMap runs
// with no live database.
func mockMetastore(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	// Registering a hook also drops the cached manager, so a nil hook leaves
	// nothing of this mock behind for the next test.
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, nil)
		_ = db.Close()
	})
	return mock
}

func TestProcessInvestigationCompleted_UnconvertibleEventAcks(t *testing.T) {
	// The row loads but cannot be converted to the event map (NaN has no JSON
	// form). The conversion reads nothing from the database, so a retry fails
	// the same way: the message must be ACK'd, not requeued forever.
	calls := withStubbedSeams(t, nil)
	loadEventMapFn = loadEventMap
	mock := mockMetastore(t)
	const eventID = "7d3f1c52-0a6b-4a1e-9c57-1f6f0f3b2a06"
	mock.ExpectQuery("FROM events WHERE id").WithArgs(eventID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "score_confidence"}).AddRow(eventID, "t", []byte("NaN")))

	err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"`+eventID+`","account_id":"a1","status":"COMPLETED"}`))
	assert.NoError(t, err, "an event that cannot be converted must be ACK'd, not requeued")
	assert.Empty(t, *calls)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessInvestigationCompleted_DatabaseErrorRequeues(t *testing.T) {
	// The same path with the query itself failing: that can succeed on a later
	// delivery, so the real loadEventMap must still surface it for a requeue.
	calls := withStubbedSeams(t, nil)
	loadEventMapFn = loadEventMap
	mock := mockMetastore(t)
	dbErr := errors.New("dial tcp: connect: connection refused")
	mock.ExpectQuery("FROM events WHERE id").WillReturnError(dbErr)

	err := processInvestigationCompleted(context.Background(), []byte(`{"event_id":"7d3f1c52-0a6b-4a1e-9c57-1f6f0f3b2a07","account_id":"a1","status":"COMPLETED"}`))
	assert.ErrorIs(t, err, dbErr, "a database error must requeue (return an error)")
	assert.Empty(t, *calls)
	require.NoError(t, mock.ExpectationsWereMet())
}
