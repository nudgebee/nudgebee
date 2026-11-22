package bedrockclient

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/config"
)

func TestParseClaudeGeneration(t *testing.T) {
	tests := []struct {
		model string
		major int
		minor int
		ok    bool
	}{
		{"claude-sonnet-4-6", 4, 6, true},
		{"claude-sonnet-4-6-20260501", 4, 6, true},
		{"anthropic.claude-sonnet-4-6-v1:0", 4, 6, true},
		{"us.anthropic.claude-sonnet-4-6-v1:0", 4, 6, true},
		{"claude-3-7-sonnet", 3, 7, true},
		{"claude-3-7-sonnet-20250219", 3, 7, true},
		{"anthropic.claude-3-7-sonnet-20250219-v1:0", 3, 7, true},
		{"claude-3-5-sonnet", 3, 5, true},
		{"claude-3-100-sonnet", 3, 100, true},
		{"claude-sonnet-4-100", 4, 100, true},
		{"claude-3-haiku", 3, 0, true},
		{"claude-opus-5", 5, 0, true},
		{"gpt-4o", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			major, minor, ok := parseClaudeGeneration(tt.model)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.major, major)
			assert.Equal(t, tt.minor, minor)
		})
	}
}

func TestResolveAnthropicThinking(t *testing.T) {
	orig := config.Config.LlmAnthropicThinkingEnabled
	defer func() { config.Config.LlmAnthropicThinkingEnabled = orig }()

	// 1. When flag is false, thinking is nil
	config.Config.LlmAnthropicThinkingEnabled = false
	opts46 := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "high",
		},
	}
	thDisabled, outDisabled := resolveAnthropicThinking("claude-sonnet-4-6", opts46, 4096)
	assert.Nil(t, thDisabled)
	assert.Nil(t, outDisabled)

	// 2. When flag is true, thinking resolves for Claude 4.6
	config.Config.LlmAnthropicThinkingEnabled = true
	th46, out46 := resolveAnthropicThinking("claude-sonnet-4-6", opts46, 4096)
	require.NotNil(t, th46)
	require.NotNil(t, out46)
	assert.Equal(t, "adaptive", th46.Type)
	assert.Equal(t, "high", out46.Effort)

	// 3. Claude 3.7 with ThinkingLevel medium -> budget 4096
	opts37 := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "medium",
		},
	}
	th37, out37 := resolveAnthropicThinking("claude-3-7-sonnet", opts37, 8192)
	require.NotNil(t, th37)
	assert.Nil(t, out37)
	assert.Equal(t, "enabled", th37.Type)
	assert.Equal(t, 4096, th37.BudgetTokens)

	// 4. Claude 3.5 -> nil thinking
	opts35 := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "medium",
		},
	}
	th35, out35 := resolveAnthropicThinking("claude-3-5-sonnet", opts35, 4096)
	assert.Nil(t, th35)
	assert.Nil(t, out35)

	// 5. Claude 4.6 with missing/empty ThinkingLevel -> nil thinking (opt-in)
	optsMissing := llms.CallOptions{
		Metadata: map[string]any{
			"SomeKey": "value",
		},
	}
	thMissing, outMissing := resolveAnthropicThinking("claude-sonnet-4-6", optsMissing, 4096)
	assert.Nil(t, thMissing)
	assert.Nil(t, outMissing)

	optsEmpty := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "",
		},
	}
	thEmpty, outEmpty := resolveAnthropicThinking("claude-sonnet-4-6", optsEmpty, 4096)
	assert.Nil(t, thEmpty)
	assert.Nil(t, outEmpty)

	// 6. Opus with ThinkingLevel none -> nil thinking
	optsNone := llms.CallOptions{
		Metadata: map[string]any{
			"ThinkingLevel": "none",
		},
	}
	thOpus, outOpus := resolveAnthropicThinking("claude-opus-5", optsNone, 4096)
	assert.Nil(t, thOpus)
	assert.Nil(t, outOpus)

	// 7. Non-opus with ThinkingLevel none -> disabled thinking
	thSonnetNone, outSonnetNone := resolveAnthropicThinking("claude-sonnet-4-6", optsNone, 4096)
	require.NotNil(t, thSonnetNone)
	assert.Equal(t, "disabled", thSonnetNone.Type)
	assert.Nil(t, outSonnetNone)
}

func TestAnthropicInput_ThinkingJSONSerialization(t *testing.T) {
	input := anthropicTextGenerationInput{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        4096,
		Thinking: &anthropicThinking{
			Type: "adaptive",
		},
		OutputConfig: &anthropicOutputConfig{
			Effort: "medium",
		},
	}

	b, err := json.Marshal(input)
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(b, &payload)
	require.NoError(t, err)

	assert.Contains(t, payload, "thinking")
	assert.Contains(t, payload, "output_config")

	thMap := payload["thinking"].(map[string]any)
	assert.Equal(t, "adaptive", thMap["type"])

	outMap := payload["output_config"].(map[string]any)
	assert.Equal(t, "medium", outMap["effort"])
}

func TestBedrockAnthropicOutput_ToolCallsParsing(t *testing.T) {
	respJSON := `{
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Executing query"},
			{"type": "tool_use", "id": "toolu_bedrock_123", "name": "sql_query", "input": {"query": "SELECT 1"}}
		],
		"stop_reason": "tool_use",
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"thinking_tokens": 20
		}
	}`

	var output anthropicTextGenerationOutput
	err := json.Unmarshal([]byte(respJSON), &output)
	require.NoError(t, err)

	assert.Equal(t, "tool_use", output.StopReason)
	assert.Equal(t, 20, output.Usage.ThinkingTokens)
	require.Len(t, output.Content, 2)
	assert.Equal(t, "tool_use", output.Content[1].Type)
	assert.Equal(t, "toolu_bedrock_123", output.Content[1].ID)
	assert.Equal(t, "sql_query", output.Content[1].Name)
}

func TestProcessInputMessagesAnthropic_ChatMessageTypeTool(t *testing.T) {
	messages := []Message{
		{
			Role:    llms.ChatMessageTypeHuman,
			Content: "Run tool",
			Type:    "text",
		},
		{
			Role:    llms.ChatMessageTypeAI,
			Content: "Executing tool",
			Type:    "text",
		},
		{
			Role:    llms.ChatMessageTypeTool,
			Content: `{"content":"result"}`,
			Type:    "tool_call_response",
		},
	}

	inputMsgs, sys, err := processInputMessagesAnthropic(messages, "")
	require.NoError(t, err)
	assert.Empty(t, sys)
	require.Len(t, inputMsgs, 3)
	assert.Equal(t, AnthropicRoleUser, inputMsgs[0].Role)
	assert.Equal(t, AnthropicRoleAssistant, inputMsgs[1].Role)
	assert.Equal(t, AnthropicRoleUser, inputMsgs[2].Role)
}

func TestProcessInputMessagesAnthropic_ImageBase64Encoding(t *testing.T) {
	rawBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	messages := []Message{
		{
			Role:     llms.ChatMessageTypeHuman,
			Content:  string(rawBytes),
			MimeType: "image/png",
			Type:     AnthropicMessageTypeImage,
		},
	}

	inputMsgs, _, err := processInputMessagesAnthropic(messages, "")
	require.NoError(t, err)
	require.Len(t, inputMsgs, 1)
	require.Len(t, inputMsgs[0].Content, 1)
	assert.Equal(t, AnthropicMessageTypeImage, inputMsgs[0].Content[0].Type)
	require.NotNil(t, inputMsgs[0].Content[0].Source)
	assert.Equal(t, "base64", inputMsgs[0].Content[0].Source.Type)
	assert.Equal(t, "image/png", inputMsgs[0].Content[0].Source.MediaType)
	assert.Equal(t, "iVBORw0KGgo=", inputMsgs[0].Content[0].Source.Data)
}

func TestProcessInputMessagesAnthropic_MultipleSystemPrompts(t *testing.T) {
	messages := []Message{
		{
			Role:    llms.ChatMessageTypeSystem,
			Content: "You are a helpful assistant.",
			Type:    AnthropicMessageTypeText,
		},
		{
			Role:    llms.ChatMessageTypeSystem,
			Content: "Always output JSON.",
			Type:    AnthropicMessageTypeText,
		},
		{
			Role:    llms.ChatMessageTypeHuman,
			Content: "Hello",
			Type:    AnthropicMessageTypeText,
		},
	}

	inputMsgs, sys, err := processInputMessagesAnthropic(messages, "")
	require.NoError(t, err)
	assert.Equal(t, "You are a helpful assistant.\n\nAlways output JSON.", sys)
	require.Len(t, inputMsgs, 1)
}

func TestProcessInputMessagesAnthropic_CacheControl(t *testing.T) {
	hour := &llms.CacheControl{Type: "ephemeral", Duration: time.Hour}
	fiveMin := &llms.CacheControl{Type: "ephemeral"}
	messages := []Message{
		{Role: llms.ChatMessageTypeSystem, Content: "You are a helpful assistant.", Type: AnthropicMessageTypeText, CacheControl: hour},
		{Role: llms.ChatMessageTypeHuman, Content: "Run tool", Type: AnthropicMessageTypeText},
		{Role: llms.ChatMessageTypeAI, Content: "Executing tool", Type: AnthropicMessageTypeText},
		{Role: llms.ChatMessageTypeTool, Content: `{"content":"result"}`, Type: "tool_call_response", CacheControl: fiveMin},
	}

	tests := []struct {
		name      string
		modelID   string
		cached    bool
		systemTTL string
	}{
		{"1h TTL on Claude 4.5+", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", true, "1h"},
		{"1h TTL on Claude 5", "global.anthropic.claude-opus-5", true, "1h"},
		{"5m only on Claude 3.7", "us.anthropic.claude-3-7-sonnet-20250219-v1:0", true, ""},
		{"no caching before 3.7", "anthropic.claude-3-5-haiku-20241022-v1:0", false, ""},
		{"no caching on unnamed model", "arn:aws:bedrock:us-east-1:000000000000:application-inference-profile/abc", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputMsgs, sys, err := processInputMessagesAnthropic(messages, tt.modelID)
			require.NoError(t, err)
			require.Len(t, inputMsgs, 3)
			toolBlock := inputMsgs[2].Content[0]
			if !tt.cached {
				assert.Equal(t, "You are a helpful assistant.", sys)
				assert.Nil(t, toolBlock.CacheControl)
				return
			}
			blocks, ok := sys.([]anthropicTextGenerationInputContent)
			require.True(t, ok, "a marked system prompt is sent as blocks")
			require.Len(t, blocks, 1)
			require.NotNil(t, blocks[0].CacheControl)
			assert.Equal(t, tt.systemTTL, blocks[0].CacheControl.TTL)
			require.NotNil(t, toolBlock.CacheControl)
			assert.Equal(t, "ephemeral", toolBlock.CacheControl.Type)
			assert.Empty(t, toolBlock.CacheControl.TTL)
			assert.Nil(t, inputMsgs[0].Content[0].CacheControl, "unmarked blocks stay unmarked")
		})
	}
}

func TestAnthropicInput_CacheControlJSON(t *testing.T) {
	messages := []Message{
		{Role: llms.ChatMessageTypeSystem, Content: "sys", Type: AnthropicMessageTypeText, CacheControl: &llms.CacheControl{Duration: time.Hour}},
		{Role: llms.ChatMessageTypeHuman, Content: "hi", Type: AnthropicMessageTypeText, CacheControl: &llms.CacheControl{}},
	}
	inputMsgs, sys, err := processInputMessagesAnthropic(messages, "anthropic.claude-haiku-4-5-20251001-v1:0")
	require.NoError(t, err)
	body, err := json.Marshal(anthropicTextGenerationInput{System: sys, Messages: inputMsgs})
	require.NoError(t, err)
	assert.Contains(t, string(body), `"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}]`)
	assert.Contains(t, string(body), `{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}`)

	// No system text: the field is omitted rather than sent empty.
	inputMsgs, sys, err = processInputMessagesAnthropic(messages[1:], "")
	require.NoError(t, err)
	body, err = json.Marshal(anthropicTextGenerationInput{System: sys, Messages: inputMsgs})
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"system"`)
}

func TestBedrockAnthropicOutput_CacheCreation1h(t *testing.T) {
	body := `{"type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],
		"usage":{"input_tokens":5,"output_tokens":1,"cache_creation_input_tokens":5000,"cache_read_input_tokens":0,
		"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":5000}}}`
	var output anthropicTextGenerationOutput
	require.NoError(t, json.Unmarshal([]byte(body), &output))
	require.NotNil(t, output.Usage.CacheCreation)
	assert.Equal(t, 5000, output.Usage.CacheCreation.Ephemeral1hInputTokens)
}

// Bedrock InvokeModel returns the Anthropic Messages body, which reports
// thinking under usage.output_tokens_details as a share of output_tokens.
func TestBedrockAnthropicOutput_ThinkingFromOutputTokensDetails(t *testing.T) {
	body := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":427,"output_tokens":161,"output_tokens_details":{"thinking_tokens":156}}}`
	var out anthropicTextGenerationOutput
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	output, thinking := splitAnthropicThinking(out.Usage.OutputTokens, out.Usage.ThinkingTokens, out.Usage.OutputTokensDetails)
	assert.Equal(t, 5, output)
	assert.Equal(t, 156, thinking)
}

// Streaming: only the final message_delta carries the breakdown.
func TestBedrockAnthropicStreaming_ThinkingFromFinalDelta(t *testing.T) {
	chunk := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},
		"usage":{"output_tokens":251,"output_tokens_details":{"thinking_tokens":40}}}`
	var resp streamingCompletionResponseChunk
	require.NoError(t, json.Unmarshal([]byte(chunk), &resp))
	output, thinking := splitAnthropicThinking(resp.Usage.OutputTokens, resp.Usage.ThinkingTokens, resp.Usage.OutputTokensDetails)
	assert.Equal(t, 211, output)
	assert.Equal(t, 40, thinking)
}

func TestSplitAnthropicThinking(t *testing.T) {
	tests := []struct {
		name                     string
		output, thinking         int
		details                  *anthropicOutputTokensDetails
		wantOutput, wantThinking int
	}{
		{"no details", 100, 0, nil, 100, 0},
		{"thinking included in output", 100, 0, &anthropicOutputTokensDetails{ThinkingTokens: 60}, 40, 60},
		{"all thinking", 60, 0, &anthropicOutputTokensDetails{ThinkingTokens: 60}, 0, 60},
		{"thinking above output is untrusted", 10, 0, &anthropicOutputTokensDetails{ThinkingTokens: 60}, 10, 0},
		{"zero thinking detail", 100, 0, &anthropicOutputTokensDetails{}, 100, 0},
		{"top-level thinking kept as separate", 100, 30, &anthropicOutputTokensDetails{ThinkingTokens: 60}, 100, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, thinking := splitAnthropicThinking(tt.output, tt.thinking, tt.details)
			assert.Equal(t, tt.wantOutput, output)
			assert.Equal(t, tt.wantThinking, thinking)
		})
	}
}
