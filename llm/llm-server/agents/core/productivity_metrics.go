package core

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/security"
	"strings"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

const (
	ClassificationInvestigation = "investigation"
	ClassificationOthers        = "others"
)

// ComputeAndSaveMessageProductivityMetrics computes classification and successful tasks count for a specific message
func ComputeAndSaveMessageProductivityMetrics(ctx *security.RequestContext, userId, accountId string, conversationId, messageId uuid.UUID) error {
	dao := GetConversationDao()
	if dao == nil {
		return fmt.Errorf("productivity: conversation dao not initialized")
	}

	// 1. Get Message
	msg, err := dao.GetConversationMessage(messageId.String(), accountId, conversationId.String())
	if err != nil {
		return fmt.Errorf("productivity: failed to fetch message: %w", err)
	}

	// 2. Get Agents/Tools for this message
	agents, err := dao.ListConversationAgents(messageId.String(), "")
	if err != nil {
		slog.Error("productivity: failed to fetch agents for message", "message_id", messageId, "error", err)
		agents = []ConversationAgent{}
	}

	// 3. Classify Message
	classification, err := ClassifyMessage(ctx, userId, accountId, conversationId.String(), messageId.String(), msg, agents)
	if err != nil {
		slog.Error("productivity: classification failed", "message_id", messageId, "error", err)
		classification = ClassificationOthers // Fallback
	}

	// 4. Count Successful Tasks for this message
	successfulTasks, err := dao.GetSuccessfulToolCallsCountByMessage(messageId.String())
	if err != nil {
		slog.Error("productivity: failed to count successful tasks for message", "message_id", messageId, "error", err)
		successfulTasks = 0
	}

	// 5. Save to DB (Update llm_conversation_messages table)
	err = dao.UpdateMessageProductivityMetrics(messageId.String(), classification, successfulTasks)
	if err != nil {
		return fmt.Errorf("productivity: failed to save message metrics: %w", err)
	}

	slog.Info("productivity: message metrics updated",
		"message_id", messageId,
		"classification", classification,
		"successful_tasks", successfulTasks)

	return nil
}

// ClassifyMessage uses LLM to classify a specific message based on its content and executed tools
func ClassifyMessage(ctx *security.RequestContext, userId, accountId, conversationId, messageId string, msg ConversationMessage, agents []ConversationAgent) (string, error) {
	// Gather unique tools executed for this message
	toolMap := make(map[string]bool)
	for _, agent := range agents {
		if agent.AgentName != "" {
			toolMap[agent.AgentName] = true
		}
	}
	var tools []string
	for tool := range toolMap {
		tools = append(tools, tool)
	}

	input := fmt.Sprintf("User Query: %s\n\nTools Executed: %s",
		msg.Message,
		strings.Join(tools, ", "))

	systemPrompt := `Classify this user message into one of the following categories:
- investigation: If the message is about troubleshooting, debugging, searching logs, analyzing metrics, checking traces, service status, slowness, or errors.
- others: If it is a general query, greeting, help request, or any other topic not directly related to active investigation/troubleshooting.

Respond ONLY with the category name.`

	options := []string{ClassificationInvestigation, ClassificationOthers}

	mcList := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: input}},
		},
	}

	// Generate content for internal classification call and track it for token counting
	result, err := GenerateAndTrackLLMContent(ctx, userId, accountId, conversationId, messageId, ClassificationInvestigation, true, mcList, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	chosen := strings.ToLower(strings.TrimSpace(result.Choices[0].Content))
	for _, opt := range options {
		if strings.Contains(chosen, opt) {
			return opt, nil
		}
	}

	return ClassificationOthers, nil
}
