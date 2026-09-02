package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/tmc/langchaingo/llms"
)

// bedrockConverseLLM talks to Bedrock through the Converse API instead of the
// legacy InvokeModel API that langchaingo's bedrock package uses.
//
// This exists because langchaingo v0.1.12 supports no tool calling on Bedrock
// at all: none of its per-family request builders (meta, anthropic, amazon,
// cohere, ai21) reference tools, so llms.WithTools is silently dropped and the
// model can never emit a tool call. For an agent whose entire job is to call
// file_view/grep and read code, that is fatal — the planner nudges for a tool
// call every step, reads nothing, and abstains with "insufficient evidence"
// while every API call reports success.
//
// Converse is Bedrock's unified API: one request shape for every model family,
// with first-class tool support (ToolConfiguration in, ToolUse blocks out) and a
// normalized InferenceConfiguration. Using it also sidesteps the per-family body
// schemas — notably Llama's max_gen_len, which is capped at 8192 and rejects
// anything larger outright rather than clamping.
//
// llm-server reaches Bedrock the same way (llm/llm-server/llms/bedrock), which
// is why tool calling works there and not here.
type bedrockConverseLLM struct {
	client  *bedrockruntime.Client
	modelID string
}

var _ llms.Model = (*bedrockConverseLLM)(nil)

func newBedrockConverseLLM(client *bedrockruntime.Client, modelID string) *bedrockConverseLLM {
	return &bedrockConverseLLM{client: client, modelID: modelID}
}

// Call implements the single-prompt convenience half of llms.Model.
func (b *bedrockConverseLLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	resp, err := b.GenerateContent(ctx,
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}, options...)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	return resp.Choices[0].Content, nil
}

func (b *bedrockConverseLLM) GenerateContent(
	ctx context.Context,
	messages []llms.MessageContent,
	options ...llms.CallOption,
) (*llms.ContentResponse, error) {
	opts := llms.CallOptions{}
	for _, o := range options {
		o(&opts)
	}

	converseMessages, systemBlocks, err := buildConverseMessages(messages)
	if err != nil {
		return nil, err
	}
	// Converse requires at least one message; a request carrying only system or
	// empty content would come back as an opaque 400. Say what is actually wrong.
	if len(converseMessages) == 0 {
		return nil, fmt.Errorf("bedrock converse (model=%s): no non-system message to send", b.modelID)
	}

	inference := &types.InferenceConfiguration{}
	if opts.Temperature > 0 {
		inference.Temperature = aws.Float32(float32(opts.Temperature))
	}
	if len(opts.StopWords) > 0 {
		inference.StopSequences = opts.StopWords
	}
	// MaxTokens is forwarded only when the caller asked for one. Converse
	// normalizes it per model, so an omitted value means "use the model's own
	// default" rather than the tiny per-family fallback InvokeModel substitutes.
	if opts.MaxTokens > 0 {
		inference.MaxTokens = aws.Int32(int32(opts.MaxTokens))
	}

	input := &bedrockruntime.ConverseInput{
		ModelId:         aws.String(b.modelID),
		Messages:        converseMessages,
		System:          systemBlocks,
		InferenceConfig: inference,
	}

	if toolConfig := buildConverseToolConfig(opts.Tools); toolConfig != nil {
		input.ToolConfig = toolConfig
	}

	resp, err := b.client.Converse(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock converse (model=%s): %w", b.modelID, err)
	}

	return parseConverseOutput(resp)
}

// buildConverseToolConfig maps langchaingo tool definitions onto Converse's tool
// specification. Returns nil when there are no usable tools — Converse rejects
// an empty ToolConfig block.
func buildConverseToolConfig(tools []llms.Tool) *types.ToolConfiguration {
	cfg := types.ToolConfiguration{}
	for _, t := range tools {
		if t.Function == nil || t.Function.Name == "" {
			continue
		}
		params := t.Function.Parameters
		if params == nil {
			// Converse requires an input schema; an empty object is the valid
			// way to say "this tool takes no arguments".
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		spec := types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(t.Function.Name),
				Description: aws.String(t.Function.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: document.NewLazyDocument(params),
				},
			},
		}
		cfg.Tools = append(cfg.Tools, &spec)
	}
	if len(cfg.Tools) == 0 {
		return nil
	}
	return &cfg
}

// buildConverseMessages converts langchaingo messages into Converse's message
// and system-block slices.
//
// Two Converse constraints drive the shape here: system blocks travel in their
// own field rather than as a message, and consecutive messages of the same role
// are rejected, so same-role runs are coalesced into one message. Empty text is
// dropped throughout — Converse rejects zero-length blocks with a
// ValidationException.
func buildConverseMessages(messages []llms.MessageContent) ([]types.Message, []types.SystemContentBlock, error) {
	var converse []types.Message
	var system []types.SystemContentBlock

	isToolResult := func(b types.ContentBlock) bool {
		_, ok := b.(*types.ContentBlockMemberToolResult)
		return ok
	}
	// A turn holds tool results or ordinary content, never both — Bedrock rejects
	// the mix with "Conversation blocks and tool result blocks cannot be provided
	// in the same turn" — and turns must alternate roles, so a same-role run
	// cannot simply be split into two. Text that lands on a tool-result turn is
	// therefore folded into the last tool result's own content, which keeps the
	// turn pure without dropping what the planner said.
	appendBlock := func(role types.ConversationRole, block types.ContentBlock) {
		n := len(converse)
		if n == 0 || converse[n-1].Role != role {
			converse = append(converse, types.Message{Role: role, Content: []types.ContentBlock{block}})
			return
		}
		last := converse[n-1].Content
		if len(last) == 0 || isToolResult(last[len(last)-1]) == isToolResult(block) {
			converse[n-1].Content = append(converse[n-1].Content, block)
			return
		}
		if text, ok := block.(*types.ContentBlockMemberText); ok {
			if prior, isTR := last[len(last)-1].(*types.ContentBlockMemberToolResult); isTR {
				prior.Value.Content = append(prior.Value.Content,
					&types.ToolResultContentBlockMemberText{Value: text.Value})
				return
			}
		}
		if tr, ok := block.(*types.ContentBlockMemberToolResult); ok {
			// The mirror case: ordinary blocks are already in this turn and a tool
			// result now has to join them. Fold every one of them into the tool
			// result, not merely the last — leaving an earlier text block behind
			// would still produce the mixed turn this whole branch exists to avoid.
			var folded []types.ToolResultContentBlock
			var kept []types.ContentBlock
			for _, b := range last {
				if t, isText := b.(*types.ContentBlockMemberText); isText {
					folded = append(folded, &types.ToolResultContentBlockMemberText{Value: t.Value})
					continue
				}
				kept = append(kept, b)
			}
			tr.Value.Content = append(folded, tr.Value.Content...)
			converse[n-1].Content = append(kept, tr)
			return
		}
		converse = append(converse, types.Message{Role: role, Content: []types.ContentBlock{block}})
	}

	for _, m := range messages {
		for _, part := range m.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				if strings.TrimSpace(p.Text) == "" {
					continue
				}
				if m.Role == llms.ChatMessageTypeSystem {
					system = append(system, &types.SystemContentBlockMemberText{Value: p.Text})
					continue
				}
				appendBlock(conversationRoleFor(m.Role), &types.ContentBlockMemberText{Value: p.Text})

			case llms.ToolCall:
				if p.FunctionCall == nil {
					continue
				}
				args := map[string]any{}
				if p.FunctionCall.Arguments != "" {
					if err := json.Unmarshal([]byte(p.FunctionCall.Arguments), &args); err != nil {
						return nil, nil, fmt.Errorf("bedrock converse: tool call %q has unparseable arguments: %w",
							p.FunctionCall.Name, err)
					}
				}
				// A tool call is something the assistant said, regardless of which
				// role the caller attached it to.
				appendBlock(types.ConversationRoleAssistant, &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						Name:      aws.String(p.FunctionCall.Name),
						ToolUseId: aws.String(p.ID),
						Input:     document.NewLazyDocument(args),
					},
				})

			case llms.ToolCallResponse:
				// Converse rejects zero-length text, so a tool that legitimately
				// produced no output (an empty grep, a silent command) would take
				// the whole request down with a ValidationException. Say so
				// explicitly instead: "" is a real result the model should see,
				// and a placeholder that describes it beats one that claims
				// success.
				content := p.Content
				if strings.TrimSpace(content) == "" {
					content = "(no output)"
				}
				// Tool results are delivered as a user-role block; that is the
				// Converse contract, not a role we get to choose.
				appendBlock(types.ConversationRoleUser, &types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						Status:    types.ToolResultStatusSuccess,
						ToolUseId: aws.String(p.ToolCallID),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: content},
						},
					},
				})
			}
		}
	}

	// Converse requires the exchange to begin with a user turn.
	for len(converse) > 0 && converse[0].Role == types.ConversationRoleAssistant {
		converse = converse[1:]
	}

	return converse, system, nil
}

func conversationRoleFor(role llms.ChatMessageType) types.ConversationRole {
	if role == llms.ChatMessageTypeAI {
		return types.ConversationRoleAssistant
	}
	return types.ConversationRoleUser
}

func parseConverseOutput(resp *bedrockruntime.ConverseOutput) (*llms.ContentResponse, error) {
	if resp == nil {
		return nil, fmt.Errorf("bedrock converse: nil response with no error")
	}
	outputMessage, ok := resp.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return nil, fmt.Errorf("bedrock converse: unexpected output type %T", resp.Output)
	}

	var text strings.Builder
	toolCalls := []llms.ToolCall{}
	for _, c := range outputMessage.Value.Content {
		switch block := c.(type) {
		case *types.ContentBlockMemberText:
			text.WriteString(block.Value)
		case *types.ContentBlockMemberToolUse:
			// Input is a document.Interface, so a tool invoked with no arguments
			// can arrive nil — calling MarshalSmithyDocument on it would panic.
			// An argument-less call is legitimate, so render it as an empty object
			// rather than failing the whole response over it.
			args := []byte("{}")
			if block.Value.Input != nil {
				marshaled, err := block.Value.Input.MarshalSmithyDocument()
				if err != nil {
					return nil, fmt.Errorf("bedrock converse: unable to marshal tool arguments for %q: %w",
						aws.ToString(block.Value.Name), err)
				}
				args = marshaled
			}
			toolCalls = append(toolCalls, llms.ToolCall{
				ID:   aws.ToString(block.Value.ToolUseId),
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      aws.ToString(block.Value.Name),
					Arguments: string(args),
				},
			})
		}
	}

	generationInfo := map[string]any{}
	if resp.Usage != nil {
		generationInfo["input_tokens"] = int(aws.ToInt32(resp.Usage.InputTokens))
		generationInfo["output_tokens"] = int(aws.ToInt32(resp.Usage.OutputTokens))
	}

	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			Content:        strings.TrimSpace(text.String()),
			StopReason:     string(resp.StopReason),
			GenerationInfo: generationInfo,
			ToolCalls:      toolCalls,
		}},
	}, nil
}
