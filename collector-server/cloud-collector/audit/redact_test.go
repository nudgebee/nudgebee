package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSensitiveKey(t *testing.T) {
	for _, k := range []string{"access_secret", "api_key", "auth_token", "password", "private_key", "Authorization"} {
		assert.Truef(t, isSensitiveKey(k), "expected %q sensitive", k)
	}
	for _, k := range []string{"resource_id", "command", "region", "resource_key", "status", "", "verification_token_expiration", "token_count", "cookie_policy"} {
		assert.Falsef(t, isSensitiveKey(k), "expected %q benign", k)
	}
}

func TestRedactEventAttr(t *testing.T) {
	attr := map[string]any{
		"command":      "run_command",
		"resource_id":  "i-123",
		"command_args": map[string]any{"script": "echo hi", "api_token": "tok"},
		"commands": []any{
			map[string]any{"command": "aws configure", "secret_key": "sk", "output": "ok"},
		},
	}
	got := redactEventAttr(attr)

	assert.Equal(t, "run_command", got["command"])
	assert.Equal(t, "i-123", got["resource_id"])
	args := got["command_args"].(map[string]any)
	assert.Equal(t, "echo hi", args["script"])
	assert.Equal(t, redactedPlaceholder, args["api_token"])
	cmd0 := got["commands"].([]any)[0].(map[string]any)
	assert.Equal(t, "aws configure", cmd0["command"])
	assert.Equal(t, redactedPlaceholder, cmd0["secret_key"])
	assert.Equal(t, "ok", cmd0["output"])

	// input not mutated
	assert.Equal(t, "tok", attr["command_args"].(map[string]any)["api_token"])
}

func TestRedactEventAttr_Nil(t *testing.T) {
	assert.Nil(t, redactEventAttr(nil))
}
