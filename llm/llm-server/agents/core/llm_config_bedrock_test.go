package core

import (
	"nudgebee/llm/config"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveLLMSecret_DBGlobal verifies that a DB-global value overrides
// an ENV-global value when the provider matches.
func TestResolveLLMSecret_DBGlobal(t *testing.T) {
	prevProvider := config.Config.LlmProvider
	prevKey := config.Config.LlmProviderAccessKey
	config.Config.LlmProvider = "bedrock"
	config.Config.LlmProviderAccessKey = "env-access-key"
	defer func() {
		config.Config.LlmProvider = prevProvider
		config.Config.LlmProviderAccessKey = prevKey
	}()

	res := &LLMConfigResolution{
		dbConfig: map[string]string{
			"llm_provider":            "bedrock",
			"llm_provider_access_key": "db-access-key",
		},
	}

	got := getLLMAccessKey("acct-1", "bedrock", "", false, res)
	assert.Equal(t, "db-access-key", got)
}

// TestResolveLLMSecret_ProviderMismatch ensures values are ignored when
// the stored provider differs from the requested provider.
func TestResolveLLMSecret_ProviderMismatch(t *testing.T) {
	prevProvider := config.Config.LlmProvider
	prevKey := config.Config.LlmProviderAccessKey
	config.Config.LlmProvider = "openai"
	config.Config.LlmProviderAccessKey = "env-access-key"
	defer func() {
		config.Config.LlmProvider = prevProvider
		config.Config.LlmProviderAccessKey = prevKey
	}()

	res := &LLMConfigResolution{
		dbConfig: map[string]string{
			"llm_provider":            "openai",
			"llm_provider_access_key": "db-openai-key",
		},
	}

	// Requesting bedrock — neither ENV-global nor DB-global should apply
	got := getLLMAccessKey("acct-1", "bedrock", "", false, res)
	assert.Equal(t, "", got)
}

// TestResolveLLMSecret_SecretKey verifies the secret-key helper wires through the
// same resolver with the correct config keys.
func TestResolveLLMSecret_SecretKey(t *testing.T) {
	res := &LLMConfigResolution{
		dbConfig: map[string]string{
			"llm_provider":            "bedrock",
			"llm_provider_secret_key": "db-secret",
		},
	}
	got := getLLMSecretKey("acct-1", "bedrock", "", false, res)
	assert.Equal(t, "db-secret", got)
}

// TestResolveLLMSecret_SessionTokenOptional confirms session token resolves
// independently and is empty when unset.
func TestResolveLLMSecret_SessionTokenOptional(t *testing.T) {
	res := &LLMConfigResolution{
		dbConfig: map[string]string{
			"llm_provider":            "bedrock",
			"llm_provider_access_key": "ak",
			"llm_provider_secret_key": "sk",
		},
	}
	got := getLLMSessionToken("acct-1", "bedrock", "", false, res)
	assert.Equal(t, "", got)
}

// TestResolveLLMSecret_AgentOverride verifies the DB agent-scoped key takes
// precedence over the DB global key.
func TestResolveLLMSecret_AgentOverride(t *testing.T) {
	res := &LLMConfigResolution{
		dbConfig: map[string]string{
			"llm_provider":                      "bedrock",
			"llm_provider_access_key":           "global-ak",
			"llm_provider_aws_debug":            "bedrock",
			"llm_provider_access_key_aws_debug": "agent-ak",
		},
	}
	got := getLLMAccessKey("acct-1", "bedrock", "aws_debug", true, res)
	assert.Equal(t, "agent-ak", got)
}
