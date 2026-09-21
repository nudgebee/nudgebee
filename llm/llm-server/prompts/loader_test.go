package prompts

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
)

// newTestLoader creates a PromptLoader backed by embeddedFS with no DB or cache warming.
func newTestLoader() *PromptLoader {
	return &PromptLoader{
		db:    nil,
		cache: NewPromptCache(1 * time.Hour),
		fs:    embeddedFS,
	}
}

// --- Basic loading ---

func TestGetPrompt_BasicLoad(t *testing.T) {
	loader := newTestLoader()
	resp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: CategoryAgents,
		Model:    "default",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Content)
	assert.Equal(t, "v1", resp.Metadata.Version)
	assert.Equal(t, "default", resp.Metadata.Model)
	assert.Equal(t, CategoryAgents, resp.Metadata.Category)
	assert.Equal(t, ConfigSourceDefault, resp.Metadata.ConfigSource)
	assert.False(t, resp.Metadata.CacheHit)
}

func TestGetPrompt_MissingName(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "",
		Category: CategoryAgents,
		Model:    "default",
	})
	assert.ErrorContains(t, err, "name is required")
}

func TestGetPrompt_MissingCategory(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: "",
		Model:    "default",
	})
	assert.ErrorContains(t, err, "category is required")
}

func TestGetPrompt_InvalidCategory(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: PromptCategory("invalid"),
		Model:    "default",
	})
	assert.ErrorContains(t, err, "invalid category")
}

func TestGetPrompt_UnknownPromptName(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "nonexistent_prompt_xyz",
		Category: CategoryAgents,
		Model:    "default",
	})
	assert.Error(t, err)
}

// --- Model normalization and fallback ---

func TestGetPrompt_EmptyModelNormalizesToDefault(t *testing.T) {
	loader := newTestLoader()
	resp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: CategoryAgents,
		Model:    "", // should normalize to "default"
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Content)
	assert.Equal(t, "default", resp.Metadata.Model)
}

func TestGetPrompt_ModelOverrideWinsOverDefault(t *testing.T) {
	// The core contract this whole scheme exists for: when a models/<model>
	// override file exists, GetPrompt must serve it instead of default/ for a
	// request configured with that model — and must NOT leak that override
	// to a request for any other model.
	testFS := fstest.MapFS{
		"default/v1/agents/foo.yaml": &fstest.MapFile{Data: []byte(
			"apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2-\n  default body\n")},
		"models/qwen3-235b-vertex/v1/agents/foo.yaml": &fstest.MapFile{Data: []byte(
			"apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2-\n  qwen-specific body\n")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	qwenResp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name: "foo", Category: CategoryAgents, Model: "qwen3-235b-vertex",
	})
	require.NoError(t, err)
	assert.Equal(t, "qwen-specific body", qwenResp.Content, "a configured model with an override file must get that file, not default")

	otherResp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name: "foo", Category: CategoryAgents, Model: "gpt-4",
	})
	require.NoError(t, err)
	assert.Equal(t, "default body", otherResp.Content, "a model with no override must still get default, not another model's override")

	defaultResp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name: "foo", Category: CategoryAgents, Model: "default",
	})
	require.NoError(t, err)
	assert.Equal(t, "default body", defaultResp.Content)
}

func TestGetPrompt_ExactOverrideWinsOverFamily(t *testing.T) {
	// When a deployment has BOTH its own exact override and a family override
	// exists for its model line, the exact one must win -- family is the
	// least specific tier (see modelResolutionBases). A sibling deployment in
	// the same family with no exact file of its own must still get the
	// family override, not default.
	testFS := fstest.MapFS{
		"default/v1/agents/foo.yaml": &fstest.MapFile{Data: []byte(
			"apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2-\n  default body\n")},
		"models/qwen/v1/agents/foo.yaml": &fstest.MapFile{Data: []byte(
			"apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2-\n  qwen family body\n")},
		"models/qwen3-235b-vertex/v1/agents/foo.yaml": &fstest.MapFile{Data: []byte(
			"apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2-\n  qwen3-235b-vertex exact body\n")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	exactResp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name: "foo", Category: CategoryAgents, Model: "qwen3-235b-vertex",
	})
	require.NoError(t, err)
	assert.Equal(t, "qwen3-235b-vertex exact body", exactResp.Content,
		"a deployment with its own exact override must get that file, not its family's")

	familyResp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name: "foo", Category: CategoryAgents, Model: "qwen/qwen3-vl-235b-a22b-instruct",
	})
	require.NoError(t, err)
	assert.Equal(t, "qwen family body", familyResp.Content,
		"a sibling deployment in the same family with no exact override must still get the family override")
}

func TestGetPrompt_ModelFallsBackToDefault(t *testing.T) {
	// No model-specific or family override exists yet for these — should fall
	// back to default. Any model containing "qwen" is excluded: it resolves to
	// the real family override under models/qwen/ (see
	// TestGetPrompt_QwenOverrideDiffersFromDefault below).
	models := []string{"gpt-4", "claude-3-5-sonnet", "gemini-2.5-pro"}
	loader := newTestLoader()

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			resp, err := loader.GetPrompt(context.Background(), PromptRequest{
				Name:     "k8s_lean",
				Category: CategoryAgents,
				Model:    model,
			})
			require.NoError(t, err, "model %q should fall back to default", model)
			assert.NotEmpty(t, resp.Content)

			// Content should match the default file
			defaultResp, _ := loader.GetPrompt(context.Background(), PromptRequest{
				Name:     "k8s_lean",
				Category: CategoryAgents,
				Model:    "default",
			})
			assert.Equal(t, defaultResp.Content, resp.Content,
				"model %q should return same content as default", model)
		})
	}
}

// TestGetPrompt_QwenOverrideDiffersFromDefault guards the real, embedded
// models/qwen/ family override tree (not a synthetic fixture): the agent
// prompt gets the scaling-investigation fragment inlined, and the shared planner
// base carries the qwen-only guardrails, both of which default/ must NOT have.
// Checked against two different real Qwen deployment strings to prove this is
// family matching (any model containing "qwen"), not one exact string.
func TestGetPrompt_QwenOverrideDiffersFromDefault(t *testing.T) {
	loader := newTestLoader()

	for _, model := range []string{"qwen3-235b-vertex", "qwen/qwen3-vl-235b-a22b-instruct"} {
		t.Run(model, func(t *testing.T) {
			agentResp, err := loader.GetPrompt(context.Background(), PromptRequest{
				Name: "k8s_lean", Category: CategoryAgents, Model: model,
			})
			require.NoError(t, err)
			assert.Contains(t, agentResp.Content, "ScalingLimited")

			plannerResp, err := loader.GetPrompt(context.Background(), PromptRequest{
				Name: "react_3_base", Category: CategoryPlanners, Model: model,
			})
			require.NoError(t, err)
			assert.Contains(t, plannerResp.Content, "Tool relevance gating")
		})
	}

	defaultAgentResp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name: "k8s_lean", Category: CategoryAgents, Model: "default",
	})
	require.NoError(t, err)
	assert.NotContains(t, defaultAgentResp.Content, "ScalingLimited")

	defaultPlannerResp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name: "react_3_base", Category: CategoryPlanners, Model: "default",
	})
	require.NoError(t, err)
	assert.NotContains(t, defaultPlannerResp.Content, "Tool relevance gating")
}

func TestModelFamily(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  string
	}{
		{"qwen3-235b-vertex", "qwen"},
		{"Qwen/Qwen3.6-35B-A3B-FP8", "qwen"},
		{"qwen/qwen3-vl-235b-a22b-instruct", "qwen"},
		{"gpt-4", ""},
		{"claude-3-5-sonnet", ""},
		{"", ""},
	} {
		t.Run(tc.model, func(t *testing.T) {
			assert.Equal(t, tc.want, modelFamily(tc.model))
		})
	}
}

// TestModelResolutionBases_PriorityOrder asserts the exact > canonical >
// family > default specificity ladder: a tier only appears once, and a more
// specific tier that matches a less specific one is not duplicated.
func TestModelResolutionBases_PriorityOrder(t *testing.T) {
	assert.Equal(t, []string{"default"}, modelResolutionBases("default"))

	// Unnormalized empty string: no caller passes this today (GetPrompt and
	// GetAvailableVersions both normalize first), but the helper must not
	// construct a bogus "models/" lookup if one ever does.
	assert.Equal(t, []string{"default"}, modelResolutionBases(""))

	// No canonical or family match: just exact then default.
	assert.Equal(t, []string{"models/gpt-4", "default"}, modelResolutionBases("gpt-4"))

	// Family match, no distinct canonical form.
	assert.Equal(t,
		[]string{"models/qwen/qwen3-vl-235b-a22b-instruct", "models/qwen", "default"},
		modelResolutionBases("qwen/qwen3-vl-235b-a22b-instruct"))

	// Exact request IS the family name: family tier must not duplicate it.
	assert.Equal(t, []string{"models/qwen", "default"}, modelResolutionBases("qwen"))
}

// --- Cache behaviour ---

func TestCache_HitOnSecondLoad(t *testing.T) {
	loader := newTestLoader()
	req := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Model: "default"}

	resp1, err := loader.GetPrompt(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, resp1.Metadata.CacheHit)

	resp2, err := loader.GetPrompt(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, resp2.Metadata.CacheHit)
	assert.Equal(t, resp1.Content, resp2.Content)
}

func TestCache_AccountIsolation(t *testing.T) {
	loader := newTestLoader()
	req1 := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Model: "default", AccountID: "acc-1"}
	req2 := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Model: "default", AccountID: "acc-2"}

	loader.GetPrompt(context.Background(), req1) //nolint
	loader.GetPrompt(context.Background(), req2) //nolint

	// Clear only account-1
	loader.ClearCacheForAccount("acc-1")

	r1, _ := loader.GetPrompt(context.Background(), req1)
	r2, _ := loader.GetPrompt(context.Background(), req2)
	assert.False(t, r1.Metadata.CacheHit, "acc-1 cache should be cleared")
	assert.True(t, r2.Metadata.CacheHit, "acc-2 cache should still be warm")
}

func TestCache_PromptIsolation(t *testing.T) {
	loader := newTestLoader()
	req1 := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Model: "default"}
	req2 := PromptRequest{Name: "k8s_native", Category: CategoryAgents, Model: "default"}

	loader.GetPrompt(context.Background(), req1) //nolint
	loader.GetPrompt(context.Background(), req2) //nolint

	loader.ClearCacheForPrompt("k8s_lean", CategoryAgents)

	r1, _ := loader.GetPrompt(context.Background(), req1)
	r2, _ := loader.GetPrompt(context.Background(), req2)
	assert.False(t, r1.Metadata.CacheHit)
	assert.True(t, r2.Metadata.CacheHit)
}

func TestCache_ClearAll(t *testing.T) {
	loader := newTestLoader()
	req := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Model: "default"}

	loader.GetPrompt(context.Background(), req) //nolint
	loader.ClearCache()

	resp, err := loader.GetPrompt(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, resp.Metadata.CacheHit)
}

func TestCache_Expiration(t *testing.T) {
	loader := &PromptLoader{
		db:    nil,
		cache: NewPromptCache(100 * time.Millisecond),
		fs:    embeddedFS,
	}
	req := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Model: "default"}

	loader.GetPrompt(context.Background(), req) //nolint
	time.Sleep(150 * time.Millisecond)

	resp, err := loader.GetPrompt(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, resp.Metadata.CacheHit, "entry should have expired")
}

// --- All categories load correctly ---

func TestAllCategories_SampleLoad(t *testing.T) {
	loader := newTestLoader()
	tests := []struct {
		name     string
		category PromptCategory
	}{
		{"k8s_lean", CategoryAgents},
		{"react_3_base", CategoryPlanners},
		{"react_3_custom_base", CategoryPlanners},
		{"react_4_custom_base", CategoryPlanners},
		{"remediation_generate", CategoryTools},
		{"response_formatter", CategoryUtilities},
		{"time_handling_rules", CategoryFragments},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s", tt.category, tt.name), func(t *testing.T) {
			resp, err := loader.GetPrompt(context.Background(), PromptRequest{
				Name:     tt.name,
				Category: tt.category,
				Model:    "default",
			})
			require.NoError(t, err)
			assert.NotEmpty(t, resp.Content)
		})
	}
}

// --- Content quality ---

func TestPromptContent_MinimumLength(t *testing.T) {
	const minLength = 20

	err := fs.WalkDir(embeddedFS, "default/v1", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".txt") {
			return err
		}
		t.Run(path, func(t *testing.T) {
			data, err := fs.ReadFile(embeddedFS, path)
			require.NoError(t, err)
			content := strings.TrimSpace(string(data))
			assert.GreaterOrEqual(t, len(content), minLength,
				"prompt file %q is too short (%d chars)", path, len(content))
		})
		return nil
	})
	require.NoError(t, err)
}

func TestPromptContent_NoTODOMarkers(t *testing.T) {
	err := fs.WalkDir(embeddedFS, "default/v1", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".txt") {
			return err
		}
		t.Run(path, func(t *testing.T) {
			data, _ := fs.ReadFile(embeddedFS, path)
			assert.NotContains(t, strings.ToUpper(string(data)), "TODO",
				"prompt file %q contains TODO marker", path)
		})
		return nil
	})
	require.NoError(t, err)
}

// --- GetAvailableVersions ---

func TestGetAvailableVersions(t *testing.T) {
	loader := newTestLoader()
	versions := loader.GetAvailableVersions("k8s_lean", CategoryAgents, "default")
	assert.NotEmpty(t, versions)
	assert.Contains(t, versions, "v1")
}

// --- forcedVersionFor ---

func TestForcedVersionFor(t *testing.T) {
	t.Run("nothing set returns empty", func(t *testing.T) {
		t.Setenv("PROMPTS_VERSION", "")

		assert.Equal(t, "", forcedVersionFor("some_prompt"))
	})

	t.Run("global PROMPTS_VERSION applies to any prompt", func(t *testing.T) {
		t.Setenv("PROMPTS_VERSION", "v3")

		assert.Equal(t, "v3", forcedVersionFor("some_prompt"))
	})

	t.Run("per-prompt env var takes precedence over global", func(t *testing.T) {
		t.Setenv("PROMPTS_VERSION", "v3")
		t.Setenv("PROMPTS_VERSION_K8S_LEAN", "v1")

		assert.Equal(t, "v1", forcedVersionFor("k8s_lean"), "per-prompt pin should win over the global value")
		assert.Equal(t, "v3", forcedVersionFor("other_prompt"), "prompts without a per-prompt override still get the global value")
	})

	t.Run("hyphenated prompt name normalizes to underscore for the env key", func(t *testing.T) {
		t.Setenv("PROMPTS_VERSION", "")
		// No registered prompt name has a hyphen today, but env vars can't
		// carry one in most shells -- PROMPTS_VERSION_K8S-LEAN isn't a
		// settable variable, so the lookup key must normalize it.
		t.Setenv("PROMPTS_VERSION_K8S_LEAN", "v2")

		assert.Equal(t, "v2", forcedVersionFor("k8s-lean"))
	})
}

// --- PROMPTS_VERSION / PROMPTS_VERSION_<NAME> dev override ---

func TestResolveConfig_ForcedVersionDevOverride_GlobalAppliesAcrossPrompts(t *testing.T) {
	t.Setenv("PROMPTS_VERSION", "v3")

	testFS := fstest.MapFS{
		"default/v1/agents/foo.yaml": &fstest.MapFile{Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2\n  v1 content\n")},
		"default/v3/agents/foo.yaml": &fstest.MapFile{Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2\n  v3 content\n")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	resp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "foo",
		Category: CategoryAgents,
		Model:    "default",
	})
	require.NoError(t, err)
	assert.Equal(t, "v3", resp.Metadata.Version)
	assert.Equal(t, ConfigSourceForcedDev, resp.Metadata.ConfigSource)
	assert.Contains(t, resp.Content, "v3 content")
}

func TestResolveConfig_ForcedVersionDevOverride_PerPromptPinsDownBelowGlobal(t *testing.T) {
	t.Setenv("PROMPTS_VERSION", "v3")
	t.Setenv("PROMPTS_VERSION_FOO", "v1")

	testFS := fstest.MapFS{
		"default/v1/agents/foo.yaml": &fstest.MapFile{Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2\n  v1 content\n")},
		"default/v3/agents/foo.yaml": &fstest.MapFile{Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2\n  v3 content\n")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	resp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "foo",
		Category: CategoryAgents,
		Model:    "default",
	})
	require.NoError(t, err)
	assert.Equal(t, "v1", resp.Metadata.Version, "the per-prompt pin holds foo at v1 even though a v3 exists and the global default is v3")
	assert.Contains(t, resp.Content, "v1 content")
}

func TestResolveConfig_ForcedVersionDevOverride_MissingVersionFallsThroughToV1(t *testing.T) {
	t.Setenv("PROMPTS_VERSION", "v9") // no v9 file exists anywhere for k8s_lean

	// loadPromptFile's resolution chain ({model}/{v} -> default/{v} ->
	// {model}/v1 -> default/v1) still applies after a forced version is
	// chosen, so a typo'd/nonexistent PROMPTS_VERSION degrades to v1 content
	// rather than failing the request -- the same graceful fallback every
	// other resolution path already relies on, not a new failure mode.
	loader := newTestLoader()
	resp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: CategoryAgents,
		Model:    "default",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Content)
	// The reported version must reflect what was actually served (v1), not
	// the requested-but-nonexistent v9 -- a typo'd pin must not be able to
	// report "using forced version v9" while silently serving v1 content.
	assert.Equal(t, "v1", resp.Metadata.Version)
}

func TestResolveConfig_ForcedVersionDevOverride_OffByDefault(t *testing.T) {
	assert.Equal(t, "", config.Config.PromptsVersion,
		"must default to empty -- production behavior depends on this never being set unless explicitly configured")
}

// --- SplitPromptLines ---

func TestSplitPromptLines(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{"empty", "", []string{}},
		{"whitespace only", "   \n\t  \n  ", []string{}},
		{"single line", "hello", []string{"hello"}},
		{"multiple lines", "line1\nline2\nline3", []string{"line1", "line2", "line3"}},
		{"leading trailing whitespace", "  \nline1\nline2\n  ", []string{"line1", "line2"}},
		{"trailing newline", "line1\nline2\n", []string{"line1", "line2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, SplitPromptLines(tt.content))
		})
	}
}

// --- PromptCache unit tests ---

func TestPromptCache_SetGet(t *testing.T) {
	cache := NewPromptCache(1 * time.Hour)
	req := PromptRequest{Name: "test", Category: CategoryAgents, Model: "default"}
	resp := &PromptResponse{Content: "hello", Metadata: PromptMetadata{Version: "v1"}}

	cache.Set(req, resp)
	cached, hit := cache.Get(req)
	assert.True(t, hit)
	assert.Equal(t, resp.Content, cached.Content)
}

func TestPromptCache_Miss(t *testing.T) {
	cache := NewPromptCache(1 * time.Hour)
	req := PromptRequest{Name: "test", Category: CategoryAgents, Model: "default"}
	cached, hit := cache.Get(req)
	assert.False(t, hit)
	assert.Nil(t, cached)
}

func TestPromptCache_Size(t *testing.T) {
	cache := NewPromptCache(1 * time.Hour)
	assert.Equal(t, 0, cache.Size())
	for i := 0; i < 5; i++ {
		req := PromptRequest{Name: "test", Category: CategoryAgents, Model: "default", AccountID: fmt.Sprintf("acc-%d", i)}
		cache.Set(req, &PromptResponse{Content: "x"})
	}
	assert.Equal(t, 5, cache.Size())
	cache.Clear()
	assert.Equal(t, 0, cache.Size())
}

func TestPromptCache_Expiration(t *testing.T) {
	cache := NewPromptCache(100 * time.Millisecond)
	req := PromptRequest{Name: "test", Category: CategoryAgents, Model: "default"}
	cache.Set(req, &PromptResponse{Content: "x"})

	_, hit := cache.Get(req)
	assert.True(t, hit)

	time.Sleep(150 * time.Millisecond)
	_, hit = cache.Get(req)
	assert.False(t, hit, "entry should have expired")
}

// --- loadPromptFile version fallback ---

func TestLoadPromptFile_FallbackToV1ResolvesIncludesAtV1NotRequestedVersion(t *testing.T) {
	// A request for a version that has no file at all for this prompt falls
	// through to the default/v1 base -- but that v1 body still has an
	// unresolved {{@include ...}}. It must resolve against v1 (where the
	// match actually happened), not against the originally-requested
	// version (which has no fragment file and would otherwise fail the
	// whole load even though a perfectly good v1 body + fragment exist).
	testFS := fstest.MapFS{
		"default/v1/agents/foo.yaml":     &fstest.MapFile{Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: foo\ncategory: agents\ninputs: {}\nbody: |2-\n  before {{@include _fragments/bar}} after\n")},
		"default/v1/_fragments/bar.yaml": &fstest.MapFile{Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: bar\ninputs: {}\nbody: |2-\n  BAR\n")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	content, servedVersion, err := loader.loadPromptFile("foo", CategoryAgents, "default", "v9")
	require.NoError(t, err)
	assert.Equal(t, "before BAR after", content)
	assert.Equal(t, "v1", servedVersion, "must report the version actually served, not the requested v9")
}

// --- Include processing ---

func TestProcessIncludes_BasicResolve(t *testing.T) {
	testFS := fstest.MapFS{
		"default/v2/_persona/greeting.txt": &fstest.MapFile{Data: []byte("Hello from partial")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	result, err := loader.processIncludes("Before {{@include _persona/greeting.txt}} After", "default", "v2", 0)
	require.NoError(t, err)
	assert.Equal(t, "Before Hello from partial After", result)
}

func TestProcessIncludes_MissingFileReturnsError(t *testing.T) {
	testFS := fstest.MapFS{}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	_, err := loader.processIncludes("{{@include _persona/nonexistent.txt}}", "default", "v2", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "include file not found")
}

func TestProcessIncludes_RecursionLimitEnforced(t *testing.T) {
	testFS := fstest.MapFS{
		"default/v2/_persona/a.txt": &fstest.MapFile{Data: []byte("{{@include _persona/b.txt}}")},
		"default/v2/_persona/b.txt": &fstest.MapFile{Data: []byte("{{@include _persona/c.txt}}")},
		"default/v2/_persona/c.txt": &fstest.MapFile{Data: []byte("{{@include _persona/d.txt}}")},
		"default/v2/_persona/d.txt": &fstest.MapFile{Data: []byte("should not reach here")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	_, err := loader.processIncludes("{{@include _persona/a.txt}}", "default", "v2", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "include depth limit exceeded")
}

func TestProcessIncludes_NoDirectivesPassThrough(t *testing.T) {
	loader := &PromptLoader{fs: fstest.MapFS{}, cache: NewPromptCache(1 * time.Hour)}

	content := "This has no includes, just plain text."
	result, err := loader.processIncludes(content, "default", "v2", 0)
	require.NoError(t, err)
	assert.Equal(t, content, result)
}

func TestProcessIncludes_GoTemplateVariablesUnaffected(t *testing.T) {
	testFS := fstest.MapFS{
		"default/v2/_persona/persona.txt": &fstest.MapFile{Data: []byte("I am Nubi")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	content := "Date: {{.today}} {{@include _persona/persona.txt}} Tools: {{.tool_names}}"
	result, err := loader.processIncludes(content, "default", "v2", 0)
	require.NoError(t, err)
	assert.Equal(t, "Date: {{.today}} I am Nubi Tools: {{.tool_names}}", result)
}

func TestProcessIncludes_ModelFallbackToDefault(t *testing.T) {
	testFS := fstest.MapFS{
		"default/v2/_persona/shared.txt": &fstest.MapFile{Data: []byte("default content")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	// Request with model "qwen3-235b-vertex" — file only exists in default, should fall back
	result, err := loader.processIncludes("{{@include _persona/shared.txt}}", "qwen3-235b-vertex", "v2", 0)
	require.NoError(t, err)
	assert.Equal(t, "default content", result)
}

func TestProcessIncludes_ModelSpecificOverridesDefault(t *testing.T) {
	testFS := fstest.MapFS{
		"models/custom/v2/_persona/shared.txt": &fstest.MapFile{Data: []byte("custom content")},
		"default/v2/_persona/shared.txt":       &fstest.MapFile{Data: []byte("default content")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	result, err := loader.processIncludes("{{@include _persona/shared.txt}}", "custom", "v2", 0)
	require.NoError(t, err)
	assert.Equal(t, "custom content", result)
}

func TestProcessIncludes_V2PromptFilesResolve(t *testing.T) {
	// Verify that actual embedded v2 prompt files with includes resolve successfully
	loader := newTestLoader()

	prompts := []struct {
		name     string
		contains string
	}{
		// response_formatter/react_base v2 were stale forks removed during the prompts_repo
		// migration; the slack formatter is the remaining v2 carrying an include.
		{"response_formatter_slack", "teammate on Slack"},
	}

	for _, p := range prompts {
		t.Run(p.name, func(t *testing.T) {
			content, _, err := loader.loadPromptFile(p.name, CategoryUtilities, "default", "v2")
			require.NoError(t, err)
			assert.Contains(t, content, p.contains,
				"resolved prompt should contain persona content")
			assert.NotContains(t, content, "{{@include",
				"resolved prompt should not contain unresolved include directives")
		})
	}
}

// --- ValidateRequest ---

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     PromptRequest
		wantErr string
	}{
		{
			name:    "valid",
			req:     PromptRequest{Name: "test", Category: CategoryAgents, Model: "default"},
			wantErr: "",
		},
		{
			name:    "missing name",
			req:     PromptRequest{Category: CategoryAgents, Model: "default"},
			wantErr: "name is required",
		},
		{
			name:    "missing category",
			req:     PromptRequest{Name: "test", Model: "default"},
			wantErr: "category is required",
		},
		{
			name:    "invalid category",
			req:     PromptRequest{Name: "test", Category: "bad", Model: "default"},
			wantErr: "invalid category",
		},
	}
	l := &PromptLoader{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateRequest(tt.req)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

// TestLoadPromptFile_KeepsMostDescriptiveError pins the error the resolution chain
// reports when nothing loads. Paths are walked most-specific first, so a malformed
// override explains the failure while the "does not exist" misses that follow are
// noise; reporting the latter sends whoever is debugging to the wrong file.
func TestLoadPromptFile_KeepsMostDescriptiveError(t *testing.T) {
	// Only a malformed model-specific v1 override exists — every other path in the chain misses.
	testFS := fstest.MapFS{
		"models/gpt-4/v1/agents/broken.yaml": &fstest.MapFile{
			Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: broken\n  bad: indentation\n"),
		},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	_, _, err := loader.loadPromptFile("broken", CategoryAgents, "gpt-4", "v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding prompt file",
		"the malformed override must be reported, not the later file-not-found misses")
	assert.NotContains(t, err.Error(), "file does not exist",
		"a generic miss must not mask the parse failure")
}

func TestGlobalLoaderUsesInstalledTestLoader(t *testing.T) {
	old := globalLoader
	t.Cleanup(func() { SetGlobalLoaderForTesting(old) })
	loader := NewLoaderForTesting()
	SetGlobalLoaderForTesting(loader)
	require.Same(t, loader, GetLoader())
}
