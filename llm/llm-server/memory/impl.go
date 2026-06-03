package memory

import (
	"sync"
)

// impl is the concrete Memory implementation.
// Unexported; callers obtain it via Default() or New().
type impl struct {
	// Future: ranker, extractor, worker pool handles.
}

var (
	defaultOnce sync.Once
	defaultImpl Memory
)

// Default returns the process-wide Memory instance. Safe to call repeatedly.
func Default() Memory {
	defaultOnce.Do(func() {
		initCaches()
		defaultImpl = &impl{}
	})
	return defaultImpl
}
