package core

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// GrantScope is what a custom-role grant must cover for a tool to be used by a
// caller whose access to the account comes from grants (security.IsGrantCheckedRun):
// a person without a built-in role there, or a Bee run.
type GrantScope struct {
	// Modules all need the class of the call: Read to read, Execute to change.
	Modules []string
	// IntegrationOnly: authorized where the call resolves its integration
	// (security.CheckIntegrationUse, integrations.<type>), not at dispatch.
	IntegrationOnly bool
	// Unscoped tools read no account data (planning helpers, web search,
	// delegation to another agent whose own tool calls are checked).
	Unscoped bool
}

// GrantScoped is implemented by tools whose scope cannot be listed by name:
// agent wrappers, automations exposed as tools, client tools.
type GrantScoped interface {
	GrantScope() GrantScope
}

//go:embed tool_modules.json
var toolModulesJSON []byte

type toolModulesFile struct {
	Modules         map[string][]string `json:"modules"`
	IntegrationOnly []string            `json:"integration_only"`
	Unscoped        []string            `json:"unscoped"`
	BuiltinOnly     []string            `json:"builtin_only"`
}

var toolGrantScopes = mustLoadToolGrantScopes(toolModulesJSON)

func mustLoadToolGrantScopes(raw []byte) map[string]GrantScope {
	var f toolModulesFile
	if err := json.Unmarshal(raw, &f); err != nil {
		panic(fmt.Errorf("tool_modules.json: %w", err))
	}
	scopes := map[string]GrantScope{}
	add := func(name string, scope GrantScope) {
		name = strings.ToLower(name)
		if _, dup := scopes[name]; dup {
			panic(fmt.Errorf("tool_modules.json: %s is listed twice", name))
		}
		scopes[name] = scope
	}
	for name, modules := range f.Modules {
		add(name, GrantScope{Modules: modules})
	}
	for _, name := range f.IntegrationOnly {
		add(name, GrantScope{IntegrationOnly: true})
	}
	for _, name := range f.Unscoped {
		add(name, GrantScope{Unscoped: true})
	}
	for _, name := range f.BuiltinOnly {
		add(name, GrantScope{})
	}
	return scopes
}

// ToolGrantScope returns the grant scope of tool: its own declaration
// (GrantScoped), else the entry for its name in tool_modules.json. ok is false for
// a tool with neither — such a tool needs a built-in role.
func ToolGrantScope(tool NBTool) (scope GrantScope, ok bool) {
	if tool == nil {
		return GrantScope{}, false
	}
	if s, declares := tool.(GrantScoped); declares {
		return s.GrantScope(), true
	}
	scope, ok = toolGrantScopes[strings.ToLower(tool.Name())]
	return scope, ok
}

// GrantScopeNames lists every tool name tool_modules.json covers (for tests).
func GrantScopeNames() []string {
	names := make([]string, 0, len(toolGrantScopes))
	for name := range toolGrantScopes {
		names = append(names, name)
	}
	return names
}

// GrantScopeModules lists every module tool_modules.json names (for tests).
func GrantScopeModules() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range toolGrantScopes {
		for _, m := range s.Modules {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}
