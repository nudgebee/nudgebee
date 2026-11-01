package agents

import (
	"encoding/json"
	"testing"
)

// The value-refresh instruction arrives in the @agent_code_2 JSON and has to
// leave for code-analysis as followup_instruction; the regular review followup
// sends none and its payload must not change.
func TestAddFollowupPassThrough(t *testing.T) {
	var withInstruction CodeAgent2Request
	if err := json.Unmarshal([]byte(`{"followup":true,"pr_url":"https://github.com/o/r/pull/7","query":"generic","instruction":"  set cpu to 200m  "}`), &withInstruction); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	addFollowupPassThrough(got, withInstruction)
	if got["followup_instruction"] != "set cpu to 200m" {
		t.Errorf("followup_instruction = %v, want the trimmed instruction", got["followup_instruction"])
	}

	var regular CodeAgent2Request
	if err := json.Unmarshal([]byte(`{"followup":true,"pr_url":"https://github.com/o/r/pull/7","query":"generic","addressed_comments":[]}`), &regular); err != nil {
		t.Fatal(err)
	}
	got = map[string]any{}
	addFollowupPassThrough(got, regular)
	if len(got) != 0 {
		t.Errorf("regular followup payload gained fields: %v", got)
	}

	regular.AddressedComments = json.RawMessage(`[{"source":"inline","comment_id":1,"action":"fixed"}]`)
	got = map[string]any{}
	addFollowupPassThrough(got, regular)
	if _, ok := got["addressed_comments"]; !ok {
		t.Error("addressed_comments no longer forwarded")
	}
}
