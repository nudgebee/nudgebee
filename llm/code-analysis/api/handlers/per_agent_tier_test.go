package handlers

import (
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
)

// Forwarded per-role tiers (llm-server's layered env+tenant-DB resolution) must
// land on the agent tier fields and win over this pod's env-derived values.
func TestResolveClientsAppliesPerAgentTiers(t *testing.T) {
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.LLM.Provider = "googleai"
	cfg.LLM.Model = "pod-default-model"
	cfg.LLM.ApiKey = "test-key"
	cfg.Agent.ModelFixer = "env-fixer-model" // pod env fallback that must lose

	ah := &AgenticAnalyzeHandler{config: cfg}
	logger := common.NewLogger("test", "", "", nil)

	req := AgenticAnalyzeRequest{
		LLMConfig: &LLMConfigOverride{
			Model: "tenant-model",
			Tiers: map[string]string{
				"retrieval": "tenant-execution-model",
				"summary":   "tenant-summary-model",
			},
		},
	}
	resolved, _, _, err := ah.resolveClients(req, logger)
	if err != nil {
		t.Fatalf("resolveClients: %v", err)
	}
	if resolved.LLM.Model != "tenant-model" {
		t.Errorf("run model must come from forwarded config, got %q", resolved.LLM.Model)
	}
	if resolved.Agent.ModelFixer != "tenant-execution-model" || resolved.Agent.ModelRouter != "tenant-execution-model" {
		t.Errorf("retrieval tier must drive router+fixer and win over pod env, got fixer=%q router=%q",
			resolved.Agent.ModelFixer, resolved.Agent.ModelRouter)
	}
	if resolved.Agent.ModelReview != "tenant-summary-model" {
		t.Errorf("summary tier must drive review, got %q", resolved.Agent.ModelReview)
	}

	// Without forwarded config, pod env values stay authoritative.
	resolved2, _, _, err := ah.resolveClients(AgenticAnalyzeRequest{}, logger)
	if err != nil {
		t.Fatalf("resolveClients without llm_config: %v", err)
	}
	if resolved2.Agent.ModelFixer != "env-fixer-model" {
		t.Errorf("pod env fallback must survive when nothing is forwarded, got %q", resolved2.Agent.ModelFixer)
	}
}
