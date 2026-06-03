package agents

import (
	"database/sql"
	"errors"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEventLookup is a narrow in-memory implementation of
// prTrackingEventLookup. Tests configure a handler that inspects the query
// and args and returns a pre-baked response (matched row or sql.ErrNoRows).
type fakeEventLookup struct {
	// handler receives (dest, query, args...) and is responsible for either
	// setting dest or returning an error. Return sql.ErrNoRows for a miss.
	handler func(dest interface{}, query string, args ...interface{}) error
	// calls records each (query, args) pair for assertion.
	calls []fakeEventCall
}

type fakeEventCall struct {
	query string
	args  []interface{}
}

func (f *fakeEventLookup) Get(dest interface{}, query string, args ...interface{}) error {
	f.calls = append(f.calls, fakeEventCall{query: query, args: args})
	if f.handler == nil {
		return sql.ErrNoRows
	}
	return f.handler(dest, query, args...)
}

// QueryRow is required by the interface but resolvePRTrackingEventId doesn't
// use it. Safe to panic — signals an unexpected code path.
func (f *fakeEventLookup) QueryRow(query string, args ...interface{}) *sql.Row {
	panic("QueryRow should not be called by resolvePRTrackingEventId")
}

// makeQuery builds an NBAgentRequest with the supplied identifier fields.
func makeQuery(eventId, sessionId, conversationId, accountId string) core.NBAgentRequest {
	return core.NBAgentRequest{
		AccountId:      accountId,
		SessionId:      sessionId,
		ConversationId: conversationId,
		QueryConfig: toolcore.NBQueryConfig{
			EventId: eventId,
		},
	}
}

// testCtx returns a RequestContext the helpers can use for logging.
// Tenant ID must be a valid UUID — NewRequestContextForTenantAdmin
// performs a metastore lookup that rejects non-UUID input with a
// pq parse error.
func testCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
}

func TestLookupEventIdBySessionId_HappyPath(t *testing.T) {
	const (
		fingerprint = "1efd2b8779ab9b227f8db6e1da2497bf4681d3286ada8a504729fb55c92c2bf3"
		accountId   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		expectedId  = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)
	db := &fakeEventLookup{
		handler: func(dest interface{}, query string, args ...interface{}) error {
			require.Len(t, args, 2, "query expects (fingerprint, accountId)")
			assert.Equal(t, fingerprint, args[0])
			assert.Equal(t, accountId, args[1])
			assert.Contains(t, query, "ORDER BY created_at DESC LIMIT 1")
			*(dest.(*string)) = expectedId
			return nil
		},
	}
	got := lookupEventIdBySessionId(testCtx(), db, "event-"+fingerprint, accountId)
	assert.Equal(t, expectedId, got)
	assert.Len(t, db.calls, 1)
}

func TestLookupEventIdBySessionId_Rejects(t *testing.T) {
	cases := []struct {
		name      string
		sessionId string
		accountId string
	}{
		{"empty session id", "", "acct-1"},
		{"missing event- prefix", "thread-abc", "acct-1"},
		{"empty fingerprint after prefix", "event-", "acct-1"},
		{"empty accountId", "event-abc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeEventLookup{handler: func(dest interface{}, query string, args ...interface{}) error {
				t.Fatalf("DB should not be queried for input: %q", tc.sessionId)
				return nil
			}}
			got := lookupEventIdBySessionId(testCtx(), db, tc.sessionId, tc.accountId)
			assert.Equal(t, "", got)
			assert.Len(t, db.calls, 0, "should short-circuit before DB")
		})
	}
}

func TestLookupEventIdBySessionId_NoRowReturnsEmpty(t *testing.T) {
	db := &fakeEventLookup{
		handler: func(dest interface{}, query string, args ...interface{}) error {
			return sql.ErrNoRows
		},
	}
	got := lookupEventIdBySessionId(testCtx(), db, "event-deadbeef", "acct-1")
	assert.Equal(t, "", got)
}

// TestResolvePRTrackingEventId_PrefersExplicit proves QueryConfig.EventId
// short-circuits — no DB hit needed — and reports hadEventAnchor=true.
func TestResolvePRTrackingEventId_PrefersExplicit(t *testing.T) {
	db := &fakeEventLookup{handler: func(dest interface{}, query string, args ...interface{}) error {
		t.Fatalf("DB should not be queried when QueryConfig.EventId is set")
		return nil
	}}
	q := makeQuery("explicit-event", "event-fp", "conv-1", "acct-1")
	got, hadAnchor := resolvePRTrackingEventId(testCtx(), db, q)
	assert.Equal(t, "explicit-event", got)
	assert.True(t, hadAnchor)
	assert.Len(t, db.calls, 0)
}

// TestResolvePRTrackingEventId_ViaSessionId reproduces the PR #249 scenario:
// QueryConfig.EventId empty but session_id is `event-<fingerprint>`; the
// helper must look up the latest event for that fingerprint on this account.
func TestResolvePRTrackingEventId_ViaSessionId(t *testing.T) {
	const (
		fp         = "1efd2b8779ab9b227f8db6e1da2497bf4681d3286ada8a504729fb55c92c2bf3"
		accountId  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		latestEvId = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)
	db := &fakeEventLookup{
		handler: func(dest interface{}, query string, args ...interface{}) error {
			assert.Contains(t, query, "fingerprint = $1")
			assert.Equal(t, fp, args[0])
			*(dest.(*string)) = latestEvId
			return nil
		},
	}
	q := makeQuery("", "event-"+fp, "conv-1", accountId)
	got, hadAnchor := resolvePRTrackingEventId(testCtx(), db, q)
	assert.Equal(t, latestEvId, got)
	assert.True(t, hadAnchor)
}

// TestResolvePRTrackingEventId_ViaConversationLookup covers the belt-and-braces
// path: SessionId is unset on the request but we can derive it from the
// llm_conversations row via ConversationId, then extract the fingerprint.
func TestResolvePRTrackingEventId_ViaConversationLookup(t *testing.T) {
	const (
		convId     = "cccccccc-cccc-cccc-cccc-cccccccccccc"
		fp         = "1efd2b8779ab9b227f8db6e1da2497bf4681d3286ada8a504729fb55c92c2bf3"
		accountId  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		latestEvId = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)
	callIdx := 0
	db := &fakeEventLookup{
		handler: func(dest interface{}, query string, args ...interface{}) error {
			callIdx++
			switch callIdx {
			case 1:
				// First call: session lookup with empty sessionId should
				// NOT happen — resolver short-circuits. The first call we
				// see is the llm_conversations session_id fetch.
				assert.Contains(t, query, "FROM llm_conversations")
				assert.Equal(t, convId, args[0])
				*(dest.(*string)) = "event-" + fp
				return nil
			case 2:
				assert.Contains(t, query, "fingerprint = $1")
				assert.Equal(t, fp, args[0])
				assert.Equal(t, accountId, args[1])
				*(dest.(*string)) = latestEvId
				return nil
			}
			t.Fatalf("unexpected extra DB call #%d: %s", callIdx, query)
			return nil
		},
	}
	q := makeQuery("", "", convId, accountId)
	got, hadAnchor := resolvePRTrackingEventId(testCtx(), db, q)
	assert.Equal(t, latestEvId, got)
	assert.True(t, hadAnchor)
	assert.Equal(t, 2, callIdx, "should make exactly 2 calls: session lookup then event lookup")
}

// TestResolvePRTrackingEventId_NoFallbackToConversationId locks in the core
// invariant: when the request *had* an event anchor (session is event-<fp> or
// conv's session is event-<fp>) but the event lookup returned no row, the
// helper MUST return ("", true). Callers see hadEventAnchor=true and skip the
// insert rather than mislink. Writing the conversation UUID would re-introduce
// the PR #249 bug.
func TestResolvePRTrackingEventId_NoFallbackWhenAnchorPresent(t *testing.T) {
	const (
		convId = "cccccccc-cccc-cccc-cccc-cccccccccccc"
		fp     = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	)
	callIdx := 0
	db := &fakeEventLookup{
		handler: func(dest interface{}, query string, args ...interface{}) error {
			callIdx++
			if callIdx == 1 {
				// llm_conversations lookup returns an event-<fp> session
				assert.Contains(t, query, "FROM llm_conversations")
				*(dest.(*string)) = "event-" + fp
				return nil
			}
			// fingerprint lookup returns no row
			return sql.ErrNoRows
		},
	}
	q := makeQuery("", "", convId, "acct-1")
	got, hadAnchor := resolvePRTrackingEventId(testCtx(), db, q)
	assert.Equal(t, "", got, "must NOT fall back to conversation id when anchor present")
	assert.True(t, hadAnchor, "anchor was present (event-<fp> session); caller must skip insert")
	assert.NotEqual(t, convId, got)
}

// TestResolvePRTrackingEventId_NoAnchorReturnsFalseFlag covers the Slack /
// plain-conversation flow: session_id is a Slack channel-id+ts, conversation's
// session_id is also non-event. There is no event anchor at all, so the helper
// returns ("", false) and the caller is free to use conversation_id as the
// resolution row's anchor.
func TestResolvePRTrackingEventId_NoAnchorReturnsFalseFlag(t *testing.T) {
	const convId = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	db := &fakeEventLookup{
		handler: func(dest interface{}, query string, args ...interface{}) error {
			if !assert.Contains(t, query, "FROM llm_conversations") {
				return errors.New("unexpected query")
			}
			// Slack-shaped session id: channel-id + slack message ts
			*(dest.(*string)) = "C05JKEDPJKH-1777401582.003819"
			return nil
		},
	}
	q := makeQuery("", "C05JKEDPJKH-1777401582.003819", convId, "acct-1")
	got, hadAnchor := resolvePRTrackingEventId(testCtx(), db, q)
	assert.Equal(t, "", got)
	assert.False(t, hadAnchor, "no event-<fp> anywhere → caller may fall back to conversation id")
}

// TestResolvePRTrackingEventId_SessionLookupFailsThenConvLookupSucceeds covers
// the case where SessionId is set but doesn't match an event (e.g. it's a
// chat session not anchored to an event), and the conversation row holds a
// valid event-<fingerprint>.
func TestResolvePRTrackingEventId_SessionLookupFailsThenConvLookupSucceeds(t *testing.T) {
	const (
		convId     = "d547b312-...."
		fp         = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
		latestEvId = "ev-123"
	)
	callIdx := 0
	db := &fakeEventLookup{
		handler: func(dest interface{}, query string, args ...interface{}) error {
			callIdx++
			switch callIdx {
			case 1:
				// SessionId "chat-xyz" is not event-<fingerprint>; this hits
				// lookupEventIdBySessionId which short-circuits before DB.
				// Actually we expect callIdx 1 to be the llm_conversations
				// lookup because SessionId is "chat-xyz" (bad prefix).
				assert.Contains(t, query, "FROM llm_conversations")
				*(dest.(*string)) = "event-" + fp
				return nil
			case 2:
				*(dest.(*string)) = latestEvId
				return nil
			}
			return errors.New("unexpected extra call")
		},
	}
	q := makeQuery("", "chat-xyz", convId, "acct-1")
	got, hadAnchor := resolvePRTrackingEventId(testCtx(), db, q)
	assert.Equal(t, latestEvId, got)
	assert.True(t, hadAnchor)
}
