package llm

import (
	"testing"

	"nudgebee/code-analysis-agent/config"
)

func usageTestClient() *Client {
	cfg := &config.Config{}
	cfg.LLM.Provider = "googleai"
	cfg.LLM.Model = "test-model"
	return &Client{config: cfg}
}

// A per-role tier client must propagate every usage delta to the run client:
// the analysis handler snapshots the run client's totals for the final
// response and billing, so unshared tier spend would silently vanish.
func TestShareUsageWith(t *testing.T) {
	run := usageTestClient()
	tiered := usageTestClient()
	tiered.ShareUsageWith(run)

	tiered.addTokenUsage(100, 10, 0, 40)
	tiered.addTokenUsage(50, 5, 60, 0)

	got := run.GetTokenUsage()
	if got.PromptTokens != 150 || got.CompletionTokens != 15 || got.CachedContentTokens != 40 {
		t.Errorf("run client must accumulate tiered usage, got %+v", got)
	}
	if got.TotalTokens != 110+60 {
		t.Errorf("total tokens mismatch, got %d", got.TotalTokens)
	}

	own := tiered.GetTokenUsage()
	if own.PromptTokens != 150 {
		t.Errorf("tiered client keeps its own totals, got %+v", own)
	}

	// Self-parenting must be ignored (no infinite recursion / double count).
	self := usageTestClient()
	self.ShareUsageWith(self)
	self.addTokenUsage(10, 1, 0, 0)
	if self.GetTokenUsage().PromptTokens != 10 {
		t.Errorf("self-share must be a no-op, got %+v", self.GetTokenUsage())
	}
}

// An indirect cycle (A→B, then B→A) must be refused — addTokenUsage recurses
// through the parent chain and a cycle would overflow the stack.
func TestShareUsageWithRejectsIndirectCycle(t *testing.T) {
	a := usageTestClient()
	b := usageTestClient()
	c := usageTestClient()
	a.ShareUsageWith(b)
	b.ShareUsageWith(c)
	c.ShareUsageWith(a) // would close a→b→c→a — must be a no-op

	a.addTokenUsage(5, 1, 0, 0) // would hang/overflow if the cycle existed
	if got := c.GetTokenUsage().PromptTokens; got != 5 {
		t.Errorf("chain propagation must still work, got %d", got)
	}
	if got := a.GetTokenUsage().PromptTokens; got != 5 {
		t.Errorf("a keeps its own count, got %d", got)
	}
	c.addTokenUsage(3, 0, 0, 0)
	if got := a.GetTokenUsage().PromptTokens; got != 5 {
		t.Errorf("rejected cycle edge must not propagate back to a, got %d", got)
	}
}
