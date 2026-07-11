package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

func TestNoOpTerminalAnswer_DetectsAndExplains(t *testing.T) {
	resp := `{
		"execution_status": "no_op",
		"pr_creation_status": "skipped",
		"pr_creation_reason": "The anomaly indexes are already applied by an existing migration.",
		"description": "Indexes present in V760."
	}`

	answer, isNoOp := noOpTerminalAnswer(resp)

	assert.True(t, isNoOp, "execution_status=no_op must be detected as a terminal no-op")
	assert.Contains(t, answer, "No pull request was created")
	// The explanatory answer must carry the agent's actual reasoning, not be a bare skip.
	assert.Contains(t, answer, "already applied by an existing migration")
}

func TestNoOpTerminalAnswer_SuccessWithPRIsNotNoOp(t *testing.T) {
	resp := `{"execution_status":"success","automated_fix_pr_info":{"url":"https://example/pr/1"}}`

	_, isNoOp := noOpTerminalAnswer(resp)

	assert.False(t, isNoOp)
}

func TestNoOpTerminalAnswer_InvalidJSONIsNotNoOp(t *testing.T) {
	_, isNoOp := noOpTerminalAnswer("not json")
	assert.False(t, isNoOp)
}

func TestHandleAnalysisResult_NoOpCachesTerminalAnswer(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	conv, msg := "conv-noop-test", "msg-noop-test"
	guardKey := conv + ":" + msg
	_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey)

	resp := `{"execution_status":"no_op","pr_creation_reason":"Already present on main."}`

	final := handleAnalysisResult(ctx, conv, msg, resp)

	// First call surfaces the explanatory terminal answer (not the raw JSON).
	assert.Contains(t, final, "No pull request was created")
	assert.Contains(t, final, "Already present on main.")

	// And it caches a NOOP-prefixed guard so a same-message re-dispatch replays it.
	cached, ok := common.CacheGet(codeAgentFailuresCacheNS, guardKey)
	assert.True(t, ok, "no-op must store a guard entry")
	replayed, isNoOp := strings.CutPrefix(string(cached), noopGuardPrefix)
	assert.True(t, isNoOp, "guard entry must carry the NOOP prefix")
	assert.Equal(t, final, replayed, "replayed answer must equal the first terminal answer")

	_ = common.CacheDelete(codeAgentFailuresCacheNS, guardKey)
}

func TestHandleAnalysisResult_NoOpWithoutIdsDoesNotCache(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	_ = common.CacheDelete(codeAgentFailuresCacheNS, ":")

	resp := `{"execution_status":"no_op","pr_creation_reason":"Already present."}`
	final := handleAnalysisResult(ctx, "", "", resp)

	// The terminal answer is still surfaced...
	assert.Contains(t, final, "No pull request was created")
	// ...but nothing is cached under a collapsed ":" key (cross-session safety).
	_, ok := common.CacheGet(codeAgentFailuresCacheNS, ":")
	assert.False(t, ok, "must not cache under a collapsed empty guard key")
}

func TestHandleAnalysisResult_GenuineSuccessClearsGuardAndPassesThrough(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	conv, msg := "conv-success-test", "msg-success-test"
	guardKey := conv + ":" + msg
	// Seed a stale guard to prove a real success clears it.
	_ = common.CacheSet(codeAgentFailuresCacheNS, guardKey, []byte("stale"))

	resp := `{"execution_status":"success","automated_fix_pr_info":{"url":"https://example/pr/2"}}`

	final := handleAnalysisResult(ctx, conv, msg, resp)

	assert.Equal(t, resp, final, "a normal success is passed through unchanged")
	_, ok := common.CacheGet(codeAgentFailuresCacheNS, guardKey)
	assert.False(t, ok, "a genuine success must clear any prior guard")
}
