package core

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/watch"
)

// newSummarizerWithMockDB wires an llmSummarizer to a fresh sqlmock-backed
// *sqlx.DB. Keeps the test file self-contained — no dependency on the
// agents/core production constructor (which would require GetDatabaseManager
// wiring).
func newSummarizerWithMockDB(t *testing.T) (*llmSummarizer, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &llmSummarizer{db: sqlx.NewDb(db, "postgres")}, mock
}

// TestLoadConversationFraming_UsesGenerationRowColumns is the regression
// test for the bug where loadConversationFraming filtered by
// message_type='question' and 'response' — neither of which is ever
// inserted (the agent's exchange lives on the `generation` row, with the
// prompt in `message` and the reply in `response`). After the fix this
// test asserts the loader returns the real user query and agent reply,
// and that they then propagate into the rendered summarizer prompt.
func TestLoadConversationFraming_UsesGenerationRowColumns(t *testing.T) {
	s, mock := newSummarizerWithMockDB(t)

	tenantID := uuid.New()
	conversationID := uuid.New()
	const wantQuery = "restart the slow-rollout-app deployment and let me know once all pods are healthy"
	const wantReply = "I have restarted the slow-rollout-app deployment in the otel-demo namespace."

	// First SELECT: earliest generation row's `message` column.
	mock.ExpectQuery(`SELECT COALESCE\(m\.message,''\)`).
		WithArgs(conversationID.String(), tenantID.String()).
		WillReturnRows(sqlmock.NewRows([]string{"message"}).AddRow(wantQuery))

	// Second SELECT: latest generation row's `response` column with the
	// `m.response IS NOT NULL` filter — matches responder.go's reader.
	mock.ExpectQuery(`SELECT COALESCE\(m\.response,''\)`).
		WithArgs(conversationID.String(), tenantID.String()).
		WillReturnRows(sqlmock.NewRows([]string{"response"}).AddRow(wantReply))

	gotQuery, gotReply, err := s.loadConversationFraming(context.Background(), tenantID.String(), conversationID.String())
	require.NoError(t, err)
	require.Equal(t, wantQuery, gotQuery)
	require.Equal(t, wantReply, gotReply)
	require.NoError(t, mock.ExpectationsWereMet())

	// Now render the prompt and assert the user query + agent reply
	// actually land in it — the bug was silent precisely because the
	// prompt rendered fine with empty fields, and nothing checked that
	// the LLM saw the parent context.
	w := watch.Watch{
		ID:             uuid.New(),
		ConversationID: conversationID,
		TenantID:       tenantID,
		AccountID:      uuid.New(),
		SourceKind:     "tool",
		PredicateKind:  "regex",
		PredicateExpr:  "successfully rolled out",
	}
	prompt, perr := s.renderPrompt(w, watch.StatusCompleted, "successfully rolled out", "", gotQuery, gotReply, "- kubectl_execute :: rollout restart")
	require.NoError(t, perr)
	require.Contains(t, prompt, "restart the slow-rollout-app",
		"rendered prompt must contain the user query — empty user_query was the original bug")
	require.Contains(t, prompt, "otel-demo namespace",
		"rendered prompt must contain the agent's last reply")
}

// TestLoadConversationFraming_RejectsEmptyTenant pins the fail-fast guard.
func TestLoadConversationFraming_RejectsEmptyTenant(t *testing.T) {
	s, _ := newSummarizerWithMockDB(t)
	_, _, err := s.loadConversationFraming(context.Background(), "", uuid.New().String())
	require.Error(t, err)
	require.Contains(t, err.Error(), "tenant_id is required")
}
