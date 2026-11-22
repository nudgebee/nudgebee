package tools

import (
	"fmt"
	"strings"

	core "nudgebee/llm/tools/core"
)

// update_notebook_v2 is the structured-plan successor of update_notebook
// (#41443). Like v1 it is a CONTROL tool: the ReAct4 planner applies the
// patches from the step's arguments (see planner_react_4_plan.go), so Call
// only validates them and reports a precise error back to the model.
func init() {
	core.RegisterNBToolFactory(core.NotebookV2ToolName, func(accountId string) (core.NBTool, error) {
		return &notebookV2Tool{}, nil
	})
}

type notebookV2Tool struct{ core.ReadOnlyTool }

func (t *notebookV2Tool) Name() string             { return core.NotebookV2ToolName }
func (t *notebookV2Tool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t *notebookV2Tool) Description() string {
	return "Add or update steps of your plan — the visible, durable state of this request. " +
		"Each step is a task (something to do) or a hypothesis (a possible cause to test). " +
		"Send only the steps you add or change, by id; unchanged steps keep their values. " +
		"Mark the step you are working on in_progress (task) or testing (hypothesis). " +
		"Closing a step (done/skipped/blocked, or supported/refuted/inconclusive) requires a one-line finding. " +
		"Call it alongside your investigation calls in the same response; it does not answer the user."
}

func (t *notebookV2Tool) InputSchema() core.ToolSchema {
	statuses := append(append([]string{}, core.PlanTaskStatuses...), core.PlanHypothesisStatuses...)
	str := func(description string, enum []string) map[string]any {
		p := map[string]any{"type": "string", "description": description}
		if len(enum) > 0 {
			p["enum"] = enum
		}
		return p
	}
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			core.PlanStepsArgument: {
				Type:        core.ToolSchemaTypeArray,
				Description: "Steps to add or update.",
				Items: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":      str("Short stable id, e.g. s1, h1, h1a.", nil),
						"phase":   str("Investigations: Scope, Verify or Conclude. Omit for other requests.", nil),
						"title":   str("What the step does or claims. Required when adding a step.", nil),
						"kind":    str("task or hypothesis.", []string{core.PlanStepKindTask, core.PlanStepKindHypothesis}),
						"status":  str("Tasks: pending/in_progress/done/skipped/blocked. Hypotheses: open/testing/supported/refuted/inconclusive.", statuses),
						"prior":   str("Hypotheses only: how likely, from what you have seen.", core.PlanPriors),
						"finding": str("One line: what was found, or why it was skipped/blocked/inconclusive. Required when closing.", nil),
						"parent":  str("Optional id of the hypothesis this check belongs to (one level).", nil),
					},
					"required": []string{"id"},
				},
			},
		},
		Required: []string{core.PlanStepsArgument},
	}
}

// Call validates the patch list. The planner applies valid patches from the
// step arguments; an invalid call returns an error the model can act on.
func (t *notebookV2Tool) Call(_ core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	args := strings.TrimSpace(input.Command)
	if args == "" {
		args = "{}"
	}
	steps, err := core.ParsePlanPatches(args)
	if err != nil {
		return core.NBToolResponse{
			Data:   core.NotebookV2ToolName + ": " + err.Error(),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	return core.NBToolResponse{
		Data:   fmt.Sprintf("Plan updated (%d step(s)).", len(steps)),
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
