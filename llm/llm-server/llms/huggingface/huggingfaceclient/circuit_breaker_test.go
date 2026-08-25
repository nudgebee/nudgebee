package huggingfaceclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunInferenceTripsCircuitAfterRepeatedUnavailable verifies the client trips the
// breaker and bails after circuitTripThreshold consecutive 503s, instead of retrying
// for the full budget.
func TestRunInferenceTripsCircuitAfterRepeatedUnavailable(t *testing.T) {
	config.Config.LlmServerLlmInitialBackoffSeconds = 0
	config.Config.LlmServerGlobalRetryBudgetMinutes = 1
	config.Config.LlmServerMaxIndividualCallTimeoutMinutes = 1

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"503 Service Unavailable"}`))
	}))
	defer srv.Close()

	var tripped int
	prevRec, prevOpen := RecordUnavailable, IsCircuitOpen
	RecordUnavailable = func(string) { tripped++ }
	IsCircuitOpen = nil
	t.Cleanup(func() { RecordUnavailable, IsCircuitOpen = prevRec, prevOpen })

	c, err := NewWithAPIType("tok", "Qwen/Test", srv.URL, "", "openai")
	require.NoError(t, err)

	_, err = c.RunInference(context.Background(), &InferenceRequest{
		Model: "Qwen/Test", Prompt: "hi", Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit tripped")
	assert.Equal(t, 1, tripped, "breaker should trip exactly once")
	assert.Equal(t, circuitTripThreshold, hits, "should bail after the trip threshold, not retry the full budget")
}

// TestRunInferenceFailsFastWhenCircuitOpen verifies an already-open circuit is not
// even dialed.
func TestRunInferenceFailsFastWhenCircuitOpen(t *testing.T) {
	config.Config.LlmServerGlobalRetryBudgetMinutes = 1
	config.Config.LlmServerMaxIndividualCallTimeoutMinutes = 1

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	prevOpen := IsCircuitOpen
	IsCircuitOpen = func(string) bool { return true }
	t.Cleanup(func() { IsCircuitOpen = prevOpen })

	c, _ := NewWithAPIType("tok", "Qwen/Test", srv.URL, "", "openai")
	_, err := c.RunInference(context.Background(), &InferenceRequest{Model: "Qwen/Test", Prompt: "hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit open")
	assert.Equal(t, 0, hits, "must not hit the endpoint when the circuit is open")
}
