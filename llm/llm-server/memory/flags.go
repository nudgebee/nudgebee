package memory

import (
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"strings"
	"sync"
	"time"
)

// FeatureMemoryModule is the catalog code for per-tenant enrolment.
// Registered by migration V717; managed via existing public.feature_flag table.
const FeatureMemoryModule = "MEMORY_MODULE"

// envAllowlistCache caches the parsed env-var allowlist to avoid repeated
// string splits when the env override is in use.
var (
	envAllowlistMu  sync.RWMutex
	envAllowlistSet map[string]bool
	envAllowlistSrc string
	envAllowlistAt  time.Time
	envAllowlistTTL = 30 * time.Second
)

// EvaluateFlags returns which memory layers are active for a given tenant+user.
func EvaluateFlags(tenantID string) map[string]bool {
	flags := map[string]bool{
		"soul":        false,
		"preferences": false,
		"patterns":    false,
		"decisions":   false,
		"collective":  false,
	}

	if !config.Config.MemoryModuleEnabled {
		return flags
	}

	if !isTenantAllowed(tenantID) {
		return flags
	}

	flags["soul"] = config.Config.MemoryLayerSoulEnabled
	flags["preferences"] = config.Config.MemoryLayerPrefsEnabled
	flags["patterns"] = config.Config.MemoryLayerPatternsEnabled
	flags["decisions"] = config.Config.MemoryLayerDecisionsEnabled
	flags["collective"] = config.Config.MemoryLayerCollectiveEnabled

	return flags
}

// ComposeEnabledFor returns true if the memory compose path should be used for this tenant.
func ComposeEnabledFor(tenantID string) bool {
	if !config.Config.MemoryModuleEnabled || !config.Config.MemoryComposeEnabled {
		return false
	}
	return isTenantAllowed(tenantID)
}

// isTenantAllowed checks whether a tenant is allowed to use the memory module.
// Resolution order:
//
//  1. Env var memory_tenant_allowlist — when non-empty, it wins (breakglass,
//     dev override, incident-time pin). Comma-separated tenant IDs.
//  2. public.feature_flag via common.IsFeatureEnabled — primary path in
//     production. Row-level enrolment per tenant against the MEMORY_MODULE
//     feature code; managed via existing feature-flag admin tooling.
//
// On DB error we fail open so a brief blip doesn't black-hole agent traffic;
// MemoryModuleEnabled is still the hard gate above this check.
func isTenantAllowed(tenantID string) bool {
	if envSet := envAllowlist(); envSet != nil {
		_, ok := envSet[tenantID]
		return ok
	}
	enabled, err := common.IsFeatureEnabled(FeatureMemoryModule, tenantID)
	if err != nil {
		slog.Warn("memory: feature flag lookup failed, failing open",
			"error", err, "feature", FeatureMemoryModule, "tenant", tenantID)
		return true
	}
	return enabled
}

// envAllowlist returns the parsed env-var allowlist, or nil when the env var
// is empty (signalling "fall through to feature_flag").
func envAllowlist() map[string]bool {
	raw := config.Config.MemoryTenantAllowlist
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	envAllowlistMu.RLock()
	if envAllowlistSet != nil && envAllowlistSrc == raw && time.Since(envAllowlistAt) < envAllowlistTTL {
		set := envAllowlistSet
		envAllowlistMu.RUnlock()
		return set
	}
	envAllowlistMu.RUnlock()

	envAllowlistMu.Lock()
	defer envAllowlistMu.Unlock()
	if envAllowlistSet != nil && envAllowlistSrc == raw && time.Since(envAllowlistAt) < envAllowlistTTL {
		return envAllowlistSet
	}

	parts := strings.Split(raw, ",")
	set := make(map[string]bool, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			set[t] = true
		}
	}
	envAllowlistSet = set
	envAllowlistSrc = raw
	envAllowlistAt = time.Now()
	return set
}
