package runtime

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
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

func TestToolsFingerprint(t *testing.T) {
	t.Run("empty_tools_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", toolsFingerprint(nil))
		assert.Equal(t, "", toolsFingerprint([]llms.Tool{}))
	})

	t.Run("nil_function_returns_empty", func(t *testing.T) {
		tools := []llms.Tool{
			{Type: "function", Function: nil},
		}
		assert.Equal(t, "", toolsFingerprint(tools))
	})

	t.Run("returns_8_hex_chars", func(t *testing.T) {
		tools := []llms.Tool{
			{
				Type: "function",
				Function: &llms.FunctionDefinition{
					Name:        "get_weather",
					Description: "Get the weather",
					Parameters:  map[string]any{"location": "string"},
				},
			},
		}
		fp := toolsFingerprint(tools)
		assert.Len(t, fp, 8)
		for _, c := range fp {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'), "expected hex char, got %c", c)
		}
	})

	t.Run("order_independent_same_tools", func(t *testing.T) {
		toolA := llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "toolA",
				Description: "Tool A",
			},
		}
		toolB := llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "toolB",
				Description: "Tool B",
			},
		}
		fp1 := toolsFingerprint([]llms.Tool{toolA, toolB})
		fp2 := toolsFingerprint([]llms.Tool{toolB, toolA})
		assert.Equal(t, fp1, fp2, "fingerprint must be independent of tool slice ordering")
	})

	t.Run("different_tools_different_fingerprint", func(t *testing.T) {
		toolA := llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name: "toolA",
			},
		}
		toolB := llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name: "toolB",
			},
		}
		assert.NotEqual(t, toolsFingerprint([]llms.Tool{toolA}), toolsFingerprint([]llms.Tool{toolB}))
	})

	t.Run("different_parameters_different_fingerprint", func(t *testing.T) {
		tool1 := llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:       "query",
				Parameters: map[string]any{"limit": 10},
			},
		}
		tool2 := llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:       "query",
				Parameters: map[string]any{"limit": 20},
			},
		}
		assert.NotEqual(t, toolsFingerprint([]llms.Tool{tool1}), toolsFingerprint([]llms.Tool{tool2}))
	})
}

func TestGenerateCacheKeyWithToolsFingerprint(t *testing.T) {
	t.Run("account_scope_appends_tools_fp_when_present", func(t *testing.T) {
		key := generateCacheKey(CacheScopeAccount, "acc1", "", "agent1", "gemini-2.5-flash", "creds123", "a1b2c3d4")
		assert.Equal(t, "account:acc1:agent1:gemini-2.5-flash:creds123:a1b2c3d4", key)
	})

	t.Run("account_scope_without_tools_fp_leaves_key_unchanged", func(t *testing.T) {
		keyWithoutVariadic := generateCacheKey(CacheScopeAccount, "acc1", "", "agent1", "gemini-2.5-flash", "creds123")
		assert.Equal(t, "account:acc1:agent1:gemini-2.5-flash:creds123", keyWithoutVariadic)

		keyWithEmptyFp := generateCacheKey(CacheScopeAccount, "acc1", "", "agent1", "gemini-2.5-flash", "creds123", "")
		assert.Equal(t, "account:acc1:agent1:gemini-2.5-flash:creds123", keyWithEmptyFp)
	})

	t.Run("other_scopes_ignore_tools_fp", func(t *testing.T) {
		globalKey := generateCacheKey(CacheScopeGlobal, "acc1", "", "agent1", "gemini-2.5-flash", "creds123", "a1b2c3d4")
		assert.Equal(t, "global:agent1:gemini-2.5-flash:creds123", globalKey)

		convKey := generateCacheKey(CacheScopeConversation, "acc1", "conv1", "agent1", "gemini-2.5-flash", "creds123", "a1b2c3d4")
		assert.Equal(t, "conv:acc1:conv1:agent1:gemini-2.5-flash:creds123", convKey)
	})
}
