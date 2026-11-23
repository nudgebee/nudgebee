package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
)

// Against the real tables: the follow-up an answer names is read with its message, key and state; the note and
// surface are written on it only while it waits; an approved step reads them back, and a denial lists them.
func TestApprovalAnswerRecordedOnTheFollowup_DB(t *testing.T) {
	db := remediationScratchDB(t, "approval_note", `CREATE TABLE llm_conversations (LIKE public.llm_conversations INCLUDING ALL);
		CREATE TABLE llm_conversation_messages (LIKE public.llm_conversation_messages INCLUDING ALL);
		CREATE TABLE llm_conversation_agent (LIKE public.llm_conversation_agent INCLUDING ALL);
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid, fingerprint text, nb_status text, created_at timestamp DEFAULT LOCALTIMESTAMP - interval '1 hour')`)
	withMetastore(t, db)
	ctx := context.Background()
	account, conv, turn, followup, agent, event := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	const key = "run_step:v-hash:1:apply"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status) VALUES ($1, $2, 'fp-note', 'OPEN')`, []any{event, account}},
		{`INSERT INTO llm_conversations (id, account_id, tenant_id, session_id, status) VALUES ($1, $2, $3, 'event-fp-note', 'WAITING')`, []any{conv, account, uuid.NewString()}},
		{`INSERT INTO llm_conversation_messages (id, conversation_id, account_id, message, role, message_type, status) VALUES ($1, $2, $3, 'go with option 1', 'human', 'chat', 'WAITING')`,
			[]any{turn, conv, account}},
		{`INSERT INTO llm_conversation_messages (id, conversation_id, account_id, message, role, message_type, status, message_config)
			VALUES ($1, $2, $3, 'approve?', 'ai', 'followup', 'WAITING', $4)`,
			[]any{followup, conv, account, `{"confirmationKey":"` + key + `","followupType":"tool_confirmation","question":"Approve?\nCommand - kubectl rollout restart deploy/api -n shop","toolName":"run_step"}`}},
		{`INSERT INTO llm_conversation_agent (id, agent_name, message_id, account_id, conversation_id, status, followup_message_id) VALUES ($1, 'remediation', $2, $3, $4, 'waiting', $5)`,
			[]any{agent, turn, account, conv, followup}},
	} {
		_, err := db.Exec(q.sql, q.args...)
		require.NoError(t, err)
	}

	approval, err := approvalOfAgent(ctx, account, agent)
	require.NoError(t, err)
	assert.Equal(t, waitingApproval{Found: true, ConversationId: conv, SessionId: "event-fp-note", Confirmation: true,
		FollowupMessageId: followup, Waiting: true, ConfirmationKey: key}, approval)

	require.NoError(t, writeApprovalAnswer(ctx, account, followup, approvalAnswer{Note: "too risky before the freeze", AnsweredFrom: ApprovalOriginSlack}))
	assert.Equal(t, approvalAnswer{Note: "too risky before the freeze", AnsweredFrom: ApprovalOriginSlack}, approvalAnswerOf(ctx, account, followup))
	assert.Equal(t, approvalAnswer{}, approvalAnswerOf(ctx, uuid.NewString(), followup), "another account's message is not read")

	// Answered (no), the follow-up is no longer written, and the denial lists the note.
	_, err = db.Exec(`UPDATE llm_conversation_messages SET status = 'COMPLETED', response = 'no', updated_at = LOCALTIMESTAMP WHERE id = $1`, followup)
	require.NoError(t, err)
	require.NoError(t, writeApprovalAnswer(ctx, account, followup, approvalAnswer{Note: "changed", AnsweredFrom: ApprovalOriginWeb}))
	assert.Equal(t, "too risky before the freeze", approvalAnswerOf(ctx, account, followup).Note)
	approval, err = approvalOfAgent(ctx, account, agent)
	require.NoError(t, err)
	assert.False(t, approval.Waiting)

	rows, err := deniedConfirmationsIn(security.NewRequestContextForTenantAdminWithUser(ctx, "tenant", "user-1"), db, account, "event-fp-note", event)
	require.NoError(t, err)
	denials := deniedApprovalsOf(account, event, rows)
	if assert.Len(t, denials, 1) {
		assert.Equal(t, "too risky before the freeze", denials[0].Note)
		assert.Equal(t, ApprovalOriginSlack, denials[0].AnsweredFrom)
		assert.Equal(t, 1, denials[0].Option)
	}
}
