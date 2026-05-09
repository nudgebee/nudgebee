package common

import (
	"nudgebee/collector/otel/config"
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
