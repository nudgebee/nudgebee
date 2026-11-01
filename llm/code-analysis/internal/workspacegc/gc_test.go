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

// writeAged creates path (and its parents) with the given content age. The
// parents' own mtimes are set too, since creating a file bumps its directory.
func writeAged(t *testing.T, path string, age time.Duration, parents ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("leftover"), 0600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	for _, p := range append([]string{path}, parents...) {
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgedRootRemovesOnlyIdleChildren(t *testing.T) {
	root := t.TempDir()
	aged := filepath.Join(root, "exec_workspaces")
	const maxAge = 24 * time.Hour

	idleDir := filepath.Join(aged, "conv-idle")
	writeAged(t, filepath.Join(idleDir, "logs.txt"), 48*time.Hour, idleDir)

	freshDir := filepath.Join(aged, "conv-fresh")
	writeAged(t, filepath.Join(freshDir, "logs.txt"), time.Minute, freshDir)

	// The folder itself looks old, but a file deep inside it was just written.
	nestedDir := filepath.Join(aged, "conv-nested")
	writeAged(t, filepath.Join(nestedDir, "old.txt"), 48*time.Hour)
	writeAged(t, filepath.Join(nestedDir, "sub", "new.txt"), time.Minute)
	old := time.Now().Add(-48 * time.Hour)
	for _, dir := range []string{nestedDir, filepath.Join(nestedDir, "sub")} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}

	idleFile := filepath.Join(aged, "cli-old.log")
	writeAged(t, idleFile, 48*time.Hour)
	freshFile := filepath.Join(aged, "cli-new.log")
	writeAged(t, freshFile, time.Minute)

	m := New(root, nil, 0)
	m.AddAgedRoot(aged, maxAge)
	removed, err := m.CollectIfIdle(context.Background())
	if err != nil {
		t.Fatalf("CollectIfIdle: %v", err)
	}
	if removed == 0 {
		t.Fatal("CollectIfIdle reported no bytes removed for idle leftovers")
	}

	for _, gone := range []string{idleDir, idleFile} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("idle leftover %s still exists or returned unexpected error: %v", gone, err)
		}
	}
	for _, kept := range []string{
		filepath.Join(freshDir, "logs.txt"),
		filepath.Join(nestedDir, "old.txt"),
		filepath.Join(nestedDir, "sub", "new.txt"),
		freshFile,
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("recently used %s was removed: %v", kept, err)
		}
	}
}

// TestAgedRootWaitsForLeases verifies the lease rule still wins: an idle
// folder is left alone while any run or command is active, and collected on
// the final release.
func TestAgedRootWaitsForLeases(t *testing.T) {
	root := t.TempDir()
	aged := filepath.Join(root, "exec_workspaces")
	idleDir := filepath.Join(aged, "conv-idle")
	writeAged(t, filepath.Join(idleDir, "logs.txt"), 48*time.Hour, idleDir)

	m := New(root, nil, 0)
	m.AddAgedRoot(aged, 24*time.Hour)

	release := m.Acquire("command:conv-idle")
	if got, err := m.CollectIfIdle(context.Background()); err != nil || got != 0 {
		t.Fatalf("collection with an active lease = (%d, %v), want (0, nil)", got, err)
	}
	if _, err := os.Stat(filepath.Join(idleDir, "logs.txt")); err != nil {
		t.Fatalf("idle folder removed while a command was running: %v", err)
	}

	release()
	if _, err := os.Stat(idleDir); !os.IsNotExist(err) {
		t.Fatalf("idle folder not collected after the final release: %v", err)
	}
}

func TestAddAgedRootIgnoresUnusableInput(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, 0)
	refused := []struct {
		name   string
		path   string
		maxAge time.Duration
	}{
		{"empty path", "", time.Hour},
		{"filesystem root", string(filepath.Separator), time.Hour},
		{"the workspace root itself", root, time.Hour},
		{"a parent of the workspace root", filepath.Dir(root), time.Hour},
		{"non-positive maxAge", filepath.Join(root, "a"), 0},
	}
	// The same parent, spelled relative to the working directory.
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, filepath.Dir(root)); err == nil {
			refused = append(refused, struct {
				name   string
				path   string
				maxAge time.Duration
			}{"relative path to a parent", rel, time.Hour})
		}
	}
	for _, tt := range refused {
		if m.AddAgedRoot(tt.path, tt.maxAge) {
			t.Errorf("%s: AddAgedRoot(%q) reported success, want refusal", tt.name, tt.path)
		}
	}
	for _, maxAge := range []time.Duration{time.Hour, 2 * time.Hour} {
		if !m.AddAgedRoot(filepath.Join(root, "missing"), maxAge) {
			t.Fatalf("AddAgedRoot refused a usable path (maxAge %s)", maxAge)
		}
	}

	if len(m.agedRoots) != 1 || m.agedRoots[0].maxAge != 2*time.Hour {
		t.Fatalf("agedRoots = %+v, want one entry with the latest maxAge", m.agedRoots)
	}
	// A registered directory that does not exist is not an error.
	if got, err := m.CollectIfIdle(context.Background()); err != nil || got != 0 {
		t.Fatalf("collection of a missing aged root = (%d, %v), want (0, nil)", got, err)
	}
}

// TestAgedChildrenOnlyMatchingNames covers the layout the workspace pod really
// has: run checkouts sit in the temp directory next to unrelated entries, and
// the workspace root itself lives in that same directory.
func TestAgedChildrenOnlyMatchingNames(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "code-analysis")
	const prefix = "code-analysis-"
	old := 48 * time.Hour

	// The workspace root: old, and its name starts like a checkout's but is not one.
	writeAged(t, filepath.Join(root, "shared-cache", "artifact"), old, filepath.Join(root, "shared-cache"), root)
	abandoned := filepath.Join(tmp, "code-analysis-conv-abandoned")
	writeAged(t, filepath.Join(abandoned, "repo", "main.go"), old, filepath.Join(abandoned, "repo"), abandoned)
	// A checkout whose top folder is old but which a run wrote into just now.
	inUse := filepath.Join(tmp, "code-analysis-conv-in-use")
	writeAged(t, filepath.Join(inUse, "repo", "main.go"), time.Minute)
	for _, dir := range []string{inUse, filepath.Join(inUse, "repo")} {
		when := time.Now().Add(-old)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(tmp, "someone-elses-old-data")
	writeAged(t, filepath.Join(unrelated, "file"), old, unrelated)

	m := New(root, nil, 0)
	if m.AddAgedRoot(tmp, 2*time.Hour) {
		t.Fatal("AddAgedRoot accepted a parent of the workspace root")
	}
	if m.AddAgedChildren(tmp, "", 2*time.Hour) {
		t.Fatal("AddAgedChildren accepted an empty prefix")
	}
	if m.AddAgedChildren(tmp, "code-analysis", 2*time.Hour) {
		t.Fatal("AddAgedChildren accepted a prefix that matches the workspace root itself")
	}
	if !m.AddAgedChildren(tmp, prefix, 2*time.Hour) {
		t.Fatal("AddAgedChildren refused the temp directory with the checkout prefix")
	}
	if _, err := m.CollectIfIdle(context.Background()); err != nil {
		t.Fatalf("CollectIfIdle: %v", err)
	}

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("abandoned checkout still exists or returned unexpected error: %v", err)
	}
	for _, kept := range []string{
		filepath.Join(root, "shared-cache", "artifact"),
		filepath.Join(inUse, "repo", "main.go"),
		filepath.Join(unrelated, "file"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}

// TestUsageCountsEveryStorageRoot verifies the cache threshold is judged on the
// whole footprint that shares the budget, not on the workspace tree alone.
func TestUsageCountsEveryStorageRoot(t *testing.T) {
	const limit = 1 << 20 // 1 MiB budget
	setup := func(t *testing.T) (m *Manager, artifact, other string) {
		t.Helper()
		root := t.TempDir()
		cache := filepath.Join(root, "cache")
		artifact = filepath.Join(cache, "artifact")
		if err := os.MkdirAll(cache, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifact, make([]byte, 64<<10), 0600); err != nil { // 64 KiB: far below 80%
			t.Fatal(err)
		}
		other = t.TempDir()
		if err := os.WriteFile(filepath.Join(other, "scratch"), make([]byte, 900<<10), 0600); err != nil { // 900 KiB
			t.Fatal(err)
		}
		return New(root, []string{cache}, limit), artifact, other
	}

	t.Run("workspace alone is under the threshold", func(t *testing.T) {
		m, artifact, _ := setup(t)
		m.Acquire("run-a")()
		if _, err := os.Stat(artifact); err != nil {
			t.Fatalf("cache reclaimed although the workspace alone is under the threshold: %v", err)
		}
	})

	t.Run("another volume pushes the total over it", func(t *testing.T) {
		m, artifact, other := setup(t)
		m.SetUsageRoots([]string{other})
		m.Acquire("run-a")()
		if _, err := os.Stat(artifact); !os.IsNotExist(err) {
			t.Fatalf("cache not reclaimed although the total footprint is over the threshold: %v", err)
		}
	})
}

func TestOutermostDropsNestedAndDuplicateRoots(t *testing.T) {
	sep := string(filepath.Separator)
	got := outermost([]string{sep + "tmp" + sep + "code-analysis", sep + "tmp", sep + "home" + sep + "appuser", sep + "tmp", ""})
	want := []string{sep + "home" + sep + "appuser", sep + "tmp"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("outermost() = %v, want %v", got, want)
	}
}

func TestAllocatedSizeCountsDiskBlocks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one-byte"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	// A one-byte file occupies at least one filesystem block; summing lengths
	// would report 1.
	if got := allocatedSize(dir); got < 512 {
		t.Fatalf("allocatedSize() = %d, want at least one disk block", got)
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
