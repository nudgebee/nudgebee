package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"

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
	assert.NotEmpty(t, prompts_repo.GetPrompt(prompts_repo.PromptAgentWebhookSubjectExtractor))
}

func TestWebhookSubjectExtractor_OptionsAlwaysIncludeNotFound(t *testing.T) {
	// With no resolvable tenant/DB, options degrade to just the sentinel — and the
	// sentinel is always present and last so the classifier can decline a match.
	a := &WebhookSubjectExtractorAgent{accountId: "does-not-exist"}
	opts := a.GetOptions()

	assert.NotEmpty(t, opts)
	assert.Equal(t, WebhookSubjectExtractorNotFound, opts[len(opts)-1])
}
