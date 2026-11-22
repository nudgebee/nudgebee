package runtime

import (
	"nudgebee/llm/config"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassificationTierUsesSummaryThinkingBudget(t *testing.T) {
	oldSummary, oldRetrieval := config.Config.LlmThinkingBudgetSummary, config.Config.LlmThinkingBudgetRetrieval
	t.Cleanup(func() {
		config.Config.LlmThinkingBudgetSummary, config.Config.LlmThinkingBudgetRetrieval = oldSummary, oldRetrieval
	})
	config.Config.LlmThinkingBudgetSummary, config.Config.LlmThinkingBudgetRetrieval = 512, 4096
	assert.Equal(t, 512, resolveThinkingBudget(ModelTierClassification))
}
