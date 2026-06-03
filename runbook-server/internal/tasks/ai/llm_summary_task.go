package ai

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
)

// LLMSummaryTask defines a task that interacts with an LLM to generate a summary.
type LLMSummaryTask struct{}

// GetName returns the unique name of the task.
func (t *LLMSummaryTask) GetName() string {
	return "llm.summary"
}

// GetDescription returns a brief description of the task.
func (t *LLMSummaryTask) GetDescription() string {
	return "Summarize text or data using AI. Pass any text, logs, or data as input and receive a concise summary highlighting the key points."
}

// GetDisplayName returns a human-readable name for the task.
func (t *LLMSummaryTask) GetDisplayName() string {
	return "AI Summary"
}

// Execute runs the core logic of the task.
func (t *LLMSummaryTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing LLMSummaryTask", "params", params)
	if params["message"] == nil || params["message"] == "" {
		return nil, errors.New("message is required")
	}
	modelProvider, modelName, err := parseModelParam(params[modelParamFieldName])
	if err != nil {
		return nil, err
	}
	requestContext := taskCtx.GetNewRequestContext()
	resp, err := llm.ProcessRequest(requestContext, llm.LLMRequest{
		Message:      "@llm " + params["message"].(string),
		AccountId:    taskCtx.GetAccountID(),
		SessionId:    taskCtx.GetWorkflowRunID(),
		LlmProvider:  modelProvider,
		LlmModelName: modelName,
	})

	if err != nil {
		return nil, err
	}

	return map[string]any{
		"data":            resp.Message,
		"conversation_id": resp.ConversationId,
		"session_id":      resp.SessionId,
	}, nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *LLMSummaryTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"message": {
				Type:        "string",
				Description: "The text, logs, or data to summarize. You can reference outputs from previous tasks using template expressions like {{ Tasks['task_id'].output.field }}.",
				Required:    true,
				SubType:     "textarea",
				Order:       1,
			},
			modelParamFieldName: modelInputSchemaProperty(2),
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *LLMSummaryTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "string",
				Description: "LLM Summary Response.",
				Required:    true,
			},
			"conversation_id": {
				Type:        "string",
				Description: "NuBi Conversation Id",
				Required:    true,
			},
			"session_id": {
				Type:        "string",
				Description: "NuBi Session Id",
				Required:    true,
			},
		},
	}
}
