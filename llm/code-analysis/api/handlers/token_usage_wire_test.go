package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/llm"
)

// collectTokenUsage must attach per-call records with snake_case keys and an
// aggregate equal to the sum of calls — the llm-server side prices each call
// individually and reconciles against the aggregate.
func TestCollectTokenUsage_SerializesCalls(t *testing.T) {
	client := llm.NewUsageRecorder("run-model", "googleai")
	client.RecordCallUsage(llm.TokenUsageCall{PromptTokens: 150000, CompletionTokens: 500, CachedContentTokens: 90000, ThinkingTokens: 20, LatencySeconds: 1.5})
	client.RecordCallUsage(llm.TokenUsageCall{PromptTokens: 160000, CompletionTokens: 700})

	tu := collectTokenUsage(client, nil, nil)
	if tu == nil {
		t.Fatal("expected usage")
	}
	if len(tu.Calls) != 2 || tu.CallsDropped != 0 {
		t.Fatalf("expected 2 calls, got %+v", tu)
	}
	if tu.PromptTokens != 310000 || tu.ThinkingTokens != 20 {
		t.Errorf("aggregate must equal sum of calls, got %+v", tu)
	}

	raw, err := json.Marshal(tu)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"calls":[{`, `"prompt_tokens":150000`, `"latency_seconds":1.5`, `"thinking_tokens":20`, `"model":"run-model"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("serialized usage missing %s in %s", key, raw)
		}
	}
	if strings.Contains(string(raw), "calls_dropped") {
		t.Errorf("calls_dropped must be omitted when zero: %s", raw)
	}
}

// A shared client (non-zero pre-analysis snapshot) cannot attribute its call
// slice to one request — calls must be omitted so llm-server falls back to
// the single aggregate row instead of double counting.
func TestCollectTokenUsage_SharedClientOmitsCalls(t *testing.T) {
	client := llm.NewUsageRecorder("run-model", "googleai")
	client.RecordCallUsage(llm.TokenUsageCall{PromptTokens: 100, CompletionTokens: 10})
	before := client.SnapshotTokenUsage() // simulates prior spend on a shared client
	client.RecordCallUsage(llm.TokenUsageCall{PromptTokens: 50, CompletionTokens: 5})

	tu := collectTokenUsage(client, before, nil)
	if tu == nil {
		t.Fatal("expected usage delta")
	}
	if tu.PromptTokens != 50 {
		t.Errorf("delta must isolate this request, got %+v", tu)
	}
	if tu.Calls != nil {
		t.Errorf("calls must be omitted for shared clients, got %d", len(tu.Calls))
	}
}
