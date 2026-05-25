package core

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
)

const testTenantID = "tenant-1"

// newTestRequestContext builds a minimal RequestContext suitable for unit tests
// that don't need a real DB. The cache code path runs before any DB lookup so
// this is enough to verify the cache hit / miss behavior of
// LoadActiveGlobalContext and InvalidateActiveGCCache.
func newTestRequestContext(t *testing.T) *security.RequestContext {
	t.Helper()
	tracer := otel.Tracer("test")
	meter := otel.Meter("test")
	sc := security.NewSecurityContextForTenantAccountAdmin(testTenantID, "user-1", []string{"account-1"})
	return security.NewRequestContext(context.Background(), sc, slog.Default(), tracer, meter)
}

func TestLoadActiveGlobalContext_CacheHit_BypassesDB(t *testing.T) {
	const accountID = "acct-cache-hit"
	t.Cleanup(func() { InvalidateActiveGCCache(testTenantID, accountID) })

	gcActiveCache.Store(gcCacheKey(testTenantID, accountID), gcCacheEntry{
		data:      "cached gc body",
		expiresAt: time.Now().Add(time.Hour),
	})

	got := LoadActiveGlobalContext(newTestRequestContext(t), accountID)
	assert.Equal(t, "cached gc body", got, "cache hit must return stored data without touching DB")
}

func TestLoadActiveGlobalContext_EmptyAccountID(t *testing.T) {
	got := LoadActiveGlobalContext(newTestRequestContext(t), "")
	assert.Equal(t, "", got, "empty account id must return empty without touching DB")
}

func TestLoadActiveGlobalContext_ExpiredCacheEntry(t *testing.T) {
	const accountID = "acct-cache-expired"
	t.Cleanup(func() { InvalidateActiveGCCache(testTenantID, accountID) })

	gcActiveCache.Store(gcCacheKey(testTenantID, accountID), gcCacheEntry{
		data:      "stale",
		expiresAt: time.Now().Add(-time.Minute),
	})

	// With no DB available, an expired entry will fall through to the DB
	// path, which soft-fails to "". The point of this test is that we
	// don't return "stale".
	got := LoadActiveGlobalContext(newTestRequestContext(t), accountID)
	assert.NotEqual(t, "stale", got, "expired cache entry must not be returned")
}

func TestLoadActiveGlobalContext_TenantIsolation(t *testing.T) {
	// A cache entry stored under tenant A must not be returned when a
	// request authenticated as tenant B loads the same account_id.
	const accountID = "acct-shared"
	const otherTenantID = "tenant-other"
	t.Cleanup(func() {
		InvalidateActiveGCCache(testTenantID, accountID)
		InvalidateActiveGCCache(otherTenantID, accountID)
	})

	gcActiveCache.Store(gcCacheKey(otherTenantID, accountID), gcCacheEntry{
		data:      "other-tenant-secret",
		expiresAt: time.Now().Add(time.Hour),
	})

	got := LoadActiveGlobalContext(newTestRequestContext(t), accountID)
	assert.NotEqual(t, "other-tenant-secret", got, "must not return another tenant's cached data")
}

func TestInvalidateActiveGCCache_RemovesEntry(t *testing.T) {
	const accountID = "acct-invalidate"
	gcActiveCache.Store(gcCacheKey(testTenantID, accountID), gcCacheEntry{
		data:      "to be cleared",
		expiresAt: time.Now().Add(time.Hour),
	})

	InvalidateActiveGCCache(testTenantID, accountID)

	_, ok := gcActiveCache.Load(gcCacheKey(testTenantID, accountID))
	assert.False(t, ok, "cache entry must be removed after invalidation")
}
