package tools

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
)

// A change the AI makes through an api-server action is checked twice: at
// dispatch against the tool's grant scope, and by api-server against the key
// sent with the call. The key must be Execute on a module the tool already
// needs, or the two services disagree on which grant allows the change.
func TestApiServerGrantKeysMatchTheToolsGrantScope(t *testing.T) {
	for _, c := range []struct {
		tool core.NBTool
		key  string
	}{
		{RecommendationApplyTool{}, recommendationsExecuteKey},
		{RecommendationDismissalTool{}, recommendationsExecuteKey},
		{RecommendationTicketResolutionTool{}, recommendationsExecuteKey},
		{RecommendationCliTool{}, cloudExecuteKey},
		{CisScanTool{}, services_server.CisScanActionPermission},
		{ImageScanTool{}, services_server.ScanImageActionPermission},
	} {
		scope, ok := core.ToolGrantScope(c.tool)
		require.True(t, ok, c.tool.Name())
		module, class, _ := strings.Cut(c.key, ":")
		assert.Equal(t, "Execute", class, c.tool.Name())
		assert.True(t, slices.Contains(scope.Modules, module), "%s: key %s is not in its grant scope %v", c.tool.Name(), c.key, scope.Modules)
	}
}
