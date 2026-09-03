package openaiclient

import "testing"

func idx(i int) *int { return &i }

// A gateway that echoes "type":"function" on the argument fragment used to be
// treated as a second, nameless tool call: the caller then dispatched the named
// tool with an empty input. Observed with OpenRouter serving gpt-5.6-terra.
func TestUpdateToolCalls_TypedArgumentFragmentMergesIntoItsCall(t *testing.T) {
	var tools []ToolCall
	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "call_1", Type: "function", Index: idx(0),
		Function: ToolFunction{Name: "kubectl_execute"},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Type: "function", Index: idx(0),
		Function: ToolFunction{Arguments: `{"command":"kubectl get pods -n nudgebee"}`},
	}})

	if len(tools) != 1 {
		t.Fatalf("want 1 merged tool call, got %d", len(tools))
	}
	if tools[0].Function.Name != "kubectl_execute" {
		t.Errorf("name = %q", tools[0].Function.Name)
	}
	if got, want := tools[0].Function.Arguments, `{"command":"kubectl get pods -n nudgebee"}`; got != want {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}

// Parallel tool calls: fragments must land on their own call, not on whichever
// entry happens to be last.
func TestUpdateToolCalls_ParallelCallsMergeByIndex(t *testing.T) {
	var tools []ToolCall
	_, tools = updateToolCalls(tools, []*ToolCall{
		{ID: "a", Type: "function", Index: idx(0), Function: ToolFunction{Name: "logs"}},
		{ID: "b", Type: "function", Index: idx(1), Function: ToolFunction{Name: "metrics"}},
	})
	_, tools = updateToolCalls(tools, []*ToolCall{{Index: idx(0), Function: ToolFunction{Arguments: `{"a":1}`}}})
	_, tools = updateToolCalls(tools, []*ToolCall{{Index: idx(1), Function: ToolFunction{Arguments: `{"b":2}`}}})

	if len(tools) != 2 {
		t.Fatalf("want 2 tool calls, got %d", len(tools))
	}
	if tools[0].Function.Arguments != `{"a":1}` || tools[1].Function.Arguments != `{"b":2}` {
		t.Errorf("arguments landed on the wrong call: %q / %q",
			tools[0].Function.Arguments, tools[1].Function.Arguments)
	}
}

// No index (older/other gateways) still appends to the most recent call.
func TestUpdateToolCalls_NoIndexFallsBackToLastCall(t *testing.T) {
	var tools []ToolCall
	_, tools = updateToolCalls(tools, []*ToolCall{{ID: "a", Type: "function", Function: ToolFunction{Name: "logs"}}})
	_, tools = updateToolCalls(tools, []*ToolCall{{Function: ToolFunction{Arguments: `{"x":1}`}}})

	if len(tools) != 1 || tools[0].Function.Arguments != `{"x":1}` {
		t.Fatalf("fallback merge failed: %d calls, args=%q", len(tools), tools[0].Function.Arguments)
	}
}

// vLLM (Vertex-hosted Qwen) closes every tool call with an empty trailer delta:
// {type:"function", index:N, function:{name:null, arguments:""}}. That trailer
// used to fail the fragment test on Arguments != "" and fall through to append,
// creating one phantom, nameless tool call per real call; the planner rejected
// each phantom ("skipping invalid tool name") every iteration until the agent
// died with "agent not finished before max iterations". Captured 2026-09-03
// from mg-endpoint-787d7c83 (Qwen/Qwen3.6-35B-A3B-FP8).
func TestUpdateToolCalls_EmptyTrailerDeltaIsNotANewCall(t *testing.T) {
	var tools []ToolCall
	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "chatcmpl-tool-96c358", Type: "function", Index: idx(0),
		Function: ToolFunction{Name: "kubectl_execute"},
	}})
	for _, frag := range []string{`{"command": `, `"kubectl get`, ` pods -n`, ` namespace-231"`, `}`} {
		_, tools = updateToolCalls(tools, []*ToolCall{{
			Type: "function", Index: idx(0), Function: ToolFunction{Arguments: frag},
		}})
	}
	// the trailer: nameless AND argument-less
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Type: "function", Index: idx(0), Function: ToolFunction{},
	}})

	if len(tools) != 1 {
		t.Fatalf("want 1 tool call, got %d (empty trailer became a phantom)", len(tools))
	}
	if tools[0].Function.Name != "kubectl_execute" {
		t.Errorf("name = %q", tools[0].Function.Name)
	}
	if got, want := tools[0].Function.Arguments, `{"command": "kubectl get pods -n namespace-231"}`; got != want {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}

// The trailer of a FIRST call must not become a phantom even when a second call
// follows it — the phantom would sit between the two real calls.
func TestUpdateToolCalls_EmptyTrailerBetweenParallelCalls(t *testing.T) {
	var tools []ToolCall
	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "a", Type: "function", Index: idx(0), Function: ToolFunction{Name: "logs"},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{Type: "function", Index: idx(0), Function: ToolFunction{Arguments: `{"a":1}`}}})
	_, tools = updateToolCalls(tools, []*ToolCall{{Type: "function", Index: idx(0), Function: ToolFunction{}}}) // trailer
	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "b", Type: "function", Index: idx(1), Function: ToolFunction{Name: "metrics"},
	}})
	_, tools = updateToolCalls(tools, []*ToolCall{{Type: "function", Index: idx(1), Function: ToolFunction{Arguments: `{"b":2}`}}})

	if len(tools) != 2 {
		t.Fatalf("want 2 tool calls, got %d", len(tools))
	}
	if tools[0].Function.Name != "logs" || tools[1].Function.Name != "metrics" {
		t.Errorf("names = %q, %q", tools[0].Function.Name, tools[1].Function.Name)
	}
}

// vLLM's parser sometimes re-emits the whole arguments object as one final
// JSON-quoted delta after the incremental fragments; appending it used to
// corrupt completed arguments into `{...}"{\"escaped\":...}"`. Captured live
// from the Qwen endpoint 2026-09-03.
func TestUpdateToolCalls_DuplicateFullArgumentsTrailerIsDropped(t *testing.T) {
	want := `{"command": "kubectl get pods -n namespace-231"}`
	var tools []ToolCall
	_, tools = updateToolCalls(tools, []*ToolCall{{
		ID: "call_1", Type: "function", Index: idx(0),
		Function: ToolFunction{Name: "kubectl_execute"},
	}})
	for _, frag := range []string{`{"command": `, `"kubectl get pods -n namespace-231"`, `}`} {
		_, tools = updateToolCalls(tools, []*ToolCall{{
			Type: "function", Index: idx(0), Function: ToolFunction{Arguments: frag},
		}})
	}
	// the duplicate trailer: full arguments again, JSON-quoted
	_, tools = updateToolCalls(tools, []*ToolCall{{
		Type: "function", Index: idx(0),
		Function: ToolFunction{Arguments: `"{\"command\": \"kubectl get pods -n namespace-231\"}"`},
	}})

	if len(tools) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(tools))
	}
	if got := tools[0].Function.Arguments; got != want {
		t.Errorf("arguments corrupted by duplicate trailer:\n got %q\nwant %q", got, want)
	}
}
