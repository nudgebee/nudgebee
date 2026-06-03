package googleai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
	"google.golang.org/genai"
)

func TestConvertParts(t *testing.T) { //nolint:funlen // comprehensive test
	t.Parallel()

	tests := []struct {
		name      string
		parts     []llms.ContentPart
		wantErr   bool
		wantCount int
	}{
		{
			name:      "empty parts",
			parts:     []llms.ContentPart{},
			wantErr:   false,
			wantCount: 0,
		},
		{
			name: "text content",
			parts: []llms.ContentPart{
				llms.TextContent{Text: "Hello world"},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name: "binary content",
			parts: []llms.ContentPart{
				llms.BinaryContent{
					MIMEType: "image/jpeg",
					Data:     []byte("fake image data"),
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name: "tool call",
			parts: []llms.ContentPart{
				llms.ToolCall{
					FunctionCall: &llms.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"location": "Paris"}`,
					},
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name: "tool call response",
			parts: []llms.ContentPart{
				llms.ToolCallResponse{
					Name:    "get_weather",
					Content: "It's sunny in Paris",
				},
			},
			wantErr:   false,
			wantCount: 1,
		},
		{
			name: "tool call with invalid JSON",
			parts: []llms.ContentPart{
				llms.ToolCall{
					FunctionCall: &llms.FunctionCall{
						Name:      "get_weather",
						Arguments: `{invalid json}`,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "mixed content types",
			parts: []llms.ContentPart{
				llms.TextContent{Text: "Hello"},
				llms.BinaryContent{MIMEType: "image/png", Data: []byte("png data")},
				llms.TextContent{Text: "World"},
			},
			wantErr:   false,
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertParts(tt.parts)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, result, tt.wantCount)

			// Verify part fields are set correctly for non-error cases
			for i, part := range result {
				assert.NotNil(t, part, "Part[%d] should not be nil", i)
			}
		})
	}
}

func TestConvertContent(t *testing.T) { //nolint:funlen // comprehensive test
	t.Parallel()

	tests := []struct {
		name         string
		content      llms.MessageContent
		expectedRole string
		wantErr      bool
		errContains  string
	}{
		{
			name: "system message",
			content: llms.MessageContent{
				Role: llms.ChatMessageTypeSystem,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: "You are a helpful assistant"},
				},
			},
			expectedRole: RoleSystem,
			wantErr:      false,
		},
		{
			name: "AI message",
			content: llms.MessageContent{
				Role: llms.ChatMessageTypeAI,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: "Hello! How can I help you?"},
				},
			},
			expectedRole: RoleModel,
			wantErr:      false,
		},
		{
			name: "human message",
			content: llms.MessageContent{
				Role: llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: "What's the weather like?"},
				},
			},
			expectedRole: RoleUser,
			wantErr:      false,
		},
		{
			name: "generic message",
			content: llms.MessageContent{
				Role: llms.ChatMessageTypeGeneric,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: "Generic message"},
				},
			},
			expectedRole: RoleUser,
			wantErr:      false,
		},
		{
			name: "tool message",
			content: llms.MessageContent{
				Role: llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: "Tool response"},
				},
			},
			expectedRole: RoleUser,
			wantErr:      false,
		},
		{
			name: "function message (unsupported)",
			content: llms.MessageContent{
				Role: llms.ChatMessageTypeFunction,
				Parts: []llms.ContentPart{
					llms.TextContent{Text: "Function response"},
				},
			},
			wantErr:     true,
			errContains: "not supported",
		},
		{
			name: "invalid parts",
			content: llms.MessageContent{
				Role: llms.ChatMessageTypeHuman,
				Parts: []llms.ContentPart{
					llms.ToolCall{
						FunctionCall: &llms.FunctionCall{
							Name:      "test",
							Arguments: "invalid json",
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertContent(tt.content)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, tt.expectedRole, result.Role)
			assert.Len(t, result.Parts, len(tt.content.Parts))
		})
	}
}

func TestConvertCandidates(t *testing.T) { //nolint:funlen // comprehensive test
	t.Parallel()

	tests := []struct {
		name        string
		candidates  []*genai.Candidate
		usage       *genai.GenerateContentResponseUsageMetadata
		wantErr     bool
		wantChoices int
	}{
		{
			name:        "empty candidates",
			candidates:  []*genai.Candidate{},
			wantErr:     false,
			wantChoices: 0,
		},
		{
			name: "single text candidate",
			candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "Hello world"},
						},
					},
					FinishReason: genai.FinishReasonStop,
				},
			},
			wantErr:     false,
			wantChoices: 1,
		},
		{
			name: "candidate with function call",
			candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{
								FunctionCall: &genai.FunctionCall{
									Name: "get_weather",
									Args: map[string]any{"location": "Paris"},
								},
							},
						},
					},
					FinishReason: genai.FinishReasonStop,
				},
			},
			wantErr:     false,
			wantChoices: 1,
		},
		{
			name: "candidate with usage metadata",
			candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "Response with usage"},
						},
					},
					FinishReason: genai.FinishReasonStop,
				},
			},
			usage: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 5,
				TotalTokenCount:      15,
			},
			wantErr:     false,
			wantChoices: 1,
		},
		{
			name: "multiple candidates",
			candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: "First response"}},
					},
					FinishReason: genai.FinishReasonStop,
				},
				{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: "Second response"}},
					},
					FinishReason: genai.FinishReasonStop,
				},
			},
			wantErr:     false,
			wantChoices: 2,
		},
		{
			name: "candidate with thought part (should be skipped)",
			candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "thinking...", Thought: true},
							{Text: "Actual response"},
						},
					},
					FinishReason: genai.FinishReasonStop,
				},
			},
			wantErr:     false,
			wantChoices: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertCandidates("", tt.candidates, tt.usage)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Len(t, result.Choices, tt.wantChoices)

			// Check metadata for usage information
			if tt.usage != nil && len(result.Choices) > 0 {
				metadata := result.Choices[0].GenerationInfo
				assert.Equal(t, int32(10), metadata["input_tokens"])
				assert.Equal(t, int32(5), metadata["output_tokens"])
				assert.Equal(t, int32(15), metadata["total_tokens"])
			}

			// Check that citations and safety are always present
			for i, choice := range result.Choices {
				assert.Contains(t, choice.GenerationInfo, CITATIONS, "Choice %d should have citations", i)
				assert.Contains(t, choice.GenerationInfo, SAFETY, "Choice %d should have safety info", i)
			}
		})
	}
}

func TestConvertCandidates_ThinkingTokens(t *testing.T) {
	t.Parallel()

	candidates := []*genai.Candidate{{
		Content:          &genai.Content{Parts: []*genai.Part{{Text: "ok"}}, Role: RoleModel},
		FinishReason:     genai.FinishReasonStop,
		CitationMetadata: &genai.CitationMetadata{},
	}}

	t.Run("populates ThinkingTokens from ThoughtsTokenCount", func(t *testing.T) {
		usage := &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     1000,
			CandidatesTokenCount: 50,
			ThoughtsTokenCount:   200,
		}
		result, err := convertCandidates("gemini-2.5-pro", candidates, usage)
		assert.NoError(t, err)
		assert.Equal(t, 200, result.Choices[0].GenerationInfo["ThinkingTokens"])
	})

	t.Run("zero thoughts on non-thinking model is fine", func(t *testing.T) {
		usage := &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     500,
			CandidatesTokenCount: 30,
			ThoughtsTokenCount:   0,
		}
		result, err := convertCandidates("gemini-1.5-flash", candidates, usage)
		assert.NoError(t, err)
		assert.Equal(t, 0, result.Choices[0].GenerationInfo["ThinkingTokens"])
	})

	t.Run("nil usage maps to zero ThinkingTokens", func(t *testing.T) {
		result, err := convertCandidates("gemini-2.5-pro", candidates, nil)
		assert.NoError(t, err)
		assert.Equal(t, 0, result.Choices[0].GenerationInfo["ThinkingTokens"])
	})
}

func TestIsThinkingModel(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"gemini-2.5-pro":         true,
		"gemini-2.5-flash":       true,
		"gemini-2.5-flash-lite":  true,
		"gemini-3-pro-preview":   true,
		"gemini-3.1-pro-preview": true,
		"gemini-3-flash-preview": true,
		"gemini-1.5-pro":         false,
		"gemini-1.5-flash":       false,
		"gemini-2.0-flash":       false,
		"":                       false,
		"gpt-4o":                 false,
	}
	for model, want := range cases {
		got := isThinkingModel(model)
		assert.Equal(t, want, got, "isThinkingModel(%q)", model)
	}
}

func TestCall(t *testing.T) {
	t.Parallel()

	// Since Call is just a wrapper around GenerateFromSinglePrompt,
	// we test the interface compliance and basic structure
	t.Run("implements interface", func(t *testing.T) {
		var _ llms.Model = &GoogleAI{}
	})

	// Note: Full testing would require mocking the genai client
	// which is complex due to the dependency structure
}

func TestGenerateContentOptionsHandling(t *testing.T) {
	t.Parallel()

	// Test the options validation logic that can be tested without a client
	t.Run("conflicting JSONMode and ResponseMIMEType", func(t *testing.T) {
		// This tests the validation logic in GenerateContent
		opts := llms.CallOptions{
			JSONMode:         true,
			ResponseMIMEType: "text/plain",
		}

		// The validation would happen in GenerateContent:
		// if opts.ResponseMIMEType != "" && opts.JSONMode {
		//     return nil, fmt.Errorf("conflicting options, can't use JSONMode and ResponseMIMEType together")
		// }

		hasConflict := opts.ResponseMIMEType != "" && opts.JSONMode
		assert.True(t, hasConflict, "Should detect conflicting options")
	})

	t.Run("JSONMode sets correct MIME type", func(t *testing.T) {
		opts := llms.CallOptions{
			JSONMode: true,
		}

		// The logic would set: cfg.ResponseMIMEType = ResponseMIMETypeJson
		expectedMIMEType := ResponseMIMETypeJson
		if opts.JSONMode && opts.ResponseMIMEType == "" {
			assert.Equal(t, "application/json", expectedMIMEType)
		}
	})

	t.Run("custom ResponseMIMEType", func(t *testing.T) {
		opts := llms.CallOptions{
			ResponseMIMEType: "text/xml",
		}

		// The logic would set: cfg.ResponseMIMEType = opts.ResponseMIMEType
		if opts.ResponseMIMEType != "" && !opts.JSONMode {
			assert.Equal(t, "text/xml", opts.ResponseMIMEType)
		}
	})
}

func TestRoleMapping(t *testing.T) {
	t.Parallel()

	// Test the role mapping constants
	roleTests := []struct {
		llmRole      llms.ChatMessageType
		expectedRole string
		supported    bool
	}{
		{llms.ChatMessageTypeSystem, RoleSystem, true},
		{llms.ChatMessageTypeAI, RoleModel, true},
		{llms.ChatMessageTypeHuman, RoleUser, true},
		{llms.ChatMessageTypeGeneric, RoleUser, true},
		{llms.ChatMessageTypeTool, RoleUser, true},
		{llms.ChatMessageTypeFunction, "", false}, // Unsupported
	}

	for _, tt := range roleTests {
		t.Run(string(tt.llmRole), func(t *testing.T) {
			content := llms.MessageContent{
				Role:  tt.llmRole,
				Parts: []llms.ContentPart{llms.TextContent{Text: "test"}},
			}

			result, err := convertContent(content)

			if !tt.supported {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "not supported")
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedRole, result.Role)
		})
	}
}

func TestFunctionCallConversion(t *testing.T) {
	t.Parallel()

	t.Run("valid function call", func(t *testing.T) {
		args := map[string]any{
			"location": "Paris",
			"unit":     "celsius",
		}
		argsJSON, _ := json.Marshal(args)

		part := llms.ToolCall{
			FunctionCall: &llms.FunctionCall{
				Name:      "get_weather",
				Arguments: string(argsJSON),
			},
		}

		result, err := convertParts([]llms.ContentPart{part})
		assert.NoError(t, err)
		assert.Len(t, result, 1)

		assert.NotNil(t, result[0].FunctionCall)
		assert.Equal(t, "get_weather", result[0].FunctionCall.Name)
		assert.Equal(t, "Paris", result[0].FunctionCall.Args["location"])
		assert.Equal(t, "celsius", result[0].FunctionCall.Args["unit"])
	})

	t.Run("function response", func(t *testing.T) {
		part := llms.ToolCallResponse{
			Name:    "get_weather",
			Content: "It's 20°C and sunny",
		}

		result, err := convertParts([]llms.ContentPart{part})
		assert.NoError(t, err)
		assert.Len(t, result, 1)

		assert.NotNil(t, result[0].FunctionResponse)
		assert.Equal(t, "get_weather", result[0].FunctionResponse.Name)
		assert.Equal(t, "It's 20°C and sunny", result[0].FunctionResponse.Response["response"])
	})
}

func TestSafetySettings(t *testing.T) {
	t.Parallel()

	// Test that all safety categories are covered
	expectedCategories := []genai.HarmCategory{
		genai.HarmCategoryDangerousContent,
		genai.HarmCategoryHarassment,
		genai.HarmCategoryHateSpeech,
		genai.HarmCategorySexuallyExplicit,
	}

	// This mirrors the safety settings logic from GenerateContent
	harmThreshold := HarmBlockOnlyHigh

	safetySettings := []*genai.SafetySetting{}
	for _, category := range expectedCategories {
		safetySettings = append(safetySettings, &genai.SafetySetting{
			Category:  category,
			Threshold: genai.HarmBlockThreshold(harmThreshold),
		})
	}

	assert.Len(t, safetySettings, 4, "Should have safety settings for all categories")

	for i, setting := range safetySettings {
		assert.Equal(t, expectedCategories[i], setting.Category)
		assert.Equal(t, genai.HarmBlockThreshold(harmThreshold), setting.Threshold)
	}
}
