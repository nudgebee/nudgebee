package azureclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func idx(i int) *int { return &i }

// A gateway that echoes a type on every fragment used to defeat the old
// Type == "" check, so each argument chunk started a fresh call and the named
// call was dispatched with an empty input.
func TestUpdateToolCalls_MergesTypedArgumentFragments(t *testing.T) {
	var tools []ToolCall

	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "call_1", Type: "function", Index: idx(0),
		Function: ToolFunction{Name: "kubectl_execute"},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Type: "function", Index: idx(0),
		Function: ToolFunction{Arguments: `{"command":`},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Type: "function", Index: idx(0),
		Function: ToolFunction{Arguments: `"kubectl get pods"}`},
	}})

	require.Len(t, tools, 1, "argument fragments must not create new tool calls")
	assert.Equal(t, "kubectl_execute", tools[0].Function.Name)
	assert.Equal(t, `{"command":"kubectl get pods"}`, tools[0].Function.Arguments)
}

// Parallel calls interleave their fragments; merging into "the last one" would
// concatenate both sets of arguments onto whichever call arrived most recently.
func TestUpdateToolCalls_RoutesParallelCallsByIndex(t *testing.T) {
	var tools []ToolCall

	_, tools = updateToolCalls(tools, []*ToolCall{
		{ID: "a", Type: "function", Index: idx(0), Function: ToolFunction{Name: "logs"}},
		{ID: "b", Type: "function", Index: idx(1), Function: ToolFunction{Name: "metrics"}},
	})
	_, tools = updateToolCalls(tools, []*ToolCall{{Index: idx(1), Function: ToolFunction{Arguments: `{"svc":"b"}`}}})
	_, tools = updateToolCalls(tools, []*ToolCall{{Index: idx(0), Function: ToolFunction{Arguments: `{"svc":"a"}`}}})

	require.Len(t, tools, 2)
	assert.Equal(t, `{"svc":"a"}`, tools[0].Function.Arguments, "index 0 fragment landed on the wrong call")
	assert.Equal(t, `{"svc":"b"}`, tools[1].Function.Arguments, "index 1 fragment landed on the wrong call")
}

// Index is matched by VALUE, not used as a slice offset: a gateway may start at
// a non-zero index, which would read past the end of the slice.
func TestUpdateToolCalls_HandlesNonZeroBasedIndex(t *testing.T) {
	var tools []ToolCall

	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "x", Type: "function", Index: idx(7),
		Function: ToolFunction{Name: "kubectl_execute"},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Index: idx(7), Function: ToolFunction{Arguments: `{"command":"get ns"}`},
	}})

	require.Len(t, tools, 1)
	assert.Equal(t, `{"command":"get ns"}`, tools[0].Function.Arguments)
}

// Providers that omit index entirely must still work — fall back to the most
// recent call rather than dropping the fragment.
func TestUpdateToolCalls_FallsBackWhenIndexAbsent(t *testing.T) {
	var tools []ToolCall

	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "y", Type: "function", Function: ToolFunction{Name: "events"},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Function: ToolFunction{Arguments: `{"ns":"prod"}`},
	}})

	require.Len(t, tools, 1)
	assert.Equal(t, `{"ns":"prod"}`, tools[0].Function.Arguments)
}

// The exported helper must behave identically to updateToolCalls — it used to
// carry its own copy of the buggy logic.
func TestStreamingChatResponseTools_DelegatesToUpdateToolCalls(t *testing.T) {
	var tools []ToolCall

	_, tools = StreamingChatResponseTools(tools, []*ToolCall{{
		ID: "z", Type: "function", Index: idx(0),
		Function: ToolFunction{Name: "kubectl_execute"},
	}})
	_, tools = StreamingChatResponseTools(tools, []*ToolCall{{
		Type: "function", Index: idx(0),
		Function: ToolFunction{Arguments: `{"command":"get po"}`},
	}})

	require.Len(t, tools, 1, "helper must not re-introduce the split-call bug")
	assert.Equal(t, `{"command":"get po"}`, tools[0].Function.Arguments)
}

// vLLM closes every streamed tool call with an empty trailer delta
// {type:"function", index:N, function:{name:null, arguments:""}}; it must merge
// as a no-op, not append a phantom nameless tool call. Mirrors the same fix in
// llms/openai/internal/openaiclient.
func TestUpdateToolCalls_EmptyTrailerDeltaIsNotANewCall(t *testing.T) {
	var tools []ToolCall
	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "call_1", Type: "function", Index: idx(0),
		Function: ToolFunction{Name: "kubectl_execute"},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Type: "function", Index: idx(0),
		Function: ToolFunction{Arguments: `{"command":"kubectl get pods"}`},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Type: "function", Index: idx(0), Function: ToolFunction{},
	}}) // trailer

	if len(tools) != 1 {
		t.Fatalf("want 1 tool call, got %d (empty trailer became a phantom)", len(tools))
	}
	if tools[0].Function.Name != "kubectl_execute" || tools[0].Function.Arguments != `{"command":"kubectl get pods"}` {
		t.Errorf("got %q / %q", tools[0].Function.Name, tools[0].Function.Arguments)
	}
}
