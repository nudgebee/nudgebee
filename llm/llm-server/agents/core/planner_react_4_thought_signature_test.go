package core

import (
	"testing"

	"nudgebee/llm/llms/googleai"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// Gemini 2.5/3.x thinking models return an opaque signature of the reasoning
// behind each native function call and require it replayed verbatim whenever
// that call reappears in history. react_4 rebuilds its message list from the
// neutral steps every Plan(), so a signature that is not captured onto the step
// and re-supplied on the next request is gone — and 3.x then rejects the whole
// request with HTTP 400 ("Function call is missing a thought_signature in
// functionCall parts"), which is what capped react_4 at roughly one tool call
// per turn.

func TestThoughtSignaturesFromChoice_ReadsPositionally(t *testing.T) {
	choice := &llms.ContentChoice{
		GenerationInfo: map[string]any{
			googleai.GenerationInfoThoughtSignatures: [][]byte{[]byte("sig-A"), nil, []byte("sig-C")},
		},
	}
	got := thoughtSignaturesFromChoice(choice)
	assert.Equal(t, [][]byte{[]byte("sig-A"), nil, []byte("sig-C")}, got)
}

func TestThoughtSignaturesFromChoice_AbsentIsNil(t *testing.T) {
	assert.Nil(t, thoughtSignaturesFromChoice(nil))
	assert.Nil(t, thoughtSignaturesFromChoice(&llms.ContentChoice{}))
	assert.Nil(t, thoughtSignaturesFromChoice(&llms.ContentChoice{GenerationInfo: map[string]any{"other": 1}}))
	// A wrong-typed value must not panic — non-Gemini providers populate
	// GenerationInfo with their own unrelated keys.
	assert.Nil(t, thoughtSignaturesFromChoice(&llms.ContentChoice{
		GenerationInfo: map[string]any{googleai.GenerationInfoThoughtSignatures: "not-a-slice"},
	}))
}

// parseCompletion must land signature i on the action built from ToolCalls[i],
// even though the loop skips entries with a nil FunctionCall.
func TestReAct4_ParseCompletion_AttachesSignaturesPositionally(t *testing.T) {
	o := &NBReActPlanner4{}
	choice := &llms.ContentChoice{
		ToolCalls: []llms.ToolCall{
			toolCall("call_1", "kubectl", `{"command":"get pods"}`),
			{ID: "call_bad", Type: "function"}, // nil FunctionCall — skipped
			toolCall("call_3", "kubectl", `{"command":"get svc"}`),
		},
		GenerationInfo: map[string]any{
			googleai.GenerationInfoThoughtSignatures: [][]byte{[]byte("sig-1"), []byte("sig-skipped"), []byte("sig-3")},
		},
	}
	actions, _, err := o.parseCompletion(choice)
	assert.NoError(t, err)
	assert.Len(t, actions, 2)
	assert.Equal(t, []byte("sig-1"), actions[0].ThoughtSignature)
	assert.Equal(t, []byte("sig-3"), actions[1].ThoughtSignature,
		"the skipped nil-FunctionCall entry must not shift signature alignment")
}

func TestReAct4_ParseCompletion_NoSignaturesIsFine(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl", `{"command":"get pods"}`)},
	})
	assert.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Empty(t, actions[0].ThoughtSignature, "providers without signatures must still parse")
}

func TestThoughtSignatureOption_NilWhenNothingToReplay(t *testing.T) {
	assert.Nil(t, thoughtSignatureOption(nil))
	assert.Nil(t, thoughtSignatureOption([]NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c1"}},
	}), "steps without signatures must add no option at all")
}

func TestThoughtSignatureOption_KeysByToolID(t *testing.T) {
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c1", ThoughtSignature: []byte("sig-1")}},
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c2"}},
		{Action: NBAgentPlannerToolAction{Tool: "logs", ToolID: "c3", ThoughtSignature: []byte("sig-3")}},
	}
	opt := thoughtSignatureOption(steps)
	assert.NotNil(t, opt)

	callOpts := &llms.CallOptions{}
	opt(callOpts)

	got, ok := callOpts.Metadata[googleai.MetadataThoughtSignatures].(map[string][]byte)
	assert.True(t, ok)
	assert.Equal(t, map[string][]byte{"c1": []byte("sig-1"), "c3": []byte("sig-3")}, got)
}

// The option must MUTATE Metadata, never replace it: llms.WithMetadata assigns
// the whole map, and the caching + thinking layers have already put
// CachedContentName / ThinkingLevel there. Dropping CachedContentName would
// silently disable prompt caching on every react_4 request that replays a tool.
func TestThoughtSignatureOption_PreservesExistingMetadata(t *testing.T) {
	opt := thoughtSignatureOption([]NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c1", ThoughtSignature: []byte("sig-1")}},
	})
	callOpts := &llms.CallOptions{Metadata: map[string]any{
		"CachedContentName": "cachedContents/abc123",
		"ThinkingLevel":     "high",
	}}
	opt(callOpts)

	assert.Equal(t, "cachedContents/abc123", callOpts.Metadata["CachedContentName"])
	assert.Equal(t, "high", callOpts.Metadata["ThinkingLevel"])
	assert.NotNil(t, callOpts.Metadata[googleai.MetadataThoughtSignatures])
}

// End-to-end of the neutral hop: a signature captured off a completion must
// survive onto the step and come back out keyed by the id used in the rebuilt
// message — this is the hop that was lossy.
func TestThoughtSignature_RoundTripsThroughSteps(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		ToolCalls: []llms.ToolCall{toolCall("call_1", "kubectl", `{"command":"get pods"}`)},
		GenerationInfo: map[string]any{
			googleai.GenerationInfoThoughtSignatures: [][]byte{[]byte("opaque-sig")},
		},
	})
	assert.NoError(t, err)

	steps := []NBAgentPlannerToolActionStep{{Action: actions[0], Observation: "pod running"}}

	// The rebuilt assistant message carries the id the map is keyed by.
	msgs := o.renderStepsToMessages(steps)
	tc, ok := msgs[0].Parts[0].(llms.ToolCall)
	if !ok {
		tc = msgs[0].Parts[1].(llms.ToolCall)
	}

	callOpts := &llms.CallOptions{}
	thoughtSignatureOption(steps)(callOpts)
	got := callOpts.Metadata[googleai.MetadataThoughtSignatures].(map[string][]byte)

	assert.Equal(t, []byte("opaque-sig"), got[tc.ID],
		"the signature must be retrievable by the tool-call id present in the replayed message")
}

// A parallel batch must be replayed as ONE assistant message carrying every
// sibling call. Splitting it is what produced Gemini's
// "...missing a thought_signature..., position 2": only the first functionCall
// part of a turn carries a signature, so siblings promoted to standalone
// messages have none of their own.
func TestReAct4_RenderStepsToMessages_ParallelBatchStaysOneAssistantMessage(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{
			Action:      NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{"command":"get pods"}`, ToolID: "c1", Log: "check both", TurnID: "t1", ThoughtSignature: []byte("sig")},
			Observation: "pods",
		},
		{
			Action:      NBAgentPlannerToolAction{Tool: "logs", ToolInput: `{"q":"err"}`, ToolID: "c2", Log: "check both", TurnID: "t1"},
			Observation: "logs",
		},
	}
	msgs := o.renderStepsToMessages(steps)

	// assistant(thought + 2 calls), tool(c1), tool(c2)
	assert.Len(t, msgs, 3)
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[0].Role)
	assert.Len(t, msgs[0].Parts, 3, "one shared thought + both sibling tool calls in a single message")

	_, isText := msgs[0].Parts[0].(llms.TextContent)
	assert.True(t, isText, "the shared thought is emitted once, ahead of the calls")
	first := msgs[0].Parts[1].(llms.ToolCall)
	second := msgs[0].Parts[2].(llms.ToolCall)
	assert.Equal(t, "c1", first.ID)
	assert.Equal(t, "c2", second.ID)

	assert.Equal(t, llms.ChatMessageTypeTool, msgs[1].Role)
	assert.Equal(t, llms.ChatMessageTypeTool, msgs[2].Role)
	assert.Equal(t, "c1", msgs[1].Parts[0].(llms.ToolCallResponse).ToolCallID)
	assert.Equal(t, "c2", msgs[2].Parts[0].(llms.ToolCallResponse).ToolCallID)
}

func TestReAct4_RenderStepsToMessages_DistinctTurnsStaySeparate(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c1", TurnID: "t1"}, Observation: "a"},
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c2", TurnID: "t2"}, Observation: "b"},
	}
	msgs := o.renderStepsToMessages(steps)
	assert.Len(t, msgs, 4, "two separate turns -> two assistant messages, each with its own result")
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[0].Role)
	assert.Equal(t, llms.ChatMessageTypeTool, msgs[1].Role)
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[2].Role)
	assert.Equal(t, llms.ChatMessageTypeTool, msgs[3].Role)
}

// Steps persisted before TurnID existed must render exactly as they used to.
func TestReAct4_RenderStepsToMessages_NoTurnIDIsOneMessagePerStep(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c1"}, Observation: "a"},
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c2"}, Observation: "b"},
	}
	assert.Len(t, o.renderStepsToMessages(steps), 4)
}

// parseCompletion must stamp every sibling of one completion with the same TurnID.
func TestReAct4_ParseCompletion_SiblingsShareTurnID(t *testing.T) {
	o := &NBReActPlanner4{}
	actions, _, err := o.parseCompletion(&llms.ContentChoice{
		ToolCalls: []llms.ToolCall{
			toolCall("c1", "kubectl", `{"command":"get pods"}`),
			toolCall("c2", "logs", `{"q":"err"}`),
		},
	})
	assert.NoError(t, err)
	assert.Len(t, actions, 2)
	assert.NotEmpty(t, actions[0].TurnID)
	assert.Equal(t, actions[0].TurnID, actions[1].TurnID)
}

// Parallel execution appends results in COMPLETION order, so siblings dispatched
// in one turn can land NON-ADJACENT in the step list. Grouping by a consecutive
// run split them back into standalone assistant messages, and since Gemini signs
// only the first functionCall part of a turn, the split siblings replayed
// unsigned — which is exactly how "missing a thought_signature ... position 2"
// reappeared on the one A/B case that used a parallel batch (18 rejections),
// while the two cases with no parallel batch had zero.
func TestReAct4_RenderStepsToMessages_ParallelSiblingsGroupWhenInterleaved(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		// t1 dispatched two calls; an unrelated t2 step completed in between.
		{Action: NBAgentPlannerToolAction{Tool: "prometheus", ToolID: "c1", TurnID: "t1", Log: "check both", ThoughtSignature: []byte("sig")}, Observation: "a"},
		{Action: NBAgentPlannerToolAction{Tool: "other", ToolID: "cX", TurnID: "t2"}, Observation: "x"},
		{Action: NBAgentPlannerToolAction{Tool: "anomaly", ToolID: "c2", TurnID: "t1"}, Observation: "b"},
	}
	msgs := o.renderStepsToMessages(steps)

	// t1 -> assistant(thought + c1 + c2), tool(c1), tool(c2); then t2 -> assistant, tool
	assert.Len(t, msgs, 5)
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[0].Role)
	assert.Len(t, msgs[0].Parts, 3, "both t1 siblings must share one assistant message even when interleaved")

	first := msgs[0].Parts[1].(llms.ToolCall)
	second := msgs[0].Parts[2].(llms.ToolCall)
	assert.Equal(t, "c1", first.ID)
	assert.Equal(t, "c2", second.ID)

	// The interleaved t2 step still renders, as its own turn, after t1's results.
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[3].Role)
	assert.Equal(t, "cX", msgs[3].Parts[0].(llms.ToolCall).ID)
}

// Each step's observation must be rendered against its OWN absolute index, or
// the window-gated compression decision shifts when siblings are regrouped.
func TestReAct4_RenderTurn_UsesAbsoluteStepIndex(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: "a", ToolID: "c1", TurnID: "t1"}, Observation: "first"},
		{Action: NBAgentPlannerToolAction{Tool: "b", ToolID: "c2", TurnID: "t1"}, Observation: "second"},
	}
	msgs := o.renderStepsToMessages(steps)
	assert.Equal(t, "first", msgs[1].Parts[0].(llms.ToolCallResponse).Content)
	assert.Equal(t, "second", msgs[2].Parts[0].(llms.ToolCallResponse).Content)
}

// A notebook step inside a MULTI-call turn must be replayed, not skipped.
// Signatures are positional, the prompt explicitly allows update_notebook
// alongside an investigation tool, and Gemini signs only part 0 — so dropping a
// signature-bearing notebook call leaves the survivors replaying unsigned.
func TestReAct4_RenderStepsToMessages_NotebookReplayedInsideBatch(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: `{"content":"nb"}`, ToolID: "c0", TurnID: "t1", Log: "record and check", ThoughtSignature: []byte("sig")}, Observation: "ok"},
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolInput: `{"command":"get pods"}`, ToolID: "c1", TurnID: "t1"}, Observation: "pods"},
	}
	msgs := o.renderStepsToMessages(steps)

	// assistant(thought + notebook call + kubectl call), tool(c0), tool(c1)
	assert.Len(t, msgs, 3)
	assert.Len(t, msgs[0].Parts, 3, "the notebook call stays in the batch so its signature is not orphaned")
	assert.Equal(t, "c0", msgs[0].Parts[1].(llms.ToolCall).ID)
	assert.Equal(t, "c1", msgs[0].Parts[2].(llms.ToolCall).ID)
}

// A notebook-ONLY turn is still skipped entirely — its state rides on the human
// message rather than bloating the replayed history.
func TestReAct4_RenderStepsToMessages_SoloNotebookTurnStillSkipped(t *testing.T) {
	o := &NBReActPlanner4{}
	steps := []NBAgentPlannerToolActionStep{
		{Action: NBAgentPlannerToolAction{Tool: toolcore.NotebookToolName, ToolInput: `{"content":"nb"}`, ToolID: "c0", TurnID: "t1"}, Observation: "ok"},
		{Action: NBAgentPlannerToolAction{Tool: "kubectl", ToolID: "c1", TurnID: "t2"}, Observation: "pods"},
	}
	msgs := o.renderStepsToMessages(steps)
	assert.Len(t, msgs, 2, "solo notebook turn contributes nothing; only the kubectl turn renders")
	assert.Equal(t, "c1", msgs[0].Parts[0].(llms.ToolCall).ID)
}
