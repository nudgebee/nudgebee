package core

import toolcore "nudgebee/llm/tools/core"

// Account scope of every tool registered in this package (see
// toolcore.AccountScope). A new tool must be declared here or the build's
// TestEveryToolDeclaresAnAccountScope fails.
func init() {
	toolcore.DeclareAccountScope(toolcore.AccountScopeIndependent,
		"clarification",
		"llm",
	)
	// memory_read / memory_write are Enterprise-only (ee/memory); OSS registers
	// neither tool, so declaring their scope here would fail
	// TestEveryToolDeclaresAnAccountScope.
}
