package core

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// The alert's one conversation (#40734) as the conversation DAO keeps it, on a disposable PostgreSQL database
// (REMEDIATION_LOOP_TEST_DB_URL), in a schema of its own.
func oneConversationDB(t *testing.T) (*sqlx.DB, *ConversationDao) {
	t.Helper()
	dsn := os.Getenv("REMEDIATION_LOOP_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set REMEDIATION_LOOP_TEST_DB_URL to a disposable PostgreSQL database")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := "core_conv_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
	_, err = db.Exec(`
		CREATE TABLE llm_conversations (id uuid PRIMARY KEY, account_id uuid, session_id text, status text, updated_at timestamp DEFAULT now());
		CREATE TABLE llm_conversation_messages (id uuid PRIMARY KEY, account_id uuid, user_id uuid, conversation_id uuid, role text, message text,
			response text, message_type text, status text, worker_name text, agent_name text, parent_agent_id uuid, message_config text,
			message_context text, llm_provider text, llm_model text, metadata jsonb, created_at timestamp DEFAULT clock_timestamp(),
			updated_at timestamp DEFAULT now());
		CREATE TABLE llm_conversation_agent (id uuid PRIMARY KEY, conversation_id uuid, account_id uuid, status text, response text, updated_at timestamp);
		CREATE TABLE llm_conversation_tool_calls (id uuid PRIMARY KEY, conversation_id uuid, account_id uuid, status text, response text, updated_at timestamp)`)
	require.NoError(t, err)
	return db, NewConversationDao(&common.DatabaseManager{Db: db})
}

func addConversation(t *testing.T, db *sqlx.DB, account string, status ConversationStatus) string {
	t.Helper()
	id := uuid.NewString()
	_, err := db.Exec(`INSERT INTO llm_conversations (id, account_id, session_id, status) VALUES ($1, $2, 'event-fp-1', $3)`, id, account, status)
	require.NoError(t, err)
	return id
}

func addMessage(t *testing.T, db *sqlx.DB, account, conversation, messageType string, status ConversationStatus, metadata string) string {
	t.Helper()
	id := uuid.NewString()
	var md any
	if metadata != "" {
		md = metadata
	}
	_, err := db.Exec(`INSERT INTO llm_conversation_messages (id, account_id, conversation_id, role, message, response, message_type, status, message_config, metadata)
		VALUES ($1, $2, $3, 'human', $7, 'r', $4, $5, '{}', $6::jsonb)`, id, account, conversation, messageType, status, md, "m-"+id)
	require.NoError(t, err)
	return id
}

// #42142 bug: once the waiting-expiry job ended an alert conversation's unanswered approval (TERMINATED), NudgeBee
// never started a planning or follow-up turn there again. Now a conversation NudgeBee closed (closed_reason on its
// newest ended message) is idle for automatic turns; one a person stopped is not.
func TestStartIdleTurn_TerminatedIdleOnlyWhenNudgeBeeClosedIt_42142(t *testing.T) {
	db, dao := oneConversationDB(t)
	account := uuid.NewString()
	ctx := context.Background()

	stopped := addConversation(t, db, account, ConversationStatusTerminated)
	addMessage(t, db, account, stopped, "generation", ConversationStatusTerminated, "")
	idle, err := dao.AutomaticTurnIdle(ctx, stopped, account, nil)
	require.NoError(t, err)
	assert.False(t, idle, "a person's Stop keeps automatic turns off")
	started, err := dao.StartIdleTurn(ctx, stopped, account, uuid.NewString(), "check", "remediation", toolcore.NBQueryConfig{}, "", nil)
	require.NoError(t, err)
	assert.False(t, started)

	lapsed := addConversation(t, db, account, ConversationStatusTerminated)
	addMessage(t, db, account, lapsed, "followup", ConversationStatusTerminated, `{"closed_reason":"lapsed"}`)
	idle, err = dao.AutomaticTurnIdle(ctx, lapsed, account, nil)
	require.NoError(t, err)
	assert.True(t, idle)
	started, err = dao.StartIdleTurn(ctx, lapsed, account, uuid.NewString(), "check", "remediation", toolcore.NBQueryConfig{}, "", nil)
	require.NoError(t, err)
	assert.True(t, started, "NudgeBee closed the prompt: its own turns start again")

	// A person stopped it after that: off again.
	again := addConversation(t, db, account, ConversationStatusTerminated)
	addMessage(t, db, account, again, "followup", ConversationStatusTerminated, `{"closed_reason":"lapsed"}`)
	addMessage(t, db, account, again, "generation", ConversationStatusTerminated, "")
	idle, err = dao.AutomaticTurnIdle(ctx, again, account, nil)
	require.NoError(t, err)
	assert.False(t, idle)
}

// A person stopped the alert's conversation, then asked for a Re-check: the Re-check waited, queued, until it
// expired, because a stopped conversation was off-limits to every turn but a typed message. A turn a person asked
// for after the stop starts; one asked before it, or NudgeBee's own, does not.
func TestStartIdleTurn_PersonAskedAfterStop(t *testing.T) {
	db, dao := oneConversationDB(t)
	account := uuid.NewString()
	ctx := context.Background()
	stoppedAt := time.Date(2026, 11, 3, 10, 17, 47, 0, time.UTC)
	before, after := stoppedAt.Add(-8*time.Minute), stoppedAt.Add(4*time.Second)

	conversation := addConversation(t, db, account, ConversationStatusTerminated)
	stopped := addMessage(t, db, account, conversation, "generation", ConversationStatusTerminated, "")
	_, err := db.Exec(`UPDATE llm_conversation_messages SET updated_at = $2 WHERE id = $1`, stopped, stoppedAt)
	require.NoError(t, err)
	// The model call in flight at the stop sets, then clears, its live status afterwards: the stop time stays.
	require.NoError(t, dao.SetMessageLiveStatus(stopped, "remediation", &llmruntime.LiveStatus{Text: "Couldn't reach the platform AI model."}))
	require.NoError(t, dao.SetMessageLiveStatus(stopped, "remediation", nil))

	for _, tc := range []struct {
		name  string
		asked *time.Time
		idle  bool
	}{
		{"NudgeBee's own turn", nil, false},
		{"asked before the stop", &before, false},
		{"asked after the stop", &after, true},
	} {
		idle, err := dao.AutomaticTurnIdle(ctx, conversation, account, tc.asked)
		require.NoError(t, err)
		assert.Equal(t, tc.idle, idle, tc.name)
	}
	started, err := dao.StartIdleTurn(ctx, conversation, account, uuid.NewString(), "recheck", "remediation", toolcore.NBQueryConfig{}, "", &before)
	require.NoError(t, err)
	assert.False(t, started)
	started, err = dao.StartIdleTurn(ctx, conversation, account, uuid.NewString(), "recheck", "remediation", toolcore.NBQueryConfig{}, "", &after)
	require.NoError(t, err)
	assert.True(t, started, "the person's Re-check starts")
}

// R1: an automatic turn does not start while the hold key (the alert's investigation lock) is held elsewhere.
func TestStartIdleTurn_RefusesWhileHoldKeyHeld(t *testing.T) {
	db, dao := oneConversationDB(t)
	account := uuid.NewString()
	ctx := context.Background()
	conversation := addConversation(t, db, account, ConversationStatusCompleted)
	key := "event-analysis:test:" + uuid.NewString()

	conn, err := db.Connx(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, key)
	require.NoError(t, err)

	started, err := dao.StartIdleTurn(ctx, conversation, account, uuid.NewString(), "plan", "remediation", toolcore.NBQueryConfig{}, key, nil)
	require.NoError(t, err)
	assert.False(t, started, "the investigation holds the conversation")
	var n int
	require.NoError(t, db.Get(&n, `SELECT count(*) FROM llm_conversation_messages`))
	assert.Zero(t, n, "nothing saved")

	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key)
	require.NoError(t, err)
	started, err = dao.StartIdleTurn(ctx, conversation, account, uuid.NewString(), "plan", "remediation", toolcore.NBQueryConfig{}, key, nil)
	require.NoError(t, err)
	assert.True(t, started)
}

// R2: the no-model close ends the waiting turn and marks every ended message with why.
func TestCloseWaitingConversation_StampsTheReason(t *testing.T) {
	db, dao := oneConversationDB(t)
	account := uuid.NewString()
	conversation := addConversation(t, db, account, ConversationStatusWaiting)
	turn := addMessage(t, db, account, conversation, "generation", ConversationStatusWaiting, "")
	followup := addMessage(t, db, account, conversation, "followup", ConversationStatusWaiting, "")
	done := addMessage(t, db, account, conversation, "generation", ConversationStatusCompleted, "")

	closed, err := dao.CloseWaitingConversation(security.NewRequestContextForSuperAdmin(), account, conversation, "closed", ClosedReasonSuperseded)
	require.NoError(t, err)
	assert.True(t, closed)
	var rows []struct {
		Id     string `db:"id"`
		Status string `db:"status"`
		Reason string `db:"reason"`
	}
	require.NoError(t, db.Select(&rows, `SELECT id::text AS id, status, COALESCE(metadata->>'closed_reason', '') AS reason FROM llm_conversation_messages`))
	got := map[string][2]string{}
	for _, r := range rows {
		got[r.Id] = [2]string{r.Status, r.Reason}
	}
	assert.Equal(t, [2]string{"TERMINATED", "superseded"}, got[turn])
	assert.Equal(t, [2]string{"TERMINATED", "superseded"}, got[followup])
	assert.Equal(t, [2]string{"COMPLETED", ""}, got[done], "a finished message is left alone")
	idle, err := dao.AutomaticTurnIdle(context.Background(), conversation, account, nil)
	require.NoError(t, err)
	assert.True(t, idle)

	closed, err = dao.ExpireWaitingConversation(security.NewRequestContextForSuperAdmin(), account, conversation, "again")
	require.NoError(t, err)
	assert.False(t, closed, "no longer waiting")
}

// R3/AC6: a stage turn is saved with its run in typed metadata, and the history leaves it out; flag-off rows
// (untagged) are history as before.
func TestStageTurnTaggedAndLeftOutOfHistory(t *testing.T) {
	db, dao := oneConversationDB(t)
	account := uuid.NewString()
	conversation := addConversation(t, db, account, ConversationStatusCompleted)
	runId := uuid.NewString()

	stage, err := dao.SaveConversationMessage("", conversation, account, "", MessageRoleHuman, MessageTypeGeneration, "investigate event e1", "the root cause", "rootcause",
		uuid.Nil, toolcore.NBQueryConfig{InvestigationRunId: runId, EventId: "e1"}, "", "", "", ConversationStatusCompleted)
	require.NoError(t, err)
	var tag string
	require.NoError(t, db.Get(&tag, `SELECT COALESCE(metadata->>'investigation_run_id', '') FROM llm_conversation_messages WHERE id = $1`, stage))
	assert.Equal(t, runId, tag)
	stageFollowup := addMessage(t, db, account, conversation, "followup", ConversationStatusCompleted, `{"investigation_run_id":"`+runId+`"}`)
	chat, err := dao.SaveConversationMessage("", conversation, account, "", MessageRoleHuman, MessageTypeGeneration, "why did it fail?", "because", "remediation",
		uuid.Nil, toolcore.NBQueryConfig{}, "", "", "", ConversationStatusCompleted)
	require.NoError(t, err)
	var untagged any
	require.NoError(t, db.Get(&untagged, `SELECT metadata FROM llm_conversation_messages WHERE id = $1`, chat))
	assert.Nil(t, untagged, "a person's turn carries no tag")

	history, err := dao.LoadConversationMessages(account, conversation, "", "", 20)
	require.NoError(t, err)
	var ids []string
	for _, m := range history {
		ids = append(ids, m["id"])
	}
	assert.Equal(t, []string{chat.String()}, ids, "the stage turn and its follow-up are not history")
	assert.NotContains(t, ids, stageFollowup)

	// A turn's history starts with the investigation's bounded report instead.
	prevDao := PeekConversationDao()
	SetConversationDao(dao)
	t.Cleanup(func() { SetConversationDao(prevDao) })
	prevHook := InvestigationReportForHistory
	InvestigationReportForHistory = func(_ *security.RequestContext, gotAccount, gotConversation string) (string, string) {
		assert.Equal(t, account, gotAccount)
		assert.Equal(t, conversation, gotConversation)
		return "e1", "Root cause: the limit is too low."
	}
	t.Cleanup(func() { InvestigationReportForHistory = prevHook })
	formatted, err := getExistingHistory(NBAgentRequest{AccountId: account, ConversationId: conversation, MessageId: uuid.NewString()}, security.NewRequestContextForSuperAdmin())
	require.NoError(t, err)
	require.Len(t, formatted, 4, "report pair, then the person's turn")
	first, err := formatted[0].FormatMessages(map[string]any{})
	require.NoError(t, err)
	assert.Contains(t, first[0].GetContent(), "NudgeBee's investigation of event e1")
	second, err := formatted[1].FormatMessages(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "Root cause: the limit is too low.", second[0].GetContent())
}
