package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The grader's JSON is model-produced, so every degenerate shape has to degrade
// to nil rather than to a half-populated grade. A chip that says "high" because
// the level field was garbage is worse than no chip.
func TestParseAnswerConfidence(t *testing.T) {
	tests := []struct {
		name            string
		raw             string
		wantNil         bool
		wantLevel       string
		wantRationale   string
		wantLimitations []string
	}{
		{
			name:    "not json at all",
			raw:     "I think this answer is pretty good actually",
			wantNil: true,
		},
		{
			name:    "empty string",
			raw:     "",
			wantNil: true,
		},
		{
			name:    "valid json but no level",
			raw:     `{"rationale": "because"}`,
			wantNil: true,
		},
		{
			name:    "unrecognised level is dropped, not guessed",
			raw:     `{"level": "very high", "rationale": "r"}`,
			wantNil: true,
		},
		{
			name:    "numeric level is dropped (we do not accept percentages)",
			raw:     `{"level": "85%"}`,
			wantNil: true,
		},
		{
			name:      "level only",
			raw:       `{"level": "high"}`,
			wantLevel: ConfidenceLevelHigh,
		},
		{
			name:      "level is case- and whitespace-insensitive",
			raw:       `{"level": "  MEDIUM  "}`,
			wantLevel: ConfidenceLevelMedium,
		},
		{
			name: "full grade",
			raw: `{"level": "low",
			       "rationale": "Only the kubectl tool ran and it returned empty.",
			       "limitations": ["metrics tool returned empty", "no deploy correlation attempted"]}`,
			wantLevel:       ConfidenceLevelLow,
			wantRationale:   "Only the kubectl tool ran and it returned empty.",
			wantLimitations: []string{"metrics tool returned empty", "no deploy correlation attempted"},
		},
		{
			// Graders routinely wrap JSON in a markdown fence despite the
			// "ONLY this JSON object" instruction; ExtractAndUnmarshalJSON
			// handles it and we pin that here.
			name:      "fenced json",
			raw:       "```json\n{\"level\": \"medium\", \"rationale\": \"one source only\"}\n```",
			wantLevel: ConfidenceLevelMedium, wantRationale: "one source only",
		},
		{
			name:      "prose around the json",
			raw:       `Here is my grade: {"level": "high", "rationale": "two tools agree"} Hope that helps.`,
			wantLevel: ConfidenceLevelHigh, wantRationale: "two tools agree",
		},
		{
			name:            "blank and duplicate limitations are dropped",
			raw:             `{"level": "low", "limitations": ["  ", "same gap", "SAME GAP", "other"]}`,
			wantLevel:       ConfidenceLevelLow,
			wantLimitations: []string{"same gap", "other"},
		},
		{
			name:      "empty limitations array yields nil, not an empty slice",
			raw:       `{"level": "high", "limitations": []}`,
			wantLevel: ConfidenceLevelHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAnswerConfidence(tt.raw)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantLevel, got.Level)
			assert.Equal(t, tt.wantRationale, got.Rationale)
			assert.Equal(t, tt.wantLimitations, got.Limitations)
		})
	}
}

// A grader that emits 40 gaps must not bloat the message metadata jsonb or the
// tooltip — the list is a glance surface, not an audit log.
func TestParseAnswerConfidence_CapsLimitations(t *testing.T) {
	items := make([]string, 0, 40)
	for i := range 40 {
		items = append(items, `"gap number `+string(rune('a'+i%26))+string(rune('a'+i/26))+`"`)
	}
	raw := `{"level": "low", "limitations": [` + strings.Join(items, ",") + `]}`

	got := parseAnswerConfidence(raw)
	require.NotNil(t, got)
	assert.Len(t, got.Limitations, maxConfidenceLimitations)
}

// Free-text fields are length-bounded so a runaway generation cannot blow up the
// metadata column. Truncation must not split a multi-byte rune.
func TestParseAnswerConfidence_TruncatesLongText(t *testing.T) {
	long := strings.Repeat("é", maxConfidenceTextLength) // 2 bytes per rune
	got := parseAnswerConfidence(`{"level": "low", "rationale": "` + long + `"}`)
	require.NotNil(t, got)
	assert.LessOrEqual(t, len(got.Rationale), maxConfidenceTextLength)
	assert.True(t, strings.HasPrefix(long, got.Rationale), "truncation must not split a rune")
}

// The manifest is the grader's only view of what actually ran, so its shape is
// load-bearing: distinct-tool count drives the "2+ independent sources" rule,
// and an empty manifest must read as "nothing was gathered" rather than as an
// absent section the grader can ignore.
func TestFormatExecutionManifest(t *testing.T) {
	t.Run("empty manifest is explicit, not blank", func(t *testing.T) {
		out := formatExecutionManifest(nil)
		assert.Contains(t, out, "no tools were executed")
	})

	t.Run("renders each outcome with status and call count", func(t *testing.T) {
		out := formatExecutionManifest([]ToolCallOutcome{
			{ToolName: "kubectl", Status: "success", Count: 3},
			{ToolName: "metrics", Status: "empty_result", Count: 1},
		})
		assert.Contains(t, out, "- kubectl: success (3 calls)")
		assert.Contains(t, out, "- metrics: empty_result (1 call)")
		assert.Contains(t, out, "Distinct tools used: 2")
	})

	t.Run("same tool under two statuses counts as ONE source", func(t *testing.T) {
		// The rubric's corroboration rule is about distinct sources; a tool that
		// partly succeeded and partly failed is still one source. Counting rows
		// here instead of names would silently inflate every grade.
		out := formatExecutionManifest([]ToolCallOutcome{
			{ToolName: "kubectl", Status: "success", Count: 2},
			{ToolName: "kubectl", Status: "fail", Count: 1},
		})
		assert.Contains(t, out, "Distinct tools used: 1")
	})

	t.Run("caps very long manifests", func(t *testing.T) {
		outcomes := make([]ToolCallOutcome, 0, 100)
		for i := range 100 {
			outcomes = append(outcomes, ToolCallOutcome{ToolName: "tool" + string(rune('a'+i%26)), Status: "success", Count: 1})
		}
		out := formatExecutionManifest(outcomes)
		assert.LessOrEqual(t, strings.Count(out, "\n- "), maxConfidenceManifestRows)
	})
}

// The gate decides whether we spend an LLM call at all. It must never fire on a
// plain retrieval turn, and must never fire without the identifiers the grader
// and the metadata write both need.
func TestShouldScoreAnswerConfidence(t *testing.T) {
	base := func() NBAgentRequest {
		return NBAgentRequest{MessageId: "msg-1", Query: "why is the checkout pod crashing?"}
	}

	t.Run("investigation query qualifies", func(t *testing.T) {
		assert.True(t, shouldScoreAnswerConfidence(base(), "root cause found"))
	})

	t.Run("plain retrieval does not", func(t *testing.T) {
		r := base()
		r.Query = "list all pods in the prod namespace"
		assert.False(t, shouldScoreAnswerConfidence(r, "here are your pods"))
	})

	t.Run("investigation source qualifies regardless of wording", func(t *testing.T) {
		r := base()
		r.Query = "list all pods in the prod namespace"
		r.ConversationSource = ConversationSourceInvestigation
		assert.True(t, shouldScoreAnswerConfidence(r, "here are your pods"))
	})

	t.Run("classifies on the top-level question, not a sub-agent brief", func(t *testing.T) {
		// OriginalQuery holds the user's verbatim ask; Query may have been
		// rewritten. Grading the rewrite would misclassify.
		r := base()
		r.OriginalQuery = "why did the deployment fail?"
		r.Query = "get pod list"
		assert.True(t, shouldScoreAnswerConfidence(r, "answer"))
	})

	t.Run("empty answer does not qualify", func(t *testing.T) {
		assert.False(t, shouldScoreAnswerConfidence(base(), "   "))
	})

	t.Run("missing message id does not qualify", func(t *testing.T) {
		r := base()
		r.MessageId = ""
		assert.False(t, shouldScoreAnswerConfidence(r, "answer"))
	})

	t.Run("empty question does not qualify", func(t *testing.T) {
		r := base()
		r.Query = ""
		assert.False(t, shouldScoreAnswerConfidence(r, "answer"))
	})
}
