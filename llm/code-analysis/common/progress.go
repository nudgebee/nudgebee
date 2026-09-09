package common

import (
	"context"
	"sync"
	"time"
)

// AnalysisState tracks the progress and result of an async analysis.
//
// Fields are guarded by mu: the async analysis goroutine writes Progress/Status/
// Result/Tracker while the /status HTTP handler reads them concurrently on every
// poll. Callers must go through the helpers below (or Snapshot) rather than
// touching fields directly.
type AnalysisState struct {
	mu          sync.Mutex
	Status      string                 // "running", "completed", "failed"
	Progress    string                 // Current progress text
	Result      any                    // Final response (set on completion)
	Error       string                 // Error message (set on failure)
	Tracker     *ToolInvocationTracker // Live per-step tool invocations (set via AttachTracker)
	Cancel      context.CancelFunc
	LastCheckIn time.Time
}

var progressStore sync.Map // map[analysisID]*AnalysisState

// InitAnalysis registers a new analysis in the progress store.
func InitAnalysis(analysisID string) {
	progressStore.Store(analysisID, &AnalysisState{Status: "running", LastCheckIn: time.Now()})
}

// SetCancelFunc associates the context cancellation function with an analysis.
// It is installed by the async handler before the analysis starts.
func SetCancelFunc(analysisID string, cancel context.CancelFunc) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.mu.Lock()
		state.Cancel = cancel
		state.mu.Unlock()
	}
}

// CheckIn records that the caller is still interested in the analysis.
func CheckIn(analysisID string) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.mu.Lock()
		if state.Status == "running" {
			state.LastCheckIn = time.Now()
		}
		state.mu.Unlock()
	}
}

// CancelAnalysis requests cancellation of a running analysis. The status is
// marked first so a concurrent completion cannot turn an explicit cancellation
// into a successful result.
func CancelAnalysis(analysisID string) bool {
	v, ok := progressStore.Load(analysisID)
	if !ok {
		return false
	}
	state := v.(*AnalysisState)
	state.mu.Lock()
	if state.Status != "running" {
		state.mu.Unlock()
		return false
	}
	state.Status = "cancelled"
	cancel := state.Cancel
	state.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
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
		if state.Status == "running" {
			state.Result = result
			state.Status = "completed"
		}
		state.mu.Unlock()
	}
}

// FailAnalysis marks an analysis as failed with an error message.
func FailAnalysis(analysisID string, errMsg string) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.mu.Lock()
		if state.Status == "running" {
			state.Error = errMsg
			state.Status = "failed"
		}
		state.mu.Unlock()
	}
}

// AnalysisSnapshot is a lock-free copy of an AnalysisState's fields for the
// /status handler to read without holding the state lock while serializing.
type AnalysisSnapshot struct {
	Status      string
	Progress    string
	Result      any
	Error       string
	Tracker     *ToolInvocationTracker
	LastCheckIn time.Time
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
		Status:      state.Status,
		Progress:    state.Progress,
		Result:      state.Result,
		Error:       state.Error,
		Tracker:     state.Tracker,
		LastCheckIn: state.LastCheckIn,
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
