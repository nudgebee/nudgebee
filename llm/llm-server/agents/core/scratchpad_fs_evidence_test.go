package core

import (
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestFileRecallHandle covers the FS-evidence-recall handle that replaces a
// compressed observation's dead-end with a live pointer to the workspace file
// the tool already saved. See WORKSPACE_FS_EVIDENCE_RECALL_SPEC.md.
func TestFileRecallHandle(t *testing.T) {
	tests := []struct {
		name     string
		refs     []toolcore.NBToolResponseReference
		wantFile string // "" means expect an empty handle
	}{
		{
			name:     "no references",
			refs:     nil,
			wantFile: "",
		},
		{
			name: "references but none are files",
			refs: []toolcore.NBToolResponseReference{
				{Type: "link", Url: "https://example.com"},
				{Type: "k8s_resource", Url: "pod/foo"},
			},
			wantFile: "",
		},
		{
			name: "file reference with url",
			refs: []toolcore.NBToolResponseReference{
				{Type: "file", Url: "logs_loki_123.txt", Description: "Raw log data"},
			},
			wantFile: "logs_loki_123.txt",
		},
		{
			name: "file reference among others returns the file",
			refs: []toolcore.NBToolResponseReference{
				{Type: "link", Url: "https://example.com"},
				{Type: "file", Url: "metrics_456.json"},
			},
			wantFile: "metrics_456.json",
		},
		{
			name: "file reference with empty url is ignored",
			refs: []toolcore.NBToolResponseReference{
				{Type: "file", Url: ""},
			},
			wantFile: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step := &NBAgentPlannerToolActionStep{References: tc.refs}
			got := fileRecallHandle(step)
			if tc.wantFile == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.wantFile, "handle should name the file so the model can grep it")
			assert.True(t, strings.HasPrefix(got, "\n"), "handle should append on its own line")
		})
	}
}

// TestConstructScratchPad_FsEvidenceHandle is the end-to-end (no-backend)
// integration check: when a large observation is compressed AND its step saved
// a workspace file, the flag turns the dead-end truncation marker into a live
// grep handle. Deterministic — drives ConstructScratchPad directly, no live log
// backend required (those are in-cluster and unreachable locally). The
// behavioral A/B (fewer re-runs) is TestFsEvidenceRecall_AB, which needs a
// cluster where the log backend is reachable.
func TestConstructScratchPad_FsEvidenceHandle(t *testing.T) {
	origBudget := config.Config.LlmServerAgentMaxScratchpadChars
	origFlag := config.Config.LlmServerFsEvidenceRecallEnabled
	t.Cleanup(func() {
		config.Config.LlmServerAgentMaxScratchpadChars = origBudget
		config.Config.LlmServerFsEvidenceRecallEnabled = origFlag
	})

	// Budget between "step 0 alone" and "everything": total exceeds it so
	// compression fires, but compressing the one dominant old observation drops
	// total back under budget — so the per-step compressObservation path runs
	// (where the handle lives), not the aggregate tail-truncation that would
	// drop step 0 wholesale. scratchpadBudget(0) falls back to this legacy char
	// budget.
	config.Config.LlmServerAgentMaxScratchpadChars = 4000

	const savedFile = "logs_evidence_test_123.txt"

	// Fresh steps per phase so the OFF run's in-place bookkeeping can't leak
	// into the ON run.
	buildSteps := func() []NBAgentPlannerToolActionStep {
		steps := make([]NBAgentPlannerToolActionStep, 5)
		for i := range steps {
			steps[i] = NBAgentPlannerToolActionStep{
				Action: NBAgentPlannerToolAction{
					Tool:      "fetch_logs",
					ToolID:    fmt.Sprintf("step_%d", i),
					ToolInput: "get logs",
					Log:       "fetching logs",
				},
				// Recent steps stay small (< 500) so they are not compressed and
				// do not dominate the budget.
				Observation: fmt.Sprintf("recent_%d %s", i, strings.Repeat("y", 50)),
				Status:      ToolStatusSuccess,
			}
		}
		// Oldest step: a large, dominant observation that WILL be compressed,
		// carrying the Type:"file" reference the fetch tool attaches when it
		// saves the raw output. Compressing it alone drops total under budget.
		steps[0].Observation = strings.Repeat("x", 8000)
		steps[0].References = []toolcore.NBToolResponseReference{
			{Type: "file", Url: savedFile, Description: "Raw log data"},
		}
		return steps
	}

	// Flag OFF: compression is a dead-end — no recall handle.
	config.Config.LlmServerFsEvidenceRecallEnabled = false
	off := ConstructScratchPad(buildSteps())
	assert.Contains(t, off, "output truncated", "the oldest large observation should be compressed")
	assert.NotContains(t, off, "full output saved to workspace file", "flag OFF must not add a recall handle")

	// Flag ON: the dead-end becomes a live handle naming the saved file.
	config.Config.LlmServerFsEvidenceRecallEnabled = true
	on := ConstructScratchPad(buildSteps())
	assert.Contains(t, on, "full output saved to workspace file", "flag ON should add a recall handle")
	assert.Contains(t, on, savedFile, "recall handle should name the saved file so the model can grep it")
}
