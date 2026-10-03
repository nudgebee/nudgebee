package api

import (
	"fmt"
	"net/http"

	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// workspaceLLMRequest is deliberately provider-neutral. Workspace processes
// send the same message/tool representation regardless of the configured
// provider; llm-server owns provider resolution, caching, retries and usage.
type workspaceLLMRequest struct {
	AccountID      string                `json:"account_id"`
	UserID         string                `json:"user_id"`
	ConversationID string                `json:"conversation_id"`
	MessageID      string                `json:"message_id"`
	Messages       []workspaceLLMMessage `json:"messages"`
	Tools          []workspaceLLMTool    `json:"tools,omitempty"`
}

type workspaceLLMMessage struct {
	Role  string                    `json:"role"`
	Parts []workspaceLLMMessagePart `json:"parts"`
}

type workspaceLLMMessagePart struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	ToolCall     *llms.ToolCall         `json:"tool_call,omitempty"`
	ToolResponse *llms.ToolCallResponse `json:"tool_response,omitempty"`
}

type workspaceLLMTool struct {
	Type     string               `json:"type"`
	Function workspaceLLMFunction `json:"function"`
}

type workspaceLLMFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

func handleWorkspaceLLM(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	r.Group("/api/v1/workspace").POST("/llm/generate", func(c *gin.Context) {
		var req workspaceLLMRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.AccountID == "" || len(req.Messages) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "account_id and messages are required"})
			return
		}

		ctx, err := authorizeWorkspaceRequest(c, req.AccountID, tracer, meter)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}

		messages := make([]llms.MessageContent, 0, len(req.Messages))
		for _, message := range req.Messages {
			parts := make([]llms.ContentPart, 0, len(message.Parts))
			for _, part := range message.Parts {
				switch part.Type {
				case "text":
					parts = append(parts, llms.TextPart(part.Text))
				case "tool_call":
					if part.ToolCall == nil {
						returnBadWorkspaceLLM(c, "tool_call part is missing tool_call")
						return
					}
					parts = append(parts, *part.ToolCall)
				case "tool_response":
					if part.ToolResponse == nil {
						returnBadWorkspaceLLM(c, "tool_response part is missing tool_response")
						return
					}
					parts = append(parts, *part.ToolResponse)
				default:
					returnBadWorkspaceLLM(c, fmt.Sprintf("unsupported message part type %q", part.Type))
					return
				}
			}
			messages = append(messages, llms.MessageContent{Role: llms.ChatMessageType(message.Role), Parts: parts})
		}

		tools := make([]llms.Tool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, llms.Tool{Type: tool.Type, Function: &llms.FunctionDefinition{
				Name: tool.Function.Name, Description: tool.Function.Description, Parameters: tool.Function.Parameters,
			}})
		}
		options := make([]llms.CallOption, 0, 1)
		if len(tools) > 0 {
			options = append(options, llms.WithTools(tools))
		}
		response, err := core.GenerateAndTrackLLMContent(ctx, req.UserID, req.AccountID, req.ConversationID, req.MessageID, agents.AgentCodeAnalyzer, false, messages, false, options...)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, response)
	})
}

func returnBadWorkspaceLLM(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": message})
}
