package security

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActionKey(t *testing.T) {
	assert.Equal(t, "recommendations:Execute", ActionKey("recommendations_update_dismissal", "recommendations:Execute"), "a key the caller sent wins")
	assert.Equal(t, "workflows:Read", ActionKey("executions_list", ""), "no key: the gateway's classification by name")
	assert.Equal(t, "", ActionKey("bees_install", ""), "not grantable: no key")
	assert.Equal(t, "", ActionKey("workflow_sync_templates", "workflows:Execute"), "not grantable: a sent key is ignored")
	assert.Equal(t, "llm:Execute", ActionKey("an_action_the_catalog_does_not_know", "llm:Execute"), "unknown action: the sent key")
	assert.Equal(t, "", ActionKey("no_such_action", ""))

	assert.Greater(t, len(actionKeys), 300)
	for name, key := range actionKeys {
		if key == "" {
			continue // not grantable
		}
		module, class, ok := strings.Cut(key, ":")
		assert.True(t, ok && module != "" && (class == "Read" || class == "Write" || class == "Execute"), "%s: %q", name, key)
	}
}
