// Package workspacegc coordinates cleanup of generated workspace data.
//
// A workspace is shared by concurrent analyses. Cleanup therefore uses leases
// rather than directory age: an active lease always wins over garbage
// collection, including when the active run owns a worktree below a shared
// repository mirror. Age is used only for data no lease owns (see AddAgedRoot),
// and only once the workspace has no active lease.
package workspacegc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	mu                sync.Mutex
	active            map[string]int
	cleanupRunning    bool
	cleanupDone       chan struct{}
	cancelCleanup     chan struct{}
	workspaceRoot     string
	cacheRoots        []string
	agedRoots         []agedRoot
	usageRoots        []string
	storageLimitBytes int64
	diskUsage         func(string) (float64, error)
}

// agedRoot is a directory whose children are collected by age rather than by
// lease. A non-empty prefix limits collection to children whose name starts
// with it. See AddAgedRoot and AddAgedChildren.
type agedRoot struct {
	path   string
	prefix string
	maxAge time.Duration
}

// sharedCacheDiskUsageThreshold is the fraction of the pod's ephemeral-storage
// budget (storageLimitBytes) at which idle shared caches become eligible for
// removal. It is deliberately measured against the pod budget, not the node
// filesystem: an emptyDir's sizeLimit is enforced by the kubelet polling `du`
// and evicting the pod, not by a quota that syscall.Statfs can see, so a
// Statfs here would report the node's free space and never trip on a large node.
const sharedCacheDiskUsageThreshold = 0.80

// cleanupTimeout bounds a single opportunistic collection triggered on lease
// release, so a detached cleanup goroutine can never leak.
const cleanupTimeout = 2 * time.Minute

// New creates a manager for one persistent workspace pod. cacheRoots must be
// explicit paths; arbitrary home-directory or /tmp deletion is intentionally
// not supported. storageLimitBytes is the pod's ephemeral-storage budget; a
// non-positive value disables shared-cache reclamation (run directories are
// still collected).
func New(workspaceRoot string, cacheRoots []string, storageLimitBytes int64) *Manager {
	m := &Manager{
		active:            make(map[string]int),
		workspaceRoot:     filepath.Clean(workspaceRoot),
		cacheRoots:        append([]string(nil), cacheRoots...),
		storageLimitBytes: storageLimitBytes,
	}
	m.diskUsage = m.measuredUsage
	return m
}

func NewWithDiskUsage(workspaceRoot string, cacheRoots []string, usage func(string) (float64, error)) *Manager {
	m := New(workspaceRoot, cacheRoots, 0)
	if usage != nil {
		m.diskUsage = usage
	}
	return m
}

// measuredUsage reports the footprint of path, plus every directory registered
// with SetUsageRoots, as a fraction of the pod's ephemeral-storage budget. See
// sharedCacheDiskUsageThreshold for why the footprint is measured directly
// rather than read from the filesystem.
//
// It counts what the kubelet counts when it enforces the budget: allocated
// disk blocks, across every volume that shares the budget. Summing file sizes
// under the workspace root alone read well below the real figure, so the
// threshold was reached only after the pod was already close to eviction.
func (m *Manager) measuredUsage(path string) (float64, error) {
	if m.storageLimitBytes <= 0 {
		return 0, nil
	}
	m.mu.Lock()
	roots := outermost(append([]string{path}, m.usageRoots...))
	m.mu.Unlock()
	var used int64
	for _, root := range roots {
		used += allocatedSize(root)
	}
	return float64(used) / float64(m.storageLimitBytes), nil
}

// SetUsageRoots names further directories that count against the same storage
// budget as the workspace root, such as the pod's temp and home volumes. They
// are only measured, never cleaned.
func (m *Manager) SetUsageRoots(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageRoots = append([]string(nil), paths...)
}

// outermost returns the given directories as absolute paths with duplicates and
// any directory nested inside another removed, so a walk of the result visits
// each file once.
func outermost(paths []string) []string {
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != "" {
			cleaned = append(cleaned, absClean(p))
		}
	}
	sort.Strings(cleaned)
	var result []string
	for _, p := range cleaned {
		nested := false
		for _, kept := range result {
			if p == kept || strings.HasPrefix(p, strings.TrimSuffix(kept, string(filepath.Separator))+string(filepath.Separator)) {
				nested = true
				break
			}
		}
		if !nested {
			result = append(result, p)
		}
	}
	return result
}

// CachePaths returns the managed cache locations used by the workspace
// runtime. They are shared across runs for cache warmth, but are safe to purge
// once the workspace has no active leases.
func CachePaths(workspaceRoot string) []string {
	root := filepath.Join(filepath.Clean(workspaceRoot), "shared-cache")
	return []string{
		filepath.Join(root, "go-build"),
		filepath.Join(root, "go-mod"),
		filepath.Join(root, "go-lint"),
		filepath.Join(root, "python"),
		filepath.Join(root, "node"),
		filepath.Join(root, "maven"),
		filepath.Join(root, "gradle"),
	}
}

// AddAgedRoot registers a directory whose children are removed during an idle
// collection once nothing beneath them has been modified for maxAge.
//
// It covers data that outlives any single lease and has no owner to release
// it: a conversation's command folder is reused by later turns, and spilled
// tool output is read back after the command that wrote it has returned. For
// those, "untouched for long enough" is the only end-of-life signal there is.
// The lease rule still wins — an aged root is only swept while the workspace
// has no active lease at all.
//
// Unusable input is refused and reported by returning false: an empty path, a
// non-positive maxAge, a filesystem root, or a path that is the workspace root
// or one of its parents (its children would include the shared caches and
// repository mirrors, which must never be collected by age). Registering a
// path again replaces its maxAge.
func (m *Manager) AddAgedRoot(path string, maxAge time.Duration) bool {
	return m.addAged(path, "", maxAge)
}

// AddAgedChildren is AddAgedRoot limited to the children of path whose name
// starts with namePrefix. It exists for directories the workspace shares with
// other data, such as the temp directory that holds each run's checkout next
// to unrelated files. Because only matching names are candidates, path may be
// a parent of the workspace root, as long as the workspace root itself does
// not sit under a name that matches. An empty namePrefix is refused.
func (m *Manager) AddAgedChildren(path, namePrefix string, maxAge time.Duration) bool {
	if namePrefix == "" {
		return false
	}
	return m.addAged(path, namePrefix, maxAge)
}

func (m *Manager) addAged(path, prefix string, maxAge time.Duration) bool {
	if path == "" || maxAge <= 0 {
		return false
	}
	// Compare absolute paths: a relative path must not slip past the checks
	// below just because the workspace root was given as an absolute one.
	path = absClean(path)
	root := absClean(m.workspaceRoot)
	if filepath.Dir(path) == path || path == root {
		return false
	}
	if below, ok := strings.CutPrefix(root, path+string(filepath.Separator)); ok {
		// path is a parent of the workspace root. Without a prefix every child
		// is a candidate, the workspace included. With one, refuse only if the
		// child that leads to the workspace would itself match.
		first, _, _ := strings.Cut(below, string(filepath.Separator))
		if prefix == "" || strings.HasPrefix(first, prefix) {
			return false
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.agedRoots {
		if m.agedRoots[i].path == path && m.agedRoots[i].prefix == prefix {
			m.agedRoots[i].maxAge = maxAge
			return true
		}
	}
	m.agedRoots = append(m.agedRoots, agedRoot{path: path, prefix: prefix, maxAge: maxAge})
	return true
}

// absClean returns path as a cleaned absolute path, or just cleaned when the
// working directory cannot be determined.
func absClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// Acquire marks a run or command as active. If a collection pass is in
// progress, it signals that pass to stop early — rather than deleting
// whatever it can before a lock release lets Acquire proceed — and waits for
// it to finish, so a new lease still never sees a shared directory that
// collection has half-deleted. The returned function releases the lease and
// opportunistically collects garbage when this was the final user.
func (m *Manager) Acquire(owner string) func() {
	m.mu.Lock()
	for m.cleanupRunning {
		select {
		case <-m.cancelCleanup:
		default:
			close(m.cancelCleanup)
		}
		done := m.cleanupDone
		m.mu.Unlock()
		<-done
		m.mu.Lock()
	}
	m.active[owner]++
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if count := m.active[owner]; count <= 1 {
				delete(m.active, owner)
			} else {
				m.active[owner] = count - 1
			}
			m.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			_, _ = m.CollectIfIdle(ctx)
		})
	}
}

func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, count := range m.active {
		total += count
	}
	return total
}

// CollectIfIdle removes only generated, whitelisted data. It returns the
// number of bytes that were removed. A second active check is performed while
// holding the cleanup state, so a new lease cannot start during deletion.
//
// The deletion loop itself runs without the lock held — RemoveAll on a large
// cache can take a while, and blocking every Acquire behind it would stall
// the synchronous request path. Instead each entry is deleted to completion
// (never interrupted mid-RemoveAll, so a directory is either fully present or
// fully gone) and cancelCleanup is checked before starting the next one. When
// Acquire signals cancellation, or ctx is done, this stops early rather than
// exhaustively deleting everything it planned to: a best-effort trade of "no
// reclamation this round" for "the caller waiting on Acquire doesn't wait for
// it all."
func (m *Manager) CollectIfIdle(ctx context.Context) (int64, error) {
	m.mu.Lock()
	if len(m.active) != 0 || m.cleanupRunning {
		m.mu.Unlock()
		return 0, nil
	}
	m.cleanupRunning = true
	m.cleanupDone = make(chan struct{})
	m.cancelCleanup = make(chan struct{})
	cancelCleanup := m.cancelCleanup
	agedRoots := append([]agedRoot(nil), m.agedRoots...)
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.cleanupRunning = false
		close(m.cleanupDone)
		m.mu.Unlock()
	}()

	isCancelled := func() bool {
		if ctx.Err() != nil {
			return true
		}
		select {
		case <-cancelCleanup:
			return true
		default:
			return false
		}
	}

	var removed int64
	var errs []string
	for _, root := range m.runRoots() {
		if isCancelled() {
			return removed, nil
		}
		bytes, err := removeChildren(root, isCancelled, nil)
		removed += bytes
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	for _, aged := range agedRoots {
		if isCancelled() {
			return removed, nil
		}
		cutoff := time.Now().Add(-aged.maxAge)
		bytes, err := removeChildren(aged.path, isCancelled, func(path string) bool {
			if aged.prefix != "" && !strings.HasPrefix(filepath.Base(path), aged.prefix) {
				return false
			}
			return !modifiedSince(path, cutoff)
		})
		removed += bytes
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if isCancelled() {
		return removed, nil
	}
	// Shared caches remain warm for follow-up analyses. Only clear them when
	// the workspace is idle and its footprint crosses the fraction of the pod's
	// ephemeral-storage budget set by sharedCacheDiskUsageThreshold, avoiding a
	// cold-cache rebuild after every completed run.
	usage, usageErr := m.diskUsage(m.workspaceRoot)
	if usageErr == nil && usage >= sharedCacheDiskUsageThreshold {
		for _, root := range m.sharedRoots() {
			if isCancelled() {
				return removed, nil
			}
			bytes, err := removeChildren(root, isCancelled, nil)
			removed += bytes
			if err != nil {
				errs = append(errs, err.Error())
			}
		}
	} else if usageErr != nil {
		errs = append(errs, fmt.Sprintf("disk usage check: %v", usageErr))
	}
	if len(errs) != 0 {
		return removed, fmt.Errorf("workspace cleanup: %s", strings.Join(errs, "; "))
	}
	return removed, nil
}

func (m *Manager) runRoots() []string {
	return []string{filepath.Join(m.workspaceRoot, "runs")}
}

func (m *Manager) sharedRoots() []string {
	roots := append([]string{}, m.cacheRoots...)
	roots = append(roots, filepath.Join(m.workspaceRoot, "repos"))
	sort.Strings(roots)
	return roots
}

// removeChildren deletes each child of root to completion — never partway —
// checking isCancelled only between children so a caller waiting on Acquire
// never observes one of them half-removed. A non-nil eligible limits deletion
// to the children it accepts; nil removes every child.
func removeChildren(root string, isCancelled func() bool, eligible func(path string) bool) (int64, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var removed int64
	var errs []string
	for _, entry := range entries {
		if isCancelled != nil && isCancelled() {
			break
		}
		path := filepath.Join(root, entry.Name())
		if eligible != nil && !eligible(path) {
			continue
		}
		size := directorySize(path)
		if err := os.RemoveAll(path); err != nil {
			// Go marks module-cache directories (GOMODCACHE) read-only, so the
			// first RemoveAll fails with a permission error. Make the subtree
			// writable and retry before giving up.
			makeWritable(path)
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, fmt.Sprintf("remove %s: %v", path, err))
				continue
			}
		}
		removed += size
	}
	if len(errs) != 0 {
		return removed, fmt.Errorf("remove children %s: %s", root, strings.Join(errs, "; "))
	}
	return removed, nil
}

// makeWritable adds owner write (and, for directories, traverse) permission
// across a subtree so a subsequent RemoveAll can delete read-only entries such
// as Go's module cache. Errors are ignored: this is a best-effort retry helper.
func makeWritable(root string) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(p, 0o700)
		} else {
			_ = os.Chmod(p, 0o600)
		}
		return nil
	})
}

// modifiedSince reports whether path, or anything beneath it, was modified
// after cutoff. A directory's own mtime only moves when an entry is added to or
// removed from it directly, so the whole subtree is checked. Anything that
// cannot be inspected counts as modified: keeping a stale folder for another
// round is cheaper than deleting one that is still in use.
func modifiedSince(path string, cutoff time.Time) bool {
	modified := false
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			modified = true
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil || info.ModTime().After(cutoff) {
			modified = true
			return filepath.SkipAll
		}
		return nil
	})
	return modified
}

// allocatedSize is the disk space path and everything beneath it occupies, in
// bytes: allocated blocks rather than file lengths, directories included,
// which is what `du` and the kubelet report. It falls back to the file length
// where the platform does not expose block counts.
func allocatedSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			total += int64(st.Blocks) * 512
		} else if !d.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func directorySize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d != nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
