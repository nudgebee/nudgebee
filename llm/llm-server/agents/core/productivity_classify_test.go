package core

import (
	"nudgebee/llm/llms/classify"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// stubGenerate replaces the LLM call with scripted answers and records each call's messages.
func stubGenerate(t *testing.T, answers []string, err error) *[][]llms.MessageContent {
	t.Helper()
	calls := &[][]llms.MessageContent{}
	old := classify.ClassificationGenerate
	classify.ClassificationGenerate = func(_ *security.RequestContext, _, _, _, _, _ string, _ bool, msgs []llms.MessageContent, _ bool, _ ...llms.CallOption) (*llms.ContentResponse, error) {
		*calls = append(*calls, append([]llms.MessageContent(nil), msgs...))
		if err != nil {
			return nil, err
		}
		answer := answers[len(*calls)-1]
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: answer}}}, nil
	}
	t.Cleanup(func() { classify.ClassificationGenerate = old })
	return calls
}

// stubClassificationTier makes the classification tier resolve through source.
func stubClassificationTier(t *testing.T, source string) {
	t.Helper()
	old := classify.ClassificationTierResolve
	classify.ClassificationTierResolve = func(rc *security.RequestContext, _, _, _ string) (*llmruntime.LLMConfigResolution, error) {
		assert.Equal(t, llmruntime.ModelTierClassification, llmruntime.ModelTierFromContext(rc), "probe asks about the classification tier")
		return &llmruntime.LLMConfigResolution{Source: source}, nil
	}
	t.Cleanup(func() { classify.ClassificationTierResolve = old })
}

func TestClassifyMessage_UsesClassifyAndKeepsFallback(t *testing.T) {
	stubClassificationTier(t, "db-global")
	var agentIDs []string
	var tiers []llmruntime.ModelTier
	answers := []string{"Investigation"}
	old := classify.ClassificationGenerate
	classify.ClassificationGenerate = func(rc *security.RequestContext, _, _, _, _, agentId string, _ bool, _ []llms.MessageContent, _ bool, _ ...llms.CallOption) (*llms.ContentResponse, error) {
		agentIDs = append(agentIDs, agentId)
		tiers = append(tiers, llmruntime.ModelTierFromContext(rc))
		require.NotEmpty(t, answers, "Classify made more LLM calls than this test scripted")
		a := answers[0]
		answers = answers[1:]
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: a}}}, nil
	}
	t.Cleanup(func() { classify.ClassificationGenerate = old })

	msg := ConversationMessage{Message: "why is checkout slow?"}
	got, err := ClassifyMessage(testRequestContext(), "u", "a", "c", "m", msg, nil)
	require.NoError(t, err)
	assert.Equal(t, ClassificationInvestigation, got)
	assert.Equal(t, []string{ClassificationInvestigation}, agentIDs, "agent id keeps keying LLM config and cost")
	assert.Equal(t, []llmruntime.ModelTier{llmruntime.ModelTierSummary}, tiers, "unconfigured classification tier resolves as summary")

	answers = []string{"hmm", "not sure", "no idea"}
	got, err = ClassifyMessage(testRequestContext(), "u", "a", "c", "m", msg, nil)
	require.NoError(t, err)
	assert.Equal(t, ClassificationOthers, got, "an invalid answer still maps to others")
}
