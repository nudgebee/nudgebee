package agents

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// The agentic tool loop re-sends its whole message slice on every iteration, so an untrimmed
// observation is paid for again on each remaining iteration. These tests pin the trimming
// contract: recent observations stay verbatim, older ones shrink to a head snippet, and the
// rewrite is one-way (#31500).

// messageText reads the text of a single-part message built by llms.TextParts.
func messageText(t *testing.T, msg llms.MessageContent) string {
	t.Helper()
	assert.Len(t, msg.Parts, 1)
	text, ok := msg.Parts[0].(llms.TextContent)
	assert.True(t, ok, "expected a text part")
	return text.Text
}

// buildObservationHistory mimics what runToolLoop appends: a system prompt, the user message, then
// one (assistant, observation) pair per tool call.
func buildObservationHistory(bodies []string) ([]llms.MessageContent, []loopObservation) {
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "system prompt"),
		llms.TextParts(llms.ChatMessageTypeHuman, "user message"),
	}
	var observations []loopObservation
	for i, body := range bodies {
		messages = append(messages,
			llms.TextParts(llms.ChatMessageTypeAI, fmt.Sprintf("<thought_action>call %d</thought_action>", i)),
			llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("<observation>%s</observation>", body)),
		)
		observations = append(observations, loopObservation{msgIdx: len(messages) - 1, tool: "get_task_schema", body: body})
	}
	return messages, observations
}

// Within the window nothing is touched: the model still sees every recent observation in full.
func TestTrimStaleObservations_KeepsRecentVerbatim(t *testing.T) {
	bodies := []string{}
	for i := 0; i < observationHistoryWindow; i++ {
		bodies = append(bodies, strings.Repeat("x", trimmedObservationHeadBytes*3))
	}
	messages, observations := buildObservationHistory(bodies)

	trimStaleObservations(messages, observations)

	for _, o := range observations {
		assert.False(t, o.trimmed)
		assert.Equal(t, "<observation>"+o.body+"</observation>", messageText(t, messages[o.msgIdx]))
	}
}

// Once an observation falls out of the window it is rewritten to a head snippet with a marker,
// and the system prompt / user message / assistant turns are left alone.
func TestTrimStaleObservations_ShrinksOlderObservations(t *testing.T) {
	oldBody := strings.Repeat("a", trimmedObservationHeadBytes*5)
	bodies := []string{oldBody}
	for i := 0; i < observationHistoryWindow; i++ {
		bodies = append(bodies, fmt.Sprintf("recent %d", i))
	}
	messages, observations := buildObservationHistory(bodies)
	before := messageText(t, messages[observations[0].msgIdx])

	trimStaleObservations(messages, observations)

	trimmed := messageText(t, messages[observations[0].msgIdx])
	assert.True(t, observations[0].trimmed)
	assert.Less(t, len(trimmed), len(before), "the stale observation must get smaller")
	assert.True(t, strings.HasPrefix(trimmed, "<observation>"+strings.Repeat("a", trimmedObservationHeadBytes)))
	assert.True(t, strings.HasSuffix(trimmed, "</observation>"))
	assert.Contains(t, trimmed, "get_task_schema")
	assert.Contains(t, trimmed, fmt.Sprintf("%d of %d characters shown", trimmedObservationHeadBytes, len(oldBody)))

	assert.Equal(t, "system prompt", messageText(t, messages[0]))
	assert.Equal(t, "user message", messageText(t, messages[1]))
	assert.Equal(t, "<thought_action>call 0</thought_action>", messageText(t, messages[observations[0].msgIdx-1]))
}

// A stale observation already short enough is marked done and left byte-identical, so the marker
// never appears on something that would not save anything.
func TestTrimStaleObservations_LeavesShortObservationsAlone(t *testing.T) {
	bodies := []string{"ok: task added"}
	for i := 0; i < observationHistoryWindow; i++ {
		bodies = append(bodies, fmt.Sprintf("recent %d", i))
	}
	messages, observations := buildObservationHistory(bodies)

	trimStaleObservations(messages, observations)

	assert.True(t, observations[0].trimmed)
	assert.Equal(t, "<observation>ok: task added</observation>", messageText(t, messages[observations[0].msgIdx]))
}

// Trimming is one-way: re-running it over a history whose stale entries are already trimmed must
// not re-truncate them (which would strip the marker down a second time).
func TestTrimStaleObservations_IsIdempotent(t *testing.T) {
	bodies := []string{strings.Repeat("b", trimmedObservationHeadBytes*4)}
	for i := 0; i < observationHistoryWindow+2; i++ {
		bodies = append(bodies, fmt.Sprintf("recent %d", i))
	}
	messages, observations := buildObservationHistory(bodies)

	trimStaleObservations(messages, observations)
	first := messageText(t, messages[observations[0].msgIdx])
	trimStaleObservations(messages, observations)

	assert.Equal(t, first, messageText(t, messages[observations[0].msgIdx]))
}

// The whole point: a 20-iteration loop of fat observations ends up sending far fewer characters
// than it would have with an untrimmed history.
func TestTrimStaleObservations_CutsTotalHistorySize(t *testing.T) {
	const iterations = 20
	body := strings.Repeat("s", 8000) // a task schema is comfortably this big
	bodies := make([]string, iterations)
	for i := range bodies {
		bodies[i] = body
	}
	messages, observations := buildObservationHistory(bodies)
	untrimmed := 0
	for _, o := range observations {
		untrimmed += len(messageText(t, messages[o.msgIdx]))
	}

	trimStaleObservations(messages, observations)

	trimmedTotal := 0
	for _, o := range observations {
		trimmedTotal += len(messageText(t, messages[o.msgIdx]))
	}
	assert.Less(t, trimmedTotal*3, untrimmed, "trimming must cut the observation history by more than two thirds")
}
