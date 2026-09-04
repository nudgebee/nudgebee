package common

import (
	"nudgebee/services/config"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCachingLocal(t *testing.T) {
	CacheCreateNamespace("test", CacheNamespaceWithExpiration(10*time.Second), CacheNamespaceWithMaxEntries(1000))
	CacheCreateNamespace("test1", CacheNamespaceWithExpiration(10*time.Second), CacheNamespaceWithMaxEntries(1000))

	err := CacheSet("test", "k1", []byte("v1"))
	assert.Nil(t, err)
	err = CacheSet("test", "k2", []byte("v2"))
	assert.Nil(t, err)
	err = CacheSet("test", "k1", []byte("v3"))
	assert.Nil(t, err)

	err = CacheSet("test1", "k1", []byte("v11"))
	assert.Nil(t, err)
	err = CacheSet("test1", "k2", []byte("v21"))
	assert.Nil(t, err)
	err = CacheSet("test1", "k3", []byte("v31"))
	assert.Nil(t, err)

	v, ok := CacheGet("test", "k1")
	assert.True(t, ok)
	assert.Equal(t, []byte("v3"), v)
	v, ok = CacheGet("test", "k2")
	assert.True(t, ok)
	assert.Equal(t, []byte("v2"), v)

	keys, err := CacheListKeys("test")
	assert.Nil(t, err)
	slices.Sort(keys)
	assert.Equal(t, []string{"k1", "k2"}, keys)

	err = CacheDelete("test", "k1")
	assert.Nil(t, err)
	_, ok = CacheGet("test", "k1")
	assert.False(t, ok)

	keys, err = CacheListKeys("test1")
	assert.Nil(t, err)
	slices.Sort(keys)
	assert.Equal(t, []string{"k1", "k2", "k3"}, keys)

}

func TestCachingRedis(t *testing.T) {
	// testenv lives under internal/database, which imports common, so an
	// internal (package common) test cannot import it without a cycle. Guard
	// on the Redis host env directly: without a reachable Redis the cache falls
	// back to in-memory bigcache and the redis-typed code path panics.
	if os.Getenv("REDIS_SERVER_HOST") == "" {
		t.Skip("skipping: requires environment variable(s) REDIS_SERVER_HOST")
	}
	config.Config.CacheProvider = "redis"

	CacheCreateNamespace("test", CacheNamespaceWithExpiration(10*time.Second), CacheNamespaceWithMaxEntries(1000))
	CacheCreateNamespace("test1", CacheNamespaceWithExpiration(10*time.Second), CacheNamespaceWithMaxEntries(1000))

	err := CacheSet("test", "k1", []byte("v1"))
	assert.Nil(t, err)
	err = CacheSet("test", "k2", []byte("v2"))
	assert.Nil(t, err)
	err = CacheSet("test", "k1", []byte("v3"))
	assert.Nil(t, err)

	err = CacheSet("test1", "k1", []byte("v11"))
	assert.Nil(t, err)
	err = CacheSet("test1", "k2", []byte("v21"))
	assert.Nil(t, err)
	err = CacheSet("test1", "k3", []byte("v31"))
	assert.Nil(t, err)

	v, ok := CacheGet("test", "k1")
	assert.True(t, ok)
	assert.Equal(t, []byte("v3"), v)
	v, ok = CacheGet("test", "k2")
	assert.True(t, ok)
	assert.Equal(t, []byte("v2"), v)

	keys, err := CacheListKeys("test")
	assert.Nil(t, err)
	slices.Sort(keys)
	assert.Equal(t, []string{"k1", "k2"}, keys)

	err = CacheDelete("test", "k1")
	assert.Nil(t, err)
	_, ok = CacheGet("test", "k1")
	assert.False(t, ok)

	keys, err = CacheListKeys("test1")
	assert.Nil(t, err)
	slices.Sort(keys)
	assert.Equal(t, []string{"k1", "k2", "k3"}, keys)

}

// TestCacheDeleteMissingKeyIsNoOp pins delete as idempotent. The bigcache backend
// reports a missing key as an error (redis DEL just returns 0), so without the
// not-found guard every invalidation of an entry nobody had cached yet logged a
// warning plus a stack trace on a completely normal path.
func TestCacheDeleteMissingKeyIsNoOp(t *testing.T) {
	const ns = "test_delete_missing"
	CacheCreateNamespace(ns, CacheNamespaceWithExpiration(time.Minute))

	assert.NoError(t, CacheDelete(ns, "never-cached"), "deleting an absent key must not error")

	assert.NoError(t, CacheSet(ns, "present", []byte("v")))
	assert.NoError(t, CacheDelete(ns, "present"), "deleting a present key must not error")
	_, found := CacheGet(ns, "present")
	assert.False(t, found, "the key must actually be gone")

	assert.NoError(t, CacheDelete(ns, "present"), "deleting the same key twice must stay a no-op")
}

// TestCacheDeleteWithTagScopedToTag proves CacheDeleteWithTag invalidates only the
// entries carrying the given tag, NOT the whole namespace. Regression for a bug
// where the namespace tag ("namespace:<ns>", stamped on every entry at set time)
// was appended to the invalidation set — and gocache tag-invalidation is an OR,
// so any tag-scoped delete wiped every entry in the namespace. Every caller here
// (security context, default log/trace filters, log label mappings) invalidates
// per account/tenant/user, so the widened delete evicted every OTHER tenant's
// entries too on each such call.
func TestCacheDeleteWithTagScopedToTag(t *testing.T) {
	const ns = "test_delete_with_tag_scope"
	CacheCreateNamespace(ns, CacheNamespaceWithExpiration(time.Minute))

	assert.NoError(t, CacheSet(ns, "acctA", []byte("a"), CacheSetWithTags("account:A")))
	assert.NoError(t, CacheSet(ns, "acctB", []byte("b"), CacheSetWithTags("account:B")))

	// Invalidate only account A's tag.
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
// uses when a caller passes no expiration. Under the redis provider that fallback
// is what stops an entry from being stored with no TTL at all (gocache passes the
// expiration straight to SET, where 0 means "keep forever"), which turned any
// missed invalidation into a permanently stale entry. The applied TTL itself is
// not observable here — the in-memory bigcache store has no per-key expiry — so
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
