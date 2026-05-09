package planners

import (
	"testing"
)

func TestSalvageForcedSubmitInput_PopulatesWhenEmpty(t *testing.T) {
	p := &ReActPlanner{}
	step := &Step{
		Number: 30,
		Action: "submit_analysis",
		Status: "failed",
		Error:  "either (title + description) or (execution_status + execution_summary) required",
		ActionInput: map[string]any{
			"title":        "Post-budget summary",
			"description":  "Investigation reached max iterations",
			"file_path":    "notifications-server/clients/ms_teams_client.py",
			"requires_fix": true,
		},
	}

	p.salvageForcedSubmitInput(step)

	got, ok := p.lastSubmitAnalysisData.(map[string]any)
	if !ok {
		t.Fatalf("expected lastSubmitAnalysisData to be map[string]any, got %T", p.lastSubmitAnalysisData)
	}
	if got["title"] != "Post-budget summary" {
		t.Errorf("title not salvaged: got %v", got["title"])
	}
	if got["description"] != "Investigation reached max iterations" {
		t.Errorf("description not salvaged: got %v", got["description"])
	}
	if got["file_path"] != "notifications-server/clients/ms_teams_client.py" {
		t.Errorf("file_path not salvaged: got %v", got["file_path"])
	}
	if got["requires_fix"] != true {
		t.Errorf("requires_fix not salvaged: got %v", got["requires_fix"])
	}
}

func TestSalvageForcedSubmitInput_NoOpWhenAlreadyPopulated(t *testing.T) {
	existing := map[string]any{"title": "Earlier successful submit"}
	p := &ReActPlanner{lastSubmitAnalysisData: existing}
	step := &Step{
		Action:      "submit_analysis",
		ActionInput: map[string]any{"title": "Forced fallback"},
	}

	p.salvageForcedSubmitInput(step)

	got, ok := p.lastSubmitAnalysisData.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", p.lastSubmitAnalysisData)
	}
	if got["title"] != "Earlier successful submit" {
		t.Errorf("salvage overwrote existing data: got %v", got["title"])
	}
}

func TestSalvageForcedSubmitInput_StripsToolOutputs(t *testing.T) {
	p := &ReActPlanner{}
	step := &Step{
		Action: "submit_analysis",
		ActionInput: map[string]any{
			"title":         "x",
			"description":   "y",
			"_tool_outputs": map[string]any{"tool_call_rg_step_1": "matched"},
		},
	}

	p.salvageForcedSubmitInput(step)

	got := p.lastSubmitAnalysisData.(map[string]any)
	if _, present := got["_tool_outputs"]; present {
		t.Error("_tool_outputs leaked into salvaged data")
	}
	if got["title"] != "x" {
		t.Errorf("expected title preserved: got %v", got["title"])
	}
}

func TestSalvageForcedSubmitInput_NilStepIsNoOp(t *testing.T) {
	p := &ReActPlanner{}
	p.salvageForcedSubmitInput(nil)
	if p.lastSubmitAnalysisData != nil {
		t.Errorf("expected nil lastSubmitAnalysisData, got %v", p.lastSubmitAnalysisData)
	}
}

func TestSalvageForcedSubmitInput_NilActionInputIsNoOp(t *testing.T) {
	p := &ReActPlanner{}
	step := &Step{Action: "submit_analysis"} // ActionInput nil
	p.salvageForcedSubmitInput(step)
	if p.lastSubmitAnalysisData != nil {
		t.Errorf("expected nil lastSubmitAnalysisData, got %v", p.lastSubmitAnalysisData)
	}
}

func TestSalvageForcedSubmitInput_DoesNotMutateOriginalInput(t *testing.T) {
	p := &ReActPlanner{}
	original := map[string]any{
		"title":         "x",
		"_tool_outputs": "should remain in original",
	}
	step := &Step{Action: "submit_analysis", ActionInput: original}

	p.salvageForcedSubmitInput(step)

	if _, ok := original["_tool_outputs"]; !ok {
		t.Error("salvage mutated the source ActionInput map")
	}
}
