package triage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestEnvCache_SharedByMultiplierAndCategory proves getEnvironmentMultiplier and
// getEnvironmentCategory read the SAME cached account_env entry: with a warm
// cache both resolve from it, so neither issues a query (db is nil — any DB
// access would panic). This is the whole point of routing both through
// getAccountEnv: one cached lookup per account, not one per scoring path.
func TestEnvCache_SharedByMultiplierAndCategory(t *testing.T) {
	const id = "acct-envcache-test"

	envCacheMu.Lock()
	envCache[id] = envCacheEntry{env: "prod", expiresAt: time.Now().Add(time.Minute)}
	envCacheMu.Unlock()
	t.Cleanup(func() {
		envCacheMu.Lock()
		delete(envCache, id)
		envCacheMu.Unlock()
	})

	// nil db: a cache miss would dereference it and panic, so passing these
	// asserts the value came from the shared cache.
	assert.Equal(t, EnvMultiplierProd, getEnvironmentMultiplier(context.Background(), nil, id))
	assert.Equal(t, "prod", getEnvironmentCategory(context.Background(), nil, id))
}

// TestEnvCache_EmptyAccountFallsBack checks the empty-account-id guard returns
// each path's default without touching the DB (nil db).
func TestEnvCache_EmptyAccountFallsBack(t *testing.T) {
	assert.Equal(t, EnvMultiplierDefault, getEnvironmentMultiplier(context.Background(), nil, ""))
	assert.Equal(t, "unknown", getEnvironmentCategory(context.Background(), nil, ""))
}

// TestEnvCache_CategoryMapping verifies the category mapping for each cached env
// value, including the non-prod aliases and the unknown fallback.
func TestEnvCache_CategoryMapping(t *testing.T) {
	cases := map[string]string{
		"prod":     "prod",
		"non_prod": "non_prod",
		"non-prod": "non_prod",
		"staging":  "unknown",
		"":         "unknown",
	}
	for env, want := range cases {
		const id = "acct-envcache-mapping"
		envCacheMu.Lock()
		envCache[id] = envCacheEntry{env: env, expiresAt: time.Now().Add(time.Minute)}
		envCacheMu.Unlock()

		got := getEnvironmentCategory(context.Background(), nil, id)
		assert.Equalf(t, want, got, "account_env %q", env)

		envCacheMu.Lock()
		delete(envCache, id)
		envCacheMu.Unlock()
	}
}
