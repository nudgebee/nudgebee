package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// isEmptyCompletion decides whether a generation is broken and should burn a
// retry / model-fallback attempt. It was inlined until native tool calling made
// it wrong: ReAct4 turns are frequently a bare tool_use block with no prose, and
// treating those as empty spent the retry budget on healthy responses and then
// failed the turn ("llm returned empty content", observed 9 times in one run).
// These cases pin both directions.

func TestIsEmptyCompletion_ToolCallWithoutTextIsNotEmpty(t *testing.T) {
	completion := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			Content:   "",
			ToolCalls: []llms.ToolCall{{ID: "call_1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "kubectl_execute", Arguments: `{"command":"get pods"}`}}},
		}},
	}
	assert.False(t, isEmptyCompletion(completion),
		"a turn whose entire payload is a tool call is COMPLETE — treating it as empty "+
			"burns the retry/fallback budget and then fails a healthy turn")
}

func TestIsEmptyCompletion_TextWithoutToolCallIsNotEmpty(t *testing.T) {
	completion := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: "the pod is OOMKilled"}},
	}
	assert.False(t, isEmptyCompletion(completion))
}

func TestIsEmptyCompletion_NoTextAndNoToolCallIsEmpty(t *testing.T) {
	completion := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: ""}},
	}
	assert.True(t, isEmptyCompletion(completion),
		"the original broken-generation case must still be caught")
}

func TestIsEmptyCompletion_NilAndNoChoicesAreEmpty(t *testing.T) {
	assert.True(t, isEmptyCompletion(nil))
	assert.True(t, isEmptyCompletion(&llms.ContentResponse{}))
	assert.True(t, isEmptyCompletion(&llms.ContentResponse{Choices: []*llms.ContentChoice{}}))
}

// Both present is the ordinary ReAct4 shape (a sentence of reasoning alongside
// the call) and is obviously not empty.
func TestIsEmptyCompletion_TextAndToolCallIsNotEmpty(t *testing.T) {
	completion := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			Content:   "checking pods",
			ToolCalls: []llms.ToolCall{{ID: "call_1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "kubectl_execute"}}},
		}},
	}
	assert.False(t, isEmptyCompletion(completion))
}
