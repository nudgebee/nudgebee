package core

import (
	"errors"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func intentTask() ClassificationTask {
	return ClassificationTask{
		Name:         "test_intent",
		Instructions: "Classify the request.",
		Options: []ClassificationOption{
			{Label: "ANSWER", Description: "wants information"},
			{Label: "EDIT", Description: "wants a change"},
			{Label: "CREATE"},
		},
	}
}

// stubGenerate replaces the LLM call with scripted answers and records each call's messages.
func stubGenerate(t *testing.T, answers []string, err error) *[][]llms.MessageContent {
	t.Helper()
	calls := &[][]llms.MessageContent{}
	old := classificationGenerate
	classificationGenerate = func(_ *security.RequestContext, _, _, _, _, _ string, _ bool, msgs []llms.MessageContent, _ bool, _ ...llms.CallOption) (*llms.ContentResponse, error) {
		*calls = append(*calls, append([]llms.MessageContent(nil), msgs...))
		if err != nil {
			return nil, err
		}
		answer := answers[len(*calls)-1]
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: answer}}}, nil
	}
	t.Cleanup(func() { classificationGenerate = old })
	return calls
}

func TestClassify_LLMBackend(t *testing.T) {
	t.Run("valid answer is canonicalized", func(t *testing.T) {
		calls := stubGenerate(t, []string{" edit\n"}, nil)
		d, err := Classify(testRequestContext(), ClassificationCall{AgentId: "caller"}, intentTask(), "fix it")
		require.NoError(t, err)
		assert.True(t, d.Valid)
		assert.Equal(t, "EDIT", d.Label())
		assert.Equal(t, "llm", d.Backend)
		assert.Len(t, *calls, 1)
		system := (*calls)[0][0].Parts[0].(llms.TextContent).Text
		assert.Contains(t, system, "Classify the request.")
		assert.Contains(t, system, "- ANSWER: wants information")
		assert.True(t, strings.HasSuffix(system, "- CREATE"), system)
	})

	t.Run("invalid answer is retried with the bad answer fed back", func(t *testing.T) {
		calls := stubGenerate(t, []string{"It is an edit request", "EDIT"}, nil)
		d, err := Classify(testRequestContext(), ClassificationCall{}, intentTask(), "fix it")
		require.NoError(t, err)
		assert.Equal(t, "EDIT", d.Label())
		require.Len(t, *calls, 2)
		retry := (*calls)[1]
		assert.Len(t, retry, 4) // system, user, bad answer, correction
		assert.Contains(t, retry[3].Parts[0].(llms.TextContent).Text, "ANSWER, EDIT, CREATE")
	})

	t.Run("invalid after all attempts is Valid=false, not an error", func(t *testing.T) {
		calls := stubGenerate(t, []string{"maybe", "unsure", "no idea"}, nil)
		d, err := Classify(testRequestContext(), ClassificationCall{}, intentTask(), "hmm")
		require.NoError(t, err)
		assert.False(t, d.Valid)
		assert.Empty(t, d.Label())
		assert.Len(t, *calls, classificationLLMAttempts)
	})

	t.Run("provider error is returned", func(t *testing.T) {
		stubGenerate(t, nil, errors.New("boom"))
		_, err := Classify(testRequestContext(), ClassificationCall{}, intentTask(), "x")
		assert.ErrorContains(t, err, "boom")
	})

	t.Run("task without options is rejected", func(t *testing.T) {
		_, err := Classify(testRequestContext(), ClassificationCall{}, ClassificationTask{Name: "x"}, "x")
		assert.Error(t, err)
	})
}

func TestMatchClassificationLabels(t *testing.T) {
	single := intentTask()
	multi := intentTask()
	multi.Multi = true
	cases := []struct {
		task   ClassificationTask
		raw    string
		labels []string
		ok     bool
	}{
		{single, "answer", []string{"ANSWER"}, true},
		{single, "**EDIT**.", []string{"EDIT"}, true},
		{single, "EDIT, CREATE", nil, false},
		{single, "This is an EDIT", nil, false},
		{multi, "edit, create, EDIT", []string{"EDIT", "CREATE"}, true},
		{multi, "EDIT, DELETE", nil, false},
		{multi, " , ", nil, false},
	}
	for _, c := range cases {
		labels, ok := matchClassificationLabels(c.task, c.raw)
		assert.Equal(t, c.ok, ok, c.raw)
		assert.Equal(t, c.labels, labels, c.raw)
	}
}

// stubClassificationTier makes the classification tier resolve through source.
func stubClassificationTier(t *testing.T, source string) {
	t.Helper()
	old := classificationTierResolve
	classificationTierResolve = func(rc *security.RequestContext, _, _, _ string) (*LLMConfigResolution, error) {
		assert.Equal(t, ModelTierClassification, modelTierFromContext(rc), "probe asks about the classification tier")
		return &LLMConfigResolution{Source: source}, nil
	}
	t.Cleanup(func() { classificationTierResolve = old })
}

func TestClassificationTier(t *testing.T) {
	ctx := testRequestContext()
	call := ClassificationCall{AccountId: "acct"}
	for source, want := range map[string]ModelTier{
		"env-tier":              ModelTierClassification,
		"db-tier":               ModelTierClassification,
		"context-override-tier": ModelTierClassification,
		"conversation-tier":     ModelTierClassification,
		"db-global":             ModelTierSummary, // account default would win: use summary instead
		"env-global":            ModelTierSummary,
		"db-agent":              ModelTierSummary,
		"pinned:abc":            ModelTierSummary,
	} {
		stubClassificationTier(t, source)
		assert.Equal(t, want, classificationTier(ctx, call), source)
	}

	old := classificationTierResolve
	classificationTierResolve = func(*security.RequestContext, string, string, string) (*LLMConfigResolution, error) {
		return nil, errors.New("db down")
	}
	t.Cleanup(func() { classificationTierResolve = old })
	assert.Equal(t, ModelTierSummary, classificationTier(ctx, call), "resolution error falls back to summary")
}

func TestClassify_StampsTier(t *testing.T) {
	var tiers []ModelTier
	old := classificationGenerate
	classificationGenerate = func(rc *security.RequestContext, _, _, _, _, _ string, _ bool, _ []llms.MessageContent, _ bool, _ ...llms.CallOption) (*llms.ContentResponse, error) {
		tiers = append(tiers, modelTierFromContext(rc))
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: "EDIT"}}}, nil
	}
	t.Cleanup(func() { classificationGenerate = old })

	stubClassificationTier(t, "db-global")
	_, err := Classify(testRequestContext(), ClassificationCall{AccountId: "acct"}, intentTask(), "x")
	require.NoError(t, err)

	stubClassificationTier(t, "db-tier")
	_, err = Classify(testRequestContext(), ClassificationCall{AccountId: "acct"}, intentTask(), "x")
	require.NoError(t, err)

	_, err = Classify(testRequestContext(), ClassificationCall{AccountId: "acct", Tier: ModelTierRetrieval}, intentTask(), "x")
	require.NoError(t, err)

	assert.Equal(t, []ModelTier{ModelTierSummary, ModelTierClassification, ModelTierRetrieval}, tiers)
}

func TestClassificationTierUsesSummaryThinkingBudget(t *testing.T) {
	oldSummary, oldRetrieval := config.Config.LlmThinkingBudgetSummary, config.Config.LlmThinkingBudgetRetrieval
	t.Cleanup(func() {
		config.Config.LlmThinkingBudgetSummary, config.Config.LlmThinkingBudgetRetrieval = oldSummary, oldRetrieval
	})
	config.Config.LlmThinkingBudgetSummary, config.Config.LlmThinkingBudgetRetrieval = 512, 4096
	assert.Equal(t, 512, resolveThinkingBudget(ModelTierClassification))
}

func TestClassificationRetryPrompt(t *testing.T) {
	single := intentTask()
	assert.Contains(t, classificationRetryPrompt(single, "maybe"), "Respond with ONLY one of: ANSWER, EDIT, CREATE.")

	multi := intentTask()
	multi.Multi = true
	p := classificationRetryPrompt(multi, "EDIT and CREATE")
	assert.Contains(t, p, "one or more of these, separated by a comma and a space: ANSWER, EDIT, CREATE.")
	assert.NotContains(t, p, "ONLY one of:")
}

// TestClassificationTier_RealResolver runs classificationTier against the real
// ResolveLLMConfig, so renaming one of the tier-layer Source labels it relies on
// fails here instead of silently routing every decision to summary.
func TestClassificationTier_RealResolver(t *testing.T) {
	ctx := testRequestContext()
	pinGlobalModel(t, "googleai", "global-model")

	t.Run("nothing configured resolves as summary", func(t *testing.T) {
		assert.Equal(t, ModelTierSummary, classificationTier(ctx, ClassificationCall{}))
	})

	t.Run("server config defines the tier", func(t *testing.T) {
		setEnvKey(t, "llm_tier_provider_classification", "googleai")
		setEnvKey(t, "llm_tier_model_classification", "classification-model")
		assert.Equal(t, ModelTierClassification, classificationTier(ctx, ClassificationCall{}))
	})

	t.Run("account config defines the tier", func(t *testing.T) {
		seedDBConfig(t, "acct-classification-db-tier", map[string]string{
			"llm_tier_provider_classification": "googleai",
			"llm_tier_model_classification":    "classification-model",
		})
		assert.Equal(t, ModelTierClassification, classificationTier(ctx, ClassificationCall{AccountId: "acct-classification-db-tier"}))
	})

	t.Run("server-only tier does not override an account's own global model", func(t *testing.T) {
		setEnvKey(t, "llm_tier_provider_classification", "googleai")
		setEnvKey(t, "llm_tier_model_classification", "classification-model")
		seedDBConfig(t, "acct-classification-db-global", map[string]string{
			"llm_provider":   "googleai",
			"llm_model_name": "account-global-model",
		})
		assert.Equal(t, ModelTierSummary, classificationTier(ctx, ClassificationCall{AccountId: "acct-classification-db-global"}))
	})
}

func TestGetAllConfiguredModels_ListsClassificationTier(t *testing.T) {
	pinGlobalModel(t, "googleai", "global-model")
	setEnvKey(t, "llm_tier_provider_classification", "googleai")
	setEnvKey(t, "llm_tier_model_classification", "classification-model")

	got, err := GetAllConfiguredModels("")
	require.NoError(t, err)
	found := false
	for _, m := range got {
		if m.Model == "classification-model" {
			found = true
			assert.Equal(t, "env:tier:classification", m.ConfigSource)
		}
	}
	assert.True(t, found, "a configured classification model must appear in the model picker")
}
