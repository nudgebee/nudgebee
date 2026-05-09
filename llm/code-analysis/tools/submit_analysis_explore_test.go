package tools

import (
	"context"
	"strings"
	"testing"
)

func TestValidateExploreContract_RejectsMissingAnswer(t *testing.T) {
	in := SubmitAnalysisInput{
		Citations: []Citation{
			{FilePath: "foo.go", LineStart: 10, Snippet: "x := 1"},
		},
	}
	errs := validateExploreContract(in)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "answer") {
		t.Errorf("expected error to mention `answer`, got: %s", errs[0])
	}
}

func TestValidateExploreContract_RejectsMissingCitations(t *testing.T) {
	in := SubmitAnalysisInput{
		Answer: "The default is 100.",
	}
	errs := validateExploreContract(in)
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "citations") {
		t.Errorf("expected error to mention `citations`, got: %s", errs[0])
	}
}

func TestValidateExploreContract_RejectsAnswerThatIsJustWhitespace(t *testing.T) {
	in := SubmitAnalysisInput{
		Answer:    "   \n\t  ",
		Citations: []Citation{{FilePath: "foo.go", LineStart: 1, Snippet: "x"}},
	}
	errs := validateExploreContract(in)
	if len(errs) == 0 || !strings.Contains(errs[0], "answer") {
		t.Errorf("expected whitespace-only answer to fail with answer error, got: %v", errs)
	}
}

func TestValidateExploreContract_RejectsCitationWithoutFilePath(t *testing.T) {
	in := SubmitAnalysisInput{
		Answer: "x",
		Citations: []Citation{
			{FilePath: "", LineStart: 5, Snippet: "y"},
		},
	}
	errs := validateExploreContract(in)
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing file_path")
	}
}

func TestValidateExploreContract_RejectsCitationWithoutLineStart(t *testing.T) {
	in := SubmitAnalysisInput{
		Answer: "x",
		Citations: []Citation{
			{FilePath: "foo.go", LineStart: 0, Snippet: "y"},
		},
	}
	errs := validateExploreContract(in)
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing line_start")
	}
}

func TestValidateExploreContract_RejectsCitationWithLineEndBeforeStart(t *testing.T) {
	in := SubmitAnalysisInput{
		Answer: "x",
		Citations: []Citation{
			{FilePath: "foo.go", LineStart: 50, LineEnd: 40, Snippet: "y"},
		},
	}
	errs := validateExploreContract(in)
	if len(errs) == 0 {
		t.Fatal("expected validation error when line_end precedes line_start")
	}
	if !strings.Contains(errs[0], "line_end") {
		t.Errorf("expected error to mention line_end, got: %s", errs[0])
	}
}

func TestValidateExploreContract_AcceptsCitationWithZeroLineEnd(t *testing.T) {
	// line_end is optional and defaults to line_start when zero — common case
	// for single-line citations. Must not be flagged.
	in := SubmitAnalysisInput{
		Answer: "x",
		Citations: []Citation{
			{FilePath: "foo.go", LineStart: 42, LineEnd: 0, Snippet: "y"},
		},
	}
	if errs := validateExploreContract(in); len(errs) != 0 {
		t.Fatalf("expected single-line citation (line_end=0) to be valid, got: %v", errs)
	}
}

func TestValidateExploreContract_RejectsCitationWithoutSnippet(t *testing.T) {
	in := SubmitAnalysisInput{
		Answer: "x",
		Citations: []Citation{
			{FilePath: "foo.go", LineStart: 1, Snippet: ""},
		},
	}
	errs := validateExploreContract(in)
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing snippet")
	}
}

func TestValidateExploreContract_AcceptsWellFormedSubmission(t *testing.T) {
	in := SubmitAnalysisInput{
		Answer:          "The default is 100.",
		Citations:       []Citation{{FilePath: "deploy/values.yaml", LineStart: 124, LineEnd: 125, Snippet: "expr: pg_stat_activity_count > 100", Note: "alert threshold"}},
		ConfidenceScore: "High",
	}
	errs := validateExploreContract(in)
	if len(errs) != 0 {
		t.Fatalf("expected no errors on well-formed submission, got: %v", errs)
	}
}

func TestTruncate_RuneSafe(t *testing.T) {
	// Each emoji is multi-byte in UTF-8; byte-based truncation would split a
	// rune and produce invalid UTF-8. Rune-based truncation must not.
	in := "🦊🦊🦊🦊🦊"
	got := truncate(in, 3)
	// truncate(n>3) appends "..." after n-3 runes; truncate(n<=3) takes first n runes raw.
	if got != "🦊🦊🦊" {
		t.Fatalf("expected 3 foxes, got %q", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("output contains replacement char — byte-split bug: %q", got)
		}
	}
}

func TestTruncate_AppendsEllipsis(t *testing.T) {
	in := "hello world this is a long sentence"
	got := truncate(in, 10)
	if got != "hello w..." {
		t.Errorf("expected ellipsis-truncated output, got %q", got)
	}
}

func TestModeFromContext_RoundTrip(t *testing.T) {
	ctx := WithMode(context.Background(), "explore")
	if got := modeFromContext(ctx); got != "explore" {
		t.Errorf("expected 'explore', got %q", got)
	}
	if got := modeFromContext(context.Background()); got != "" {
		t.Errorf("expected empty for unmarked context, got %q", got)
	}
}

// TestSubmitAnalysisExecute_ExploreModeRejectsNonContract verifies the
// validator hooks into the real Execute() path and returns an actionable
// error response that the ReAct planner can surface as a retry signal.
func TestSubmitAnalysisExecute_ExploreModeRejectsNonContract(t *testing.T) {
	tool := NewSubmitAnalysisTool()
	ctx := WithMode(context.Background(), "explore")
	resp := tool.Execute(ctx, map[string]any{
		"title":       "Connection limit",
		"description": "Says 100 in values.yaml",
		// No `answer`, no `citations` — must fail.
	})
	if resp.Status != "error" {
		t.Fatalf("expected explore-mode submission without answer/citations to fail; got success: %+v", resp)
	}
	combined := resp.Error + "\n" + resp.Observation
	if !strings.Contains(combined, "answer") || !strings.Contains(combined, "citations") {
		t.Errorf("error/observation should explain the contract; got error=%q observation=%q", resp.Error, resp.Observation)
	}
}

func TestSubmitAnalysisExecute_ExploreModeAcceptsContract(t *testing.T) {
	tool := NewSubmitAnalysisTool()
	ctx := WithMode(context.Background(), "explore")
	resp := tool.Execute(ctx, map[string]any{
		"title":       "Connection limit",
		"description": "details",
		"answer":      "The Bitnami chart defaults max_connections to 100; not overridden in Nudgebee values.yaml.",
		"citations": []any{
			map[string]any{
				"file_path":  "deploy/kubernetes/nudgebee/values.yaml",
				"line_start": 124,
				"line_end":   125,
				"snippet":    "expr: pg_stat_activity_count > 100",
				"note":       "alert threshold reflects chart-default max_connections",
			},
		},
	})
	if resp.Status == "error" {
		t.Fatalf("expected well-formed explore submission to succeed, got error: %s", resp.Error)
	}
}

// TestSubmitAnalysisExecute_FixModeIgnoresExploreContract ensures the
// validator only applies in explore mode — fix-mode submissions still flow
// through the existing ErrorRCA / CodeFixer validation paths unchanged.
func TestSubmitAnalysisExecute_FixModeIgnoresExploreContract(t *testing.T) {
	tool := NewSubmitAnalysisTool()
	ctx := WithMode(context.Background(), "fix")
	resp := tool.Execute(ctx, map[string]any{
		"title":       "Bump connection limit",
		"description": "Increase max_connections to 500.",
		// No answer, no citations — should still pass in fix mode.
	})
	if resp.Status == "error" {
		t.Fatalf("fix-mode submission must not be blocked by the explore contract; got error: %s", resp.Error)
	}
}
