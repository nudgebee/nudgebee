package core

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

type claimCaptureDAO struct {
	IConversationDao
	mu        sync.Mutex
	rows      []ClaimCritiqueAuditRecord
	writes    int
	failWrite int
	usage     chan *TokenUsageRecord
}

func (d *claimCaptureDAO) SaveClaimCritiqueAudit(ctx context.Context, r *ClaimCritiqueAuditRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.writes++
	if d.writes == d.failWrite {
		return errors.New("storage unavailable")
	}
	data, _ := json.Marshal(r)
	var snapshot ClaimCritiqueAuditRecord
	_ = json.Unmarshal(data, &snapshot)
	d.rows = append(d.rows, snapshot)
	return nil
}
func (d *claimCaptureDAO) GetConversation(_ string) (Conversation, error)   { return Conversation{}, nil }
func (d *claimCaptureDAO) GetAgentNameFromAgentId(_ string) (string, error) { return "claim_test", nil }
func (d *claimCaptureDAO) GetConversationCost(_, _ string, _, _, _, _, _ int, _ string) (float64, error) {
	return 0, errors.New("unpriced test model")
}
func (d *claimCaptureDAO) InsertTokenUsage(r *TokenUsageRecord) error {
	if d.usage != nil {
		d.usage <- r
	}
	return nil
}

// pinClaimAuditOnly keeps a test on the audit-only path it was written for:
// the default mode (continuation) also turns on guidance and continuation calls.
func pinClaimAuditOnly(t *testing.T) {
	t.Helper()
	guidance, continuation := config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled
	t.Cleanup(func() {
		config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled = guidance, continuation
	})
	config.Config.ClaimCritiqueActionableFeedbackEnabled, config.Config.CritiqueContinuationEnabled = false, false
}

func installClaimCaptureDAO(t *testing.T, d *claimCaptureDAO) {
	t.Helper()
	previous := GetConversationDao()
	d.IConversationDao = previous
	conversationDaoMutex.Lock()
	SetConversationDao(d)
	conversationDaoMutex.Unlock()
	t.Cleanup(func() { conversationDaoMutex.Lock(); defer conversationDaoMutex.Unlock(); SetConversationDao(previous) })
}

func TestClaimCapturePersistsWithTracingDisabled(t *testing.T) {
	pinClaimAuditOnly(t)
	previous, tracing := config.Config.ClaimCritiqueFeedbackEnabled, config.Config.LlmTraceEnabled
	config.Config.ClaimCritiqueFeedbackEnabled, config.Config.LlmTraceEnabled = true, false
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled, config.Config.LlmTraceEnabled = previous, tracing })
	for _, output := range []string{validClaimAudit, `{"claims":[`} {
		t.Run(output, func(t *testing.T) {
			dao := &claimCaptureDAO{usage: make(chan *TokenUsageRecord, 4)}
			installClaimCaptureDAO(t, dao)
			fake := &fakeLLMModel{turns: []fakeTurn{{content: output}}}
			withFakeLLMModel(t, fake)
			request := NBAgentRequest{AccountId: uuid.NewString(), ConversationId: uuid.NewString(), MessageId: uuid.NewString(), AgentId: uuid.NewString()}
			runClaimCritiqueShadow(llmOverrideContext(), request, "react4", "test_agent", "accept", "baseline feedback", "why", "answer", "notes",
				[]NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}, false, nil)
			require.Equal(t, 1, fake.callCount())
			require.Len(t, dao.rows, 2)
			initial, final := dao.rows[0], dao.rows[1]
			require.Equal(t, "started", initial.Status)
			require.False(t, initial.Completed)
			require.Equal(t, initial.ID, final.ID)
			require.True(t, final.Completed)
			require.Equal(t, "baseline feedback", final.BaselineFeedback)
			require.Equal(t, "test_agent", final.AgentName)
			require.Equal(t, "why", final.Input)
			require.Equal(t, "answer", final.Answer)
			require.Contains(t, string(final.Detail.InputPacket), "CPU high")
			require.Equal(t, claimHash([]byte(final.Detail.Prompt)), final.Detail.PromptSHA256)
			require.NotEmpty(t, final.Detail.RawResponse)
			if output == validClaimAudit {
				require.Equal(t, "refine", final.Decision)
				require.NotNil(t, final.Detail.Audit)
			} else {
				require.Empty(t, final.Decision)
				require.Nil(t, final.Detail.Audit)
			}
			select {
			case usage := <-dao.usage:
				require.Equal(t, final.TokenUsageID, usage.ID)
				require.Equal(t, request.AgentId, *usage.AgentID)
				require.Nil(t, usage.PromptMessages, "global tracing remains off")
			case <-time.After(3 * time.Second):
				t.Fatal("missing linked token usage")
			}
		})
	}
}

func TestClaimCaptureStorageFailureSkipsInference(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{failWrite: 1}
	installClaimCaptureDAO(t, dao)
	fake := &fakeLLMModel{}
	withFakeLLMModel(t, fake)
	runClaimCritiqueShadow(llmOverrideContext(), NBAgentRequest{}, "react3", "test_agent", "accept", "", "why", "answer", "", nil, false, nil)
	require.Zero(t, fake.callCount())
	require.Empty(t, dao.rows)
}

func TestClaimCaptureOversizeAndCancellationRemainVisible(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{}
	installClaimCaptureDAO(t, dao)
	fake := &fakeLLMModel{}
	withFakeLLMModel(t, fake)
	base := llmOverrideContext()
	cancelled, cancel := context.WithCancel(base.GetContext())
	cancel()
	ctx := security.NewRequestContext(cancelled, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
	runClaimCritiqueShadow(ctx, NBAgentRequest{}, "react3", "test_agent", "accept", "", "why", strings.Repeat("x", claimCritiqueInputLimit), "", nil, false, nil)
	require.Zero(t, fake.callCount())
	require.Len(t, dao.rows, 2)
	require.Equal(t, "input_limit", dao.rows[1].Status)
	require.True(t, dao.rows[1].Completed)
	require.Empty(t, dao.rows[1].Detail.InputPacket)
	require.Greater(t, dao.rows[1].Detail.InputBytes, claimCritiqueInputLimit)
}

func TestClaimCaptureResponseBound(t *testing.T) {
	raw, truncated := captureClaimResponse(&llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: strings.Repeat("日", 128*1024)}}})
	require.True(t, truncated)
	require.LessOrEqual(t, len(raw), 128*1024)
}

func TestClaimCaptureDAOAndUsageID(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	record := &ClaimCritiqueAuditRecord{ID: uuid.NewString(), TokenUsageID: uuid.NewString(), Status: "started"}
	mock.ExpectExec("INSERT INTO llm_conversation_agent_critiques").WithArgs(record.ID, record.TokenUsageID, "", "", "", "", "", "", claimShadowCritiqueType, "skipped", "", claimMetadataArgument{status: "started"}).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, dao.SaveClaimCritiqueAudit(context.Background(), record))
	// A fallback audit runs under a new usage id; the completed row must point at it.
	record.Status, record.Decision, record.Completed, record.TokenUsageID = "ok", "refine", true, uuid.NewString()
	mock.ExpectExec(`INSERT INTO llm_conversation_agent_critiques[\s\S]*DO UPDATE SET token_usage_id=EXCLUDED\.token_usage_id`).WithArgs(record.ID, record.TokenUsageID, "", "", "", "", "", "", claimShadowCritiqueType, "refine", "", claimMetadataArgument{status: "ok", completed: true}).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, dao.SaveClaimCritiqueAudit(context.Background(), record))
	mock.ExpectExec("INSERT INTO llm_conversation_token_usage").WithArgs(
		nil, nil, nil, "", nil, nil, "", "", 0, 0, 0, 0, false, nil, 0, nil, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, record.TokenUsageID,
	).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, dao.InsertTokenUsage(&TokenUsageRecord{ID: record.TokenUsageID}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// A completed review without a verdict is stored apart from a review that never
// ran because the refinement budget was spent (#40236).
func TestClaimCaptureNoVerdictDecision(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	for _, c := range []struct{ status, want string }{
		{"generation_error", claimDecisionNoVerdict},
		{"input_limit", claimDecisionNoVerdict},
		{"budget_exhausted", "skipped"},
	} {
		record := &ClaimCritiqueAuditRecord{ID: uuid.NewString(), TokenUsageID: uuid.NewString(), Status: c.status, Completed: true}
		mock.ExpectExec("INSERT INTO llm_conversation_agent_critiques").WithArgs(record.ID, record.TokenUsageID, "", "", "", "", "", "", claimShadowCritiqueType, c.want, "", claimMetadataArgument{status: c.status, completed: true}).WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, dao.SaveClaimCritiqueAudit(context.Background(), record), c.status)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimCaptureFailureUsageID(t *testing.T) {
	dao := &claimCaptureDAO{usage: make(chan *TokenUsageRecord, 1)}
	installClaimCaptureDAO(t, dao)
	base := llmOverrideContext()
	id := uuid.NewString()
	goctx := context.WithValue(base.GetContext(), contextKeyPreserveInput, true)
	goctx = context.WithValue(goctx, contextKeyClaimUsageID, id)
	ctx := security.NewRequestContext(goctx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
	recordTokenUsageFailure(ctx, "", "", "", "claim_test", "test", "test", "", "", nil, errors.New("provider unavailable"))
	select {
	case usage := <-dao.usage:
		require.Equal(t, id, usage.ID)
		require.Equal(t, "failure", usage.RequestStatus)
	case <-time.After(3 * time.Second):
		t.Fatal("missing failure usage")
	}
	require.Empty(t, claimUsageID(base), "unrelated inference retains default IDs")
}

func TestClaimCaptureCompletionFailureLeavesStarted(t *testing.T) {
	pinClaimAuditOnly(t)
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = true
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	dao := &claimCaptureDAO{failWrite: 2, usage: make(chan *TokenUsageRecord, 1)}
	installClaimCaptureDAO(t, dao)
	fake := &fakeLLMModel{turns: []fakeTurn{{content: validClaimAudit}}}
	withFakeLLMModel(t, fake)
	runClaimCritiqueShadow(llmOverrideContext(), NBAgentRequest{}, "react3", "test_agent", "accept", "", "why", "answer", "",
		[]NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: "CPU high"}}, false, nil)
	require.Equal(t, 1, fake.callCount())
	require.Len(t, dao.rows, 1)
	require.Equal(t, "started", dao.rows[0].Status)
	select {
	case <-dao.usage:
	case <-time.After(3 * time.Second):
		t.Fatal("missing usage")
	}
}

// Check the actual JSONB argument, including unfinished-vs-completed semantics.
type claimMetadataArgument struct {
	status    string
	completed bool
}

func (a claimMetadataArgument) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var data map[string]any
	if json.Unmarshal([]byte(raw), &data) != nil {
		return false
	}
	completed, exists := data["completed_at"]
	return exists && data["status"] == a.status && (completed != nil) == a.completed
}
