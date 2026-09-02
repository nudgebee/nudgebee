package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsUserConversationSource(t *testing.T) {
	// Live user chats — someone is watching for the ack.
	assert.True(t, isUserConversationSource(ConversationSourceUserInvestigation))
	assert.True(t, isUserConversationSource(ConversationSourceUserInvestigationCLI))

	// System/automated — no user watching a chat for the acknowledgment.
	assert.False(t, isUserConversationSource(ConversationSourceInvestigation))
	assert.False(t, isUserConversationSource(ConversationSourceAutomation))
	assert.False(t, isUserConversationSource(ConversationSourceInstantNotification))
	assert.False(t, isUserConversationSource(ConversationSourceWorkflowBuilder))
	assert.False(t, isUserConversationSource(ConversationSourceOptimize))
	assert.False(t, isUserConversationSource(ConversationSourcePrometheusQuery))
	assert.False(t, isUserConversationSource(""))
}

func TestUseStaticAcknowledgment(t *testing.T) {
	longUserQuestion := "Why is the checkout-service returning intermittent 5xx errors in the production namespace during peak traffic hours today"

	// Only a substantial question on a user conversation warrants the LLM ack.
	assert.False(t, useStaticAcknowledgment(longUserQuestion, ConversationSourceUserInvestigation),
		"substantial user question → dynamic (LLM) ack")
	assert.False(t, useStaticAcknowledgment(longUserQuestion, ConversationSourceUserInvestigationCLI))

	// Empty / whitespace-only queries stay static — never an LLM call on nothing.
	assert.True(t, useStaticAcknowledgment("", ConversationSourceUserInvestigation))
	assert.True(t, useStaticAcknowledgment("   ", ConversationSourceUserInvestigation))

	// Small talk / short user queries stay static even on a user conversation.
	assert.True(t, useStaticAcknowledgment("hi", ConversationSourceUserInvestigation))
	assert.True(t, useStaticAcknowledgment("how are you doing today", ConversationSourceUserInvestigation))
	assert.True(t, useStaticAcknowledgment("why is my pod crashing", ConversationSourceUserInvestigation))

	// Non-user conversations are always static — even a huge system prompt query
	// (the event-RCA case that was ballooning the ack LLM call to ~29k tokens).
	assert.True(t, useStaticAcknowledgment(longUserQuestion, ConversationSourceInvestigation),
		"event RCA → static, no LLM, regardless of query size")
	assert.True(t, useStaticAcknowledgment("investigate "+strings.Repeat("evidence ", 500), ConversationSourceInvestigation))
	assert.True(t, useStaticAcknowledgment(longUserQuestion, ConversationSourceAutomation))
	assert.True(t, useStaticAcknowledgment(longUserQuestion, ConversationSourceInstantNotification))
}
