package core

import (
	"encoding/json"
	"testing"

	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/require"
)

// Tools gained an object "target" property; plain-text queries that mention a
// "target" label or column must still reach the tool as its command.
func TestPlainTextInputIgnoresObjectTargetProperty(t *testing.T) {
	for _, tc := range []struct {
		tool  toolcore.NBTool
		input string
	}{
		{tools.PrometheusExecuteTool{}, `up{job="a",target="x"}`},
		{tools.EventsExecuteTool{}, `SELECT * FROM events WHERE target=1 LIMIT 5`},
		{tools.EventsExecuteTool{}, `<target>k8s-prod</target> SELECT 1`},
	} {
		var args map[string]any
		require.NoError(t, json.Unmarshal([]byte(normalizeToolInputForTool(tc.tool, tc.input)), &args))
		require.NotContains(t, args, "target", "%s: %q must not become a target argument", tc.tool.Name(), tc.input)
		require.Equal(t, tc.input, args["command"], "%s keeps the query as its command", tc.tool.Name())
	}
}
