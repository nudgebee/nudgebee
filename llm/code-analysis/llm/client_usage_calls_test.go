package llm

import (
	"fmt"
	"sync"
	"testing"

	"nudgebee/code-analysis-agent/config"
)

func usageTestClientWithModel(model string) *Client {
	cfg := &config.Config{}
	cfg.LLM.Provider = "googleai"
	cfg.LLM.Model = model
	return &Client{config: cfg}
}

// Every recorded call must land in the calls slice AND the cumulative
// aggregate, including thinking tokens.
func TestRecordCallUsage_AppendsAndAggregates(t *testing.T) {
	c := usageTestClient()
	c.RecordCallUsage(TokenUsageCall{PromptTokens: 100, CompletionTokens: 10, CachedContentTokens: 40, ThinkingTokens: 5})
	c.RecordCallUsage(TokenUsageCall{PromptTokens: 50, CompletionTokens: 5})

	calls, dropped := c.SnapshotCalls()
	if len(calls) != 2 || dropped != 0 {
		t.Fatalf("expected 2 retained calls, got %d (dropped %d)", len(calls), dropped)
	}
	if calls[0].Model != "test-model" || calls[0].Provider != "googleai" {
		t.Errorf("call must be stamped with the calling client's model/provider, got %+v", calls[0])
	}
	if calls[0].TotalTokens != 115 {
		t.Errorf("TotalTokens must default to prompt+completion+thinking, got %d", calls[0].TotalTokens)
	}
	agg := c.GetTokenUsage()
	if agg.PromptTokens != 150 || agg.CompletionTokens != 15 || agg.CachedContentTokens != 40 || agg.ThinkingTokens != 5 {
		t.Errorf("aggregate mismatch: %+v", agg)
	}
	if agg.TotalTokens != 115+55 {
		t.Errorf("aggregate total mismatch: %d", agg.TotalTokens)
	}
}

// A tier client's records must reach the run client's slice with the TIER
// model, while the run aggregate keeps the run model label (wire compat).
func TestRecordCallUsage_FunnelsWithChildAttribution(t *testing.T) {
	run := usageTestClientWithModel("run-model")
	tier := usageTestClientWithModel("tier-model")
	tier.ShareUsageWith(run)

	tier.RecordCallUsage(TokenUsageCall{PromptTokens: 10, CompletionTokens: 2})

	calls, _ := run.SnapshotCalls()
	if len(calls) != 1 || calls[0].Model != "tier-model" {
		t.Fatalf("run client must retain the tier call with tier attribution, got %+v", calls)
	}
	if got := run.GetTokenUsage(); got.Model != "run-model" || got.PromptTokens != 10 {
		t.Errorf("run aggregate keeps run label and accumulates, got %+v", got)
	}
}

// Concurrent recording on child and parent must not race or lose records
// (run with -race).
func TestRecordCallUsage_Concurrent(t *testing.T) {
	run := usageTestClient()
	tier := usageTestClient()
	tier.ShareUsageWith(run)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			tier.RecordCallUsage(TokenUsageCall{PromptTokens: 1})
		}()
		go func() {
			defer wg.Done()
			run.RecordCallUsage(TokenUsageCall{PromptTokens: 1})
		}()
	}
	wg.Wait()

	calls, dropped := run.SnapshotCalls()
	if len(calls)+dropped != 100 {
		t.Errorf("run must see all 100 calls, got %d retained + %d dropped", len(calls), dropped)
	}
	if got := run.GetTokenUsage().PromptTokens; got != 100 {
		t.Errorf("aggregate must count all calls, got %d", got)
	}
}

// The cap drops the OLDEST records but the aggregate still counts everything,
// so the consumer can reconcile via callsDropped.
func TestRecordCallUsage_CapDropsOldest(t *testing.T) {
	c := usageTestClient()
	for i := 0; i < maxCallRecords+5; i++ {
		c.RecordCallUsage(TokenUsageCall{PromptTokens: 1, Model: fmt.Sprintf("m%d", i)})
	}
	calls, dropped := c.SnapshotCalls()
	if len(calls) != maxCallRecords || dropped != 5 {
		t.Fatalf("expected %d retained / 5 dropped, got %d / %d", maxCallRecords, len(calls), dropped)
	}
	if calls[0].Model != "m5" {
		t.Errorf("oldest records must be dropped first, first retained = %s", calls[0].Model)
	}
	if got := c.GetTokenUsage().PromptTokens; got != maxCallRecords+5 {
		t.Errorf("aggregate must include dropped calls, got %d", got)
	}
}

// The legacy addTokenUsage wrapper must produce call records too, so any
// remaining caller stays consistent with the calls[] wire contract.
func TestAddTokenUsage_ProducesCallRecords(t *testing.T) {
	c := usageTestClient()
	c.addTokenUsage(100, 10, 0, 40)
	calls, _ := c.SnapshotCalls()
	if len(calls) != 1 || calls[0].PromptTokens != 100 || calls[0].CachedContentTokens != 40 {
		t.Fatalf("wrapper must record a call, got %+v", calls)
	}
	if calls[0].TotalTokens != 110 {
		t.Errorf("wrapper must default total, got %d", calls[0].TotalTokens)
	}
}
