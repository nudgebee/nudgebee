package core

import (
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
)

// Installs the conversation-side hooks the LLM runtime calls (see llm_store.go).
func init() {
	llmruntime.GetStoreFn = func() llmruntime.Store { return GetConversationDao() }
	llmruntime.PeekStoreFn = func() llmruntime.Store { return PeekConversationDao() }
	llmruntime.ConversationOverrideFn = GetConversationOverride
	llmruntime.AddRunTokensFn = func(ctx *security.RequestContext, tokens int) { policyFor(ctx).AddTokens(ctx, tokens) }
}
