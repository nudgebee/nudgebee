package core

import (
	"context"
	"fmt"
	"math"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"sort"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

type ConversationSuggestionRequest struct {
	ConversationId string `json:"conversation_id" validate:"required"`
	AccountId      string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId         string `json:"user_id" mapstructure:"required"`
	MessageId      string `json:"message_id" validate:"required"`
}

type ConversationSuggestionMessageResponse struct {
	Message string `json:"message" validate:"required"`
}

type ConversationSuggestionResponse struct {
	ConversationId string                                  `json:"conversation_id" validate:"required"`
	AccountId      string                                  `json:"account_id" mapstructure:"required" validate:"required"`
	MessageId      string                                  `json:"message_id" validate:"required"`
	Suggestions    []ConversationSuggestionMessageResponse `json:"suggestions" validate:"required"`
}

var llmSystemInstructionForSuggestions = prompts_repo.GetPrompt(prompts_repo.PromptConversationSuggestion)

func HandleConversationSuggestionRequest(ctx *security.RequestContext, request ConversationSuggestionRequest) (ConversationSuggestionResponse, error) {
	mesasge, err := GetConversationDao().GetConversationMessage(request.MessageId, request.AccountId, request.ConversationId)
	if err != nil {
		return ConversationSuggestionResponse{}, err
	}

	if mesasge.Suggestions != nil {
		var suggestions []ConversationSuggestionMessageResponse
		err = common.UnmarshalJson([]byte(*mesasge.Suggestions), &suggestions)
		if err == nil && len(suggestions) > 0 {
			return ConversationSuggestionResponse{
				ConversationId: request.ConversationId,
				AccountId:      request.AccountId,
				MessageId:      request.MessageId,
				Suggestions:    suggestions,
			}, err
		} else {
			ctx.GetLogger().Error("suggestions: unable to parse suggestions", "error", err, "suggestions", *mesasge.Suggestions)
		}
	}

	// Build the per-account tools list. Sort by agent name so the byte
	// sequence is deterministic across calls — ListAgents iterates a
	// map internally and Go map iteration is intentionally randomized,
	// so without this sort the cache blob would differ every call and
	// hit rate would collapse to ~0%.
	agentDtos := ListAgents(ctx, request.AccountId, true)
	sort.SliceStable(agentDtos, func(i, j int) bool { return agentDtos[i].Name < agentDtos[j].Name })
	agents := make([]string, 0, len(agentDtos))
	for _, a := range agentDtos {
		agents = append(agents, fmt.Sprintf("%s:  %s", a.Name, a.Description))
	}

	// Put the tools list in a second system message so it's captured by
	// the Account-scope cache below (stable per account, changes only
	// when agents are enabled/disabled). Previously this was inlined into
	// the human message, which meant ~500-800 tokens of per-account-static
	// content shipped fresh on every call.
	llmMessages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, llmSystemInstructionForSuggestions),
		llms.TextParts(llms.ChatMessageTypeSystem, "available_tools:\n"+strings.Join(agents, "\n")),
		llms.TextParts(
			llms.ChatMessageTypeHuman,
			fmt.Sprintf(
				"question: %s\nanswer: %s",
				mesasge.Message,
				mesasge.Response,
			),
		),
	}

	maxAttempts := 3
	var response *llms.ContentResponse
	var lastErr error

	// Account cache scope: the second system message above contains the
	// per-account available_tools list, so different accounts must land in
	// different cache slots. Global scope keys on agent+model only (see
	// generateCacheKey), which would cause every account to thrash a single
	// shared slot each time the tools-list bytes differ.
	suggestionCtx := security.NewRequestContext(
		context.WithValue(
			context.WithValue(ctx.GetContext(), ContextKeyCacheScope, CacheScopeAccount),
			ContextKeyModelTier, ModelTierSummary,
		),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	// Create a slice of integers to iterate over
	for attempt := range make([]int, maxAttempts) {
		if attempt > 0 {
			// Add exponential backoff
			backoffDuration := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			ctx.GetLogger().Info("llm: retrying content generation",
				"attempt", attempt+1,
				"maxAttempts", maxAttempts,
				"backoff", backoffDuration.String(),
				"error", lastErr)
			time.Sleep(backoffDuration)
		}

		response, err = GenerateAndTrackLLMContent(suggestionCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, "conversation_suggestion", true, llmMessages, true, llms.WithTemperature(0.7), llms.WithJSONMode())
		if err == nil {
			break // Success, exit the retry loop
		}

		lastErr = err
		errMsg := err.Error()

		// Check if error is retryable
		isRetryableErr := strings.Contains(errMsg, "Model has timed out") ||
			strings.Contains(errMsg, "StatusCode: 408") ||
			strings.Contains(errMsg, "googleapi: Error 500:") ||
			strings.Contains(errMsg, "googleapi: Error 503:")

		if isRetryableErr && attempt < maxAttempts-1 {
			continue // Retryable error and we have attempts left
		}

		// Non-retryable error or last attempt failed
		ctx.GetLogger().Error("llm: unable to generate content",
			"error", err,
			"attempt", attempt+1,
			"maxAttempts", maxAttempts)
		return ConversationSuggestionResponse{}, fmt.Errorf("failed to generate content after %d attempts: %w", attempt+1, err)
	}
	responseData := response.Choices[0].Content
	//somedata cleanup because llms are adding these json quotes
	responseData = strings.TrimPrefix(responseData, "```json")
	responseData = strings.TrimSuffix(responseData, "```")
	responseData = strings.TrimPrefix(responseData, "`")
	responseData = strings.TrimSuffix(responseData, "`")

	responseMap := map[string][]string{}
	err = common.UnmarshalJson([]byte(responseData), &responseMap)
	if err != nil {
		ctx.GetLogger().Error("suggestions: unable to parse response", "error", err, "response", responseData)
		return ConversationSuggestionResponse{}, err
	}

	suggestions := responseMap["suggested_questions"]
	messages := []ConversationSuggestionMessageResponse{}
	for _, suggestion := range suggestions {
		messages = append(messages, ConversationSuggestionMessageResponse{
			Message: suggestion,
		})
	}

	if len(messages) > 0 {
		err = GetConversationDao().SaveConversationMessageSuggestion(request.MessageId, request.AccountId, request.ConversationId, messages)
		if err != nil {
			ctx.GetLogger().Error("suggestions: unable to save suggestions", "error", err)
		}
	}

	return ConversationSuggestionResponse{
		ConversationId: request.ConversationId,
		AccountId:      request.AccountId,
		MessageId:      request.MessageId,
		Suggestions:    messages,
	}, nil
}
