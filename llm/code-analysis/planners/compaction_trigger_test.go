package planners

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// compactionPlanner builds a planner with the production compaction defaults
// for a 30-iteration run, without touching the LLM client.
func compactionPlanner(t *testing.T) *ReActPlanner {
	t.Helper()
	return &ReActPlanner{
		maxIterations:               30,
		maxContextTokens:            maxContextTokensFromEnv(),
		compactionTriggerPct:        compactionTriggerPctFromEnv(),
		compactionMaxMessages:       compactionMaxMessagesFromEnv(30),
		compactionMinRemainingIters: compactionMinRemainingItersFromEnv(),
	}
}

// msgsOfSize builds n messages of roughly charsEach characters apiece.
func msgsOfSize(n, charsEach int) []llms.MessageContent {
	out := make([]llms.MessageContent, 0, n)
	body := strings.Repeat("x", charsEach)
	for i := 0; i < n; i++ {
		out = append(out, llms.MessageContent{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextPart(body)},
		})
	}
	return out
}

// The regression this whole change exists for: a normal-sized 30-iteration run
// must never trip compaction. The old hardcoded `len(msgs) > 40` arm fired
// around iteration 20 regardless of actual size, collapsing the cached prefix.
func TestShouldCompact_TypicalRunNeverCompacts(t *testing.T) {
	p := compactionPlanner(t)
	// 2 header messages + 2 per round, at a realistic ~1200 chars per message.
	for iteration := 0; iteration < 30; iteration++ {
		msgs := msgsOfSize(2+2*iteration, 1200)
		assert.False(t, p.shouldCompact(msgs, iteration),
			"iteration %d (%d messages) must not compact", iteration, len(msgs))
	}
}

// The token arm is the real trigger and must actually fire — parallel tool
// responses at the 15000-char cap get a conversation there well before the
// message count does.
func TestShouldCompact_TokenArmFires(t *testing.T) {
	p := compactionPlanner(t)
	// ~150K tokens: over the 70% soft budget but under the 90% hard ceiling, so
	// this exercises the soft arm rather than the safety net.
	msgs := msgsOfSize(20, 30000)
	est := estimateMessageTokens(msgs)
	require.Greater(t, est, p.maxContextTokens*p.compactionTriggerPct/100)
	require.Less(t, est, p.maxContextTokens*90/100)
	require.LessOrEqual(t, len(msgs), p.compactionMaxMessages, "must not be the message arm firing")

	assert.True(t, p.shouldCompact(msgs, 5))
}

// The message backstop still exists for models without meaningful token
// accounting — it is just derived from the iteration budget instead of frozen.
func TestShouldCompact_MessageBackstopFires(t *testing.T) {
	p := compactionPlanner(t)
	msgs := msgsOfSize(p.compactionMaxMessages+1, 10)

	assert.True(t, p.shouldCompact(msgs, 5))
	assert.False(t, p.shouldCompact(msgsOfSize(p.compactionMaxMessages, 10), 5),
		"exactly at the cap must not fire")
}

// Near the end of a run a discretionary compaction pays a summariser call and a
// cold cache with too few calls left to earn either back.
func TestShouldCompact_AmortisationGuardSuppressesLate(t *testing.T) {
	p := compactionPlanner(t)
	msgs := msgsOfSize(20, 30000) // over the soft budget, under the hard ceiling
	require.Greater(t, estimateMessageTokens(msgs), p.maxContextTokens*p.compactionTriggerPct/100)
	require.Less(t, estimateMessageTokens(msgs), p.maxContextTokens*90/100)

	assert.True(t, p.shouldCompact(msgs, 5), "mid-run it should compact")
	assert.False(t, p.shouldCompact(msgs, 28), "with 2 iterations left it must not")
}

// The hard ceiling is a safety net, so the amortisation guard must never
// suppress it — that is what makes relaxing the other arms safe.
func TestShouldCompact_HardCeilingIgnoresAmortisationGuard(t *testing.T) {
	p := compactionPlanner(t)
	msgs := msgsOfSize(60, 45000)
	require.Greater(t, estimateMessageTokens(msgs), p.maxContextTokens*90/100)

	assert.True(t, p.shouldCompact(msgs, 29), "hard ceiling must fire even on the last iteration")
}

func TestCompactionEnvOverrides(t *testing.T) {
	t.Run("explicit values are honoured", func(t *testing.T) {
		t.Setenv("REACT_MAX_CONTEXT_TOKENS", "50000")
		t.Setenv("COMPACTION_TRIGGER_PCT", "50")
		t.Setenv("COMPACTION_MAX_MESSAGES", "25")
		t.Setenv("COMPACTION_MIN_REMAINING_ITERS", "0")

		assert.Equal(t, 50000, maxContextTokensFromEnv())
		assert.Equal(t, 50, compactionTriggerPctFromEnv())
		assert.Equal(t, 25, compactionMaxMessagesFromEnv(30))
		assert.Equal(t, 0, compactionMinRemainingItersFromEnv())
	})

	t.Run("garbage and out-of-range fall back to defaults", func(t *testing.T) {
		t.Setenv("REACT_MAX_CONTEXT_TOKENS", "not-a-number")
		t.Setenv("COMPACTION_TRIGGER_PCT", "95") // would invert the hard ceiling
		t.Setenv("COMPACTION_MAX_MESSAGES", "-5")
		t.Setenv("COMPACTION_MIN_REMAINING_ITERS", "-1")

		assert.Equal(t, 200000, maxContextTokensFromEnv())
		assert.Equal(t, 70, compactionTriggerPctFromEnv())
		assert.Equal(t, 90, compactionMaxMessagesFromEnv(30))
		assert.Equal(t, 4, compactionMinRemainingItersFromEnv())
	})

	t.Run("message cap derives from the iteration budget", func(t *testing.T) {
		assert.Equal(t, 90, compactionMaxMessagesFromEnv(30))
		assert.Equal(t, 130, compactionMaxMessagesFromEnv(50))
		assert.Equal(t, 60, compactionMaxMessagesFromEnv(5), "floored for short budgets")
	})
}

// The production defaults are what actually run; nothing else asserts them.
func TestNewReActPlanner_ProductionDefaults(t *testing.T) {
	p := NewReActPlanner(nil, nil, 30)

	assert.Equal(t, 200000, p.maxContextTokens)
	assert.Equal(t, 70, p.compactionTriggerPct)
	assert.Equal(t, 90, p.compactionMaxMessages)
	assert.Equal(t, 4, p.compactionMinRemainingIters)
	assert.Equal(t, 3, p.recentObservationWindow)
	assert.Equal(t, 45000, p.agingBudgetTokens)
}
