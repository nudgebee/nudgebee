package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCacheDeleteWithTagScopedToTag proves CacheDeleteWithTag invalidates only the
// entries carrying the given tag, NOT the whole namespace. Every entry is tagged
// "namespace:<ns>" at set time and gocache tag-invalidation is an OR, so appending
// that tag to the invalidation set turned any tag-scoped delete into a
// namespace-wide wipe. Same regression as api-server's and llm-server's copies of
// this file.
func TestCacheDeleteWithTagScopedToTag(t *testing.T) {
	const ns = "test_delete_with_tag_scope"
	CacheCreateNamespace(ns, CacheNamespaceWithExpiration(time.Minute))

	assert.NoError(t, CacheSet(ns, "acctA", []byte("a"), CacheSetWithTags("account:A")))
	assert.NoError(t, CacheSet(ns, "acctB", []byte("b"), CacheSetWithTags("account:B")))

	assert.NoError(t, CacheDeleteWithTag(ns, "account:A"))

	_, okA := CacheGet(ns, "acctA")
	_, okB := CacheGet(ns, "acctB")
	assert.False(t, okA, "account A's tagged entry must be invalidated")
	assert.True(t, okB, "account B's entry must survive a tag-scoped delete (not namespace-wide)")

	// An empty tag set is a no-op, not a namespace wipe.
	assert.NoError(t, CacheDeleteWithTag(ns))
	_, okB1 := CacheGet(ns, "acctB")
	assert.True(t, okB1, "CacheDeleteWithTag with no tags must not invalidate anything")

	// CacheClear still wipes the whole namespace.
	assert.NoError(t, CacheClear(ns))
	_, okB2 := CacheGet(ns, "acctB")
	assert.False(t, okB2, "CacheClear must drop everything in the namespace")
}

// TestCacheNamespaceExpirationDefaultsAppliedAtSet covers the TTL fallback CacheSet
// uses when a caller passes no expiration. Under the redis provider that fallback is
// what stops an entry from being stored with no TTL at all (gocache passes the
// expiration straight to SET, where 0 means "keep forever"). The applied TTL itself
// is not observable here — the in-memory bigcache store has no per-key expiry — so
// this asserts the value CacheSet reads.
func TestCacheNamespaceExpirationDefaultsAppliedAtSet(t *testing.T) {
	const ns = "test_namespace_ttl"
	CacheCreateNamespace(ns, CacheNamespaceWithExpiration(7*time.Minute))

	assert.Equal(t, 7*time.Minute, cacheNamespaceExpiration(ns),
		"a set with no explicit expiration must inherit the namespace TTL")
	assert.Equal(t, time.Duration(0), cacheNamespaceExpiration("test_namespace_never_registered"),
		"an unknown namespace must not invent a TTL")

	// Registered namespaces without an explicit option fall back to the
	// configured default, never to "no expiry".
	const nsDefault = "test_namespace_ttl_default"
	CacheCreateNamespace(nsDefault)
	assert.Positive(t, cacheNamespaceExpiration(nsDefault))
}
