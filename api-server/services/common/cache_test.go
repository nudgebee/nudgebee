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
