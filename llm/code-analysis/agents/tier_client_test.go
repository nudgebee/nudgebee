package agents

import (
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/llm"
)

func tierTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.LLM.Provider = "googleai"
	cfg.LLM.Model = "run-model"
	cfg.LLM.ApiKey = "test-key"
	return cfg
}

func TestTierClientFallsBackWithoutConfig(t *testing.T) {
	cfg := tierTestConfig(t)
	logger := common.NewLogger("test", "", "", nil)
	run, err := llm.NewClient(cfg)
	if err != nil {
		t.Fatalf("run client: %v", err)
	}

	// Empty tier and same-as-run tier must return the run client untouched.
	if got := tierClient(cfg, run, "", "fixer", llm.ModelTierRetrieval, logger); got != run {
		t.Error("empty tier must return the run client")
	}
	if got := tierClient(cfg, run, "run-model", "fixer", llm.ModelTierRetrieval, logger); got != run {
		t.Error("tier equal to run model must return the run client")
	}
}

func TestTierClientDerivesAndSharesUsage(t *testing.T) {
	cfg := tierTestConfig(t)
	logger := common.NewLogger("test", "", "", nil)
	run, err := llm.NewClient(cfg)
	if err != nil {
		t.Fatalf("run client: %v", err)
	}

	tiered := tierClient(cfg, run, "cheap-model", "fixer", llm.ModelTierRetrieval, logger)
	if tiered == run {
		t.Fatal("configured tier must derive a separate client")
	}
	// Usage sharing between derived and run clients is asserted in the llm
	// package (TestShareUsageWith), where the unexported accumulator is reachable.
}
