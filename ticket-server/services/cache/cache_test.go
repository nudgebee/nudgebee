package cache

import (
	"context"
	"testing"
	"time"
)

// These tests run against the in-memory (bigcache) provider, which is the default
// when cache_provider is unset — so they need no Redis. The graceful-degradation
// contract (miss on unregistered namespace, no panics) is provider-agnostic.

func TestCache_HitAndMiss(t *testing.T) {
	const ns = "test_hit_miss"
	CreateNamespace(ns)

	if err := Set(context.Background(), ns, "k1", []byte("v1"), time.Minute, "int-A"); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if got, ok := Get(context.Background(), ns, "k1"); !ok || string(got) != "v1" {
		t.Fatalf("Get(k1) = (%q, %v), want (\"v1\", true)", string(got), ok)
	}
	if _, ok := Get(context.Background(), ns, "absent"); ok {
		t.Error("Get(absent) should miss")
	}
}

func TestCache_UnregisteredNamespaceMisses(t *testing.T) {
	if _, ok := Get(context.Background(), "never_created", "k"); ok {
		t.Error("Get on an unregistered namespace must miss, not panic")
	}
	if err := Set(context.Background(), "never_created", "k", []byte("v"), time.Minute, "int"); err == nil {
		t.Error("Set on an unregistered namespace should return an error (caller swallows it)")
	}
}

func TestCache_DeleteByIntegration(t *testing.T) {
	const ns = "test_del_by_integration"
	CreateNamespace(ns)

	// Two entries for integration A, one for B.
	_ = Set(context.Background(), ns, "a:proj1", []byte("1"), time.Minute, "int-A")
	_ = Set(context.Background(), ns, "a:proj2", []byte("2"), time.Minute, "int-A")
	_ = Set(context.Background(), ns, "b:proj1", []byte("3"), time.Minute, "int-B")

	if err := DeleteByIntegration(context.Background(), ns, "int-A"); err != nil {
		t.Fatalf("DeleteByIntegration returned error: %v", err)
	}

	if _, ok := Get(context.Background(), ns, "a:proj1"); ok {
		t.Error("int-A entry a:proj1 should be invalidated")
	}
	if _, ok := Get(context.Background(), ns, "a:proj2"); ok {
		t.Error("int-A entry a:proj2 should be invalidated")
	}
	if _, ok := Get(context.Background(), ns, "b:proj1"); !ok {
		t.Error("int-B entry must survive invalidation of int-A")
	}
}
