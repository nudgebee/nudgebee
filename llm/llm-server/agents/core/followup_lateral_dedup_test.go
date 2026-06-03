package core

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lateral sibling dedup for tool_config followups (#30515).
//
// When parallel sub-agent instances each independently raise the SAME
// tool_config followup (their AgentId UUIDs differ, so same-agent dedup
// misses), GenerateFollowup must find any active sibling followup keyed by
// (conversation_id, message_id, toolName), bind the current sibling to it,
// and return its id WITHOUT inserting a new row.

// ─── DAO-level test (sqlmock) ────────────────────────────────────────────────

// TestFindActiveSiblingToolConfigFollowup_QueryShape asserts the SQL exactly
// filters by (conversation_id, message_id, toolName=$3, followupType='tool_config',
// non-terminal status). This is the precise contract used by GenerateFollowup
// to detect "another sibling already raised this followup, reuse it" — a
// regression in any of the WHERE clauses would re-introduce the bug.
func TestFindActiveSiblingToolConfigFollowup_QueryShape(t *testing.T) {
	cases := []struct {
		name         string
		mockRows     *sqlmock.Rows
		expectId     uuid.UUID
		expectErrNil bool
	}{
		{
			name: "match returns the existing followup id",
			mockRows: sqlmock.NewRows([]string{"id"}).
				AddRow("99999999-9999-9999-9999-999999999999"),
			expectId:     uuid.MustParse("99999999-9999-9999-9999-999999999999"),
			expectErrNil: true,
		},
		{
			name:         "no match returns uuid.Nil with nil error (sql.ErrNoRows swallowed)",
			mockRows:     sqlmock.NewRows([]string{"id"}),
			expectId:     uuid.Nil,
			expectErrNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

			mock.ExpectQuery(`SELECT id FROM llm_conversation_messages`).
				WithArgs("acct-1", "conv-1", "msg-1", "aws_execute").
				WillReturnRows(tc.mockRows)

			gotId, gotErr := dao.FindActiveSiblingToolConfigFollowup("acct-1", "conv-1", "msg-1", "aws_execute")
			if tc.expectErrNil {
				require.NoError(t, gotErr)
			}
			assert.Equal(t, tc.expectId, gotId)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// ─── End-to-end reproduction test (stateful in-memory DAO) ────────────────────

// reproductionDao is a stateful fake DAO that simulates the database side of
// GenerateFollowup. It tracks "inserted" followups and serves
// FindActiveSiblingToolConfigFollowup based on prior inserts.
type reproductionDao struct {
	IConversationDao

	mu sync.Mutex

	// state
	storedFollowupId uuid.UUID         // first sibling's "inserted" message id
	saveCallCount    int               // how many times SaveConversationMessage was hit
	bindCallCount    int               // how many times an agent was bound to a followup id
	boundAgents      map[string]string // agentId → followupMessageId

	// findDelay forces the lookup-then-create race window open in concurrent
	// tests: after reading storedFollowupId, FindActiveSibling sleeps this
	// duration before returning. Goroutines fire their lookups while the
	// first sibling hasn't saved yet — so without the GenerateFollowup
	// mutex, all of them see Nil and all race into SaveConversationMessage.
	// Zero (default) keeps the sequential tests behaving normally.
	findDelay time.Duration
}

func newReproductionDao() *reproductionDao {
	return &reproductionDao{boundAgents: map[string]string{}}
}

// First call returns Nil (no sibling yet); subsequent calls return the
// remembered id (simulating "the first sibling already inserted").
func (d *reproductionDao) FindActiveSiblingToolConfigFollowup(accountId, conversationId, userMessageId, toolName string) (uuid.UUID, error) {
	d.mu.Lock()
	id := d.storedFollowupId
	d.mu.Unlock()
	if d.findDelay > 0 {
		time.Sleep(d.findDelay)
	}
	return id, nil
}

func (d *reproductionDao) ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error) {
	return nil, nil // empty → same-agent dedup branch skipped on the create path
}

func (d *reproductionDao) SaveConversationMessage(id, conversationId, accountId, userId string, role MessageRole, messageType MessageType, message, response, agentName string, agentId uuid.UUID, messageConfig any, messageContext, queryConfig, evalMetrics string) (uuid.UUID, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.saveCallCount++
	// Remember the id we "inserted" so the next FindActiveSibling call returns it.
	newId := uuid.MustParse(id)
	d.storedFollowupId = newId
	return newId, nil
}

func (d *reproductionDao) UpdateConversationMessage(messageId, response string, status ConversationStatus) error {
	return nil
}

func (d *reproductionDao) UpdateConversationAgentWithFollowup(agentId, followupMessageId string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bindCallCount++
	d.boundAgents[agentId] = followupMessageId
	return nil
}

// Returning empty parent terminates the ancestor walk inside GenerateFollowup.
func (d *reproductionDao) GetConversationAgentParentAgentIdAndPreviousState(agentId string) (string, string) {
	return "", ""
}

func newReproTestCtx() *security.RequestContext {
	return security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForSuperAdmin(),
		slog.Default(), nil, nil,
	)
}

// TestGenerateFollowup_LateralToolConfigDedup_3SiblingsCollapseToOne is the
// actual bug reproduction: three parallel sub-agent instances (distinct
// AgentIds) under the same user message_id, each calling GenerateFollowup for
// the same toolName. Before the fix, all 3 inserted separate rows → UI saw
// 3 cards. After the fix:
//   - Only the FIRST call inserts a message.
//   - Subsequent calls return that first id and bind their own agent to it.
//   - All 3 agents are bound to the SAME followup_message_id.
func TestGenerateFollowup_LateralToolConfigDedup_3SiblingsCollapseToOne(t *testing.T) {
	dao := newReproductionDao()
	original := GetConversationDao()
	SetConversationDao(dao)
	defer SetConversationDao(original)

	agentA := uuid.New()
	agentB := uuid.New()
	agentC := uuid.New()

	mkReq := func(agentId uuid.UUID, agentName string) (NBAgentRequest, FollowupRequest) {
		return NBAgentRequest{
				MessageId:      "msg-1",
				ConversationId: "conv-1",
				AccountId:      "acct-1",
				UserId:         "user-1",
				AgentId:        agentId.String(),
			}, FollowupRequest{
				Question:        "I have found multiple configurations for the tool aws_execute, please select the one you are looking for:",
				FollowupType:    FollowupTypeToolConfig,
				ToolName:        "aws_execute",
				FollowupOptions: []string{"aws-prod", "aws-dev"},
				AgentName:       agentName,
				AgentId:         agentId,
			}
	}

	// Sibling A: no existing sibling → falls through to create path, inserts.
	reqA, fA := mkReq(agentA, "aws_observability")
	idA, errA := GenerateFollowup(newReproTestCtx(), reqA, fA)
	require.NoError(t, errA)
	assert.NotEqual(t, uuid.Nil, idA, "sibling A must get a real followup id")
	assert.Equal(t, 1, dao.saveCallCount, "sibling A is the only one to insert")

	// Sibling B: existing sibling now visible → lateral dedup short-circuits.
	reqB, fB := mkReq(agentB, "aws_observability")
	idB, errB := GenerateFollowup(newReproTestCtx(), reqB, fB)
	require.NoError(t, errB)
	assert.Equal(t, idA, idB, "sibling B returns the same id as sibling A")
	assert.Equal(t, 1, dao.saveCallCount, "sibling B must NOT insert")

	// Sibling C: same.
	reqC, fC := mkReq(agentC, "aws")
	idC, errC := GenerateFollowup(newReproTestCtx(), reqC, fC)
	require.NoError(t, errC)
	assert.Equal(t, idA, idC, "sibling C returns the same id as sibling A")
	assert.Equal(t, 1, dao.saveCallCount, "sibling C must NOT insert")

	// All three agents must be bound to the same followup_message_id.
	assert.Equal(t, 3, len(dao.boundAgents),
		"all three sibling agents must be bound to a followup")
	assert.Equal(t, idA.String(), dao.boundAgents[agentA.String()])
	assert.Equal(t, idA.String(), dao.boundAgents[agentB.String()])
	assert.Equal(t, idA.String(), dao.boundAgents[agentC.String()])
}

// Scope test: ask_clarification (single_select) followups must NOT be
// collapsed. Their options can legitimately differ between sibling sub-agents
// (e.g., two ticket sub-agents asking for different field values), so
// dedupping them would lose user intent.
func TestGenerateFollowup_LateralToolConfigDedup_SkippedForSelectFollowup(t *testing.T) {
	dao := newReproductionDao()
	// Pre-populate so the lateral dedup WOULD match if the type-gate weren't there.
	dao.storedFollowupId = uuid.New()
	original := GetConversationDao()
	SetConversationDao(dao)
	defer SetConversationDao(original)

	_, err := GenerateFollowup(
		newReproTestCtx(),
		NBAgentRequest{
			MessageId:      "msg-1",
			ConversationId: "conv-1",
			AccountId:      "acct-1",
			UserId:         "user-1",
			AgentId:        uuid.New().String(),
		},
		FollowupRequest{
			Question:        "which channel?",
			FollowupType:    FollowupTypeSingleSelect,
			ToolName:        "ask_clarification",
			FollowupOptions: []string{"#alerts", "#engineering"},
			AgentName:       "tickets",
			AgentId:         uuid.New(),
		},
	)
	require.NoError(t, err)

	// The followup was inserted (1 save) — proving the lateral dedup did NOT
	// short-circuit it just because a tool_config row happened to exist.
	assert.Equal(t, 1, dao.saveCallCount,
		"single_select followup must go through the create path, not lateral dedup")
}

// TestGenerateFollowup_LateralToolConfigDedup_ConcurrentSiblings is the actual
// regression net for the toolConfigCreationLocks mutex. The sequential 3-sibling
// test passes whether or not the mutex exists, because each call observes the
// previous one's storedFollowupId before returning. To exercise the mutex,
// fire all three siblings as goroutines AND set findDelay so their lookups
// overlap in time — without the mutex, all three lookups return Nil before
// any save commits, and all three race into SaveConversationMessage producing
// 3 rows (the original prod bug). With the mutex, GenerateFollowup serializes
// the create-flow so sibling A saves first and B/C see A's id on their lookup.
func TestGenerateFollowup_LateralToolConfigDedup_ConcurrentSiblings(t *testing.T) {
	dao := newReproductionDao()
	// 50ms is long enough that all three goroutines enter the lookup phase
	// before any save returns, but short enough to keep the test fast.
	dao.findDelay = 50 * time.Millisecond

	original := GetConversationDao()
	SetConversationDao(dao)
	defer SetConversationDao(original)

	mkReq := func(agentId uuid.UUID) (NBAgentRequest, FollowupRequest) {
		return NBAgentRequest{
				MessageId:      "msg-1",
				ConversationId: "conv-1",
				AccountId:      "acct-1",
				UserId:         "user-1",
				AgentId:        agentId.String(),
			}, FollowupRequest{
				Question:        "Select tool config:",
				FollowupType:    FollowupTypeToolConfig,
				ToolName:        "aws_execute",
				FollowupOptions: []string{"aws-prod", "aws-dev"},
				AgentName:       "aws_observability",
				AgentId:         agentId,
			}
	}

	agents := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	results := make([]uuid.UUID, len(agents))
	errs := make([]error, len(agents))

	var wg sync.WaitGroup
	for i, ag := range agents {
		wg.Add(1)
		go func(idx int, agentId uuid.UUID) {
			defer wg.Done()
			req, fr := mkReq(agentId)
			id, err := GenerateFollowup(newReproTestCtx(), req, fr)
			results[idx] = id
			errs[idx] = err
		}(i, ag)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "sibling %d errored", i)
	}

	// Exactly ONE save — the mutex serialized the create-flow so only the
	// first sibling to acquire the (conv, msg, tool) lock inserts; the others
	// see its row on their lookup and bind to it.
	assert.Equal(t, 1, dao.saveCallCount,
		"concurrent siblings must collapse to one SaveConversationMessage — mutex regression")

	// All three siblings receive the same followup id.
	assert.Equal(t, results[0], results[1], "sibling 0 and 1 must share followup id")
	assert.Equal(t, results[0], results[2], "sibling 0 and 2 must share followup id")
	assert.NotEqual(t, uuid.Nil, results[0], "followup id must be non-nil")

	// All three agents bound to that same followup id.
	assert.Equal(t, 3, len(dao.boundAgents), "all three agents bound")
	for _, ag := range agents {
		assert.Equal(t, results[0].String(), dao.boundAgents[ag.String()],
			"agent %s bound to wrong followup", ag.String())
	}
}
