package aws

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"
)

func newDedupTestCtx(t *testing.T) (providers.CloudProviderContext, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	return providers.NewCloudProviderContext(ctx), cancel
}

// newBoundDeduper returns a deduper with its context already bound — the
// typical setup for a test that exercises Allow().
func newBoundDeduper(t *testing.T, ttl time.Duration, onFire func(providers.CloudProviderContext, EventBridgeEvent, providers.Account)) (*ruleDeduper, context.CancelFunc) {
	t.Helper()
	pCtx, cancel := newDedupTestCtx(t)
	d := newRuleDeduper(ttl, onFire)
	d.bindContext(pCtx)
	return d, cancel
}

// waitUntil polls cond until it returns true or the deadline passes. Used
// instead of a fixed sleep so the trailing-fire goroutine has a chance to
// run on slow CI without making fast machines wait the worst case.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waitUntil: condition not satisfied within %v", timeout)
}

func TestRuleDeduper_FirstEventAllowedSubsequentSuppressed(t *testing.T) {
	d, cancel := newBoundDeduper(t, 50*time.Millisecond, func(pc providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {})
	defer cancel()

	if !d.Allow("fp1", EventBridgeEvent{ID: "a"}, providers.Account{}) {
		t.Fatalf("first Allow should return true")
	}
	for i := 0; i < 5; i++ {
		if d.Allow("fp1", EventBridgeEvent{ID: "b"}, providers.Account{}) {
			t.Fatalf("duplicate Allow should return false (i=%d)", i)
		}
	}
}

func TestRuleDeduper_TrailingFireUsesLatestEvent(t *testing.T) {
	var (
		mu        sync.Mutex
		fireCount int
		lastEv    EventBridgeEvent
	)
	d, cancel := newBoundDeduper(t, 40*time.Millisecond, func(pc providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {
		mu.Lock()
		fireCount++
		lastEv = ev
		mu.Unlock()
	})
	defer cancel()

	// First event fires the action immediately (in caller). The deduper
	// itself does not call onFire for the first one — that's the caller's
	// job. So fireCount stays 0 here.
	if !d.Allow("fp1", EventBridgeEvent{ID: "first"}, providers.Account{}) {
		t.Fatalf("first Allow should return true")
	}
	// Second + third arrive during the TTL — last one wins for trailing fire.
	if d.Allow("fp1", EventBridgeEvent{ID: "second"}, providers.Account{}) {
		t.Fatalf("second Allow should return false")
	}
	if d.Allow("fp1", EventBridgeEvent{ID: "third"}, providers.Account{}) {
		t.Fatalf("third Allow should return false")
	}

	waitUntil(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return fireCount >= 1
	})

	mu.Lock()
	if fireCount != 1 {
		t.Fatalf("expected exactly 1 trailing fire, got %d", fireCount)
	}
	if lastEv.ID != "third" {
		t.Fatalf("expected trailing fire on event 'third', got %q", lastEv.ID)
	}
	mu.Unlock()
}

func TestRuleDeduper_QuietWindowRemovesEntryAndNextAllows(t *testing.T) {
	d, cancel := newBoundDeduper(t, 30*time.Millisecond, func(pc providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {})
	defer cancel()

	if !d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
		t.Fatalf("first Allow should return true")
	}
	// No further events during the window — entry should be removed.
	waitUntil(t, time.Second, func() bool {
		_, ok := d.entries.Load("fp1")
		return !ok
	})
	// Now a new event with the same fingerprint should be allowed again.
	if !d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
		t.Fatalf("Allow after quiet window should return true (entry not cleaned up)")
	}
}

func TestRuleDeduper_DistinctFingerprintsIndependent(t *testing.T) {
	d, cancel := newBoundDeduper(t, time.Second, func(pc providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {})
	defer cancel()

	if !d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
		t.Fatalf("fp1 first Allow should be true")
	}
	if !d.Allow("fp2", EventBridgeEvent{}, providers.Account{}) {
		t.Fatalf("fp2 first Allow should be true (independent of fp1)")
	}
	if d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
		t.Fatalf("fp1 second Allow should be deduped")
	}
}

func TestRuleDeduper_CtxCancelStopsGoroutine(t *testing.T) {
	d, cancel := newBoundDeduper(t, time.Hour, func(pc providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {
		t.Errorf("onFire should not be called after ctx cancel")
	})
	if !d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
		t.Fatalf("first Allow should be true")
	}
	// Mark pending so the goroutine has work queued. If ctx cancel doesn't
	// short-circuit, the test would either hang or fire onFire (failing).
	if d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
		t.Fatalf("second Allow should be deduped")
	}
	cancel()
	waitUntil(t, time.Second, func() bool {
		_, ok := d.entries.Load("fp1")
		return !ok
	})
}

func TestRuleDeduper_ConcurrentAllowsAllSafe(t *testing.T) {
	var fires int32
	d, cancel := newBoundDeduper(t, 50*time.Millisecond, func(pc providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {
		atomic.AddInt32(&fires, 1)
	})
	defer cancel()

	const goroutines = 50
	var wg sync.WaitGroup
	var allowedCount int32
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
				atomic.AddInt32(&allowedCount, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&allowedCount); got != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent Allows to return true, got %d", goroutines, got)
	}
	// Trailing fire should happen at most once.
	waitUntil(t, time.Second, func() bool { return atomic.LoadInt32(&fires) >= 1 })
	if got := atomic.LoadInt32(&fires); got != 1 {
		t.Fatalf("expected exactly 1 trailing fire, got %d", got)
	}
}

// TestRuleDeduper_UnboundIsNoOp covers the degraded-mode path where the
// processor was constructed but BindContext was never called (e.g. a unit
// test that doesn't exercise dedup). Allow should always return true so
// the caller proceeds as if there was no deduper, rather than dropping
// events silently.
func TestRuleDeduper_UnboundIsNoOp(t *testing.T) {
	d := newRuleDeduper(time.Minute, func(pc providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {
		t.Errorf("onFire should not be called on an unbound deduper")
	})
	for i := 0; i < 5; i++ {
		if !d.Allow("fp1", EventBridgeEvent{}, providers.Account{}) {
			t.Fatalf("Allow on unbound deduper should always return true (i=%d)", i)
		}
	}
	if _, ok := d.entries.Load("fp1"); ok {
		t.Fatalf("unbound deduper should not create entries")
	}
}
