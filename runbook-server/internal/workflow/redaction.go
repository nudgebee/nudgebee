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

// collectSecretParamKeys records, by name, every key whose leaf value references Secrets.
// Returns true when a reference sits where no key can address it, so the caller blanks the container.
func collectSecretParamKeys(value any, out map[string]bool) bool {
	switch v := value.(type) {
	case map[string]any:
		unaddressable := false
		for k, val := range v {
			if containsSecretReference(k) { // the resolved key is the secret
				unaddressable = true
			}
			switch val.(type) {
			case map[string]any, []any:
				if collectSecretParamKeys(val, out) {
					out[k] = true
				}
			default:
				if containsSecretReference(val) {
					out[k] = true
				}
			}
		}
		return unaddressable
	case []any:
		unaddressable := false
		for _, val := range v {
			switch val.(type) {
			case map[string]any, []any:
				if collectSecretParamKeys(val, out) {
					unaddressable = true
				}
			default:
				// a bare element has no key of its own
				if containsSecretReference(val) {
					unaddressable = true
				}
			}
		}
		return unaddressable
	}
	return false
}

// buildSecretParamKeys identifies which param keys in each task definition
// contain references to Secrets, at any nesting depth.
// Returns taskID -> set of param keys.
func buildSecretParamKeys(taskDefs map[string]*model.Task) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	for taskID, task := range taskDefs {
		if task.Params == nil {
			continue
		}
		secretKeys := make(map[string]bool)
		// A secret as a top-level param key has no container to blank; only value
		// scrubbing reaches it, and allowedParams validation should prevent the shape.
		_ = collectSecretParamKeys(task.Params, secretKeys)
		if len(secretKeys) > 0 {
			result[taskID] = secretKeys
		}
	}
	return result
}

// redactDeep copies value, blanking secret-keyed fields through maps and slices.
// Copy-on-write is load-bearing: synthesized and skipped task Inputs alias the definition.
func redactDeep(value any, secretKeys map[string]bool) any {
	switch v := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(v))
		for k, val := range v {
			if secretKeys[k] {
				redacted[k] = RedactedValue
				continue
			}
			redacted[k] = redactDeep(val, secretKeys)
		}
		return redacted
	case []any:
		redacted := make([]any, len(v))
		for i, val := range v {
			redacted[i] = redactDeep(val, secretKeys)
		}
		return redacted
	default:
		return value
	}
}

// redactTaskParams blanks secret-containing fields at any depth, returning a fresh copy.
func redactTaskParams(params map[string]any, secretKeys map[string]bool) map[string]any {
	if len(secretKeys) == 0 || params == nil {
		return params
	}
	redacted, ok := redactDeep(params, secretKeys).(map[string]any)
	if !ok {
		return params
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

// RedactSecretsFromTasks blanks resolved secrets in task Input and RenderedParams.
// RenderedParams matters because ExecutionsView renders `rendered_params ?? input`.
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
			if keys := resolveSecretKeysForTask(task.ID, secretParamKeys); keys != nil {
				task.Input = redactTaskParams(task.Input, keys)
				task.RenderedParams = redactTaskParams(task.RenderedParams, keys)
			}
			if len(task.Children) > 0 {
				redactRecursive(task.Children)
			}
		}
	}
	redactRecursive(tasks)
}
