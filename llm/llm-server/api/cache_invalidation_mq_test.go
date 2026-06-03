package api

import (
	"nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessCacheInvalidationMessage_ClearsCacheForListedAccount(t *testing.T) {
	const accountId = "acct-cache-mq-test"
	core.SetMCPIntegrationToolCache(accountId, []core.NBTool{})
	defer core.ClearMCPIntegrationToolCache(accountId)

	msg := []byte(`{"account_ids":["` + accountId + `"]}`)
	err := processCacheInvalidationMessage(msg)
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
	err := processCacheInvalidationMessage(msg)
	assert.NoError(t, err)

	_, present := core.GetMCPIntegrationToolCache(accountId)
	assert.False(t, present, "cache entry should be removed for single-id payload")
}

func TestProcessCacheInvalidationMessage_DropsInvalidJson(t *testing.T) {
	err := processCacheInvalidationMessage([]byte("not-json"))
	// Invalid payloads must Ack (return nil) — requeueing would loop forever.
	assert.NoError(t, err, "malformed JSON must be dropped, not requeued")
}

func TestProcessCacheInvalidationMessage_DropsEmptyPayload(t *testing.T) {
	err := processCacheInvalidationMessage([]byte(`{}`))
	assert.NoError(t, err, "empty payload must drop without error")

	err = processCacheInvalidationMessage([]byte(`{"account_ids":[]}`))
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
	err := processCacheInvalidationMessage(msg)
	assert.NoError(t, err)

	_, presentA := core.GetMCPIntegrationToolCache(a)
	_, presentB := core.GetMCPIntegrationToolCache(b)
	assert.False(t, presentA, "cache for account %s should be cleared", a)
	assert.False(t, presentB, "cache for account %s should be cleared", b)
}
