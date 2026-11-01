package core

import (
	"context"
	"nudgebee/llm/security"
	"sync"
	"sync/atomic"

	"github.com/tmc/langchaingo/llms"
)

// LLMClient defines the interface for LLM operations that tools can use
// This breaks the import cycle between tools and agents/core
type LLMClient interface {
	GenerateContent(
		ctx *security.RequestContext,
		userId, accountId, conversationId, messageId, agentId string,
		trackContent bool,
		promptMessages []llms.MessageContent,
		cleanupMarkdown bool,
		options ...llms.CallOption,
	) (*llms.ContentResponse, error)
}

// promptCachingContextKey types ContextKeyDisableCaching. It lives here, not in
// agents/core, so tools can set it without importing agents/core.
type promptCachingContextKey string

// ContextKeyDisableCaching disables provider-level prompt caching for the current
// request subtree (agents/core re-exports it). Set to true for
// AgentPlannerTypeCustom agents whose LLM calls embed dynamic content (query
// text, log data) directly in the system message, making caching wasteful.
// Individual custom agents that know a specific call has a stable system prompt
// can create a sub-context that overrides this to false.
const ContextKeyDisableCaching promptCachingContextKey = "disable_caching"

// WithPromptCachingDisabled returns ctx with conversation-keyed prompt caching
// turned off. For one-shot calls (a single filter or planning prompt) that pass
// a conversation id only so their token usage is billed to it: the id must not
// also create a conversation cache entry that nothing will reuse.
func WithPromptCachingDisabled(ctx *security.RequestContext) *security.RequestContext {
	return ctx.WithContext(context.WithValue(ctx.GetContext(), ContextKeyDisableCaching, true))
}

var (
	llmClient     atomic.Value // stores LLMClient
	llmClientOnce sync.Once
)

// SetLLMClient registers the LLM client implementation
// This is called by agents/core during initialization
// Thread-safe: can only be set once, subsequent calls are ignored
func SetLLMClient(client LLMClient) {
	llmClientOnce.Do(func() {
		llmClient.Store(client)
	})
}

// GetLLMClient returns the registered LLM client
// Tools can use this to generate LLM content without importing agents/core
// Thread-safe: safe for concurrent access
func GetLLMClient() LLMClient {
	if v := llmClient.Load(); v != nil {
		return v.(LLMClient)
	}
	return nil
}
