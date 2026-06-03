package common

import "sync"

// AnalysisState tracks the progress and result of an async analysis.
type AnalysisState struct {
	Status   string // "running", "completed", "failed"
	Progress string // Current progress text
	Result   any    // Final response (set on completion)
	Error    string // Error message (set on failure)
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
		state.Progress = text
	}
}

// CompleteAnalysis marks an analysis as completed with its result.
func CompleteAnalysis(analysisID string, result any) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.Result = result
		state.Status = "completed"
	}
}

// FailAnalysis marks an analysis as failed with an error message.
func FailAnalysis(analysisID string, errMsg string) {
	if v, ok := progressStore.Load(analysisID); ok {
		state := v.(*AnalysisState)
		state.Error = errMsg
		state.Status = "failed"
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
