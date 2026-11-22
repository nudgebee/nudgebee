package api

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"regexp"
	"testing"
	"time"
)

func TestAbandonedAnalysisDoesNotConfirmUnpersistedOrRCAFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      events.EventAnalysisType
		updateErr error
	}{
		{"failed write", events.AnalysisTypeInvestigation, errors.New("database unavailable")},
		{"RCA belongs to separate pipeline", events.AnalysisTypeRCA, nil},
		{"RCA attempt belongs to separate pipeline", events.AnalysisTypeRCAAttempt, nil},
		{"failed RCA write", events.AnalysisTypeRCA, errors.New("database unavailable")},
		{"failed RCA attempt write", events.AnalysisTypeRCAAttempt, errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			var update *sqlmock.ExpectedExec
			if isRCAAnalysisType(tc.kind) {
				query := "UPDATE event_log_analysis SET status=$4,status_reason=$5,updated_at=NOW() WHERE id=$1 AND event_id=$2 AND cloud_account_id=$3 AND analysis_type='" + string(tc.kind) + "' AND status='IN_PROGRESS'"
				update = mock.ExpectExec(regexp.QuoteMeta(query)).WithArgs("abandoned-row", "event", "account", string(events.AnalysisStatusFailed), "recovery abandoned: analysis stuck for over 24 hours")
			} else {
				update = mock.ExpectExec(`UPDATE event_log_analysis SET status=`).WithArgs("abandoned-row", string(events.AnalysisStatusFailed), "recovery abandoned: analysis stuck for over 24 hours")
			}
			if tc.updateErr != nil {
				update.WillReturnError(tc.updateErr)
			} else {
				update.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			response, terminal, err := failAbandonedEventAnalysis(security.NewRequestContextForSuperAdmin(), events.InProgressAnalysis{ID: "abandoned-row", AnalysisType: tc.kind, EventId: "event", AccountId: "account"}, &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, abandonReasonStuck)
			require.Equal(t, tc.updateErr, err)
			require.False(t, terminal)
			require.Empty(t, response.Status)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPendingAnalysisRetriesConfirmationAfterFinalStagePersisted(t *testing.T) {
	// The registry, not an IN_PROGRESS database row, schedules both sync ticks.
	originalProvider := config.Config.CacheProvider
	config.Config.CacheProvider = "memory"
	t.Cleanup(func() { config.Config.CacheProvider = originalProvider })
	const eventID = "pending-terminal-retry-test"
	const accountID = "account"
	require.NoError(t, common.RegisterPendingToken(context.Background(), eventID, "workflow-token"))
	t.Cleanup(func() { _, _ = common.DrainPendingTokens(context.Background(), eventID) })
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	manager := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	ctx := security.NewRequestContextForSuperAdmin()
	publications := 0
	publish := func(ctx context.Context, account, event string, response EventAnalysisResponse, status string) {
		publications++
		require.Equal(t, accountID, account)
		require.Equal(t, eventID, event)
		require.Equal(t, string(events.AnalysisStatusCompleted), status)
		require.Equal(t, "consolidated findings", response.DetailedResponse)
		tokens, err := common.DrainPendingTokens(ctx, event)
		require.NoError(t, err)
		require.Equal(t, []string{"workflow-token"}, tokens)
	}
	expectAccount := func() {
		mock.ExpectQuery("SELECT cloud_account_id FROM events").WithArgs(eventID).
			WillReturnRows(sqlmock.NewRows([]string{"cloud_account_id"}).AddRow(accountID))
	}
	expectAccount()
	mock.ExpectQuery("SELECT id, fingerprint, aggregation_key, created_at FROM events").WithArgs(eventID, accountID).
		WillReturnError(errors.New("transient confirmation read failure"))
	syncPendingEventAnalysisTerminals(ctx, manager, publish)
	require.Zero(t, publications)
	require.NoError(t, mock.ExpectationsWereMet())

	expectAccount()
	mock.ExpectQuery("SELECT id, fingerprint, aggregation_key, created_at FROM events").WithArgs(eventID, accountID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}).AddRow(eventID, "fingerprint", "group", time.Now()))
	// All four stages were persisted before the first tick. Confirmation and
	// hydration on the next tick must succeed without rerunning any agent.
	for _, kind := range []events.EventAnalysisType{
		events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeLog, events.AnalysisTypeDetailedResponse,
		events.AnalysisTypeLog, events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeDetailedResponse,
	} {
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping").WithArgs(eventID, kind).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
		mock.ExpectQuery("SELECT id, analysis, status.*FROM event_log_analysis WHERE event_fingerprint").WithArgs("fingerprint", accountID, "group", kind).
			WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
				AddRow("row", "{}", string(events.AnalysisStatusCompleted), eventID, "consolidated findings", "", time.Now()))
	}
	syncPendingEventAnalysisTerminals(ctx, manager, publish)
	require.Equal(t, 1, publications)
	require.NoError(t, mock.ExpectationsWereMet())
	// Draining removes retry eligibility; another tick emits nothing.
	syncPendingEventAnalysisTerminals(ctx, manager, publish)
	require.Equal(t, 1, publications)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAbandonReason(t *testing.T) {
	require.Equal(t, abandonReasonStopped, abandonReason(core.ConversationStatusTerminated))
	for _, s := range []core.ConversationStatus{core.ConversationStatusFailed, core.ConversationStatusKilled, ""} {
		require.Equal(t, abandonReasonStuck, abandonReason(s), s)
	}
}
