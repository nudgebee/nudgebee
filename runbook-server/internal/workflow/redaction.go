package workflow

import (
	"nudgebee/runbook/internal/model"
	"regexp"
	"strings"
)

const RedactedValue = "***REDACTED***"

// secretRefPattern matches template references to Secrets in both Jinja and Go template syntax.
// Examples: {{ Secrets['key'] }}, {{ Secrets.key }}, {{.Secrets.key}}
var secretRefPattern = regexp.MustCompile(`Secrets[\[.\s]`)

// containsSecretReference recursively checks if a value contains
// a template reference to Secrets.
func containsSecretReference(value any) bool {
	switch v := value.(type) {
	case string:
		return secretRefPattern.MatchString(v)
	case map[string]any:
		for _, val := range v {
			if containsSecretReference(val) {
				return true
			}
		}
	case []any:
		for _, val := range v {
			if containsSecretReference(val) {
				return true
			}
		}
	}
	return false
}

// buildTaskDefinitionMap builds a flat map of taskID -> *model.Task from
// the recursive workflow definition task tree.
func buildTaskDefinitionMap(tasks []model.Task) map[string]*model.Task {
	m := make(map[string]*model.Task)
	var build func(tasks []model.Task)
	build = func(tasks []model.Task) {
		for i := range tasks {
			task := &tasks[i]
			m[task.ID] = task
			if len(task.Tasks) > 0 {
				build(task.Tasks)
			}
		}
	}
	build(tasks)
	return m
}

// buildSecretParamKeys identifies which param keys in each task definition
// contain references to Secrets. Returns taskID -> set of param keys.
func buildSecretParamKeys(taskDefs map[string]*model.Task) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	for taskID, task := range taskDefs {
		if task.Params == nil {
			continue
		}
		secretKeys := make(map[string]bool)
		for key, val := range task.Params {
			if containsSecretReference(val) {
				secretKeys[key] = true
			}
		}
		if len(secretKeys) > 0 {
			result[taskID] = secretKeys
		}
	}
	return result
}

// redactTaskInput replaces secret-containing fields in a task input map
// with RedactedValue.
func redactTaskInput(input map[string]any, secretKeys map[string]bool) map[string]any {
	if len(secretKeys) == 0 || input == nil {
		return input
	}
	redacted := make(map[string]any, len(input))
	for k, v := range input {
		if secretKeys[k] {
			redacted[k] = RedactedValue
		} else {
			redacted[k] = v
		}
	}
	return redacted
}

// resolveSecretKeysForTask finds the secret param keys for a given task ID.
// It handles foreach-prefixed IDs where the Temporal task ID is
// "{foreachID}-{index}-{childID}" but the definition only has "{childID}".
func resolveSecretKeysForTask(taskID string, secretParamKeys map[string]map[string]bool) map[string]bool {
	// Direct match
	if keys, ok := secretParamKeys[taskID]; ok {
		return keys
	}
	// Foreach child pattern: check if any defined task ID is a suffix of taskID
	for defID, keys := range secretParamKeys {
		suffix := "-" + defID
		if strings.HasSuffix(taskID, suffix) {
			return keys
		}
	}
	return nil
}

// RedactSecretsFromTasks redacts secret values from task inputs in workflow
// execution details, based on which params in the workflow definition
// reference Secrets templates.
func RedactSecretsFromTasks(tasks []model.TaskExecutionDetails, wfDef model.WorkflowDefinition) {
	taskDefs := buildTaskDefinitionMap(wfDef.Tasks)
	secretParamKeys := buildSecretParamKeys(taskDefs)

	if len(secretParamKeys) == 0 {
		return
	}

	var redactRecursive func(tasks []model.TaskExecutionDetails)
	redactRecursive = func(tasks []model.TaskExecutionDetails) {
		for i := range tasks {
			task := &tasks[i]
			if task.Input != nil {
				if keys := resolveSecretKeysForTask(task.ID, secretParamKeys); keys != nil {
					task.Input = redactTaskInput(task.Input, keys)
				}
			}
			if len(task.Children) > 0 {
				redactRecursive(task.Children)
			}
		}
	}
	redactRecursive(tasks)
}
