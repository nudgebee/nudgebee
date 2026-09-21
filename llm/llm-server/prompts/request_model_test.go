package prompts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
)

// --- modelForRequest: per-request model vs env fallback ---

func TestModelForRequest_UsesContextValue(t *testing.T) {
	ctx := WithRequestModel(context.Background(), "qwen3-235b-vertex")
	assert.Equal(t, "qwen3-235b-vertex", modelForRequest(ctx))
}

func TestModelForRequest_NormalizesCasingAndWhitespace(t *testing.T) {
	// The "" case falls through to GetModelFromConfig(), which reads the
	// deployment-wide config.Config.LlmModel global -- stub it so the
	// expected "default" holds regardless of what other tests or the
	// environment set it to, matching the save/defer-restore pattern already
	// used elsewhere in this module for the same global (agents/core). This
	// only works because Go runs top-level tests in a package sequentially by
	// default -- do NOT add t.Parallel() here or to any other test in this
	// package that touches config.Config.LlmModel, or these mutations race.
	origModel := config.Config.LlmModel
	config.Config.LlmModel = ""
	defer func() { config.Config.LlmModel = origModel }()

	cases := map[string]string{
		"Qwen3-235B-Vertex": "qwen3-235b-vertex",
		"  gpt-4  ":         "gpt-4",
		"":                  "default",
	}
	for in, want := range cases {
		ctx := WithRequestModel(context.Background(), in)
		assert.Equal(t, want, modelForRequest(ctx), "input %q", in)
	}
}

func TestModelForRequest_FallsBackWithoutContextValue(t *testing.T) {
	// No per-request model attached: background jobs and startup validation
	// must keep the deployment-wide config default.
	assert.Equal(t, GetModelFromConfig(), modelForRequest(context.Background()))
	assert.Equal(t, GetModelFromConfig(), modelForRequest(nil)) //nolint:staticcheck // nil ctx is the documented degenerate case
}

func TestWithRequestModel_NilContextTolerated(t *testing.T) {
	ctx := WithRequestModel(nil, "qwen3-235b-vertex") //nolint:staticcheck // nil ctx is the documented degenerate case
	require.NotNil(t, ctx)
	assert.Equal(t, "qwen3-235b-vertex", modelForRequest(ctx))
}

func TestModelForRequest_EmptyValueFallsBack(t *testing.T) {
	ctx := WithRequestModel(context.Background(), "")
	assert.Equal(t, GetModelFromConfig(), modelForRequest(ctx))
}

// --- cache clone: metadata must survive a cache hit intact ---

func TestCache_HitPreservesExperimentMetadata(t *testing.T) {
	loader := &PromptLoader{
		db:    nil,
		cache: NewPromptCache(1 * time.Hour),
		fs:    embeddedFS,
	}
	req := PromptRequest{
		Name:      "k8s_lean",
		Category:  CategoryAgents,
		Model:     "default",
		AccountID: "acct-1",
	}

	first, err := loader.GetPrompt(context.Background(), req)
	require.NoError(t, err)

	// Simulate an experiment-attributed entry the way tier-1 resolution would
	// produce it. Seed a fresh copy rather than mutating `first`: GetPrompt hands
	// the same struct to an async metrics goroutine, so callers must not write it.
	name := "exp-test"
	seeded := &PromptResponse{Content: first.Content, Metadata: first.Metadata}
	seeded.Metadata.ExperimentName = &name
	loader.cache.Set(req, seeded)

	second, err := loader.GetPrompt(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, second.Metadata.CacheHit)
	require.NotNil(t, second.Metadata.ExperimentName, "cache clone must not drop ExperimentName")
	assert.Equal(t, "exp-test", *second.Metadata.ExperimentName)
	assert.Equal(t, first.Metadata.Version, second.Metadata.Version)
	assert.Equal(t, first.Metadata.ConfigSource, second.Metadata.ConfigSource)
}
