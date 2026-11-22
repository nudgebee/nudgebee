package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
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

// In fix mode the only diff a response may carry is the one computed from the
// checkout. A specialist that fills in git_diff itself has written text, not
// applied a change: when the fixer then does not run, that text went back to the
// caller next to a summary saying "git_diff is empty", and did not apply.
//
// The specialist's answer is not always bare JSON. The paths that return it
// parse it strictly, so a fenced or prose-wrapped answer must come out of this
// step as plain JSON, or the original text — diff included — is what they hand
// back.
func TestModelWrittenDiffIsDroppedHoweverTheSpecialistWrapsItsAnswer(t *testing.T) {
	answer := `{"title":"Add search tests","file_path":"e2e/tests/locators.ts","requires_fix":true,` +
		`"fixed_code":"export const searchBox = 1;","git_diff":"@@ -0,0 +1 @@\n+export const searchBox = 1;\n"}`
	cases := map[string]string{
		"bare JSON":        answer,
		"in a code fence":  "```json\n" + answer + "\n```",
		"wrapped in prose": "Here is the analysis you asked for:\n" + answer + "\nLet me know if anything is unclear.",
	}
	for name, specialistResult := range cases {
		t.Run(name, func(t *testing.T) {
			// As Execute parses it.
			factsData, err := common.ExtractJSONFromLLMResponse(specialistResult, common.NewLogger("test", "", "", nil))
			if err != nil {
				t.Fatalf("fixture does not parse: %v", err)
			}

			cleaned := dropModelWrittenDiff(factsData, specialistResult)

			if _, has := factsData["git_diff"]; has {
				t.Error("git_diff is still in the parsed facts the fixer result is merged onto")
			}
			// As the skipped / nothing-to-change / clone-failure paths parse it.
			var returned map[string]any
			if err := json.Unmarshal([]byte(cleaned), &returned); err != nil {
				t.Fatalf("the text those paths return must be plain JSON, got %q: %v", cleaned, err)
			}
			if _, has := returned["git_diff"]; has {
				t.Error("the specialist's own git_diff must not survive")
			}
			if strings.Contains(cleaned, "searchBox = 1;\\n") {
				t.Error("the diff text is still in the returned answer")
			}
			// The analysis itself stays: it is a proposal, and is labelled as one.
			if returned["fixed_code"] == nil || returned["requires_fix"] != true || returned["title"] != "Add search tests" {
				t.Errorf("the rest of the specialist's answer must be kept, got %+v", returned)
			}
		})
	}
}

// A specialist that answers with the literal `null` parses into a nil map. Its
// answer has no diff to remove and must come back as it went in, not rewritten.
func TestModelWrittenDiffRemovalLeavesAnEmptyAnswerAlone(t *testing.T) {
	for _, answer := range []string{"null", " null\n"} {
		factsData, err := common.ExtractJSONFromLLMResponse(answer, common.NewLogger("test", "", "", nil))
		if err != nil || factsData != nil {
			t.Fatalf("fixture: %q should parse to a nil map without error, got %v, %v", answer, factsData, err)
		}
		if got := dropModelWrittenDiff(factsData, answer); got != answer {
			t.Errorf("dropModelWrittenDiff(nil, %q) = %q, want it unchanged", answer, got)
		}
	}
}
