package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var evalTestedKnownProviders = map[string]bool{
	"anthropic": true, "bedrock": true, "googleai": true, "vertexai": true, "openai": true,
	"azure": true, "custom": true, "huggingface": true, "cloudflare": true,
}

func TestEvalTestedModelsCatalogIsValid(t *testing.T) {
	models, err := loadEvalTestedModels()
	require.NoError(t, err)
	require.NotEmpty(t, models)

	seen := map[string]bool{}
	for _, m := range models {
		assert.NotEmpty(t, m.Model)
		assert.NotEmpty(t, m.DisplayName, m.Model)
		assert.False(t, seen[m.Model], "duplicate model %s", m.Model)
		seen[m.Model] = true
		for _, d := range []string{m.DeprecatedOn, m.RetiresOn} {
			if d != "" {
				_, err := time.Parse(evalTestedDateLayout, d)
				assert.NoError(t, err, m.Model)
			}
		}
		require.NotEmpty(t, m.Providers, m.Model)
		for _, p := range m.Providers {
			assert.True(t, evalTestedKnownProviders[p.Provider], "%s: unknown provider %q", m.Model, p.Provider)
			require.NotEmpty(t, p.ModelIDs, m.Model)
			for _, id := range p.ModelIDs {
				assert.Equal(t, id, strings.TrimSpace(id), m.Model)
				if p.Provider == "bedrock" {
					assert.True(t, strings.HasPrefix(id, bedrockRegionPlaceholder+"."),
						"%s: a Bedrock name must start with %s so it matches the config region", m.Model, bedrockRegionPlaceholder)
				}
			}
		}
	}
}

func TestListEvalTestedModels_BedrockNamesFollowRegion(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	cases := map[string][]string{
		"eu-west-1":      {"eu.anthropic.claude-sonnet-4-6", "global.anthropic.claude-sonnet-4-6"},
		"us-east-1":      {"us.anthropic.claude-sonnet-4-6", "global.anthropic.claude-sonnet-4-6"},
		"ap-northeast-1": {"jp.anthropic.claude-sonnet-4-6", "apac.anthropic.claude-sonnet-4-6", "global.anthropic.claude-sonnet-4-6"},
		"us-gov-west-1":  {"us-gov.anthropic.claude-sonnet-4-6"},
		"":               {"global.anthropic.claude-sonnet-4-6"},
	}
	for region, want := range cases {
		options, err := ListEvalTestedModels("bedrock", region, false, now)
		require.NoError(t, err)
		var got []string
		for _, o := range options {
			if o.Model == "claude-sonnet-4-6" {
				for _, n := range o.Names {
					got = append(got, n.Name)
					assert.True(t, n.Tested)
				}
			}
		}
		assert.Equal(t, want, got, region)
	}
}

func TestListEvalTestedModels_FiltersByProviderSlotAndRetirement(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

	options, err := ListEvalTestedModels("custom", "", false, now)
	require.NoError(t, err)
	byModel := map[string]EvalTestedModelOption{}
	for _, o := range options {
		byModel[o.Model] = o
	}
	assert.Equal(t, []EvalTestedModelName{
		{Name: "vertex/claude-sonnet-4-6", Tested: true},
		{Name: "claude-sonnet-4-6", Tested: true},
		{Name: "anthropic/claude-sonnet-4.6", Tested: false},
	}, byModel["claude-sonnet-4-6"].Names)
	assert.NotContains(t, byModel, "gemini-3.8-flash")
	assert.NotContains(t, byModel, "clef")

	padded, err := ListEvalTestedModels(" Custom ", "", false, now)
	require.NoError(t, err)
	assert.Equal(t, options, padded)

	decision, err := ListEvalTestedModels("cloudflare", "", true, now)
	require.NoError(t, err)
	require.Len(t, decision, 1)
	assert.Equal(t, "clef", decision[0].Model)

	google, err := ListEvalTestedModels("googleai", "", false, now)
	require.NoError(t, err)
	for _, o := range google {
		if o.Model == "gemini-2.5-pro" {
			assert.True(t, o.Deprecated)
		}
		if o.Model == "gemini-3.8-flash" {
			assert.False(t, o.Deprecated)
		}
	}
}

func TestListEvalTestedModels_HidesRetiredModels(t *testing.T) {
	orig := loadEvalTestedModels
	t.Cleanup(func() { loadEvalTestedModels = orig })
	loadEvalTestedModels = func() ([]evalTestedModel, error) {
		return []evalTestedModel{
			{Model: "a", DisplayName: "A", Providers: []evalTestedProvider{{Provider: "googleai", ModelIDs: []string{"a"}}}},
			{Model: "b", DisplayName: "B", RetiresOn: "2026-10-21", Providers: []evalTestedProvider{{Provider: "googleai", ModelIDs: []string{"b"}}}},
		}, nil
	}

	before, err := ListEvalTestedModels("googleai", "", false, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	on, err := ListEvalTestedModels("googleai", "", false, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Len(t, on, len(before)-1)
}
