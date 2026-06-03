package bedrockclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/tmc/langchaingo/llms"
)

func createMetaCompletion(ctx context.Context,
	client *bedrockruntime.Client,
	modelID string,
	messages []Message,
	options llms.CallOptions,
) (*llms.ContentResponse, error) {

	bedRockMessages, bedrockSystemMessage, err := buildConverseMessages(messages)
	if err != nil {
		return nil, err
	}

	modelInput := &bedrockruntime.ConverseInput{
		ModelId:    aws.String(modelID),
		Messages:   bedRockMessages,
		System:     bedrockSystemMessage,
		ToolConfig: nil,
		InferenceConfig: &types.InferenceConfiguration{
			Temperature:   aws.Float32(float32(options.Temperature)),
			StopSequences: options.StopWords,
		},
	}

	if len(options.Tools) > 0 {
		bedrockToolsConfiguration := types.ToolConfiguration{}
		for _, f := range options.Tools {
			if f.Function.Name == "" {
				continue
			}
			bedRockTool := types.ToolMemberToolSpec{
				Value: types.ToolSpecification{
					Name:        &f.Function.Name,
					Description: &f.Function.Description,
					InputSchema: &types.ToolInputSchemaMemberJson{
						Value: document.NewLazyDocument(f.Function.Parameters),
					},
				},
			}
			bedrockToolsConfiguration.Tools = append(bedrockToolsConfiguration.Tools, &bedRockTool)
		}
		modelInput.ToolConfig = &bedrockToolsConfiguration
	}

	//filter first message if its assistent message
	for len(bedRockMessages) > 0 && bedRockMessages[0].Role == types.ConversationRoleAssistant {
		bedRockMessages = slices.Delete(bedRockMessages, 0, 1)
	}

	var resp *bedrockruntime.ConverseOutput
	backoff := time.Duration(config.Config.LlmServerLlmInitialBackoffSeconds) * time.Second
	startTime := time.Now()
	maxRetryDuration := time.Duration(config.Config.LlmServerGlobalRetryBudgetMinutes) * time.Minute
	individualTimeout := time.Duration(config.Config.LlmServerMaxIndividualCallTimeoutMinutes) * time.Minute

	for time.Since(startTime) < maxRetryDuration {
		// Create a per-call context with timeout
		callCtx, cancel := context.WithTimeout(ctx, individualTimeout)
		resp, err = client.Converse(callCtx, modelInput)
		cancel()

		if err == nil {
			break
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "ServiceUnavailableException":
				slog.Error("service unavailable, please try again later", "error", err)
				return nil, fmt.Errorf("service unavailable, please try again later")
			case "ModelNotReadyException":
				slog.Warn("Service unavailable, retrying...", "elapsed", time.Since(startTime), "error", err)
				time.Sleep(backoff)
				backoff *= 2
				continue
			case "ModelTimeoutException":
				slog.Warn("Model timeout, retrying...", "elapsed", time.Since(startTime), "error", err)
				time.Sleep(backoff)
				backoff *= 2
				continue
			case "ModelThrottledException":
				slog.Warn("Model throttled, retrying...", "elapsed", time.Since(startTime), "error", err)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
		}

		slog.Error("Non-retryable error encountered", "error", err)
		return nil, err
	}

	// if during retry time expires, then next retry wont happen..
	if err != nil {
		return nil, err
	}

	outputMessage, ok := resp.Output.(*types.ConverseOutputMemberMessage)

	var outputMessageStr = ""
	toolCalls := []llms.ToolCall{}
	if ok {
		for _, c := range outputMessage.Value.Content {
			switch c1 := c.(type) {
			case *types.ContentBlockMemberText:
				outputMessageStr = outputMessageStr + " " + c1.Value
			case *types.ContentBlockMemberToolUse:
				args, err := c1.Value.Input.MarshalSmithyDocument()
				if err != nil {
					return nil, fmt.Errorf("unable to handl command args - %v", c1.Value.Input)
				}
				toolCall := llms.ToolCall{
					ID:   *c1.Value.ToolUseId,
					Type: *c1.Value.Name,
					FunctionCall: &llms.FunctionCall{
						Name:      *c1.Value.Name,
						Arguments: string(args),
					},
				}
				toolCalls = append(toolCalls, toolCall)
			default:
				return nil, fmt.Errorf("cannot handle output message type - %v", c1)
			}
		}
	}

	outputMessageStr = strings.TrimSpace(outputMessageStr)

	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{
				Content:    outputMessageStr,
				StopReason: string(resp.StopReason),
				GenerationInfo: map[string]any{
					"input_tokens":  resp.Usage.InputTokens,
					"output_tokens": resp.Usage.OutputTokens,
				},
				ToolCalls: toolCalls,
			},
		},
	}, nil
}

// buildConverseMessages converts internal Message records into the message
// and system-block slices expected by Bedrock's Converse API. Empty/whitespace
// content is skipped for every role — the Converse API rejects empty system
// blocks with a 400 ValidationException, and back-to-back human/AI messages
// must be coalesced.
func buildConverseMessages(messages []Message) ([]types.Message, []types.SystemContentBlock, error) {
	bedRockMessages := []types.Message{}
	bedrockSystemMessage := []types.SystemContentBlock{}

	for i, m := range messages {
		switch m.Role {
		case llms.ChatMessageTypeHuman:
			if m.Content == "" {
				continue
			}
			textBlock := types.ContentBlockMemberText{
				Value: m.Content,
			}
			// bedrock doesnt allow 2 human/ai messages consicutevely
			if i > 0 && len(bedRockMessages) > 0 && bedRockMessages[len(bedRockMessages)-1].Role == types.ConversationRoleUser {
				bedRockMessages[len(bedRockMessages)-1].Content = append(bedRockMessages[len(bedRockMessages)-1].Content, &textBlock)
			} else {
				bedRockMessages = append(bedRockMessages, types.Message{
					Role:    types.ConversationRoleUser,
					Content: []types.ContentBlock{&textBlock},
				})
			}
		case llms.ChatMessageTypeAI:
			if m.Content == "" {
				continue
			}
			textBlock := types.ContentBlockMemberText{
				Value: m.Content,
			}

			// bedrock doesnt allow 2 human/ai messages consicutevely
			if i > 0 && len(bedRockMessages) > 0 && bedRockMessages[len(bedRockMessages)-1].Role == types.ConversationRoleAssistant {
				bedRockMessages[len(bedRockMessages)-1].Content = append(bedRockMessages[len(bedRockMessages)-1].Content, &textBlock)
			} else {
				bedRockMessages = append(bedRockMessages, types.Message{
					Role:    types.ConversationRoleAssistant,
					Content: []types.ContentBlock{&textBlock},
				})
			}
		case llms.ChatMessageTypeTool:
			switch m.Type {
			case "tool_call_response":
				toolCallResponseMap := map[string]any{}
				err := common.UnmarshalJson([]byte(m.Content), &toolCallResponseMap)
				if err != nil {
					return nil, nil, err
				}
				// Use comma-ok assertions: payload originates from the LLM and may be
				// missing fields or have wrong types. Fall back to empty strings rather
				// than panic; downstream Bedrock will surface a clearer error if the
				// tool_use_id is empty.
				toolCallID, _ := toolCallResponseMap["tool_call_id"].(string)
				toolName, _ := toolCallResponseMap["name"].(string)
				toolContent, _ := toolCallResponseMap["content"].(string)
				toolCallResponse := llms.ToolCallResponse{
					ToolCallID: toolCallID,
					Name:       toolName,
					Content:    toolContent,
				}
				textBlock := types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						Status: types.ToolResultStatusSuccess,
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{
								Value: toolCallResponse.Content,
							},
						},
						ToolUseId: &toolCallResponse.ToolCallID,
					},
				}
				bedRockMessages = append(bedRockMessages, types.Message{
					Role:    types.ConversationRoleUser,
					Content: []types.ContentBlock{&textBlock},
				})

			case "tool_call":
				toolCallMap := map[string]any{}
				err := common.UnmarshalJson([]byte(m.Content), &toolCallMap)
				if err != nil {
					return nil, nil, err
				}
				// `function` is required for a valid tool call; bail loudly if it is
				// missing or the wrong shape rather than panicking on the assertion.
				toolCallFunctionMap, ok := toolCallMap["function"].(map[string]any)
				if !ok {
					slog.Error("provider_meta: tool_call missing or invalid `function` block, skipping", "data", m.Content)
					continue
				}
				// Comma-ok assertions for every field — payload is LLM-controlled.
				toolCallID, _ := toolCallMap["id"].(string)
				toolCallType, _ := toolCallMap["type"].(string)
				toolName, _ := toolCallFunctionMap["name"].(string)
				toolArgs, _ := toolCallFunctionMap["arguments"].(string)
				toolCall := llms.ToolCall{
					ID:   toolCallID,
					Type: toolCallType,
					FunctionCall: &llms.FunctionCall{
						Name:      toolName,
						Arguments: toolArgs,
					},
				}

				inputMap := map[string]any{}
				if toolCall.FunctionCall.Arguments != "" {
					if err := common.UnmarshalJson([]byte(toolCall.FunctionCall.Arguments), &inputMap); err != nil {
						slog.Error("unable to parse tool args", "error", err, "data", toolCall.FunctionCall.Arguments)
					}
				}

				textBlock := types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						Name:      &toolCall.FunctionCall.Name,
						ToolUseId: &toolCall.ID,
						Input:     document.NewLazyDocument(inputMap),
					},
				}
				bedRockMessages = append(bedRockMessages, types.Message{
					Role:    types.ConversationRoleAssistant,
					Content: []types.ContentBlock{&textBlock},
				})

			}
		case llms.ChatMessageTypeSystem:
			// Bedrock Converse rejects system blocks with empty text
			// (ValidationException: "Member must have length greater than or equal to 1").
			// Skip empty/whitespace-only blocks defensively — symmetric with the
			// Human/AI handlers above.
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			bedrockSystemMessage = append(bedrockSystemMessage, &types.SystemContentBlockMemberText{
				Value: m.Content,
			})
		default:
			slog.Error("unable to handle message type", "type", m.Role)
		}
	}

	return bedRockMessages, bedrockSystemMessage, nil
}
