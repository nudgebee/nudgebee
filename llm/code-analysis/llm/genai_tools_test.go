package llm

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"google.golang.org/genai"
)

// skipIfNoAPIKey skips the test if no Google API key is set.
func skipIfNoAPIKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("GOOGLE_API_KEY")
	if key == "" {
		key = os.Getenv("LLM_PROVIDER_API_KEY")
	}
	if key == "" {
		t.Skip("Set GOOGLE_API_KEY or LLM_PROVIDER_API_KEY to run this test")
	}
	return key
}

func newTestClient(t *testing.T, apiKey, model string) *Client {
	t.Helper()
	cfg := &config.Config{}
	cfg.LLM.Provider = "googleai"
	cfg.LLM.ApiKey = apiKey
	cfg.LLM.Model = model

	client, err := NewClient(cfg)
	require.NoError(t, err)
	return client
}

// TestGenAI_SimpleTextGeneration verifies basic text generation works with the new SDK.
func TestGenAI_SimpleTextGeneration(t *testing.T) {
	apiKey := skipIfNoAPIKey(t)

	for _, model := range []string{"gemini-2.5-flash", "gemini-3-flash-preview"} {
		t.Run(model, func(t *testing.T) {
			client := newTestClient(t, apiKey, model)
			defer func() { _ = client.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			messages := []llms.MessageContent{
				{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
					llms.TextContent{Text: "What is 2+2? Reply with just the number."},
				}},
			}

			resp, err := client.GenerateContentWithTools(ctx, messages, nil, NewGenAISession())
			require.NoError(t, err)
			require.NotEmpty(t, resp.Choices)
			assert.Contains(t, resp.Choices[0].Content, "4")
		})
	}
}

// TestGenAI_FunctionCalling verifies single-turn function calling works.
func TestGenAI_FunctionCalling(t *testing.T) {
	apiKey := skipIfNoAPIKey(t)

	for _, model := range []string{"gemini-2.5-flash", "gemini-3-flash-preview"} {
		t.Run(model, func(t *testing.T) {
			client := newTestClient(t, apiKey, model)
			defer func() { _ = client.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tools := []ToolDefinition{{
				Name:        "read_file",
				Description: "Read the contents of a file",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "The file path to read",
						},
					},
					"required": []any{"path"},
				},
			}}

			messages := []llms.MessageContent{
				{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
					llms.TextContent{Text: "You are a code analysis assistant. Use the read_file tool to read files."},
				}},
				{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
					llms.TextContent{Text: "Read the file main.go"},
				}},
			}

			resp, err := client.GenerateContentWithTools(ctx, messages, tools, NewGenAISession())
			require.NoError(t, err)
			require.NotEmpty(t, resp.Choices)
			require.NotEmpty(t, resp.Choices[0].ToolCalls, "expected a function call")
			assert.Equal(t, "read_file", resp.Choices[0].ToolCalls[0].FunctionCall.Name)
		})
	}
}

// TestGenAI_MultiTurnFunctionCalling verifies multi-turn function calling works.
// This is the scenario that was broken with Gemini 3 + old SDK due to thoughtSignature.
func TestGenAI_MultiTurnFunctionCalling(t *testing.T) {
	apiKey := skipIfNoAPIKey(t)

	for _, model := range []string{"gemini-2.5-flash", "gemini-3-flash-preview"} {
		t.Run(model, func(t *testing.T) {
			client := newTestClient(t, apiKey, model)
			defer func() { _ = client.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			tools := []ToolDefinition{{
				Name:        "read_file",
				Description: "Read the contents of a file",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "The file path to read",
						},
					},
					"required": []any{"path"},
				},
			}}

			// Step 1: User asks to read a file → model should call read_file
			messages := []llms.MessageContent{
				{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
					llms.TextContent{Text: "You are a code analysis assistant. Use the read_file tool to read files when asked."},
				}},
				{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
					llms.TextContent{Text: "Read the file main.go and tell me what it does."},
				}},
			}

			session := NewGenAISession()
			resp1, err := client.GenerateContentWithTools(ctx, messages, tools, session)
			require.NoError(t, err, "Step 1 failed")
			require.NotEmpty(t, resp1.Choices)
			require.NotEmpty(t, resp1.Choices[0].ToolCalls, "Step 1: expected a function call")

			fc := resp1.Choices[0].ToolCalls[0]
			assert.Equal(t, "read_file", fc.FunctionCall.Name)

			// Step 2: Send the function response back → model should generate text summary
			// This is the step that previously failed with 400 on Gemini 3 due to
			// thoughtSignature being deserialized as empty Text("") by the old SDK.
			messages = append(messages,
				llms.MessageContent{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
					llms.ToolCall{FunctionCall: fc.FunctionCall},
				}},
				llms.MessageContent{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
					llms.ToolCallResponse{
						Name:    fc.FunctionCall.Name,
						Content: `package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("Hello, World!")\n}`,
					},
				}},
			)

			resp2, err := client.GenerateContentWithTools(ctx, messages, tools, session)
			require.NoError(t, err, "Step 2 failed — this was the Gemini 3 thoughtSignature bug")
			require.NotEmpty(t, resp2.Choices)
			// Model should respond with a text description of the code
			assert.NotEmpty(t, resp2.Choices[0].Content, "Step 2: expected text response describing the code")

			t.Logf("Step 2 response: %.200s", resp2.Choices[0].Content)
		})
	}
}

// TestIsTransientError verifies transient error detection covers all expected patterns.
func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"non-transient error", errors.New("invalid argument"), false},
		{"429 rate limit", errors.New("Error 429: too many requests"), true},
		{"rate limit text", errors.New("rate limit exceeded"), true},
		{"quota exceeded", errors.New("quota exceeded for model"), true},
		{"too many requests", errors.New("too many requests"), true},
		{"504 deadline exceeded", errors.New("Error 504, Message: Deadline expired, Status: DEADLINE_EXCEEDED"), true},
		{"503 service unavailable", errors.New("Error 503: service unavailable"), true},
		{"500 internal server error", errors.New("Error 500: internal server error"), true},
		{"DEADLINE_EXCEEDED", errors.New("Status: DEADLINE_EXCEEDED"), true},
		{"deadline exceeded lowercase", errors.New("deadline exceeded"), true},
		{"service unavailable text", errors.New("service unavailable"), true},
		{"internal server error text", errors.New("internal server error"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"auth error", errors.New("Error 401: unauthorized"), false},
		{"bad request", errors.New("Error 400: bad request"), false},
		{"permission denied", errors.New("permission denied"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isTransientError(tt.err))
		})
	}
}

// TestConvertMapToGenAISchema verifies schema conversion handles nested structures correctly.
func TestConvertMapToGenAISchema(t *testing.T) {
	schema := convertMapToGenAISchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name",
			},
			"age": map[string]any{
				"type": "integer",
			},
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
			"address": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{
						"type": "string",
					},
				},
				"required": []any{"city"},
			},
		},
		"required": []any{"name"},
	})

	require.NotNil(t, schema)
	assert.Equal(t, "OBJECT", string(schema.Type))
	assert.Equal(t, []string{"name"}, schema.Required)

	require.Contains(t, schema.Properties, "name")
	assert.Equal(t, "STRING", string(schema.Properties["name"].Type))
	assert.Equal(t, "The name", schema.Properties["name"].Description)

	require.Contains(t, schema.Properties, "age")
	assert.Equal(t, "INTEGER", string(schema.Properties["age"].Type))

	require.Contains(t, schema.Properties, "tags")
	assert.Equal(t, "ARRAY", string(schema.Properties["tags"].Type))
	require.NotNil(t, schema.Properties["tags"].Items)
	assert.Equal(t, "STRING", string(schema.Properties["tags"].Items.Type))

	require.Contains(t, schema.Properties, "address")
	assert.Equal(t, "OBJECT", string(schema.Properties["address"].Type))
	require.Contains(t, schema.Properties["address"].Properties, "city")
	assert.Equal(t, []string{"city"}, schema.Properties["address"].Required)
}

// TestConvertMessagesToGenAI verifies message conversion handles all role types.
func TestConvertMessagesToGenAI(t *testing.T) {
	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
			llms.TextContent{Text: "You are a helper."},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "Hello"},
		}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.TextContent{Text: "Hi there!"},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{Name: "read_file", Content: "file contents"},
		}},
	}

	sysInstruction, history := convertMessagesToGenAI(messages)

	require.NotNil(t, sysInstruction)
	assert.Equal(t, "You are a helper.", sysInstruction.Parts[0].Text)

	require.Len(t, history, 3) // human, AI, tool (system is extracted)
	assert.Equal(t, "user", history[0].Role)
	assert.Equal(t, "model", history[1].Role)
	assert.Equal(t, "user", history[2].Role) // tool responses go as user role
}

// TestSanitizeFunctionCallOrdering verifies that function call ordering is
// corrected when intervening messages exist between a function call and its response.
func TestSanitizeFunctionCallOrdering(t *testing.T) {
	t.Run("already correct ordering", func(t *testing.T) {
		history := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("Hello")}},
			{Role: "model", Parts: []*genai.Part{genai.NewPartFromFunctionCall("read_file", map[string]any{"path": "main.go"})}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromFunctionResponse("read_file", map[string]any{"response": "contents"})}},
			{Role: "model", Parts: []*genai.Part{genai.NewPartFromText("Here is the file")}},
		}

		result := sanitizeFunctionCallOrdering(history)
		require.Len(t, result, 4)
		assert.Equal(t, "user", result[0].Role)
		assert.NotNil(t, result[1].Parts[0].FunctionCall)
		assert.NotNil(t, result[2].Parts[0].FunctionResponse)
		assert.Equal(t, "model", result[3].Role)
	})

	t.Run("separator between function call and response", func(t *testing.T) {
		history := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("Hello")}},
			{Role: "model", Parts: []*genai.Part{genai.NewPartFromFunctionCall("read_file", map[string]any{"path": "main.go"})}},
			// This separator message was inserted by compaction
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("[Earlier steps summarized]")}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromFunctionResponse("read_file", map[string]any{"response": "contents"})}},
		}

		result := sanitizeFunctionCallOrdering(history)
		require.Len(t, result, 4)
		// Separator should be moved BEFORE the function call
		assert.Equal(t, "user", result[0].Role)
		assert.Equal(t, "[Earlier steps summarized]", result[1].Parts[0].Text)
		assert.NotNil(t, result[2].Parts[0].FunctionCall, "function call should be at index 2")
		assert.NotNil(t, result[3].Parts[0].FunctionResponse, "function response should immediately follow")
	})

	t.Run("multiple user interlopers between call and response", func(t *testing.T) {
		history := []*genai.Content{
			{Role: "model", Parts: []*genai.Part{genai.NewPartFromFunctionCall("search", map[string]any{"q": "test"})}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("budget warning")}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("separator")}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromFunctionResponse("search", map[string]any{"response": "results"})}},
		}

		result := sanitizeFunctionCallOrdering(history)
		require.Len(t, result, 4)
		// User interlopers moved before the function call
		assert.Equal(t, "budget warning", result[0].Parts[0].Text)
		assert.Equal(t, "separator", result[1].Parts[0].Text)
		assert.NotNil(t, result[2].Parts[0].FunctionCall)
		assert.NotNil(t, result[3].Parts[0].FunctionResponse)
	})

	t.Run("no function calls", func(t *testing.T) {
		history := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("Hello")}},
			{Role: "model", Parts: []*genai.Part{genai.NewPartFromText("Hi")}},
		}

		result := sanitizeFunctionCallOrdering(history)
		require.Len(t, result, 2)
		assert.Equal(t, "Hello", result[0].Parts[0].Text)
		assert.Equal(t, "Hi", result[1].Parts[0].Text)
	})

	t.Run("multiple function call pairs with one broken", func(t *testing.T) {
		history := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("query")}},
			// Pair 1: correct
			{Role: "model", Parts: []*genai.Part{genai.NewPartFromFunctionCall("tool_a", map[string]any{})}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromFunctionResponse("tool_a", map[string]any{"response": "a"})}},
			// Pair 2: broken by separator
			{Role: "model", Parts: []*genai.Part{genai.NewPartFromFunctionCall("tool_b", map[string]any{})}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromText("separator")}},
			{Role: "user", Parts: []*genai.Part{genai.NewPartFromFunctionResponse("tool_b", map[string]any{"response": "b"})}},
		}

		result := sanitizeFunctionCallOrdering(history)
		require.Len(t, result, 6)
		// Pair 1 should be intact
		assert.NotNil(t, result[1].Parts[0].FunctionCall)
		assert.NotNil(t, result[2].Parts[0].FunctionResponse)
		// Separator moved before pair 2
		assert.Equal(t, "separator", result[3].Parts[0].Text)
		assert.NotNil(t, result[4].Parts[0].FunctionCall)
		assert.NotNil(t, result[5].Parts[0].FunctionResponse)
	})
}

// TestConvertMessagesToGenAI_FunctionCallOrdering verifies end-to-end that
// convertMessagesToGenAI produces correct function call ordering even when
// the input has intervening messages.
func TestConvertMessagesToGenAI_FunctionCallOrdering(t *testing.T) {
	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
			llms.TextContent{Text: "system prompt"},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "user query"},
		}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{
			llms.TextContent{Text: "thinking..."},
			llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "read_file", Arguments: `{"path":"main.go"}`}},
		}},
		// Intervening human message (e.g. from compaction separator)
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "[summary]"},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{Name: "read_file", Content: "file contents"},
		}},
	}

	sysInstruction, history := convertMessagesToGenAI(messages)
	require.NotNil(t, sysInstruction)

	// Find the function call and verify function response immediately follows
	for i, content := range history {
		if contentHasFunctionCall(content) {
			require.Less(t, i+1, len(history), "function call at end of history with no response")
			assert.True(t, contentHasFunctionResponse(history[i+1]),
				"function response must immediately follow function call, but got: %v", history[i+1])
			break
		}
	}
}

// TestSchemaTypeFromString verifies all type mappings.
func TestSchemaTypeFromString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"string", "STRING"},
		{"number", "NUMBER"},
		{"integer", "INTEGER"},
		{"boolean", "BOOLEAN"},
		{"array", "ARRAY"},
		{"object", "OBJECT"},
		{"unknown", "TYPE_UNSPECIFIED"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(schemaTypeFromString(tt.input)))
		})
	}
}

// TestToStringSlice verifies conversion of various types to string slices.
func TestToStringSlice(t *testing.T) {
	t.Run("[]string", func(t *testing.T) {
		result := toStringSlice([]string{"a", "b"})
		assert.Equal(t, []string{"a", "b"}, result)
	})

	t.Run("[]any", func(t *testing.T) {
		result := toStringSlice([]any{"a", "b", 123})
		assert.Equal(t, []string{"a", "b"}, result)
	})

	t.Run("nil", func(t *testing.T) {
		result := toStringSlice(nil)
		assert.Nil(t, result)
	})

	t.Run("unsupported", func(t *testing.T) {
		result := toStringSlice(42)
		assert.Nil(t, result)
	})
}

// TestSpliceModelResponses verifies splicing aligns recorded responses with
// FC-containing history contents even when text-only model messages exist.
func TestSpliceModelResponses(t *testing.T) {
	newFCContent := func(name string, sig string) *genai.Content {
		p := &genai.Part{FunctionCall: &genai.FunctionCall{Name: name, Args: map[string]any{}}}
		if sig != "" {
			p.ThoughtSignature = []byte(sig)
		}
		return &genai.Content{Role: "model", Parts: []*genai.Part{p}}
	}
	newTextContent := func(role, text string) *genai.Content {
		return &genai.Content{Role: role, Parts: []*genai.Part{genai.NewPartFromText(text)}}
	}
	// A reconstructed model message (from langchaingo) lacks ThoughtSignature.
	newReconstructedFC := func(name string) *genai.Content {
		return &genai.Content{Role: "model", Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: name, Args: map[string]any{}}},
		}}
	}

	t.Run("nil session returns history unchanged", func(t *testing.T) {
		var s *GenAISession
		history := []*genai.Content{newTextContent("user", "hi"), newReconstructedFC("file_view")}
		result := s.spliceModelResponses(history)
		require.Len(t, result, 2)
		assert.Nil(t, result[1].Parts[0].ThoughtSignature)
	})

	t.Run("empty recorded returns history unchanged", func(t *testing.T) {
		s := NewGenAISession()
		history := []*genai.Content{newTextContent("user", "hi"), newReconstructedFC("file_view")}
		result := s.spliceModelResponses(history)
		require.Len(t, result, 2)
		assert.Nil(t, result[1].Parts[0].ThoughtSignature)
	})

	t.Run("splices FC contents by position", func(t *testing.T) {
		s := &GenAISession{
			responses: []*genai.Content{
				newFCContent("file_view", "sig-1"),
				newFCContent("file_view", "sig-2"),
			},
		}
		history := []*genai.Content{
			newTextContent("user", "query"),
			newReconstructedFC("file_view"),
			newTextContent("user", "FR1"),
			newReconstructedFC("file_view"),
			newTextContent("user", "FR2"),
		}
		result := s.spliceModelResponses(history)
		require.Len(t, result, 5)
		assert.Equal(t, []byte("sig-1"), result[1].Parts[0].ThoughtSignature)
		assert.Equal(t, []byte("sig-2"), result[3].Parts[0].ThoughtSignature)
	})

	t.Run("text-only model content in history does not consume a recording", func(t *testing.T) {
		// Regression: if a nudge-path AI message lands in history as a text-only
		// model content, the old positional splicer would consume a recorded FC
		// response for it, shifting all subsequent FC splices by one.
		s := &GenAISession{
			responses: []*genai.Content{
				newFCContent("file_view", "sig-for-fc-1"),
				newFCContent("replace", "sig-for-fc-2"),
			},
		}
		history := []*genai.Content{
			newTextContent("user", "query"),
			newTextContent("model", "just thinking out loud"),
			newReconstructedFC("file_view"),
			newTextContent("user", "FR1"),
			newReconstructedFC("replace"),
			newTextContent("user", "FR2"),
		}
		result := s.spliceModelResponses(history)
		require.Len(t, result, 6)
		// Text-only model content preserved as-is (no FC so no signature needed).
		assert.Equal(t, "just thinking out loud", result[1].Parts[0].Text)
		// FC contents spliced with the correct recorded signatures.
		require.NotNil(t, result[2].Parts[0].FunctionCall)
		assert.Equal(t, "file_view", result[2].Parts[0].FunctionCall.Name)
		assert.Equal(t, []byte("sig-for-fc-1"), result[2].Parts[0].ThoughtSignature)
		require.NotNil(t, result[4].Parts[0].FunctionCall)
		assert.Equal(t, "replace", result[4].Parts[0].FunctionCall.Name)
		assert.Equal(t, []byte("sig-for-fc-2"), result[4].Parts[0].ThoughtSignature)
	})

	t.Run("splices even when compaction truncates FC args", func(t *testing.T) {
		// Compaction may rewrite the reconstructed FC's Args to a truncated
		// form. The splicer must still replace that content with the full
		// recorded response (which carries the signature).
		s := &GenAISession{
			responses: []*genai.Content{{
				Role: "model",
				Parts: []*genai.Part{{
					FunctionCall:     &genai.FunctionCall{Name: "file_view", Args: map[string]any{"path": "/very/long/original/path/that/was/truncated.go"}},
					ThoughtSignature: []byte("sig"),
				}},
			}},
		}
		history := []*genai.Content{
			newTextContent("user", "query"),
			{Role: "model", Parts: []*genai.Part{{
				FunctionCall: &genai.FunctionCall{Name: "file_view", Args: map[string]any{"path": "/very/long/..."}},
			}}},
			newTextContent("user", "FR"),
		}
		result := s.spliceModelResponses(history)
		require.Len(t, result, 3)
		assert.Equal(t, []byte("sig"), result[1].Parts[0].ThoughtSignature)
		assert.Equal(t, "/very/long/original/path/that/was/truncated.go", result[1].Parts[0].FunctionCall.Args["path"])
	})

	t.Run("more history FCs than recorded leaves trailing FCs as-is", func(t *testing.T) {
		s := &GenAISession{
			responses: []*genai.Content{newFCContent("file_view", "sig-1")},
		}
		history := []*genai.Content{
			newReconstructedFC("file_view"),
			newTextContent("user", "FR1"),
			newReconstructedFC("replace"),
			newTextContent("user", "FR2"),
		}
		result := s.spliceModelResponses(history)
		require.Len(t, result, 4)
		assert.Equal(t, []byte("sig-1"), result[0].Parts[0].ThoughtSignature)
		// Trailing FC without a recorded counterpart is kept as-is (unsigned).
		assert.Nil(t, result[2].Parts[0].ThoughtSignature)
	})
}

// TestGenAISession_RecordIfFC verifies recordings only capture FC-containing
// responses and that distinct sessions don't share recorded state — this is
// the property that prevents thought_signature drift when two analyses run
// concurrently on the same *Client.
func TestGenAISession_RecordIfFC(t *testing.T) {
	fcContent := &genai.Content{Role: "model", Parts: []*genai.Part{{
		FunctionCall: &genai.FunctionCall{Name: "file_view"}, ThoughtSignature: []byte("sig"),
	}}}
	textContent := &genai.Content{Role: "model", Parts: []*genai.Part{genai.NewPartFromText("just text")}}

	t.Run("nil session is a no-op", func(t *testing.T) {
		var s *GenAISession
		s.recordIfFC(fcContent) // must not panic
	})

	t.Run("ignores text-only content", func(t *testing.T) {
		s := NewGenAISession()
		s.recordIfFC(textContent)
		assert.Empty(t, s.responses)
	})

	t.Run("records FC-bearing content", func(t *testing.T) {
		s := NewGenAISession()
		s.recordIfFC(fcContent)
		require.Len(t, s.responses, 1)
		assert.Equal(t, []byte("sig"), s.responses[0].Parts[0].ThoughtSignature)
	})

	t.Run("two sessions are isolated", func(t *testing.T) {
		// Concurrent-analysis regression: each Plan() call gets its own session
		// so one analysis's recordings never leak into another's request.
		a := NewGenAISession()
		b := NewGenAISession()
		a.recordIfFC(fcContent)
		assert.Len(t, a.responses, 1)
		assert.Empty(t, b.responses)
	})
}
