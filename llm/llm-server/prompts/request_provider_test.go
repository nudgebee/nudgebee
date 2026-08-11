package prompts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- providerForRequest: per-request provider vs env fallback ---

func TestProviderForRequest_UsesContextValue(t *testing.T) {
	ctx := WithRequestProvider(context.Background(), "bedrock")
	assert.Equal(t, "bedrock", providerForRequest(ctx))
}

func TestProviderForRequest_NormalizesConfigNames(t *testing.T) {
	cases := map[string]string{
		"aws_bedrock":  "bedrock",
		"azure_openai": "azure",
		"gemini":       "googleai",
		"GoogleAI":     "googleai",
		"vertex":       "vertexai",
		"unknown-name": "default",
	}
	for in, want := range cases {
		ctx := WithRequestProvider(context.Background(), in)
		assert.Equal(t, want, providerForRequest(ctx), "input %q", in)
	}
}

func TestProviderForRequest_FallsBackWithoutContextValue(t *testing.T) {
	// No per-request provider attached: background jobs and startup validation
	// must keep the deployment-wide config default.
	assert.Equal(t, GetProviderFromConfig(), providerForRequest(context.Background()))
	assert.Equal(t, GetProviderFromConfig(), providerForRequest(nil)) //nolint:staticcheck // nil ctx is the documented degenerate case
}

func TestWithRequestProvider_NilContextTolerated(t *testing.T) {
	ctx := WithRequestProvider(nil, "bedrock") //nolint:staticcheck // nil ctx is the documented degenerate case
	require.NotNil(t, ctx)
	assert.Equal(t, "bedrock", providerForRequest(ctx))
}

func TestProviderForRequest_EmptyValueFallsBack(t *testing.T) {
	ctx := WithRequestProvider(context.Background(), "")
	assert.Equal(t, GetProviderFromConfig(), providerForRequest(ctx))
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
		Provider:  "default",
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
