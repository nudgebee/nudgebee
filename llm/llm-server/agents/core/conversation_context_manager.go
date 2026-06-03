package core

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"nudgebee/llm/security"
	"sort"
	"strings"
	"time"

	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/tmc/langchaingo/llms"
)

// ConversationContextManager manages the conversation context
type ConversationContextManager struct{}

// ConversationContextData represents the structured context data
type ConversationContextData struct {
	// Tracks mentioned entities and their attributes
	Entities map[string]EntityInfo `json:"entities,omitempty"`

	// Tracks the conversation topic and flow
	ConversationSummary string `json:"conversation_summary,omitempty"`

	// Tracks previous intents for continuity
	IntentHistory []IntentInfo `json:"intent_history,omitempty"`

	// Tracks references (it, that, they, etc.)
	References map[string]string `json:"references,omitempty"`

	// Tracks the last mentioned subject
	LastSubject string `json:"last_subject,omitempty"`
}

// EntityInfo stores information about an entity mentioned in conversation
type EntityInfo struct {
	Value       string         `json:"value"`
	Type        string         `json:"type,omitempty"`
	MentionedAt time.Time      `json:"mentioned_at"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// IntentInfo stores information about user intents
type IntentInfo struct {
	Intent    string    `json:"intent"`
	Timestamp time.Time `json:"timestamp"`
}

// GetContextManager returns the singleton instance of ConversationContextManager
func GetContextManager() *ConversationContextManager {
	return &ConversationContextManager{}
}

func (m *ConversationContextManager) SummarizeConversationContext(ctx *security.RequestContext, userId string, accountId string, conversationId string, messageId string, query string, history string) (string, error) {
	// If query or history is empty, nothing to summarize
	if query == "" || history == "" {
		return "", nil
	}

	systemPrompt := `You are a helpful assistant that reviews conversation history to extract context relevant to answering the user's latest question.
Only include facts that are still valid and do not require re-evaluation. This includes configuration details, user preferences, previously asked questions, or static descriptions.
Do NOT include prior answers to questions that involve real-time, dynamic, or frequently changing information (e.g., system counts, current status, metrics, time-based data).
Be brief, accurate, and return only factual information. If nothing is useful or valid, return an empty string.`

	userPrompt := fmt.Sprintf("Current user question:\n%s\n\nConversation history:\n%s\n\nBased on the conversation history, extract only valid and reusable information relevant to the current question. If nothing is relevant or still valid, return an empty string.", query, history)

	mclist := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: userPrompt}},
		},
	}

	summaryCtx := security.NewRequestContext(
		context.WithValue(context.WithValue(ctx.GetContext(), ContextKeyModelTier, ModelTierSummary), ContextKeyCacheScope, CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	llmResponse, err := GenerateAndTrackLLMContent(summaryCtx, userId, accountId, conversationId, messageId, "conversation_context_summary", false, mclist, true, WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		return "", err
	}

	if llmResponse != nil && len(llmResponse.Choices) > 0 {
		return llmResponse.Choices[0].Content, nil
	}

	return "", nil
}

// context propagation work in progress
func getJsonFromLlm(ctx *security.RequestContext, inputString string, userId string, accountId string, conversationId string, messageId string) (string, error) {
	// Call LLM to extract entities, references, and intent
	mclist := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: "You are a helpful assistant that extracts json data from a given context string. The context string may contain entities, references, and intents. Output the extracted data in a valid JSON format without any additional text or explanations."}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: inputString}},
		},
	}

	jsonCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), ContextKeyCacheScope, CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	llmResponse, err := GenerateAndTrackLLMContent(jsonCtx, userId, accountId, conversationId, messageId, "context_extraction_agent", false, mclist, true)
	if err != nil {
		return "{}", err
	}
	if llmResponse != nil && len(llmResponse.Choices) > 0 {
		var result map[string]any
		err := common.UnmarshalJson([]byte(llmResponse.Choices[0].Content), &result)
		if err != nil {
			ctx.GetLogger().Warn("Failed to unmarshal LLM response", "error", err, "response", llmResponse.Choices[0].Content)
			return "{}", err
		}
		return llmResponse.Choices[0].Content, nil
	}
	return "{}", nil
}

// context propagation work in progress // truncateString truncates a string to the specified length with ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// context propagation work in progress // parseAndValidateJSONResponse extracts and validates JSON from LLM response
func parseAndValidateJSONResponse(response string) (map[string]any, error) {
	// Clean and extract JSON from response
	response = strings.TrimSpace(response)

	// Handle cases where LLM wraps JSON in markdown
	if strings.Contains(response, "```json") {
		start := strings.Index(response, "```json") + 7
		end := strings.LastIndex(response, "```")
		if start < end && end > start {
			response = strings.TrimSpace(response[start:end])
		}
	}

	// Attempt to parse the response as-is first
	var result map[string]any
	if err := json.Unmarshal([]byte(response), &result); err == nil {
		return result, nil
	}

	// If parsing fails, try to extract a JSON block from the text
	if jsonStart := strings.Index(response, "{"); jsonStart != -1 {
		if jsonEnd := strings.LastIndex(response, "}"); jsonEnd != -1 && jsonEnd > jsonStart {
			jsonStr := response[jsonStart : jsonEnd+1]
			if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
				return result, nil
			}
		}
	}

	// If all attempts fail, return an error
	return nil, fmt.Errorf("failed to parse or extract valid JSON from response: starts with '%s', ends with '%s'",
		truncateString(response, 20),
		response[max(0, len(response)-20):])
}

// max returns the maximum of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ExtractContextFromExchange analyzes a user query and system response to extract context.
// agentName identifies the agent that produced the response, used for domain-aware context hints.
func (m *ConversationContextManager) ExtractContextFromExchange(ctx *security.RequestContext, query NBAgentRequest, response string, currentContext map[string]any, agentName string) (map[string]any, error) {
	// Validate inputs
	if query.Query == "" || response == "" {
		ctx.GetLogger().Debug("Empty query or response, returning current context")
		return currentContext, nil
	}

	if config.Config.ConversationContextEnabled {
		return m.extractSimpleContext(ctx, query, response, currentContext, agentName)
	}

	return m.extractContextFromExchangeOld(ctx, query, response, currentContext)
}

// unifiedExtraction sends the conversation transcript to the LLM and parses
// the returned LlmUnifiedExtraction. Called only during full redistillation.
// prevContext carries facts from the last distillation that may no longer
// appear in the history window — the LLM is instructed to preserve them.
func (m *ConversationContextManager) unifiedExtraction(
	ctx *security.RequestContext,
	query NBAgentRequest,
	userQuery string,
	fullTranscript string,
	prevContext *LlmUnifiedExtraction,
) (*LlmUnifiedExtraction, error) {

	system, prompt := BuildUnifiedContextExtractionPrompt(
		userQuery,
		fullTranscript,
		prevContext,
	)

	mclist := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: system}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: prompt}},
		},
	}

	extractCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), ContextKeyCacheScope, CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	llmResponse, err := GenerateAndTrackLLMContent(
		extractCtx,
		query.UserId,
		query.AccountId,
		query.ConversationId,
		query.MessageId,
		"context_memories_extractions",
		false,
		mclist,
		true,
	)
	if err != nil {
		ctx.GetLogger().Error("LLM call failed after retries", "error", err)
		return nil, err
	}

	if llmResponse == nil || len(llmResponse.Choices) == 0 {
		ctx.GetLogger().Warn("Empty LLM response from redistillation")
		return nil, fmt.Errorf("empty LLM response during redistillation")
	}

	rawContent := []byte(llmResponse.Choices[0].Content)

	unifiedCtx, memErr := ExtractUnifiedContext(rawContent)
	if memErr != nil {
		ctx.GetLogger().Warn("Failed to extract context with facts", "error", memErr)
		return nil, memErr
	}
	if unifiedCtx == nil {
		ctx.GetLogger().Warn("Unified context is nil after extraction")
		return nil, fmt.Errorf("nil context after redistillation extraction")
	}

	return unifiedCtx, nil
}

func mapFromStruct(contextConversationState *LlmUnifiedExtraction, stateMap *map[string]any) error {
	if stateMap == nil {
		return fmt.Errorf("stateMap is nil")
	}

	b, err := json.Marshal(contextConversationState)
	if err != nil {
		return fmt.Errorf("failed to marshal contextConversationState: %v", err)
	}

	if err := json.Unmarshal(b, stateMap); err != nil {
		return fmt.Errorf("failed to unmarshal JSON into stateMap: %v", err)
	}

	return nil
}

func (m *ConversationContextManager) extractContextFromExchangeOld(ctx *security.RequestContext, query NBAgentRequest, response string, currentContext map[string]any) (map[string]any, error) {
	// Get structured context data
	contextData := getStructuredContextData(currentContext)

	// Create prompt for LLM to extract context
	prompt := createContextExtractionPrompt(query.Query, response, contextData)

	mclist := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeSystem,
			// Parts: []llms.ContentPart{llms.TextContent{Text: memory.MemoryExtractionPromptSystem}},
			Parts: []llms.ContentPart{llms.TextContent{Text: "Extract relevant context from this conversation exchange."}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: prompt}},
		},
	}

	// Use the existing retry mechanism in GenerateAndTrackLLMContent
	llmResponse, err := GenerateAndTrackLLMContent(ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId, "conversation_context_extraction", false, mclist, true)
	if err != nil {
		ctx.GetLogger().Error("LLM call failed after retries", "error", err)
		return currentContext, err
	}

	if llmResponse == nil || len(llmResponse.Choices) == 0 {
		ctx.GetLogger().Warn("Empty LLM response, returning current context")
		return currentContext, nil
	}

	// Process the LLM response to update context with enhanced error handling
	updatedContext, err := processContextExtractionResponse(ctx, llmResponse.Choices[0].Content, contextData, query.AccountId, query.ConversationId, query.MessageId, query.UserId)
	if err != nil {
		ctx.GetLogger().Error("Context extraction failed, returning current context", "error", err)
		return currentContext, nil // Return current context instead of failing
	}

	// Convert back to map[string]any
	result, err := convertContextDataToMap(updatedContext)
	if err != nil {
		ctx.GetLogger().Error("Failed to convert context data to map, returning current context", "error", err)
		return currentContext, nil
	}

	return result, nil
}

// extractSimpleContext performs full re-distillation every N turns to capture
// long-term facts that will eventually slide out of the planner's history window.
//
// Between redistillation cycles no LLM call is made — the planner already
// receives the message history and can resolve references natively. Only the
// turn counter is incremented so the next redistillation fires on schedule.
//
// The stored context map is a serialised LlmUnifiedExtraction.
// Turn count is tracked via ConversationState.TurnCount.
func (m *ConversationContextManager) extractSimpleContext(
	ctx *security.RequestContext,
	query NBAgentRequest,
	response string,
	currentContext map[string]any,
	agentName string,
) (map[string]any, error) {
	convCtxData := getStructuredConversationContextData(currentContext)
	turnCount := convCtxData.ConversationState.TurnCount

	redistillInterval := config.Config.DistillationRedistillInterval

	// Decide: is this a re-distillation turn?
	// Always extract on the first turn (turnCount == 0) so the planner has a
	// populated "Current Subject" from the very start of the conversation.
	isFirstTurn := turnCount == 0
	needsFullRedistill := isFirstTurn || (redistillInterval > 0 && turnCount >= redistillInterval && turnCount%redistillInterval == 0)

	if needsFullRedistill {
		// Full re-distillation: load all messages in the window and extract.
		// Pass previous context so facts that slid out of the history window are preserved.
		ctx.GetLogger().Info("extractSimpleContext: performing full re-distillation", "turnCount", turnCount)
		unifiedCtx, err := m.fullRedistillUnified(ctx, query, response, convCtxData)

		if err != nil {
			ctx.GetLogger().Error("extractSimpleContext: redistillation failed, using stale context", "error", err)
			return currentContext, nil
		}
		if unifiedCtx == nil {
			return currentContext, nil
		}

		unifiedCtx.ConversationState.TurnCount = turnCount + 1
		unifiedCtx.ConversationState.UpdatedAt = time.Now()

		stateMap := make(map[string]any)
		if err := mapFromStruct(unifiedCtx, &stateMap); err != nil {
			ctx.GetLogger().Warn("Failed to convert unified context to map, returning current context", "error", err)
			return currentContext, nil
		}
		return stateMap, nil
	}

	// Non-redistillation turn: just bump the turn counter.
	// The planner already receives the message history window, so it resolves
	// references and context natively — no extra LLM call needed.
	ctx.GetLogger().Debug("extractSimpleContext: skipping extraction, planner has history", "turnCount", turnCount)
	convCtxData.ConversationState.TurnCount = turnCount + 1
	convCtxData.ConversationState.UpdatedAt = time.Now()

	stateMap := make(map[string]any)
	if err := mapFromStruct(convCtxData, &stateMap); err != nil {
		ctx.GetLogger().Warn("Failed to convert context to map, returning current context", "error", err)
		return currentContext, nil
	}
	return stateMap, nil
}

// fullRedistillUnified loads the full history window and produces a unified
// LlmUnifiedExtraction. Previous context is included so the LLM can preserve
// facts that may have slid out of the history window since the last distillation.
func (m *ConversationContextManager) fullRedistillUnified(
	ctx *security.RequestContext,
	query NBAgentRequest,
	response string,
	prevContext *LlmUnifiedExtraction,
) (*LlmUnifiedExtraction, error) {
	messages, err := GetConversationDao().LoadConversationMessages(
		query.AccountId,
		query.ConversationId,
		"",
		"",
		config.Config.ConversationHistoryWindowSize,
	)
	if err != nil {
		ctx.GetLogger().Error("fullRedistillUnified: failed to load messages", "error", err)
		return nil, err
	}

	const maxTranscriptLength = 50000

	var sb strings.Builder
	for _, msg := range messages {
		if msg["id"] == query.MessageId {
			continue
		}
		if sb.Len() > maxTranscriptLength {
			ctx.GetLogger().Warn("fullRedistillUnified: transcript truncated",
				"length", sb.Len(), "limit", maxTranscriptLength)
			break
		}
		mType := MessageType(msg["message_type"])
		if mType == MessageTypeFollowup {
			if msg["content"] != "" {
				sb.WriteString("Assistant: " + msg["content"] + "\n")
			}
			if msg["response"] != "" {
				sb.WriteString("User: " + msg["response"] + "\n")
			}
		} else {
			if msg["content"] != "" {
				sb.WriteString("User: " + msg["content"] + "\n")
			}
			if msg["response"] != "" {
				sb.WriteString("Assistant: " + msg["response"] + "\n")
			}
		}
	}
	sb.WriteString("User: " + query.Query + "\n")
	sb.WriteString("Assistant: " + response + "\n")

	fullTranscript := sb.String()
	if fullTranscript == "" {
		return &LlmUnifiedExtraction{}, nil
	}

	// Redistill: pass previous context so facts outside the history window are preserved
	return m.unifiedExtraction(
		ctx, query,
		query.Query,
		fullTranscript,
		prevContext,
	)
}

// ContextualizeQuery enhances a query with conversation context
func (m *ConversationContextManager) ContextualizeQuery(ctx *security.RequestContext, query NBAgentRequest, conversationContext map[string]any) (string, error) {
	if len(conversationContext) == 0 {
		return query.Query, nil
	}

	// When ConversationContextEnabled, skip query rewriting — the agent LLM resolves
	// references naturally from the increased history window + summary context.
	if config.Config.ConversationContextEnabled {
		return query.Query, nil
	}

	// Get structured context data
	contextData := getStructuredContextData(conversationContext)

	// Check if query needs contextualization
	needsContextualization, err := m.queryNeedsContextualization(ctx, query.Query, contextData, query.AccountId, query.ConversationId, query.MessageId, query.UserId)
	if err != nil {
		ctx.GetLogger().Error("Failed to check if query needs contextualization", "error", err)
		return query.Query, nil
	}
	if !needsContextualization {
		return query.Query, nil
	}

	// Create prompt for LLM to rewrite query with context
	prompt := createQueryContextualizationPrompt(query.Query, contextData, conversationContext)

	mclist := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: "You are a helpful assistant that clarifies user queries by incorporating context from previous conversation."}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: prompt}},
		},
	}

	llmResponse, err := GenerateAndTrackLLMContent(ctx, query.UserId, query.AccountId, query.ConversationId, query.MessageId, "conversation_query_context", false, mclist, true)
	if err != nil {
		return query.Query, err
	}

	if llmResponse != nil && len(llmResponse.Choices) > 0 {
		response := llmResponse.Choices[0].Content

		//patch to handle specific agentName
		if strings.HasPrefix(query.Query, "@") {
			response = strings.Fields(query.Query)[0] + " " + response
		}

		// Save the context reference for debugging when contextualizing
		unifiedCtx := getStructuredConversationContextData(conversationContext)
		if unifiedCtx != nil {
			if err := GetConversationDao().SaveConversationContextReference(
				query.AccountId,
				query.ConversationId,
				query.MessageId,
				unifiedCtx,
			); err != nil {
				ctx.GetLogger().Warn(
					"Failed to save conversation context reference",
					"error", err,
				)
			}
		}

		return response, nil
	}

	return query.Query, nil
}

// context propagation work in progress // queryNeedsContextualization checks if a query lacks context and needs clarification
func (m *ConversationContextManager) queryNeedsContextualization(
	ctx *security.RequestContext,
	query string,
	contextData *ConversationContextData,
	accountId string,
	conversationId string,
	messageId string,
	userId string,
) (bool, error) {

	// Determine availability independently
	hasContext := contextData != nil &&
		(len(contextData.Entities) > 0 ||
			len(contextData.References) > 0 ||
			contextData.LastSubject != "")

	// Nothing to contextualize with
	if !hasContext {
		return false, nil
	}

	// Detect ambiguous references
	ambiguousWords := []string{
		"it", "this", "that", "they", "them",
		"its", "their", "these", "those",
	}

	queryLower := strings.ToLower(query)

	for _, word := range ambiguousWords {
		if strings.Contains(queryLower, " "+word+" ") ||
			strings.HasPrefix(queryLower, word+" ") {
			// Ambiguous + something exists to resolve it
			return true, nil
		}
	}

	// Prepare safe context values
	entityCount := 0
	referenceCount := 0
	lastSubject := ""
	conxtprompt := ""

	if contextData != nil {

		entityCount = len(contextData.Entities)
		referenceCount = len(contextData.References)
		lastSubject = contextData.LastSubject
		conxtprompt = fmt.Sprintf(`
		- Entities: %d
		- References: %d
		- Last subject: %s`,
			entityCount,
			referenceCount,
			lastSubject,
		)
	}

	// Ask LLM only when ambiguity is subtle
	prompt := fmt.Sprintf(
		`Determine if the following query requires additional context to be fully understood.

		Query:
		%s
		Available information:
		%s

		Reply ONLY with "YES" or "NO".
		Respond "YES" only if the query is unclear without using the available information.`,
		query,
		conxtprompt,
	)

	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{
				llms.TextContent{
					Text: "You are a strict classifier. Respond YES only when the query cannot be understood on its own.",
				},
			},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: prompt}},
		},
	}

	llmResponse, err := GenerateAndTrackLLMContent(
		ctx,
		userId,
		accountId,
		conversationId,
		messageId,
		"conversation_query_context",
		false,
		messages,
		true,
	)
	if err != nil {
		// Fail-safe: do not contextualize if uncertain
		return false, nil
	}

	if llmResponse != nil && len(llmResponse.Choices) > 0 {
		return strings.TrimSpace(
			strings.ToUpper(llmResponse.Choices[0].Content),
		) == "YES", nil
	}

	return false, nil
}

// Helper functions

// getStructuredContextData converts a generic context map to structured data
func getStructuredContextData(contextMap map[string]any) *ConversationContextData {
	if contextMap == nil {
		return &ConversationContextData{
			Entities:      make(map[string]EntityInfo),
			References:    make(map[string]string),
			IntentHistory: []IntentInfo{},
		}
	}

	// Try to convert directly
	contextBytes, err := common.MarshalJson(contextMap)
	if err != nil {
		return &ConversationContextData{
			Entities:      make(map[string]EntityInfo),
			References:    make(map[string]string),
			IntentHistory: []IntentInfo{},
		}
	}

	var result ConversationContextData
	err = common.UnmarshalJson(contextBytes, &result)
	if err != nil || result.Entities == nil {
		return &ConversationContextData{
			Entities:      make(map[string]EntityInfo),
			References:    make(map[string]string),
			IntentHistory: make([]IntentInfo, 0),
		}
	}

	return &result
}

// getStructuredConversationContextData converts a generic context map to structured data
func getStructuredConversationContextData(contextMap map[string]any) *LlmUnifiedExtraction {
	if contextMap == nil {
		return &LlmUnifiedExtraction{}
	}

	// Try to convert directly
	contextBytes, err := common.MarshalJson(contextMap)
	if err != nil {
		return &LlmUnifiedExtraction{}
	}

	var result LlmUnifiedExtraction
	err = common.UnmarshalJson(contextBytes, &result)
	if err != nil {
		return &LlmUnifiedExtraction{}
	}

	return &result
}

// context propagation work in progress // convertContextDataToMap converts structured data back to a map
func convertContextDataToMap(data *ConversationContextData) (map[string]any, error) {
	result := make(map[string]any)

	dataBytes, err := common.MarshalJson(data)
	if err != nil {
		return result, err
	}

	err = common.UnmarshalJson(dataBytes, &result)
	return result, err
}

// createContextExtractionPrompt creates a prompt for context extraction
func createContextExtractionPromptOld(query, response string, contextData *ConversationContextData) string {
	contextJSON, _ := common.MarshalJsonIndent(contextData, "", "  ")

	return `Analyze this conversation exchange and extract context information:

USER QUERY: 
` + query + `

SYSTEM RESPONSE:
` + response + `

CURRENT CONTEXT:
` + string(contextJSON) + `

Instructions:
1. If user asks to "forget" or "remove" something, include it in the "entities_to_remove" field
2. For existing entities, UPDATE them with new information rather than duplicating
3. Only extract entities, references, and intents that are relevant and mentioned in this exchange
4. If user query contains words like "forget", "remove", "delete", "ignore" about specific entities, mark them for removal

Please extract:
1. Entities mentioned (with their types and attributes) - UPDATE existing ones with new info
2. References (pronouns like "it", "them", etc.)
3. Current user intent
4. Entities to remove/forget (if user asks to forget something)
5. A brief summary of what this conversation is about with the last discussed subject and any important technical details including but not limited to: metrics (system resources, performance indicators, scaling parameters), quantitative data, timestamps, locations, configurations, and other relevant numerical or factual information.

Return JSON in this format without any additional text or explanations:
{
  "entities": {
    "entity_name": {"value": "entity_value", "type": "entity_type", "attributes": {"attr1": "value1"}}
  },
  "entities_to_remove": ["entity1", "entity2"],
  "references": {"it": "reference_to_entity"},
  "intent": "user_intent",
  "summary": "brief conversation summary",
  "last_subject": "last_discussed_entity"
}`
}

// context propagation work in progress // createContextExtractionPrompt creates a prompt for context extraction
func createContextExtractionPrompt(query, response string, contextData *ConversationContextData) string {
	if !config.Config.ConversationContextEnabled {
		return createContextExtractionPromptOld(query, response, contextData)
	}
	contextJSON, _ := common.MarshalJsonIndent(contextData, "", "  ")

	return `Analyze this conversation exchange and extract context information:

USER QUERY: 
` + query + `

SYSTEM RESPONSE:
` + response + `

CURRENT CONTEXT:
` + string(contextJSON) + `

Instructions:
1. If user asks to "forget" or "remove" something, include it in the "entities_to_remove" field
2. For existing entities, UPDATE them with new information rather than duplicating
3. Only extract entities, references, and intents that are relevant and mentioned in this exchange
4. If user query contains words like "forget", "remove", "delete", "ignore" about specific entities, mark them for removal

Please extract:
1. Entities mentioned (with their types and attributes) - UPDATE existing ones with new info
2. References (pronouns like "it", "them", etc.)
3. Current user intent
4. Entities to remove/forget (if user asks to forget something)
5. A brief summary of what this conversation is about with the last discussed subject and any important technical details including but not limited to: metrics (system resources, performance indicators, scaling parameters), quantitative data, timestamps, locations, configurations, and other relevant numerical or factual information.

Return JSON in this format without any additional text or explanations:
{
  "entities": {
    "entity_name": {"value": "entity_value", "type": "entity_type", "attributes": {"attr1": "value1"}}
  },
  "entities_to_remove": ["entity1", "entity2"],
  "references": {"it": "reference_to_entity"},
  "intent": "user_intent",
  "summary": "brief conversation summary",
  "last_subject": "last_discussed_entity"
}`
}

// createQueryContextualizationPrompt creates a prompt for query contextualization
func createQueryContextualizationPromptOld(query string, contextData *ConversationContextData) string {
	contextJSON, _ := common.MarshalJsonIndent(contextData, "", "  ")

	return `Rewrite this query to be fully self-contained by incorporating relevant context information:

QUERY: ` + query + `

CONVERSATION CONTEXT:
` + string(contextJSON) + `

Important instructions:
1. Resolve pronouns (it, they, etc.) using the references
2. Include entity specifics where needed
3. Maintain the original intent of the query
4. Only add context that is necessary for understanding
5. Return ONLY the rewritten query without explanations
6. Must consider timestamp to ensure the query is relevant to the latest context`
}

// createQueryContextualizationPrompt creates a prompt for query contextualization
func createQueryContextualizationPrompt(
	query string,
	contextData *ConversationContextData,
	conversationContext map[string]any,
) string {
	if !config.Config.ConversationContextEnabled {
		return createQueryContextualizationPromptOld(query, contextData)
	}
	var sb strings.Builder

	sb.WriteString(`You are a SRE CoPilot query contextualizer. Rewrite the current query by incorporating relevant context from the provided operational knowledge.

TASK: Make the current query complete and unambiguous by resolving pronouns and implicit references using the RELEVANT OPERATIONAL KNOWLEDGE.

RULES:
1. **RESOLVE PRONOUNS**: Replace "it", "they", "them", "these", "those", "ones", etc. with specific resource names/types from the knowledge.
2. **CARRY FORWARD CONTEXT**: If the knowledge mentions specific namespaces, resource types, or labels, include them in the rewritten query.
3. **BE PRECISE**: Only add context that is directly relevant to resolving the current query's ambiguity.
4. **PRESERVE INTENT**: Keep the original query's action (list, get, describe, etc.) unchanged.

KEY INSIGHT: The "RELEVANT OPERATIONAL KNOWLEDGE" contains extracted facts from recent conversation turns. Use these to understand what the user is referring to.

EXAMPLES:

Example 1:
Knowledge: "pods are running in nudgebee namespace"
Current query: "list me healthy ones"
Rewritten: "list me healthy pods in nudgebee namespace"

Example 2:  
Knowledge: "nginx deployment is running in nudgebee namespace"
Current query: "get me logs"
Rewritten: "get me logs from nginx deployment in nudgebee namespace"

Example 3:
Knowledge: "Out-Of-Memory (OOMKilled) events result in a container termination, typically yielding an Exit Code 137"
Current query: "investigate it"
Rewritten: "investigate OOMKilled events in nudgebee namespace"



CURRENT QUERY:
`)
	sb.WriteString(query)
	sb.WriteString("\n\n")

	// Append relevant operational knowledge (memory facts)
	sb.WriteString("RELEVANT OPERATIONAL KNOWLEDGE:\n")

	unified := getStructuredConversationContextData(conversationContext)
	sb.WriteString(unified.String())
	sb.WriteString("\n")

	sb.WriteString(`ANALYSIS PROCESS:
1. Scan current query for pronouns or vague terms (it, they, ones, them, etc.)
2. Look at the RELEVANT OPERATIONAL KNOWLEDGE for what these terms likely refer to
3. Identify resource types, namespaces, names, or labels mentioned in the knowledge
4. Rewrite the query to replace pronouns with these specific references
5. Ensure the rewritten query is complete and unambiguous

OUTPUT REQUIREMENTS:
- Output ONLY the rewritten query
- No explanations, no markdown formatting
- Just the complete, contextualized query

If the current query is already complete and unambiguous (no pronouns or vague references), return it unchanged.

REWRITTEN QUERY:
`)

	return sb.String()
}

// context propagation work in progress // processContextExtractionResponse processes LLM's context extraction response
func processContextExtractionResponse(ctx *security.RequestContext, llmResponse string, currentContext *ConversationContextData, accountId string, conversationId string, messageId string, userId string) (*ConversationContextData, error) {
	// Try to parse JSON from the LLM response
	var extractedData struct {
		Entities         map[string]EntityInfo `json:"entities"`
		EntitiesToRemove []string              `json:"entities_to_remove"`
		References       map[string]string     `json:"references"`
		Intent           string                `json:"intent"`
		Summary          string                `json:"summary"`
		LastSubject      string                `json:"last_subject"`
	}

	// Log response for debugging
	ctx.GetLogger().Debug("Processing context extraction response",
		"response_length", len(llmResponse),
		"response_preview", truncateString(llmResponse, 200))

	// Enhanced JSON parsing with validation
	parsedData, err := parseAndValidateJSONResponse(llmResponse)
	if err != nil {
		ctx.GetLogger().Warn("Failed to parse JSON response directly", "error", err)
		return processWithFallback(ctx, llmResponse, currentContext, &extractedData, accountId, conversationId, messageId, userId)
	}

	// Convert parsed data to struct using DecodeMapToStruct for efficiency
	err = common.DecodeMapToStruct(parsedData, &extractedData)
	if err != nil {
		ctx.GetLogger().Warn("Failed to convert to expected structure", "error", err)
		return processWithFallback(ctx, llmResponse, currentContext, &extractedData, accountId, conversationId, messageId, userId)
	}

	return updateContextData(currentContext, &extractedData), nil
}

// context propagation work in progress // processWithFallback handles JSON parsing errors with graceful degradation
func processWithFallback(ctx *security.RequestContext, llmResponse string, currentContext *ConversationContextData, extractedData *struct {
	Entities         map[string]EntityInfo `json:"entities"`
	EntitiesToRemove []string              `json:"entities_to_remove"`
	References       map[string]string     `json:"references"`
	Intent           string                `json:"intent"`
	Summary          string                `json:"summary"`
	LastSubject      string                `json:"last_subject"`
}, accountId string, conversationId string, messageId string, userId string) (*ConversationContextData, error) {
	// Try the secondary JSON extraction only once
	jsonStr, err := getJsonFromLlm(ctx, llmResponse, userId, accountId, conversationId, messageId)
	if err != nil {
		ctx.GetLogger().Warn("Fallback JSON extraction failed, returning current context", "error", err)
		return currentContext, nil // Don't fail, just return existing context
	}

	err = json.Unmarshal([]byte(jsonStr), extractedData)
	if err != nil {
		ctx.GetLogger().Warn("Fallback JSON parsing failed, returning current context", "error", err)
		return currentContext, nil
	}

	return updateContextData(currentContext, extractedData), nil
}

// context propagation work in progress // updateContextData intelligently updates context data
func updateContextData(currentContext *ConversationContextData, extractedData *struct {
	Entities         map[string]EntityInfo `json:"entities"`
	EntitiesToRemove []string              `json:"entities_to_remove"`
	References       map[string]string     `json:"references"`
	Intent           string                `json:"intent"`
	Summary          string                `json:"summary"`
	LastSubject      string                `json:"last_subject"`
}) *ConversationContextData {
	now := time.Now()

	// Initialize maps if nil
	initializeContextMaps(currentContext)

	// Remove entities that user wants to forget
	removeEntitiesFromContext(currentContext, extractedData.EntitiesToRemove)

	// Smart update of entities
	updateEntitiesInContext(currentContext, extractedData.Entities, now)

	// Update other context fields
	updateContextFields(currentContext, extractedData, now)

	return currentContext
}

// context propagation work in progress // initializeContextMaps ensures context maps are initialized
func initializeContextMaps(currentContext *ConversationContextData) {
	if currentContext.Entities == nil {
		currentContext.Entities = make(map[string]EntityInfo)
	}
	if currentContext.References == nil {
		currentContext.References = make(map[string]string)
	}
}

// context propagation work in progress // removeEntitiesFromContext removes entities that user wants to forget
func removeEntitiesFromContext(currentContext *ConversationContextData, entitiesToRemove []string) {
	for _, entityToRemove := range entitiesToRemove {
		delete(currentContext.Entities, entityToRemove)
		// Also remove from references if it exists
		for refKey, refValue := range currentContext.References {
			if refValue == entityToRemove {
				delete(currentContext.References, refKey)
			}
		}
	}
}

// context propagation work in progress // updateEntitiesInContext performs smart update of entities
func updateEntitiesInContext(currentContext *ConversationContextData, newEntities map[string]EntityInfo, now time.Time) {
	for k, newEntity := range newEntities {
		if existingEntity, exists := currentContext.Entities[k]; exists {
			// Update existing entity
			updatedEntity := mergeEntityInfo(existingEntity, newEntity, now)
			currentContext.Entities[k] = updatedEntity
		} else {
			// Add new entity
			newEntity.MentionedAt = now
			newEntity.UpdatedAt = now
			currentContext.Entities[k] = newEntity
		}
	}
}

// context propagation work in progress // mergeEntityInfo merges new entity information with existing entity
func mergeEntityInfo(existing, new EntityInfo, now time.Time) EntityInfo {
	updated := existing
	updated.UpdatedAt = now

	// Update value if it's different and not empty
	if new.Value != "" && new.Value != existing.Value {
		updated.Value = new.Value
	}

	// Update type if provided
	if new.Type != "" {
		updated.Type = new.Type
	}

	// Merge attributes
	if updated.Attributes == nil {
		updated.Attributes = make(map[string]any)
	}
	for attrKey, attrValue := range new.Attributes {
		updated.Attributes[attrKey] = attrValue
	}

	return updated
}

// context propagation work in progress // updateContextFields updates references, summary, subject and intent
func updateContextFields(currentContext *ConversationContextData, extractedData *struct {
	Entities         map[string]EntityInfo `json:"entities"`
	EntitiesToRemove []string              `json:"entities_to_remove"`
	References       map[string]string     `json:"references"`
	Intent           string                `json:"intent"`
	Summary          string                `json:"summary"`
	LastSubject      string                `json:"last_subject"`
}, now time.Time) {
	// Update references (overwrite old ones)
	for k, v := range extractedData.References {
		currentContext.References[k] = v
	}

	// Update conversation summary if provided
	if extractedData.Summary != "" {
		currentContext.ConversationSummary = extractedData.Summary
	}

	// Update last subject if provided
	if extractedData.LastSubject != "" {
		currentContext.LastSubject = extractedData.LastSubject
	}

	// Add intent to history
	if extractedData.Intent != "" {
		currentContext.IntentHistory = append(currentContext.IntentHistory, IntentInfo{
			Intent:    extractedData.Intent,
			Timestamp: now,
		})

		// Keep intent history at a reasonable size
		if len(currentContext.IntentHistory) > 10 {
			currentContext.IntentHistory = currentContext.IntentHistory[len(currentContext.IntentHistory)-10:]
		}
	}
}

// BuildUnifiedContextExtractionPrompt builds the system + user prompt for full
// redistillation. The LLM receives the conversation transcript plus any
// previously distilled facts (which may cover turns that have since slid out
// of the history window) and produces an updated LlmUnifiedExtraction.
func BuildUnifiedContextExtractionPrompt(
	userQuery string,
	fullTranscript string,
	prevContext *LlmUnifiedExtraction,
) (systemPrompt string, userPrompt string) {

	// Include previous memory facts so the LLM can preserve knowledge from
	// turns that are no longer in the transcript.
	prevFactsSection := ""
	if prevContext != nil && len(prevContext.MemoryFacts) > 0 {
		factsJSON, _ := common.MarshalJsonIndent(prevContext.MemoryFacts, "", "  ")
		prevFactsSection = fmt.Sprintf(`
PREVIOUS MEMORY FACTS (from last distillation — retain if still valid, some may cover turns no longer in the transcript):
%s
`, string(factsJSON))
	}

	userPrompt = fmt.Sprintf(`FULL CONVERSATION TRANSCRIPT:
%s

CURRENT USER QUESTION: %s
%s
TASK:
- Extract conversation_state reflecting the current state of the investigation
- Extract ALL eligible memory_facts from the entire conversation
- Retain previous memory facts that are still valid (they may cover turns outside the transcript window)
- Drop previous facts only if they are contradicted or no longer relevant
- Focus salience scoring relative to the current user question
- Output valid JSON with conversation_state and memory_facts
`,
		fullTranscript,
		userQuery,
		prevFactsSection,
	)

	return prompts_repo.GetPrompt(prompts_repo.PromptUnifiedContextMemory), userPrompt
}

// WorkingContext holds domain-specific key-value properties for the current
// investigation.  The Domain field identifies which agent category populated
// the context (e.g. "kubernetes", "aws", "datadog", "postgres") so that
// downstream consumers can interpret the Properties map correctly.
type WorkingContext struct {
	Domain     string         `json:"domain,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	TimeRange  string         `json:"time_range,omitempty"`
}

type LlmConversationState struct {
	Topic             string         `json:"topic"`
	LastFocus         string         `json:"last_focus"`
	WorkingContext    WorkingContext `json:"working_context,omitempty"`
	ActiveConstraints []string       `json:"active_constraints"`
	SettledDecisions  []string       `json:"settled_decisions"`
	DoNotRevisit      []string       `json:"do_not_revisit"`
	FailedApproaches  []string       `json:"failed_approaches,omitempty"`
	TurnCount         int            `json:"turn_count"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

func (c *LlmConversationState) UnmarshalJSON(data []byte) error {
	type Alias LlmConversationState
	var aux Alias
	if err := json.Unmarshal(data, &aux); err == nil {
		*c = LlmConversationState(aux)
		return nil
	}

	// If unmarshaling into struct fails, check if it's a string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		// It's a string, treat it as the topic
		*c = LlmConversationState{
			Topic: s,
		}
		return nil
	}

	// If it's neither an object nor a string, return the original error (or a generic one)
	return fmt.Errorf("conversation_state must be an object or a string")
}

func (c *LlmConversationState) String() string {
	var sb strings.Builder

	// Emit a structured "Current Subject" block that the planner can
	// mechanically reference when the user's query is ambiguous.
	hasSubject := c.Topic != "" || c.LastFocus != "" || c.WorkingContext.Domain != "" || len(c.WorkingContext.Properties) > 0
	if hasSubject {
		sb.WriteString("### Current Subject\n")
		if c.Topic != "" {
			sb.WriteString("- **Topic:** " + c.Topic + "\n")
		}
		if c.LastFocus != "" {
			sb.WriteString("- **Focus:** " + c.LastFocus + "\n")
		}
		if c.WorkingContext.Domain != "" {
			sb.WriteString("- **Domain:** " + c.WorkingContext.Domain + "\n")
		}
		for k, v := range c.WorkingContext.Properties {
			fmt.Fprintf(&sb, "- **%s:** %v\n", k, v)
		}
		if c.WorkingContext.TimeRange != "" {
			sb.WriteString("- **Time Range:** " + c.WorkingContext.TimeRange + "\n")
		}
	}

	writeStringSlice(&sb, "Active Constraints", c.ActiveConstraints)
	writeStringSlice(&sb, "Settled Decisions", c.SettledDecisions)
	writeStringSlice(&sb, "Do Not Revisit", c.DoNotRevisit)
	writeStringSlice(&sb, "Failed Approaches", c.FailedApproaches)

	return sb.String()
}

func writeStringSlice(sb *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	sb.WriteString(label + ":\n")
	for _, item := range items {
		sb.WriteString("  - " + item + "\n")
	}
}

// String returns a human-readable representation of the unified extraction,
// suitable for injection into LLM prompts. The output uses a structured
// "Current Subject" + "Established Facts" format so planners can mechanically
// resolve ambiguous user queries by defaulting to the subject properties.
func (u *LlmUnifiedExtraction) String() string {
	var sb strings.Builder

	stateStr := u.ConversationState.String()
	if stateStr != "" {
		sb.WriteString(stateStr)
	}

	if len(u.MemoryFacts) > 0 {
		sb.WriteString("### Established Facts\n")
		for _, f := range u.MemoryFacts {
			fmt.Fprintf(&sb, "- [%s] %s\n", f.Type, f.Content)
		}
	}

	return sb.String()
}

// LlmMemoryFact represents an atomic, persistent fact extracted from conversation.
// Type should be one of the domain-agnostic values:
//
//	configuration, resource_state, metric, event, error_pattern, relationship,
//	query_pattern, credential_ref, api_endpoint, user_preference, decision,
//	tooling_stack, constraint, correction
type LlmMemoryFact struct {
	Content  string         `json:"content"`
	Type     string         `json:"type"`
	Tags     []string       `json:"tags"`
	Metadata map[string]any `json:"metadata"`
	Salience float64        `json:"salience"`
}

type LlmUnifiedExtraction struct {
	ConversationState LlmConversationState `json:"conversation_state"`
	MemoryFacts       []LlmMemoryFact      `json:"memory_facts"`
}

func ExtractUnifiedContext(raw []byte) (*LlmUnifiedExtraction, error) {

	var parsed LlmUnifiedExtraction
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("invalid unified JSON: %w", err)
	}

	out := make([]LlmMemoryFact, 0, len(parsed.MemoryFacts))

	for _, m := range parsed.MemoryFacts {

		// HARD filters — do not relax these
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		if m.Salience < 0.3 {
			continue
		}
		if m.Type == "" {
			continue
		}

		out = append(out, LlmMemoryFact{
			Content:  m.Content,
			Type:     m.Type,
			Tags:     dedupeStrings(m.Tags),
			Metadata: m.Metadata,
			Salience: m.Salience,
		})
	}

	return &LlmUnifiedExtraction{
		ConversationState: parsed.ConversationState,
		MemoryFacts:       out,
	}, nil
}

func MergeFacts(oldFacts, newFacts []LlmMemoryFact) []LlmMemoryFact {
	// 1. Start with all new facts (trust latest LLM extraction as the primary source)
	result := make([]LlmMemoryFact, 0, len(oldFacts)+len(newFacts))
	result = append(result, newFacts...)

	// 2. Identify which old facts should be carried forward ("Safety Net")
	for _, of := range oldFacts {
		isRepresented := false

		// Check if any new fact is "essentially the same" as this old fact
		for _, nf := range newFacts {
			if areFactsSimilar(of, nf) {
				isRepresented = true
				break
			}
		}

		if !isRepresented {
			// If the LLM omitted an old fact, we only carry it forward if:
			// a) It's highly salient (threshold 0.5+) — we don't carry forward trivia.
			// b) It doesn't appear to be superseded (handled by similarity check above).
			if of.Salience >= 0.5 {
				result = append(result, of)
			}
		}
	}

	// 3. Cap total facts to avoid unbounded growth. Keep highest-salience facts.
	maxFacts := config.Config.MaxMemoryFactsPerConversation
	if maxFacts < 10 {
		maxFacts = 10
	}
	if len(result) > maxFacts {
		sort.Slice(result, func(i, j int) bool {
			return result[i].Salience > result[j].Salience
		})
		result = result[:maxFacts]
	}
	return result
}

func areFactsSimilar(f1, f2 LlmMemoryFact) bool {
	// Exact match
	if f1.Content == f2.Content {
		return true
	}

	// Type mismatch usually means different facts even if wording is similar
	if f1.Type != f2.Type {
		return false
	}

	s1 := strings.ToLower(strings.TrimSpace(f1.Content))
	s2 := strings.ToLower(strings.TrimSpace(f2.Content))

	// Normalization check
	if s1 == s2 {
		return true
	}

	// Fuzzy check: Levenshtein distance
	dist := fuzzy.LevenshteinDistance(s1, s2)
	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}

	if maxLen == 0 {
		return true
	}

	// Heuristic: If similarity is > 85%, they are likely the same fact
	return float64(dist)/float64(maxLen) < 0.15
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
