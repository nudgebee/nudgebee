package egressfilter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// Integration-style tests that exercise the wrapper end-to-end against
// realistic langchaingo message shapes (multi-turn conversations, tool calls,
// system+human+ai+tool roles, multimodal-ish parts). These do NOT require any
// running provider — they use fakeModel from egressfilter_test.go to assert the
// egressfilter gates the call without altering payload semantics.

// realisticConversation builds a representative ReWOO-style multi-turn message
// slice (system prompt + history + tool round-trip + new query).
func realisticConversation(toolOutput string) []llms.MessageContent {
	return []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
			llms.TextContent{Text: "You are an SRE assistant. Investigate the following issue."},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "Pod foo-api in prod is restarting."},
		}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.ToolCall{
				ID:   "call_1",
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      "kubectl_describe_pod",
					Arguments: `{"pod":"foo-api","namespace":"prod"}`,
				},
			},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{
				ToolCallID: "call_1",
				Name:       "kubectl_describe_pod",
				Content:    toolOutput,
			},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "What should we do next?"},
		}},
	}
}

func TestIntegration_CleanConversationPassesThrough(t *testing.T) {
	inner := &fakeModel{respText: "Increase memory limit and re-roll."}
	wrapped := WrapModel(inner, "bedrock", "claude-sonnet-4", true, ModeEnforce)

	msgs := realisticConversation("Status: CrashLoopBackOff\nLast exit: 137 (OOMKilled)\nMemory: 511Mi/512Mi")
	resp, err := wrapped.GenerateContent(context.Background(), msgs)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, inner.callCount())
	// The wrapper must not mutate the messages it forwards.
	assert.Equal(t, msgs, inner.lastMessages)
}

func TestIntegration_SecretInToolResponseIsBlocked(t *testing.T) {
	// A common real attack surface: a kubectl command leaks a secret (e.g. a
	// kubeconfig or env var dump) into the tool response that then feeds
	// straight into the next LLM call.
	leakedToolOutput := `Pod env vars:
  AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
  AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`

	inner := &fakeModel{respText: "should never reach"}
	wrapped := WrapModel(inner, "bedrock", "claude-sonnet-4", true, ModeEnforce)

	_, err := wrapped.GenerateContent(context.Background(), realisticConversation(leakedToolOutput))
	require.Error(t, err)
	assert.Equal(t, 0, inner.callCount(), "provider must not be called when a tool response leaked a secret")

	se, ok := AsError(err)
	require.True(t, ok)
	assert.Contains(t, se.RuleIDs, "aws-access-key-id")
	// The user-facing message must NOT echo the leaked value.
	assert.NotContains(t, se.Error(), "AKIA")
	assert.NotContains(t, se.Error(), "wJalrXUtnFEMI")
}

func TestIntegration_SecretInSystemPromptIsBlocked(t *testing.T) {
	// Equally important: a runbook-derived system prompt may have been
	// authored with hardcoded credentials. Audit mode catches these without
	// blocking; enforce mode blocks.
	leakySystem := `Use this token to call the GitHub API: ghp_Abcdefghijklmnopqrstuvwxyz0123456789`

	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: leakySystem}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "List my repos."}}},
	}
	_, err := wrapped.GenerateContent(context.Background(), msgs)
	require.Error(t, err)
	assert.Equal(t, 0, inner.callCount())
	se, _ := AsError(err)
	assert.Contains(t, se.RuleIDs, "github-pat")
}

func TestIntegration_DetectModeNeverBlocksButRecords(t *testing.T) {
	leakySystem := `GH token: ghp_Abcdefghijklmnopqrstuvwxyz0123456789`
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)

	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: leakySystem}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "List my repos."}}},
	}
	resp, err := wrapped.GenerateContent(context.Background(), msgs)
	require.NoError(t, err, "audit mode must not return an error even on hits")
	require.NotNil(t, resp)
	assert.Equal(t, 1, inner.callCount(), "provider must still be called in audit mode")
}

func TestIntegration_DisabledFlagIsTrulyZeroOverhead(t *testing.T) {
	// When the flag is off, WrapModel must return the same pointer as the
	// inner model — guarantees no allocation, no scan, no metric emission.
	leaky := `key=ghp_Abcdefghijklmnopqrstuvwxyz0123456789`
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", false, ModeEnforce)
	assert.Same(t, inner, wrapped, "disabled egressfilter must return inner model as-is")

	resp, err := wrapped.GenerateContent(context.Background(),
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, leaky)},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, inner.callCount())
}

func TestIntegration_InnerErrorIsPropagatedUnwrapped(t *testing.T) {
	// When the egressfilter passes the call through cleanly, downstream errors
	// from the provider must surface unchanged. A egressfilter wrap should not
	// disguise upstream failures (rate-limit, network, etc.).
	rateLimit := errors.New("provider: 429 too many requests")
	inner := &fakeModel{returnOnError: rateLimit}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	_, err := wrapped.GenerateContent(context.Background(),
		realisticConversation("clean output"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, rateLimit), "non-egressfilter errors must propagate verbatim")
	assert.False(t, errors.Is(err, ErrSecretsBlocked), "must not impersonate a egressfilter block")
}

func TestIntegration_ModeFlipTakesEffectOnNewWrapper(t *testing.T) {
	// Simulates flipping LLM_SERVER_EGRESSFILTER_SECRETS_MODE from audit to
	// enforce by re-wrapping with the new mode (which is what a fresh cache
	// entry would do after InvalidateAllLLMClientCache).
	leaky := `key=AKIAIOSFODNN7EXAMPLE`
	msgs := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, leaky)}

	inner := &fakeModel{respText: "ok"}
	detectWrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)
	_, err := detectWrapped.GenerateContent(context.Background(), msgs)
	require.NoError(t, err)
	assert.Equal(t, 1, inner.callCount())

	enforceWrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)
	_, err = enforceWrapped.GenerateContent(context.Background(), msgs)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSecretsBlocked))
	assert.Equal(t, 1, inner.callCount(), "enforce mode must not call inner")
}

func TestIntegration_ConcurrentCallsAreRaceFree(t *testing.T) {
	// The wrapper must be safe to call from many goroutines simultaneously —
	// the cache in agents/core/llm_config.go shares a single wrapped instance
	// across the planner's parallel-execution worker pool.
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	const goroutines = 32
	const callsPerGoroutine = 25
	var wg sync.WaitGroup
	var blocked atomic.Int64
	var passed atomic.Int64

	for g := 0; g < goroutines; g++ {
		gid := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < callsPerGoroutine; i++ {
				var payload string
				if (gid+i)%5 == 0 {
					payload = fmt.Sprintf("worker %d iter %d: leak=AKIAIOSFODNN7EXAMPLE", gid, i)
				} else {
					payload = fmt.Sprintf("worker %d iter %d: clean diagnostic query", gid, i)
				}
				msgs := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, payload)}
				_, err := wrapped.GenerateContent(context.Background(), msgs)
				if errors.Is(err, ErrSecretsBlocked) {
					blocked.Add(1)
				} else if err == nil {
					passed.Add(1)
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	total := int64(goroutines * callsPerGoroutine)
	assert.Equal(t, total, blocked.Load()+passed.Load())
	assert.Greater(t, blocked.Load(), int64(0), "some calls must have been blocked")
	assert.Greater(t, passed.Load(), int64(0), "some calls must have passed through")
	// Each block must NOT have called the inner model. We can sanity-check
	// the count is consistent (allow for the test running concurrently with
	// fakeModel.calls being incremented non-atomically — so we use a relaxed
	// inequality).
	assert.LessOrEqual(t, int64(inner.callCount()), passed.Load(), "inner.callCount must not exceed pass-through count")
}

func TestIntegration_AuditIDsAreUniquePerBlock(t *testing.T) {
	// Each block must produce a fresh audit ID so operators can correlate
	// individual events.
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	seen := make(map[string]struct{})
	for i := 0; i < 20; i++ {
		msgs := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman,
			fmt.Sprintf("iter %d key=AKIAIOSFODNN7EXAMPLE", i))}
		_, err := wrapped.GenerateContent(context.Background(), msgs)
		require.Error(t, err)
		se, ok := AsError(err)
		require.True(t, ok)
		_, dup := seen[se.AuditID]
		assert.False(t, dup, "audit IDs must be unique across blocks (saw %s twice)", se.AuditID)
		seen[se.AuditID] = struct{}{}
	}
	assert.Len(t, seen, 20)
}

func TestIntegration_EmptyAndOnlyWhitespacePayloadsAreNoOp(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	cases := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: nil},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: ""}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "   \n\t   "}}},
	}
	for _, msg := range cases {
		_, err := wrapped.GenerateContent(context.Background(), []llms.MessageContent{msg})
		assert.NoError(t, err)
	}
	assert.Equal(t, len(cases), inner.callCount())
}

func TestIntegration_PointerTextContentIsHandled(t *testing.T) {
	// Some callers construct *llms.TextContent rather than the value form.
	// Both must be scanned.
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	msgs := []llms.MessageContent{{
		Role:  llms.ChatMessageTypeHuman,
		Parts: []llms.ContentPart{&llms.TextContent{Text: "key=ghp_Abcdefghijklmnopqrstuvwxyz0123456789"}},
	}}
	_, err := wrapped.GenerateContent(context.Background(), msgs)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSecretsBlocked))
}
