package api

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Leader jobs: tenants take turns, each gets a bounded share of a tick, and one slow cluster does not stall the
// others.
func TestTickScheduler_RoundRobinWithAPerTenantCap(t *testing.T) {
	swap(t, &tickTenantOf, func(accountId string) string { return accountId[:1] })
	var ran []string
	item := func(key string) tickItem {
		return tickItem{Key: key, AccountId: key, Run: func() { ran = append(ran, key) }}
	}
	items := []tickItem{item("a1"), item("a2"), item("a3"), item("b1"), item("c1"), item("c2")}
	s := newTickScheduler("test")
	s.each(items, 2, time.Second)
	assert.Equal(t, []string{"a1", "b1", "c1", "a2", "c2"}, ran, "round-robin, at most two of a tenant")

	ran = nil
	s.each(items, 2, time.Second)
	assert.Equal(t, []string{"b1", "c1", "a1", "c2", "a2"}, ran, "the next tick starts with the next tenant")
}

func TestTickScheduler_SlowTenantDoesNotStallOthers(t *testing.T) {
	swap(t, &tickTenantOf, func(accountId string) string { return accountId[:1] })
	release := make(chan struct{})
	var mu sync.Mutex
	var ran []string
	record := func(key string) {
		mu.Lock()
		defer mu.Unlock()
		ran = append(ran, key)
	}
	items := []tickItem{
		{Key: "a1", AccountId: "a1", Run: func() { record("a1"); <-release }}, // a cluster that does not answer
		{Key: "a2", AccountId: "a2", Run: func() { record("a2") }},
		{Key: "b1", AccountId: "b1", Run: func() { record("b1") }},
		{Key: "b2", AccountId: "b2", Run: func() { record("b2") }},
	}
	s := newTickScheduler("test")
	s.lead = "b" // a goes first
	start := time.Now()
	s.each(items, 10, 50*time.Millisecond)
	assert.Less(t, time.Since(start), time.Second, "the tick moved on")
	mu.Lock()
	assert.Equal(t, []string{"a1", "b1", "b2"}, ran, "the slow tenant's other rows wait; the other tenant's run")
	ran = nil
	mu.Unlock()

	// Next tick: the overrunning row is still running and is not started again.
	s.each(items, 10, 50*time.Millisecond)
	mu.Lock()
	assert.NotContains(t, ran, "a1")
	assert.Contains(t, ran, "a2")
	mu.Unlock()
	close(release)
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.inflight["a1"]
	}, time.Second, 10*time.Millisecond, "once it returns it may run again")
}
