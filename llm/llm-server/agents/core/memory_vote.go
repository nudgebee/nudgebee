package core

import (
	"context"
	"fmt"
	"strings"

	"nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// voteSubjectMaxChars matches the budget the UI previously truncated to.
const voteSubjectMaxChars = 240

// DeriveVoteSubject turns the answer a user voted on into the subject of the
// decision that records the vote. The vote is about the finding, not the
// question that prompted it — using the query as the subject is what produced
// decisions reading "hi: user thumbs-up on RCA".
//
// Deliberately not extractLongTermMemory: that pass already ran when the answer
// was delivered, so re-running it here would re-emit every fact across every
// layer. Returns "" when the answer names no root cause worth remembering,
// which the caller treats as "record nothing".
func DeriveVoteSubject(ctx *security.RequestContext, accountID, conversationID, messageID, finalAnswer string) (string, error) {
	finalAnswer = strings.TrimSpace(finalAnswer)
	if finalAnswer == "" {
		return "", fmt.Errorf("DeriveVoteSubject: final answer is empty")
	}
	sc := ctx.GetSecurityContext()
	if sc == nil {
		return "", fmt.Errorf("DeriveVoteSubject: no security context")
	}

	votePrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptVoteSubject, accountID)
	if err != nil {
		return "", fmt.Errorf("DeriveVoteSubject: loading prompt: %w", err)
	}
	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: votePrompt}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: finalAnswer}}},
	}

	voteCtx := security.NewRequestContext(
		context.WithValue(context.WithValue(ctx.GetContext(), ContextKeyModelTier, ModelTierSummary), ContextKeyCacheScope, CacheScopeGlobal),
		sc,
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	result, err := GenerateAndTrackLLMContent(voteCtx, sc.GetUserId(), accountID, conversationID, messageID,
		"vote_subject", false, messages, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		return "", fmt.Errorf("DeriveVoteSubject: %w", err)
	}
	if result == nil || len(result.Choices) == 0 {
		return "", fmt.Errorf("DeriveVoteSubject: model returned no choices")
	}

	subject := strings.TrimSpace(result.Choices[0].Content)
	if subject == "" || strings.EqualFold(subject, "NONE") {
		return "", nil
	}
	if r := []rune(subject); len(r) > voteSubjectMaxChars {
		subject = strings.TrimSpace(string(r[:voteSubjectMaxChars]))
	}
	return subject, nil
}
