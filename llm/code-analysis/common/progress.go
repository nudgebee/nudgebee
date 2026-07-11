package common

import "sync"

// AnalysisState tracks the progress and result of an async analysis.
//
// Fields are guarded by mu: the async analysis goroutine writes Progress/Status/
// Result/Tracker while the /status HTTP handler reads them concurrently on every
// poll. Callers must go through the helpers below (or Snapshot) rather than
// touching fields directly.
type AnalysisState struct {
	mu       sync.Mutex
	Status   string                 // "running", "completed", "failed"
	Progress string                 // Current progress text
	Result   any                    // Final response (set on completion)
	Error    string                 // Error message (set on failure)
	Tracker  *ToolInvocationTracker // Live per-step tool invocations (set via AttachTracker)
}

var progressStore sync.Map // map[analysisID]*AnalysisState

// InitAnalysis registers a new analysis in the progress store.
func InitAnalysis(analysisID string) {
	progressStore.Store(analysisID, &AnalysisState{Status: "running"})
}

// SetProgress updates the progress text for a running analysis.
func SetProgress(analysisID, text string) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.mu.Lock()
		state.Progress = text
		state.mu.Unlock()
	}
}

// AttachTracker binds the analysis's tool-invocation tracker to its state so the
// /status handler can stream the steps taken so far. Safe to call once after the
// tracker is created.
func AttachTracker(analysisID string, t *ToolInvocationTracker) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.mu.Lock()
		state.Tracker = t
		state.mu.Unlock()
	}
}

// CompleteAnalysis marks an analysis as completed with its result.
func CompleteAnalysis(analysisID string, result any) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.mu.Lock()
		state.Result = result
		state.Status = "completed"
		state.mu.Unlock()
	}
}

// FailAnalysis marks an analysis as failed with an error message.
func FailAnalysis(analysisID string, errMsg string) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.mu.Lock()
		state.Error = errMsg
		state.Status = "failed"
		state.mu.Unlock()
	}
}

// AnalysisSnapshot is a lock-free copy of an AnalysisState's fields for the
// /status handler to read without holding the state lock while serializing.
type AnalysisSnapshot struct {
	Status   string
	Progress string
	Result   any
	Error    string
	Tracker  *ToolInvocationTracker
}

// Snapshot returns a consistent copy of the analysis state, or nil if not found.
// The Tracker pointer is shared (it is internally synchronized).
func Snapshot(analysisID string) *AnalysisSnapshot {
	v, ok := progressStore.Load(analysisID)
	if !ok {
		return nil
	}
	state := v.(*AnalysisState)
	state.mu.Lock()
	defer state.mu.Unlock()
	return &AnalysisSnapshot{
		Status:   state.Status,
		Progress: state.Progress,
		Result:   state.Result,
		Error:    state.Error,
		Tracker:  state.Tracker,
	}
}

// GetAnalysisState returns the current state of an analysis, or nil if not found.
func GetAnalysisState(analysisID string) *AnalysisState {
	if v, ok := progressStore.Load(analysisID); ok {
		return v.(*AnalysisState)
	}
	return nil
}

// CleanupAnalysis removes an analysis from the progress store.
func CleanupAnalysis(analysisID string) {
	progressStore.Delete(analysisID)
}
