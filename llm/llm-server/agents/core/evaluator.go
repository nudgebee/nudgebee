package core

import (
	"context"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"time"

	"nudgebee/llm/agents/prompts_repo"

	"github.com/tmc/langchaingo/llms"
)

// Evaluation metrics for query/response/followup
type QueryResponseEvaluation struct {
	Correctness  float64 `json:"correctness"`
	Relevance    float64 `json:"relevance"`
	Completeness float64 `json:"completeness"`
	Helpfulness  float64 `json:"helpfulness"`
	Feedback     string  `json:"feedback"`
}

// Evaluation metrics for each tool call (stepResponse)
type ToolCallEvaluation struct {
	SelectionAccuracy float64 `json:"selection_accuracy"`
	UsageCorrectness  float64 `json:"usage_correctness"`
	OutputRelevance   float64 `json:"output_relevance"`
	ErrorRate         float64 `json:"error_rate"`
	Feedback          string  `json:"feedback"`
}

type AgentResponseEvaluationResult struct {
	QueryResponseMetrics QueryResponseEvaluation `json:"query_response_metrics"`
	ToolCallMetric       ToolCallEvaluation      `json:"tool_call_metric"`
}

// Evaluates an NBAgentResponse using LLM for both main response and each tool call
func EvaluateAgentResponse(ctx *security.RequestContext, agentResponse NBAgentResponse, accountId string, availableTools []toolcore.NBTool, userId string) (AgentResponseEvaluationResult, error) {
	// Response evaluation is a reasoning-heavy sub-operation; tag the context so
	// its LLM calls resolve the Reasoning-tier model regardless of the host agent.
	evalCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), ContextKeyModelTier, ModelTierReasoning),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	// Get prompts from prompt_repo
	systemPrompt := prompts_repo.GetPrompt(prompts_repo.PromptEvaluatorSystem)
	mainPrompt := prompts_repo.GetPrompt(prompts_repo.PromptEvaluatorQueryResponse, agentResponse.Query, agentResponse.Response)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, mainPrompt),
	}

	completion, err := GenerateAndTrackLLMContent(evalCtx, userId, accountId, agentResponse.ConversationId, agentResponse.MessageId, "evaluation_agent", false, messageContent, true)
	if err != nil {
		return AgentResponseEvaluationResult{}, err
	}

	var mainEval QueryResponseEvaluation
	err = common.ExtractAndUnmarshalJSON([]byte(completion.Choices[0].Content), &mainEval)
	if err != nil {
		return AgentResponseEvaluationResult{}, err
	}

	var toolEval ToolCallEvaluation
	if len(agentResponse.AgentStepResponse) > 0 {
		var toolDetails []string
		for _, step := range agentResponse.AgentStepResponse {
			toolDetails = append(toolDetails, fmt.Sprintf("ToolName: %s\nInput: %v\nLog: %s", step.Response.Name, step.Call.FunctionCall.Arguments, step.Log))
		}
		allToolsInfo := strings.Join(toolDetails, "\n---\n")
		// Convert NBTool slice to comma-separated tool names with descriptions
		availableToolInfos := make([]string, len(availableTools))
		for i, t := range availableTools {
			availableToolInfos[i] = fmt.Sprintf("%s (%s)", t.Name(), t.Description())
		}
		availableToolsInfo := strings.Join(availableToolInfos, "\n---\n")

		// Get tool evaluation prompts from prompt_repo
		toolSystemPrompt := prompts_repo.GetPrompt(prompts_repo.PromptEvaluatorSystem)
		toolPrompt := prompts_repo.GetPrompt(prompts_repo.PromptEvaluatorToolCalls, agentResponse.Query, allToolsInfo, availableToolsInfo)

		toolMsg := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, toolSystemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, toolPrompt),
		}
		toolCompletion, err := GenerateAndTrackLLMContent(evalCtx, userId, accountId, agentResponse.ConversationId, agentResponse.MessageId, "evaluation_agent", false, toolMsg, true)
		if err == nil {
			err = common.ExtractAndUnmarshalJSON([]byte(toolCompletion.Choices[0].Content), &toolEval)
			if err != nil {
				return AgentResponseEvaluationResult{}, err
			}
		}
	}

	return AgentResponseEvaluationResult{
		QueryResponseMetrics: mainEval,
		ToolCallMetric:       toolEval,
	}, nil
}

// evaluateAgentResponseAsync submits agent response evaluation to the worker pool and saves metrics
func evaluateAgentResponseAsync(ctx *security.RequestContext, resp NBAgentResponse, accId string, tools []toolcore.NBTool, userId string) {
	if !config.Config.EvaluationEnabled {
		return
	}
	// Create a new RequestContext for the background task
	bgCtx := security.NewRequestContext(
		context.Background(),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()

	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		metrics, err := EvaluateAgentResponse(bgCtx, resp, accId, tools, userId)
		if err != nil {
			bgCtx.GetLogger().Error("conversation: agent response evaluation failed", "error", err)
			return
		}
		metricsJson, err := common.MarshalJson(metrics)
		if err != nil {
			bgCtx.GetLogger().Error("conversation: failed to marshal evaluation metrics", "error", err)
			return
		}
		if resp.MessageId != "" {
			err = GetConversationDao().UpdateConversationMessageMetrics(resp.MessageId, string(metricsJson))
			if err != nil {
				bgCtx.GetLogger().Error("conversation: failed to save evaluation metrics to DB", "error", err)
			} else {
				bgCtx.GetLogger().Info("conversation: evaluation metrics saved to DB", "message_id", resp.MessageId)
				bgCtx.GetLogger().Info("conversation: agent response evaluation task completed successfully", "agent_name", resp.AgentName, "message_id", resp.MessageId, "conversation_id", resp.ConversationId)
			}
		}
	})
	if err != nil {
		bgCtx.GetLogger().Error("conversation: failed to submit agent response evaluation task", "error", err)
	}
}
