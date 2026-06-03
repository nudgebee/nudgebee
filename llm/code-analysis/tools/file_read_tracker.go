package tools

import (
	"sync"
	"time"
)

// FileReadTracker records which files have been read via file_view.
// Shared between file_view (records reads) and replace (gates edits on unread files).
type FileReadTracker struct {
	mu    sync.RWMutex
	reads map[string]time.Time // absolute path → last read timestamp
}

func NewFileReadTracker() *FileReadTracker {
	return &FileReadTracker{
		reads: make(map[string]time.Time),
	}
}

// RecordRead marks a file as having been read.
func (t *FileReadTracker) RecordRead(absPath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads[absPath] = time.Now()
}

// WasRead returns true if the file has been read at least once.
func (t *FileReadTracker) WasRead(absPath string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.reads[absPath]
	return ok
}

// GetReadTime returns the last read time for a file.
func (t *FileReadTracker) GetReadTime(absPath string) (time.Time, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ts, ok := t.reads[absPath]
	return ts, ok
}

// Reset clears all read records.
func (t *FileReadTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads = make(map[string]time.Time)
}
