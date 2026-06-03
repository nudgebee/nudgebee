package core

import (
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/tmc/langchaingo/llms"
)

// llmClientAdapter implements toolcore.LLMClient interface
// This allows tools package to use GenerateAndTrackLLMContent without importing agents/core
type llmClientAdapter struct{}

// Wire up the adapter at package initialization
func init() {
	toolcore.SetLLMClient(&llmClientAdapter{})
}

// GenerateContent implements toolcore.LLMClient interface
// Delegates to the existing GenerateAndTrackLLMContent function
func (a *llmClientAdapter) GenerateContent(
	ctx *security.RequestContext,
	userId, accountId, conversationId, messageId, agentId string,
	trackContent bool,
	promptMessages []llms.MessageContent,
	cleanupMarkdown bool,
	options ...llms.CallOption,
) (*llms.ContentResponse, error) {
	return GenerateAndTrackLLMContent(
		ctx,
		userId,
		accountId,
		conversationId,
		messageId,
		agentId,
		trackContent,
		promptMessages,
		cleanupMarkdown,
		options...,
	)
}
