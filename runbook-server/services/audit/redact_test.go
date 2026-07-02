package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSensitiveKey(t *testing.T) {
	for _, k := range []string{"access_secret", "AccessSecret", "api_key", "auth_token", "password", "private_key", "Authorization"} {
		assert.Truef(t, isSensitiveKey(k), "expected %q sensitive", k)
	}
	for _, k := range []string{"id", "name", "resource_key", "primary_key", "status", "", "verification_token_expiration", "token_count", "cookie_policy"} {
		assert.Falsef(t, isSensitiveKey(k), "expected %q benign", k)
	}
}

func TestRedactAudit(t *testing.T) {
	a := Audit{
		EventState:     map[string]any{"id": "1", "access_secret": "s", "data": map[string]any{"token": "t"}},
		EventPrevState: map[string]any{"api_key": "k"},
		EventAttr:      map[string]any{"table": "workflows", "password": "p"},
	}
	got := redactAudit(a)

	state := got.EventState.(map[string]any)
	assert.Equal(t, "1", state["id"])
	assert.Equal(t, redactedPlaceholder, state["access_secret"])
	assert.Equal(t, redactedPlaceholder, state["data"].(map[string]any)["token"])
	assert.Equal(t, redactedPlaceholder, got.EventPrevState.(map[string]any)["api_key"])
	assert.Equal(t, "workflows", got.EventAttr["table"])
	assert.Equal(t, redactedPlaceholder, got.EventAttr["password"])

	// input not mutated
	assert.Equal(t, "s", a.EventState.(map[string]any)["access_secret"])
}
