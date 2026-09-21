package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/prompts"

	"github.com/stretchr/testify/assert"
)

func TestWebhookSubjectExtractor_InterfaceCompliance(t *testing.T) {
	a := &WebhookSubjectExtractorAgent{accountId: "acc-1"}

	// Compile-time contracts the executor relies on.
	var _ core.NBAgent = a
	var _ core.NBClassificationAgent = a
	var _ core.NBAgentCacheScopeProvider = a

	assert.Equal(t, WebhookSubjectExtractorAgentName, a.GetName())
	assert.Contains(t, a.GetNameAliases(), WebhookSubjectExtractorAgentName)
	assert.Equal(t, core.AgentPlannerTypeClassification, a.GetPlannerType())
	assert.Equal(t, core.CacheScopeAccount, a.GetCacheScope())
	assert.Nil(t, a.GetSupportedTools(nil), "classifier must expose no tools")
}

func TestWebhookSubjectExtractor_PromptRegistered(t *testing.T) {
	// The system prompt must be embedded and resolvable.
	assert.NotEmpty(t, prompts.GetPrompt(context.Background(), prompts.PromptWebhookSubjectExtractor, ""))
}

func TestWebhookSubjectExtractor_OptionsAlwaysIncludeNotFound(t *testing.T) {
	// With no resolvable tenant/DB, options degrade to just the sentinel — and the
	// sentinel is always present and last so the classifier can decline a match.
	a := &WebhookSubjectExtractorAgent{accountId: "does-not-exist"}
	opts := a.GetOptions()

	assert.NotEmpty(t, opts)
	assert.Equal(t, WebhookSubjectExtractorNotFound, opts[len(opts)-1])
}

// TestRenderHistoricalPatterns covers the prompt block that used to be unbounded:
// it must dedupe, cap, and render byte-identically for a given set of rows.
func TestRenderHistoricalPatterns(t *testing.T) {
	t.Run("one line per title and service", func(t *testing.T) {
		out := renderHistoricalPatterns([]historicalMappingRow{
			{Title: "checkout latency", Services: "checkout-api, checkout-worker"},
			{Title: "rating-service down", Services: "rating-service"},
		})
		assert.Equal(t, "- \"checkout latency\" -> \"checkout-api\"\n"+
			"- \"checkout latency\" -> \"checkout-worker\"\n"+
			"- \"rating-service down\" -> \"rating-service\"\n", out)
	})

	t.Run("skips blank rows and blank services", func(t *testing.T) {
		out := renderHistoricalPatterns([]historicalMappingRow{
			{Title: "", Services: "orphan"},
			{Title: "no services", Services: ""},
			{Title: "kept", Services: ",,payment-service,"},
		})
		assert.Equal(t, "- \"kept\" -> \"payment-service\"\n", out)
	})

	t.Run("dedupes titles case-insensitively", func(t *testing.T) {
		out := renderHistoricalPatterns([]historicalMappingRow{
			{Title: "Checkout Latency", Services: "checkout-api"},
			{Title: "checkout latency", Services: "checkout-api"},
		})
		assert.Equal(t, "- \"Checkout Latency\" -> \"checkout-api\"\n", out)
	})

	t.Run("caps the block", func(t *testing.T) {
		rows := make([]historicalMappingRow, 0, maxHistoricalPatternLines*2)
		for i := range maxHistoricalPatternLines * 2 {
			rows = append(rows, historicalMappingRow{Title: fmt.Sprintf("alert-%04d", i), Services: "svc"})
		}
		out := renderHistoricalPatterns(rows)
		assert.Equal(t, maxHistoricalPatternLines, strings.Count(out, "\n"))
	})

	t.Run("render is stable regardless of row order", func(t *testing.T) {
		rows := []historicalMappingRow{
			{Title: "b alert", Services: "b-svc"},
			{Title: "a alert", Services: "a-svc"},
			{Title: "c alert", Services: "c-svc"},
		}
		reversed := []historicalMappingRow{rows[2], rows[1], rows[0]}
		assert.Equal(t, renderHistoricalPatterns(rows), renderHistoricalPatterns(reversed))
	})
}
