package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
	"regexp"
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

	responseFormat, _ := params["response_format"].(string)
	if responseFormat == "json" {
		msg += "\n\nIMPORTANT: Your final answer MUST be a valid JSON object only — no markdown prose, no code fences, no text outside the JSON."
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := llm.ProcessRequest(requestContext, llm.LLMRequest{
		Message:   msg,
		AccountId: taskCtx.GetAccountID(),
		SessionId: taskCtx.GetWorkflowRunID(),
		Tools:     tools,
	})

	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"data":            resp.Message,
		"conversation_id": resp.ConversationId,
		"session_id":      resp.SessionId,
	}

	if responseFormat == "json" {
		parsed, parseErr := extractJSON(resp.Message)
		if parseErr != nil {
			taskCtx.GetLogger().Warn("response_format=json but failed to extract JSON from LLM response",
				"error", parseErr)
			result["parse_error"] = parseErr.Error()
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

var codeBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?```")

// extractJSON attempts to parse JSON from text, trying direct parse first,
// then extraction from markdown code blocks.
func extractJSON(text string) (any, error) {
	trimmed := strings.TrimSpace(text)
	var direct any
	if err := json.Unmarshal([]byte(trimmed), &direct); err == nil {
		return direct, nil
	}

	re := codeBlockRe
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		var parsed any
		if err := json.Unmarshal([]byte(strings.TrimSpace(matches[1])), &parsed); err == nil {
			return parsed, nil
		}
	}

	return nil, fmt.Errorf("could not extract valid JSON from response (%d chars)", len(text))
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
		},
	}
}

func (t *LLMInvestigateTask) RuntimeNotes() []string {
	return []string{
		"Output is in the 'data' field. When response_format='text', data is a raw string. When response_format='json', data is a parsed object.",
		"If you need structured data from the investigation, set response_format='json'. The agent is automatically instructed to return a valid JSON object — add a JSON schema or example in your message to control the exact shape.",
		"If JSON extraction fails with response_format='json', 'data' contains the raw response and 'parse_error' explains the failure.",
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
