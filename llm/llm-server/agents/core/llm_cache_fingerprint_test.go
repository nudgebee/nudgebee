package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCapabilityFingerprint(t *testing.T) {
	t.Run("empty_capabilities_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", capabilityFingerprint(nil))
		assert.Equal(t, "", capabilityFingerprint(map[string]any{}))
	})

	t.Run("no_allowed_tools_key_returns_empty", func(t *testing.T) {
		caps := map[string]any{"other_key": "value"}
		assert.Equal(t, "", capabilityFingerprint(caps))
	})

	t.Run("empty_allowed_tools_returns_empty", func(t *testing.T) {
		caps := map[string]any{"allowed_tools": []string{}}
		assert.Equal(t, "", capabilityFingerprint(caps))

		caps2 := map[string]any{"allowed_tools": []any{}}
		assert.Equal(t, "", capabilityFingerprint(caps2))
	})

	t.Run("returns_8_hex_chars", func(t *testing.T) {
		caps := map[string]any{"allowed_tools": []string{"kubectl", "logs"}}
		fp := capabilityFingerprint(caps)
		assert.Len(t, fp, 8)
		for _, c := range fp {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'), "expected hex char, got %c", c)
		}
	})

	t.Run("order_independent_same_tools", func(t *testing.T) {
		fp1 := capabilityFingerprint(map[string]any{"allowed_tools": []string{"kubectl", "logs", "metrics"}})
		fp2 := capabilityFingerprint(map[string]any{"allowed_tools": []string{"metrics", "kubectl", "logs"}})
		assert.Equal(t, fp1, fp2, "fingerprint must be order-independent")
	})

	t.Run("different_tools_different_fingerprint", func(t *testing.T) {
		fp1 := capabilityFingerprint(map[string]any{"allowed_tools": []string{"kubectl"}})
		fp2 := capabilityFingerprint(map[string]any{"allowed_tools": []string{"logs"}})
		assert.NotEqual(t, fp1, fp2)
	})

	t.Run("accepts_json_deserialized_any_slice", func(t *testing.T) {
		// JSON unmarshalling produces []any, not []string
		fp1 := capabilityFingerprint(map[string]any{"allowed_tools": []string{"kubectl", "logs"}})
		fp2 := capabilityFingerprint(map[string]any{"allowed_tools": []any{"kubectl", "logs"}})
		assert.Equal(t, fp1, fp2, "[]string and []any with same values must produce the same fingerprint")
	})
}

func TestGenerateCacheKeyWithCapabilityFingerprint(t *testing.T) {
	t.Run("no_capabilities_unchanged_key", func(t *testing.T) {
		key := generateCacheKey(CacheScopeAccount, "acc1", "", "k8s_debug", "gemini-2.5-flash")
		assert.Equal(t, "account:acc1:k8s_debug:gemini-2.5-flash", key)
	})

	t.Run("fingerprinted_agent_name_produces_distinct_key", func(t *testing.T) {
		fp := capabilityFingerprint(map[string]any{"allowed_tools": []string{"kubectl", "logs"}})
		agentNameFP := "k8s_debug:" + fp

		key := generateCacheKey(CacheScopeAccount, "acc1", "", agentNameFP, "gemini-2.5-flash")
		baseKey := generateCacheKey(CacheScopeAccount, "acc1", "", "k8s_debug", "gemini-2.5-flash")
		assert.NotEqual(t, key, baseKey)
	})
}
