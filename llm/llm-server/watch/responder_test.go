package watch

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers ---------------------------------------------------------------

func newWatchForResponder(t *testing.T) Watch {
	t.Helper()
	return Watch{
		ID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ConversationID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		// TenantID is required by appendWatchUpdate's fail-fast guard
		// added to address gemini-code-assist's defense-in-depth flag.
		// Use a stable test UUID so sqlmock expectations can bind it.
		TenantID: uuid.MustParse("44444444-4444-4444-4444-444444444444"),
	}
}

// ---------------------------------------------------------------------------
// renderWatchUpdateBlock — pure formatting, no DB
// ---------------------------------------------------------------------------

func TestRenderWatchUpdateBlock_IncludesMarkerHeadingAndBody(t *testing.T) {
	w := newWatchForResponder(t)
	got := renderWatchUpdateBlock(w, StatusCompleted, "all 5 replicas are now running 1.2.3")

	assert.Contains(t, got, "<!-- watch-update:"+w.ID.String()+" -->",
		"marker must be present so re-runs can skip duplicate appends")
	assert.Contains(t, got, "Watch update — COMPLETED",
		"status heading must read as the explicit terminal state")
	assert.Contains(t, got, "all 5 replicas are now running 1.2.3",
		"summary body must be preserved verbatim inside the blockquote")
	assert.True(t, strings.HasPrefix(got, "\n\n---\n\n"),
		"block must start with the horizontal rule that separates it from the prior agent reply")
}

// TestRenderWatchUpdateBlock_NeutralizesForeignMarker locks in the
// marker-injection defense: a summary derived from untrusted observation
// data must not be able to embed ANOTHER watch's literal idempotency marker
// (which would poison that watch's strings.Contains skip-check) or open a
// raw HTML comment in the stored message.
func TestRenderWatchUpdateBlock_NeutralizesForeignMarker(t *testing.T) {
	w := newWatchForResponder(t)
	foreign := "00000000-0000-0000-0000-00000000beef"
	malicious := "done <!-- watch-update:" + foreign + " --> and more"
	got := renderWatchUpdateBlock(w, StatusCompleted, malicious)

	// Our own marker (the only one keyed on w.ID) must still be present...
	assert.Contains(t, got, "<!-- watch-update:"+w.ID.String()+" -->")
	// ...but the foreign marker must NOT survive intact in the body.
	assert.NotContains(t, got, "<!-- watch-update:"+foreign+" -->",
		"a foreign watch-update marker in untrusted summary text must be neutralized")
	// The neutralized escape form is what the user sees instead.
	assert.Contains(t, got, "&lt;!--")
}

func TestRenderWatchUpdateBlock_HandlesEmptySummary(t *testing.T) {
	w := newWatchForResponder(t)
	got := renderWatchUpdateBlock(w, StatusFailed, "")
	assert.Contains(t, got, "(no summary available)",
		"empty summary must render a stable placeholder, not an empty blockquote (which renders badly)")
	assert.Contains(t, got, "Watch update — FAILED")
}

func TestRenderWatchUpdateBlock_MultilineSummaryGetsBlockquoted(t *testing.T) {
	w := newWatchForResponder(t)
	body := "line one\nline two\nline three"
	got := renderWatchUpdateBlock(w, StatusCompleted, body)
	// Every line after the first must get the "> " continuation prefix so
	// markdown renders the whole block as a single quote, not just the
	// first line followed by orphan text.
	assert.Contains(t, got, "> line one")
	assert.Contains(t, got, "> line two")
	assert.Contains(t, got, "> line three")
}

func TestRenderWatchUpdateBlock_StatusIconsPerStatus(t *testing.T) {
	w := newWatchForResponder(t)
	cases := map[Status]string{
		StatusCompleted: "✅",
		StatusFailed:    "❌",
		StatusExpired:   "⌛",
		StatusCancelled: "🚫",
	}
	for status, expectedIcon := range cases {
		got := renderWatchUpdateBlock(w, status, "x")
		assert.Contains(t, got, expectedIcon,
			"status %s must render with its glanceable icon %s — the UI relies on this to colour-match the chip", status, expectedIcon)
	}
}

// ---------------------------------------------------------------------------
// appendWatchUpdate — DB write path against sqlmock
// ---------------------------------------------------------------------------

// The SELECT and UPDATE regexes below match the JOIN-form queries the
// responder now emits to scope by tenant_id (defense-in-depth flagged
// by gemini-code-assist in PR review). Both the SELECT and the UPDATE
// take tenant_id as an additional bound parameter so the mock expects
// it. We use prefix matching so sqlmock's regex engine tolerates the
// rest of the JOIN clause.

func TestAppendWatchUpdate_AppendsToExistingResponse(t *testing.T) {
	m, mock, _ := newMockManager(t)
	w := newWatchForResponder(t)
	messageID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(w.ConversationID, w.TenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"}).
			AddRow(messageID, "original reply"))
	// New atomic UPDATE: args are (block, messageID, tenantID, marker).
	// The DB performs the concatenation server-side via response || $1 and
	// the marker-guard via `position($4 IN response) = 0`.
	mock.ExpectExec("UPDATE llm_conversation_messages").
		WithArgs(sqlmock.AnyArg(), messageID, w.TenantID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "rollout settled")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendWatchUpdate_NoResponseMessageIsSilentNoOp(t *testing.T) {
	// Watch can terminate before any agent reply landed (rare but possible
	// for fast operations). Responder must not error — the notifier path
	// still fires; only the in-thread append is skipped.
	m, mock, _ := newMockManager(t)
	w := newWatchForResponder(t)

	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(w.ConversationID, w.TenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"})) // empty

	err := appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "x")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendWatchUpdate_IdempotentOnDuplicateMarker(t *testing.T) {
	// If the dispatcher re-calls terminate for the same watch (e.g., a
	// crashed dispatcher that resumes), the second append must detect
	// the marker already present and skip the UPDATE. Otherwise the
	// response message grows with duplicate watch-update blocks.
	m, mock, _ := newMockManager(t)
	w := newWatchForResponder(t)
	messageID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	priorBlock := renderWatchUpdateBlock(w, StatusCompleted, "first time")
	existing := "original reply" + priorBlock

	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(w.ConversationID, w.TenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"}).
			AddRow(messageID, existing))
	// No UPDATE expectation — the responder must skip the write.

	err := appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "second time")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendWatchUpdate_UpdateBindsBlockAndMarker(t *testing.T) {
	// Atomic-append shape: the UPDATE bind args are
	//   ($1 = block, $2 = messageID, $3 = tenantID, $4 = marker)
	// and the SQL does `response = response || $1 WHERE … position($4 IN
	// response) = 0`. This test pins both bindings — a refactor that drops
	// the marker arg (and thus the TOCTOU guard) becomes visible here.
	m, mock, _ := newMockManager(t)
	w := newWatchForResponder(t)
	messageID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	original := "I have restarted the deployment."
	expectedBlock := renderWatchUpdateBlock(w, StatusCompleted, "rolled out")
	expectedMarker := "<!-- watch-update:" + w.ID.String() + " -->"

	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(w.ConversationID, w.TenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"}).
			AddRow(messageID, original))
	mock.ExpectExec("UPDATE llm_conversation_messages").
		WithArgs(expectedBlock, messageID, w.TenantID, expectedMarker).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "rolled out")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAppendWatchUpdate_AtomicGuardSkipsOnConcurrentMarker simulates the
// distinct-watches-same-conversation race: between this responder's SELECT
// and UPDATE, another watch's marker landed in `response`. Our UPDATE's
// position(marker IN response)=0 predicate matches THIS watch's marker,
// not the other's — so the marker-guard does NOT block us, but rows
// affected can be 0 if e.g. this watch's own marker also landed (same
// watch firing twice through a different path). The responder treats
// rows==0 as a silent no-op.
func TestAppendWatchUpdate_AtomicGuardSkipsOnConcurrentMarker(t *testing.T) {
	m, mock, _ := newMockManager(t)
	w := newWatchForResponder(t)
	messageID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(w.ConversationID, w.TenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"}).
			AddRow(messageID, "original reply"))
	mock.ExpectExec("UPDATE llm_conversation_messages").
		WithArgs(sqlmock.AnyArg(), messageID, w.TenantID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows — concurrent write

	err := appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "x")
	require.NoError(t, err, "concurrent marker must be a silent no-op, not an error")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAppendWatchUpdate_TargetsParentMessageIDWhenSet pins the responder's
// preference for the watch's stored parent_message_id over the
// "latest generation" heuristic. The fallback was the bug source — it
// landed the terminal block on the wrong reply when the user kept
// chatting while the watch was running. ParentMessageID binds the
// append to the exact reply the watch was registered against.
func TestAppendWatchUpdate_TargetsParentMessageIDWhenSet(t *testing.T) {
	m, mock, _ := newMockManager(t)
	w := newWatchForResponder(t)
	parentID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	w.ParentMessageID = &parentID

	// SELECT lookup must use the parent_message_id form — pinned by the
	// `m.id = $1 AND m.conversation_id = $2` shape the new branch emits.
	// If a refactor reverts to the latest-generation lookup it must fail
	// here.
	mock.ExpectQuery(`m\.id = \$1 AND m\.conversation_id = \$2`).
		WithArgs(parentID, w.ConversationID, w.TenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"}).AddRow(parentID, "original reply"))

	// Atomic UPDATE binds the same parentID — same shape as the fallback
	// path, validated by the existing concurrent-append tests.
	mock.ExpectExec(`UPDATE llm_conversation_messages AS m`).
		WithArgs(sqlmock.AnyArg(), parentID, w.TenantID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "rollout completed"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAppendWatchUpdate_RejectsMissingTenantID locks in the fail-fast
// guard added per gemini-code-assist's defense-in-depth review. Without
// the guard a future caller that forgets to populate TenantID would
// silently bypass tenant scoping (the join would no-op match nothing,
// which feels safe but masks the bug).
func TestAppendWatchUpdate_RejectsMissingTenantID(t *testing.T) {
	m, _, _ := newMockManager(t)
	w := newWatchForResponder(t)
	w.TenantID = uuid.Nil

	err := appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tenant_id is required")
}

// TestAppendWatchUpdate_RealDBErrorIsReturned pins the corrected behavior:
// a non-sql.ErrNoRows error on the target-message lookup (connection drop,
// timeout, scan failure) is a real failure and must be returned — not
// swallowed — so the executor logs it and increments
// nb_llm_watch_responder_failures instead of silently dropping the
// in-thread follow-up. The sql.ErrNoRows case (no parent reply yet) is
// still a silent skip; that path is covered by the idempotency/no-row tests.
func TestAppendWatchUpdate_RealDBErrorIsReturned(t *testing.T) {
	m, mock, _ := newMockManager(t)
	w := newWatchForResponder(t)

	// Simulate a real DB error on the SELECT — not sql.ErrNoRows.
	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(w.ConversationID, w.TenantID).
		WillReturnError(assertableConnectionError{})

	err := appendWatchUpdate(context.Background(), m.db, w, StatusCompleted, "x")
	require.Error(t, err,
		"a non-ErrNoRows lookup error must surface so the caller can log + count it, not be swallowed")
	require.Contains(t, err.Error(), "target message lookup failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestAppendWatchUpdate_DistinctMarkersBothLand_Sequential locks the
// per-watch marker semantics: two distinct watches appending to the same
// message use distinct markers, so the second append's `position($marker
// IN response) = 0` guard finds no match (different marker) and proceeds.
//
// NOTE: This is the SEQUENTIAL approximation. The real concurrency case
// — two goroutines racing on the same row under a real Postgres — would
// be the proof that the atomic UPDATE is actually serialized correctly.
// sqlmock cannot model concurrent transactions sharing a row, so the
// integration variant is a TODO that lives under
// restart_resume_integration_test.go's pattern (env-gated, real DB).
//
// What this test DOES prove: a refactor that re-uses one marker across
// distinct watches would cause the second UPDATE to silently no-op
// (rows == 0). The test fails if that happens.
func TestAppendWatchUpdate_DistinctMarkersBothLand_Sequential(t *testing.T) {
	m, mock, _ := newMockManager(t)
	convID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	tenant := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	messageID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	wA := Watch{
		ID:             uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		ConversationID: convID,
		TenantID:       tenant,
	}
	wB := Watch{
		ID:             uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
		ConversationID: convID,
		TenantID:       tenant,
	}

	// Capture the bound marker arg on each UPDATE so we can assert it
	// CONTAINS the watch.ID (the real contract) rather than equals a
	// specific string. A refactor that changed the marker template but
	// kept watch.ID in it should still pass this test; a refactor that
	// dropped watch.ID from the marker (and silently reused a constant)
	// would fail.
	var capturedMarkerA, capturedMarkerB string

	// First responder run: SELECT sees no markers, UPDATE appends block A.
	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(convID, tenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"}).
			AddRow(messageID, "agent reply"))
	mock.ExpectExec("UPDATE llm_conversation_messages").
		WithArgs(sqlmock.AnyArg(), messageID, tenant, stringCapture{out: &capturedMarkerA}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, appendWatchUpdate(context.Background(), m.db, wA, StatusCompleted, "summary A"))

	// Second responder run: SELECT now sees the response with watch A's
	// marker embedded. The check for watch B's marker still passes
	// (different watch.ID → different marker substring). UPDATE succeeds —
	// both blocks coexist in the final response.
	priorBlockA := renderWatchUpdateBlock(wA, StatusCompleted, "summary A")
	mock.ExpectQuery("SELECT m.id, m.response").
		WithArgs(convID, tenant).
		WillReturnRows(sqlmock.NewRows([]string{"id", "response"}).
			AddRow(messageID, "agent reply"+priorBlockA))
	mock.ExpectExec("UPDATE llm_conversation_messages").
		WithArgs(sqlmock.AnyArg(), messageID, tenant, stringCapture{out: &capturedMarkerB}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, appendWatchUpdate(context.Background(), m.db, wB, StatusCompleted, "summary B"))
	require.NoError(t, mock.ExpectationsWereMet())

	// The contract being pinned: each marker must contain the source watch's ID.
	assert.Contains(t, capturedMarkerA, wA.ID.String(),
		"marker for watch A must contain watch A's ID — otherwise two distinct watches with the same predicate could collide on a constant marker")
	assert.Contains(t, capturedMarkerB, wB.ID.String(),
		"marker for watch B must contain watch B's ID")
	assert.NotEqual(t, capturedMarkerA, capturedMarkerB,
		"distinct watches must produce distinct markers; equality would break the position() guard on subsequent appends")
}

// stringCapture matches any driver.Value that is a string and stashes it
// into out. Used to assert on the BOUND value rather than equality against
// a test-constructed string — surfaces semantics ("marker contains
// watch.ID") instead of stringly-typed equality.
type stringCapture struct{ out *string }

func (s stringCapture) Match(v driver.Value) bool {
	str, ok := v.(string)
	if !ok {
		return false
	}
	*s.out = str
	return true
}

// assertableConnectionError is a stand-in for the kind of error
// database/sql can surface when the connection drops mid-query. It's NOT
// sql.ErrNoRows — the responder currently treats it as if it were.
type assertableConnectionError struct{}

func (assertableConnectionError) Error() string { return "pq: connection refused" }
