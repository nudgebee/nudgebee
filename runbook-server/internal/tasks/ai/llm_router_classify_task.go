package ai

import (
	"errors"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/llm"
	"strings"
)

// LLMRouterClassifyTask defines a task that uses LLM to classify a prompt into one of the provided options.
type LLMRouterClassifyTask struct{}

// GetName returns the unique name of the task.
func (t *LLMRouterClassifyTask) GetName() string {
	return "llm.classify"
}

// GetDescription returns a brief description of the task.
func (t *LLMRouterClassifyTask) GetDescription() string {
	return "Use AI to categorize input into one of several predefined options."
}

// GetDisplayName returns a human-readable name for the task.
func (t *LLMRouterClassifyTask) GetDisplayName() string {
	return "AI Classifier"
}

// Execute runs the core logic of the task.
func (t *LLMRouterClassifyTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing LLMRouterClassifyTask", "params", params)

	prompt, ok := params["prompt"].(string)
	if !ok || prompt == "" {
		return nil, errors.New("prompt is required")
	}

	optionsRaw, ok := params["options"].([]any)
	if !ok || len(optionsRaw) == 0 {
		return nil, errors.New("options list is required")
	}

	var sb strings.Builder
	sb.WriteString("You are a classification system. Your task is to determine which of the following options best matches the user's request.\n\n")
	fmt.Fprintf(&sb, "User Request: %s\n\n", prompt)
	sb.WriteString("Options:\n")

	validOptionNames := make(map[string]bool)

	for i, opt := range optionsRaw {
		optMap, ok := opt.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("option at index %d is invalid", i)
		}
		name, okName := optMap["name"].(string)
		desc, okDesc := optMap["description"].(string)

		if !okName || name == "" {
			return nil, fmt.Errorf("option at index %d is missing a valid name", i)
		}
		if !okDesc {
			// Description is marked as required in InputSchema, so we should enforce it.
			return nil, fmt.Errorf("option at index %d is missing a valid description", i)
		}

		validOptionNames[strings.TrimSpace(name)] = true
		fmt.Fprintf(&sb, "- Name: %s\n", name)
		fmt.Fprintf(&sb, "  Description: %s\n", desc)
	}

	sb.WriteString("\nInstruction: Return ONLY the Name of the selected option. Do not include any other text, explanation, or formatting. Example: database_issue")

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := llm.ProcessRequest(requestContext, llm.LLMRequest{
		Message:   "@llm " + sb.String(),
		AccountId: taskCtx.GetAccountID(),
		SessionId: taskCtx.GetWorkflowRunID(),
	})

	if err != nil {
		return nil, err
	}

	rawSelected := resp.Message
	selected := strings.TrimSpace(rawSelected)
	// Basic cleanup if the LLM adds extra quotes or periods
	selected = strings.Trim(selected, "\"' .")

	if !validOptionNames[selected] {
		// Attempt to extract the valid option name from a potentially conversational response
		var bestMatch string

		// 1. Look for exact matches surrounded by common markers (backticks, bold, quotes)
		for name := range validOptionNames {
			if strings.Contains(rawSelected, "`"+name+"`") ||
				strings.Contains(rawSelected, "**"+name+"**") ||
				strings.Contains(rawSelected, "\""+name+"\"") {
				bestMatch = name
				break
			}
		}

		// 2. Look for case-sensitive match as a substring
		if bestMatch == "" {
			for name := range validOptionNames {
				if strings.Contains(rawSelected, name) {
					bestMatch = name
					break
				}
			}
		}

		// 3. Look for case-insensitive match as a substring
		if bestMatch == "" {
			lowerRaw := strings.ToLower(rawSelected)
			for name := range validOptionNames {
				if strings.Contains(lowerRaw, strings.ToLower(name)) {
					bestMatch = name
					break
				}
			}
		}

		if bestMatch != "" {
			selected = bestMatch
		} else {
			taskCtx.GetLogger().Warn("LLM returned an option that was not in the list", "returned", rawSelected)
		}
	}

	return map[string]any{
		"selected_branch": selected,
	}, nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *LLMRouterClassifyTask) InputSchema() *types.Schema {
	optionSchema := types.Schema{
		Properties: map[string]types.Property{
			"name": {
				Type:        "string",
				Description: "Name of the option (branch name).",
				Required:    true,
			},
			"description": {
				Type:        "string",
				Description: "Description of what this option represents.",
				Required:    true,
			},
		},
	}

	return &types.Schema{
		Properties: map[string]types.Property{
			"prompt": {
				Type:        types.PropertyTypeString,
				Description: "The user query or context to evaluate.",
				Required:    true,
			},
			"options": {
				Type:        types.PropertyTypeArray,
				Description: "List of options to choose from.",
				Schema:      &optionSchema,
				Required:    true,
				SubType:     "string",
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *LLMRouterClassifyTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"selected_branch": {
				Type:        "string",
				Description: "The name of the selected option.",
				Required:    true,
			},
		},
	}
}
