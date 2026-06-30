package ai

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
	"strings"
)

// LLMInvestigateTask defines a task that interacts with an LLM for investigation.
type LLMInvestigateTask struct{}

// GetName returns the unique name of the task.
func (t *LLMInvestigateTask) GetName() string {
	return "llm.investigate"
}

// GetDescription returns a brief description of the task.
func (t *LLMInvestigateTask) GetDescription() string {
	return "Ask AI to analyze and investigate a problem. Provide a message describing the issue and the AI will research it using available tools and context, returning a detailed analysis with findings and recommendations."
}

// GetDisplayName returns a human-readable name for the task.
func (t *LLMInvestigateTask) GetDisplayName() string {
	return "AI Investigation"
}

// Execute runs the core logic of the task.
func (t *LLMInvestigateTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing LLMInvestigateTask", "params", params)
	if params["message"] == nil || params["message"] == "" {
		return nil, errors.New("message is required")
	}
	msg, ok := params["message"].(string)
	if !ok {
		return nil, errors.New("message parameter must be a string")
	}

	tools, err := parseToolsParam(params["tools"])
	if err != nil {
		return nil, err
	}

	modelProvider, modelName, err := parseModelParam(params[modelParamFieldName])
	if err != nil {
		return nil, err
	}

	responseFormat, _ := params["response_format"].(string)
	if responseFormat == "json" {
		msg += "\n\nIMPORTANT: Your final answer MUST be a valid JSON object only — no markdown prose, no code fences, no text outside the JSON."
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := llm.ProcessRequest(requestContext, applyWorkflowTrace(taskCtx, llm.LLMRequest{
		Message:      msg,
		AccountId:    taskCtx.GetAccountID(),
		Tools:        tools,
		LlmProvider:  modelProvider,
		LlmModelName: modelName,
	}))

	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"data":            resp.Message,
		"conversation_id": resp.ConversationId,
		"session_id":      resp.SessionId,
	}

	if responseFormat == "json" {
		parsed, parseErr := common.ExtractJSONFromLLMResponse(resp.Message)
		// This task's contract (OutputSchema + RuntimeNotes) is that `data` is a
		// JSON object on success. A valid but non-object JSON value (array or
		// primitive) would otherwise pass through and break downstream templates
		// that index data.<field>, so treat it as a parse failure and fall into
		// the raw_text path below — keeping data-is-an-object invariant intact.
		if parseErr == nil {
			if _, isObj := parsed.(map[string]any); !isObj {
				parseErr = fmt.Errorf("extracted JSON is not an object (got %T)", parsed)
			}
		}
		if parseErr != nil {
			// Log a truncated copy of the raw response so we can see what the LLM
			// actually produced when extraction failed, without dumping a
			// multi-KB blob into the logs.
			taskCtx.GetLogger().Warn("response_format=json but failed to extract JSON from LLM response",
				"error", parseErr,
				"response_preview", truncateForLog(resp.Message, 1000))
			result["parse_error"] = parseErr.Error()
			// Keep `data` a map even on failure. Downstream tasks template into
			// data (e.g. {{ Tasks['x'].output.data.summary }}); if data were left
			// as the raw LLM string, that field access resolves to empty and a
			// dependent task — typically Notifications Email — then fails with
			// "body is required". Wrapping the raw text under a known key keeps
			// the data-is-an-object invariant so such templates resolve to a
			// deterministic empty value, and the raw response is still inspectable.
			result["data"] = map[string]any{"raw_text": resp.Message}
		} else {
			result["data"] = parsed
		}
	}

	return result, nil
}

// parseToolsParam normalises the optional `tools` parameter into a clean []string.
// Accepts either []string (Go-native) or []any (JSON-deserialised). Empty / nil input
// returns nil so callers fall back to the agent's default tool set on the LLM server.
func parseToolsParam(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []string:
		return filterEmpty(v), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("tools parameter must be an array of strings, got element %T", item)
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tools parameter must be an array of strings, got %T", raw)
	}
}

// truncateForLog returns at most max runes of s, appending an ellipsis marker
// when the input was longer. Operates on runes so it never splits a multi-byte
// UTF-8 sequence.
func truncateForLog(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…[truncated]"
}

func filterEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// InputSchema returns the schema for the task's expected parameters.
func (t *LLMInvestigateTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"message": {
				Type:        types.PropertyTypeString,
				Description: "Describe the problem or question to investigate. You can reference outputs from previous tasks using template expressions like {{ Tasks['task_id'].output.field }}.",
				Required:    true,
				SubType:     "textarea",
				Order:       1,
			},
			"response_format": {
				Type:        types.PropertyTypeString,
				Description: "Output format. 'text' (default) returns the raw LLM response. 'json' extracts a JSON object from the response.",
				Required:    false,
				Default:     "text",
				Options:     []string{"text", "json"},
				Order:       2,
			},
			"tools": {
				Type:        types.PropertyTypeArray,
				Description: "Optional allow-list of tool names. When set, the investigation can only use these tools. Leave empty to use the auto-selected agent's full tool set.",
				Help: "Pinning tools is a strict allow-list: anything not selected is hidden from the agent, " +
					"including `shell_execute` and `load_skills` (knowledge-base loader). " +
					"If the auto-selected agent supports none of the picked tools the investigation will fail with no usable tools — " +
					"prefer an empty list when in doubt.",
				Required: false,
				Order:    3,
				OptionsSource: &types.OptionsSource{
					Type: "llm_tools",
				},
			},
			modelParamFieldName: modelInputSchemaProperty(4),
		},
	}
}

func (t *LLMInvestigateTask) RuntimeNotes() []string {
	return []string{
		"Output is in the 'data' field. When response_format='text', data is a raw string. When response_format='json', data is a parsed object.",
		"If you need structured data from the investigation, set response_format='json'. The agent is automatically instructed to return a valid JSON object — add a JSON schema or example in your message to control the exact shape.",
		"If JSON extraction fails with response_format='json', 'data' is an object of the form {\"raw_text\": \"<raw response>\"} and 'parse_error' explains the failure. Referencing other fields on 'data' (e.g. data.summary) will resolve empty in that case — guard downstream tasks accordingly.",
		"Setting 'tools' restricts the investigation to that allow-list — the auto-selected agent's other tools, shell_execute, and load_skills (knowledge bases) are all hidden unless explicitly listed. Leave empty for the default tool set.",
	}
}

// OutputSchema returns the schema for the task's output.
func (t *LLMInvestigateTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        "any",
				Description: "LLM investigation response. String when response_format=text, parsed JSON object when response_format=json.",
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
			"parse_error": {
				Type:        "string",
				Description: "Present only when response_format=json and JSON extraction failed.",
			},
		},
	}
}
