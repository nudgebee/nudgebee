package sagemaker

import (
	"context"
	"log"
	"nudgebee/llm/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestSagemakerLLM_GenerateContent(t *testing.T) {
	modelKwargs := map[string]any{}

	llm, err := New(config.Config.LlmProviderApiEndpoint, config.Config.LlmProviderRegion, modelKwargs)
	if err != nil {
		t.Fatalf("Failed to create SagemakerLLM: %v", err)
	}

	// Test with multiple messages
	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "You are expert in prometheus query language"},
			},
		},
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: "What is max memory for app-dev in nudgebee namespace ?"},
			},
		},
	}
	contentResponse, err := llm.GenerateContent(context.Background(), messages)
	if err != nil {
		t.Fatalf("Failed to generate content: %v", err)
	}

	assert.NotNil(t, contentResponse)
	assert.NotEmpty(t, contentResponse.Choices)
	assert.NotEmpty(t, contentResponse.Choices[0].Content)
	log.Println("GenerateContent Response:", contentResponse.Choices[0].Content)
	assert.True(t, len(contentResponse.Choices[0].Content) > 5)
}
