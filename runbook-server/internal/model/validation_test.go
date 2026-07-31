package model_test

import (
	"strings"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
)

func TestValidateDuration(t *testing.T) {
	validate := validator.New()
	_ = validate.RegisterValidation("duration", model.ValidateDuration)

	tests := []struct {
		name     string
		duration string
		isValid  bool
	}{
		{"Valid duration - seconds", "10s", true},
		{"Valid duration - minutes", "5m", true},
		{"Valid duration - hours", "2h", true},
		{"Valid duration - mixed", "1h30m", true},
		{"Valid duration - zero", "0s", true},
		{"Empty duration", "", true}, // Empty string is considered valid (omitempty)
		{"Invalid duration - missing unit", "10", false},
		{"Invalid duration - unknown unit", "10x", false},
		{"Invalid duration - non-numeric", "abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type TestStruct struct {
				Duration string `validate:"duration"`
			}
			s := TestStruct{Duration: tt.duration}
			err := validate.Struct(s)
			if tt.isValid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.IsType(t, validator.ValidationErrors{}, err)
			}
		})
	}
}

func TestValidateWorkflowTrigger(t *testing.T) {
	validate := validator.New()
	_ = validate.RegisterValidation("workflowtrigger", model.ValidateWorkflowTrigger)

	tests := []struct {
		name    string
		trigger model.WorkflowTrigger
		isValid bool
	}{
		{"Valid trigger - schedule", model.WorkflowTriggerSchedule, true},
		{"Valid trigger - manual", model.WorkflowTriggerManual, true},
		{"Valid trigger - webhook", model.WorkflowTriggerWebhook, true},
		{"Invalid trigger", "unknown", false},
		{"Empty trigger", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type TestStruct struct {
				Trigger model.WorkflowTrigger `validate:"workflowtrigger"`
			}
			s := TestStruct{Trigger: tt.trigger}
			err := validate.Struct(s)
			if tt.isValid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.IsType(t, validator.ValidationErrors{}, err)
			}
		})
	}
}

func TestValidateWorkflow(t *testing.T) {
	t.Run("Valid Workflow", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerManual},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
				},
				Hooks: &model.Hooks{}, // Explicitly initialize On to prevent dive error
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})

	t.Run("Workflow with missing name", func(t *testing.T) {
		wf := model.Workflow{
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerManual, Params: map[string]any{}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", Params: map[string]any{"url": "http://example.com"}},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("Workflow with invalid trigger type", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: "invalid_trigger", Params: map[string]any{}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", Params: map[string]any{"url": "http://example.com"}},
				},
				Hooks: &model.Hooks{},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "triggers[0].type")
	})

	t.Run("Workflow with invalid task timeout", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerManual, Params: map[string]any{}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", Timeout: "invalid_duration"},
				},
				Hooks: &model.Hooks{},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tasks[0].timeout")
	})

	t.Run("Workflow with missing task ID", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerManual, Params: map[string]any{}},
				},
				Tasks: []model.Task{
					{Type: "http_request", Params: map[string]any{"url": "http://example.com"}},
				},
				Hooks: &model.Hooks{},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tasks[0].id")
	})

	t.Run("Valid schedule trigger params", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 0 * * *"}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})

	t.Run("Invalid schedule trigger params - missing cron", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"foo": "bar"}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cron_missing")
	})

	t.Run("Invalid schedule trigger params - empty cron", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": ""}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cron_invalid")
	})

	t.Run("Schedule trigger cron syntax", func(t *testing.T) {
		scheduleWorkflow := func(cronExpr string) model.Workflow {
			return model.Workflow{
				Name: "test-workflow",
				Definition: model.WorkflowDefinition{
					Version: "v1",
					Triggers: []model.Trigger{
						{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": cronExpr}},
					},
					Tasks: []model.Task{
						{ID: "task1", Type: "http_request"},
					},
				},
			}
		}

		accepted := []string{
			"0 9 * * 1-5",
			"*/15 * * * *",
			"0 0,12 1 */2 *",
			"@every 1h",
			"@daily",
			"CRON_TZ=Asia/Kolkata 0 9 * * *",
			"TZ=UTC 0 9 * * *",
		}
		for _, expr := range accepted {
			assert.NoError(t, model.ValidateWorkflow(scheduleWorkflow(expr)), "expected %q to be accepted", expr)
		}

		rejected := []string{
			"not a cron",
			"99 * * * *",
			"0 9 * *",
			"0 9 * * * *",
			"* * * * 9",
			"CRON_TZ=Asia/Kolkata",
		}
		for _, expr := range rejected {
			err := model.ValidateWorkflow(scheduleWorkflow(expr))
			if assert.Error(t, err, "expected %q to be rejected", expr) {
				assert.Contains(t, err.Error(), "cron_syntax_invalid", "expected %q to fail cron syntax validation", expr)
			}
		}
	})

	t.Run("Valid webhook trigger params", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerWebhook, Params: map[string]any{"integration_name": "my-hook"}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})

	t.Run("Invalid webhook trigger params - empty integration_name", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerWebhook, Params: map[string]any{"integration_name": ""}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "integration_name_invalid")
	})

	t.Run("Invalid webhook trigger params - missing integration_name", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerWebhook, Params: map[string]any{"secret": "s"}},
				},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "integration_name_missing")
	})

	eventWorkflow := func(params map[string]any) model.Workflow {
		return model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version: "v1",
				Triggers: []model.Trigger{
					{Type: model.WorkflowTriggerEvent, Params: params},
				},
				Tasks: []model.Task{{ID: "task1", Type: "http_request"}},
			},
		}
	}

	t.Run("Event trigger - scalar event_type accepted", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": "alert"})
		assert.NoError(t, model.ValidateWorkflow(wf))
	})

	t.Run("Event trigger - array event_type accepted", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": []any{"alert", "incident"}})
		assert.NoError(t, model.ValidateWorkflow(wf))
	})

	t.Run("Event trigger - filter alone accepted", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"filter": "{{ event.severity == 'critical' }}"})
		assert.NoError(t, model.ValidateWorkflow(wf))
	})

	t.Run("Event trigger - empty array plus filter accepted", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": []any{}, "filter": "{{ event.cluster == 'prod' }}"})
		assert.NoError(t, model.ValidateWorkflow(wf))
	})

	t.Run("Event trigger - no event_type and no filter rejected", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{})
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "event_trigger_needs_filter")
	})

	t.Run("Event trigger - nil params rejected (post-JSON-omitempty round-trip)", func(t *testing.T) {
		wf := eventWorkflow(nil)
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "event_trigger_needs_filter")
	})

	t.Run("Event trigger - empty event_type and whitespace filter rejected", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": "", "filter": "   "})
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "event_trigger_needs_filter")
	})

	t.Run("Event trigger - non-string array item rejected", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": []any{"alert", 42}})
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "event_type_invalid_item")
	})

	t.Run("Event trigger - empty string in array rejected", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": []any{"alert", ""}})
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "event_type_invalid_item")
	})

	t.Run("Event trigger - wrong type for event_type rejected", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": 42, "filter": "{{ true }}"})
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "event_type_invalid_type")
	})

	t.Run("Event trigger - unsupported param rejected", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": "alert", "unknown": true})
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported_event_param")
	})

	t.Run("Event trigger - invalid gonja filter rejected", func(t *testing.T) {
		wf := eventWorkflow(map[string]any{"event_type": "alert", "filter": "{{ unclosed"})
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "filter_invalid_syntax")
	})
}

func TestValidateWorkflow_TaskTimeoutsExceedWorkflow(t *testing.T) {
	t.Run("Task timeouts sum exceeds workflow timeout", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Timeout:  "5m",
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", Timeout: "3m"},
					{ID: "task2", Type: "http_request", Timeout: "3m"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "task_timeouts_exceed_workflow")
	})

	t.Run("Task timeouts sum equals workflow timeout - valid", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Timeout:  "5m",
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", Timeout: "3m"},
					{ID: "task2", Type: "http_request", Timeout: "2m"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})

	t.Run("Task timeouts sum less than workflow timeout - valid", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Timeout:  "10m",
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", Timeout: "3m"},
					{ID: "task2", Type: "http_request", Timeout: "2m"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})

	t.Run("No workflow timeout set - skip validation", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", Timeout: "30m"},
					{ID: "task2", Type: "http_request", Timeout: "30m"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})

	t.Run("Tasks without timeouts - skip validation", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Timeout:  "5m",
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
					{ID: "task2", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})
}

func TestValidateWorkflow_DuplicateAndMissingDependencies(t *testing.T) {
	t.Run("Duplicate task IDs", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request"},
					{ID: "task1", Type: "http_request"}, // duplicate
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate_task_id")
	})

	t.Run("Missing dependency", func(t *testing.T) {
		wf := model.Workflow{
			Name: "test-workflow",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", DependsOn: []string{"task2"}}, // task2 does not exist
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing_dependency")
	})
}

func TestValidateWorkflow_DependencyEdgeCases(t *testing.T) {
	t.Run("Self-dependency", func(t *testing.T) {
		wf := model.Workflow{
			Name: "wf-self-dep",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", DependsOn: []string{"task1"}},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "self_dependency") // Updated to check for self_dependency
	})

	t.Run("Circular dependency (A->B->A)", func(t *testing.T) {
		wf := model.Workflow{
			Name: "wf-circular",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "A", Type: "http_request", DependsOn: []string{"B"}},
					{ID: "B", Type: "http_request", DependsOn: []string{"A"}},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circular_dependency")
	})

	t.Run("Circular dependency (A->B->C->A)", func(t *testing.T) {
		wf := model.Workflow{
			Name: "wf-circular-3",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "A", Type: "http_request", DependsOn: []string{"B"}},
					{ID: "B", Type: "http_request", DependsOn: []string{"C"}},
					{ID: "C", Type: "http_request", DependsOn: []string{"A"}},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circular_dependency")
	})

	t.Run("DependsOn with duplicate entries", func(t *testing.T) {
		wf := model.Workflow{
			Name: "wf-dup-depends",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", DependsOn: []string{"task2", "task2"}},
					{ID: "task2", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})

	t.Run("Multiple missing dependencies", func(t *testing.T) {
		wf := model.Workflow{
			Name: "wf-multi-missing",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", DependsOn: []string{"foo", "bar"}},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing_dependency") // Only check for the tag, not the value
	})

	t.Run("Empty string task ID", func(t *testing.T) {
		wf := model.Workflow{
			Name: "wf-empty-id",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "", Type: "http_request"},
					{ID: "task2", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tasks[0].id")
	})

	t.Run("Workflow with invalid name - too short", func(t *testing.T) {
		wf := model.Workflow{
			Name: "ab", // Too short
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task1", Type: "http_request"}},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("Workflow with invalid name - too long", func(t *testing.T) {
		wf := model.Workflow{
			Name: strings.Repeat("a", 51), // Too long
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task1", Type: "http_request"}},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("Workflow with invalid name - starts with hyphen", func(t *testing.T) {
		wf := model.Workflow{
			Name: "-invalid-name",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task1", Type: "http_request"}},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("Workflow with invalid name - ends with hyphen", func(t *testing.T) {
		wf := model.Workflow{
			Name: "invalid-name-",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task1", Type: "http_request"}},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("Workflow with invalid name - starts with space", func(t *testing.T) {
		wf := model.Workflow{
			Name: " invalid-name",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task1", Type: "http_request"}},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("Workflow with invalid name - ends with space", func(t *testing.T) {
		wf := model.Workflow{
			Name: "invalid-name ",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task1", Type: "http_request"}},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("Task with invalid ID - too short", func(t *testing.T) {
		wf := model.Workflow{
			Name: "valid-workflow-name",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "ab", Type: "http_request"}}, // Too short
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tasks[0].id")
	})

	t.Run("Task with invalid ID - too long", func(t *testing.T) {
		wf := model.Workflow{
			Name: "valid-workflow-name",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: strings.Repeat("a", 65), Type: "http_request"}}, // Too long
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tasks[0].id")
	})

	t.Run("Task with invalid ID - contains special characters", func(t *testing.T) {
		wf := model.Workflow{
			Name: "valid-workflow-name",
			Definition: model.WorkflowDefinition{
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks:    []model.Task{{ID: "task!1", Type: "http_request"}}, // Contains '!'
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tasks[0].id")
	})

	t.Run("Empty DependsOn", func(t *testing.T) {
		wf := model.Workflow{
			Name: "wf-empty-depends",
			Definition: model.WorkflowDefinition{
				Version:  "v1",
				Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
				Tasks: []model.Task{
					{ID: "task1", Type: "http_request", DependsOn: []string{}},
					{ID: "task2", Type: "http_request"},
				},
			},
		}
		err := model.ValidateWorkflow(wf)
		assert.NoError(t, err)
	})
}

// aiInvocableWorkflow builds a workflow that satisfies every AI-invocation
// precondition, so each subtest below can knock out exactly one of them.
func aiInvocableWorkflow() model.Workflow {
	return model.Workflow{
		Name:        "restart-payment-consumers",
		AIInvocable: true,
		Definition: model.WorkflowDefinition{
			Version:        "v1",
			LLMDescription: "Restarts the payment consumers and drains the stuck queue.",
			Triggers:       []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks:          []model.Task{{ID: "task1", Type: "http_request"}},
		},
	}
}

func TestValidateWorkflowAIInvocable(t *testing.T) {
	t.Run("AI-invocable with description and manual trigger", func(t *testing.T) {
		assert.NoError(t, model.ValidateWorkflow(aiInvocableWorkflow()))
	})

	t.Run("AI-invocable without llm_description is rejected", func(t *testing.T) {
		wf := aiInvocableWorkflow()
		wf.Definition.LLMDescription = ""
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ai_invocable")
	})

	t.Run("whitespace-only llm_description is rejected", func(t *testing.T) {
		wf := aiInvocableWorkflow()
		wf.Definition.LLMDescription = "   \n\t "
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ai_invocable")
	})

	t.Run("AI-invocable without a manual trigger is rejected", func(t *testing.T) {
		wf := aiInvocableWorkflow()
		wf.Definition.Triggers = []model.Trigger{
			{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
		}
		err := model.ValidateWorkflow(wf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ai_invocable")
	})

	t.Run("a manual trigger alongside others is enough", func(t *testing.T) {
		wf := aiInvocableWorkflow()
		wf.Definition.Triggers = []model.Trigger{
			{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
			{Type: model.WorkflowTriggerManual},
		}
		assert.NoError(t, model.ValidateWorkflow(wf))
	})

	t.Run("preconditions do not apply when the flag is off", func(t *testing.T) {
		// A scheduled automation with no AI description is perfectly valid — it
		// just cannot be invoked by the AI.
		wf := aiInvocableWorkflow()
		wf.AIInvocable = false
		wf.Definition.LLMDescription = ""
		wf.Definition.Triggers = []model.Trigger{
			{Type: model.WorkflowTriggerSchedule, Params: map[string]any{"cron": "0 * * * *"}},
		}
		assert.NoError(t, model.ValidateWorkflow(wf))
	})
}

func TestHasManualTrigger(t *testing.T) {
	tests := []struct {
		name     string
		triggers []model.Trigger
		want     bool
	}{
		{"manual only", []model.Trigger{{Type: model.WorkflowTriggerManual}}, true},
		{"schedule only", []model.Trigger{{Type: model.WorkflowTriggerSchedule}}, false},
		{"webhook only", []model.Trigger{{Type: model.WorkflowTriggerWebhook}}, false},
		{"event only", []model.Trigger{{Type: model.WorkflowTriggerEvent}}, false},
		{"manual among many", []model.Trigger{
			{Type: model.WorkflowTriggerWebhook},
			{Type: model.WorkflowTriggerManual},
		}, true},
		{"no triggers", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := model.WorkflowDefinition{Triggers: tt.triggers}
			assert.Equal(t, tt.want, def.HasManualTrigger())
		})
	}
}
