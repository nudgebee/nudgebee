package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// llmServerModel is the code-analysis-side adapter for llm-server's internal
// workspace generation API. Provider selection and all provider-specific
// credentials stay in llm-server; this process only transports the normalized
// message/tool contract required by the ReAct loop.
type llmServerModel struct {
	baseURL   string
	token     string
	accountID string
	client    *http.Client
}

type llmServerRequest struct {
	AccountID string `json:"account_id"`
	// AgentRunID, ModelTier and TaskType attribute the usage llm-server records
	// for this call; none of them changes how the call is generated.
	AgentRunID string             `json:"agent_run_id,omitempty"`
	ModelTier  string             `json:"model_tier,omitempty"`
	TaskType   string             `json:"task_type,omitempty"`
	Messages   []llmServerMessage `json:"messages"`
	Tools      []llms.Tool        `json:"tools,omitempty"`
}

// llmServerResponse is the generation result plus llm-server's receipt for the
// call's usage. llm-server writes the result as langchaingo marshals it; it is
// decoded into these types rather than langchaingo's, whose ToolCall decoder
// drops the function name and arguments.
type llmServerResponse struct {
	Choices     []llmServerChoice `json:"Choices"`
	UsageLinked bool              `json:"usage_linked"`
}

type llmServerChoice struct {
	Content          string
	StopReason       string
	GenerationInfo   map[string]any
	FuncCall         *llms.FunctionCall
	ToolCalls        []llmServerToolCall
	ReasoningContent string
}

// llmServerToolCall is one tool call in langchaingo's JSON envelope:
// {"type":"tool_call","tool_call":{"id":…,"type":…,"function":{…}}}.
type llmServerToolCall struct {
	ToolCall struct {
		ID       string             `json:"id"`
		Type     string             `json:"type"`
		Function *llms.FunctionCall `json:"function"`
	} `json:"tool_call"`
}

func (r llmServerResponse) contentResponse() *llms.ContentResponse {
	response := &llms.ContentResponse{Choices: make([]*llms.ContentChoice, 0, len(r.Choices))}
	for _, choice := range r.Choices {
		converted := &llms.ContentChoice{
			Content:          choice.Content,
			StopReason:       choice.StopReason,
			GenerationInfo:   choice.GenerationInfo,
			FuncCall:         choice.FuncCall,
			ReasoningContent: choice.ReasoningContent,
		}
		for _, call := range choice.ToolCalls {
			converted.ToolCalls = append(converted.ToolCalls, llms.ToolCall{
				ID:           call.ToolCall.ID,
				Type:         call.ToolCall.Type,
				FunctionCall: call.ToolCall.Function,
			})
		}
		response.Choices = append(response.Choices, converted)
	}
	return response
}

// agentRunIDKey types the context value carrying the llm-server agent run an
// analysis was dispatched for.
type agentRunIDKey struct{}

// WithAgentRunID tags ctx with the llm-server agent run an analysis was
// dispatched for. Model calls made through llm-server on this context return
// it, so llm-server records their usage against that run's conversation.
func WithAgentRunID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, agentRunIDKey{}, id)
}

// bridgeCall is what a Client tells the llm-server bridge about one call, and
// what the bridge reports back about it.
type bridgeCall struct {
	// tier is the calling client's model tier, for the usage row.
	tier string
	// usageLinked is set when llm-server recorded this call's usage against the
	// run's conversation; the client then records nothing for the call itself.
	usageLinked bool
}

type bridgeCallKey struct{}

// withBridgeCall tags ctx for one call of c, so that when the llm-server bridge
// is the model behind c it can label the call and report whether it recorded
// the usage. A linked call must not also be recorded here: llm-server holds the
// provider's real counts, and a second record would count the call twice.
func (c *Client) withBridgeCall(ctx context.Context) (context.Context, *bridgeCall) {
	if ctx == nil {
		ctx = context.Background()
	}
	call := &bridgeCall{tier: c.tier}
	return context.WithValue(ctx, bridgeCallKey{}, call), call
}

type llmServerMessage struct {
	Role  string          `json:"role"`
	Parts []llmServerPart `json:"parts"`
}

type llmServerPart struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	ToolCall     *llms.ToolCall         `json:"tool_call,omitempty"`
	ToolResponse *llms.ToolCallResponse `json:"tool_response,omitempty"`
}

func newLLMServerModel() (*llmServerModel, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("NB_LLM_SERVER_URL")), "/")
	token := strings.TrimSpace(os.Getenv("NB_WORKSPACE_TOKEN"))
	accountID := strings.TrimSpace(os.Getenv("NB_ACCOUNT_ID"))
	if baseURL == "" || token == "" || accountID == "" {
		return nil, fmt.Errorf("NB_LLM_SERVER_URL, NB_WORKSPACE_TOKEN and NB_ACCOUNT_ID are required")
	}
	return &llmServerModel{baseURL: baseURL, token: token, accountID: accountID, client: &http.Client{}}, nil
}

func (m *llmServerModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	request := llmServerRequest{AccountID: m.accountID, TaskType: callPhaseFromContext(ctx), Messages: make([]llmServerMessage, 0, len(messages))}
	request.AgentRunID, _ = ctx.Value(agentRunIDKey{}).(string)
	call, _ := ctx.Value(bridgeCallKey{}).(*bridgeCall)
	if call != nil {
		request.ModelTier = call.tier
	}
	for _, message := range messages {
		wire := llmServerMessage{Role: string(message.Role), Parts: make([]llmServerPart, 0, len(message.Parts))}
		for _, part := range message.Parts {
			switch value := part.(type) {
			case llms.TextContent:
				wire.Parts = append(wire.Parts, llmServerPart{Type: "text", Text: value.Text})
			case llms.ToolCall:
				call := value
				wire.Parts = append(wire.Parts, llmServerPart{Type: "tool_call", ToolCall: &call})
			case llms.ToolCallResponse:
				response := value
				wire.Parts = append(wire.Parts, llmServerPart{Type: "tool_response", ToolResponse: &response})
			default:
				return nil, fmt.Errorf("unsupported message part type %T", part)
			}
		}
		request.Messages = append(request.Messages, wire)
	}
	for _, option := range options {
		var config llms.CallOptions
		option(&config)
		if len(config.Tools) > 0 {
			request.Tools = config.Tools
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal llm-server request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/api/v1/workspace/llm/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create llm-server request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Token", m.token)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm-server request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read llm-server response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm-server returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var response llmServerResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode llm-server response: %w", err)
	}
	if call != nil {
		call.usageLinked = response.UsageLinked
	}
	return response.contentResponse(), nil
}

func (m *llmServerModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	response, err := m.GenerateContent(ctx, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}, options...)
	if err != nil {
		return "", err
	}
	if response == nil || len(response.Choices) == 0 {
		return "", fmt.Errorf("llm-server returned no choices")
	}
	return response.Choices[0].Content, nil
}
