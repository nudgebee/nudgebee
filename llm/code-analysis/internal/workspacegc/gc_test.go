package workspacegc

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCollectIfIdleNeverTouchesActiveRun(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	runs := filepath.Join(root, "runs")
	if err := os.MkdirAll(filepath.Join(cache, "shared"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runs, "run-b"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "shared", "artifact"), []byte("cache"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "run-b", "worktree"), []byte("active"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewWithDiskUsage(root, []string{cache}, func(string) (float64, error) { return 0.90, nil })
	release := m.Acquire("run-b")
	if got, err := m.CollectIfIdle(context.Background()); err != nil || got != 0 {
		t.Fatalf("active collection = (%d, %v), want (0, nil)", got, err)
	}
	if _, err := os.Stat(filepath.Join(runs, "run-b", "worktree")); err != nil {
		t.Fatalf("active worktree was removed: %v", err)
	}
	release()

	if _, err := os.Stat(filepath.Join(runs, "run-b")); !os.IsNotExist(err) {
		t.Fatalf("completed run directory still exists or returned unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "shared")); !os.IsNotExist(err) {
		t.Fatalf("cache contents still exist or returned unexpected error: %v", err)
	}
}

// TestCollectionWaitsForFinalRelease verifies the active-count guard: releasing
// one lease while another is still held collects nothing, and only the final
// release runs collection.
func TestCollectionWaitsForFinalRelease(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "artifact"), []byte("cache"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runs", "run-b"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runs", "run-b", "worktree"), []byte("active"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewWithDiskUsage(root, []string{cache}, func(string) (float64, error) { return 0.90, nil })
	releaseA := m.Acquire("run-a")
	releaseB := m.Acquire("run-b")

	releaseA()
	if _, err := os.Stat(filepath.Join(root, "runs", "run-b", "worktree")); err != nil {
		t.Fatalf("run directory removed while another lease was active: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "artifact")); err != nil {
		t.Fatalf("shared cache removed while another lease was active: %v", err)
	}

	releaseB()
	if _, err := os.Stat(filepath.Join(root, "runs", "run-b")); !os.IsNotExist(err) {
		t.Fatalf("idle run directory not collected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "artifact")); !os.IsNotExist(err) {
		t.Fatalf("idle shared cache not collected above threshold: %v", err)
	}
}

// TestSharedCacheReclaimedByStorageLimit exercises the real (non-injected)
// usage check: the workspace tree's own footprint is measured against the pod's
// ephemeral-storage budget, not the node filesystem.
func TestSharedCacheReclaimedByStorageLimit(t *testing.T) {
	newWorkspace := func(t *testing.T, limitBytes int64) (*Manager, string) {
		t.Helper()
		root := t.TempDir()
		cache := filepath.Join(root, "cache")
		if err := os.MkdirAll(cache, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, "artifact"), make([]byte, 4096), 0600); err != nil {
			t.Fatal(err)
		}
		return New(root, []string{cache}, limitBytes), filepath.Join(cache, "artifact")
	}

	t.Run("above threshold reclaims", func(t *testing.T) {
		m, artifact := newWorkspace(t, 4096) // usage ~1.0, over the 0.80 mark
		release := m.Acquire("run-a")
		release()
		if _, err := os.Stat(artifact); !os.IsNotExist(err) {
			t.Fatalf("shared cache not reclaimed above the storage limit: %v", err)
		}
	})

	t.Run("below threshold stays warm", func(t *testing.T) {
		m, artifact := newWorkspace(t, 1<<30) // 4KiB / 1GiB is well under 0.80
		release := m.Acquire("run-a")
		release()
		if _, err := os.Stat(artifact); err != nil {
			t.Fatalf("shared cache reclaimed below the storage limit: %v", err)
		}
	})
}

func TestAcquireCancelsInProgressCleanupAndWaits(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, 0)

	m.mu.Lock()
	m.cleanupRunning = true
	m.cleanupDone = make(chan struct{})
	m.cancelCleanup = make(chan struct{})
	cancelCh := m.cancelCleanup
	doneCh := m.cleanupDone
	m.mu.Unlock()

	acquireReturned := make(chan func(), 1)
	go func() {
		acquireReturned <- m.Acquire("new-run")
	}()

	select {
	case <-cancelCh:
		// Acquire signalled the in-progress cleanup to stop.
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not signal cancellation of the in-progress cleanup")
	}

	select {
	case <-acquireReturned:
		t.Fatal("Acquire returned before the in-progress cleanup finished")
	default:
	}

	m.mu.Lock()
	m.cleanupRunning = false
	close(doneCh)
	m.mu.Unlock()

	select {
	case release := <-acquireReturned:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not unblock once the in-progress cleanup finished")
	}
}

func TestCollectIfIdleStopsEarlyWhenAcquireCancels(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "artifact"), []byte("warm cache"), 0600); err != nil {
		t.Fatal(err)
	}

	diskUsageCalled := make(chan struct{})
	proceed := make(chan struct{})
	var signalOnce sync.Once
	m := NewWithDiskUsage(root, []string{cache}, func(string) (float64, error) {
		// release()'s own CollectIfIdle call re-enters this closure; only the
		// first invocation needs to signal and wait — proceed is already
		// closed by then, so later calls return immediately.
		signalOnce.Do(func() { close(diskUsageCalled) })
		<-proceed
		return 0.90, nil
	})

	collectDone := make(chan struct{})
	go func() {
		_, _ = m.CollectIfIdle(context.Background())
		close(collectDone)
	}()
	<-diskUsageCalled // runRoots is done; CollectIfIdle is blocked in the disk-usage check.

	m.mu.Lock()
	cancelCh := m.cancelCleanup
	m.mu.Unlock()

	acquireReturned := make(chan func(), 1)
	go func() {
		acquireReturned <- m.Acquire("new-run")
	}()

	select {
	case <-cancelCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not cancel the in-progress cleanup")
	}
	close(proceed)

	select {
	case <-collectDone:
	case <-time.After(2 * time.Second):
		t.Fatal("CollectIfIdle did not stop after cancellation")
	}

	release := <-acquireReturned
	defer release()

	if _, err := os.Stat(filepath.Join(cache, "artifact")); err != nil {
		t.Fatalf("cancelled cleanup should not have removed the shared cache: %v", err)
	}
}

func TestSharedCacheSurvivesBelowDiskUsageThreshold(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	cacheFile := filepath.Join(cache, "artifact")
	if err := os.WriteFile(cacheFile, []byte("warm cache"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewWithDiskUsage(root, []string{cache}, func(string) (float64, error) { return 0.79, nil })
	release := m.Acquire("run-a")
	release()
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("warm shared cache was removed below threshold: %v", err)
	}
}
