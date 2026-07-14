package tools

import "testing"

// Git-archaeology answers cite commits or whole files — line_start 0 must be
// accepted as file/commit-level evidence. A hard >0 requirement made such runs
// fail the explore contract on every retry and die to the parse-error fallback.
func TestValidateExploreContract_CommitLevelCitation(t *testing.T) {
	p := SubmitAnalysisInput{
		Answer: "The /rpc/metrics schema changed in commit abc123.",
		Citations: []Citation{
			{FilePath: "api-server/services/api/handle_actions_metrics.go", LineStart: 0, Snippet: "commit abc123: renamed field X to Y"},
		},
	}
	if errs := validateExploreContract(p); len(errs) != 0 {
		t.Fatalf("line_start=0 (commit-level) must pass, got: %v", errs)
	}
	p.Citations[0].LineStart = -1
	if errs := validateExploreContract(p); len(errs) == 0 {
		t.Fatalf("negative line_start must still fail")
	}
}
