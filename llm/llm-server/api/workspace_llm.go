package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// workspaceLLMRequest is deliberately provider-neutral. Workspace processes
// send the same message/tool representation regardless of the configured
// provider; llm-server owns provider resolution, caching, retries and usage.
type workspaceLLMRequest struct {
	AccountID string `json:"account_id"`
	// AgentRunID is the agent run llm-server named when it dispatched the
	// analysis; the workspace returns it so the call's usage is recorded against
	// that run's conversation. It is the only attribution a workspace supplies.
	AgentRunID string `json:"agent_run_id,omitempty"`
	// ModelTier and TaskType label the usage row; they do not select the model.
	ModelTier string                `json:"model_tier,omitempty"`
	TaskType  string                `json:"task_type,omitempty"`
	Messages  []workspaceLLMMessage `json:"messages"`
	Tools     []workspaceLLMTool    `json:"tools,omitempty"`
}

// workspaceLLMResponse is the generation result plus a receipt for its usage.
type workspaceLLMResponse struct {
	*llms.ContentResponse
	// UsageLinked reports that this call's usage is recorded against the
	// conversation of the agent run the workspace named, so the workspace must
	// not report its own estimate for the call.
	UsageLinked bool `json:"usage_linked,omitempty"`
}

type workspaceLLMMessage struct {
	Role  string                    `json:"role"`
	Parts []workspaceLLMMessagePart `json:"parts"`
}

type workspaceLLMMessagePart struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	ToolCall     *workspaceLLMToolCall  `json:"tool_call,omitempty"`
	ToolResponse *llms.ToolCallResponse `json:"tool_response,omitempty"`
}

// workspaceLLMToolCall is a tool call as a workspace sends it, in langchaingo's
// JSON envelope: {"type":"tool_call","tool_call":{"id":…,"type":…,"function":{…}}}.
// It is decoded here rather than by langchaingo, whose ToolCall decoder drops
// the function name and arguments.
type workspaceLLMToolCall struct {
	ToolCall struct {
		ID       string             `json:"id"`
		Type     string             `json:"type"`
		Function *llms.FunctionCall `json:"function"`
	} `json:"tool_call"`
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

		messages, err := workspaceLLMMessages(req.Messages)
		if err != nil {
			returnBadWorkspaceLLM(c, err.Error())
			return
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
		link, userID, linked := workspaceUsageLink(ctx, req)
		if linked {
			ctx = core.WithUsageLink(ctx, link)
		}
		response, err := core.GenerateAndTrackLLMContent(ctx, userID, req.AccountID, "", "", agents.AgentCodeAnalyzer, false, messages, false, options...)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, workspaceLLMResponse{ContentResponse: response, UsageLinked: linked})
	})
}

// workspaceLLMMessages converts a workspace's messages into the form the
// provider clients take. A workspace sends all tool results of one step in a
// single tool message; each result becomes its own tool message here, the
// layout llm-server's own planner sends, because the OpenAI-compatible clients
// accept exactly one result per tool message.
func workspaceLLMMessages(wire []workspaceLLMMessage) ([]llms.MessageContent, error) {
	messages := make([]llms.MessageContent, 0, len(wire))
	for _, message := range wire {
		role := llms.ChatMessageType(message.Role)
		parts := make([]llms.ContentPart, 0, len(message.Parts))
		for _, part := range message.Parts {
			switch part.Type {
			case "text":
				parts = append(parts, llms.TextPart(part.Text))
			case "tool_call":
				if part.ToolCall == nil || part.ToolCall.ToolCall.Function == nil {
					return nil, errors.New("tool_call part is missing its function")
				}
				call := part.ToolCall.ToolCall
				parts = append(parts, llms.ToolCall{ID: call.ID, Type: call.Type, FunctionCall: call.Function})
			case "tool_response":
				if part.ToolResponse == nil {
					return nil, errors.New("tool_response part is missing tool_response")
				}
				parts = append(parts, *part.ToolResponse)
			default:
				return nil, fmt.Errorf("unsupported message part type %q", part.Type)
			}
		}
		if role == llms.ChatMessageTypeTool && len(parts) > 1 {
			for _, part := range parts {
				messages = append(messages, llms.MessageContent{Role: role, Parts: []llms.ContentPart{part}})
			}
			continue
		}
		messages = append(messages, llms.MessageContent{Role: role, Parts: parts})
	}
	return messages, nil
}

// workspaceUsageLink resolves the agent run a workspace names into the usage
// attribution for its call. The conversation, message and user are read from
// llm-server's own row for that run, within the account the workspace token is
// bound to, so a workspace cannot attribute usage to a conversation that is not
// its own. A run that does not resolve leaves the call unlinked.
func workspaceUsageLink(ctx *security.RequestContext, req workspaceLLMRequest) (core.UsageLink, string, bool) {
	if req.AgentRunID == "" {
		return core.UsageLink{}, "", false
	}
	if _, err := uuid.Parse(req.AgentRunID); err != nil {
		ctx.GetLogger().Warn("workspace: llm usage not linked, agent run id is not a uuid", "account_id", req.AccountID, "agent_run_id", req.AgentRunID)
		return core.UsageLink{}, "", false
	}
	dao := core.GetConversationDao()
	if dao == nil {
		ctx.GetLogger().Warn("workspace: llm usage not linked, conversation DAO unavailable", "account_id", req.AccountID, "agent_run_id", req.AgentRunID)
		return core.UsageLink{}, "", false
	}
	conversationID, messageID, userID, err := dao.GetAgentRunAttribution(req.AgentRunID, req.AccountID, agents.AgentCodeAnalyzer)
	if err != nil {
		ctx.GetLogger().Warn("workspace: llm usage not linked, failed to get agent run attribution", "account_id", req.AccountID, "agent_run_id", req.AgentRunID, "error", err)
		return core.UsageLink{}, "", false
	}
	return core.UsageLink{
		ConversationID: conversationID,
		MessageID:      messageID,
		AgentID:        req.AgentRunID,
		ModelTier:      req.ModelTier,
		TaskType:       req.TaskType,
	}, userID, true
}

func returnBadWorkspaceLLM(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": message})
}
