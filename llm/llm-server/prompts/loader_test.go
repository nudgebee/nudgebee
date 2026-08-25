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
		Provider: "default",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Content)
	assert.Equal(t, "v1", resp.Metadata.Version)
	assert.Equal(t, "default", resp.Metadata.Provider)
	assert.Equal(t, CategoryAgents, resp.Metadata.Category)
	assert.Equal(t, ConfigSourceDefault, resp.Metadata.ConfigSource)
	assert.False(t, resp.Metadata.CacheHit)
}

func TestGetPrompt_MissingName(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "",
		Category: CategoryAgents,
		Provider: "default",
	})
	assert.ErrorContains(t, err, "name is required")
}

func TestGetPrompt_MissingCategory(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: "",
		Provider: "default",
	})
	assert.ErrorContains(t, err, "category is required")
}

func TestGetPrompt_InvalidCategory(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: PromptCategory("invalid"),
		Provider: "default",
	})
	assert.ErrorContains(t, err, "invalid category")
}

func TestGetPrompt_UnknownPromptName(t *testing.T) {
	loader := newTestLoader()
	_, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "nonexistent_prompt_xyz",
		Category: CategoryAgents,
		Provider: "default",
	})
	assert.Error(t, err)
}

// --- Provider normalization and fallback ---

func TestGetPrompt_EmptyProviderNormalizesToDefault(t *testing.T) {
	loader := newTestLoader()
	resp, err := loader.GetPrompt(context.Background(), PromptRequest{
		Name:     "k8s_lean",
		Category: CategoryAgents,
		Provider: "", // should normalize to "default"
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Content)
	assert.Equal(t, "default", resp.Metadata.Provider)
}

func TestGetPrompt_ProviderFallsBackToDefault(t *testing.T) {
	// No bedrock/openai/azure specific files exist yet — should fall back to default
	providers := []string{"bedrock", "openai", "azure", "anthropic", "googleai"}
	loader := newTestLoader()

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			resp, err := loader.GetPrompt(context.Background(), PromptRequest{
				Name:     "k8s_lean",
				Category: CategoryAgents,
				Provider: provider,
			})
			require.NoError(t, err, "provider %q should fall back to default", provider)
			assert.NotEmpty(t, resp.Content)

			// Content should match the default file
			defaultResp, _ := loader.GetPrompt(context.Background(), PromptRequest{
				Name:     "k8s_lean",
				Category: CategoryAgents,
				Provider: "default",
			})
			assert.Equal(t, defaultResp.Content, resp.Content,
				"provider %q should return same content as default", provider)
		})
	}
}

// --- Cache behaviour ---

func TestCache_HitOnSecondLoad(t *testing.T) {
	loader := newTestLoader()
	req := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Provider: "default"}

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
	req1 := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Provider: "default", AccountID: "acc-1"}
	req2 := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Provider: "default", AccountID: "acc-2"}

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
	req1 := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Provider: "default"}
	req2 := PromptRequest{Name: "k8s_native", Category: CategoryAgents, Provider: "default"}

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
	req := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Provider: "default"}

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
	req := PromptRequest{Name: "k8s_lean", Category: CategoryAgents, Provider: "default"}

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
		{"remediation_generate", CategoryTools},
		{"response_formatter", CategoryUtilities},
		{"time_handling_rules", CategoryFragments},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s", tt.category, tt.name), func(t *testing.T) {
			resp, err := loader.GetPrompt(context.Background(), PromptRequest{
				Name:     tt.name,
				Category: tt.category,
				Provider: "default",
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
	req := PromptRequest{Name: "test", Category: CategoryAgents, Provider: "default"}
	resp := &PromptResponse{Content: "hello", Metadata: PromptMetadata{Version: "v1"}}

	cache.Set(req, resp)
	cached, hit := cache.Get(req)
	assert.True(t, hit)
	assert.Equal(t, resp.Content, cached.Content)
}

func TestPromptCache_Miss(t *testing.T) {
	cache := NewPromptCache(1 * time.Hour)
	req := PromptRequest{Name: "test", Category: CategoryAgents, Provider: "default"}
	cached, hit := cache.Get(req)
	assert.False(t, hit)
	assert.Nil(t, cached)
}

func TestPromptCache_Size(t *testing.T) {
	cache := NewPromptCache(1 * time.Hour)
	assert.Equal(t, 0, cache.Size())
	for i := 0; i < 5; i++ {
		req := PromptRequest{Name: "test", Category: CategoryAgents, Provider: "default", AccountID: fmt.Sprintf("acc-%d", i)}
		cache.Set(req, &PromptResponse{Content: "x"})
	}
	assert.Equal(t, 5, cache.Size())
	cache.Clear()
	assert.Equal(t, 0, cache.Size())
}

func TestPromptCache_Expiration(t *testing.T) {
	cache := NewPromptCache(100 * time.Millisecond)
	req := PromptRequest{Name: "test", Category: CategoryAgents, Provider: "default"}
	cache.Set(req, &PromptResponse{Content: "x"})

	_, hit := cache.Get(req)
	assert.True(t, hit)

	time.Sleep(150 * time.Millisecond)
	_, hit = cache.Get(req)
	assert.False(t, hit, "entry should have expired")
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

func TestProcessIncludes_ProviderFallbackToDefault(t *testing.T) {
	testFS := fstest.MapFS{
		"default/v2/_persona/shared.txt": &fstest.MapFile{Data: []byte("default content")},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	// Request with provider "bedrock" — file only exists in default, should fall back
	result, err := loader.processIncludes("{{@include _persona/shared.txt}}", "bedrock", "v2", 0)
	require.NoError(t, err)
	assert.Equal(t, "default content", result)
}

func TestProcessIncludes_ProviderSpecificOverridesDefault(t *testing.T) {
	testFS := fstest.MapFS{
		"custom/v2/_persona/shared.txt":  &fstest.MapFile{Data: []byte("custom content")},
		"default/v2/_persona/shared.txt": &fstest.MapFile{Data: []byte("default content")},
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
			content, err := loader.loadPromptFile(p.name, CategoryUtilities, "default", "v2")
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
			req:     PromptRequest{Name: "test", Category: CategoryAgents, Provider: "default"},
			wantErr: "",
		},
		{
			name:    "missing name",
			req:     PromptRequest{Category: CategoryAgents, Provider: "default"},
			wantErr: "name is required",
		},
		{
			name:    "missing category",
			req:     PromptRequest{Name: "test", Provider: "default"},
			wantErr: "category is required",
		},
		{
			name:    "invalid category",
			req:     PromptRequest{Name: "test", Category: "bad", Provider: "default"},
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
	// Only a malformed googleai/v1 override exists — every other path in the chain misses.
	testFS := fstest.MapFS{
		"googleai/v1/agents/broken.yaml": &fstest.MapFile{
			Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: broken\n  bad: indentation\n"),
		},
	}
	loader := &PromptLoader{fs: testFS, cache: NewPromptCache(1 * time.Hour)}

	_, err := loader.loadPromptFile("broken", CategoryAgents, "googleai", "v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding prompt file",
		"the malformed override must be reported, not the later file-not-found misses")
	assert.NotContains(t, err.Error(), "file does not exist",
		"a generic miss must not mask the parse failure")
}
