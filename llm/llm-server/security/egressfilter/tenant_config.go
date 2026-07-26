package egressfilter

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TenantConfig is the per-tenant override row. Absence of a row (Resolve
// returns nil) means env-level defaults apply across the board.
//
// Composition rule (enforced here, not the DB): tenant config is ADDITIVE
// on top of env baselines. Allowlist extends, DisabledRules removes
// individual env-loaded rules for this tenant only, CustomRules adds. A
// tenant cannot weaken the env baseline by removing entries.
type TenantConfig struct {
	TenantID      uuid.UUID `db:"tenant_id"     json:"tenant_id"`
	Mode          Mode      `db:"mode"          json:"mode"`
	Enabled       bool      `db:"enabled"       json:"enabled"`
	Allowlist     []string  `db:"allowlist"     json:"allowlist"`       // additive on env allowlist
	CustomRules   []byte    `db:"custom_rules"  json:"custom_rules"`    // raw JSONB; no admin-API surface yet
	DisabledRules []string  `db:"disabled_rules" json:"disabled_rules"` // additive; disables env-loaded rules per tenant
	UpdatedAt     time.Time `db:"updated_at"    json:"updated_at"`

	// compiledCustomRules holds the tenant's enabled custom patterns compiled
	// for the scan path. Populated by Resolve after a load (not by the DAO, not
	// serialized) so the compile cost is paid once per cache entry, not per LLM
	// call. Unexported: the scan path reads it via scanCustomRules.
	compiledCustomRules []compiledCustomRule
}

// tenantIDContextKey is the context key under which the agent layer
// attaches the current tenant ID before invoking GenerateContent. The
// wrapper extracts it inside scanAndDecide; absent key → nil tenant
// config → env defaults (fail-open).
type tenantIDContextKey struct{}

// WithTenantID attaches a tenant ID to ctx so the egressfilter wrapper
// can resolve a per-tenant TenantConfig on the LLM call path. Callers in
// the agent layer should set this once at the top of any request that
// can produce LLM calls — typically as the first ctx wrap after the
// security context is established.
func WithTenantID(ctx context.Context, tenantID uuid.UUID) context.Context {
	if tenantID == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, tenantIDContextKey{}, tenantID)
}

// TenantIDFromContext is the public accessor for the tenant id attached via
// WithTenantID. Returns uuid.Nil + false when no tenant has been attached.
// Useful for downstream code that wants to read tenant scoping without
// re-extracting from a SecurityContext, and for tests verifying the
// wire-up at the agent layer.
func TenantIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	return tenantIDFromContext(ctx)
}

// tenantIDFromContext is the wrapper-side accessor. Returns uuid.Nil + false
// when no tenant has been attached (background jobs, system calls, or any
// path that hasn't yet been instrumented to call WithTenantID).
func tenantIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	v := ctx.Value(tenantIDContextKey{})
	if v == nil {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// TenantConfigLoader is the per-tenant lookup function the cache delegates
// to on miss. Wiring is via SetTenantConfigLoader; left nil by default
// (resolver returns nil immediately → env defaults). Keeping this as a
// hook avoids a hard dependency from the egressfilter package on the
// DAO / sqlx — same pattern as ActionGate.
type TenantConfigLoader func(ctx context.Context, tenantID uuid.UUID) (*TenantConfig, error)

var tenantConfigLoader TenantConfigLoader

// SetTenantConfigLoader registers the DAO-backed loader. Wired once at
// process startup from a sibling package (typically the admin-API
// registration site or an init() in a dedicated wiring file).
func SetTenantConfigLoader(loader TenantConfigLoader) {
	tenantConfigLoader = loader
}

// tenantConfigTTL is how long a single tenant's config stays cached before
// the next call to Resolve re-fetches from the loader. Set high — config
// changes propagate via API-driven cache invalidation, not TTL expiry.
var tenantConfigTTL = 5 * time.Minute

// tenantConfigErrorTTL is how long we suppress retries after the loader
// returns an error. Without this, a DB outage causes every LLM call for
// every tenant to block on the same upstream failure, exhausting the
// connection pool and amplifying the outage. 10s is short enough to
// recover quickly, long enough to dampen retry storms.
var tenantConfigErrorTTL = 10 * time.Second

// SetTenantConfigTTL overrides the default cache TTL. Test-only convenience
// for exercising expiry paths without sleeping for minutes.
func SetTenantConfigTTL(d time.Duration) {
	tenantConfigTTL = d
}

// SetTenantConfigErrorTTL overrides the negative-cache-on-error TTL.
// Test-only.
func SetTenantConfigErrorTTL(d time.Duration) {
	tenantConfigErrorTTL = d
}

type tenantCacheEntry struct {
	cfg       *TenantConfig // nil = "no override row exists" (negative cache)
	expiresAt time.Time
}

var (
	tenantCacheMu      sync.RWMutex
	tenantCacheEntries = map[uuid.UUID]*tenantCacheEntry{}
)

// Resolve returns the TenantConfig for the tenant in ctx, or nil when no
// tenant is attached / no override row exists / loader is not registered.
// Callers receiving nil should fall back to env-level defaults.
//
// On loader error (DB unreachable) we log + return nil (fail-open). The
// security baseline keeps working at env defaults; tenant overrides are
// temporarily ignored.
func Resolve(ctx context.Context) *TenantConfig {
	tenantID, ok := tenantIDFromContext(ctx)
	if !ok {
		return nil
	}
	if tenantConfigLoader == nil {
		return nil
	}

	// Fast path: cached entry, not expired.
	tenantCacheMu.RLock()
	if e, hit := tenantCacheEntries[tenantID]; hit && time.Now().Before(e.expiresAt) {
		cfg := e.cfg
		tenantCacheMu.RUnlock()
		return cfg
	}
	tenantCacheMu.RUnlock()

	// Slow path: load. Could stampede under concurrent miss for the same
	// tenant; acceptable in Stage 2a (LLM call latency dominates the
	// duplicate read). Singleflight is a future optimisation.
	cfg, err := tenantConfigLoader(ctx, tenantID)
	if err != nil {
		slog.Warn("egressfilter: tenant config loader error; falling through to env defaults",
			"tenant_id", tenantID.String(), "error", err)
		// Negative-cache the error for a short TTL so a DB outage doesn't
		// cause every LLM call for every tenant to block on the same
		// upstream failure. BUT only when the error is upstream-shaped
		// (DB unavailable, etc.) — if the cause was this specific request's
		// ctx cancellation or timeout, that's transient and request-local;
		// caching it would temporarily disable this tenant's config for
		// every other healthy concurrent request. ctx.Err() == nil tells us
		// the error came from the loader, not our deadline.
		if ctx.Err() == nil {
			tenantCacheMu.Lock()
			tenantCacheEntries[tenantID] = &tenantCacheEntry{
				cfg:       nil,
				expiresAt: time.Now().Add(tenantConfigErrorTTL),
			}
			tenantCacheMu.Unlock()
		}
		return nil
	}

	// Compile the tenant's enabled custom patterns once, here, so the LLM
	// call path (scanCustomRules) never pays the compile cost. A corrupted
	// custom_rules blob is logged and treated as "no custom rules" — it must
	// not break the baseline scan for this tenant (fail-open on custom rules
	// only; the built-in corpus still runs).
	if cfg != nil {
		if parsed, perr := ParseCustomRules(cfg.CustomRules); perr != nil {
			slog.Warn("egressfilter: tenant custom_rules parse failed; ignoring custom rules",
				"tenant_id", tenantID.String(), "error", perr)
		} else {
			cfg.compiledCustomRules = compileCustomRules(parsed)
		}
	}

	tenantCacheMu.Lock()
	tenantCacheEntries[tenantID] = &tenantCacheEntry{
		cfg:       cfg,
		expiresAt: time.Now().Add(tenantConfigTTL),
	}
	tenantCacheMu.Unlock()

	return cfg
}

// InvalidateTenantConfig drops the cached entry for one tenant so the
// next Resolve performs a fresh load. Called by the admin API after a
// successful write. Process-local only in Stage 2a; cross-pod broadcast
// is queued for Stage 2c.
func InvalidateTenantConfig(tenantID uuid.UUID) {
	tenantCacheMu.Lock()
	delete(tenantCacheEntries, tenantID)
	tenantCacheMu.Unlock()
}

// invalidateAllTenantConfigsForTest exists for test setup/teardown.
// Production code has no reason to clear the entire cache.
func invalidateAllTenantConfigsForTest() {
	tenantCacheMu.Lock()
	tenantCacheEntries = map[uuid.UUID]*tenantCacheEntry{}
	tenantCacheMu.Unlock()
}

// StartTenantConfigCacheCleanup runs a periodic cleanup goroutine that
// removes expired entries from the tenant-config cache. Without this, the
// map grows unbounded with one entry per tenant ever resolved — small but
// not zero, and a multi-tenant deployment with transient tenants would
// leak memory over weeks. The cleanup is bounded by tenant count (cheap
// even at thousands of entries).
//
// Cancelled by ctx; safe to call once at process startup. Cleanup
// interval is fixed at 1 minute — frequent enough to keep the map small,
// rare enough that the lock contention is negligible.
func StartTenantConfigCacheCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tenantConfigCacheReapExpired()
			}
		}
	}()
}

// tenantConfigCacheReapExpired walks the cache and deletes entries whose
// expiresAt is in the past. Acquires the write lock once for the full
// scan, which is fine since the cache is bounded by tenant count — at
// 10k tenants the walk takes ~1ms with the lock held.
func tenantConfigCacheReapExpired() {
	now := time.Now()
	tenantCacheMu.Lock()
	defer tenantCacheMu.Unlock()
	for id, e := range tenantCacheEntries {
		if now.After(e.expiresAt) {
			delete(tenantCacheEntries, id)
		}
	}
}

// applyTenantOverrides filters Scan hits using a per-tenant TenantConfig:
// - drops hits whose RuleID is in tcfg.DisabledRules (tenant disabled the rule)
// - drops hits whose matched substring is in tcfg.Allowlist (tenant docs-sample)
//
// Returns the result unchanged when tcfg is nil or both lists are empty
// (zero allocation on the hot path when nothing applies).
//
// CustomRules are not applied here — they need a fresh regex pass against
// the payload and require admin-API surface that does not ship in 2a.
func applyTenantOverrides(r Result, payload string, tcfg *TenantConfig) Result {
	if tcfg == nil || (len(tcfg.DisabledRules) == 0 && len(tcfg.Allowlist) == 0) {
		return r
	}
	if len(r.Hits) == 0 {
		return r
	}

	var disabledSet map[string]struct{}
	if len(tcfg.DisabledRules) > 0 {
		disabledSet = make(map[string]struct{}, len(tcfg.DisabledRules))
		for _, id := range tcfg.DisabledRules {
			disabledSet[id] = struct{}{}
		}
	}
	var allowSet map[string]struct{}
	if len(tcfg.Allowlist) > 0 {
		allowSet = make(map[string]struct{}, len(tcfg.Allowlist))
		for _, v := range tcfg.Allowlist {
			allowSet[v] = struct{}{}
		}
	}

	kept := r.Hits[:0]
	for _, h := range r.Hits {
		if disabledSet != nil {
			if _, ok := disabledSet[h.RuleID]; ok {
				continue
			}
		}
		if allowSet != nil {
			// Defensive bounds check — custom Filter impls can produce
			// out-of-range offsets. Fail-closed by keeping the hit if the
			// offsets are invalid.
			if h.Start < 0 || h.End < h.Start || h.End > len(payload) {
				kept = append(kept, h)
				continue
			}
			if _, ok := allowSet[payload[h.Start:h.End]]; ok {
				continue
			}
		}
		kept = append(kept, h)
	}
	return Result{Hits: kept}
}
