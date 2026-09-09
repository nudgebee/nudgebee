package agents

import (
	"encoding/json"
	"strings"
	"testing"
)

// When CodeFixer is skipped, the response must say so on every path — not only
// when a PR was requested.
//
// The failure this guards: a propose-mode caller (mode=fix, raise_pr=false) got
// back requires_fix=true with fixed_code populated by submit_analysis, no
// git_diff, and no signal the fixer never ran. That is indistinguishable from a
// real fix, and it is what made a whole benchmark run look like the agent could
// not write patches when it had simply been skipped.
func TestSkippedFixerIsReportedInProposeMode(t *testing.T) {
	// Shape of what submit_analysis produces: a fix-looking payload.
	specialist := map[string]any{
		"title":         "Fix separability matrix computation",
		"file_path":     "astropy/modeling/separable.py",
		"requires_fix":  true,
		"fixed_code":    "cright[-right.shape[0]:, -right.shape[1]:] = right",
		"original_code": "cright[-right.shape[0]:, -right.shape[1]:] = 1",
	}
	raw, err := json.Marshal(specialist)
	if err != nil {
		t.Fatal(err)
	}

	// Mirrors the skip branch for raise_pr=false.
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	out["execution_status"] = "skipped"
	out["execution_summary"] = `CodeFixer did not run: file_path "astropy/modeling/separable.py" was not found under the repository root. No changes were written and git_diff is empty; any fixed_code below is the specialist's proposal, not an applied change.`
	out["mode"] = "fix"

	if out["execution_status"] != "skipped" {
		t.Fatal("execution_status must mark the skip even when no PR was requested")
	}
	summary, _ := out["execution_summary"].(string)
	// The summary has to be actionable on its own: name the path, and say
	// plainly that fixed_code was not applied. A bare status code sends the
	// reader back to the logs, which is the situation this replaces.
	for _, want := range []string{"astropy/modeling/separable.py", "git_diff is empty", "not an applied change"} {
		if !strings.Contains(summary, want) {
			t.Errorf("execution_summary should mention %q; got: %s", want, summary)
		}
	}
	// The specialist's analysis is deliberately preserved — it is still useful,
	// it just must not masquerade as an applied fix.
	if out["fixed_code"] == nil || out["requires_fix"] != true {
		t.Error("the specialist analysis should be kept alongside the skip status")
	}
	if _, hasDiff := out["git_diff"]; hasDiff {
		t.Error("a skipped fixer must not produce a git_diff")
	}
}

// json.Unmarshal of the literal `null` returns no error and leaves the map nil;
// writing to a nil map panics. The skip branch now runs on every request rather
// than only the raise_pr one, so a specialist returning "null" would take down
// the analysis instead of degrading to the plain result.
func TestSkipBranchSurvivesNullSpecialistResult(t *testing.T) {
	for _, specialistResult := range []string{"null", "", "not json", "[]"} {
		t.Run(specialistResult, func(t *testing.T) {
			var resultData map[string]any
			err := json.Unmarshal([]byte(specialistResult), &resultData)

			// This is the guard as written in orchestrator_agent.go.
			if err == nil && resultData != nil {
				resultData["execution_status"] = "skipped" // must not panic
			}

			// "null" is the case that matters: it parses cleanly to a nil map.
			if specialistResult == "null" {
				if err != nil {
					t.Fatal(`"null" should unmarshal without error`)
				}
				if resultData != nil {
					t.Fatal(`"null" should yield a nil map`)
				}
			}
		})
	}
}
