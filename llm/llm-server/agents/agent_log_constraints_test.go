package agents

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEnforceCanonicalLogConstraints(t *testing.T) {
	for _, wrapper := range []string{"%s", "```json\n%s\n```", "```\n%s\n```"} {
		t.Run(wrapper, func(t *testing.T) {
			input := fmt.Sprintf(wrapper, `{"where":{"namespace":{"_eq":"nudgebee"}},"limit":5000,"range":"24h"}`)
			output := enforceCanonicalLogConstraints(input, "logs last 15m limit 25")
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(output), &got))
			require.Equal(t, float64(25), got["limit"])
			require.Equal(t, "15m", got["time_range"])
			require.NotContains(t, got, "range")
			require.Equal(t, map[string]any{"namespace": map[string]any{"_eq": "nudgebee"}}, got["where"])
		})
	}
	for _, input := range []string{"null", "```json\nnull\n```", "invalid JSON", ""} {
		t.Run("invalid/"+input, func(t *testing.T) {
			require.NotPanics(t, func() { require.Equal(t, input, enforceCanonicalLogConstraints(input, "last 15m limit 25")) })
		})
	}
	input := "```json\n{\"limit\":5000}\n```"
	require.Equal(t, input, enforceCanonicalLogConstraints(input, "find errors"))
}
