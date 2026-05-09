package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitAnalysis(t *testing.T) {
	InitAnalysis("test-1")
	defer CleanupAnalysis("test-1")

	state := GetAnalysisState("test-1")
	require.NotNil(t, state)
	assert.Equal(t, "running", state.Status)
	assert.Empty(t, state.Progress)
	assert.Nil(t, state.Result)
	assert.Empty(t, state.Error)
}

func TestSetProgress(t *testing.T) {
	InitAnalysis("test-2")
	defer CleanupAnalysis("test-2")

	SetProgress("test-2", "Analyzing root cause...")
	state := GetAnalysisState("test-2")
	require.NotNil(t, state)
	assert.Equal(t, "Analyzing root cause...", state.Progress)
	assert.Equal(t, "running", state.Status)

	SetProgress("test-2", "Applying fix...")
	state = GetAnalysisState("test-2")
	assert.Equal(t, "Applying fix...", state.Progress)
}

func TestSetProgress_NoOpForUnknownID(t *testing.T) {
	// Should not panic
	SetProgress("nonexistent", "some progress")
	assert.Nil(t, GetAnalysisState("nonexistent"))
}

func TestCompleteAnalysis(t *testing.T) {
	InitAnalysis("test-3")
	defer CleanupAnalysis("test-3")

	result := map[string]string{"title": "Fix applied"}
	CompleteAnalysis("test-3", result)

	state := GetAnalysisState("test-3")
	require.NotNil(t, state)
	assert.Equal(t, "completed", state.Status)
	assert.Equal(t, result, state.Result)
}

func TestFailAnalysis(t *testing.T) {
	InitAnalysis("test-4")
	defer CleanupAnalysis("test-4")

	FailAnalysis("test-4", "LLM timeout")

	state := GetAnalysisState("test-4")
	require.NotNil(t, state)
	assert.Equal(t, "failed", state.Status)
	assert.Equal(t, "LLM timeout", state.Error)
}

func TestCleanupAnalysis(t *testing.T) {
	InitAnalysis("test-5")
	require.NotNil(t, GetAnalysisState("test-5"))

	CleanupAnalysis("test-5")
	assert.Nil(t, GetAnalysisState("test-5"))
}

func TestGetAnalysisState_NotFound(t *testing.T) {
	assert.Nil(t, GetAnalysisState("does-not-exist"))
}

func TestProgressPreservedAfterComplete(t *testing.T) {
	InitAnalysis("test-6")
	defer CleanupAnalysis("test-6")

	SetProgress("test-6", "Creating pull request...")
	CompleteAnalysis("test-6", "done")

	state := GetAnalysisState("test-6")
	require.NotNil(t, state)
	assert.Equal(t, "completed", state.Status)
	assert.Equal(t, "Creating pull request...", state.Progress)
}
