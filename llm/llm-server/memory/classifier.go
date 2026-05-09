package memory

// TargetStore names the typed store a legacy memory row (or a freshly
// extracted fact) should land in.
type TargetStore string

const (
	TargetPreferences TargetStore = "preferences"
	TargetPatterns    TargetStore = "patterns"
	TargetDecisions   TargetStore = "decisions"
	TargetCollective  TargetStore = "collective"
	TargetQuarantine  TargetStore = "quarantine" // unclassifiable — human review required
)

// Legacy memory_type string constants. Kept as package-local strings (not
// imported from agents/core) because agents/core also imports this package
// via memory_v2_bridge, and Go forbids import cycles. The authoritative
// constants live in agents/core/interface.go; this list mirrors them.
const (
	legacyTypeUserPreference      = "user_preference"
	legacyTypePattern             = "pattern"
	legacyTypeWorkflow            = "workflow"
	legacyTypeInvestigationResult = "investigation_result"
	legacyTypeArchitecturalFact   = "architectural_fact"
	legacyTypeConfigInsight       = "configuration_insight"
	legacyTypeDependencyMapping   = "dependency_mapping"
	legacyTypeTroubleshooting     = "troubleshooting_guide"
	// Also accept the bare form used in newer code paths.
	legacyTypeTroubleshootingAlt = "troubleshooting"
)

// ClassifyLegacyType maps a legacy llm_conversation_memory.memory_type string
// onto a typed store. This is the contract Phase 2 depends on; any
// reclassification of what goes where is a schema-level decision and must be
// made here.
//
// Summary:
//   - user_preference      → Preferences (user-scoped, explicit vocabulary)
//   - pattern / workflow   → Patterns (user-scoped, decayed, inferred)
//   - investigation_result → Decisions (per-conversation episodic, immutable)
//   - architectural_fact   \
//   - configuration_insight \
//   - dependency_mapping    } → Collective (tenant-scoped, cross-user)
//   - troubleshooting_guide /
func ClassifyLegacyType(memoryType string) TargetStore {
	switch memoryType {
	case legacyTypeUserPreference:
		return TargetPreferences
	case legacyTypePattern, legacyTypeWorkflow:
		return TargetPatterns
	case legacyTypeInvestigationResult:
		return TargetDecisions
	case legacyTypeArchitecturalFact,
		legacyTypeConfigInsight,
		legacyTypeDependencyMapping,
		legacyTypeTroubleshooting,
		legacyTypeTroubleshootingAlt:
		return TargetCollective
	default:
		return TargetQuarantine
	}
}

// CollectiveKindFromLegacyType maps legacy memory_type values onto the
// vocabulary used by the Collective store. Returns empty string for
// non-collective types.
func CollectiveKindFromLegacyType(memoryType string) string {
	switch memoryType {
	case legacyTypeArchitecturalFact:
		return "architectural_fact"
	case legacyTypeConfigInsight:
		return "configuration_insight"
	case legacyTypeDependencyMapping:
		return "dependency_mapping"
	case legacyTypeTroubleshooting, legacyTypeTroubleshootingAlt:
		return "troubleshooting"
	default:
		return ""
	}
}
