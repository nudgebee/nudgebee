package core

import (
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"
	"strconv"
)

// SwitchTask implements the Task interface for executing conditional branches of tasks.
//
// Supports three modes (checked in order):
//
// 1. Expression-based with next (preferred): Evaluates a single expression and routes to a task by ID.
//
//	params:
//	  expression: "{{ Inputs.env }}"
//	  cases:
//	    - value: "prod"
//	      next: "deploy-prod"
//	    - value: "staging"
//	      next: "deploy-staging"
//	  default_next: "deploy-dev"
//
// 2. Expression-based with tasks (backward compat): Same as above but cases embed task arrays.
//
//	params:
//	  expression: "{{ Inputs.env }}"
//	  cases:
//	    - value: "prod"
//	      tasks: [{ id: deploy-prod }]
//	  default:
//	    - { id: deploy-dev }
//
// 3. Condition-based (legacy): Each branch has a boolean condition evaluated by the template engine.
//
//	params:
//	  branches:
//	    - condition: "{{ Inputs.env == 'prod' }}"
//	      tasks: [{ id: deploy-prod }]
//	  default:
//	    - { id: deploy-dev }
type SwitchTask struct{}

func (t *SwitchTask) GetName() string {
	return "core.switch"
}

// GetDescription returns a brief description of the task.
func (t *SwitchTask) GetDescription() string {
	return "Branch into different paths based on a condition (like if/else). Define an expression and multiple cases — the workflow evaluates the expression and routes to the matching case. Supports a default path when no case matches."
}

// GetDisplayName returns a human-readable name for the task.
func (t *SwitchTask) GetDisplayName() string {
	return "Switch"
}

// Execute resolves the switch's cases and returns a slim, JSON-friendly map
// describing the routing decision. The executor invokes this method as a
// regular Temporal activity (dispatched by params shape — see processTaskLoop)
// so the switch's input, selected case, status, and timings show up in
// workflow_get_execution like any other task.
//
// Result shape:
//
//	{
//	  "selected_case": "<matched value | 'default'>",
//	  "routed_to":    ["<task-id>", ...],
//	  "embedded_tasks": [...]   // only for legacy modes 2/3 (cases[].tasks)
//	}
//
// The executor reads `routed_to` (and `embedded_tasks` for legacy switches) to
// build the in-coroutine WorkflowDefinition for the matched branch.
func (t *SwitchTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	def, err := t.GetChildWorkflowDefinition(taskCtx, params)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if def.Output != nil {
		if sc, ok := def.Output["selected_case"].(string); ok {
			out["selected_case"] = sc
		}
	}
	routedTo := make([]string, 0, len(def.Tasks))
	var embedded []model.Task
	for _, task := range def.Tasks {
		routedTo = append(routedTo, task.ID)
		// Mode 1 leaves Type empty (only ID set) — those route by ID against
		// the workflow's top-level definitions. Modes 2/3 embed the task body
		// directly so it can be hydrated without a top-level lookup.
		if task.Type != "" {
			embedded = append(embedded, task)
		}
	}
	out["routed_to"] = routedTo
	if len(embedded) > 0 {
		out["embedded_tasks"] = embedded
	}
	return out, nil
}

func (t *SwitchTask) InputSchema() *types.Schema {
	caseSchema := types.Schema{
		Properties: map[string]types.Property{
			"value": {
				Type:        types.PropertyTypeString,
				Description: "The value to match against the evaluated expression.",
				Required:    true,
				Title:       "Case Value",
				Order:       1,
			},
			"next": {
				Type:        types.PropertyTypeString,
				Description: "ID of the task to execute if this case matches.",
				Title:       "Next Task",
				Order:       2,
			},
		},
	}

	return &types.Schema{
		Properties: map[string]types.Property{
			"expression": {
				Type:        types.PropertyTypeString,
				Description: "Template expression to evaluate. The result is matched against case values. Example: {{ Inputs.env }}",
				Required:    true,
				Title:       "Expression",
				SubType:     "template",
				Order:       1,
			},
			"cases": {
				Type:        types.PropertyTypeArray,
				Description: "List of cases. Each case has a value to match and a next task to execute.",
				Schema:      &caseSchema,
				Title:       "Cases",
				Order:       2,
			},
			"default_next": {
				Type:        types.PropertyTypeString,
				Description: "ID of the task to execute if no case matches.",
				Title:       "Default Task",
				Order:       3,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *SwitchTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"selected_case": {
				Type:        types.PropertyTypeString,
				Description: "The case value that was matched, or 'default' if no case matched.",
				Title:       "Selected Case",
			},
		},
	}
}

// GetChildWorkflowDefinition constructs a dynamic workflow definition based on the provided parameters.
// This allows the executor to run the switch task as an inline child workflow (or sub-routine).
//
// Supports expression-based (new), expression-based with tasks (backward compat), and condition-based (legacy) formats.
func (t *SwitchTask) GetChildWorkflowDefinition(taskCtx types.TaskContext, params map[string]any) (*model.WorkflowDefinition, error) {
	// Try expression-based format first (new format)
	if _, hasExpression := params["expression"]; hasExpression {
		return t.getChildWorkflowFromExpression(taskCtx, params)
	}

	// Fall back to condition-based format (legacy)
	if _, hasBranches := params["branches"]; hasBranches {
		return t.getChildWorkflowFromConditions(taskCtx, params)
	}

	// No expression and no branches - return empty workflow
	return &model.WorkflowDefinition{
		Tasks:  []model.Task{},
		Inputs: make([]model.Input, 0),
	}, nil
}

// getChildWorkflowFromExpression handles the expression-based switch format.
// The expression has already been evaluated by the template engine before reaching this method.
//
// Supports both "next" (preferred, single task ID) and "tasks" (backward compat, task array) per case.
func (t *SwitchTask) getChildWorkflowFromExpression(taskCtx types.TaskContext, params map[string]any) (*model.WorkflowDefinition, error) {
	expressionValue := fmt.Sprintf("%v", params["expression"])

	logger := taskCtx.GetLogger()
	logger.Info("Switch evaluating expression", "resolved_value", expressionValue)

	cases, _ := params["cases"].([]any)
	if result, err := t.matchCase(cases, expressionValue, logger); result != nil || err != nil {
		return result, err
	}

	defaultNext, _ := params["default_next"].(string)
	_, hasDefaultArr := params["default"]
	if defaultNext == "" && !hasDefaultArr {
		return nil, fmt.Errorf("switch: expression value %q did not match any case and no default is configured", expressionValue)
	}

	logger.Info("Switch no case matched, using default")
	return t.resolveDefault(params)
}

// matchCase iterates over cases and returns the result for the first match.
// Returns (nil, nil) if no case matches.
func (t *SwitchTask) matchCase(cases []any, expressionValue string, logger interface{ Info(string, ...any) }) (*model.WorkflowDefinition, error) {
	for _, c := range cases {
		caseMap, ok := c.(map[string]any)
		if !ok {
			continue
		}

		caseValue := ""
		if val, ok := caseMap["value"]; ok {
			caseValue = fmt.Sprintf("%v", val)
		}

		if expressionValue != caseValue {
			continue
		}

		logger.Info("Switch matched case", "case_value", caseValue)
		return resolveCase(caseMap, caseValue)
	}
	return nil, nil
}

// resolveCase extracts the task(s) for a matched case.
// Prefers "next" (single task ID), falls back to "tasks" (array), then empty.
func resolveCase(caseMap map[string]any, caseValue string) (*model.WorkflowDefinition, error) {
	// Preferred format: cases[].next (single task ID)
	if nextID, ok := caseMap["next"].(string); ok && nextID != "" {
		return newSwitchResult([]model.Task{{ID: nextID}}, caseValue), nil
	}

	// Backward compat: cases[].tasks (task array)
	if tasksData, ok := caseMap["tasks"]; ok {
		tasks, err := unmarshalTasks(tasksData)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal tasks for case '%s': %w", caseValue, err)
		}
		return newSwitchResult(tasks, caseValue), nil
	}

	return nil, fmt.Errorf("switch: case %q matched but has no 'next' task configured", caseValue)
}

// resolveDefault returns the default workflow when no case matches.
// Prefers "default_next" (single task ID), falls back to "default" (array), then empty.
func (t *SwitchTask) resolveDefault(params map[string]any) (*model.WorkflowDefinition, error) {
	// Preferred format: default_next (single task ID)
	if defaultNext, ok := params["default_next"].(string); ok && defaultNext != "" {
		return newSwitchResult([]model.Task{{ID: defaultNext}}, "default"), nil
	}

	// Backward compat: default (task array)
	if defaultTasksData, ok := params["default"]; ok {
		tasks, err := unmarshalTasks(defaultTasksData)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal default tasks: %w", err)
		}
		return newSwitchResult(tasks, "default"), nil
	}

	return nil, fmt.Errorf("switch: no default is configured")
}

// getChildWorkflowFromConditions handles the legacy condition-based switch format.
// Each branch has a boolean condition already resolved by the template engine.
func (t *SwitchTask) getChildWorkflowFromConditions(_ types.TaskContext, params map[string]any) (*model.WorkflowDefinition, error) {
	var selectedTasks []model.Task
	branchSelected := false

	// Check branches
	if branches, ok := params["branches"].([]any); ok {
		for _, b := range branches {
			branchMap, ok := b.(map[string]any)
			if !ok {
				continue
			}

			// Check condition
			condVal, ok := branchMap["condition"]
			if !ok {
				continue
			}

			isTrue := false
			// Condition might be boolean or string "true"/"false"
			if val, ok := condVal.(bool); ok {
				isTrue = val
			} else if val, ok := condVal.(string); ok {
				parsed, err := strconv.ParseBool(val)
				if err == nil {
					isTrue = parsed
				}
			}

			if isTrue {
				if tasksData, ok := branchMap["tasks"]; ok {
					var err error
					selectedTasks, err = unmarshalTasks(tasksData)
					if err != nil {
						return nil, err
					}
					branchSelected = true
					break
				}
			}
		}
	}

	// If no branch selected, check default
	if !branchSelected {
		defaultTasksData, ok := params["default"]
		if !ok {
			return nil, fmt.Errorf("switch: no branch condition matched and no default is configured")
		}
		var err error
		selectedTasks, err = unmarshalTasks(defaultTasksData)
		if err != nil {
			return nil, err
		}
	}

	return &model.WorkflowDefinition{
		Tasks:  selectedTasks,
		Inputs: make([]model.Input, 0),
	}, nil
}

// newSwitchResult constructs a WorkflowDefinition for a matched switch case.
func newSwitchResult(tasks []model.Task, selectedCase string) *model.WorkflowDefinition {
	return &model.WorkflowDefinition{
		Tasks:  tasks,
		Inputs: make([]model.Input, 0),
		Output: map[string]any{"selected_case": selectedCase},
	}
}

// unmarshalTasks converts []any or []model.Task to []model.Task.
// Returns an empty slice for nil input.
// Used by legacy condition-based format and backward-compat expression-based format.
func unmarshalTasks(input any) ([]model.Task, error) {
	if input == nil {
		return []model.Task{}, nil
	}
	if tList, ok := input.([]model.Task); ok {
		return tList, nil
	}
	if tSlice, ok := input.([]any); ok {
		tasksBytes, err := json.Marshal(tSlice)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tasks: %w", err)
		}
		var tasks []model.Task
		if err := json.Unmarshal(tasksBytes, &tasks); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tasks: %w", err)
		}
		return tasks, nil
	}
	return nil, fmt.Errorf("unexpected tasks type: %T", input)
}
