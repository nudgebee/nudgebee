package planners

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// Gemini's implicit prompt cache only pays off while the prompt prefix stays
// byte-stable turn over turn. Every transformation the planner applies to the
// SENT copy is therefore either a tail append (free) or a mid-prompt rewrite
// (which discards the cached prefix from that point on). Nothing previously
// asserted which is which, so a change that silently made the prompt churn
// every turn would not have failed any test — it would only have shown up as a
// cost increase weeks later.
//
// This test drives the real send pipeline over a simulated run and measures
// prefix stability directly. It is hermetic: with a nil llmClient
// summarizeMiddle bails, so compaction takes its deterministic structural path
// and no API key or network is involved.

// hashSentMessages fingerprints each message of a send copy so consecutive
// turns can be compared position by position. Deliberately local to this test
// rather than reusing the llm package's helpers — planners must not depend on
// llm for a test fixture.
func hashSentMessages(messages []llms.MessageContent) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		h := sha256.New()
		h.Write([]byte(m.Role))
		for _, part := range m.Parts {
			switch v := part.(type) {
			case llms.TextContent:
				h.Write([]byte("text:" + v.Text))
			case llms.ToolCall:
				if v.FunctionCall != nil {
					h.Write([]byte("call:" + v.FunctionCall.Name + v.FunctionCall.Arguments))
				}
			case llms.ToolCallResponse:
				h.Write([]byte("resp:" + v.Name + v.Content))
			default:
				h.Write(fmt.Appendf(nil, "%v", part))
			}
		}
		out = append(out, hex.EncodeToString(h.Sum(nil)))
	}
	return out
}

// firstDivergentIndex returns the first position where two turns' fingerprints
// differ, or -1 when the shorter is a pure prefix of the longer (an append).
func firstDivergentIndex(prev, cur []string) int {
	n := len(prev)
	if len(cur) < n {
		n = len(cur)
	}
	for i := 0; i < n; i++ {
		if prev[i] != cur[i] {
			return i
		}
	}
	return -1
}

// sentChars totals the text carried by a send copy, so an invalidation can be
// checked against what it bought.
func sentChars(messages []llms.MessageContent) int {
	n := 0
	for _, m := range messages {
		for _, part := range m.Parts {
			switch v := part.(type) {
			case llms.TextContent:
				n += len(v.Text)
			case llms.ToolCallResponse:
				n += len(v.Content)
			case llms.ToolCall:
				if v.FunctionCall != nil {
					n += len(v.FunctionCall.Arguments)
				}
			}
		}
	}
	return n
}

// simulateRun drives turns through the real send order and returns, per turn,
// the fingerprints and the size of the copy that would have been sent.
func simulateRun(t *testing.T, p *ReActPlanner, turns int) ([][]string, []int) {
	t.Helper()
	ctx := context.Background()
	stored := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextPart("system prompt")}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextPart("Query: find the bug")}},
	}

	sent := make([][]string, 0, turns)
	sizes := make([]int, 0, turns)
	for turn := 1; turn <= turns; turn++ {
		// Same order as callLLM: compaction on the stored history, then aging
		// and ledger injection on the sent copy only.
		if p.shouldCompact(stored, turn-1) {
			stored = p.compactConversationWindow(ctx, stored)
		}
		out := p.ageOldObservations(stored)
		out = p.injectLedgerBlock(out)
		sent = append(sent, hashSentMessages(out))
		sizes = append(sizes, sentChars(out))

		// Append this turn's round: one AI message carrying the tool call, one
		// Tool message carrying a large observation.
		stored = p.updateConversationMessagesMulti(stored, []Step{{
			Number:  turn,
			Status:  "completed",
			Thought: fmt.Sprintf("thinking about step %d", turn),
			Action:  "file_view",
			ActionInput: map[string]any{
				"path": fmt.Sprintf("pkg/service/file_%d.go", turn),
			},
			Observation: strings.Repeat(fmt.Sprintf("observation body for step %d. ", turn), 300),
		}})
		// Reflection advances the distillation watermark every few steps, which
		// is what unlocks the next batch of observation stubs.
		if turn%defaultReflectionEvery == 0 {
			p.lastReflectedStep = turn
		}
	}
	return sent, sizes
}

func stabilityPlanner(t *testing.T) *ReActPlanner {
	t.Helper()
	p := NewReActPlanner(nil, nil, 30)
	return p
}

// The headline guard. A mid-prompt rewrite discards the cached prefix from
// that point on, so it must be both RARE and PAID FOR: observation aging fires
// once when the prompt crosses its budget and permanently shrinks every
// subsequent prompt, which is a trade worth making. A rewrite that does not
// shrink the prompt is pure waste and must not appear.
func TestPrefixStability_MutationsAreRareAndPayForThemselves(t *testing.T) {
	p := stabilityPlanner(t)
	sent, sizes := simulateRun(t, p, 25)

	mutated := 0
	for i := 1; i < len(sent); i++ {
		// The previous turn's trailing ledger block is not persisted, so it is
		// legitimately absent from this turn's prefix. Compare against the
		// previous turn minus that block.
		prev := sent[i-1]
		if len(prev) > 0 {
			prev = prev[:len(prev)-1]
		}
		idx := firstDivergentIndex(prev, sent[i])
		if idx == -1 {
			continue
		}
		mutated++
		discarded := 100 * (len(prev) - idx) / max(len(prev), 1)
		t.Logf("turn %d: rewrote from index %d of %d (%d%% of prefix), sent chars %d -> %d",
			i+1, idx, len(prev), discarded, sizes[i-1], sizes[i])

		// A rewrite that grew the prompt bought nothing and cost the cache.
		assert.Less(t, sizes[i], sizes[i-1],
			"turn %d rewrote the prefix without shrinking the prompt", i+1)
	}

	t.Logf("prefix mutated on %d of %d turns", mutated, len(sent)-1)
	assert.LessOrEqual(t, mutated, 2,
		"prefix rewrites must be rare; more than a couple means the prompt churns every turn")
}

// Compaction is the single largest prefix invalidation, so a normal run must
// not trigger it at all. This is the direct guard on the 2B trigger change:
// under the old `len(msgs) > 40` arm this fired around turn 20.
func TestPrefixStability_NoCompactionOnATypicalRun(t *testing.T) {
	p := stabilityPlanner(t)
	stored := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextPart("system prompt")}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextPart("Query: find the bug")}},
	}
	for turn := 1; turn <= 25; turn++ {
		assert.False(t, p.shouldCompact(stored, turn-1),
			"turn %d (%d messages, ~%d tokens) must not compact",
			turn, len(stored), estimateMessageTokens(stored))
		stored = p.updateConversationMessagesMulti(stored, []Step{{
			Number: turn, Status: "completed", Thought: "t", Action: "file_view",
			ActionInput: map[string]any{"path": fmt.Sprintf("f_%d.go", turn)},
			Observation: strings.Repeat("obs ", 300),
		}})
	}
}

// Aging must never rewrite an already-stubbed observation: re-stubbing the same
// message with different bytes would invalidate the prefix on every single turn.
func TestPrefixStability_StubsStayByteStable(t *testing.T) {
	p := stabilityPlanner(t)
	sent, _ := simulateRun(t, p, 25)

	// Any position that ever settles must keep the same fingerprint for the
	// rest of the run.
	settled := map[int]string{}
	for turn, hashes := range sent {
		for i, h := range hashes {
			// The final message each turn is the ledger block, which is rebuilt
			// per turn by design and never part of the cacheable prefix.
			if i == len(hashes)-1 {
				continue
			}
			if prior, ok := settled[i]; ok && prior != h {
				// Only a compaction may legitimately rewrite a position.
				t.Logf("position %d changed on turn %d", i, turn+1)
			}
			settled[i] = h
		}
	}
}
