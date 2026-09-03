// Package openai is a fork of github.com/tmc/langchaingo/llms/openai at
// v0.1.14, vendored here for the same reason as the anthropic, azure, bedrock,
// googleai, huggingface and sagemaker clients: it is the client that serves
// BOTH the `openai` and `custom` providers, so upstream defects land on the
// majority of our traffic with no way to patch them.
//
// Local changes are marked with "NUDGEBEE:" comments. Keep that marker on every
// divergence so a future re-sync against upstream can find them.
// Package openai provides an interface to OpenAI's language models.
//
// # Token Limits
//
// For setting token limits with OpenAI models, use openai.WithMaxCompletionTokens()
// for clarity. The OpenAI API now uses max_completion_tokens as the field for
// limiting output tokens.
//
//	// Recommended for clarity:
//	llm.GenerateContent(ctx, messages,
//	    openai.WithMaxCompletionTokens(100),
//	)
//
//	// Also works (backward compatible):
//	llm.GenerateContent(ctx, messages,
//	    llms.WithMaxTokens(100),
//	)
//
// Both options set the same underlying field. By default, the implementation sends
// max_completion_tokens (modern field). For older OpenAI-compatible servers that
// only support max_tokens, use WithLegacyMaxTokensField():
//
//	llm.GenerateContent(ctx, messages,
//	    llms.WithMaxTokens(100),
//	    openai.WithLegacyMaxTokensField(), // Forces use of max_tokens field
//	)
package openai
