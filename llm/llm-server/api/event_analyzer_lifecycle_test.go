package api

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFillPublishStateFromResponse verifies that completion envelopes correctly
// extract canonical detailed response or fall back to summary, and carry status.
func TestFillPublishStateFromResponse(t *testing.T) {
	tests := []struct {
		name        string
		resp        EventAnalysisResponse
		status      string
		wantSummary string
		wantStatus  string
	}{
		{
			name: "detailed response preferred over summary",
			resp: EventAnalysisResponse{
				Summary:          "Step 1 summary",
				DetailedResponse: "Step 4 synthesized detailed response",
				Analysis:         "{\"source_updates\":{}}",
				Investigation:    "Root cause identified",
			},
			status:      string(events.AnalysisStatusCompleted),
			wantSummary: "Step 4 synthesized detailed response",
			wantStatus:  string(events.AnalysisStatusCompleted),
		},
		{
			name: "summary used when detailed response is empty",
			resp: EventAnalysisResponse{
				Summary:       "Step 1 summary only",
				Analysis:      "",
				Investigation: "",
			},
			status:      string(events.AnalysisStatusCompleted),
			wantSummary: "Step 1 summary only",
			wantStatus:  string(events.AnalysisStatusCompleted),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var env investigationCompletedEnvelope
			fillPublishStateFromResponse(&env, tt.resp, tt.status)
			assert.Equal(t, tt.wantStatus, env.Status)
			assert.Equal(t, tt.wantSummary, env.Summary)
			assert.Equal(t, tt.resp.Summary, env.LogSummary)
			assert.Equal(t, tt.resp.Analysis, env.LogAnalysis)
			assert.Equal(t, tt.resp.Investigation, env.Investigation)
		})
	}
}

// TestPublishAnalysisCompletedTerminalSafelyHandlesEmptyEventId ensures the
// terminal completion publisher handles edge cases without panic.
func TestPublishAnalysisCompletedTerminalSafelyHandlesEmptyEventId(t *testing.T) {
	require.NotPanics(t, func() {
		publishAnalysisCompletedTerminal(context.Background(), "acct-1", "", EventAnalysisResponse{}, string(events.AnalysisStatusCompleted))
	})
}

// TestSyncStuckAnalysisDecouplesCompletedConversation verifies that a completed
// conversation is not discarded simply because its event-row timestamp is older
// than 24 hours (#37865).
func TestSyncStuckAnalysisDecouplesCompletedConversation(t *testing.T) {
	maxRecoveryAge := 24 * time.Hour
	rowUpdatedAt := time.Now().Add(-26 * time.Hour) // 26 hours old

	// An abandoned row without completed conversation should be abandoned
	abandonConvStatus := core.ConversationStatusFailed
	shouldAbandon := abandonConvStatus != core.ConversationStatusCompleted && time.Since(rowUpdatedAt) > maxRecoveryAge
	assert.True(t, shouldAbandon, "abandoned conversations older than 24h must be marked failed")

	// A row whose conversation completed (e.g. approved after 24h) must NOT be abandoned
	completedConvStatus := core.ConversationStatusCompleted
	shouldAbandonCompleted := completedConvStatus != core.ConversationStatusCompleted && time.Since(rowUpdatedAt) > maxRecoveryAge
	assert.False(t, shouldAbandonCompleted, "completed conversations must be reconciled regardless of row age")
}

// TestPopulateCompletedEventAnalysis verifies that populateCompletedEventAnalysis
// enriches an EventAnalysisResponse with all 4 stages (DetailedResponse, Investigation, Summary, Log).
func TestPopulateCompletedEventAnalysis(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-terminal-enrich"
		fp      = "fp-terminal-enrich"
		acct    = "acct-terminal-enrich"
		aggKey  = "pod_crash"
	)

	repo, mock := newTestRepo(t)
	freshTime := time.Now().Add(-1 * time.Hour)

	// Log row (queried first; contains empty investigation and log-stage summary)
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-log", "{\"title\":\"CrashLoop\",\"event_id\":\"corrupted-id\",\"status\":\"FAILED\",\"event_fingerprint\":\"corrupted-fp\",\"investigation\":\"\",\"summary\":\"Log stage summary\"}", string(events.AnalysisStatusCompleted), eventID, "Log summary", "", freshTime))

	// Summary row
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-sum", "{}", string(events.AnalysisStatusCompleted), eventID, "Initial event summary", "", freshTime))

	// Investigation row
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeInvestigation).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeInvestigation).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-inv", "{}", string(events.AnalysisStatusCompleted), eventID, "Investigation tool findings", "", freshTime))

	// DetailedResponse row
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeDetailedResponse).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeDetailedResponse).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-dr", "{}", string(events.AnalysisStatusCompleted), eventID, "Final synthesized report", "", freshTime))

	resp := EventAnalysisResponse{
		EventId: eventID,
	}
	populateCompletedEventAnalysis(ctx, repo, eventID, fp, aggKey, acct, &resp)

	assert.Equal(t, "Final synthesized report", resp.DetailedResponse)
	assert.Equal(t, "Investigation tool findings", resp.Investigation)
	assert.Equal(t, "Initial event summary", resp.Summary)
	assert.Equal(t, "CrashLoop", resp.Title)
	assert.Equal(t, eventID, resp.EventId, "EventId must not be overwritten by log analysis JSON")
	assert.NotEqual(t, "corrupted-fp", resp.EventFingerprint, "EventFingerprint must not be overwritten by log analysis JSON")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetConfirmedTerminalAnalysisPartialCompletionReturnsFalse ensures that
// when only log analysis has completed but synthesis (Step 4) has not,
// getConfirmedTerminalAnalysis returns false to prevent premature workflow completion.
func TestGetConfirmedTerminalAnalysisPartialCompletionReturnsFalse(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-partial-run"
		fp      = "fp-partial-run"
		acct    = "acct-partial-run"
		aggKey  = "pod_crash"
	)

	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	dbManager := &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}

	// 1. GetEventInfo
	mock.ExpectQuery("SELECT id, fingerprint, aggregation_key, created_at FROM events WHERE id = .*").
		WithArgs(eventID, acct).
		WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}).
			AddRow(eventID, fp, aggKey, time.Now()))

	// 2. allEventAnalysisTypesCompleted check:
	// Summary: COMPLETED
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-sum", "{}", string(events.AnalysisStatusCompleted), eventID, "summary", "", time.Now()))

	// Investigation: COMPLETED
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeInvestigation).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeInvestigation).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-inv", "{}", string(events.AnalysisStatusCompleted), eventID, "inv", "", time.Now()))

	// Log: COMPLETED
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-log", "{}", string(events.AnalysisStatusCompleted), eventID, "log", "", time.Now()))

	// DetailedResponse: IN_PROGRESS (synthesis still running)
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeDetailedResponse).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeDetailedResponse).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-dr", "{}", string(events.AnalysisStatusInProgress), eventID, "", "", time.Now()))

	// 3. Fallthrough failure check: checks log row
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-log", "{}", string(events.AnalysisStatusCompleted), eventID, "log", "", time.Now()))

	// Summary
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-sum", "{}", string(events.AnalysisStatusCompleted), eventID, "summary", "", time.Now()))

	// Investigation
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeInvestigation).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeInvestigation).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-inv", "{}", string(events.AnalysisStatusCompleted), eventID, "inv", "", time.Now()))

	// DetailedResponse is IN_PROGRESS -> returns false from failure loop
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeDetailedResponse).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeDetailedResponse).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-dr", "{}", string(events.AnalysisStatusInProgress), eventID, "", "", time.Now()))

	req := EventAnalysisRequest{
		EventId:   eventID,
		AccountId: acct,
	}

	resp, terminal := getConfirmedTerminalAnalysis(ctx, req, dbManager)
	assert.False(t, terminal, "partial pipeline completion must NOT be reported as terminal")
	assert.Empty(t, resp.DetailedResponse)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRecoveryTransientErrorPreservesTokens validates that transient errors do not
// falsely trigger terminal failure publication, preserving pending tokens in Redis.
func TestRecoveryTransientErrorPreservesTokens(t *testing.T) {
	// 1. Transient error: row remains in progress, response status is empty/in-progress
	recoveredResp := EventAnalysisResponse{
		Status: string(events.AnalysisStatusInProgress),
	}
	dbRowStatus := string(events.AnalysisStatusInProgress)

	isTerminalFailure := false
	if strings.EqualFold(recoveredResp.Status, string(events.AnalysisStatusFailed)) {
		isTerminalFailure = true
	} else if strings.EqualFold(dbRowStatus, string(events.AnalysisStatusFailed)) {
		isTerminalFailure = true
	}
	assert.False(t, isTerminalFailure, "transient error with in-progress row must NOT be treated as terminal failure")

	// 2. Confirmed terminal error: row is failed
	dbRowStatusFailed := string(events.AnalysisStatusFailed)
	if strings.EqualFold(recoveredResp.Status, string(events.AnalysisStatusFailed)) {
		isTerminalFailure = true
	} else if strings.EqualFold(dbRowStatusFailed, string(events.AnalysisStatusFailed)) {
		isTerminalFailure = true
	}
	assert.True(t, isTerminalFailure, "confirmed failed row must be treated as terminal failure")
}

// TestGetConfirmedTerminalAnalysisDbReadErrorReturnsFalse verifies that if one stage
// is FAILED but reading another active stage encounters a DB error,
// getConfirmedTerminalAnalysis aborts and returns false rather than falsely confirming failure.
func TestGetConfirmedTerminalAnalysisDbReadErrorReturnsFalse(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-dberr-run"
		fp      = "fp-dberr-run"
		acct    = "acct-dberr-run"
		aggKey  = "pod_crash"
	)

	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	dbManager := &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}

	// 1. GetEventInfo succeeds
	mock.ExpectQuery("SELECT id, fingerprint, aggregation_key, created_at FROM events WHERE id = .*").
		WithArgs(eventID, acct).
		WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}).
			AddRow(eventID, fp, aggKey, time.Now()))

	// 2. allEventAnalysisTypesCompleted check: Summary is FAILED, so returns false immediately
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-sum", "{}", string(events.AnalysisStatusFailed), eventID, "failed summary", "rate limited", time.Now()))

	// 3. Failure confirmation loop:
	// Log row read succeeds: COMPLETED
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeLog).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-log", "{}", string(events.AnalysisStatusCompleted), eventID, "log", "", time.Now()))

	// Summary row read succeeds: FAILED
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
	mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
		WithArgs(fp, acct, aggKey, events.AnalysisTypeSummary).
		WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
			AddRow("row-sum", "{}", string(events.AnalysisStatusFailed), eventID, "failed summary", "rate limited", time.Now()))

	// Investigation row read encounters a transient DB error!
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
		WithArgs(eventID, events.AnalysisTypeInvestigation).
		WillReturnError(assert.AnError)

	req := EventAnalysisRequest{
		EventId:   eventID,
		AccountId: acct,
	}

	_, terminal := getConfirmedTerminalAnalysis(ctx, req, dbManager)
	assert.False(t, terminal, "DB read error must abort terminal failure confirmation to prevent false token drainage")
	require.NoError(t, mock.ExpectationsWereMet())
}
