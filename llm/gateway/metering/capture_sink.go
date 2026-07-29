package metering

import "sync"

// CapturingSink records events in memory. Exported for handler tests in other
// packages that need to assert a metering row was emitted without a real DB.
type CapturingSink struct {
	mu     sync.Mutex
	events []UsageEvent
}

func (s *CapturingSink) Record(ev UsageEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *CapturingSink) Close() error { return nil }

// Events returns a copy of the captured events.
func (s *CapturingSink) Events() []UsageEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UsageEvent, len(s.events))
	copy(out, s.events)
	return out
}
