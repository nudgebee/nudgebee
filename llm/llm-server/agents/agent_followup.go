package agents

import (
	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"strings"
)

const FollowupAgentName = "ask_clarification"

// [Changed for TicketV2] Enhanced the ask_clarification tool to support structured followup types
// (single_select, multi_select, text) with an options array. TicketV2 needs to collect specific
// fields (priority, type, assignee) via dropdown UIs rather than free-form text.
// Backward-compatible: followup_type defaults to "text" if not provided by the LLM, which maps
// to the old FollowupTypeUserInput behavior. The "required" in schema is only a hint to the LLM,
// not enforced server-side — existing agents that omit it will still work.
var toolDescription = `Sends a clarification or follow-up question back to the user when additional information is needed to provide a complete and accurate response.

		**Usage:**

		* **When to use this tool:** Use this tool when you need more details, context, or clarification from the user to properly answer their question or assist with their task.
		* **Input:** Provide a clear, concise question via "command", specify the appropriate "followup_type", and include "options" when the user must choose from a known set of values.

		**followup_type values:**
		* "single_select" — Use when the user must pick exactly ONE option from a fixed list (e.g., ticket type, priority level, team).
		* "multi_select" — Use when the user can pick ONE OR MORE options from a fixed list (e.g., labels, tags, multi-checkbox fields).
		* "text" — Use when the answer is free-form with no predefined options (e.g., dates, descriptions, custom text fields).

		**Rules:**
		* When using "single_select" or "multi_select", you MUST provide the "options" array with all valid choices.
		* When using "text", do NOT provide "options".
		* Ask only ONE field per call so each field gets the correct UI (select vs text). Do NOT bundle multiple fields into one call.
		* Only use this tool when you genuinely need additional information — do not re-ask questions the user has already answered.
		* Do NOT use this tool to request approval before delivering an answer or to confirm readiness — if you have sufficient information to answer, answer directly.
		`
var toolInput = "The clarification or follow-up question to ask the user"

// [Added for TicketV2] bracketOptionsRegex matches "[A, B, C]" patterns in question text only when
// preceded by option-indicating keywords (e.g., "Options:", "Choose from:", "Allowed:").
// This avoids false-matching markdown links like [text](url) or array notation in ticket descriptions.
var bracketOptionsRegex = regexp.MustCompile(`(?i)(?:options|choose from|select from|allowed|values|types?)\s*:?\s*\[([^\]]+)\]`)

func init() {
	toolcore.RegisterNBToolFactory(FollowupAgentName, func(accountId string) (toolcore.NBTool, error) {
		return FollowupAgent{}, nil
	})
}

type FollowupAgent struct {
}

func (m FollowupAgent) Name() string {
	return FollowupAgentName
}

func (m FollowupAgent) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeTool
}

func (m FollowupAgent) Description() string {
	return toolDescription
}

func (m FollowupAgent) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: toolInput,
			},
			"followup_type": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "The type of followup UI to show. Use 'single_select' when the user must pick one option from a list, 'multi_select' when multiple options can be chosen, or 'text' for free-form input.",
				Enum:        []any{"text", "single_select", "multi_select"},
				Default:     "text",
			},
			"options": {
				Type:        toolcore.ToolSchemaTypeArray,
				Description: "The list of selectable options. Required when followup_type is 'single_select' or 'multi_select'. Omit for 'text' type.",
				Items:       map[string]any{"type": "string"},
			},
		},
		Required: []string{"command", "followup_type"},
	}
}

func (m FollowupAgent) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	question := strings.TrimSpace(input.Command)
	if question == "" && input.Arguments != nil {
		if q, ok := input.Arguments["question"].(string); ok {
			question = strings.TrimSpace(q)
		}
	}

	if question == "" {
		return toolcore.NBToolResponse{
			Data:   "No question provided. Please provide a clear question to ask the user.",
			Type:   toolcore.NBToolResponseTypeText,
			Status: toolcore.NBToolResponseStatusError,
		}, nil
	}

	// Determine followup type from arguments
	followupType := core.FollowupTypeText
	if input.Arguments != nil {
		if ft, ok := input.Arguments["followup_type"].(string); ok {
			switch ft {
			case "single_select":
				followupType = core.FollowupTypeSingleSelect
			case "multi_select":
				followupType = core.FollowupTypeMultiSelect
			case "text":
				followupType = core.FollowupTypeText
			}
		}
	}

	// Extract options from structured arguments
	var options []string
	if input.Arguments != nil {
		if opts, ok := input.Arguments["options"].([]any); ok {
			for _, opt := range opts {
				if s, ok := opt.(string); ok {
					options = append(options, s)
				}
			}
		}
	}

	// Fallback: if no structured options were provided, try to extract them from
	// the question text. The LLM often puts options inline like "Options: [A, B, C]"
	// or "allowed: [High, Medium, Low]" instead of passing them as structured params.
	if len(options) == 0 {
		options = extractOptionsFromText(question)
	}

	// Auto-correct: if options are provided but type is text, infer the right select type
	if len(options) > 0 && followupType == core.FollowupTypeText {
		followupType = core.FollowupTypeSingleSelect
	}

	// Validate: select types require options — return error so the LLM retries with the options array
	if len(options) == 0 && (followupType == core.FollowupTypeSingleSelect || followupType == core.FollowupTypeMultiSelect) {
		return toolcore.NBToolResponse{
			Data:   "Error: followup_type is '" + string(followupType) + "' but no options were provided. You MUST include the 'options' array with all valid choices when using 'single_select' or 'multi_select'.",
			Type:   toolcore.NBToolResponseTypeText,
			Status: toolcore.NBToolResponseStatusError,
		}, nil
	}

	followup := core.FollowupRequest{
		Question:        question,
		FollowupType:    followupType,
		FollowupOptions: options,
		ToolName:        m.Name(),
		ToolId:          nbRequestContext.ToolCallId,
	}

	resp := toolcore.NBToolResponse{
		Data:   "",
		Type:   toolcore.NBToolResponseTypeText,
		Status: toolcore.NBToolResponseStatusWaiting, // Special status to indicate waiting for user input
		AdditionalDetails: map[string]any{
			"followup_request": followup,
		},
	}

	return resp, nil
}

// extractOptionsFromText attempts to find bracketed option lists in the question text.
// It looks for patterns like "[A, B, C]" and returns the parsed options.
// Returns nil if no valid options are found.
func extractOptionsFromText(text string) []string {
	matches := bracketOptionsRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	// Use the last bracketed list (most likely the actual options, not e.g. "[required]")
	lastMatch := matches[len(matches)-1][1]

	// Skip if it looks like a truncated list hint like "... +5 more"
	if strings.Contains(lastMatch, "+") && strings.Contains(lastMatch, "more") {
		if len(matches) > 1 {
			lastMatch = matches[len(matches)-2][1]
		} else {
			return nil
		}
	}

	parts := strings.Split(lastMatch, ",")
	var options []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Skip empty parts and ellipsis markers
		if p == "" || p == "..." || strings.HasPrefix(p, "...") {
			continue
		}
		options = append(options, p)
	}

	// Only return if we have at least 2 options (otherwise it's likely not an option list)
	if len(options) >= 2 {
		return options
	}
	return nil
}
