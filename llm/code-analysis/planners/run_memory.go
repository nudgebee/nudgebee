package planners

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// RunMemory is the working memory for a single /analyze run, shared across all of
// that run's phases (specialist → fixer → reviewer). It lives only for the lifetime
// of one run and is NOT persisted or reused across runs.
//
// It provides cross-phase read-result caching: identical read-only tool calls
// (file_view, rg, file_find, …) are served from memory instead of being re-executed
// and re-appended. The existing per-planner dedup never spans phases — each phase
// builds a fresh planner — so a re-read in a later phase always re-ran. RunMemory
// closes that gap.
//
// Correctness: any tool that can mutate the working tree calls Invalidate, which
// clears the read cache — so a read issued after an edit misses and re-executes
// against the new state; a stale read is never served.
//
// All methods are safe on a nil receiver and on a zero-value RunMemory (maps are
// lazily initialized), so callers needn't nil-check.
type RunMemory struct {
	mu        sync.Mutex
	readCache map[string]readEntry

	// seenFixDiffs tracks the code diff produced by each fix attempt so the
	// orchestrator can detect a non-converging revert→redo loop — an attempt that
	// reproduces a previously-rejected diff byte-for-byte. Maps diff hash → the
	// attempt number it was first seen at.
	seenFixDiffs map[string]int

	// editedFiles is the authoritative set of repo files the agent changed through
	// its own mutation tools (replace/write_file). Staging the PR off this set —
	// rather than `git add -A` or the LLM's self-report — keeps verify-step side
	// effects (e.g. `poetry add` touching pyproject.toml/poetry.lock, build
	// artifacts, generated lockfiles) out of the commit.
	editedFiles map[string]bool
}

type readEntry struct {
	firstStep int
	obs       string
}

// NewRunMemory returns an empty run memory.
func NewRunMemory() *RunMemory {
	return &RunMemory{
		readCache:    make(map[string]readEntry),
		seenFixDiffs: make(map[string]int),
		editedFiles:  make(map[string]bool),
	}
}

// RecordEditedFile records a repo file the agent changed through a mutation tool.
// nil receiver and empty paths are no-ops.
func (m *RunMemory) RecordEditedFile(path string) {
	if m == nil || path == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.editedFiles == nil {
		m.editedFiles = make(map[string]bool)
	}
	m.editedFiles[path] = true
}

// EditedFiles returns the authoritative set of files the agent edited this run.
// nil receiver returns nil.
func (m *RunMemory) EditedFiles() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.editedFiles))
	for f := range m.editedFiles {
		out = append(out, f)
	}
	return out
}

// RecordFixAttempt records the diff produced by a fix attempt and reports whether
// that exact diff was already produced by an earlier attempt this run. priorAttempt
// is the earlier attempt's number when the diff is a repeat (non-convergence), or 0
// when the diff is new. Empty diffs are ignored (returns 0). nil receiver returns 0.
func (m *RunMemory) RecordFixAttempt(diff string, attempt int) (priorAttempt int) {
	if m == nil || diff == "" {
		return 0
	}
	sum := sha256.Sum256([]byte(diff))
	h := hex.EncodeToString(sum[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seenFixDiffs == nil {
		m.seenFixDiffs = make(map[string]int)
	}
	if prior, seen := m.seenFixDiffs[h]; seen {
		return prior
	}
	m.seenFixDiffs[h] = attempt
	return 0
}

// LookupRead returns the cached observation for a read-only call hash, along with the
// step number it was first seen at. nil receiver is safe (returns a miss).
func (m *RunMemory) LookupRead(hash string) (obs string, firstStep int, ok bool) {
	if m == nil {
		return "", 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, found := m.readCache[hash]
	if !found {
		return "", 0, false
	}
	return e.obs, e.firstStep, true
}

// StoreRead records a read-only call's observation, keeping the first observation
// (and its step number) for a given hash until Invalidate clears the cache. nil
// receiver is a no-op.
func (m *RunMemory) StoreRead(hash string, step int, obs string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readCache == nil {
		m.readCache = make(map[string]readEntry)
	}
	if _, exists := m.readCache[hash]; exists {
		return // keep the first observation
	}
	m.readCache[hash] = readEntry{firstStep: step, obs: obs}
}

// Invalidate clears the read cache after a tool that can mutate the working tree, so
// a subsequent identical read re-executes against the changed state. nil receiver is
// a no-op.
func (m *RunMemory) Invalidate() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.readCache = nil
	m.mu.Unlock()
}
