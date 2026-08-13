package handlers

import (
	"strings"
	"testing"
)

// The relevance validator has measured false negatives (it rejected an analysis
// whose own reasoning acknowledged the answer was correct). Its verdict must be
// advisory: annotate, never discard.
func TestApplyRelevanceAdvisory_PreservesAnalysis(t *testing.T) {
	resp := &AnalysisResult{
		Title:       "Database Connection Pool Exhaustion in api-server",
		Description: "Pool limits are set in rdbms.go via ServiceDBMaxConnection.",
		FilePath:    "api-server/services/internal/database/rdbms.go",
		GitDiff:     "diff --git a/x b/x",
	}
	check := &RelevanceCheckResult{
		IsRelevant:      false,
		Reasoning:       "partially relevant but does not name a root cause",
		Recommendation:  "refocus on the root cause",
		ConfidenceLevel: "medium",
	}

	applyRelevanceAdvisory(resp, check)

	// Original findings must survive untouched.
	if resp.Title != "Database Connection Pool Exhaustion in api-server" {
		t.Errorf("title was replaced: %q", resp.Title)
	}
	if !strings.Contains(resp.Description, "ServiceDBMaxConnection") {
		t.Errorf("original description lost: %q", resp.Description)
	}
	if resp.FilePath != "api-server/services/internal/database/rdbms.go" || resp.GitDiff == "" {
		t.Errorf("file path / diff lost")
	}
	// Advisory must be visible with the validator's reasoning.
	if !strings.Contains(resp.Description, "relevance check") ||
		!strings.Contains(resp.Description, "partially relevant but does not name a root cause") {
		t.Errorf("advisory note missing: %q", resp.Description)
	}
	// The old destructive placeholder must be gone for good.
	if strings.Contains(resp.Title, "Manual Review Required") {
		t.Errorf("placeholder title leaked back in")
	}
}

func TestApplyRelevanceAdvisory_NilSafe(t *testing.T) {
	applyRelevanceAdvisory(nil, &RelevanceCheckResult{})
	applyRelevanceAdvisory(&AnalysisResult{}, nil) // must not panic
}
