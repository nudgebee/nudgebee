//nolint:all
package googleai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/llms/googleai/internal/imageutil"

	"github.com/tmc/langchaingo/llms"
	"google.golang.org/genai"
)

var (
	ErrNoContentInResponse   = errors.New("no content in generation response")
	ErrUnknownPartInResponse = errors.New("unknown part type in generation response")
	ErrInvalidMimeType       = errors.New("invalid mime type on content")
	ErrorNilIterator         = errors.New("iterator is nil")
)

const (
	CITATIONS            = "citations"
	SAFETY               = "safety"
	RoleSystem           = "system"
	RoleModel            = "model"
	RoleUser             = "user"
	RoleTool             = "tool"
	ResponseMIMETypeJson = "application/json"
)

// Call implements the [llms.Model] interface.
func (g *GoogleAI) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, g, prompt, options...)
}

// GenerateContent implements the [llms.Model] interface.
func (g *GoogleAI) GenerateContent(
	ctx context.Context,
	messages []llms.MessageContent,
	options ...llms.CallOption,
) (*llms.ContentResponse, error) {
	if g.CallbacksHandler != nil {
		g.CallbacksHandler.HandleLLMGenerateContentStart(ctx, messages)
	}

	opts := llms.CallOptions{
		Model:          g.opts.DefaultModel,
		CandidateCount: g.opts.DefaultCandidateCount,
		MaxTokens:      g.opts.DefaultMaxTokens,
		Temperature:    g.opts.DefaultTemperature,
		TopP:           g.opts.DefaultTopP,
		TopK:           g.opts.DefaultTopK,
	}
	for _, opt := range options {
		opt(&opts)
	}

	// Update the tracked model if it was overridden
	effectiveModel := opts.Model
	if effectiveModel != "" && effectiveModel != g.model {
		g.model = effectiveModel
	}

	// Build the GenerateContentConfig
	temp := float32(opts.Temperature)
	topP := float32(opts.TopP)
	topK := float32(opts.TopK)

	cfg := &genai.GenerateContentConfig{
		CandidateCount:  int32(opts.CandidateCount),
		MaxOutputTokens: int32(opts.MaxTokens),
		Temperature:     &temp,
		TopP:            &topP,
		TopK:            &topK,
		StopSequences:   opts.StopWords,
		SafetySettings: []*genai.SafetySetting{
			{
				Category:  genai.HarmCategoryDangerousContent,
				Threshold: genai.HarmBlockThreshold(g.opts.HarmThreshold),
			},
			{
				Category:  genai.HarmCategoryHarassment,
				Threshold: genai.HarmBlockThreshold(g.opts.HarmThreshold),
			},
			{
				Category:  genai.HarmCategoryHateSpeech,
				Threshold: genai.HarmBlockThreshold(g.opts.HarmThreshold),
			},
			{
				Category:  genai.HarmCategorySexuallyExplicit,
				Threshold: genai.HarmBlockThreshold(g.opts.HarmThreshold),
			},
		},
	}

	// Apply cached content name so Gemini prepends the cached system instruction.
	// Without this, the caching layer strips the system message from the request but
	// never tells Gemini which cached content to use, resulting in a bare human message
	// that triggers MALFORMED_FUNCTION_CALL on XML-heavy ReAct/ReWoo prompts.
	if val, ok := opts.Metadata["CachedContentName"]; ok {
		if name, ok := val.(string); ok && name != "" {
			cfg.CachedContent = name
		}
	}

	// Support for ThinkingConfig (Gemini 2.5+ models).
	if val, ok := opts.Metadata["ThinkingLevel"]; ok {
		if level, ok := val.(string); ok && level != "" {
			cfg.ThinkingConfig = &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevel(strings.ToUpper(level)),
			}
		}
	}

	// set ResponseMIMEType from either opts.JSONMode or opts.ResponseMIMEType
	switch {
	case opts.ResponseMIMEType != "" && opts.JSONMode:
		return nil, fmt.Errorf("conflicting options, can't use JSONMode and ResponseMIMEType together")
	case opts.ResponseMIMEType != "" && !opts.JSONMode:
		cfg.ResponseMIMEType = opts.ResponseMIMEType
	case opts.ResponseMIMEType == "" && opts.JSONMode:
		cfg.ResponseMIMEType = ResponseMIMETypeJson
	}

	var err error
	if cfg.Tools, err = convertTools(opts.Tools); err != nil {
		return nil, err
	}

	// When no tools are provided, explicitly disable function calling.
	// Gemini 3 Flash models may attempt function calls when prompts contain XML-like
	// tags (e.g. <final_answer>, <missing_information>), returning MALFORMED_FUNCTION_CALL
	// with empty content. Setting ToolConfig to NONE prevents this behavior.
	// EXCEPTION: When CachedContent is set, Gemini rejects any combination of
	// CachedContent with tools, tool_config, or system_instruction in the live request —
	// all must have been baked into the cached content at creation time.
	// NOTE: cfg.Tools must be set to a non-nil empty slice for the API to respect ToolConfig;
	// when Tools is nil, the API ignores ToolConfig entirely.
	if len(cfg.Tools) == 0 && cfg.CachedContent == "" {
		cfg.Tools = []*genai.Tool{}
		cfg.ToolConfig = &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeNone,
			},
		}
	}

	var response *llms.ContentResponse

	if len(messages) == 1 {
		theMessage := messages[0]
		if theMessage.Role != llms.ChatMessageTypeHuman {
			return nil, fmt.Errorf("got %v message role, want human", theMessage.Role)
		}
		response, err = generateFromSingleMessage(ctx, g.client, opts.Model, theMessage.Parts, cfg, &opts)
	} else {
		response, err = generateFromMessages(ctx, g.client, opts.Model, messages, cfg, &opts)
	}
	if err != nil {
		return nil, err
	}

	if g.CallbacksHandler != nil {
		g.CallbacksHandler.HandleLLMGenerateContentEnd(ctx, response)
	}

	return response, nil
}

// convertCandidates converts a sequence of genai.Candidate to a response.
// isThinkingModel returns true for Gemini families that support thoughts
// (2.5 and 3.x). Used as the gate for the schema-drift warning below — if
// one of these returns ThoughtsTokenCount=0 alongside non-zero output, the
// SDK has likely renamed the field and we're silently undercounting cost.
func isThinkingModel(modelName string) bool {
	if modelName == "" {
		return false
	}
	m := strings.ToLower(modelName)
	return strings.HasPrefix(m, "gemini-2.5") || strings.HasPrefix(m, "gemini-3")
}

func convertCandidates(modelName string, candidates []*genai.Candidate, usage *genai.GenerateContentResponseUsageMetadata) (*llms.ContentResponse, error) {
	var contentResponse llms.ContentResponse
	var toolCalls []llms.ToolCall

	for _, candidate := range candidates {
		buf := strings.Builder{}

		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Thought {
					// Skip thinking/reasoning tokens (Gemini 2.5+)
					continue
				}
				if part.Text != "" {
					_, err := buf.WriteString(part.Text)
					if err != nil {
						return nil, err
					}
				}
				if part.FunctionCall != nil {
					fc := part.FunctionCall
					b, err := common.MarshalJson(fc.Args)
					if err != nil {
						return nil, err
					}
					toolCall := llms.ToolCall{
						FunctionCall: &llms.FunctionCall{
							Name:      fc.Name,
							Arguments: string(b),
						},
					}
					toolCalls = append(toolCalls, toolCall)
				}
				if part.InlineData != nil {
					// Silently skip inline data parts (e.g. generated images)
					slog.Warn("googleai: skipping InlineData part in response")
				}
			}
		}

		metadata := make(map[string]any)
		metadata[CITATIONS] = candidate.CitationMetadata
		metadata[SAFETY] = candidate.SafetyRatings

		if usage != nil {
			metadata["input_tokens"] = usage.PromptTokenCount
			metadata["output_tokens"] = usage.CandidatesTokenCount
			metadata["total_tokens"] = usage.TotalTokenCount
			// Standardized field names for cross-provider compatibility
			metadata["PromptTokens"] = usage.PromptTokenCount
			metadata["CompletionTokens"] = usage.CandidatesTokenCount
			metadata["TotalTokens"] = usage.TotalTokenCount

			// Cache-related token information (if available)
			if usage.CachedContentTokenCount > 0 {
				metadata["CachedTokens"] = usage.CachedContentTokenCount
				metadata["CacheReadInputTokens"] = usage.CachedContentTokenCount // Anthropic compatibility
				// Google AI includes cached tokens in the prompt count, calculate non-cached
				metadata["NonCachedInputTokens"] = usage.PromptTokenCount - usage.CachedContentTokenCount
			}
		}

		// Google AI doesn't surface thinking content as a separate Part — we keep an
		// empty placeholder for cross-provider symmetry with OpenAI o1.
		metadata["ThinkingContent"] = ""
		// ThoughtsTokenCount is reported by Gemini 2.5+ thinking models in
		// usage_metadata. Pre-2.5 models return 0. Captured here so the
		// cost formula can charge thinking tokens at the output rate.
		if usage != nil {
			thoughts := int(usage.ThoughtsTokenCount)
			metadata["ThinkingTokens"] = thoughts
			// Defensive: thinking-class models with non-zero prompt+output but
			// zero thoughts are suspicious. Catches SDK schema drift (e.g.
			// genai v2 renaming the field) — without this, cost would silently
			// undercount by output_rate × thinking_tokens for every call.
			if thoughts == 0 && usage.PromptTokenCount > 0 && usage.CandidatesTokenCount > 0 && isThinkingModel(modelName) {
				slog.Warn("googleai: thinking-class model returned 0 thoughts; SDK schema may have changed",
					"model", modelName,
					"prompt_tokens", usage.PromptTokenCount,
					"output_tokens", usage.CandidatesTokenCount)
			}
		} else {
			metadata["ThinkingTokens"] = 0
		}

		// Note: Google AI's CachedContent requires pre-created cached content via API,
		// not inline cache control like Anthropic. Use Client.CreateCachedContent() for caching.

		contentResponse.Choices = append(contentResponse.Choices,
			&llms.ContentChoice{
				Content:        buf.String(),
				StopReason:     string(candidate.FinishReason),
				GenerationInfo: metadata,
				ToolCalls:      toolCalls,
			})
	}
	return &contentResponse, nil
}

// convertParts converts between a sequence of langchain parts and genai parts.
func convertParts(parts []llms.ContentPart) ([]*genai.Part, error) {
	convertedParts := make([]*genai.Part, 0, len(parts))
	for _, part := range parts {
		var out *genai.Part

		switch p := part.(type) {
		case llms.TextContent:
			if p.Text == "" {
				continue // Skip empty text parts — Google AI rejects them with INVALID_ARGUMENT
			}
			out = &genai.Part{Text: p.Text}
		case llms.BinaryContent:
			out = &genai.Part{InlineData: &genai.Blob{MIMEType: p.MIMEType, Data: p.Data}}
		case llms.ImageURLContent:
			typ, data, err := imageutil.DownloadImageData(p.URL)
			if err != nil {
				return nil, err
			}
			out = &genai.Part{InlineData: &genai.Blob{MIMEType: typ, Data: data}}
		case llms.ToolCall:
			fc := p.FunctionCall
			var argsMap map[string]any
			if err := common.UnmarshalJson([]byte(fc.Arguments), &argsMap); err != nil {
				return convertedParts, err
			}
			out = &genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: fc.Name,
					Args: argsMap,
				},
			}
		case llms.ToolCallResponse:
			out = &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name: p.Name,
					Response: map[string]any{
						"response": p.Content,
					},
				},
			}
		default:
			// Skip unknown part types gracefully
			slog.Warn("googleai: skipping unknown content part type", "type", fmt.Sprintf("%T", part))
			continue
		}

		convertedParts = append(convertedParts, out)
	}
	return convertedParts, nil
}

// convertContent converts between a langchain MessageContent and genai content.
func convertContent(content llms.MessageContent) (*genai.Content, error) {
	parts, err := convertParts(content.Parts)
	if err != nil {
		return nil, err
	}

	c := &genai.Content{
		Parts: parts,
	}

	switch content.Role {
	case llms.ChatMessageTypeSystem:
		c.Role = RoleSystem
	case llms.ChatMessageTypeAI:
		c.Role = RoleModel
	case llms.ChatMessageTypeHuman:
		c.Role = RoleUser
	case llms.ChatMessageTypeGeneric:
		c.Role = RoleUser
	case llms.ChatMessageTypeTool:
		c.Role = RoleUser
	case llms.ChatMessageTypeFunction:
		fallthrough
	default:
		return nil, fmt.Errorf("role %v not supported", content.Role)
	}

	return c, nil
}

// generateFromSingleMessage generates content from the parts of a single message.
func generateFromSingleMessage(
	ctx context.Context,
	client *genai.Client,
	modelName string,
	parts []llms.ContentPart,
	cfg *genai.GenerateContentConfig,
	opts *llms.CallOptions,
) (*llms.ContentResponse, error) {
	convertedParts, err := convertParts(parts)
	if err != nil {
		return nil, err
	}

	contents := []*genai.Content{
		{Parts: convertedParts, Role: RoleUser},
	}

	if opts.StreamingFunc == nil {
		resp, err := client.Models.GenerateContent(ctx, modelName, contents, cfg)
		if err != nil {
			return nil, err
		}
		if len(resp.Candidates) == 0 {
			return nil, ErrNoContentInResponse
		}
		return convertCandidates(modelName, resp.Candidates, resp.UsageMetadata)
	}
	return streamFromContents(ctx, client, modelName, contents, cfg, opts)
}

func generateFromMessages(
	ctx context.Context,
	client *genai.Client,
	modelName string,
	messages []llms.MessageContent,
	cfg *genai.GenerateContentConfig,
	opts *llms.CallOptions,
) (*llms.ContentResponse, error) {
	contents := make([]*genai.Content, 0, len(messages))

	// Merge all system messages into a single SystemInstruction.
	// Google AI expects exactly one SystemInstruction — multiple system messages
	// must have their parts concatenated, not overwritten.
	var systemParts []*genai.Part
	for _, mc := range messages {
		content, err := convertContent(mc)
		if err != nil {
			return nil, err
		}
		if len(content.Parts) == 0 {
			continue // Skip messages with no valid parts (e.g. all empty text)
		}
		if content.Role == RoleSystem {
			systemParts = append(systemParts, content.Parts...)
			continue
		}
		contents = append(contents, content)
	}
	if len(systemParts) > 0 {
		cfg.SystemInstruction = &genai.Content{Role: RoleSystem, Parts: systemParts}
	}

	if opts.StreamingFunc == nil {
		resp, err := client.Models.GenerateContent(ctx, modelName, contents, cfg)
		if err != nil {
			return nil, err
		}
		if len(resp.Candidates) == 0 {
			return nil, ErrNoContentInResponse
		}
		return convertCandidates(modelName, resp.Candidates, resp.UsageMetadata)
	}
	return streamFromContents(ctx, client, modelName, contents, cfg, opts)
}

// streamFromContents handles streaming generation and returns a fully accumulated response.
func streamFromContents(
	ctx context.Context,
	client *genai.Client,
	modelName string,
	contents []*genai.Content,
	cfg *genai.GenerateContentConfig,
	opts *llms.CallOptions,
) (*llms.ContentResponse, error) {
	// Accumulate parts and usage from stream chunks
	accCandidate := &genai.Candidate{
		Content: &genai.Content{},
	}
	var lastUsage *genai.GenerateContentResponseUsageMetadata

	for resp, err := range client.Models.GenerateContentStream(ctx, modelName, contents, cfg) {
		if err != nil {
			return nil, fmt.Errorf("error in stream mode: %w", err)
		}

		if len(resp.Candidates) == 0 {
			// No candidates – likely blocked by safety filter; break gracefully.
			break
		}
		if len(resp.Candidates) != 1 {
			return nil, fmt.Errorf("expect single candidate in stream mode; got %v", len(resp.Candidates))
		}
		respCandidate := resp.Candidates[0]

		if respCandidate.Content == nil {
			break
		}

		accCandidate.Content.Parts = append(accCandidate.Content.Parts, respCandidate.Content.Parts...)
		accCandidate.Content.Role = respCandidate.Content.Role
		accCandidate.FinishReason = respCandidate.FinishReason
		accCandidate.SafetyRatings = respCandidate.SafetyRatings
		accCandidate.CitationMetadata = respCandidate.CitationMetadata
		accCandidate.TokenCount += respCandidate.TokenCount

		if resp.UsageMetadata != nil {
			lastUsage = resp.UsageMetadata
		}

		// Stream text tokens as they arrive
		for _, part := range respCandidate.Content.Parts {
			if part.Thought {
				continue
			}
			if part.Text != "" {
				if opts.StreamingFunc(ctx, []byte(part.Text)) != nil {
					goto done
				}
			}
		}
	}
done:
	return convertCandidates(modelName, []*genai.Candidate{accCandidate}, lastUsage)
}

// convertSchemaRecursive recursively converts a schema map to a genai.Schema
func convertSchemaRecursive(schemaMap map[string]any, toolIndex int, propertyPath string) (*genai.Schema, error) {
	schema := &genai.Schema{}

	if ty, ok := schemaMap["type"]; ok {
		tyString, ok := ty.(string)
		if !ok {
			return nil, fmt.Errorf("tool [%d], property [%s]: expected string for type", toolIndex, propertyPath)
		}
		schema.Type = convertToolSchemaType(tyString)
	}

	if desc, ok := schemaMap["description"]; ok {
		descString, ok := desc.(string)
		if !ok {
			return nil, fmt.Errorf("tool [%d], property [%s]: expected string for description", toolIndex, propertyPath)
		}
		schema.Description = descString
	}

	// Handle object properties recursively
	if properties, ok := schemaMap["properties"]; ok {
		propMap, ok := properties.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool [%d], property [%s]: expected map for properties", toolIndex, propertyPath)
		}

		schema.Properties = make(map[string]*genai.Schema)
		for propName, propValue := range propMap {
			valueMap, ok := propValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("tool [%d], property [%s.%s]: expect to find a value map", toolIndex, propertyPath, propName)
			}

			nestedPath := propName
			if propertyPath != "" {
				nestedPath = propertyPath + "." + propName
			}

			nestedSchema, err := convertSchemaRecursive(valueMap, toolIndex, nestedPath)
			if err != nil {
				return nil, err
			}
			schema.Properties[propName] = nestedSchema
		}
	} else if schema.Type == genai.TypeObject && propertyPath == "" {
		// For top-level object schemas without properties, this is an error
		return nil, fmt.Errorf("tool [%d]: expected to find a map of properties", toolIndex)
	}

	// Handle array items recursively
	if items, ok := schemaMap["items"]; ok && schema.Type == genai.TypeArray {
		itemMap, ok := items.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool [%d], property [%s]: expect to find a map for array items", toolIndex, propertyPath)
		}

		itemsPath := propertyPath + "[]"
		itemsSchema, err := convertSchemaRecursive(itemMap, toolIndex, itemsPath)
		if err != nil {
			return nil, err
		}
		schema.Items = itemsSchema
	}

	// Handle required fields
	if required, ok := schemaMap["required"]; ok {
		if rs, ok := required.([]string); ok {
			schema.Required = rs
		} else if ri, ok := required.([]any); ok {
			rs := make([]string, 0, len(ri))
			for _, r := range ri {
				rString, ok := r.(string)
				if !ok {
					return nil, fmt.Errorf("tool [%d], property [%s]: expected string for required", toolIndex, propertyPath)
				}
				rs = append(rs, rString)
			}
			schema.Required = rs
		} else {
			return nil, fmt.Errorf("tool [%d], property [%s]: expected array for required", toolIndex, propertyPath)
		}
	}

	return schema, nil
}

// convertTools converts from a list of langchaingo tools to a list of genai tools.
func convertTools(tools []llms.Tool) ([]*genai.Tool, error) {
	genaiFuncDecls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for i, tool := range tools {
		if tool.Type != "function" {
			return nil, fmt.Errorf("tool [%d]: unsupported type %q, want 'function'", i, tool.Type)
		}

		genaiFuncDecl := &genai.FunctionDeclaration{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
		}

		// Expect the Parameters field to be a map[string]any, from which we will
		// extract properties to populate the schema.
		params, ok := tool.Function.Parameters.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool [%d]: unsupported type %T of Parameters", i, tool.Function.Parameters)
		}

		schema, err := convertSchemaRecursive(params, i, "")
		if err != nil {
			return nil, err
		}
		genaiFuncDecl.Parameters = schema

		// google genai only support one tool, multiple tools must be embedded into function declarations:
		// https://github.com/GoogleCloudPlatform/generative-ai/issues/636
		// https://cloud.google.com/vertex-ai/generative-ai/docs/multimodal/function-calling#chat-samples
		genaiFuncDecls = append(genaiFuncDecls, genaiFuncDecl)
	}

	// Return nil if no tools are provided
	if len(genaiFuncDecls) == 0 {
		return nil, nil
	}

	genaiTools := []*genai.Tool{{FunctionDeclarations: genaiFuncDecls}}

	return genaiTools, nil
}

// convertToolSchemaType converts a tool's schema type from its langchaingo
// representation (string) to a genai enum.
func convertToolSchemaType(ty string) genai.Type {
	switch ty {
	case "object":
		return genai.TypeObject
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
	default:
		return genai.TypeUnspecified
	}
}

// showContent is a debugging helper for genai.Content.
func showContent(w io.Writer, cs []*genai.Content) {
	fmt.Fprintf(w, "Content (len=%v)\n", len(cs))
	for i, c := range cs {
		fmt.Fprintf(w, "[%d]: Role=%s\n", i, c.Role)
		for j, p := range c.Parts {
			fmt.Fprintf(w, "  Parts[%v]: ", j)
			switch {
			case p.Text != "":
				fmt.Fprintf(w, "Text %q\n", p.Text)
			case p.InlineData != nil:
				fmt.Fprintf(w, "Blob MIME=%q, size=%d\n", p.InlineData.MIMEType, len(p.InlineData.Data))
			case p.FunctionCall != nil:
				fmt.Fprintf(w, "FunctionCall Name=%v, Args=%v\n", p.FunctionCall.Name, p.FunctionCall.Args)
			case p.FunctionResponse != nil:
				fmt.Fprintf(w, "FunctionResponse Name=%v Response=%v\n", p.FunctionResponse.Name, p.FunctionResponse.Response)
			default:
				fmt.Fprintf(w, "unknown/empty part\n")
			}
		}
	}
}
