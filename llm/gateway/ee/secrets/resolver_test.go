package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm-gateway/auth"
)

func TestBuildProviderKeys(t *testing.T) {
	byIntegration := map[string]map[string]string{
		"i1": {"llm_provider": "anthropic", "llm_provider_api_key": "sk-ant"},
		"i2": {"llm_provider": "openai", "llm_provider_api_key": "sk-oai"},
		"i3": {"llm_provider": "googleai"},                               // no api key → skipped
		"i4": {"llm_provider_api_key": "orphan"},                         // no provider → skipped
		"i5": {"llm_provider": "google", "llm_provider_api_key": "AIza"}, // google → Gemini
	}
	got := buildProviderKeys(byIntegration)

	assert.Equal(t, "sk-ant", got[schemas.Anthropic])
	assert.Equal(t, "sk-oai", got[schemas.OpenAI])
	assert.Equal(t, "AIza", got[schemas.Gemini], "google/googleai normalize to Gemini")
	assert.NotContains(t, got, schemas.ModelProvider(""), "no provider / no key rows are dropped")
	assert.Len(t, got, 3)
}

// seededResolver builds a resolver with a fresh cache entry, bypassing the DB.
func seededResolver(tenant string, providers map[schemas.ModelProvider]string) *Resolver {
	return &Resolver{cache: map[string]entry{
		tenant: {providers: providers, at: time.Now()},
	}}
}

func TestResolve_ReturnsTenantKeyForMatchingProvider(t *testing.T) {
	r := seededResolver("t1", map[schemas.ModelProvider]string{schemas.Anthropic: "sk-tenant"})

	key, ok := r.Resolve(context.Background(), schemas.Anthropic, auth.Identity{TenantID: "t1"})
	require.True(t, ok)
	assert.Equal(t, "sk-tenant", key.Value.Val)
	assert.Equal(t, schemas.WhiteList{"*"}, key.Models, "wildcard so core's pool doesn't deny by default")
}

func TestResolve_FallsBackWhenNoTenant(t *testing.T) {
	r := seededResolver("t1", map[schemas.ModelProvider]string{schemas.Anthropic: "sk-tenant"})

	_, ok := r.Resolve(context.Background(), schemas.Anthropic, auth.Identity{}) // no tenant id
	assert.False(t, ok, "no tenant identity → operator default")
}

func TestResolve_FallsBackWhenProviderNotConfigured(t *testing.T) {
	r := seededResolver("t1", map[schemas.ModelProvider]string{schemas.Anthropic: "sk-tenant"})

	_, ok := r.Resolve(context.Background(), schemas.Gemini, auth.Identity{TenantID: "t1"})
	assert.False(t, ok, "tenant has no Gemini key → operator default")
}
