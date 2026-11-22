package classify

import (
	"context"
	"log/slog"
	"nudgebee/llm/config"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
	"testing"
)

// pinGlobalModel sets the ENV-global provider/model config for the test.
func pinGlobalModel(t *testing.T, provider, model string) {
	t.Helper()
	p, m := config.Config.LlmProvider, config.Config.LlmModel
	config.Config.LlmProvider = provider
	config.Config.LlmModel = model
	t.Cleanup(func() {
		config.Config.LlmProvider = p
		config.Config.LlmModel = m
	})
}

// setEnvKey sets a dynamic viper config key (agent- or tier-scoped) for the test.
func setEnvKey(t *testing.T, key, value string) {
	t.Helper()
	prev := config.Config.GetString(key, "")
	config.Config.SetString(key, value)
	t.Cleanup(func() { config.Config.SetString(key, prev) })
}

// seedDBConfig pre-populates the LLM integration-config cache so ResolveLLMConfig
// resolves the DB layers without touching a database.
func seedDBConfig(t *testing.T, accountId string, cfg map[string]string) {
	t.Helper()
	t.Cleanup(llmruntime.SeedIntegrationConfigForTest(accountId, cfg))
}

func testRequestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), nil, slog.Default(), nil, nil)
}
