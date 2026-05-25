package core

import (
	"context"
	"log/slog"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestSummarizeContent_LargeInput(t *testing.T) {
	// This is an integration test and requires a configured LLM provider.
	// Skip if not running integration tests.
	if testing.Short() {
		t.Skip("skipping integration test in short mode.")
	}

	// 1. Setup: Configure a low token limit
	config.Config.SetString("llm_max_tokens_per_message", "100")
	defer config.Config.SetString("llm_max_tokens_per_message", "110000")

	// 2. Create a large text input that will exceed the token limit
	sentence := "This is a test sentence that we will repeat to create a very long document for summarization. "
	largeContent := strings.Repeat(sentence, 40000)

	// 3. Get LLM and context. You may need to adjust this based on your project's test setup.
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	agentName := "llm"
	conversationId := uuid.New().String()
	messageId := uuid.New().String()

	llm, err := GetLlmModel(nil, agentName, accountId, conversationId)
	if err != nil {
		t.Fatalf("Failed to get LLM model for integration test: %v. Ensure your environment is configured to run LLM calls.", err)
	}

	reqCtx := security.NewRequestContext(context.Background(), security.NewSecurityContextForTenantAdmin(os.Getenv("TEST_TENANT")), slog.Default(), nil, nil)

	summary := SummarizeContent(reqCtx, llm, largeContent, accountId, agentName, conversationId, messageId, userId)

	// 5. Assertions
	assert.NotEmpty(t, summary, "Summary should not be empty")
	assert.NotEqual(t, summary, largeContent, "Summary should not be the same as the original content, which indicates summarization failed and returned the input.")
	assert.Less(t, len(summary), len(largeContent), "Summary should be shorter than the original content.")

	t.Logf("Successfully summarized large content. Original length: %d, Summary length: %d", len(largeContent), len(summary))
	t.Logf("Summary: %s", summary)
}
