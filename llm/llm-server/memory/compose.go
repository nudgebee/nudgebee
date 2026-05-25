package memory

import (
	"context"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	memcollective "nudgebee/llm/memory/stores/collective"
	memdecisions "nudgebee/llm/memory/stores/decisions"
	mempatterns "nudgebee/llm/memory/stores/patterns"
	memprefs "nudgebee/llm/memory/stores/preferences"
	memsoul "nudgebee/llm/memory/stores/soul"
	"sync"
	"time"
)

// Compose builds the memory slab for a prompt. Each enabled layer is fetched
// in parallel; each layer fetch is independent and its failure only empties
// that layer's block — never propagates as a Compose error.
func (m *impl) Compose(ctx context.Context, req ComposeRequest) (MemorySlab, error) {
	flags := EvaluateFlags(req.TenantID)
	trace := ComposeTrace{
		FlagsApplied: flags,
		LayerLatency: map[string]time.Duration{},
		TokenUsage:   map[string]int{},
	}

	if !config.Config.MemoryModuleEnabled {
		return MemorySlab{Trace: trace}, nil
	}

	var slab MemorySlab
	var wg sync.WaitGroup
	var traceMu sync.Mutex
	recordLatency := func(layer string, d time.Duration) {
		traceMu.Lock()
		trace.LayerLatency[layer] = d
		traceMu.Unlock()
	}

	if flags["soul"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			slab.Soul = composeSoulLayer(req.TenantID, req.UserID)
			recordLatency("soul", time.Since(start))
		}()
	}

	if flags["preferences"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			slab.Preferences = composePreferencesLayer(req.TenantID, req.UserID, req.AgentModule)
			recordLatency("preferences", time.Since(start))
		}()
	}

	if flags["patterns"] && ShouldReadFromNew() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			slab.Patterns = composePatternsLayer(req.TenantID, req.UserID, req.AgentModule)
			recordLatency("patterns", time.Since(start))
		}()
	}

	if flags["decisions"] && ShouldReadFromNew() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			slab.Decisions = composeDecisionsLayer(req.TenantID, req.UserID, req.AgentModule, req.Query)
			recordLatency("decisions", time.Since(start))
		}()
	}

	if flags["collective"] && ShouldReadFromNew() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			slab.Collective = composeCollectiveLayer(req.TenantID, req.AgentModule, req.Query)
			recordLatency("collective", time.Since(start))
		}()
	}

	wg.Wait()

	// Per-layer token caps.
	slab.Soul = trimToTokenBudget(slab.Soul, config.Config.MemorySoulMaxTokens)
	slab.Preferences = trimToTokenBudget(slab.Preferences, config.Config.MemoryPrefsMaxTokens)
	slab.Patterns = trimToTokenBudget(slab.Patterns, config.Config.MemoryPatternsMaxTokens)
	slab.Decisions = trimToTokenBudget(slab.Decisions, config.Config.MemoryDecisionsMaxTokens)
	slab.Collective = trimToTokenBudget(slab.Collective, config.Config.MemoryCollectiveMaxTokens)

	trace.TokenUsage["soul"] = estimateTokens(slab.Soul)
	trace.TokenUsage["preferences"] = estimateTokens(slab.Preferences)
	trace.TokenUsage["patterns"] = estimateTokens(slab.Patterns)
	trace.TokenUsage["decisions"] = estimateTokens(slab.Decisions)
	trace.TokenUsage["collective"] = estimateTokens(slab.Collective)

	slab.Trace = trace
	return slab, nil
}

// composeSoulLayer fetches and renders the soul block for a user, with caching.
// Returns "" on cold-start, DB error, or disabled layer.
func composeSoulLayer(tenantID, userID string) string {
	key := soulCacheKey(tenantID, userID)
	if cached, ok := common.CacheGet(cacheNamespaceSoul, key); ok {
		return string(cached)
	}

	s, err := memsoul.Get(tenantID, userID)
	if err != nil {
		slog.Warn("memory.compose: soul fetch failed", "error", err, "tenant", tenantID, "user", userID)
		return ""
	}
	block := memsoul.Render(s)

	// Cache even empty blocks to avoid repeat DB hits during cold-start.
	if err := common.CacheSet(cacheNamespaceSoul, key, []byte(block)); err != nil {
		slog.Debug("memory.compose: soul cache set failed", "error", err)
	}
	return block
}

// composePreferencesLayer fetches and renders the prefs block filtered by module.
func composePreferencesLayer(tenantID, userID, agentModule string) string {
	key := prefsCacheKey(tenantID, userID, agentModule)
	if cached, ok := common.CacheGet(cacheNamespacePrefs, key); ok {
		return string(cached)
	}

	prefs, err := memprefs.ListForUser(tenantID, userID, agentModule)
	if err != nil {
		slog.Warn("memory.compose: prefs fetch failed", "error", err, "tenant", tenantID, "user", userID)
		return ""
	}
	block := memprefs.Render(prefs)

	if err := common.CacheSet(cacheNamespacePrefs, key, []byte(block)); err != nil {
		slog.Debug("memory.compose: prefs cache set failed", "error", err)
	}
	return block
}

// composePatternsLayer fetches top-N patterns ordered by decayed score.
// Not cached: pattern results shift per query context.
func composePatternsLayer(tenantID, userID, agentModule string) string {
	pats, err := mempatterns.TopForUser(tenantID, userID, agentModule, 10)
	if err != nil {
		slog.Warn("memory.compose: patterns fetch failed", "error", err)
		return ""
	}
	return mempatterns.Render(pats)
}

// composeDecisionsLayer fetches recent decisions, optionally keyword-filtered
// against the current query. Window: 30 days.
func composeDecisionsLayer(tenantID, userID, agentModule, query string) string {
	since := time.Now().AddDate(0, 0, -30)
	decs, err := memdecisions.RecentForUser(tenantID, userID, agentModule, query, since, 10)
	if err != nil {
		slog.Warn("memory.compose: decisions fetch failed", "error", err)
		return ""
	}
	return memdecisions.Render(decs)
}

// composeCollectiveLayer fetches top-N tenant-scoped collective entries.
// Tenant + module filter; keyword fallback against the query.
func composeCollectiveLayer(tenantID, agentModule, query string) string {
	entries, err := memcollective.TopForTenant(tenantID, agentModule, query, 8)
	if err != nil {
		slog.Warn("memory.compose: collective fetch failed", "error", err)
		return ""
	}
	return memcollective.Render(entries)
}
