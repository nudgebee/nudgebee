package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/common"

	"github.com/tmc/langchaingo/llms"
	"google.golang.org/genai"
)

// ToolDefinition represents a tool that can be used for native function calling.
// This is a provider-agnostic representation used by the planner.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema as map: {type, properties, required}
}

// GenAISession holds per-analysis recording state for the genai
// function-calling round-trip: the original model responses (with
// ThoughtSignatures) that must be spliced back into subsequent requests.
//
// Recording state is per-conversation, not per-Client. Sharing it on the
// long-lived *Client caused thought_signature drift when two analyses ran
// concurrently on the same workspace pod (e.g., an in-flight code-analysis
// run plus a PR-lifecycle followup): each Plan() call's ResetChat wiped the
// other's recordings, and concurrent appends interleaved entries from
// different conversations. By the time either analysis reached its 5th FC,
// the splicer had run out of (or replaced with the wrong) recorded responses,
// and Gemini rejected the request.
//
// Construct one with NewGenAISession at the start of an analysis and pass
// it through every GenerateContentWithTools call in that analysis. Never
// reuse across analyses.
type GenAISession struct {
	responses []*genai.Content
}

// NewGenAISession returns a fresh recording session for one analysis.
func NewGenAISession() *GenAISession {
	return &GenAISession{}
}

// recordIfFC appends content if it contains a FunctionCall part. Text- or
// thought-only model responses don't carry signatures Gemini requires on
// replay, so skipping them keeps responses in lockstep with FC-containing
// model contents the planner will rebuild in future turns.
func (s *GenAISession) recordIfFC(content *genai.Content) {
	if s == nil || content == nil || !contentHasFunctionCall(content) {
		return
	}
	s.responses = append(s.responses, content)
}

// spliceModelResponses replaces reconstructed FC-containing model contents
// in history with the originally recorded responses (which carry
// ThoughtSignatures). Matching is positional among FC-containing contents
// only — text/thought-only model messages pass through unchanged.
func (s *GenAISession) spliceModelResponses(history []*genai.Content) []*genai.Content {
	if s == nil || len(s.responses) == 0 {
		return history
	}
	result := make([]*genai.Content, 0, len(history))
	fcIdx := 0
	for _, content := range history {
		if content.Role == "model" && contentHasFunctionCall(content) && fcIdx < len(s.responses) {
			result = append(result, s.responses[fcIdx])
			fcIdx++
			continue
		}
		result = append(result, content)
	}
	return result
}

// GenerateContentWithTools calls the LLM with native function calling support.
// For GoogleAI, this bypasses langchaingo's limited convertTools and uses the
// genai SDK directly with proper nested schema support. The session captures
// per-analysis ThoughtSignature recordings and MUST be unique to one analysis.
// For other providers, the session is unused and may be nil.
func (c *Client) GenerateContentWithTools(
	ctx context.Context,
	messages []llms.MessageContent,
	tools []ToolDefinition,
	session *GenAISession,
	options ...llms.CallOption,
) (*llms.ContentResponse, error) {
	if Provider(c.config.LLM.Provider) == ProviderGoogleAI {
		return c.generateContentWithGenAI(ctx, messages, tools, session)
	}
	// Fallback for other providers: convert to langchaingo format
	return c.GenerateContent(ctx, messages, append(options, llms.WithTools(convertToLlmsTools(tools)))...)
}

// convertToLlmsTools converts ToolDefinition to langchaingo format (for non-GoogleAI providers).
func convertToLlmsTools(tools []ToolDefinition) []llms.Tool {
	result := make([]llms.Tool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return result
}

// generateContentWithGenAI uses the genai SDK directly to make function calling
// requests with properly nested tool schemas.
//
// Uses stateless GenerateContent (not Chat.Send) to avoid the persistent session
// mismatch bug where injected messages (budget warnings, circuit breakers) would
// prevent the FunctionResponse from reaching the chat session. Instead, we send
// the full conversation history each call and preserve ThoughtSignature by
// recording raw model responses on the per-analysis session and splicing them
// back into subsequent requests.
//
// This approach matches how Gemini CLI handles multi-turn function calling.
func (c *Client) generateContentWithGenAI(
	ctx context.Context,
	messages []llms.MessageContent,
	tools []ToolDefinition,
	session *GenAISession,
) (*llms.ContentResponse, error) {
	// Create genai client (lazily, once)
	if c.genaiClient == nil {
		if c.config.LLM.ApiKey == "" {
			return nil, fmt.Errorf("LLM_PROVIDER_API_KEY environment variable is required for GoogleAI provider")
		}
		client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:  c.config.LLM.ApiKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create genai client: %w", err)
		}
		c.genaiClient = client
	}

	// Convert langchaingo messages to genai format, extracting system instruction.
	// sanitizeFunctionCallOrdering ensures FC→FR adjacency in the history.
	systemInstruction, history := convertMessagesToGenAI(messages)
	if len(history) == 0 {
		return nil, fmt.Errorf("no messages to send")
	}

	// Splice recorded model responses (with ThoughtSignatures) into the history.
	// The planner's langchaingo messages don't carry ThoughtSignature, but we
	// recorded the raw genai model responses from previous calls in the
	// per-analysis session. Replace reconstructed "model" Content with the
	// originals to preserve ThoughtSignature.
	history = session.spliceModelResponses(history)

	// Build config
	temp := float32(0.1)
	genaiConfig := &genai.GenerateContentConfig{
		MaxOutputTokens: 16384,
		Temperature:     &temp,
		SafetySettings: []*genai.SafetySetting{
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
		},
	}

	genaiTools := convertToolDefsToGenAI(tools)
	if len(genaiTools) > 0 {
		genaiConfig.Tools = genaiTools
	}
	if systemInstruction != nil {
		genaiConfig.SystemInstruction = systemInstruction
	}

	// Retry with exponential backoff for rate limits
	const maxRetries = 5
	const baseDelay = 2 * time.Second

	var resp *genai.GenerateContentResponse
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = c.genaiClient.Models.GenerateContent(ctx, c.config.LLM.Model, history, genaiConfig)
		if err == nil {
			break
		}

		if !isTransientError(err) {
			if c.logger != nil {
				c.logger.Error(common.EventAnalysisFailure, "GenAI generation failed", err, map[string]any{
					"provider": c.config.LLM.Provider,
					"model":    c.config.LLM.Model,
					"attempt":  attempt + 1,
					"error":    err.Error(),
				})
			}
			return nil, fmt.Errorf("LLM generation failed (provider=%s, model=%s): %w",
				c.config.LLM.Provider, c.config.LLM.Model, err)
		}

		if attempt < maxRetries {
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
			if c.logger != nil {
				c.logger.Log(common.EventStepStart, "Transient error, retrying with backoff", map[string]any{
					"provider":    c.config.LLM.Provider,
					"attempt":     attempt + 1,
					"max_retries": maxRetries,
					"retry_after": delay.String(),
				})
			}
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}
		} else {
			return nil, fmt.Errorf("LLM generation failed after %d retries (transient errors): %w", maxRetries, err)
		}
	}

	if resp == nil || len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("empty response from genai")
	}

	// Record the raw model response (with ThoughtSignature) on the session for
	// future calls in this analysis. recordIfFC ignores text/thought-only
	// responses, which keeps the recording in lockstep with FC-containing model
	// messages the planner will rebuild in future turns' history.
	if len(resp.Candidates) > 0 {
		session.recordIfFC(resp.Candidates[0].Content)
	}

	// Convert response to langchaingo format
	contentResp := convertGenAIResponse(resp)

	// Track token usage under lock
	if resp.UsageMetadata != nil {
		c.addTokenUsage(
			int(resp.UsageMetadata.PromptTokenCount),
			int(resp.UsageMetadata.CandidatesTokenCount),
			int(resp.UsageMetadata.TotalTokenCount),
			int(resp.UsageMetadata.CachedContentTokenCount),
		)
	}

	return contentResp, nil
}

// convertToolDefsToGenAI converts ToolDefinition slice to genai Tool format
// with proper recursive schema handling.
func convertToolDefsToGenAI(tools []ToolDefinition) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		decl := &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
		}
		if tool.Parameters != nil {
			decl.Parameters = convertMapToGenAISchema(tool.Parameters)
		}
		decls = append(decls, decl)
	}

	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// convertMapToGenAISchema recursively converts a JSON Schema map to genai.Schema.
func convertMapToGenAISchema(m map[string]any) *genai.Schema {
	if m == nil {
		return nil
	}

	schema := &genai.Schema{}

	// Type
	if t, ok := m["type"].(string); ok {
		schema.Type = schemaTypeFromString(t)
	}

	// Description
	if d, ok := m["description"].(string); ok {
		schema.Description = d
	}

	// Format
	if f, ok := m["format"].(string); ok {
		schema.Format = f
	}

	// Nullable
	if n, ok := m["nullable"].(bool); ok {
		schema.Nullable = &n
	}

	// Enum
	if e, ok := m["enum"]; ok {
		schema.Enum = toStringSlice(e)
	}

	// Items (for arrays)
	if items, ok := m["items"].(map[string]any); ok {
		schema.Items = convertMapToGenAISchema(items)
	}

	// Properties (for objects)
	if props, ok := m["properties"].(map[string]any); ok {
		schema.Properties = make(map[string]*genai.Schema)
		for name, val := range props {
			if valMap, ok := val.(map[string]any); ok {
				schema.Properties[name] = convertMapToGenAISchema(valMap)
			}
		}
	}

	// Required
	if req, ok := m["required"]; ok {
		schema.Required = toStringSlice(req)
	}

	return schema
}

// schemaTypeFromString converts a JSON Schema type string to genai.Type.
func schemaTypeFromString(t string) genai.Type {
	switch t {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeUnspecified
	}
}

// toStringSlice converts an interface{} to []string.
// Handles both []string and []interface{} formats.
func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		result := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	return nil
}

// convertMessagesToGenAI converts langchaingo messages to genai Content format.
// Returns system instruction (if any) and the conversation history.
//
// Gemini requires that a FunctionResponse content immediately follows the
// content containing the corresponding FunctionCall. This function enforces
// that invariant by post-processing the history to relocate any messages
// that were inserted between a function call and its response.
func convertMessagesToGenAI(messages []llms.MessageContent) (*genai.Content, []*genai.Content) {
	var systemInstruction *genai.Content
	history := make([]*genai.Content, 0, len(messages))

	for _, msg := range messages {
		parts := convertLlmsPartsToGenAI(msg.Parts)
		if len(parts) == 0 {
			continue
		}

		content := &genai.Content{Parts: parts}

		switch msg.Role {
		case llms.ChatMessageTypeSystem:
			// System instructions go to a separate field
			systemInstruction = content
			continue
		case llms.ChatMessageTypeAI:
			content.Role = "model"
		case llms.ChatMessageTypeHuman:
			content.Role = "user"
		case llms.ChatMessageTypeTool:
			// Tool responses are sent as "user" role in genai
			content.Role = "user"
		default:
			content.Role = "user"
		}

		history = append(history, content)
	}

	history = sanitizeFunctionCallOrdering(history)

	return systemInstruction, history
}

// sanitizeFunctionCallOrdering ensures every Content with a FunctionCall part
// is immediately followed by a Content with the corresponding FunctionResponse.
// Any non-function-response messages between them are relocated before the
// function call to preserve Gemini's strict ordering requirement.
func sanitizeFunctionCallOrdering(history []*genai.Content) []*genai.Content {
	result := make([]*genai.Content, 0, len(history))

	for i := 0; i < len(history); i++ {
		msg := history[i]

		// Check if this message contains a FunctionCall
		if !contentHasFunctionCall(msg) {
			result = append(result, msg)
			continue
		}

		// This is a model message with a FunctionCall. The next message MUST
		// be a user message containing a FunctionResponse. If there are
		// intervening messages (e.g. a separator text), collect them and
		// move them before this function call.
		var interlopers []*genai.Content
		j := i + 1
		for j < len(history) && !contentHasFunctionResponse(history[j]) {
			// Only relocate user-role messages (e.g. budget warnings, separators).
			// Never move model messages — they may contain FunctionCalls which
			// would create consecutive FCs without FRs if relocated.
			if history[j].Role == "user" {
				interlopers = append(interlopers, history[j])
			}
			j++
		}

		if len(interlopers) > 0 {
			// Move interlopers before the function call
			result = append(result, interlopers...)
		}

		// Append the function call
		result = append(result, msg)

		// Append the function response if found
		if j < len(history) {
			result = append(result, history[j])
			i = j // skip past everything we've consumed
		}
	}

	return result
}

// contentHasFunctionCall returns true if the Content contains a FunctionCall part.
func contentHasFunctionCall(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p.FunctionCall != nil {
			return true
		}
	}
	return false
}

// contentHasFunctionResponse returns true if the Content contains a FunctionResponse part.
func contentHasFunctionResponse(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

// convertLlmsPartsToGenAI converts langchaingo content parts to genai parts.
func convertLlmsPartsToGenAI(parts []llms.ContentPart) []*genai.Part {
	result := make([]*genai.Part, 0, len(parts))

	for _, part := range parts {
		switch p := part.(type) {
		case llms.TextContent:
			if p.Text != "" {
				result = append(result, genai.NewPartFromText(p.Text))
			}
		case llms.ToolCall:
			if p.FunctionCall != nil {
				var argsMap map[string]any
				if err := json.Unmarshal([]byte(p.FunctionCall.Arguments), &argsMap); err != nil {
					argsMap = map[string]any{}
				}
				result = append(result, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   p.ID,
						Name: p.FunctionCall.Name,
						Args: argsMap,
					},
				})
			}
		case llms.ToolCallResponse:
			result = append(result, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       p.ToolCallID,
					Name:     p.Name,
					Response: map[string]any{"response": p.Content},
				},
			})
		}
	}

	return result
}

// convertGenAIResponse converts a genai response to langchaingo format.
func convertGenAIResponse(resp *genai.GenerateContentResponse) *llms.ContentResponse {
	contentResponse := &llms.ContentResponse{}

	for _, candidate := range resp.Candidates {
		var buf strings.Builder
		var toolCalls []llms.ToolCall

		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					buf.WriteString(part.Text)
				}
				if part.FunctionCall != nil {
					b, err := json.Marshal(part.FunctionCall.Args)
					if err != nil {
						continue
					}
					toolCalls = append(toolCalls, llms.ToolCall{
						ID: part.FunctionCall.ID,
						FunctionCall: &llms.FunctionCall{
							Name:      part.FunctionCall.Name,
							Arguments: string(b),
						},
					})
				}
			}
		}

		metadata := make(map[string]any)
		if resp.UsageMetadata != nil {
			metadata["input_tokens"] = resp.UsageMetadata.PromptTokenCount
			metadata["output_tokens"] = resp.UsageMetadata.CandidatesTokenCount
			metadata["total_tokens"] = resp.UsageMetadata.TotalTokenCount
		}

		contentResponse.Choices = append(contentResponse.Choices, &llms.ContentChoice{
			Content:        buf.String(),
			StopReason:     string(candidate.FinishReason),
			GenerationInfo: metadata,
			ToolCalls:      toolCalls,
		})
	}

	return contentResponse
}
