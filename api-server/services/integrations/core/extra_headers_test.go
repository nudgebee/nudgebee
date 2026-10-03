package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExtraHeaders(t *testing.T) {
	t.Run("empty is no headers", func(t *testing.T) {
		headers, err := ParseExtraHeaders("")
		require.NoError(t, err)
		assert.Empty(t, headers)
	})

	t.Run("string values parse", func(t *testing.T) {
		headers, err := ParseExtraHeaders(`{"Authorization":"Bearer abc","X-Scope-OrgID":"team-a"}`)
		require.NoError(t, err)
		assert.Equal(t, "Bearer abc", headers["Authorization"])
		assert.Equal(t, "team-a", headers["X-Scope-OrgID"])
	})

	// A null value used to be accepted by the save-time check and rejected at request
	// time, so an integration could save as valid and then fail every query made
	// through it. Both paths now run this function.
	t.Run("rejects non-string values", func(t *testing.T) {
		for _, raw := range []string{`{"A":null}`, `{"X-Num":5}`, `{"A":true}`, `{"A":{"b":"c"}}`} {
			_, err := ParseExtraHeaders(raw)
			assert.Error(t, err, "input %s must be rejected", raw)
		}
	})

	t.Run("rejects non-objects", func(t *testing.T) {
		for _, raw := range []string{`[1,2]`, `"str"`, `not-json`, `5`} {
			_, err := ParseExtraHeaders(raw)
			assert.Error(t, err, "input %s must be rejected", raw)
		}
	})
}
