package memory

import (
	"nudgebee/llm/config"
	"strings"
)

// MigrationMode drives the Shadow → Dual → Cutover → Retired transition
// for retiring llm_conversation_memory in favour of typed stores.
type MigrationMode string

const (
	MigrationOff     MigrationMode = "off"     // Phase 1: legacy only
	MigrationShadow  MigrationMode = "shadow"  // legacy reads; both write
	MigrationDual    MigrationMode = "dual"    // new reads (fallback legacy); both write
	MigrationCutover MigrationMode = "cutover" // new reads + writes; legacy read-only
	MigrationRetired MigrationMode = "retired" // legacy table dropped
)

// CurrentMigrationMode returns the configured mode, defaulting to off.
func CurrentMigrationMode() MigrationMode {
	switch strings.ToLower(config.Config.MemoryMigrationMode) {
	case string(MigrationShadow):
		return MigrationShadow
	case string(MigrationDual):
		return MigrationDual
	case string(MigrationCutover):
		return MigrationCutover
	case string(MigrationRetired):
		return MigrationRetired
	default:
		return MigrationOff
	}
}

// ShouldDualWrite reports whether the legacy extractor should also project
// into typed stores (Shadow or Dual mode).
func ShouldDualWrite() bool {
	m := CurrentMigrationMode()
	return m == MigrationShadow || m == MigrationDual
}

// ShouldReadFromNew reports whether Compose should consult typed stores for
// Patterns / Decisions / Collective layers (Dual or Cutover mode).
func ShouldReadFromNew() bool {
	m := CurrentMigrationMode()
	return m == MigrationDual || m == MigrationCutover
}

// ShouldFallbackToLegacy reports whether a typed-store read miss should
// fall back to the legacy llm_conversation_memory table (Dual mode only).
func ShouldFallbackToLegacy() bool {
	return CurrentMigrationMode() == MigrationDual
}

// IsLegacyRetired reports whether legacy reads/writes are forbidden (Retired).
func IsLegacyRetired() bool {
	return CurrentMigrationMode() == MigrationRetired
}
