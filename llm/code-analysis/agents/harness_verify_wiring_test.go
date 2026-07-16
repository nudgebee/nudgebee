package agents

import (
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
)

// The fixer prompt must flip its verification instructions with the flag: harness
// mode tells the model NOT to run builds (the harness does), legacy mode keeps the
// self-verification workflow. A template syntax error would only surface at
// runtime, so render both variants here.
func TestCodeFixerTemplateHarnessVerifyVariants(t *testing.T) {
	loader := common.NewPromptLoader()
	base := map[string]any{
		"AuditFindings":        map[string]any{"root_cause": "x"},
		"OriginalQuery":        "fix it",
		"InvestigationHistory": "",
		"BuildConfig":          nil,
		"BuildVerifyEnabled":   true,
		"Skills":               "",
	}

	base["HarnessVerify"] = true
	harness, err := loader.LoadPrompt("code_fixer", base)
	if err != nil {
		t.Fatalf("render with HarnessVerify=true: %v", err)
	}
	if !strings.Contains(harness, "Do NOT run build") {
		t.Error("harness variant must tell the fixer not to self-verify")
	}
	if strings.Contains(harness, "Find the project root") {
		t.Error("harness variant must not include the self-build guessing instructions")
	}

	base["HarnessVerify"] = false
	legacy, err := loader.LoadPrompt("code_fixer", base)
	if err != nil {
		t.Fatalf("render with HarnessVerify=false: %v", err)
	}
	if !strings.Contains(legacy, "Find the project root") {
		t.Error("legacy variant must keep the self-verification workflow")
	}
	if strings.Contains(legacy, "Do NOT run build") {
		t.Error("legacy variant must not include harness instructions")
	}
}

// PR gating must be governed by the tri-state verification when present: failed
// blocks with evidence, unverified/verified allow, and advisory review rejection
// no longer blocks.
func TestShouldCreatePRVerificationGoverns(t *testing.T) {
	a := &OrchestratorAgent{logger: common.NewLogger("test", "", "", nil)}

	failed := map[string]any{
		"git_diff":     "diff --git a/x b/x",
		"verification": map[string]any{"status": "failed", "evidence": "main.go:6:1: missing import path"},
		"review":       map[string]any{"approved": true},
	}
	create, noChanges, reason := a.shouldCreatePR(failed)
	if create || noChanges {
		t.Fatalf("failed verification must block PR, got create=%v noChanges=%v", create, noChanges)
	}
	if !strings.Contains(reason, "missing import path") {
		t.Errorf("block reason must carry the build evidence, got: %q", reason)
	}

	unverifiedWithRejectedReview := map[string]any{
		"git_diff":     "diff --git a/x b/x",
		"verification": map[string]any{"status": "unverified", "reason": "no modules"},
		"review":       map[string]any{"approved": false, "feedback": "style nit"},
	}
	create, _, _ = a.shouldCreatePR(unverifiedWithRejectedReview)
	if !create {
		t.Error("advisory review rejection must not block PR when verification governs")
	}

	legacyRejected := map[string]any{
		"git_diff": "diff --git a/x b/x",
		"review":   map[string]any{"approved": false, "feedback": "broken"},
	}
	create, _, _ = a.shouldCreatePR(legacyRejected)
	if create {
		t.Error("legacy path (no verification key) must keep review gating")
	}
}
