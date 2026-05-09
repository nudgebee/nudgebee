package workflow

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"regexp"
	"strings"
)

// taskOutputRefRegex matches {{ Tasks['task-id'].output... }} patterns in template strings.
// Used to detect which tasks a given task implicitly depends on via template references.
var taskOutputRefRegex = regexp.MustCompile(`Tasks\[['"]([^'"]+)['"]\]`)

// ValidateDAG checks for cycles in the workflow tasks,
// ensures task ID uniqueness, and verifies dependency existence.
// It returns an error if a cycle is detected,
// duplicate task IDs exist, or a dependency is not found.
func ValidateDAG(tasksList []model.Task) error {
	taskIDs := make(map[string]bool)
	taskMap := make(map[string]model.Task) // To quickly check for dependency existence

	for _, task := range tasksList {
		// Check for unique task IDs
		if _, exists := taskIDs[task.ID]; exists {
			return fmt.Errorf("duplicate task ID found: %s", task.ID)
		}
		taskIDs[task.ID] = true
		taskMap[task.ID] = task // Populate taskMap for dependency existence check

		// Recursively validate nested tasks (core.group uses Tasks field)
		if len(task.Tasks) > 0 {
			if err := ValidateDAG(task.Tasks); err != nil {
				return fmt.Errorf("in group task %s: %w", task.ID, err)
			}
		}

		// Recursively validate foreach subtasks (core.foreach uses params.tasks)
		if task.Type == "core.foreach" {
			if subtasks := extractSubtasksFromParams(task.Params); len(subtasks) > 0 {
				if err := ValidateDAG(subtasks); err != nil {
					return fmt.Errorf("in foreach task %s: %w", task.ID, err)
				}
			}
		}
	}

	// Second pass: Check for dependency existence
	for _, task := range tasksList {
		for _, depID := range task.DependsOn {
			if _, exists := taskMap[depID]; !exists {
				return fmt.Errorf("task %s depends on non-existent task: %s", task.ID, depID)
			}
		}
	}

	// Third pass: Check that template references have corresponding depends_on
	if err := ValidateDependencyCompleteness(tasksList); err != nil {
		return err
	}

	// Finally, check for cycles using topological sort
	_, err := TopologicalSort(tasksList)
	return err
}

// ValidateDependencyCompleteness scans task definitions for {{ Tasks['X']... }} template
// references and verifies that each referenced task is reachable via the transitive closure
// of the referencing task's depends_on chain. A direct depends_on entry is not required —
// any ancestor in the dependency graph is sufficient, since topological execution guarantees
// the referenced task's output is populated by the time the referencing task runs.
func ValidateDependencyCompleteness(tasksList []model.Task) error {
	// Build set of known task IDs and direct-deps adjacency at this scope
	taskIDs := make(map[string]bool, len(tasksList))
	directDeps := make(map[string][]string, len(tasksList))
	for _, task := range tasksList {
		taskIDs[task.ID] = true
		directDeps[task.ID] = task.DependsOn
	}

	// Memoised transitive closure of depends_on within this scope
	closure := make(map[string]map[string]bool)
	var ancestors func(id string) map[string]bool
	ancestors = func(id string) map[string]bool {
		if c, ok := closure[id]; ok {
			return c
		}
		c := make(map[string]bool)
		closure[id] = c // placeholder — defends against cycles even though ValidateDAG rejects them
		for _, d := range directDeps[id] {
			if !taskIDs[d] {
				continue
			}
			c[d] = true
			for k := range ancestors(d) {
				c[k] = true
			}
		}
		closure[id] = c
		return c
	}

	for _, task := range tasksList {
		referencedTasks := extractTaskReferences(task)
		reachable := ancestors(task.ID)

		for refTaskID := range referencedTasks {
			if refTaskID == task.ID {
				continue // self-reference is valid in some edge cases
			}
			if !taskIDs[refTaskID] {
				continue // reference to a task outside this scope (e.g., parent scope) — skip
			}
			if !reachable[refTaskID] {
				return fmt.Errorf("task '%s' references Tasks['%s'] in its definition but is not (transitively) downstream of it. "+
					"Add \"%s\" (or an ancestor of it) to the depends_on chain of task '%s' so the executor runs them in order",
					task.ID, refTaskID, refTaskID, task.ID)
			}
		}

		// Recurse into nested tasks
		if len(task.Tasks) > 0 {
			if err := ValidateDependencyCompleteness(task.Tasks); err != nil {
				return fmt.Errorf("in group task %s: %w", task.ID, err)
			}
		}
		if task.Type == "core.foreach" {
			if subtasks := extractSubtasksFromParams(task.Params); len(subtasks) > 0 {
				if err := ValidateDependencyCompleteness(subtasks); err != nil {
					return fmt.Errorf("in foreach task %s: %w", task.ID, err)
				}
			}
		}
	}

	return nil
}

// extractTaskReferences finds all Tasks['xxx'] references in a task's params, if condition,
// set_vars, and set_state fields. Returns a set of unique referenced task IDs.
func extractTaskReferences(task model.Task) map[string]bool {
	refs := make(map[string]bool)

	// Scan the if condition
	if task.If != "" {
		for _, id := range findTaskRefs(task.If) {
			refs[id] = true
		}
	}

	// Scan params
	if task.Params != nil {
		paramsJSON, _ := json.Marshal(task.Params)
		for _, id := range findTaskRefs(string(paramsJSON)) {
			refs[id] = true
		}
	}

	// Scan set_vars
	if task.SetVars != nil {
		setVarsJSON, _ := json.Marshal(task.SetVars)
		for _, id := range findTaskRefs(string(setVarsJSON)) {
			refs[id] = true
		}
	}

	// Scan set_state
	if task.SetState != nil {
		setStateJSON, _ := json.Marshal(task.SetState)
		for _, id := range findTaskRefs(string(setStateJSON)) {
			refs[id] = true
		}
	}

	return refs
}

// findTaskRefs extracts all task IDs from Tasks['xxx'] patterns in a string.
func findTaskRefs(s string) []string {
	matches := taskOutputRefRegex.FindAllStringSubmatch(s, -1)
	seen := make(map[string]bool)
	var ids []string
	for _, m := range matches {
		id := m[1]
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// extractSubtasksFromParams extracts the "tasks" array from a task's params (used by core.foreach).
// Returns nil if not present or not parseable.
func extractSubtasksFromParams(params map[string]any) []model.Task {
	if params == nil {
		return nil
	}
	tasksRaw, ok := params["tasks"]
	if !ok {
		return nil
	}

	// Handle already-typed tasks
	if tasks, ok := tasksRaw.([]model.Task); ok {
		return tasks
	}

	// Handle []any (common from JSON unmarshalling)
	if tasksSlice, ok := tasksRaw.([]any); ok {
		tasksBytes, err := json.Marshal(tasksSlice)
		if err != nil {
			return nil
		}
		var tasks []model.Task
		if err := json.Unmarshal(tasksBytes, &tasks); err != nil {
			return nil
		}
		return tasks
	}

	// Handle JSON string
	if tasksStr, ok := tasksRaw.(string); ok && strings.HasPrefix(tasksStr, "[") {
		var tasks []model.Task
		if err := json.Unmarshal([]byte(tasksStr), &tasks); err != nil {
			return nil
		}
		return tasks
	}

	return nil
}

// TopologicalSort performs a topological sort on the workflow tasks.
// It returns a list of tasks in a valid execution order, or an error if a cycle is detected.
func TopologicalSort(tasks []model.Task) ([]model.Task, error) {
	graph := make(map[string][]string)
	taskMap := make(map[string]model.Task)
	for _, task := range tasks {
		graph[task.ID] = task.DependsOn
		taskMap[task.ID] = task
	}

	var sorted []model.Task
	visiting := make(map[string]bool) // Nodes currently in the recursion stack
	visited := make(map[string]bool)  // All nodes that have been visited

	var dfs func(node string) error
	dfs = func(node string) error {
		visiting[node] = true
		visited[node] = true

		for _, neighbor := range graph[node] {
			if visiting[neighbor] {
				return fmt.Errorf("a circular dependency was detected in the workflow involving task: %s", node)
			}
			if !visited[neighbor] {
				if err := dfs(neighbor); err != nil {
					return err
				}
			}
		}

		visiting[node] = false
		sorted = append(sorted, taskMap[node])
		return nil
	}

	for taskID := range graph {
		if !visited[taskID] {
			if err := dfs(taskID); err != nil {
				return nil, err
			}
		}
	}

	return sorted, nil
}
