package tools

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestToolPrompt_WarnsAgainstEmptyResultAsCleanBill guards against a real
// production miss: an agent asked whether a specific under-provisioned
// workload's HPA was pinned at max replicas queried this tool, got zero
// rows (no rule here models that pattern), and reported "no issues found"
// as if the pattern had been checked and come back clean. The prompt must
// tell the LLM that an empty result requires checking rule coverage before
// concluding health, not just handing back a schema without that guidance.
func TestToolPrompt_WarnsAgainstEmptyResultAsCleanBill(t *testing.T) {
	prompt := strings.Join(RecommendationExecuteTool{}.ToolPrompt(), "\n")
	assert.Contains(t, prompt, "NOT proof",
		"prompt must warn that a zero-row result does not prove the resource is healthy")
	assert.Contains(t, prompt, "no rule",
		"prompt must tell the LLM to say plainly when no rule_name covers the user's question")
	assert.Contains(t, prompt, "live check",
		"prompt must direct the LLM to a live fallback instead of presenting silence as a clean bill of health")
}

// TestEmptyResultWithRuleCatalog_IncludesLiveCategories proves the empty-result
// enrichment actually queries live category data rather than a hardcoded list
// — the whole point being that the categories can never drift from what the
// database really contains, unlike a prose list embedded in the prompt.
//
// Stubs queryRecommendationCategories directly rather than mocking
// common.GetDatabaseManager: that manager is cached process-wide once any
// test in this package populates it, so a sqlmock hook registered here can
// be silently ignored depending on what ran first in a full `go test
// ./tools/...` run — see the test-seam comment on queryRecommendationCategories.
func TestEmptyResultWithRuleCatalog_IncludesLiveCategories(t *testing.T) {
	original := queryRecommendationCategories
	t.Cleanup(func() { queryRecommendationCategories = original })
	queryRecommendationCategories = func() ([]string, error) {
		return []string{"Configuration", "RightSizing", "Security"}, nil
	}

	reqCtx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	nbCtx := core.NbToolContext{Ctx: reqCtx}

	result := emptyResultWithRuleCatalog(nbCtx)

	assert.Contains(t, result, "Configuration")
	assert.Contains(t, result, "RightSizing")
	assert.Contains(t, result, "Security")
	assert.Contains(t, result, "available_categories")
	assert.Contains(t, result, "does NOT mean the situation is healthy")
	assert.Contains(t, result, `"rows":[]`)
}

// TestEmptyResultWithRuleCatalog_FallsBackOnDBError proves a catalog-lookup
// failure degrades to the original generic message rather than surfacing a
// DB error to the LLM or returning malformed JSON.
func TestEmptyResultWithRuleCatalog_FallsBackOnDBError(t *testing.T) {
	original := queryRecommendationCategories
	t.Cleanup(func() { queryRecommendationCategories = original })
	queryRecommendationCategories = func() ([]string, error) {
		return nil, assert.AnError
	}

	reqCtx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	nbCtx := core.NbToolContext{Ctx: reqCtx}

	result := emptyResultWithRuleCatalog(nbCtx)

	assert.Equal(t, emptyRecommendationResultFallback, result)
}

func TestTruncateRecommendationJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		wantTruc bool // expect truncation
	}{
		{
			name:     "short string stays unchanged",
			input:    map[string]any{"recommendation": `{"action":"scale_down"}`},
			wantTruc: false,
		},
		{
			name:     "long string is truncated",
			input:    map[string]any{"recommendation": strings.Repeat("x", 1000)},
			wantTruc: true,
		},
		{
			name:     "byte slice is handled",
			input:    map[string]any{"recommendation": []byte(strings.Repeat("y", 1000))},
			wantTruc: true,
		},
		{
			name:     "missing field is no-op",
			input:    map[string]any{"category": "RightSizing"},
			wantTruc: false,
		},
		{
			name:     "exactly at limit stays unchanged",
			input:    map[string]any{"recommendation": strings.Repeat("z", recommendationMaxJSONChars)},
			wantTruc: false,
		},
		{
			name:     "one over limit is truncated",
			input:    map[string]any{"recommendation": strings.Repeat("z", recommendationMaxJSONChars+1)},
			wantTruc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateRecommendationJSON(tt.input, 0, 0)
			rec, ok := result["recommendation"]
			if !ok {
				// Field wasn't present — nothing to check for truncation
				assert.False(t, tt.wantTruc)
				return
			}
			s, ok := rec.(string)
			assert.True(t, ok, "recommendation should be string after truncation")
			if tt.wantTruc {
				assert.True(t, strings.HasSuffix(s, "...(truncated)"))
				assert.LessOrEqual(t, len(s), recommendationMaxJSONChars+len("...(truncated)"))
			} else {
				assert.False(t, strings.HasSuffix(s, "...(truncated)"))
			}
		})
	}
}
