package workflow

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"strings"
)

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

	// Finally, check for cycles using topological sort
	_, err := TopologicalSort(tasksList)
	return err
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
