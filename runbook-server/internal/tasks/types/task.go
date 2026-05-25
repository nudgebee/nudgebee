package types

import "nudgebee/runbook/internal/model"

// Task defines the interface that all runnable tasks must implement.
type Task interface {
	// GetName returns the unique name of the task, used for registration.
	GetName() string

	// GetDescription returns a brief description of the task.
	GetDescription() string

	// GetDisplayName returns a human-readable name for the task.
	GetDisplayName() string

	// Execute runs the core logic of the task, using TaskContext for rich metadata and utilities.
	Execute(ctx TaskContext, params map[string]any) (any, error)

	// InputSchema returns a structured schema for the task's expected parameters.
	InputSchema() *Schema

	// OutputSchema returns a structured schema for the task's output.
	OutputSchema() *Schema
}

// TaskInlineWorkflow is an optional interface for tasks that should be executed
// as an ephemeral inline child workflow instead of a standard activity.
type TaskInlineWorkflow interface {
	Task
	// GetChildWorkflowDefinition constructs a dynamic workflow definition based on the provided parameters.
	GetChildWorkflowDefinition(taskCtx TaskContext, params map[string]any) (*model.WorkflowDefinition, error)
}

// TaskExecutionStrategy is an optional interface for TaskInlineWorkflow tasks
// to control their execution model (Inline vs. Child Workflow).
type TaskExecutionStrategy interface {
	// ShouldExecuteAsChildWorkflow returns true if the workflow should be executed
	// as a full Temporal Child Workflow. Returns false for Inline/Coroutine execution.
	ShouldExecuteAsChildWorkflow() bool
}

// LoopConfiguration defines the execution parameters for a loop task.
type LoopConfiguration struct {
	Items       []any                     // The list of items to iterate over
	ItemVarName string                    // The variable name for the current item
	Body        *model.WorkflowDefinition // The workflow definition to execute for each iteration
	BatchSize   int                       // Number of items to process concurrently (0 or less means sequential, 1 means one at a time).
}

// TaskLoop is an interface for tasks that implement looping logic (e.g. for-each).
// The Executor handles the iteration and execution of the Body definition.
type TaskLoop interface {
	Task
	GetLoopConfig(taskCtx TaskContext, params map[string]any) (*LoopConfiguration, error)
}

// RuntimeNotesProvider is an optional interface that tasks can implement
// to provide usage hints and common pitfalls that help the LLM agent
// build correct workflow definitions.
type RuntimeNotesProvider interface {
	RuntimeNotes() []string
}

// DryRunAware is an interface that tasks can implement to indicate support for dry-run execution.
type DryRunAware interface {
	SupportsDryRun() bool
}

// MemoKeyCallWorkflowDepth is the Temporal Memo key used to track how many
// core.call-workflow boundaries a workflow execution sits behind. The executor
// reads it on workflow start, propagates +1 to spawned children, and refuses
// to spawn past MaxCallWorkflowDepth so that a recursive call chain
// (Wf A -> Wf B -> Wf A) can't blow up the Temporal cluster.
const (
	MemoKeyCallWorkflowDepth = "call_workflow_depth"
	MaxCallWorkflowDepth     = 10
)
