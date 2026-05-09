package core

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestLogger struct {
	*slog.Logger
}

func (l *TestLogger) Debug(msg string, keyvals ...any) { l.Logger.Debug(msg, keyvals...) }
func (l *TestLogger) Info(msg string, keyvals ...any)  { l.Logger.Info(msg, keyvals...) }
func (l *TestLogger) Warn(msg string, keyvals ...any)  { l.Logger.Warn(msg, keyvals...) }
func (l *TestLogger) Error(msg string, keyvals ...any) { l.Logger.Error(msg, keyvals...) }

func newTestContext() types.TaskContext {
	logger := &TestLogger{Logger: slog.Default()}
	return testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", logger)
}

// --- Expression-based format tests (new: cases[].next) ---

func TestSwitchTask_Expression_MatchFirstCase(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"expression": "prod",
		"cases": []any{
			map[string]any{"value": "prod", "next": "deploy-prod"},
			map[string]any{"value": "staging", "next": "deploy-staging"},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "deploy-prod", wfDef.Tasks[0].ID)
	assert.Equal(t, "", wfDef.Tasks[0].Type) // executor hydrates Type
	assert.Equal(t, map[string]any{"selected_case": "prod"}, wfDef.Output)
}

func TestSwitchTask_Expression_MatchSecondCase(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"expression": "staging",
		"cases": []any{
			map[string]any{"value": "prod", "next": "deploy-prod"},
			map[string]any{"value": "staging", "next": "deploy-staging"},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "deploy-staging", wfDef.Tasks[0].ID)
	assert.Equal(t, map[string]any{"selected_case": "staging"}, wfDef.Output)
}

func TestSwitchTask_Expression_MatchDefaultNext(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"expression": "dev",
		"cases": []any{
			map[string]any{"value": "prod", "next": "deploy-prod"},
		},
		"default_next": "deploy-dev",
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "deploy-dev", wfDef.Tasks[0].ID)
	assert.Equal(t, map[string]any{"selected_case": "default"}, wfDef.Output)
}

func TestSwitchTask_Expression_NoMatchNoDefault(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"expression": "unknown",
		"cases": []any{
			map[string]any{"value": "prod", "next": "deploy-prod"},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, wfDef)
	assert.Contains(t, err.Error(), `"unknown"`)
	assert.Contains(t, err.Error(), "no default is configured")
}

func TestSwitchTask_Expression_CaseWithNoNext(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	// Case matches but has no "next" / "tasks" — treated as a misconfiguration.
	params := map[string]any{
		"expression": "noop",
		"cases": []any{
			map[string]any{"value": "noop"},
		},
		"default_next": "should-not-reach",
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, wfDef)
	assert.Contains(t, err.Error(), `"noop"`)
	assert.Contains(t, err.Error(), "has no 'next'")
}

func TestSwitchTask_Expression_NoMatchEmptyStringDefaultNext(t *testing.T) {
	// The UI sends default_next as an empty string when no default edge is wired.
	// This should surface the user-facing "no default configured" error, not the
	// internal resolveDefault sentinel.
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"expression":   "three",
		"default_next": "",
		"cases": []any{
			map[string]any{"value": "two", "next": "core_print-1-1"},
			map[string]any{"value": "one", "next": "core_print-1"},
			map[string]any{"value": "four", "next": ""},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, wfDef)
	assert.Contains(t, err.Error(), `"three"`)
	assert.Contains(t, err.Error(), "no default is configured")
}

func TestSwitchTask_Expression_NumericValue(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"expression": 200,
		"cases": []any{
			map[string]any{"value": "200", "next": "handle-ok"},
			map[string]any{"value": "500", "next": "handle-error"},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "handle-ok", wfDef.Tasks[0].ID)
}

// --- Backward compatibility: expression-based with tasks array ---

func TestSwitchTask_Expression_BackwardCompat_Tasks(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	// Old format: cases[].tasks instead of cases[].next
	params := map[string]any{
		"expression": "prod",
		"cases": []any{
			map[string]any{
				"value": "prod",
				"tasks": []any{
					map[string]any{"id": "deploy-prod", "type": "k8s.apply"},
				},
			},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "deploy-prod", wfDef.Tasks[0].ID)
	assert.Equal(t, "k8s.apply", wfDef.Tasks[0].Type)
}

func TestSwitchTask_Expression_BackwardCompat_DefaultArray(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	// Old format: "default" as task array
	params := map[string]any{
		"expression": "miss",
		"cases":      []any{},
		"default": []any{
			map[string]any{"id": "fallback", "type": "core.print"},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.Len(t, wfDef.Tasks, 1)
	assert.Equal(t, "fallback", wfDef.Tasks[0].ID)
	assert.Equal(t, map[string]any{"selected_case": "default"}, wfDef.Output)
}

// --- Legacy condition-based format tests ---

func TestSwitchTask_Legacy_MatchFirstBranch(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"branches": []any{
			map[string]any{
				"condition": true,
				"tasks":     []any{map[string]any{"id": "t1", "type": "print"}},
			},
			map[string]any{
				"condition": false,
				"tasks":     []any{map[string]any{"id": "t2", "type": "print"}},
			},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 1)
}

func TestSwitchTask_Legacy_MatchSecondBranch(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"branches": []any{
			map[string]any{
				"condition": false,
				"tasks":     []any{map[string]any{"id": "t1", "type": "print"}},
			},
			map[string]any{
				"condition": "true",
				"tasks":     []any{map[string]any{"id": "t2", "type": "print"}},
			},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 1)
}

func TestSwitchTask_Legacy_MatchDefault(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"branches": []any{
			map[string]any{"condition": false, "tasks": []any{}},
		},
		"default": []any{
			map[string]any{"id": "d1", "type": "print"},
			map[string]any{"id": "d2", "type": "print"},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 2)
}

func TestSwitchTask_Legacy_NoMatchNoDefault(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{
		"branches": []any{
			map[string]any{"condition": false, "tasks": []any{}},
		},
	}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, wfDef)
	assert.Contains(t, err.Error(), "no branch condition matched")
}

// --- General tests ---

func TestSwitchTask_Execute_Deprecated(t *testing.T) {
	task := &SwitchTask{}
	logger := &TestLogger{Logger: slog.Default()}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", logger)
	_, err := task.Execute(ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deprecated")
}

func TestSwitchTask_InputSchema(t *testing.T) {
	task := &SwitchTask{}
	schema := task.InputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "expression")
	assert.Contains(t, schema.Properties, "cases")
	assert.Contains(t, schema.Properties, "default_next")
	assert.True(t, schema.Properties["expression"].Required)
}

func TestSwitchTask_OutputSchema(t *testing.T) {
	task := &SwitchTask{}
	schema := task.OutputSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "selected_case")
}

func TestSwitchTask_NoExpressionNoBranches(t *testing.T) {
	task := &SwitchTask{}
	ctx := newTestContext()

	params := map[string]any{}

	wfDef, err := task.GetChildWorkflowDefinition(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, wfDef)
	assert.Len(t, wfDef.Tasks, 0)
}
