package core

import (
	"context"
	"fmt"
	"math/rand"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// AcknowledgmentResponse represents the immediate acknowledgment response
type AcknowledgmentResponse struct {
	Status            string `json:"status"`
	Acknowledgment    string `json:"acknowledgment"`
	ConversationId    string `json:"conversation_id"`
	MessageId         string `json:"message_id"`
	EstimatedTime     string `json:"estimated_time"`
	InterpretedIntent string `json:"interpreted_intent"`
}

// generateUserAcknowledgment generates an immediate acknowledgment for the user's question
func generateUserAcknowledgment(ctx *security.RequestContext, userId string, accountId string, conversationId string, messageId string, query string, agentName string) (string, string, error) {
	_, err := GetLlmModel(ctx, "summary_agent", accountId, conversationId)
	if err != nil {
		ctx.GetLogger().Warn("acknowledgment: unable to get LLM model, using fallback", "error", err)
		return generateFallbackAcknowledgment(query, agentName)
	}

	systemPrompt := fmt.Sprintf(`You are an expert AI assistant named %s created by %s that provides immediate acknowledgments for user questions.`, config.Config.AIAssistantName, config.Config.AIAssistantCompany) + ` Your task is to:

1. Quickly understand the user's intent and what they're trying to accomplish
2. Provide a brief, professional acknowledgment that shows understanding
3. Generate a clear interpretation of their intent

Guidelines:
- Be concise (1-2 sentences max for acknowledgment)
- Show that you understand what they're asking
- Be professional and helpful in tone
- Don't provide the actual answer, just acknowledge the request
- Focus on the main intent, not minor details
- Include any identifiers or keywords that clarify the intent

Format your response as:
ACKNOWLEDGMENT: [brief acknowledgment message]
INTENT: [clear interpretation of what the user wants to accomplish]

Examples:
User: "Why is my pod crashing in the production namespace?"
ACKNOWLEDGMENT: I understand you're experiencing pod crashes in your production environment. Let me investigate the issue for you.
INTENT: Troubleshoot and identify the root cause of pod crashes in production namespace

User: "Show me CPU usage for the last 24 hours"
ACKNOWLEDGMENT: I'll help you retrieve CPU usage metrics for the past 24 hours.
INTENT: Display CPU utilization metrics over a 24-hour time period

User: "Investigate event ID 12345 for errors"
ACKNOWLEDGMENT: I see you want to investigate event ID 12345 for potential errors. I'll look into it right away.
INTENT: Analyze event ID 12345 to identify any associated errors or issues`

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, fmt.Sprintf("User Query: %s", query)),
	}

	// Use Lite model for acknowledgment
	ackCtx := security.NewRequestContext(
		context.WithValue(context.WithValue(ctx.GetContext(), ContextKeyUseLiteModel, true), ContextKeyCacheScope, CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := GenerateAndTrackLLMContent(ackCtx, userId, accountId, conversationId, messageId, "summary_agent", false, messageContent, true, WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		ctx.GetLogger().Warn("acknowledgment: LLM call failed, using fallback", "error", err)
		return generateFallbackAcknowledgment(query, agentName)
	}

	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		ctx.GetLogger().Warn("acknowledgment: LLM generated empty response, using fallback")
		return generateFallbackAcknowledgment(query, agentName)
	}

	return parseAcknowledgmentResponse(completion.Choices[0].Content, query, agentName)
}

// parseAcknowledgmentResponse parses the LLM response and extracts acknowledgment and intent
func parseAcknowledgmentResponse(response, query, agentName string) (string, string, error) {
	lines := strings.Split(response, "\n")
	var acknowledgment, intent string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ACKNOWLEDGMENT:") {
			acknowledgment = strings.TrimSpace(strings.TrimPrefix(line, "ACKNOWLEDGMENT:"))
		} else if strings.HasPrefix(line, "INTENT:") {
			intent = strings.TrimSpace(strings.TrimPrefix(line, "INTENT:"))
		}
	}

	// Fallback if parsing fails
	if acknowledgment == "" || intent == "" {
		return generateFallbackAcknowledgment(query, agentName)
	}

	return acknowledgment, intent, nil
}

// generateFallbackAcknowledgment provides a fallback acknowledgment when LLM is unavailable
func generateFallbackAcknowledgment(query, agentName string) (string, string, error) {
	// Determine intent based on keywords and agent
	intent := generateFallbackIntent(query, agentName)

	acknowledgment := fmt.Sprintf("I understand your request and will help you with %s. Let me process this for you.", strings.ToLower(intent))

	return acknowledgment, intent, nil
}

// generateFallbackIntent generates intent based on keywords and agent type
func generateFallbackIntent(query, agentName string) string {
	queryLower := strings.ToLower(query)

	// Agent-specific intents
	switch agentName {
	case "k8s_debug_react", "kubectl":
		if strings.Contains(queryLower, "crash") || strings.Contains(queryLower, "fail") {
			return "Kubernetes troubleshooting and issue diagnosis"
		}
		return "Kubernetes resource management and investigation"

	case "prometheus":
		if strings.Contains(queryLower, "metric") || strings.Contains(queryLower, "cpu") || strings.Contains(queryLower, "memory") {
			return "Performance metrics analysis"
		}
		return "Monitoring data retrieval"

	case "logs", "loki":
		if strings.Contains(queryLower, "error") || strings.Contains(queryLower, "exception") {
			return "Error log analysis"
		}
		return "Log data investigation"

	case "github":
		return "Source code and repository analysis"

	case "security":
		return "Security assessment and vulnerability analysis"

	default:
		// General keyword-based intent detection
		if strings.Contains(queryLower, "debug") || strings.Contains(queryLower, "troubleshoot") {
			return "System troubleshooting and debugging"
		} else if strings.Contains(queryLower, "show") || strings.Contains(queryLower, "get") || strings.Contains(queryLower, "list") {
			return "Information retrieval and display"
		} else if strings.Contains(queryLower, "why") || strings.Contains(queryLower, "how") {
			return "Analysis and explanation"
		} else {
			return "Task processing and assistance"
		}
	}
}

// estimateProcessingTime estimates how long the request might take based on agent and query complexity
func estimateProcessingTime(agentName, query string) string {
	queryLower := strings.ToLower(query)

	// Complex operations that typically take longer
	if strings.Contains(queryLower, "analyze") ||
		strings.Contains(queryLower, "troubleshoot") ||
		strings.Contains(queryLower, "debug") ||
		strings.Contains(queryLower, "investigate") {
		return "30-60 seconds"
	}

	// Agent-specific time estimates
	switch agentName {
	case "k8s_debug_react":
		return "20-45 seconds"
	case "prometheus", "logs", "loki":
		return "15-30 seconds"
	case "github", "security":
		return "20-40 seconds"
	default:
		return "15-30 seconds"
	}
}

var commonAcks = []string{
	"Got it! Let me look into that for you.",
	"I'm on it. Fetching the information now.",
	"Sure thing! I'll process your request right away.",
	"I've received your request and I'm starting the investigation.",
	"I'll help you with that. Just a moment while I gather the data.",
}

// CreateAcknowledgmentResponse creates a complete acknowledgment response
func CreateAcknowledgmentResponse(ctx *security.RequestContext, userId, accountId, query, agentName, conversationId, messageId string) AcknowledgmentResponse {
	var acknowledgment, intent string
	var err error

	// Optimization: For short queries, use a random common acknowledgment and skip LLM
	wordCount := common.GetWordCount(query)
	if wordCount > 0 && wordCount <= common.ShortQueryWordCountThreshold {
		intent = generateFallbackIntent(query, agentName)
		// Pick a random common acknowledgment
		acknowledgment = commonAcks[rand.Intn(len(commonAcks))]

		ctx.GetLogger().Info("acknowledgment: using static random acknowledgment (short query optimization)", "conversation_id", conversationId)
		return AcknowledgmentResponse{
			Status:            "acknowledged",
			Acknowledgment:    acknowledgment,
			ConversationId:    conversationId,
			MessageId:         messageId,
			EstimatedTime:     estimateProcessingTime(agentName, query),
			InterpretedIntent: intent,
		}
	}

	acknowledgment, intent, err = generateUserAcknowledgment(ctx, userId, accountId, conversationId, messageId, query, agentName)
	if err != nil {
		ctx.GetLogger().Error("acknowledgment: failed to generate acknowledgment", "error", err)
		acknowledgment = "I've received your request and will process it for you."
		intent = "Task processing"
	}

	return AcknowledgmentResponse{
		Status:            "acknowledged",
		Acknowledgment:    acknowledgment,
		ConversationId:    conversationId,
		MessageId:         messageId,
		EstimatedTime:     estimateProcessingTime(agentName, query),
		InterpretedIntent: intent,
	}
}
