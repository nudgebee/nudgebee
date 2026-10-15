package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
)

func TestDelegatedAccountContext(t *testing.T) {
	orig := loadActiveGlobalContextFn
	t.Cleanup(func() { loadActiveGlobalContextFn = orig })
	var loaded []string
	loadActiveGlobalContextFn = func(_ *security.RequestContext, accountId string) string {
		loaded = append(loaded, accountId)
		return "context of " + accountId
	}
	ctx := security.NewRequestContextForSuperAdmin()
	query := core.NBAgentRequest{AccountId: "chat-acc", AccountContext: "context of chat-acc"}

	assert.Equal(t, "context of chat-acc", delegatedAccountContext(ctx, query, "chat-acc"))
	assert.Empty(t, loaded, "same account reuses the request's context")

	assert.Equal(t, "context of target-acc", delegatedAccountContext(ctx, query, "target-acc"))
	assert.Equal(t, []string{"target-acc"}, loaded, "a targeted call uses the queried account's context")
}
