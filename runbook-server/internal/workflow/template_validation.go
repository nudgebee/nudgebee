package workflow

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"regexp"
	"strings"
	"time"

	"github.com/nikolalohinski/gonja/v2"
)

// Regexes that pull the literal string argument out of a date filter call so we can
// validate / lint it at save time. They intentionally only match literal arguments
// (e.g. tz("Asia/Kolkata")); dynamic args like tz(my_var) cannot be checked statically
// and are backstopped by the filters' runtime behavior.
var (
	tzArgRe         = regexp.MustCompile(`tz\(\s*["']([^"']*)["']`)
	dateFormatArgRe = regexp.MustCompile(`date_format\(\s*["']([^"']*)["']`)
	strftimeArgRe   = regexp.MustCompile(`strftime\(\s*["']([^"']*)["']`)
	strftimeCodeRe  = regexp.MustCompile(`%[a-zA-Z]`)
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
	// Hard-validate literal timezone names so a typo (tz("Asia/Kolkta")) is caught at
	// save time rather than failing the task render — matching the filter's fail-loud
	// runtime behavior.
	return validateTzZones(tpl)
}

// validateTzZones checks every literal tz("...") argument resolves to a real IANA zone.
func validateTzZones(tpl string) error {
	for _, m := range tzArgRe.FindAllStringSubmatch(tpl, -1) {
		zone := m[1]
		if _, err := time.LoadLocation(zone); err != nil {
			return fmt.Errorf("invalid timezone %q in tz() filter", zone)
		}
	}
	return nil
}

// LintTemplates returns non-fatal warnings about likely date-format mistakes across all
// task templates — specifically mixing the two format dialects (strftime %-codes vs Go
// reference layout). These are heuristics surfaced to the user, not hard errors.
func LintTemplates(tasks []model.Task) []string {
	var warnings []string
	for _, task := range tasks {
		ctx := fmt.Sprintf("task '%s'", task.ID)
		warnings = append(warnings, lintTemplateValue(task.If, ctx)...)
		warnings = append(warnings, lintTemplatesInMap(task.Params, ctx)...)
		warnings = append(warnings, lintTemplatesInMap(task.SetVars, ctx)...)
		warnings = append(warnings, lintTemplatesInMap(task.SetState, ctx)...)
		if len(task.Tasks) > 0 {
			warnings = append(warnings, LintTemplates(task.Tasks)...)
		}
		if task.Type == "core.foreach" {
			if subtasks := extractSubtasksFromParams(task.Params); len(subtasks) > 0 {
				warnings = append(warnings, LintTemplates(subtasks)...)
			}
		}
	}
	return warnings
}

func lintTemplatesInMap(m map[string]any, context string) []string {
	var warnings []string
	for key, val := range m {
		warnings = append(warnings, lintTemplateValueAny(val, fmt.Sprintf("%s.%s", context, key))...)
	}
	return warnings
}

func lintTemplateValueAny(val any, context string) []string {
	switch v := val.(type) {
	case string:
		return lintTemplateValue(v, context)
	case map[string]any:
		var warnings []string
		for key, inner := range v {
			warnings = append(warnings, lintTemplateValueAny(inner, fmt.Sprintf("%s.%s", context, key))...)
		}
		return warnings
	case []any:
		var warnings []string
		for i, inner := range v {
			warnings = append(warnings, lintTemplateValueAny(inner, fmt.Sprintf("%s[%d]", context, i))...)
		}
		return warnings
	}
	return nil
}

// lintTemplateValue flags the strongest cross-dialect signals only, to keep false
// positives low: %-codes inside date_format() (which wants a Go layout) and the Go
// reference year 2006 inside strftime() (which wants C-style %-codes).
func lintTemplateValue(tpl, context string) []string {
	if !strings.Contains(tpl, "{{") && !strings.Contains(tpl, "{%") {
		return nil
	}
	var warnings []string
	for _, m := range dateFormatArgRe.FindAllStringSubmatch(tpl, -1) {
		if strftimeCodeRe.MatchString(m[1]) {
			warnings = append(warnings, fmt.Sprintf(
				"%s: date_format(%q) looks like strftime — date_format uses a Go reference layout (e.g. \"2006-01-02 15:04\"), not %%-codes", context, m[1]))
		}
	}
	for _, m := range strftimeArgRe.FindAllStringSubmatch(tpl, -1) {
		if strings.Contains(m[1], "2006") {
			warnings = append(warnings, fmt.Sprintf(
				"%s: strftime(%q) looks like a Go layout — strftime uses C-style codes (e.g. \"%%Y-%%m-%%d %%H:%%M\"), not 2006-01-02", context, m[1]))
		}
	}
	return warnings
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
