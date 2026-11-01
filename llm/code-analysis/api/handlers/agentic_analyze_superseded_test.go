package handlers

import (
	"encoding/json"
	"testing"

	"nudgebee/code-analysis-agent/agents"
)

func followupJSON(t *testing.T, result *agents.PRFollowupResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(followupAnalysisResult(AgenticAnalyzeRequest{PRURL: "https://github.com/o/r/pull/1"}, result))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A superseded run emits the two contract fields and still reports no_op, so an
// api-server that predates the fields treats it like any other no-op run.
func TestFollowupAnalysisResultSupersededFields(t *testing.T) {
	out := followupJSON(t, &agents.PRFollowupResult{Success: true, NoOp: true, Superseded: true, AssessedHeadSHA: "abc123"})
	if out["followup_superseded"] != true {
		t.Fatalf("followup_superseded = %v, want true", out["followup_superseded"])
	}
	if out["followup_assessed_head_sha"] != "abc123" {
		t.Fatalf("followup_assessed_head_sha = %v, want abc123", out["followup_assessed_head_sha"])
	}
	if out["execution_status"] != "no_op" {
		t.Fatalf("execution_status = %v, want no_op", out["execution_status"])
	}
}

func TestFollowupAnalysisResultOmitsSupersededFieldsWhenUnset(t *testing.T) {
	for name, result := range map[string]*agents.PRFollowupResult{
		"no_op":   {Success: true, NoOp: true},
		"success": {Success: true},
		"failed":  {Success: false},
	} {
		t.Run(name, func(t *testing.T) {
			out := followupJSON(t, result)
			for _, key := range []string{"followup_superseded", "followup_assessed_head_sha"} {
				if _, ok := out[key]; ok {
					t.Fatalf("%s must be omitted when unset, got %v", key, out[key])
				}
			}
		})
	}
}
