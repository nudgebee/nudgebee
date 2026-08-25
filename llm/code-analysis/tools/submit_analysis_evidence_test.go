package tools

import (
	"context"
	"fmt"
	"testing"

	"nudgebee/code-analysis-agent/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEvidence stands in for common.ToolInvocationTracker via the narrow
// EvidenceSource interface.
type stubEvidence struct{ files []common.FileEvidence }

func (s stubEvidence) ReadFileEvidence() []common.FileEvidence { return s.files }

func evidenceFor(paths ...string) stubEvidence {
	var files []common.FileEvidence
	for _, p := range paths {
		files = append(files, common.FileEvidence{Path: p, Snippet: "package main"})
	}
	return stubEvidence{files: files}
}

func exploreCtx(src EvidenceSource) context.Context {
	ctx := WithMode(context.Background(), "explore")
	if src != nil {
		ctx = WithEvidence(ctx, src)
	}
	return ctx
}

func TestCitationsFromEvidence(t *testing.T) {
	t.Run("builds one citation per file read", func(t *testing.T) {
		got := citationsFromEvidence(evidenceFor("a.go", "b.go").files)
		require.Len(t, got, 2)
		assert.Equal(t, "a.go", got[0].FilePath)
		assert.Zero(t, got[0].LineStart, "file-level evidence, which the contract allows")
		assert.NotEmpty(t, got[0].Note, "a synthesized citation must say where it came from")
	})

	t.Run("caps the list", func(t *testing.T) {
		var many []string
		for i := 0; i < maxSynthesizedCitations+7; i++ {
			many = append(many, fmt.Sprintf("f%d.go", i))
		}
		assert.Len(t, citationsFromEvidence(evidenceFor(many...).files), maxSynthesizedCitations)
	})

	t.Run("no evidence yields nothing", func(t *testing.T) {
		assert.Empty(t, citationsFromEvidence(nil))
	})
}

// The point of the change: a weak model that produces a sound answer but omits
// citations must not fail the run when the tools recorded what it read.
func TestSubmitAnalysis_ExploreBacksAnswerWithEvidence(t *testing.T) {
	tool := NewSubmitAnalysisTool()
	resp := tool.Execute(exploreCtx(evidenceFor("llm/client.go")), map[string]any{
		"answer": "The Bedrock client is built in llm/client.go.",
	})

	assert.Equal(t, "success", resp.Status,
		"an answer backed by real reads must not be rejected for missing citations: %s", resp.Data)
}

// The contract still has teeth: no answer, or an answer with nothing read,
// remains a failure.
func TestSubmitAnalysis_ExploreStillEnforcesContract(t *testing.T) {
	tool := NewSubmitAnalysisTool()

	t.Run("no citations and no evidence still fails", func(t *testing.T) {
		resp := tool.Execute(exploreCtx(stubEvidence{}), map[string]any{
			"answer": "Something happened somewhere.",
		})
		assert.Equal(t, "error", resp.Status)
	})

	t.Run("no evidence source at all still fails", func(t *testing.T) {
		resp := tool.Execute(exploreCtx(nil), map[string]any{
			"answer": "Something happened somewhere.",
		})
		assert.Equal(t, "error", resp.Status)
	})

	t.Run("an empty answer is not rescued by evidence", func(t *testing.T) {
		resp := tool.Execute(exploreCtx(evidenceFor("a.go")), map[string]any{
			"answer": "   ",
		})
		assert.Equal(t, "error", resp.Status,
			"evidence backs an answer; it does not substitute for one")
	})
}

// A model that did declare citations keeps exactly what it declared — synthesis
// fills a gap, it does not overwrite.
func TestSubmitAnalysis_ExplorePreservesDeclaredCitations(t *testing.T) {
	tool := NewSubmitAnalysisTool()
	resp := tool.Execute(exploreCtx(evidenceFor("unrelated.go")), map[string]any{
		"answer": "The parser rejects empty input.",
		"citations": []any{map[string]any{
			"file_path":  "parser.go",
			"line_start": float64(42),
			"snippet":    "if len(in) == 0 {",
		}},
	})

	require.Equal(t, "success", resp.Status)
	assert.Contains(t, fmt.Sprintf("%v", resp.Data), "parser.go")
	assert.NotContains(t, fmt.Sprintf("%v", resp.Data), "unrelated.go",
		"declared citations must survive untouched")
}
