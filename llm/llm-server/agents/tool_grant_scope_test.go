package agents

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	toolcore "nudgebee/llm/tools/core"
)

// Tools in tool_modules.json registered only by the enterprise build (ee/), so
// absent from this package's registry.
var enterpriseOnlyGrantScopeTools = []string{"memory_read", "memory_write"}

// Every tool the chat can call must say what grant a person without a built-in
// role needs for it: an entry in tools/core/tool_modules.json, or its own
// GrantScope (agent wrappers, automation and client tools). A tool with neither
// is refused for such callers, so a new tool that forgets this is unusable for
// custom roles rather than open to them.
func TestEveryRegisteredToolHasAGrantScope(t *testing.T) {
	account := "00000000-0000-0000-0000-0000000000aa"
	listed := map[string]bool{}
	for _, name := range toolcore.GrantScopeNames() {
		listed[name] = true
	}

	var missing []string
	registered := map[string]bool{}
	for _, name := range toolcore.ListRegisteredSystemToolNames() {
		if strings.HasPrefix(name, "mock_") {
			continue // registered by other tests in this package
		}
		registered[strings.ToLower(name)] = true
		if listed[strings.ToLower(name)] {
			continue
		}
		tool, ok := toolcore.GetNBTool(account, name)
		if ok && tool != nil {
			if _, declares := tool.(toolcore.GrantScoped); declares {
				continue
			}
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "add these tools to tools/core/tool_modules.json (modules, integration_only or unscoped)")

	var stale []string
	for name := range listed {
		if !registered[name] && !slices.Contains(enterpriseOnlyGrantScopeTools, name) {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	assert.Empty(t, stale, "tool_modules.json lists tools that are no longer registered")
}
