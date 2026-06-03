package ai

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
)

// LLMNubiTask defines a task that interacts with an LLM for investigation.
type LLMNubiTask struct{}

// GetName returns the unique name of the task.
func (t *LLMNubiTask) GetName() string {
	return "llm.nubi"
}

// GetDescription returns a brief description of the task.
func (t *LLMNubiTask) GetDescription() string {
	return "Ask NuBi to investigate an issue or answer a question. NuBi uses your infrastructure context (K8s clusters, services, events) to provide relevant answers and diagnostics."
}

// GetDisplayName returns a human-readable name for the task.
func (t *LLMNubiTask) GetDisplayName() string {
	return "Ask NuBi"
}

// Execute runs the core logic of the task.
func (t *LLMNubiTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing LLMNubiTask", "params", params)
	if params["message"] == nil || params["message"] == "" {
		return nil, errors.New("message is required")
	}
	modelProvider, modelName, err := parseModelParam(params[modelParamFieldName])
	if err != nil {
		return nil, err
	}
	requestContext := taskCtx.GetNewRequestContext()
	resp, err := llm.ProcessRequest(requestContext, llm.LLMRequest{
		Message:      params["message"].(string),
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
func (t *LLMNubiTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"message": {
				Type:        "string",
				Description: "The question or issue to ask NuBi about. NuBi has access to your infrastructure context and can investigate K8s issues, check service health, and more.",
				Required:    true,
				SubType:     "textarea",
				Order:       1,
			},
			modelParamFieldName: modelInputSchemaProperty(2),
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *LLMNubiTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": { // Changed from "data" to "body"
				Type:        "string",
				Description: "Nubi Response Data.", // Updated description
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
