package core

import (
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
