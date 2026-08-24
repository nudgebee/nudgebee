package handlers

import (
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
)

// TestConvertTrackedInvocations_PrefersObservation verifies the human-readable
// observation is surfaced as the step output instead of the raw tool data blob,
// falling back to raw Output when no observation was recorded.
func TestConvertTrackedInvocations_PrefersObservation(t *testing.T) {
	ah := &AgenticAnalyzeHandler{}
	tracked := []common.TrackedToolInvocation{
		{
			ToolName: "cli",
			Status:   "success",
			Output:   map[string]any{"result": map[string]any{"stdout": "total 0"}},
			Metadata: map[string]any{"observation": "Executed: ls -l\nExit Code: 0\nOutput:\ntotal 0"},
		},
		{
			ToolName: "raw_tool",
			Status:   "success",
			Output:   "raw-output-no-observation",
		},
	}

	got := ah.convertTrackedInvocations(tracked)
	if out, _ := got[0].Output.(string); out == "" || !strings.Contains(out, "Executed: ls -l") {
		t.Errorf("expected observation as output, got %v", got[0].Output)
	}
	if out, _ := got[1].Output.(string); out != "raw-output-no-observation" {
		t.Errorf("expected raw output fallback, got %v", got[1].Output)
	}
}

// TestConvertTrackedInvocations_ThoughtAndStepNumber verifies the planner's
// reasoning (stored as the tracked "intention") and step ordinal are surfaced
// on the response so callers can render per-step reasoning and stable identity.
func TestConvertTrackedInvocations_ThoughtAndStepNumber(t *testing.T) {
	ah := &AgenticAnalyzeHandler{}
	tracked := []common.TrackedToolInvocation{
		{
			ToolName:   "repo_clone",
			Status:     "success",
			StepNumber: 1,
			Metadata:   map[string]any{"intention": "Need the source to investigate"},
		},
		{
			ToolName:   "file_view",
			Status:     "running",
			StepNumber: 2,
			// no intention → empty thought, must not panic
		},
	}

	got := ah.convertTrackedInvocations(tracked)
	if len(got) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(got))
	}
	if got[0].Thought != "Need the source to investigate" {
		t.Errorf("thought not propagated: %q", got[0].Thought)
	}
	if got[0].StepNumber != 1 || got[1].StepNumber != 2 {
		t.Errorf("step numbers not propagated: %d, %d", got[0].StepNumber, got[1].StepNumber)
	}
	if got[1].Thought != "" {
		t.Errorf("expected empty thought when intention absent, got %q", got[1].Thought)
	}
}

// TestProjectLiveInvocations_TruncateAndScrub verifies the live /status
// projection truncates large outputs and scrubs credentials.
func TestProjectLiveInvocations_TruncateAndScrub(t *testing.T) {
	ah := &AgenticAnalyzeHandler{}

	bigOutput := strings.Repeat("A", liveOutputPreviewCap+5000)
	tracked := []common.TrackedToolInvocation{
		{
			ToolName: "shell",
			Status:   "success",
			Output:   bigOutput,
		},
		{
			ToolName: "config_read",
			Status:   "success",
			Output:   `{"token": "ghp_abcdefghijklmnopqrstuvwxyz0123456789"}`,
		},
	}

	got := ah.projectLiveInvocations(tracked)
	if len(got) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(got))
	}

	out0, _ := got[0].Output.(string)
	if len(out0) > liveOutputPreviewCap+len("…[truncated]") {
		t.Errorf("output not truncated: len=%d", len(out0))
	}
	if !strings.HasSuffix(out0, "[truncated]") {
		t.Errorf("expected truncation marker on truncated output")
	}

	out1, _ := got[1].Output.(string)
	if strings.Contains(out1, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Errorf("credential not scrubbed from live output: %q", out1)
	}
}

// TestAttachTracker_StatusStreamsInvocations verifies the tracker bridges into
// the analysis state so /status can read invocations while still running.
func TestAttachTracker_StatusStreamsInvocations(t *testing.T) {
	analysisID := "analysis_test_1"
	common.InitAnalysis(analysisID)
	t.Cleanup(func() { common.CleanupAnalysis(analysisID) })

	tracker := common.NewToolInvocationTracker(analysisID)
	common.AttachTracker(analysisID, tracker)

	// Simulate a running step.
	tracker.StartInvocationWithIntention("repo_clone", map[string]any{"url": "x"}, "clone it")

	snap := common.Snapshot(analysisID)
	if snap == nil {
		t.Fatal("snapshot nil")
	}
	if snap.Status != "running" {
		t.Errorf("expected running, got %q", snap.Status)
	}
	if snap.Tracker == nil {
		t.Fatal("tracker not attached to state")
	}
	invs := snap.Tracker.GetInvocations()
	if len(invs) != 1 || invs[0].ToolName != "repo_clone" {
		t.Fatalf("expected 1 live invocation while running, got %+v", invs)
	}
	if got, _ := invs[0].Metadata["intention"].(string); got != "clone it" {
		t.Errorf("intention/thought not captured: %q", got)
	}
}
