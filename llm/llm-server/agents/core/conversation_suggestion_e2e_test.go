//go:build e2e

package core

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test basic suggestion request with real conversation data
func TestHandleConversationSuggestionRequest(t *testing.T) {
	// Skip if database is not available
	if GetConversationDao() == nil {
		t.Skip("Skipping test: database not available")
	}

	RequireEnv(t, "TEST_TENANT", "TEST_ACCOUNT", "TEST_USER")

	ctx := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"),
		os.Getenv("TEST_USER"),
		[]string{os.Getenv("TEST_ACCOUNT")},
	)

	// Use the conversation from the issue: octocat-conversation-t88DON8j
	conversationId := "38a02773-7b17-441f-9105-68950a4846d7"
	messageId := "aafd82a5-60b7-4da4-9556-50d3fc4b80c7"

	request := ConversationSuggestionRequest{
		ConversationId: conversationId,
		AccountId:      os.Getenv("TEST_ACCOUNT"),
		UserId:         os.Getenv("TEST_USER"),
		MessageId:      messageId,
	}

	response, err := HandleConversationSuggestionRequest(ctx, request)

	// Basic assertions
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, conversationId, response.ConversationId)
	assert.Equal(t, messageId, response.MessageId)

	t.Logf("Generated %d suggestions", len(response.Suggestions))
	for i, suggestion := range response.Suggestions {
		t.Logf("  Suggestion %d: %s", i+1, suggestion.Message)
	}
}
