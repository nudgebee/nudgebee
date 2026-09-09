package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// NOTE: the former TestSummarizeContent_LargeInput live-provider integration test
// was removed — SummarizeContent's size-based branching is now covered hermetically
// (and in CI) by the tests in llm_message_summarization_unit_test.go.

// TestSummarizationChunkSize verifies the chunk size fills the window minus the
// reserved output (replacing the old maxTokens/2), stays within the window, and
// clamps safely on tiny/pathological windows.
func TestSummarizationChunkSize(t *testing.T) {
	cases := []struct {
		name      string
		maxTokens int
		model     string
		want      int
	}{
		{"32k, unknown model (floor output reserve)", 32000, "some-model", 32000 - DefaultMaxOutputTokensFloor - 512},
		{"32k, gpt-4o (16384 output reserve)", 32000, "gpt-4o", 32000 - 16384 - 512},
		{"tiny 1k window clamps reserve and hits floor", 1024, "some-model", 256},
		{"pathologically small 200 window caps to half", 200, "some-model", 100},
	}
	// Output reserves come from the pricing catalog (V878); pin the one this
	// test depends on so it stays hermetic.
	withFakeModelLimitsCatalog(t, map[string]modelTokenLimits{
		"openai:gpt-4o": {MaxOutput: 16384},
	})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarizationChunkSize(c.maxTokens, c.model)
			assert.Equal(t, c.want, got)
			assert.Less(t, got, c.maxTokens, "chunk must be smaller than the window")
			assert.Greater(t, got, 0, "chunk must be strictly positive")
		})
	}
}
