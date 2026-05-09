package workflow

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"strings"

	"github.com/nikolalohinski/gonja/v2"
)

// ValidateTemplateSyntax parses all template expressions in a workflow's task definitions
// to catch syntax errors at save/validate time rather than at execution time.
// This covers params, if conditions, set_vars, and set_state fields.
func ValidateTemplateSyntax(tasks []model.Task) error {
	for _, task := range tasks {
		// Validate the 'if' condition
		if task.If != "" {
			if err := tryParseTemplate(task.If); err != nil {
				return fmt.Errorf("task '%s' has invalid template syntax in 'if' condition: %w", task.ID, err)
			}
		}

		// Validate all template strings in params
		if task.Params != nil {
			if err := validateTemplatesInMap(task.Params, fmt.Sprintf("task '%s' params", task.ID)); err != nil {
				return err
			}
		}

		// Validate set_vars
		if task.SetVars != nil {
			if err := validateTemplatesInMap(task.SetVars, fmt.Sprintf("task '%s' set_vars", task.ID)); err != nil {
				return err
			}
		}

		// Validate set_state
		if task.SetState != nil {
			if err := validateTemplatesInMap(task.SetState, fmt.Sprintf("task '%s' set_state", task.ID)); err != nil {
				return err
			}
		}

		// Recurse into nested tasks (core.group)
		if len(task.Tasks) > 0 {
			if err := ValidateTemplateSyntax(task.Tasks); err != nil {
				return fmt.Errorf("in group task '%s': %w", task.ID, err)
			}
		}

		// Recurse into foreach subtasks
		if task.Type == "core.foreach" {
			if subtasks := extractSubtasksFromParams(task.Params); len(subtasks) > 0 {
				if err := ValidateTemplateSyntax(subtasks); err != nil {
					return fmt.Errorf("in foreach task '%s': %w", task.ID, err)
				}
			}
		}
	}
	return nil
}

// tryParseTemplate attempts to parse a string as a Gonja template.
// It only checks syntax — it does not evaluate expressions.
// Returns nil if the template parses successfully, or an error describing the syntax problem.
func tryParseTemplate(tpl string) error {
	// Only attempt to parse strings that actually contain template expressions
	if !strings.Contains(tpl, "{{") && !strings.Contains(tpl, "{%") {
		return nil
	}
	_, err := gonja.FromString(tpl)
	if err != nil {
		return fmt.Errorf("template parse error: %s", err.Error())
	}
	return nil
}

// validateTemplatesInMap recursively walks a map and validates all string values
// that contain template expressions.
func validateTemplatesInMap(m map[string]any, context string) error {
	for key, val := range m {
		if err := validateTemplateValue(val, fmt.Sprintf("%s.%s", context, key)); err != nil {
			return err
		}
	}
	return nil
}

// validateTemplateValue validates template syntax in any value type (string, map, slice).
func validateTemplateValue(val any, context string) error {
	switch v := val.(type) {
	case string:
		if err := tryParseTemplate(v); err != nil {
			return fmt.Errorf("%s has invalid template syntax: %w", context, err)
		}
	case map[string]any:
		for key, inner := range v {
			if err := validateTemplateValue(inner, fmt.Sprintf("%s.%s", context, key)); err != nil {
				return err
			}
		}
	case []any:
		for i, inner := range v {
			if err := validateTemplateValue(inner, fmt.Sprintf("%s[%d]", context, i)); err != nil {
				return err
			}
		}
	case json.Number:
		// no template strings in numbers
	}
	return nil
}
