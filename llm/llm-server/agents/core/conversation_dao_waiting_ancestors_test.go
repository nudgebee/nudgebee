package core

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarkWaitingAncestorsTerminal_HappyPath pins the SQL the sweep emits:
// a recursive CTE bounded by maxWaitingAncestorSweepHops, followed by an
// UPDATE that only touches rows whose current status is 'waiting'. The
// bound and the status filter are the two safety properties — a corrupted
// parent chain must not run away, and an already-terminal ancestor (e.g. a
// separately-completed grandparent) must not be re-flipped by this sweep.
//
// Also pins the nil-UUID exclusion in the CTE (parent_agent_id <>
// '00000000-…'): this codebase persists top-level agents with the nil UUID,
// not NULL, so without the explicit filter the CTE would waste a lookup
// against a phantom row (and, in the pathological case where a row with
// id=nil-UUID happens to exist in Waiting, would flip it incorrectly).
func TestMarkWaitingAncestorsTerminal_HappyPath(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	childID := uuid.New().String()

	// The SQL is a single Exec; assert its shape + args. WITH RECURSIVE +
	// hops bound + status='waiting' filter + nil-UUID exclusion are the
	// four safety invariants — all must appear in the SQL.
	mock.ExpectExec(`WITH RECURSIVE ancestors\(id, hops\).*'00000000-0000-0000-0000-000000000000'`).
		WithArgs(childID, string(AgentExecutionStatusSuccess), maxWaitingAncestorSweepHops).
		WillReturnResult(sqlmock.NewResult(0, 2)) // 2 ancestors flipped

	err := dao.markWaitingAncestorsTerminal(childID, AgentExecutionStatusSuccess)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMarkWaitingAncestorsTerminal_FailStatusPropagates asserts the same
// method carries a fail status through correctly. Historically Waiting
// ancestor rows should reflect the descendant's ACTUAL outcome (success or
// fail), not a hardcoded status — otherwise a failed retry-resume would
// leave the DB claiming the parent succeeded.
func TestMarkWaitingAncestorsTerminal_FailStatusPropagates(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	childID := uuid.New().String()

	mock.ExpectExec(`WITH RECURSIVE ancestors`).
		WithArgs(childID, string(AgentExecutionStatusFail), maxWaitingAncestorSweepHops).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := dao.markWaitingAncestorsTerminal(childID, AgentExecutionStatusFail)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMarkWaitingAncestorsTerminal_DBErrorPropagates asserts the sweep
// returns errors up (the caller in UpdateConversationAgentResponse logs
// them but doesn't roll back the primary write — that's a deliberate
// best-effort contract). This test pins the "returns err on DB failure"
// half of that contract so a future refactor doesn't silently swallow
// errors here (which would mask real DB issues).
func TestMarkWaitingAncestorsTerminal_DBErrorPropagates(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	childID := uuid.New().String()

	mock.ExpectExec(`WITH RECURSIVE ancestors`).
		WithArgs(childID, string(AgentExecutionStatusSuccess), maxWaitingAncestorSweepHops).
		WillReturnError(errors.New("simulated DB failure"))

	err := dao.markWaitingAncestorsTerminal(childID, AgentExecutionStatusSuccess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "markWaitingAncestorsTerminal:")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateConversationAgentResponse_TriggersAncestorSweepOnTerminal pins
// the invariant that the sweep is invoked from UpdateConversationAgentResponse
// (the actual write path prod exercises) EXACTLY when the new status is
// terminal (success or fail). The Waiting-ancestor bookkeeping bug only
// occurs on the resume path, which reaches this function to flip the child
// from waiting → success; if a future refactor bypasses this function or
// gates the sweep on the wrong condition, the DB stale-rows accumulate again.
func TestUpdateConversationAgentResponse_TriggersAncestorSweepOnTerminal(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	childID := uuid.New().String()

	// The pre-check for termination cache issues an inner SELECT — mirror
	// what the DAO emits so the mock sequence lines up. The row-not-found
	// path is fine (the code proceeds without termination override, which is
	// the codepath we want to exercise here).
	mock.ExpectQuery(`SELECT message_id::text, account_id::text, conversation_id::text`).
		WithArgs(childID).
		WillReturnError(errors.New("no rows")) // triggers the debug-log fallback; response/agentStatus unchanged

	// Primary agent-status write.
	mock.ExpectExec(`UPDATE llm_conversation_agent SET response = \$2`).
		WithArgs(childID, "done", AgentExecutionStatusSuccess, "state", "summary", "steps", "refs").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// The follow-up sweep. If this expectation is NOT met, the test fails —
	// pinning that the sweep call actually happens for terminal statuses.
	mock.ExpectExec(`WITH RECURSIVE ancestors`).
		WithArgs(childID, string(AgentExecutionStatusSuccess), maxWaitingAncestorSweepHops).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := dao.UpdateConversationAgentResponse(childID, "done", AgentExecutionStatusSuccess, "state", "summary", "steps", "refs")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "primary + sweep must both fire on terminal completion")
}

// TestUpdateConversationAgentResponse_SkipsSweepWhenPrimaryUpdateNoOps
// closes the Gemini-flagged race: the primary UPDATE at line 1201 carries
// a `status NOT IN ('terminated','fail','success')` guard that no-ops
// when the child is already terminal from another path (user termination,
// concurrent write). In that case, the child's real status is NOT what
// this call intended — sweeping ancestors with the intended status
// would flag them 'success' even when the actual outcome was 'fail'
// (or 'terminated'). Sweep must be gated on rows-affected > 0 so a
// lost race silently no-ops rather than propagating a stale intent up
// the ancestor chain.
func TestUpdateConversationAgentResponse_SkipsSweepWhenPrimaryUpdateNoOps(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	childID := uuid.New().String()

	mock.ExpectQuery(`SELECT message_id::text, account_id::text, conversation_id::text`).
		WithArgs(childID).
		WillReturnError(errors.New("no rows"))

	// Primary write no-ops (0 rows affected) — child was already in a
	// terminal state, so the WHERE-guard rejected the write.
	mock.ExpectExec(`UPDATE llm_conversation_agent SET response = \$2`).
		WithArgs(childID, "done", AgentExecutionStatusSuccess, "state", "summary", "steps", "refs").
		WillReturnResult(sqlmock.NewResult(0, 0)) // ← 0 rows affected

	// Sweep must NOT fire — no mock.ExpectExec for the CTE. If the sweep
	// runs, ExpectationsWereMet will fail.

	err := dao.UpdateConversationAgentResponse(childID, "done", AgentExecutionStatusSuccess, "state", "summary", "steps", "refs")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "sweep MUST NOT fire when primary UPDATE affected 0 rows — propagating stale intent would spread the wrong outcome to ancestors")
}

// TestUpdateConversationAgentResponse_SkipsSweepOnNonTerminal is the
// negative half of the previous test. Waiting-ancestor sweeps are only
// meaningful when the child transitions TO a terminal state — running the
// sweep on a waiting→waiting or in_progress→waiting update would spread the
// wrong signal upward (parents would get flipped to whatever placeholder
// status the caller was writing mid-turn). This test ensures the sweep does
// NOT fire on non-terminal statuses.
func TestUpdateConversationAgentResponse_SkipsSweepOnNonTerminal(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	childID := uuid.New().String()

	mock.ExpectQuery(`SELECT message_id::text, account_id::text, conversation_id::text`).
		WithArgs(childID).
		WillReturnError(errors.New("no rows"))

	// Only the primary write fires — no ancestor sweep expectation follows.
	mock.ExpectExec(`UPDATE llm_conversation_agent SET response = \$2`).
		WithArgs(childID, "still waiting", AgentExecutionStatusWaiting, "state", "summary", "steps", "refs").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := dao.UpdateConversationAgentResponse(childID, "still waiting", AgentExecutionStatusWaiting, "state", "summary", "steps", "refs")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "no sweep must fire on non-terminal (waiting) status")
}

// TestUpdateConversationAgentResponse_SweepErrorDoesNotFailPrimaryWrite
// pins the "best-effort" contract: if the sweep errors, the caller still
// gets nil (primary write succeeded), and the caller's warning-log path
// handles the sweep failure without unwinding the successful write. If a
// future refactor bubbles the sweep error up, a transient DB blip on the
// cosmetic sweep would turn every successful agent completion into a
// visible failure — a serious regression from what is meant as best-effort.
func TestUpdateConversationAgentResponse_SweepErrorDoesNotFailPrimaryWrite(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	childID := uuid.New().String()

	mock.ExpectQuery(`SELECT message_id::text, account_id::text, conversation_id::text`).
		WithArgs(childID).
		WillReturnError(errors.New("no rows"))

	mock.ExpectExec(`UPDATE llm_conversation_agent SET response = \$2`).
		WithArgs(childID, "done", AgentExecutionStatusSuccess, "state", "summary", "steps", "refs").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(`WITH RECURSIVE ancestors`).
		WithArgs(childID, string(AgentExecutionStatusSuccess), maxWaitingAncestorSweepHops).
		WillReturnError(errors.New("simulated sweep failure"))

	err := dao.UpdateConversationAgentResponse(childID, "done", AgentExecutionStatusSuccess, "state", "summary", "steps", "refs")
	assert.NoError(t, err, "sweep failure must NOT bubble up — primary write already committed, sweep is best-effort")
	assert.NoError(t, mock.ExpectationsWereMet())
}
