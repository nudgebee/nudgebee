package core

import (
	"log/slog"
	"sync"
	"time"
)

// imageSupportCacheTTL bounds how stale the in-memory vision-capability
// catalog (backed by llm_model_pricing.supports_image_input) can get before
// the next lookup triggers a reload.
const imageSupportCacheTTL = 5 * time.Minute

// imageSupportCache is a lazily-refreshed, in-memory view of the explicit
// per-model vision-capability verdicts recorded in llm_model_pricing. Only
// (provider, model) pairs with a non-NULL supports_image_input row are
// present — a missing entry means "unknown", not "false".
type imageSupportCache struct {
	mu       sync.RWMutex
	values   map[string]bool
	loadedAt time.Time
	// refreshMu serializes refresh attempts so concurrent stale lookups don't
	// all hit the DB at once (a request storm right as the TTL expires).
	refreshMu sync.Mutex
}

var imageSupport = &imageSupportCache{}

// lookup returns the recorded vision-capability verdict for (provider, model).
// known is false when there is no explicit DB verdict (column NULL, no row,
// or the catalog could not be loaded) — callers must fall back to their own
// default heuristic in that case.
func (c *imageSupportCache) lookup(provider, model string) (supported bool, known bool) {
	c.mu.RLock()
	stale := time.Since(c.loadedAt) > imageSupportCacheTTL
	values := c.values
	c.mu.RUnlock()

	if stale {
		values = c.refresh()
	}

	supported, known = values[provider+":"+model]
	return supported, known
}

// refresh reloads the catalog via the conversation DAO. Serialized by
// refreshMu: double-checked locking means a goroutine that waited on the
// lock re-checks staleness before querying, so only one goroutine per
// TTL window actually hits the DB — the rest reuse whatever it loaded.
//
// Best-effort and does NOT cache failure: if the DAO/DB is unavailable, the
// previous snapshot (if any) is returned as-is and loadedAt is left
// untouched, so the very next lookup retries immediately rather than
// treating an outage (or a DAO that simply hasn't been established yet at
// startup) as a 5-minute-long "known empty" catalog — which, combined with
// IsVisionCapableModel's default-deny fallback, would otherwise disable
// image support for every model during that window.
//
// Uses PeekConversationDao (not GetConversationDao) deliberately — this is a
// nice-to-have lookup and must never pay the cost of establishing a fresh DB
// connection itself. By the time a real request reaches an image check, the
// conversation DAO singleton is already warm from earlier, mandatory use in
// the same request's conversation lifecycle.
func (c *imageSupportCache) refresh() map[string]bool {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	c.mu.RLock()
	stale := time.Since(c.loadedAt) > imageSupportCacheTTL
	values := c.values
	c.mu.RUnlock()
	if !stale {
		return values
	}

	dao := PeekConversationDao()
	if dao == nil {
		return values
	}

	catalog, err := dao.GetImageSupportCatalog()
	if err != nil {
		slog.Warn("image: failed to load image-support catalog, falling back to default heuristic", "error", err)
		return values
	}

	return c.markRefreshed(catalog)
}

func (c *imageSupportCache) markRefreshed(catalog map[string]bool) map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadedAt = time.Now()
	c.values = catalog
	return c.values
}
