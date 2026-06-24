package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestCapabilityFingerprint(t *testing.T) {
	t.Run("empty_capabilities_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", capabilityFingerprint(toolcore.AgentCapabilities{}))
	})

	t.Run("empty_allowed_tools_returns_empty", func(t *testing.T) {
		caps := toolcore.AgentCapabilities{AllowedTools: []string{}}
		assert.Equal(t, "", capabilityFingerprint(caps))
	})

	t.Run("returns_8_hex_chars", func(t *testing.T) {
		caps := toolcore.AgentCapabilities{AllowedTools: []string{"kubectl", "logs"}}
		fp := capabilityFingerprint(caps)
		assert.Len(t, fp, 8)
		for _, c := range fp {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'), "expected hex char, got %c", c)
		}
	})

	t.Run("order_independent_same_tools", func(t *testing.T) {
		fp1 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"kubectl", "logs", "metrics"}})
		fp2 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"metrics", "kubectl", "logs"}})
		assert.Equal(t, fp1, fp2, "fingerprint must be order-independent")
	})

	t.Run("different_tools_different_fingerprint", func(t *testing.T) {
		fp1 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"kubectl"}})
		fp2 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"logs"}})
		assert.NotEqual(t, fp1, fp2)
	})

	t.Run("case_insensitive_same_fingerprint", func(t *testing.T) {
		fp1 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"Kubectl", "LOGS"}})
		fp2 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"kubectl", "logs"}})
		assert.Equal(t, fp1, fp2, "tool names differing only in case must produce the same fingerprint")
	})

	t.Run("whitespace_trimmed_same_fingerprint", func(t *testing.T) {
		fp1 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{" kubectl ", "logs"}})
		fp2 := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"kubectl", "logs"}})
		assert.Equal(t, fp1, fp2, "leading/trailing whitespace must not affect the fingerprint")
	})
}

func TestGenerateCacheKeyWithCapabilityFingerprint(t *testing.T) {
	t.Run("no_capabilities_unchanged_key", func(t *testing.T) {
		key := generateCacheKey(CacheScopeAccount, "acc1", "", "k8s_debug", "gemini-2.5-flash", "")
		assert.Equal(t, "account:acc1:k8s_debug:gemini-2.5-flash:", key)
	})

	t.Run("fingerprinted_agent_name_produces_distinct_key", func(t *testing.T) {
		fp := capabilityFingerprint(toolcore.AgentCapabilities{AllowedTools: []string{"kubectl", "logs"}})
		agentNameFP := "k8s_debug:" + fp

		key := generateCacheKey(CacheScopeAccount, "acc1", "", agentNameFP, "gemini-2.5-flash", "")
		baseKey := generateCacheKey(CacheScopeAccount, "acc1", "", "k8s_debug", "gemini-2.5-flash", "")
		assert.NotEqual(t, key, baseKey)
	})
}
