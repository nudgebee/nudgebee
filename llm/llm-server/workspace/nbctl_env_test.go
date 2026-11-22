package workspace

import (
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

func TestAddNbctlEnvPointsAtTheProxy(t *testing.T) {
	prevURL := config.Config.LlmServerUrl
	t.Cleanup(func() { config.Config.LlmServerUrl = prevURL })
	config.Config.LlmServerUrl = "http://llm-server:8000/"

	env := map[string]string{}
	addNbctlEnv(env, "tok", "workspace-account", "target-account")
	assert.Equal(t, map[string]string{
		"NUDGEBEE_ENDPOINT":         "http://llm-server:8000/api/v1/workspace/nbctl",
		"NUDGEBEE_API_KEY":          "tok",
		"NUDGEBEE_ACCOUNT_ID":       "target-account",
		"NUDGEBEE_ENABLED_COMMANDS": "metrics,logs",
		"NUDGEBEE_HTTP_TIMEOUT":     "52s",
	}, env)
}
