package api

import (
	"context"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestProcessCacheInvalidationMessage_ClearsCacheForListedAccount(t *testing.T) {
	const accountId = "acct-cache-mq-test"
	core.SetMCPIntegrationToolCache(accountId, []core.NBTool{})
	defer core.ClearMCPIntegrationToolCache(accountId)

	msg := []byte(`{"account_ids":["` + accountId + `"]}`)
	err := processCacheInvalidationMessage(context.Background(), msg)
	assert.NoError(t, err)

	// MCP cache for the listed account must be gone after processing.
	_, present := core.GetMCPIntegrationToolCache(accountId)
	assert.False(t, present, "cache entry should be removed after invalidation")
}

func TestProcessCacheInvalidationMessage_AcceptsSingleAccountIdField(t *testing.T) {
	const accountId = "acct-cache-mq-single"
	core.SetMCPIntegrationToolCache(accountId, []core.NBTool{})
	defer core.ClearMCPIntegrationToolCache(accountId)

	msg := []byte(`{"account_id":"` + accountId + `"}`)
	err := processCacheInvalidationMessage(context.Background(), msg)
	assert.NoError(t, err)

	_, present := core.GetMCPIntegrationToolCache(accountId)
	assert.False(t, present, "cache entry should be removed for single-id payload")
}

func TestProcessCacheInvalidationMessage_DropsInvalidJson(t *testing.T) {
	err := processCacheInvalidationMessage(context.Background(), []byte("not-json"))
	// Invalid payloads must Ack (return nil) — requeueing would loop forever.
	assert.NoError(t, err, "malformed JSON must be dropped, not requeued")
}

func TestProcessCacheInvalidationMessage_DropsEmptyPayload(t *testing.T) {
	err := processCacheInvalidationMessage(context.Background(), []byte(`{}`))
	assert.NoError(t, err, "empty payload must drop without error")

	err = processCacheInvalidationMessage(context.Background(), []byte(`{"account_ids":[]}`))
	assert.NoError(t, err, "empty account list must drop without error")
}

func TestProcessCacheInvalidationMessage_DedupsAccountIds(t *testing.T) {
	const a = "acct-cache-mq-dedup-a"
	const b = "acct-cache-mq-dedup-b"
	core.SetMCPIntegrationToolCache(a, []core.NBTool{})
	core.SetMCPIntegrationToolCache(b, []core.NBTool{})
	defer core.ClearMCPIntegrationToolCache(a)
	defer core.ClearMCPIntegrationToolCache(b)

	msg := []byte(`{"account_id":"` + a + `","account_ids":["` + a + `","` + b + `","",""]}`)
	err := processCacheInvalidationMessage(context.Background(), msg)
	assert.NoError(t, err)

	_, presentA := core.GetMCPIntegrationToolCache(a)
	_, presentB := core.GetMCPIntegrationToolCache(b)
	assert.False(t, presentA, "cache for account %s should be cleared", a)
	assert.False(t, presentB, "cache for account %s should be cleared", b)
}

// #40994: a health change of one MCP integration drops only the tool lists that
// hold it. It travels as the mcp_integration_health_changed replica broadcast.
func TestMCPIntegrationHealthChanged_DropsOnlyListsHoldingTheIntegration(t *testing.T) {
	const withIt, withOther, scoped = "acct-mcp-health-a", "acct-mcp-health-b", "acct-mcp-health-c"
	core.SetMCPIntegrationToolCache(withIt, []core.NBTool{core.NewMCPIntegrationToolForTest("int-down", "down_a")})
	core.SetMCPIntegrationToolCache(withOther, []core.NBTool{core.NewMCPIntegrationToolForTest("int-up", "up_a")})
	core.SetMCPIntegrationToolCache(scoped, []core.NBTool{core.NewMCPIntegrationToolForTest("int-down", "down_a")})
	for _, a := range []string{withIt, withOther, scoped} {
		defer core.ClearMCPIntegrationToolCache(a)
	}

	// Scoped to one account (a vm_agent integration).
	core.MCPIntegrationHealthChanged("int-down", []string{scoped})
	_, present := core.GetMCPIntegrationToolCache(scoped)
	assert.False(t, present)
	_, present = core.GetMCPIntegrationToolCache(withIt)
	assert.True(t, present, "other accounts are left alone when the change is scoped")

	// Every account (a direct integration).
	core.MCPIntegrationHealthChanged("int-down", nil)
	_, present = core.GetMCPIntegrationToolCache(withIt)
	assert.False(t, present)
	_, present = core.GetMCPIntegrationToolCache(withOther)
	assert.True(t, present, "a list without the integration is kept")
}

// services-server sends integration changes as the integration_changed
// replica broadcast; it must clear the same caches the fanout message does.
func TestIntegrationChangedBroadcast_ClearsCacheForListedAccounts(t *testing.T) {
	const a, b, other = "acct-integration-change-a", "acct-integration-change-b", "acct-integration-change-other"
	for _, id := range []string{a, b, other} {
		core.SetMCPIntegrationToolCache(id, []core.NBTool{})
		defer core.ClearMCPIntegrationToolCache(id)
	}

	common.ReplicaBroadcast(context.Background(), common.ChangeIntegration, a, b, a, "")

	for _, id := range []string{a, b} {
		_, present := core.GetMCPIntegrationToolCache(id)
		assert.False(t, present, "cache for account %s should be cleared", id)
	}
	_, present := core.GetMCPIntegrationToolCache(other)
	assert.True(t, present, "an account that was not listed keeps its cache")
}
