package api

import (
	"testing"

	"nudgebee/llm/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The handler distinguishes "disable this KB" from "the caller forgot to send
// enabled" purely by Enabled being a *bool, so the decoder has to preserve that
// distinction across every shape the gateway can hand it.
func TestKbUpdateEnabledRequestDecode(t *testing.T) {
	t.Run("explicit false decodes to a non-nil false", func(t *testing.T) {
		var req kbUpdateEnabledRequest
		require.NoError(t, common.DecodeMapToStruct(map[string]any{
			"account_id": "acct", "kb_id": "kb", "enabled": false,
		}, &req))
		require.NotNil(t, req.Enabled)
		assert.False(t, *req.Enabled)
	})

	t.Run("explicit true decodes to a non-nil true", func(t *testing.T) {
		var req kbUpdateEnabledRequest
		require.NoError(t, common.DecodeMapToStruct(map[string]any{
			"account_id": "acct", "kb_id": "kb", "enabled": true,
		}, &req))
		require.NotNil(t, req.Enabled)
		assert.True(t, *req.Enabled)
	})

	t.Run("omitted enabled stays nil so the handler can 400", func(t *testing.T) {
		var req kbUpdateEnabledRequest
		require.NoError(t, common.DecodeMapToStruct(map[string]any{
			"account_id": "acct", "kb_id": "kb",
		}, &req))
		assert.Nil(t, req.Enabled, "an omitted flag must not read as 'disable'")
	})

	t.Run("stringly-typed booleans survive WeaklyTypedInput", func(t *testing.T) {
		// The decoder runs with WeaklyTypedInput, and a GraphQL variable that
		// arrives as a JSON string must not silently land on the wrong value.
		for raw, want := range map[string]bool{"true": true, "false": false} {
			var req kbUpdateEnabledRequest
			require.NoError(t, common.DecodeMapToStruct(map[string]any{
				"account_id": "acct", "kb_id": "kb", "enabled": raw,
			}, &req))
			require.NotNil(t, req.Enabled, "raw=%s", raw)
			assert.Equal(t, want, *req.Enabled, "raw=%s", raw)
		}
	})
}
