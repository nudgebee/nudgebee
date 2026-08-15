package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCodeStepMetadata(t *testing.T) {
	// Valid Go duration string → {"duration_ns": N}.
	b := buildCodeStepMetadata(map[string]any{"duration": "2.5ms"})
	require.NotNil(t, b)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.EqualValues(t, 2500000, m["duration_ns"])

	// Missing / empty / unparseable / non-positive → nil (no metadata).
	assert.Nil(t, buildCodeStepMetadata(map[string]any{}))
	assert.Nil(t, buildCodeStepMetadata(map[string]any{"duration": ""}))
	assert.Nil(t, buildCodeStepMetadata(map[string]any{"duration": "not-a-duration"}))
	assert.Nil(t, buildCodeStepMetadata(map[string]any{"duration": "0s"}))
}

// savedToolCall captures the args of a SaveConversationToolCall invocation.
type savedToolCall struct {
	toolId   string
	toolName string
	toolArgs string
	thought  string
	result   string
	status   toolcore.NBToolResponseStatus
}

// fakeStepsDao embeds IConversationDao (nil) and overrides only the one method
// persistCodeAnalysisSteps calls. Any other method would panic — none are called.
type fakeStepsDao struct {
	core.IConversationDao
	mu    sync.Mutex
	calls []savedToolCall
}

func (f *fakeStepsDao) SaveConversationToolCall(conversationID, accountID, userID, messageId, agenId, toolId, toolName, toolArgs, thought, toolArgsSql, toolResult string, status toolcore.NBToolResponseStatus, toolType toolcore.NBToolType, childAgentId *string, references []toolcore.NBToolResponseReference, metadata []byte, memoryRefs []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, savedToolCall{
		toolId:   toolId,
		toolName: toolName,
		toolArgs: toolArgs,
		thought:  thought,
		result:   toolResult,
		status:   status,
	})
	return nil
}

func withFakeDao(t *testing.T) *fakeStepsDao {
	t.Helper()
	fake := &fakeStepsDao{}
	prev := core.GetConversationDao()
	core.SetConversationDao(fake)
	t.Cleanup(func() { core.SetConversationDao(prev) })
	return fake
}

// TestSanitizeCodeStepArgs covers the two persistence guarantees: planner
// working-memory keys are stripped, and the stored blob stays valid JSON under
// the size cap even when individual values are huge (a raw head-slice of the
// serialized JSON left an unparseable fragment the UI showed as an escaped dump).
func TestSanitizeCodeStepArgs(t *testing.T) {
	tests := []struct {
		name string
		in   any
	}{
		{"small map passes through", map[string]any{"file_path": "a.go", "purpose": "fix import"}},
		{"strips working memory", map[string]any{
			"__intention":   "submit",
			"_tool_outputs": map[string]any{"tool_call_file_view_step_10": strings.Repeat("x", 30000)},
			"answer":        "done",
		}},
		{"huge string field", map[string]any{
			"analysis": strings.Repeat("a", 40000),
			"answer":   strings.Repeat("b", 40000),
		}},
		{"huge non-string field", map[string]any{
			"citations": []any{map[string]any{"quote": strings.Repeat("c", 40000)}},
		}},
		{"many medium fields", func() map[string]any {
			m := map[string]any{}
			for i := 0; i < 20; i++ {
				m[fmt.Sprintf("field_%02d", i)] = strings.Repeat("z", 2000)
			}
			return m
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeCodeStepArgs(tt.in)
			assert.LessOrEqual(t, len(got), maxCodeStepFieldBytes)
			var parsed map[string]any
			require.NoError(t, json.Unmarshal([]byte(got), &parsed), "stored args must stay parseable JSON")
			_, hasOutputs := parsed["_tool_outputs"]
			_, hasIntention := parsed["__intention"]
			assert.False(t, hasOutputs, "_tool_outputs must be stripped")
			assert.False(t, hasIntention, "__intention must be stripped")
		})
	}

	// Non-map input falls back to plain truncation (no JSON guarantee to keep).
	assert.Equal(t, "plain text input", sanitizeCodeStepArgs("plain text input"))
	long := sanitizeCodeStepArgs(strings.Repeat("q", 20000))
	assert.Equal(t, maxCodeStepFieldBytes, len(long))
	assert.Equal(t, "", sanitizeCodeStepArgs(nil))
}

func TestMapCodeStepStatus(t *testing.T) {
	cases := map[string]toolcore.NBToolResponseStatus{
		"running":     toolcore.NBToolResponseStatusInProgress,
		"in_progress": toolcore.NBToolResponseStatusInProgress,
		"pending":     toolcore.NBToolResponseStatusInProgress,
		"error":       toolcore.NBToolResponseStatusError,
		"failed":      toolcore.NBToolResponseStatusError,
		"failure":     toolcore.NBToolResponseStatusError,
		"success":     toolcore.NBToolResponseStatusSuccess,
		"completed":   toolcore.NBToolResponseStatusSuccess,
		"":            toolcore.NBToolResponseStatusSuccess,
		"SUCCESS":     toolcore.NBToolResponseStatusSuccess,
		" running ":   toolcore.NBToolResponseStatusInProgress,
	}
	for in, want := range cases {
		assert.Equalf(t, want, mapCodeStepStatus(in), "status %q", in)
	}
}

func TestPersistCodeAnalysisSteps_NoOp(t *testing.T) {
	fake := withFakeDao(t)
	ctx := security.NewRequestContextForSuperAdmin()

	step := []any{map[string]any{"tool_name": "repo_clone", "status": "success"}}

	// No agent id → no-op.
	persistCodeAnalysisSteps(ctx, core.NBAgentRequest{AccountId: "acc-1"}, step, map[string]string{})
	assert.Empty(t, fake.calls, "must not persist without an agent id")

	// No account id (tenant scope) → no-op, even with an agent id.
	persistCodeAnalysisSteps(ctx, core.NBAgentRequest{AgentId: "agent-1"}, step, map[string]string{})
	assert.Empty(t, fake.calls, "must not persist without a tenant/account id")

	// Empty invocations → no-op.
	persistCodeAnalysisSteps(ctx, core.NBAgentRequest{AccountId: "acc-1", AgentId: "agent-1"}, nil, map[string]string{})
	assert.Empty(t, fake.calls, "must not persist with no invocations")
}

func TestPersistCodeAnalysisSteps_RunningThenSuccess(t *testing.T) {
	fake := withFakeDao(t)
	ctx := security.NewRequestContextForSuperAdmin()
	query := core.NBAgentRequest{
		AgentId:        "agent-1",
		ConversationId: "conv-1",
		MessageId:      "msg-1",
		AccountId:      "acc-1",
		UserId:         "user-1",
	}
	persisted := map[string]string{}

	// Poll 1: step is running (no output yet). The input carries planner working
	// memory, which must be stripped from the persisted row (regression guard
	// that the persist path is wired through sanitizeCodeStepArgs).
	persistCodeAnalysisSteps(ctx, query, []any{
		map[string]any{
			"tool_name":   "repo_clone",
			"status":      "running",
			"step_number": float64(1),
			"thought":     "Need the source to investigate",
			"input": map[string]any{
				"url":           "https://example/repo",
				"_tool_outputs": map[string]any{"tool_call_rg_step_1": "matched"},
				"__intention":   "clone it",
			},
		},
	}, persisted)

	require.Len(t, fake.calls, 1)
	assert.Equal(t, "code-001-repo_clone", fake.calls[0].toolId)
	assert.Equal(t, toolcore.NBToolResponseStatusInProgress, fake.calls[0].status)
	assert.Equal(t, "Need the source to investigate", fake.calls[0].thought)
	assert.Contains(t, fake.calls[0].toolArgs, "example/repo")
	assert.NotContains(t, fake.calls[0].toolArgs, "_tool_outputs", "working memory must not be persisted")
	assert.NotContains(t, fake.calls[0].toolArgs, "__intention")

	// Poll 2: same step now completed → one more write, same stable tool_id.
	persistCodeAnalysisSteps(ctx, query, []any{
		map[string]any{
			"tool_name":   "repo_clone",
			"status":      "success",
			"step_number": float64(1),
			"output":      "cloned 1234 files",
		},
	}, persisted)

	require.Len(t, fake.calls, 2)
	assert.Equal(t, "code-001-repo_clone", fake.calls[1].toolId)
	assert.Equal(t, toolcore.NBToolResponseStatusSuccess, fake.calls[1].status)
	assert.Equal(t, "cloned 1234 files", fake.calls[1].result)

	// Poll 3: terminal step is re-sent → deduped, no new write.
	persistCodeAnalysisSteps(ctx, query, []any{
		map[string]any{"tool_name": "repo_clone", "status": "success", "step_number": float64(1)},
	}, persisted)
	assert.Len(t, fake.calls, 2, "terminal step must not be rewritten")
}

func TestPersistCodeAnalysisSteps_DistinctStepsSameTool(t *testing.T) {
	fake := withFakeDao(t)
	ctx := security.NewRequestContextForSuperAdmin()
	query := core.NBAgentRequest{AccountId: "acc-1", AgentId: "agent-1", ConversationId: "c", MessageId: "m"}

	// Two file_view steps must get distinct stable tool_ids via step_number.
	persistCodeAnalysisSteps(ctx, query, []any{
		map[string]any{"tool_name": "file_view", "status": "success", "step_number": float64(2)},
		map[string]any{"tool_name": "file_view", "status": "success", "step_number": float64(3)},
	}, map[string]string{})

	require.Len(t, fake.calls, 2)
	assert.Equal(t, "code-002-file_view", fake.calls[0].toolId)
	assert.Equal(t, "code-003-file_view", fake.calls[1].toolId)
}
