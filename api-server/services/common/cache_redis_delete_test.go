package common

import (
	"nudgebee/services/config"
	"testing"

	"github.com/stretchr/testify/assert"
)

// CacheRedisDeleteNamespace is a no-op under the in-memory provider — there
// is no shared Redis to SCAN, and per-process bigcache layers in other
// services can't be reached from here. Pinning the contract so a future
// refactor that "accidentally tries to clear bigcache too" doesn't ship.
func TestCacheRedisDeleteNamespace_InMemoryIsNoOp(t *testing.T) {
	old := config.Config.CacheProvider
	config.Config.CacheProvider = "" // forces in-memory branch
	t.Cleanup(func() { config.Config.CacheProvider = old })

	CacheCreateNamespace("itest.redisdel_inmem")
	_ = CacheSet("itest.redisdel_inmem", "k1", []byte("v"))

	n, err := CacheRedisDeleteNamespace("itest.redisdel_inmem")
	assert.NoError(t, err)
	assert.Equal(t, 0, n, "in-memory provider must report 0 cleared (silent no-op)")
}
