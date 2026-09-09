package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func textMsg(role llms.ChatMessageType, s string) llms.MessageContent {
	return llms.MessageContent{Role: role, Parts: []llms.ContentPart{llms.TextContent{Text: s}}}
}

// The shape that produced the wrong answer in session f1a7ddd5: two system
// messages plus a human turn that carries the question AND a large tool result,
// making the human turn the biggest message. Before the guard it absorbed the
// whole overshoot, hit the 256-token floor (18,482 chars -> 915), and the model
// answered a question it could no longer see.
func TestPreflightCapKeepsTheUserQuestion(t *testing.T) {
	withFakeModelLimitsCatalog(t, map[string]modelTokenLimits{
		"openai:gpt-4o": {MaxContext: 32_000, MaxOutput: 4096},
	})
	ctx := llmOverrideContext()

	const question = "Investigate the current health of the services-server deployment"
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, strings.Repeat("system instructions. ", 4000)),
		textMsg(llms.ChatMessageTypeSystem, strings.Repeat("output format rules. ", 500)),
		textMsg(llms.ChatMessageTypeHuman,
			question+"\n<scratchpad>\n"+strings.Repeat("kubectl output line with pod status. ", 3000)+"\n</scratchpad>"),
	}

	out := applyPreflightContextWindowCap(ctx, msgs, "openai", "gpt-4o", nil, "k8s_orchestrator")

	human := out[2].Parts[0].(llms.TextContent).Text
	assert.Contains(t, human, question,
		"the user's question must survive the trim — losing it is what made the model invent a task")
	assert.NotEqual(t, 0, len(human))

	// The system messages are where the budget should come from instead.
	sys0 := out[0].Parts[0].(llms.TextContent).Text
	assert.Less(t, len(sys0), len(msgs[0].Parts[0].(llms.TextContent).Text),
		"the trim should fall on a system message, not the user turn")
}

// largestTextMessageIndex must honour the protected index, and -1 must mean
// "consider everything" so the reactive summarization path is unchanged.
func TestLargestTextMessageIndexProtect(t *testing.T) {
	msgs := []llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "a"),
		textMsg(llms.ChatMessageTypeHuman, "b"),
	}
	idx, _ := largestTextMessageIndex(msgs, []int{10, 9999}, -1)
	assert.Equal(t, 1, idx, "with no protection the biggest message wins")

	idx, tok := largestTextMessageIndex(msgs, []int{10, 9999}, 1)
	assert.Equal(t, 0, idx, "the protected index is skipped")
	assert.Equal(t, 10, tok)

	idx, _ = largestTextMessageIndex(msgs[1:], []int{9999}, 0)
	assert.Equal(t, -1, idx, "protecting the only candidate yields no index")
}

func TestLastHumanTextMessageIndex(t *testing.T) {
	assert.Equal(t, 2, lastHumanTextMessageIndex([]llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "s"),
		textMsg(llms.ChatMessageTypeHuman, "first"),
		textMsg(llms.ChatMessageTypeHuman, "last"),
	}), "the LAST human turn is the one carrying the current question")

	assert.Equal(t, -1, lastHumanTextMessageIndex([]llms.MessageContent{
		textMsg(llms.ChatMessageTypeSystem, "s"),
	}))
}

// Head+tail keeps both ends; the old head-only cut kept the question but threw
// away every recent observation the next ReAct step depends on.
func TestTruncateHeadTailKeepsBothEnds(t *testing.T) {
	text := "QUESTION-AT-TOP " + strings.Repeat("filler ", 5000) + " NEWEST-OBSERVATION"
	got := truncateToTokenLimitHeadTail(text, 200, "openai", "gpt-4o")
	assert.Less(t, len(got), len(text))
	assert.Contains(t, got, "QUESTION-AT-TOP")
	assert.Contains(t, got, "NEWEST-OBSERVATION")
}

// TestTruncateHeadTailShortTextSmallBudget covers the case where the 100-char
// floor lands above the text's own length: the text must come back whole rather
// than picking up a truncation marker it has no room for.
func TestTruncateHeadTailShortTextSmallBudget(t *testing.T) {
	short := "the pods are crashlooping, why?"
	got := truncateToTokenLimitHeadTail(short, 1, "openai", "gpt-4o")
	assert.Equal(t, short, got, "text shorter than the floor is returned unmodified")

	// A budget large enough to clear SmartTruncateToolOutput's 1024-byte
	// small-cap path, so the head/tail branch this helper exists for is the one
	// under test.
	long := strings.Repeat("evidence ", 5000)
	trimmed := truncateToTokenLimitHeadTail(long, 1000, "openai", "gpt-4o")
	assert.Less(t, len(trimmed), len(long), "an over-budget message is still trimmed")
	assert.Contains(t, trimmed, "TRUNCATED", "and says so")
	assert.True(t, strings.HasPrefix(trimmed, "evidence evidence"), "head is kept")
	assert.True(t, strings.HasSuffix(trimmed, "evidence "), "tail is kept")
}
