package egressfilter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// Two distinct, well-formed AWS access key ids (documentation-shaped, not real
// credentials). Both match the aws-access-key-id baseline rule.
const (
	secretPrior   = "AKIAIOSFODNN7EXAMPLE" // "pasted on turn 1"
	secretCurrent = "AKIAQQQQQQQQQQQQQQQQ" // "typed on turn 2"
)

func TestReportScope_EmptyHistoryKeepsFirstSighting(t *testing.T) {
	payload := "here is " + secretCurrent + " ok"
	hits := Scan(payload).Hits
	require.Len(t, hits, 1)

	kept, mask := newReportScope("").filter(hits, payload)
	assert.Len(t, kept, 1, "an empty history seed keeps the first sighting")
	assert.Equal(t, []bool{true}, mask)
}

func TestReportScope_DropsCarriedOverSecret(t *testing.T) {
	// Payload re-sends the prior secret (history) and adds a fresh one.
	scope := newReportScope("previous message pasted " + secretPrior)
	payload := "<history>" + secretPrior + "</history> <question>" + secretCurrent + "</question>"

	hits := Scan(payload).Hits
	require.Len(t, hits, 2, "both AWS keys should be detected in the payload")

	kept, mask := scope.filter(hits, payload)
	require.Len(t, kept, 1, "the carried-over secret must be dropped from the report")

	// The kept hit must be the current-turn secret, not the carried one.
	assert.Equal(t, secretCurrent, payload[kept[0].Start:kept[0].End])
	assert.Len(t, mask, 2)
	assert.Equal(t, len(hits), len(mask), "mask must align with the input hits")
}

func TestReportScope_AllCarriedOverYieldsEmpty(t *testing.T) {
	scope := newReportScope("history holds " + secretPrior)
	payload := "re-sent history: " + secretPrior

	hits := Scan(payload).Hits
	require.Len(t, hits, 1)

	kept, _ := scope.filter(hits, payload)
	assert.Empty(t, kept, "a pure carry-over payload reports nothing new")
}

func TestReportScope_DedupesRepeatedCalls(t *testing.T) {
	// The same current-turn secret scanned on two LLM calls of one turn must
	// count exactly once — the scope remembers it after the first call.
	scope := newReportScope("")
	payload := "question: echo " + secretCurrent
	hits := Scan(payload).Hits
	require.Len(t, hits, 1)

	kept1, _ := scope.filter(hits, payload)
	assert.Len(t, kept1, 1, "first sighting is reported")

	kept2, _ := scope.filter(Scan(payload).Hits, payload)
	assert.Empty(t, kept2, "second sighting in the same turn is suppressed")
}

func TestFilterByMask_KeepsAlignedEntries(t *testing.T) {
	reds := []Redaction{{Placeholder: "a"}, {Placeholder: "b"}, {Placeholder: "c"}}
	out := filterByMask(reds, []bool{true, false, true})
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Placeholder)
	assert.Equal(t, "c", out[1].Placeholder)
}

// collectEvents wires a reporter into ctx and returns the accumulated events.
func collectEvents(base context.Context, events *[]FilterEvent) context.Context {
	return WithFilterEventReporter(base, func(e FilterEvent) {
		*events = append(*events, e)
	})
}

// TestWrapper_DetectReportsOnlyCurrentTurnSecret is the end-to-end contract for
// the carry-over fix: with a baseline holding the prior-turn secret, a detect
// call whose payload contains BOTH the prior (history) and the current secret
// must forward unchanged AND report only the current-turn secret.
func TestWrapper_DetectReportsOnlyCurrentTurnSecret(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)

	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "history: " + secretPrior + "\nquestion: echo " + secretCurrent},
		}},
	}

	var events []FilterEvent
	ctx := collectEvents(context.Background(), &events)
	ctx = WithReportBaseline(ctx, "earlier turn contained "+secretPrior)

	_, err := wrapped.GenerateContent(ctx, msgs)
	require.NoError(t, err, "detect mode must not block")
	require.Equal(t, 1, inner.callCount(), "provider is still reached in detect mode")

	require.Len(t, events, 1, "exactly one per-message event")
	assert.Equal(t, 1, events[0].HitCount, "only the current-turn secret is counted")
	assert.Contains(t, events[0].RuleIDs, "aws-access-key-id")
}

// TestWrapper_DedupesAcrossCallsInTurn: a turn drives several LLM calls that
// re-scan the same user text; sharing one per-turn scope, the freshly-typed
// secret is reported once, not once per call.
func TestWrapper_DedupesAcrossCallsInTurn(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)

	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "question: echo " + secretCurrent},
		}},
	}

	var events []FilterEvent
	ctx := collectEvents(context.Background(), &events)
	ctx = WithReportBaseline(ctx, "") // fresh turn, no history

	_, err := wrapped.GenerateContent(ctx, msgs)
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, msgs)
	require.NoError(t, err)

	require.Len(t, events, 1, "the secret is reported once across the turn's calls")
	assert.Equal(t, 1, events[0].HitCount)
}

// TestWrapper_DetectSuppressesPureCarryOver: when the ONLY secret in the
// payload is one already present in the baseline, no per-message event is
// emitted — the badge must not carry a stale secret into a clean turn.
func TestWrapper_DetectSuppressesPureCarryOver(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)

	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "history: " + secretPrior + "\nquestion: what is the weather"},
		}},
	}

	var events []FilterEvent
	ctx := collectEvents(context.Background(), &events)
	ctx = WithReportBaseline(ctx, "earlier turn contained "+secretPrior)

	_, err := wrapped.GenerateContent(ctx, msgs)
	require.NoError(t, err)
	require.Equal(t, 1, inner.callCount())
	assert.Empty(t, events, "a carried-over-only turn reports no new secret")
}

// TestWrapper_EnforceStillBlocksCarriedOver: security is unaffected — a
// carried-over secret re-sent in history is still egressing, so enforce mode
// must still block even though the reported event would be empty.
func TestWrapper_EnforceStillBlocksCarriedOver(t *testing.T) {
	// Register a blocking gate for the duration of this test.
	prev := ActionGate
	ActionGate = func(mode Mode, _ Result) Action {
		if mode == ModeEnforce {
			return ActionBlock
		}
		return ActionAudit
	}
	t.Cleanup(func() { ActionGate = prev })

	inner := &fakeModel{respText: "should never reach provider"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeEnforce)

	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "history: " + secretPrior + "\nquestion: what is the weather"},
		}},
	}

	var events []FilterEvent
	ctx := collectEvents(context.Background(), &events)
	ctx = WithReportBaseline(ctx, "earlier turn contained "+secretPrior)

	_, err := wrapped.GenerateContent(ctx, msgs)
	require.Error(t, err, "carried-over secret still egresses and must be blocked")
	assert.Equal(t, 0, inner.callCount(), "provider must not be reached")
	assert.Empty(t, events, "no NEW secret this turn, so no per-message badge")
}

// TestReportScope_ReTypedSecretInNewTurnIsReported locks the contract that when
// a secret from prior conversation history is re-typed in the current turn,
// the scope recognizes the surplus occurrence and reports it (fixes #35811).
func TestReportScope_ReTypedSecretInNewTurnIsReported(t *testing.T) {
	// Baseline history has 1 occurrence of secretPrior.
	scope := newReportScope("history holds " + secretPrior)
	// Payload contains history (1 occurrence) PLUS new message with secretPrior (2nd occurrence).
	payload := "history: " + secretPrior + "\nquestion: echo " + secretPrior

	hits := Scan(payload).Hits
	require.Len(t, hits, 2, "both occurrences in payload are scanned")

	kept, mask := scope.filter(hits, payload)
	require.Len(t, kept, 1, "re-typed secret must be reported for the current turn")
	assert.Equal(t, secretPrior, payload[kept[0].Start:kept[0].End])
	assert.Equal(t, []bool{false, true}, mask, "history occurrence is dropped, current turn occurrence is kept")
}

// TestWrapper_DetectReportsReTypedSecret: end-to-end wrapper test ensuring that
// re-typing a secret from prior history emits a FilterEvent for the current turn.
func TestWrapper_DetectReportsReTypedSecret(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)

	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "history: " + secretPrior + "\nquestion: echo " + secretPrior},
		}},
	}

	var events []FilterEvent
	ctx := collectEvents(context.Background(), &events)
	ctx = WithReportBaseline(ctx, "earlier turn contained "+secretPrior)

	_, err := wrapped.GenerateContent(ctx, msgs)
	require.NoError(t, err)
	require.Equal(t, 1, inner.callCount())

	require.Len(t, events, 1, "re-typed secret must emit a FilterEvent for this turn")
	assert.Equal(t, 1, events[0].HitCount)
	assert.Contains(t, events[0].RuleIDs, "aws-access-key-id")
}

// TestReportScope_ReTypedSecretInSubsequentCallIsReported verifies that a secret
// suppressed in an earlier LLM call of a turn (due to matching history count)
// is still reported when a subsequent call in the same turn introduces a fresh occurrence.
func TestReportScope_ReTypedSecretInSubsequentCallIsReported(t *testing.T) {
	scope := newReportScope("history holds " + secretPrior)

	// Call 1: 1 occurrence (same as history) -> should be suppressed
	payload1 := "history: " + secretPrior
	hits1 := Scan(payload1).Hits
	require.Len(t, hits1, 1)
	kept1, _ := scope.filter(hits1, payload1)
	assert.Empty(t, kept1, "pure carry-over in first call is suppressed")

	// Call 2: 2 occurrences (1 more than history) -> should report the new one
	payload2 := "history: " + secretPrior + "\nquestion: echo " + secretPrior
	hits2 := Scan(payload2).Hits
	require.Len(t, hits2, 2)
	kept2, _ := scope.filter(hits2, payload2)
	assert.Len(t, kept2, 1, "new occurrence in second call must be reported")
}
