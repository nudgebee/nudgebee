package tools

import (
	"context"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/tools/core"
)

// stubTool is a minimal NBTool used to exercise the tracking wrapper.
type stubTool struct{ name string }

func (s *stubTool) Name() string                 { return s.name }
func (s *stubTool) Description() string          { return "stub" }
func (s *stubTool) InputSchema() core.ToolSchema { return core.ToolSchema{} }
func (s *stubTool) GetType() core.NBToolType     { return core.NBToolTypeTool }
func (s *stubTool) Execute(_ context.Context, _ map[string]any) core.NBToolResponse {
	return core.CreateSuccessResponse("ok", "ok", nil)
}

// TestLooksLikeToken_KeepsPathsAndURLs guards the redaction seam: a real
// high-entropy token must be flagged, but repo_url / working_directory /
// file_path values must NOT be (the old ratio heuristic blanked them wholesale,
// producing the "field-redacted twin" noise).
func TestLooksLikeToken_KeepsPathsAndURLs(t *testing.T) {
	w := &TrackedToolWrapper{}

	tokens := []string{
		"ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("b", 40),
		strings.Repeat("A1b2C3d4", 4), // 32 contiguous alphanumerics
	}
	for _, s := range tokens {
		if !w.looksLikeToken(s) {
			t.Errorf("expected %q to be flagged as a token", s)
		}
	}

	notTokens := []string{
		"/tmp/code-analysis-agent-2414360469/nudgebee-enterprise",
		"https://github.com/nudgebee/nudgebee-enterprise.git",
		"app/src/pages/api/auth/[...nextauth].ts",
		"origin/main",
		"rg --type go ldapLogin app-e2e-tests",
		"short",
	}
	for _, s := range notTokens {
		if w.looksLikeToken(s) {
			t.Errorf("expected %q NOT to be flagged as a token", s)
		}
	}
}

// TestSanitizeInput_KeepsPathsRedactsSecrets verifies the surviving tracked
// record keeps human-readable paths while still redacting credential-bearing
// keys and values with an embedded token.
func TestSanitizeInput_KeepsPathsRedactsSecrets(t *testing.T) {
	w := &TrackedToolWrapper{}
	in := map[string]any{
		"github_token":      "ghp_" + strings.Repeat("c", 36),
		"repo_url":          "https://github.com/o/r.git",
		"working_directory": "/tmp/code-analysis-agent-2414360469/r",
		"file_path":         "app/src/x.ts",
		"authed_url":        "https://x-access-token:ghp_" + strings.Repeat("d", 36) + "@github.com/o/r",
	}
	out := w.sanitizeInput(in)

	if out["github_token"] != "***REDACTED***" {
		t.Errorf("github_token not redacted: %v", out["github_token"])
	}
	if out["authed_url"] != "***REDACTED***" {
		t.Errorf("url with embedded token not redacted: %v", out["authed_url"])
	}
	for _, k := range []string{"repo_url", "working_directory", "file_path"} {
		if out[k] != in[k] {
			t.Errorf("%s should be preserved, got %v", k, out[k])
		}
	}
}

// TestSanitizeInput_DropsPlannerWorkingMemory verifies planner-injected keys
// never reach the tracked record: "__intention" is tracked separately as the
// invocation intention, and "_tool_outputs" (injected for submit_analysis) is a
// bulky echo of earlier tool outputs that blows past downstream persistence
// caps and renders as a raw dump in the UI.
func TestSanitizeInput_DropsPlannerWorkingMemory(t *testing.T) {
	w := &TrackedToolWrapper{}
	in := map[string]any{
		"__intention":   "resolve conflict markers",
		"_tool_outputs": map[string]string{"tool_call_file_view_step_10": strings.Repeat("x", 10000)},
		"answer":        "the conflict is in code_agent_pr.go",
	}
	out := w.sanitizeInput(in)

	for _, k := range []string{"__intention", "_tool_outputs"} {
		if _, present := out[k]; present {
			t.Errorf("%s should be dropped from tracked input", k)
		}
	}
	if out["answer"] != in["answer"] {
		t.Errorf("answer should be preserved, got %v", out["answer"])
	}
}

// TestTrackedWrapper_TracksExactlyOnce is the regression guard for the dual-write
// fix: a single Execute must record exactly one invocation on the shared tracker.
// (The planner's own tracking is suppressed for wrapped tools; see
// ReActPlanner.toolSelfTracks.)
func TestTrackedWrapper_TracksExactlyOnce(t *testing.T) {
	tracker := common.NewToolInvocationTracker("analysis-1")
	w := NewTrackedToolWrapper(&stubTool{name: "file_view"}, tracker, nil)

	w.Execute(context.Background(), map[string]any{"file_path": "x.go"})

	if got := tracker.GetInvocationCount(); got != 1 {
		t.Fatalf("expected exactly 1 tracked invocation, got %d", got)
	}
}

// The planner detects self-tracking tools via this interface; keep it satisfied.
var _ interface {
	GetTracker() *common.ToolInvocationTracker
} = (*TrackedToolWrapper)(nil)
