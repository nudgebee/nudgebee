package types

// AutoTaskResponse holds the result of an auto-executed task.
type AutoTaskResponse struct {
	// Data is the primary output of the task, similar to a regular task's output.
	Data any
	// SetVars is a map of new variables to be merged into the global `vars` context.
	// This explicitly updates the TaskContext.Vars.
	SetVars map[string]any
}

// TaskAutoExecute is an optional interface that a Task can implement
// to support automatic execution by the Planner.
type TaskAutoExecute interface {
	// Task is an embedded interface, meaning that any implementation of TaskAutoExecute
	// must also implement the base Task interface (GetName, Execute, etc.)
	Task

	// CanAutoExecute checks if the task is currently runnable with the given context.
	// It should inspect the context's Vars to determine if all necessary inputs are present.
	CanAutoExecute(ctx TaskContext) bool

	// AutoExecute runs the task automatically. It takes the context (with available vars)
	// and returns the task's primary data output and any new vars to be set.
	AutoExecute(ctx TaskContext) (AutoTaskResponse, error)
}
