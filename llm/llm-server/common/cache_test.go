package common

import (
	"testing"

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
