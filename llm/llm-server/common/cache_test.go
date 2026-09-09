package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheDeleteWithTag_ScopedToTag proves CacheDeleteWithTag invalidates only
// the entries carrying the given tag, NOT the whole namespace. Regression for a
// bug where the namespace tag ("namespace:<ns>", stamped on every entry at set
// time) was appended to the invalidation set — and gocache tag-invalidation is
// an OR, so any tag-scoped delete wiped every entry in the namespace.
func TestCacheDeleteWithTag_ScopedToTag(t *testing.T) {
	ns := "test.cache.scoped"
	CacheCreateNamespace(ns)

	require.NoError(t, CacheSet(ns, "userA", []byte("a"), CacheSetWithTags("user:A")))
	require.NoError(t, CacheSet(ns, "userB", []byte("b"), CacheSetWithTags("user:B")))

	// Invalidate only user A's tag.
	require.NoError(t, CacheDeleteWithTag(ns, "user:A"))

	_, okA := CacheGet(ns, "userA")
	_, okB := CacheGet(ns, "userB")
	assert.False(t, okA, "user A's tagged entry must be invalidated")
	assert.True(t, okB, "user B's entry must survive a tag-scoped delete (not namespace-wide)")

	// An empty tag set is a no-op, not a namespace wipe.
	require.NoError(t, CacheDeleteWithTag(ns))
	_, okB1 := CacheGet(ns, "userB")
	assert.True(t, okB1, "CacheDeleteWithTag with no tags must not invalidate anything")

	// CacheClear still wipes the whole namespace.
	require.NoError(t, CacheClear(ns))
	_, okB2 := CacheGet(ns, "userB")
	assert.False(t, okB2, "CacheClear must drop everything in the namespace")
}

// TestCacheNamespaceExpiration_DefaultsAppliedAtSet covers the TTL fallback
// CacheSet uses when a caller passes no expiration. Under the redis provider
// that fallback is what stops an entry from being stored with no TTL at all
// (gocache passes the expiration straight to SET, where 0 means "keep
// forever"), turning any missed invalidation into a permanent stale entry. The
// applied TTL itself is not observable here — the in-memory bigcache store has
// no per-key expiry — so this asserts the value CacheSet reads.
func TestCacheNamespaceExpiration_DefaultsAppliedAtSet(t *testing.T) {
	ns := "test.cache.ttl"
	CacheCreateNamespace(ns, CacheNamespaceWithExpiration(7*time.Minute))

	assert.Equal(t, 7*time.Minute, cacheNamespaceExpiration(ns),
		"a set with no explicit expiration must inherit the namespace TTL")
	assert.Equal(t, time.Duration(0), cacheNamespaceExpiration("test.cache.never.registered"),
		"an unknown namespace must not invent a TTL")

	// Registered namespaces without an explicit option fall back to the
	// configured default, never to "no expiry".
	nsDefault := "test.cache.ttl.default"
	CacheCreateNamespace(nsDefault)
	assert.Positive(t, cacheNamespaceExpiration(nsDefault))
}
