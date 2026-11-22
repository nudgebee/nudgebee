package core

import (
	"strings"
	"testing"

	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const savedFilesNoteFullHints = ` — read with shell_execute (grep/head); a metric series file can be passed to analyze_metric_series as file_ref]`

func withFsEvidenceRecall(t *testing.T, on bool) {
	t.Helper()
	orig := config.Config.LlmServerFsEvidenceRecallEnabled
	t.Cleanup(func() { config.Config.LlmServerFsEvidenceRecallEnabled = orig })
	config.Config.LlmServerFsEvidenceRecallEnabled = on
}

func fileRef(name string) toolcore.NBToolResponseReference {
	return toolcore.NBToolResponseReference{Type: "file", Url: name, Text: name}
}

func TestSavedFilesNote(t *testing.T) {
	withFsEvidenceRecall(t, true)
	allTools := []toolcore.NBTool{&stubTool{name: "shell_execute"}, &stubTool{name: "analyze_metric_series"}}

	tests := []struct {
		name     string
		refs     []toolcore.NBToolResponseReference
		rendered string
		tools    []toolcore.NBTool
		want     string
	}{
		{name: "no references", want: ""},
		{
			name: "references but no files",
			refs: []toolcore.NBToolResponseReference{{Type: "link", Url: "https://example.com"}, {Type: "file", Url: ""}},
			want: "",
		},
		{
			name:  "one file ref",
			refs:  []toolcore.NBToolResponseReference{fileRef("metrics_prometheus_1.json")},
			tools: allTools,
			want:  "\n" + `[saved workspace files: "metrics_prometheus_1.json"` + savedFilesNoteFullHints,
		},
		{
			name: "duplicates and a name already in the text are dropped",
			refs: []toolcore.NBToolResponseReference{
				fileRef("a.json"), {Type: "link", Url: "https://example.com"}, fileRef("b.txt"), fileRef("a.json"), fileRef("in_text.json"),
			},
			rendered: "result: file_ref in_text.json",
			tools:    allTools,
			want:     "\n" + `[saved workspace files: "a.json", "b.txt"` + savedFilesNoteFullHints,
		},
		{
			name:     "every name already in the text",
			refs:     []toolcore.NBToolResponseReference{fileRef("a.json")},
			rendered: `[workspace file "a.json" holds the full output]`,
			tools:    allTools,
			want:     "",
		},
		{
			name: "more than five names",
			refs: []toolcore.NBToolResponseReference{
				fileRef("1"), fileRef("2"), fileRef("3"), fileRef("4"), fileRef("5"), fileRef("6"), fileRef("7"),
			},
			tools: allTools,
			want:  "\n" + `[saved workspace files: "1", "2", "3", "4", "5" +2 more` + savedFilesNoteFullHints,
		},
		{
			name:  "hints name only tools the planner has",
			refs:  []toolcore.NBToolResponseReference{fileRef("a.json")},
			tools: []toolcore.NBTool{&stubTool{name: "shell_execute"}},
			want:  "\n" + `[saved workspace files: "a.json" — read with shell_execute (grep/head)]`,
		},
		{
			name: "no file tools: names only",
			refs: []toolcore.NBToolResponseReference{fileRef("a.json")},
			want: "\n" + `[saved workspace files: "a.json"]`,
		},
		{
			name: "knowledge document keeps its load_skills pointer",
			refs: []toolcore.NBToolResponseReference{
				{Type: "file", Url: "knowledge/runbook.md", Query: "knowledge:abc"},
				fileRef("a.json"),
			},
			tools: allTools,
			want: "\n" + `[saved workspace files: "a.json"` + savedFilesNoteFullHints +
				"\n" + `[reference documentation saved: "knowledge/runbook.md" (skill_name=knowledge:abc) — read with load_skills using that skill_name with keyword or start_line; if a handle expired, search again]`,
		},
		{
			name:     "knowledge document already named in the text",
			refs:     []toolcore.NBToolResponseReference{{Type: "file", Url: "knowledge/runbook.md", Query: "knowledge:abc"}},
			rendered: "Saved knowledge document to workspace: knowledge/runbook.md",
			want:     "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step := &NBAgentPlannerToolActionStep{References: tc.refs}
			got := savedFilesNote(step, tc.rendered, tc.tools)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, got, savedFilesNote(step, tc.rendered, tc.tools), "same step must render the same note")
		})
	}

	t.Run("flag off", func(t *testing.T) {
		withFsEvidenceRecall(t, false)
		assert.Empty(t, savedFilesNote(&NBAgentPlannerToolActionStep{References: []toolcore.NBToolResponseReference{fileRef("a.json")}}, "", allTools))
	})
}

// Both planners name a step's saved files in the rendered observation, once,
// on full and compressed steps, without changing what is stored on the step.
func TestSavedFilesNoteInPlannerRendering(t *testing.T) {
	withFsEvidenceRecall(t, true)
	origSumm := config.Config.LlmServerScratchpadSummarizationEnabled
	t.Cleanup(func() { config.Config.LlmServerScratchpadSummarizationEnabled = origSumm })
	// On, with a nil ctx: SummarizeObservation byte-truncates and caches the
	// result on the step, so the test can check the cache stays note-free.
	config.Config.LlmServerScratchpadSummarizationEnabled = true

	tools := []toolcore.NBTool{&stubTool{name: "shell_execute"}, &stubTool{name: "analyze_metric_series"}}
	const name = "metrics_prometheus_1760000000000000000.json"
	const line = "\n" + `[saved workspace files: "` + name + `"` + savedFilesNoteFullHints
	newStep := func(obs string) *NBAgentPlannerToolActionStep {
		return &NBAgentPlannerToolActionStep{
			Action:      NBAgentPlannerToolAction{Tool: "metrics", ToolInput: "cpu of api pods"},
			Observation: obs,
			Status:      ToolStatusSuccess,
			References:  []toolcore.NBToolResponseReference{fileRef(name), fileRef(name)},
		}
	}
	short := "CPU peaked at 92% at 10:04."
	long := strings.Repeat("x", 6000)

	r4 := &NBReActPlanner4{tools: tools}
	r3 := &NBReActPlanner3{tools: tools}
	cases := []struct {
		name   string
		obs    string
		render func(step *NBAgentPlannerToolActionStep) string
	}{
		{"ReAct4 recent", short, func(s *NBAgentPlannerToolActionStep) string { return r4.renderObservation(s, 0, 1, false, 4096) }},
		{"ReAct4 compressed", long, func(s *NBAgentPlannerToolActionStep) string {
			return r4.renderObservation(s, 0, recentStepsFullContext+5, true, 4096)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := newStep(tc.obs)
			got := tc.render(step)
			assert.True(t, strings.HasSuffix(got, line), got)
			assert.Equal(t, 1, strings.Count(got, "[saved workspace files:"))
			assert.Equal(t, 1, strings.Count(got, name))
			assert.Equal(t, got, tc.render(step), "re-rendering a step must give the same text")
			assert.Equal(t, tc.obs, step.Observation, "stored observation unchanged")
			assert.NotContains(t, step.CompressedObservation, "[saved workspace files:", "cached summary stays note-free")
		})
	}

	// ReAct3 needs no note: formatObservation already lists every reference,
	// file names included, on every step.
	t.Run("ReAct3 already shows the file name", func(t *testing.T) {
		step := newStep(short)
		got := r3.formatObservation(*step, r3.getToolResponse(step, true), true)
		assert.Contains(t, got, name)
		assert.NotContains(t, got, "[saved workspace files:")
	})

	t.Run("name already in the observation is not repeated", func(t *testing.T) {
		step := newStep("Raw result of the last successful query: file_ref " + name)
		got := r4.renderObservation(step, 0, 1, false, 4096)
		assert.NotContains(t, got, "[saved workspace files:")
		assert.Equal(t, 1, strings.Count(got, name))
	})

	t.Run("step without refs renders as before", func(t *testing.T) {
		step := &NBAgentPlannerToolActionStep{Action: NBAgentPlannerToolAction{Tool: "metrics"}, Observation: short, Status: ToolStatusSuccess}
		require.Equal(t, short, r4.renderObservation(step, 0, 1, false, 4096))
	})
}
